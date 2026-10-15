// Package games 管牌局和报名（docs/server-design.md §5.1）。
//
// 每一个操作都是**一个写事务**：读牌局、判断、改数据、写提醒、登记提交后回调，
// 全部在 store.Write 里完成，所以并发报名天然串行，应用层不需要任何锁（§11.2）。
// 事务里不做任何网络 I/O（§11.1）。
package games

import (
	"errors"
	"time"
)

// 牌局状态（§4）。
const (
	StatusOpen      = "open"
	StatusFull      = "full"
	StatusCancelled = "cancelled"
	StatusDone      = "done"
)

// Capacity 是一局的座位数；§4 的 CHECK 约束也是这个值。
const Capacity = 4

// JoinGrace 是开局之后还能补人的时长。
//
// 缺一个人的局到点了，正是最需要临时补人的时候，所以开局后一段时间内仍然可以报名；
// 超过这个窗口就返回 409 game_started，首页列表也不再显示这一类局。
// 退出、移出、取消不受这条限制，直到牌局变成 done。
const JoinGrace = 2 * time.Hour

// Kind 的取值（§4）。
var Kinds = []string{"classic", "no-level", "teaching"}

// Areas 是允许的片区。§12.2 要求片区只接受固定列表里的值。
var Areas = []string{
	"SCBD", "PIK", "Central Park", "Kemang",
	"Kuningan", "Senayan", "Kelapa Gading", "BSD", "Other",
}

// 配额（§10.8）。
const (
	// MaxConcurrentHolds 是一个人同时能占的座位或候补位数。
	MaxConcurrentHolds = 5
	// MaxGamesPerDay 是一个人每天能开的局数。
	MaxGamesPerDay = 10
	// MaxInvitesPerCall、MaxInvitesPerDay 限制局头邀请牌友（#115）：一次最多邀请几个人，
	// 一个人 24 小时内最多发出多少条邀请（每条都会推送给对方，免得被用来骚扰）。
	MaxInvitesPerCall = 20
	MaxInvitesPerDay  = 50
)

// Jakarta 是算「今天」用的时区（§13.2：在服务端按 Asia/Jakarta 计算）。
var jakarta = mustLoadJakarta()

func mustLoadJakarta() *time.Location {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		// 时区数据由 time/tzdata 编进二进制（§13.2），正常不会失败。
		panic("加载 Asia/Jakarta: " + err.Error())
	}
	return loc
}

// Game 是一局牌。
type Game struct {
	ID        string
	ShortCode string
	HostID    string
	StartsAt  time.Time
	Area      string
	Venue     string
	Kind      string
	Note      string
	Status    string
	Version   int64
	CreatedAt time.Time
	// RescheduledAt 是局头最近一次改时间或地点的时刻；零值表示没改过（#114）。
	RescheduledAt time.Time
}

// Open 表示这局还能报名。
func (g Game) Open() bool { return g.Status == StatusOpen || g.Status == StatusFull }

// Seat 是座位上的人。
type Seat struct {
	SeatNo    int
	UserID    string
	Nickname  string
	CreatedAt time.Time
	// WaitlistedAt 是候补补位上来的人原来排候补的时间；直接报名坐下的是零值（#160）。
	WaitlistedAt time.Time
}

// JoinedAt 是这个人加入这一局的时间：补位上来的从排候补算，其余就是坐下的时间（#160）。
func (s Seat) JoinedAt() time.Time {
	if !s.WaitlistedAt.IsZero() {
		return s.WaitlistedAt
	}
	return s.CreatedAt
}

// PromotedAt 是候补补位的时间，也就是坐下的时间；不是补位上来的是零值（#160）。
func (s Seat) PromotedAt() time.Time {
	if s.WaitlistedAt.IsZero() {
		return time.Time{}
	}
	return s.CreatedAt
}

// WaitEntry 是候补里的人。
type WaitEntry struct {
	UserID    string
	Nickname  string
	CreatedAt time.Time
	Position  int // 从 1 开始
}

// Detail 是 03 屏需要的全部数据。
type Detail struct {
	Game     Game
	HostName string
	Seats    []Seat
	Waitlist []WaitEntry
	// Missing 是还差几个人成局。
	Missing int
	// Me 是当前用户在这一局里的位置；「seat」「waitlist」「none」。
	Me string
	// MySeatNo / MyPosition 只在 Me 不是 none 时有意义。
	MySeatNo   int
	MyPosition int
	// MyInviter 是邀请当前用户来这一局的人（#115）；没被邀请为 nil。只给被邀请的人自己看。
	MyInviter *Seat
	// MyJoinedAt 是当前用户加入这一局的时间（座位或候补的；补位上来的从排候补算）；不在局里是零值。
	// MyPromotedAt 是当前用户候补补位的时间；不是补位上来的是零值。换了设备、没有本机快照时，
	// 客户端拿它们判断改时间横幅和「轮到你了」要不要出（#160）。
	MyJoinedAt   time.Time
	MyPromotedAt time.Time
}

// Summary 是 02 屏列表里的条目。
//
// 带上座位是因为首页卡片要画座位圆点（显示称呼首字），只给个数字画不出来。
// 座位里只有 id 和称呼，符合 §10.4 的白名单。
type Summary struct {
	Game     Game
	HostName string
	Seats    []Seat
	Missing  int
	// MyRole / WaitlistPos 是当前身份在这一局的位置：seated / waitlist / none（#114）。
	// 空串表示没有按身份算过（首页事件是广播，不带这两项）。
	MyRole      string
	WaitlistPos int
	// MyJoinedAt / MyPromotedAt 同 Detail（#160）；MyRole 为空或 none 时是零值。
	MyJoinedAt   time.Time
	MyPromotedAt time.Time
}

// 业务错误。接口层把它们映射成 §6 的状态码。
var (
	ErrNotFound         = errors.New("牌局不存在")
	ErrClosed           = errors.New("牌局已经结束或取消")
	ErrBanned           = errors.New("你被移出过这一局")
	ErrNotHost          = errors.New("只有局头能做这件事")
	ErrVersionConflict  = errors.New("牌局已经被改过")
	ErrQuota            = errors.New("超出配额")
	ErrRateLimited      = errors.New("操作太频繁")
	ErrInvalidInput     = errors.New("输入不合法")
	ErrHostCantKickSelf = errors.New("局头不能移出自己")
	ErrNotInGame        = errors.New("这个人不在这一局里")
	ErrStarted          = errors.New("这一局已经开始了")
)

// VersionConflict 带上最新的牌局，让前端能提示「局已被修改，请确认」（§5.1）。
type VersionConflict struct{ Latest Detail }

func (e *VersionConflict) Error() string { return ErrVersionConflict.Error() }
func (e *VersionConflict) Unwrap() error { return ErrVersionConflict }
