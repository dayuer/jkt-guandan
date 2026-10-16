// Package practice 是「练习局」：一个人和三个 AI 打的一局牌（#88 第 1 件）。
//
// 它和「约局」（games）不是一回事：练习局没有座位、候补、报名、邀请、分享，也没有短码 ——
// 它是**单人**的、只属于一个人的。所以它自己一张表（practice_games）、自己一组接口，
// **不塞进 games**（塞进去就得把 seats / waitlist / feed / invites 那一整套拖进来）。
//
// 这个包只做**存档**：建局、取局（断线回来 / 刷新）。出牌裁决、AI 回合、进贡、结算分别是
// 第 3–5 件的事。它是 server/internal/rules 的**第一个生产消费方**（在那之前只有测试用它）。
//
// 「别家的手牌」是这个包的红线：接口只回**自己的手牌 + 各家的张数**（`View` 的形状就是为此定的），
// 和 AI 那层（server/internal/ai 的 View）是同一条约定。
package practice

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/ratelimit"
	"github.com/dayuer/jkt-guandan/server/internal/rules"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

var (
	// ErrNotFound：这局不存在，**或者不是你的**。两种情况返回同一个错误 —— 见 Get 的注释。
	ErrNotFound = errors.New("练习局不存在")
	// ErrRateLimited：建局太频繁。
	ErrRateLimited = errors.New("操作太频繁")
)

// firstLevel 是练习局从打几开始：掼蛋从 2 打起（第十三条 一）。
const firstLevel = rules.Two

// openPerMinute 是建局限流：每人每分钟 30 次。
// 建一局要写一行、还要把上一局收掉，不设限表会被刷爆；正常玩一分钟建不了 30 局。
const openPerMinute = 30

// Service 是练习局的入口。
type Service struct {
	st     *store.Store
	clk    clock.Clock
	opener *ratelimit.Limiter
}

// NewService 建练习局服务。clk 为 nil 时用系统时钟。
func NewService(st *store.Store, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	return &Service{st: st, clk: clk, opener: ratelimit.New(openPerMinute, openPerMinute, clk)}
}

// View 是给客户端看的局面：**只有自己的手牌 + 各家的张数**。
//
// 牌用**牌面 key**（`♠K/0`）表示，和客户端的 app/rules（`ParseKey`）是同一套写法 ——
// 两端共用牌例之外的又一处白捡的一致，不另定义一套编码。
type View struct {
	ID       string           `json:"id"`
	Revision int64            `json:"revision"`
	Level    int              `json:"level"`
	Seat     int              `json:"seat"`
	Hand     []string         `json:"hand"`
	Counts   [rules.Seats]int `json:"counts"` // 四家各剩几张，下标 0 是 1 号座
	Turn     int              `json:"turn"`
	Ended    bool             `json:"ended"`
}

