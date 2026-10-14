package games_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// recorder 把发布出去的事件记下来，替代 #40 的 Hub。
type recorder struct {
	mu     sync.Mutex
	events []games.Event
}

func (r *recorder) Publish(ev games.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recorder) all() []games.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]games.Event, len(r.events))
	copy(out, r.events)
	return out
}

func (r *recorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}

// find 找第一条符合条件的事件。
func (r *recorder) find(eventType, topic string) (games.Event, bool) {
	for _, ev := range r.all() {
		if ev.Type == eventType && (topic == "" || ev.Topic == topic) {
			return ev, true
		}
	}
	return games.Event{}, false
}

func newEventFixture(t *testing.T) (*fixture, *recorder) {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	fake := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	rec := &recorder{}
	f := &fixture{
		t:     t,
		st:    st,
		clk:   fake,
		fake:  fake,
		ident: identity.NewService(st, fake, true),
		games: games.NewService(st, fake, rec),
	}
	return f, rec
}

// 报名、满员、退出、修改各自发出对应的实时事件，主题是 game:<短码>，
// 并且带上新的版本号（§5.5）。
func TestRealtimeEventsArePublishedAfterCommit(t *testing.T) {
	f, rec := newEventFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)

	// 开局会通知首页列表。
	if ev, ok := rec.find(games.EventFeedAdded, games.FeedTopic("SCBD")); !ok {
		t.Fatalf("开局应当发布 feed.game_added，实际 %v", rec.all())
	} else if ev.V == 0 {
		t.Fatalf("事件应当带版本号，实际 %+v", ev)
	}

	rec.reset()

	// 第 2、3 个人报名：seat.taken。
	for i := 0; i < 2; i++ {
		if _, err := f.games.Join(context.Background(), code, f.newUser("牌友")); err != nil {
			t.Fatalf("报名: %v", err)
		}
	}
	ev, ok := rec.find(games.EventSeatTaken, games.GameTopic(code))
	if !ok {
		t.Fatalf("报名应当发布 seat.taken，实际 %v", rec.all())
	}
	if ev.V == 0 {
		t.Fatalf("seat.taken 应当带版本号，实际 %+v", ev)
	}
	// 事件里带着座位快照，客户端不用再拉一次 REST。
	data, _ := ev.Data.(map[string]any)
	if data == nil || data["seats"] == nil || data["missing"] == nil {
		t.Fatalf("seat.taken 应当带座位快照，实际 %+v", ev.Data)
	}

	rec.reset()

	// 第 4 个人：满员，事件变成 game.full。
	if _, err := f.games.Join(context.Background(), code, f.newUser("第四位")); err != nil {
		t.Fatalf("报名: %v", err)
	}
	if _, ok := rec.find(games.EventGameFull, games.GameTopic(code)); !ok {
		t.Fatalf("坐满时应当发布 game.full，实际 %v", rec.all())
	}

	rec.reset()

	// 有人退出：seat.left。
	var someone string
	if err := f.st.Read().QueryRow(
		`SELECT s.user_id FROM seats s JOIN games g ON g.id = s.game_id
		  WHERE g.short_code = ? AND s.user_id <> ? LIMIT 1`, code, host).Scan(&someone); err != nil {
		t.Fatalf("取座位: %v", err)
	}
	if _, err := f.games.Leave(context.Background(), code, someone); err != nil {
		t.Fatalf("退出: %v", err)
	}
	if _, ok := rec.find(games.EventSeatLeft, games.GameTopic(code)); !ok {
		t.Fatalf("退出应当发布 seat.left，实际 %v", rec.all())
	}

	rec.reset()

	// 局头改时间：game.updated。
	detail, err := f.games.Get(context.Background(), code, host)
	if err != nil {
		t.Fatalf("读牌局: %v", err)
	}
	newStart := f.clk.Now().Add(5 * time.Hour)
	if _, err := f.games.Update(context.Background(), code, host,
		games.Patch{StartsAt: &newStart}, detail.Game.Version); err != nil {
		t.Fatalf("改时间: %v", err)
	}
	if _, ok := rec.find(games.EventGameUpdated, games.GameTopic(code)); !ok {
		t.Fatalf("修改应当发布 game.updated，实际 %v", rec.all())
	}
}

