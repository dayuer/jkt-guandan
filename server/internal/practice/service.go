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
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/ai"
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
	// ErrDealOver：这一副已经打完了（下一副是第 5 件的事，这里不偷偷开新副）。
	ErrDealOver = errors.New("这一副已经打完了")
	// ErrPracticeEnded：这一局练习局已经被新局收掉了（建新局时写了 ended_at）。
	// 收掉的局只能取（复盘），不能再改 —— 复盘和第 6/7 件都依赖「收掉的局不再变」。
	ErrPracticeEnded = errors.New("这局练习已经结束了")
)

// 实时事件（#88 第 4 件，§5.5）。REST 始终是事实来源，事件只是「告诉你哪里变了」；
// 断线重连后一律重新 GET 当前局面。
const (
	EventStarted = "practice.started"
	EventPlayed  = "practice.played"
	EventEnded   = "practice.ended"
)

// UserTopic 是某个用户的订阅主题（和 identity 的事件同一个主题，只有这个人的设备订阅得到）。
func UserTopic(userID string) string { return "user:" + userID }

// Publisher 是实时事件的出口。业务包不依赖 realtime，main 里用一个几行的适配器接到 Hub。
type Publisher interface {
	Publish(topic, eventType string, data any)
}

// NopPublisher 什么也不做（测试 / 没接实时时用）。
type NopPublisher struct{}

func (NopPublisher) Publish(string, string, any) {}

// Decider 是 AI 走一手的入口。默认是 ai.Decide；测试可以换成替身，用来造「AI 出错」「走到上限」
// 这类正常 AI 不会出现的局面。
type Decider func(v ai.View, cfg ai.Config) (ai.Decision, error)

// defaultMaxAILoop 是一次请求里 AI 最多走几手。
//
// 正常一副牌不可能接近它（出牌 ≤ 108 次，两次出牌之间最多 3 次过牌）；它只是兜底：
// 万一引擎/策略哪天破坏了「每次动作都有进度」这个不变量，不让一个请求在写事务里转死。
const defaultMaxAILoop = 512

// practiceEvent 是一条待发的事件；提交之后按这个顺序推出去。
type practiceEvent struct {
	typ  string
	data any
}

// startedEvent 是 practice.started 的 data。
type startedEvent struct {
	Revision int64            `json:"revision"`
	Seat     int              `json:"seat"`
	Level    int              `json:"level"`
	Counts   [rules.Seats]int `json:"counts"`
	Turn     int              `json:"turn"`
}

// playedEvent 是 practice.played 的 data。
//
// `Seq` 是这一手在存档动作序列里的 1-based 下标 —— 一次请求里 AI 可能连走好几手，它们共用
// 同一个 revision，客户端靠 seq 排序/去重。`Cards` 只有**刚打出来的那几张**（公开信息），
// **别家的手牌一条都不出**。
type playedEvent struct {
	Revision int64            `json:"revision"`
	Seq      int              `json:"seq"`
	Seat     int              `json:"seat"`
	Pass     bool             `json:"pass"`
	Cards    []string         `json:"cards,omitempty"`
	Kind     string           `json:"kind,omitempty"`
	Rank     string           `json:"rank,omitempty"`
	Counts   [rules.Seats]int `json:"counts"`
	Turn     int              `json:"turn"`
	DealOver bool             `json:"deal_over"`
}

// endedEvent 是 practice.ended 的 data。
type endedEvent struct {
	Revision int64            `json:"revision"`
	Seq      int              `json:"seq"`
	Counts   [rules.Seats]int `json:"counts"`
	Turn     int              `json:"turn"`
}

// VersionConflict：客户端手里的 revision 旧了（或者有人并发改了）。**带上最新局面**，
// 客户端拿它直接刷新就行（照 games 的 VersionConflict 那套）。
type VersionConflict struct {
	Latest View
}

func (e *VersionConflict) Error() string {
	return "练习局已经被改过，请按最新的局面重来"
}

// MoveRejected：这一手在当前局面下不成立。`Reason` 是**引擎的封闭原因码**（rules 的 `RejectReason`，
// 七个取值：not-your-turn / leading-cannot-pass / no-cards / cards-not-in-hand / bad-combo /
// does-not-beat / game-over）—— 直接当接口的 `error.code` 发出去。
//
// 不另编一套文案码：客户端拿到的取值和它本地 `app/rules` 算出来的是**同一套**
// （两端共用牌例之外的又一处白捡的一致），提示文案可以共用。
type MoveRejected struct {
	Reason string
}

func (e *MoveRejected) Error() string { return "这一手不成立：" + e.Reason }