// Create 建一局：发牌、摆好座位、落库，返回局面。
//
// **一人同时只有一局**：建新局会把上一局收掉（#88 设计第二节）。练习局没有对手、没有跨人竞争，
// 这一条一加，并发问题基本就没了。收掉的局**还能取**（复盘要用，第 6 件），只是 `ended` 为真。
func (s *Service) Create(ctx context.Context, userID string) (View, error) {
	if !s.opener.Allow(userID) {
		return View{}, ErrRateLimited
	}
	seat, err := randomSeat()
	if err != nil {
		return View{}, err
	}
	g, err := newGame(seat)
	if err != nil {
		return View{}, err
	}
	state, err := json.Marshal(g)
	if err != nil {
		return View{}, fmt.Errorf("练习局存档序列化失败：%w", err)
	}

	now := s.clk.Now()
	id := newID(now)
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 先收掉上一局。收掉之后 ended_at 不是 NULL，「一人只有一局」那条部分唯一索引就放行了。
		if _, err := tx.ExecContext(ctx,
			`UPDATE practice_games SET ended_at = ? WHERE user_id = ? AND ended_at IS NULL`,
			now.Unix(), userID); err != nil {
			return fmt.Errorf("收掉上一局练习局：%w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO practice_games (id, user_id, level, seat, state, revision, created_at)
			 VALUES (?, ?, ?, ?, ?, 1, ?)`,
			id, userID, int(g.Level()), seat, string(state), now.Unix()); err != nil {
			return fmt.Errorf("建练习局：%w", err)
		}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return viewOf(g, id, 1, seat, false), nil
}

// Get 取一局：断线回来、刷新、复盘都走它。
//
// 局面永远从存档**重放**出来（rules.Game 的 UnmarshalJSON 会把动作重放并对账），
// 不存「当前局面」快照 —— 两份数据迟早会打架，重放永远自洽，坏存档读的一刻就报错。
//
// 不是自己的局**也返回 ErrNotFound**（不是 403）：id 是不透明的随机串，
// 确认「这个 id 存在、但不是你的」对调用方没有好处，只会泄露别人开没开练习局。
func (s *Service) Get(ctx context.Context, id, userID string) (View, error) {
	row, err := load(ctx, s.st.Read(), id)
	if err != nil {
		return View{}, err
	}
	if row.UserID != userID {
		return View{}, ErrNotFound
	}
	var g rules.Game
	if err := json.Unmarshal(row.State, &g); err != nil {
		return View{}, fmt.Errorf("练习局 %s 的存档读不回来：%w", id, err)
	}
	return viewOf(&g, row.ID, row.Revision, row.Seat, !row.EndedAt.IsZero()), nil
}

// practiceRow 是 practice_games 的一行（只取这个包用得着的列）。
type practiceRow struct {
	ID       string
	UserID   string
	Seat     int
	State    []byte
	Revision int64
	EndedAt  time.Time // 零值 = 还开着
}

func load(ctx context.Context, db *sql.DB, id string) (practiceRow, error) {
	var (
		r       practiceRow
		endedAt sql.NullInt64
	)
	err := db.QueryRowContext(ctx,
		`SELECT id, user_id, seat, state, revision, ended_at FROM practice_games WHERE id = ?`, id).
		Scan(&r.ID, &r.UserID, &r.Seat, &r.State, &r.Revision, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return practiceRow{}, ErrNotFound
	}
	if err != nil {
		return practiceRow{}, fmt.Errorf("读练习局：%w", err)
	}
	if endedAt.Valid {
		r.EndedAt = time.Unix(endedAt.Int64, 0)
	}
	return r, nil
}

// viewOf 把一局局面收成给客户端看的形状。
//
// **只取自己的手牌 + 各家的张数**：别家的牌一律不出这个函数（`g.Hand(s)` 谁的手牌都给得出来，
// 所以这里是那条红线唯一需要守住的地方）。张数只取长度，不取内容。
func viewOf(g *rules.Game, id string, revision int64, seat int, ended bool) View {
	v := View{
		ID:       id,
		Revision: revision,
		Level:    int(g.Level()),
		Seat:     seat,
		Hand:     keysOf(g.Hand(seat)),
		Turn:     g.Turn(),
		Ended:    ended,
	}
	for s := 1; s <= rules.Seats; s++ {
		v.Counts[s-1] = len(g.Hand(s))
	}
	return v
}

func keysOf(cards []rules.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Key()
	}
	return out
}

// newGame 发一副新牌：从 2 打起，我坐 seat，**首圈也是我领出**。
//
// 座位随机（1–4）：它只影响我上家下家是谁、我的搭档是谁，接口形状不受影响；
// 但每局的座位不一样，客户端和后面的 AI 回合才会碰到各种相对位置（而不是永远「我坐 1 号座」）。
func newGame(seat int) (*rules.Game, error) {
	perm, err := randomPerm(len(rules.NewDeck()))
	if err != nil {
		return nil, err
	}
	dealt, err := rules.Deal(rules.PermShuffler(perm))
	if err != nil {
		return nil, fmt.Errorf("发牌：%w", err)
	}
	// Deal 的下标是「从**首抓者**起逆时针第几家」，不是座位号；首抓者就是我，所以旋一下座。
	var hands [rules.Seats][]rules.Card
	for k := 0; k < rules.Seats; k++ {
		hands[(seat-1+k)%rules.Seats] = dealt[k]
	}
	return rules.NewGame(firstLevel, hands, seat)
}

// randomSeat 随机挑一个座位（1–4）。
func randomSeat() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(rules.Seats)))
	if err != nil {
		return 0, fmt.Errorf("挑座位：%w", err)
	}
	return int(n.Int64()) + 1, nil
}

// crockford 是 Crockford base32 字母表（去掉了 I、L、O、U）—— 和 identity / games 里那份同一套，
// 26 个字符表示 16 个字节。
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newID 生成 26 个字符的 ULID 形状标识：前 6 个字节是毫秒时间戳、后面 10 个字节是随机数。
// 所以它按时间单调递增，适合做数据库主键（和 games / identity 里那个是同一套做法，
// 三个包各有一份 —— 这里是这个包唯一要用的那种 id）。
func newID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		// crypto/rand 读失败时进程已经不可信，这里没有可恢复的余地。
		panic(fmt.Sprintf("生成练习局标识失败: %v", err))
	}
	return crockford.EncodeToString(b[:])
}

// randomPerm 排一个 0..n-1 的随机顺序，给发牌用。
//
// 用 crypto/rand：练习局也是牌局，发牌必须**不可预测**（math/rand 的种子能被问出来）。
func randomPerm(n int) ([]int, error) {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	for i := n - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, fmt.Errorf("洗牌：%w", err)
		}
		k := int(j.Int64())
		out[i], out[k] = out[k], out[i]
	}
	return out, nil
}