// 事务回滚时一条都不该发出去（§5.5：客户端不能看到「幽灵座位」）。
func TestNoEventsWhenTransactionRollsBack(t *testing.T) {
	f, rec := newEventFixture(t)

	host := f.newUser("局头")
	code := f.newGame(host)

	// 找一个人，先把他移出，这样他再报名会被 game_bans 拦下。
	target := f.newUser("捣乱的")
	if _, err := f.games.Join(context.Background(), code, target); err != nil {
		t.Fatalf("报名: %v", err)
	}
	if _, err := f.games.Kick(context.Background(), code, host, target); err != nil {
		t.Fatalf("移出: %v", err)
	}
	rec.reset()

	// 被移出的人再报名：业务上被拒，事务回滚，不该有任何事件。
	if _, err := f.games.Join(context.Background(), code, target); err == nil {
		t.Fatal("被移出的人再报名应当失败")
	}
	if got := rec.all(); len(got) != 0 {
		t.Fatalf("回滚的事务不该发布任何事件，实际 %v", got)
	}
}

// #46：feed 事件带列表条目（和 GET /api/games 的一项相同，加上 version），首页据此只更新那一张卡片。
func TestFeedEventsCarryListItem(t *testing.T) {
	f, rec := newEventFixture(t)
	host := f.newUser("局头")
	code := f.newGame(host)

	added, ok := rec.find(games.EventFeedAdded, games.FeedTopic("SCBD"))
	if !ok {
		t.Fatalf("没有 feed.game_added：%v", rec.all())
	}
	item, _ := added.Data.(map[string]any)
	if item["short_code"] != code || item["area"] != "SCBD" || item["venue"] != "Pacific Place" ||
		item["missing"] != 3 || item["seats"] != 1 || item["version"] != int64(1) || item["host"] == nil {
		t.Fatalf("feed.game_added 的数据：%+v", item)
	}

	rec.reset()
	if _, err := f.games.Join(context.Background(), code, f.newUser("牌友")); err != nil {
		t.Fatal(err)
	}
	changed, ok := rec.find(games.EventFeedChanged, games.FeedTopic("SCBD"))
	if !ok {
		t.Fatalf("报名应通知首页：%v", rec.all())
	}
	item, _ = changed.Data.(map[string]any)
	if item["short_code"] != code || item["missing"] != 2 || item["version"] != changed.V {
		t.Fatalf("feed.game_changed 的数据：%+v（V=%d）", item, changed.V)
	}
}

// #46：局头把局改到别的片区，原片区的首页收到一条 area 已变的 feed.game_changed（据此移除卡片），
// 新片区收到一条（据此加上）；不再给新片区重复发。
func TestAreaChangeNotifiesOldAndNewFeed(t *testing.T) {
	f, rec := newEventFixture(t)
	host := f.newUser("局头")
	code := f.newGame(host)
	d, err := f.games.Get(context.Background(), code, host)
	if err != nil {
		t.Fatal(err)
	}
	rec.reset()
	pik := "PIK"
	if _, err := f.games.Update(context.Background(), code, host, games.Patch{Area: &pik}, d.Game.Version); err != nil {
		t.Fatal(err)
	}
	count := func(topic string) (int, map[string]any) {
		n := 0
		var last map[string]any
		for _, ev := range rec.all() {
			if ev.Topic == topic && ev.Type == games.EventFeedChanged {
				n++
				last, _ = ev.Data.(map[string]any)
			}
		}
		return n, last
	}
	if n, item := count(games.FeedTopic("SCBD")); n != 1 || item["area"] != "PIK" {
		t.Fatalf("原片区应收到 1 条、area=PIK：%d %+v", n, item)
	}
	if n, _ := count(games.FeedTopic("PIK")); n != 1 {
		t.Fatalf("新片区应只收到 1 条，实际 %d", n)
	}
}