// Move 是玩家提交的一手：`Pass` 为真就是过牌，否则是打出 `Cards` 这几张、当 `Type`（`Rank`）那一种解读。
type Move struct {
	Pass  bool
	Cards []rules.Card
	Type  rules.ComboType
	Rank  rules.Rank
}

// moveKey 把一手收成一个稳定的字符串，给幂等指纹用（同一个键 + 同一手 = 同一次提交）。
func moveKey(m Move) string {
	if m.Pass {
		return "pass"
	}
	keys := make([]string, len(m.Cards))
	for i, c := range m.Cards {
		keys[i] = c.Key()
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s/%v/%v", strings.Join(keys, ","), m.Type, m.Rank)
}

// Play 出牌（或过牌）：校验 → 推进 → 写回存档。
//
//   - `expectedRev` 对不上（或者并发改过）→ `*VersionConflict`（带最新局面）；
//   - 这一局已经被新局收掉（`ended_at` 写了）→ `ErrPracticeEnded`，**局面一点不动**；
//   - 这一手不成立 → `*MoveRejected`（带引擎的原因码），**局面一点不动**；
//   - 同一个 `Idempotency-Key` 重发 → 不重复出牌，返回现在的局面（可能比第一次新 —— 幂等说的是
//     「这一手不落两次」，不是「响应冻住」）；
//   - 玩家这一手落下之后，**在同一个事务里替 AI 连走到轮回玩家或这一副打完**（#88 第 4 件）；
//     AI 那几手不各自加 revision —— `revision` 还是每个客户端请求 +1 一次。
func (s *Service) Play(ctx context.Context, id, userID string, mv Move, expectedRev int64, idemKey string) (View, error) {
	const route = "POST /api/practice/{id}/play"
	var out View
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		row, err := loadTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if row.UserID != userID {
			return ErrNotFound
		}
		// 幂等：同一个键 + 同一手重发 → 不重复出牌，直接把现在的局面回给他。
		fp := idemFingerprint("id", id, "revision", expectedRev, "move", moveKey(mv))
		replay, err := idemReplay(ctx, tx, userID, idemKey, route, fp)
		if err != nil {
			return err
		}
		if replay {
			return s.viewNow(ctx, tx, row, &out)
		}
		// 收掉的局不再变（建新局时写了 ended_at）：只能取来复盘，不能出牌。
		// 放在幂等重放之后 —— 已经成功过的那一手重发仍然要返回 200。
		if !row.EndedAt.IsZero() {
			return ErrPracticeEnded
		}
		if row.Revision != expectedRev {
			return s.conflict(ctx, tx, id, &out)
		}
		var g rules.Game
		if err := json.Unmarshal(row.State, &g); err != nil {
			return fmt.Errorf("练习局 %s 的存档读不回来：%w", id, err)
		}
		if g.Over() {
			return ErrDealOver
		}
		if mv.Pass {
			err = g.Pass(row.Seat)
		} else {
			err = g.Play(row.Seat, mv.Cards, mv.Type, mv.Rank)
		}
		if err != nil {
			// 引擎的拒绝原因是封闭取值，原样发出去（不在这里改成别的码）。
			var ruleErr *rules.RuleError
			if errors.As(err, &ruleErr) {
				return &MoveRejected{Reason: string(ruleErr.Reason)}
			}
			return err
		}

		// 玩家这一手落下了；AI 的回合在**同一个事务**里走完（#88 第 4 件）。
		// revision 每个客户端请求只 +1 一次，AI 那几手不是客户端动作。
		newRev := row.Revision + 1
		events := make([]practiceEvent, 0, 8)
		record := func() {
			moves := g.Moves()
			m := moves[len(moves)-1]
			ev := playedEvent{
				Revision: newRev,
				Seq:      len(moves),
				Seat:     m.Seat,
				Pass:     m.Pass,
				Counts:   countsOf(&g),
				Turn:     g.Turn(),
				DealOver: g.Over(),
			}
			if !m.Pass {
				ev.Cards = keysOf(m.Combo.Cards)
				ev.Kind = m.Combo.Type.String()
				ev.Rank = m.Combo.Rank.RankName()
			}
			events = append(events, practiceEvent{typ: EventPlayed, data: ev})
		}
		record()

		// 只要没打完、而且轮到 AI 座位，就一直走 —— 走到轮回玩家或者这一副打完。
		//
		// 进度不变量：每一手要么出牌（四家总牌数严格减少），要么过牌（一圈的过牌有上限，
		// 过完必须由最后出牌的人领出）。所以正常情况下这个循环远小于 maxAILoop；
		// 超过上限说明引擎/策略违反了不变量，整个事务回滚，不留半截局面。
		cfg := ai.DefaultConfig()
		for i := 0; i < s.maxAILoop && !g.Over() && g.Turn() != row.Seat; i++ {
			seat := g.Turn()
			d, decideErr := s.decide(ai.ViewOf(&g, seat), cfg)
			if decideErr != nil {
				return fmt.Errorf("练习局 AI 决策失败：%w", decideErr)
			}
			if d.Pass {
				err = g.Pass(seat)
			} else {
				err = g.Play(seat, d.Combo.Cards, d.Combo.Type, d.Combo.Rank)
			}
			if err != nil {
				return fmt.Errorf("练习局 AI 出牌被拒（%d 号座）：%w", seat, err)
			}
			record()
		}
		if !g.Over() && g.Turn() != row.Seat {
			return fmt.Errorf("练习局 AI 连续走了 %d 手还没轮回玩家（引擎或策略违反进度）", s.maxAILoop)
		}
		if g.Over() {
			moves := g.Moves()
			events = append(events, practiceEvent{typ: EventEnded, data: endedEvent{
				Revision: newRev,
				Seq:      len(moves),
				Counts:   countsOf(&g),
				Turn:     g.Turn(),
			}})
		}

		state, err := json.Marshal(&g)
		if err != nil {
			return fmt.Errorf("练习局存档序列化失败：%w", err)
		}
		// 乐观锁：读到的 revision 就是我写回去的前提。0 行 = 有人在我们中间动过。
		res, err := tx.ExecContext(ctx,
			`UPDATE practice_games SET state = ?, revision = revision + 1 WHERE id = ? AND revision = ?`,
			string(state), id, row.Revision)
		if err != nil {
			return fmt.Errorf("写练习局：%w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return s.conflict(ctx, tx, id, &out)
		}
		if err := idemSave(ctx, tx, userID, idemKey, route, fp, map[string]any{"id": id}, s.clk.Now()); err != nil {
			return err
		}
		// 提交之后才推（回滚就不推）；顺序就是落子的顺序。
		tx.AfterCommit(func(context.Context) {
			topic := UserTopic(row.UserID)
			for _, ev := range events {
				s.pub.Publish(topic, ev.typ, ev.data)
			}
		})
		out = viewOf(&g, id, newRev, row.Seat, false)
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return out, nil
}

// viewNow 把**库里现在这一行**读出来收成 View（幂等重放时用：要的是「现在的局面」）。
func (s *Service) viewNow(ctx context.Context, tx *store.Tx, row practiceRow, out *View) error {
	var g rules.Game
	if err := json.Unmarshal(row.State, &g); err != nil {
		return fmt.Errorf("练习局 %s 的存档读不回来：%w", row.ID, err)
	}
	*out = viewOf(&g, row.ID, row.Revision, row.Seat, !row.EndedAt.IsZero())
	return nil
}

// conflict 读最新的行、构成 409 的载荷（局面 + revision 都是最新的）。
func (s *Service) conflict(ctx context.Context, tx *store.Tx, id string, out *View) error {
	latest, err := loadTx(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := s.viewNow(ctx, tx, latest, out); err != nil {
		return err
	}
	return &VersionConflict{Latest: *out}
}

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

	pub       Publisher
	decide    Decider
	maxAILoop int
}

// NewService 建练习局服务。clk 为 nil 时用系统时钟；pub 为 nil 时不发实时事件。
func NewService(st *store.Store, clk clock.Clock, pub Publisher) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	if pub == nil {
		pub = NopPublisher{}
	}
	return &Service{
		st: st, clk: clk, opener: ratelimit.New(openPerMinute, openPerMinute, clk),
		pub: pub, decide: ai.Decide, maxAILoop: defaultMaxAILoop,
	}
}

// SetDecider 换掉 AI 的决策函数（测试用；nil 忽略）。
func (s *Service) SetDecider(d Decider) {
	if d != nil {
		s.decide = d
	}
}

// SetMaxAILoop 调整一次请求里 AI 最多走几手（测试用；<=0 忽略）。
func (s *Service) SetMaxAILoop(n int) {
	if n > 0 {
		s.maxAILoop = n
	}
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
	// DealOver：**这一副**打完了（四家出完）。和 `Ended` 不是一回事 ——
	// `Ended` 是「这一局练习局收掉了」（#88 第 1 件的收局），而一副打完只是这一副完了，
	// 下一副还在后头（第 5 件：进贡 / 跨副）。客户端靠这个决定是弹结算还是继续出牌，
	// 而不是自己去猜 `turn == 0`（0 是「没人该出牌」的内部约定，不该当成接口契约）。
	DealOver bool `json:"deal_over"`
	Ended    bool `json:"ended"`
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
		// 落库成功之后才推（回滚就不推）。
		tx.AfterCommit(func(context.Context) {
			s.pub.Publish(UserTopic(userID), EventStarted, startedEvent{
				Revision: 1,
				Seat:     seat,
				Level:    int(g.Level()),
				Counts:   countsOf(g),
				Turn:     g.Turn(),
			})
		})
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

// queryer 是「能查一行」的最小接口：读池（`*sql.DB`）和写事务（`*store.Tx`）都满足它。
// 读一行的逻辑只写一份（`load` / `loadTx` 只是入口不同）。
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func load(ctx context.Context, db *sql.DB, id string) (practiceRow, error) {
	return loadTx(ctx, db, id)
}

// loadTx 读一行的当前状态（读池和写事务都能用 —— 写事务里读是为了拿自己那份快照）。
func loadTx(ctx context.Context, q queryer, id string) (practiceRow, error) {
	var (
		r       practiceRow
		endedAt sql.NullInt64
	)
	err := q.QueryRowContext(ctx,
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

// ── 幂等（照 games/score.go 那三件套抄过来）──
//
// 三个包各有一份（games 一份、这里一份）：它们读写的是同一张 `idempotency_keys`，口径必须一样 ——
// 「同一个键用在别的路由/别的指纹上 → `ErrIdemKeyReused`」这条是**跨接口**成立的，
// 所以指纹里要带上接口自己的参数（这里：局的 id + expected_revision + 这一手）。

// errIdemKeyReused 和 games 那边同一个取值：422 idempotency_key_reused。
var errIdemKeyReused = errors.New("idempotency_key_reused")

// IdemKeyReused 给 handler 判断用（不导出那个内部的哨兵）。
func IsIdemKeyReused(err error) bool { return errors.Is(err, errIdemKeyReused) }

// idemReplay 判断这是不是同一个键的重发：命中（同路由 + 同指纹）返回 true；
// 键被用在别处（别的路由或别的指纹）返回 errIdemKeyReused；键为空返回 false（调用方没打算幂等）。
func idemReplay(ctx context.Context, tx *store.Tx, userID, key, route, fingerprint string) (bool, error) {
	if key == "" {
		return false, nil
	}
	// 和 games 的 idemReplay 同一口径：库里的指纹可能为空（旧版本写的），这时只比路由。
	var (
		gotRoute string
		gotFP    sql.NullString
	)
	err := tx.QueryRowContext(ctx,
		`SELECT route, fingerprint FROM idempotency_keys WHERE user_id = ? AND key = ?`, userID, key).
		Scan(&gotRoute, &gotFP)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("查幂等键：%w", err)
	case gotRoute != route, gotFP.Valid && gotFP.String != fingerprint:
		return false, errIdemKeyReused
	}
	return true, nil
}

// idemFingerprint 把这次请求的参数按 `名=值` 的行拼成 sha256（用**解析后**的参数，
// JSON 字段序/空白不影响）。参数是成对的 `名, 值, 名, 值…`，和 games 那边那份写法一致
// （两边算出来的串不一样没关系：指纹只在自己这边做等值比较，跨接口靠 route 区分）。
func idemFingerprint(kv ...any) string {
	h := sha256.New()
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(h, "%v=%v\n", kv[i], kv[i+1])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// idemSave 把这个键落库（摘要即可：重放时读的是**现在的局面**，不靠这里存的东西）。
func idemSave(ctx context.Context, tx *store.Tx, userID, key, route, fingerprint string, summary any, now time.Time) error {
	if key == "" {
		return nil
	}
	body, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("幂等摘要序列化：%w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO idempotency_keys (key, user_id, route, fingerprint, status, response, created_at)
		VALUES (?, ?, ?, ?, 200, ?, ?)`, key, userID, route, fingerprint, string(body), now.Unix()); err != nil {
		return fmt.Errorf("存幂等键：%w", err)
	}
	return nil
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
		Counts:   countsOf(g),
		Turn:     g.Turn(),
		DealOver: g.Over(),
		Ended:    ended,
	}
	return v
}

// countsOf 是四家各剩几张（下标 0 是 1 号座）。张数是公开信息，接口和事件都只给这个。
func countsOf(g *rules.Game) [rules.Seats]int {
	var c [rules.Seats]int
	for s := 1; s <= rules.Seats; s++ {
		c[s-1] = len(g.Hand(s))
	}
	return c
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
