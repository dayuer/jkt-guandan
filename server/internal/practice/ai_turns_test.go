package practice_test

// #88 第 4 件：AI 回合 + 实时推送的验收。
//
// 这一件的核心口径：
//   - 玩家出完手之后，AI 在**同一个请求/同一个事务**里连走到轮回玩家或这一副打完；
//   - 事件在提交之后才推，`practice.played` 的每一手带 `seq`（一次请求里 AI 连走多手共用 revision）；
//   - 接口 JSON 和事件 JSON 都只能出现「刚打出来的那几张」，别家的手牌一条都不出；
//   - AI 循环失败/超过上限 → 整个请求回滚，玩家这一手也不落库。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/ai"
	"github.com/dayuer/jkt-guandan/server/internal/rules"
)

type recordedEvent struct {
	topic string
	typ   string
	data  any
	raw   string
}

// recordPublisher 记下每一条实时事件；onPublish 用于「提交之后才推」的读库断言。
type recordPublisher struct {
	mu        sync.Mutex
	events    []recordedEvent
	onPublish func(recordedEvent)
}

func (p *recordPublisher) Publish(topic, typ string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	ev := recordedEvent{topic: topic, typ: typ, data: data, raw: string(b)}
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
	if p.onPublish != nil {
		p.onPublish(ev)
	}
}

func (p *recordPublisher) all() []recordedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]recordedEvent{}, p.events...)
}

func (p *recordPublisher) ofType(typ string) []recordedEvent {
	var out []recordedEvent
	for _, ev := range p.all() {
		if ev.typ == typ {
			out = append(out, ev)
		}
	}
	return out
}

// stateGame 从库里读回这一局的存档并重放成引擎局面（测试自己算「我有哪些合法出法」用）。
func (f *fixture) stateGame(t *testing.T, id string) *rules.Game {
	t.Helper()
	var state string
	if err := f.st.Read().QueryRowContext(context.Background(),
		`SELECT state FROM practice_games WHERE id = ?`, id).Scan(&state); err != nil {
		t.Fatalf("读存档：%v", err)
	}
	var g rules.Game
	if err := json.Unmarshal([]byte(state), &g); err != nil {
		t.Fatalf("存档读不回来：%v", err)
	}
	return &g
}

func (f *fixture) revisionOf(t *testing.T, id string) int64 {
	t.Helper()
	var rev int64
	if err := f.st.Read().QueryRowContext(context.Background(),
		`SELECT revision FROM practice_games WHERE id = ?`, id).Scan(&rev); err != nil {
		t.Fatalf("读 revision：%v", err)
	}
	return rev
}

func toInts(v any) []int {
	arr, _ := v.([]any)
	out := make([]int, len(arr))
	for i, x := range arr {
		f, _ := x.(float64)
		out[i] = int(f)
	}
	return out
}

func keysOfCards(cards []rules.Card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Key()
	}
	return out
}

// 建局落库成功之后推 `practice.started`。
func TestPracticeCreatePublishesStarted(t *testing.T) {
	pub := &recordPublisher{}
	f := newServerWithPublisher(t, pub)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	if created.status != http.StatusOK {
		t.Fatalf("建局：%d %s", created.status, created.raw)
	}

	evs := pub.ofType("practice.started")
	if len(evs) != 1 {
		t.Fatalf("该正好一条 practice.started，实际 %d 条", len(evs))
	}
	if !strings.HasPrefix(evs[0].topic, "user:") {
		t.Fatalf("事件该推到 user:{id}，实际 %q", evs[0].topic)
	}
	var d struct {
		Revision int64 `json:"revision"`
		Seat     int   `json:"seat"`
		Level    int   `json:"level"`
		Counts   []int `json:"counts"`
		Turn     int   `json:"turn"`
	}
	if err := json.Unmarshal([]byte(evs[0].raw), &d); err != nil {
		t.Fatalf("事件 data 不是合法 JSON：%v", err)
	}
	if d.Revision != 1 || d.Seat != int(created.field("seat").(float64)) ||
		d.Level != int(created.field("level").(float64)) || d.Turn != d.Seat {
		t.Fatalf("started 的 data 和建局响应对不上：%s", evs[0].raw)
	}
	for i, n := range toInts(created.field("counts")) {
		if d.Counts[i] != n {
			t.Fatalf("started 的 counts 和响应不一致：%v vs %v", d.Counts, toInts(created.field("counts")))
		}
	}
}

// 一整个请求里：我的手 + AI 连走，直到轮回我或者这一副打完。
func TestPracticeAIPlaysUntilMyTurn(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))

	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil) // 首圈是我领出
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}
	r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), nil)
	if r.status != http.StatusOK {
		t.Fatalf("出一手：%d %s", r.status, r.raw)
	}
	if got := int(r.field("turn").(float64)); got != seat && !r.field("deal_over").(bool) {
		t.Fatalf("AI 走完该轮回我（或这一副打完），turn=%d，我坐 %d：%s", got, seat, r.raw)
	}
	// revision 每个客户端请求只 +1 一次（AI 的手不各自 +1）。
	if got := r.field("revision").(float64); got != 2 {
		t.Fatalf("revision 该是 2，实际 %v", got)
	}
}

// 一整副能自动打完：每次轮到我挑一手合法出法（要不起就过），直到 deal_over。
func TestPracticeAIPlaysFullDeal(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))

	view := created
	for step := 0; !view.field("deal_over").(bool); step++ {
		if step > 200 {
			t.Fatalf("打不完（%d 步）—— AI 或引擎卡住了", step)
		}
		if got := int(view.field("turn").(float64)); got != seat {
			t.Fatalf("轮到客户端提交时 turn 该是我：%d，我坐 %d：%s", got, seat, view.raw)
		}
		rev := int64(view.field("revision").(float64))

		g := f.stateGame(t, id)
		var lead *rules.Combo
		if !g.Leading() {
			_, combo, ok := g.LastPlay()
			if !ok {
				t.Fatalf("不是领出却读不到上一手：%s", view.raw)
			}
			lead = &combo
		}
		cands, err := rules.Moves(g.Hand(seat), g.Level(), lead)
		if err != nil {
			t.Fatalf("算合法出法：%v", err)
		}
		body := map[string]any{"pass": true, "expected_revision": rev}
		if len(cands) > 0 {
			body = playBody(cands[0], rev)
		}
		r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", body, nil)
		if r.status != http.StatusOK {
			t.Fatalf("第 %d 步：%d %s", step, r.status, r.raw)
		}
		view = r
	}

	if !view.field("deal_over").(bool) {
		t.Fatal("最后该是 deal_over")
	}
	// 掼蛋是**一方两家都出完**就结束（不是四家全出完）：结束时应有一对搭档是 0 张。
	counts := toInts(view.field("counts"))
	zero := func(seat int) bool { return counts[seat-1] == 0 }
	if !((zero(1) && zero(3)) || (zero(2) && zero(4))) {
		t.Fatalf("这一副结束时应有一方的两家都出完：%v", counts)
	}
}

// 事件重放：把 practice.played 按 seq 重放到初始局面，得到和 GET 一样的最终局面。
func TestPracticePlayEventsReplayToView(t *testing.T) {
	pub := &recordPublisher{}
	f := newServerWithPublisher(t, pub)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))
	initial := f.stateGame(t, id)

	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}
	final := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), nil)
	if final.status != http.StatusOK {
		t.Fatalf("出一手：%d %s", final.status, final.raw)
	}

	evs := pub.ofType("practice.played")
	if len(evs) == 0 {
		t.Fatal("一条 practice.played 都没有")
	}
	var revision int64
	for i, ev := range evs {
		var d struct {
			Revision int64    `json:"revision"`
			Seq      int      `json:"seq"`
			Seat     int      `json:"seat"`
			Pass     bool     `json:"pass"`
			Cards    []string `json:"cards"`
			Kind     string   `json:"kind"`
			Rank     string   `json:"rank"`
		}
		if err := json.Unmarshal([]byte(ev.raw), &d); err != nil {
			t.Fatalf("事件 data 不是合法 JSON：%v", err)
		}
		if d.Seq != i+1 {
			t.Fatalf("第 %d 条事件的 seq 该是 %d，实际 %d", i, i+1, d.Seq)
		}
		if i == 0 {
			revision = d.Revision
			if d.Seat != seat {
				t.Fatalf("第一条事件该是我出的，实际 %d 号座", d.Seat)
			}
		} else if d.Revision != revision {
			t.Fatalf("同一次请求里的事件该共用一个 revision：%d vs %d", d.Revision, revision)
		}
		if d.Pass {
			if err := initial.Pass(d.Seat); err != nil {
				t.Fatalf("重放 %d 号座过牌：%v", d.Seat, err)
			}
			continue
		}
		cards := make([]rules.Card, len(d.Cards))
		for j, k := range d.Cards {
			c, err := rules.ParseKey(k)
			if err != nil {
				t.Fatalf("事件里的牌 %q：%v", k, err)
			}
			cards[j] = c
		}
		typ, err := rules.ParseComboType(d.Kind)
		if err != nil {
			t.Fatalf("事件里的牌型 %q：%v", d.Kind, err)
		}
		rk, err := rules.ParseRank(d.Rank)
		if err != nil {
			t.Fatalf("事件里的点数 %q：%v", d.Rank, err)
		}
		if err := initial.Play(d.Seat, cards, typ, rk); err != nil {
			t.Fatalf("重放 %d 号座的 %v：%v", d.Seat, d.Cards, err)
		}
	}
	if initial.Turn() != int(final.field("turn").(float64)) {
		t.Fatalf("事件重放出来的轮次该和接口一致：%d vs %v", initial.Turn(), final.field("turn"))
	}
	if initial.Over() != final.field("deal_over").(bool) {
		t.Fatalf("事件重放出来的 deal_over 该和接口一致")
	}
	gotCounts := toInts(final.field("counts"))
	for s := 1; s <= rules.Seats; s++ {
		if len(initial.Hand(s)) != gotCounts[s-1] {
			t.Fatalf("%d 号座：重放 %d 张，接口 %d 张", s, len(initial.Hand(s)), gotCounts[s-1])
		}
	}
}

// 事件必须在**提交之后**推：Fake Publisher 在 Publish 里当场读库，要能读到这一手。
func TestPracticePlayPublishesAfterCommit(t *testing.T) {
	pub := &recordPublisher{}
	f := newServerWithPublisher(t, pub)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))

	var checked int
	pub.onPublish = func(ev recordedEvent) {
		if ev.typ != "practice.played" {
			return
		}
		var d struct {
			Revision int64 `json:"revision"`
			Seq      int   `json:"seq"`
		}
		if err := json.Unmarshal([]byte(ev.raw), &d); err != nil {
			t.Errorf("事件 data 不是合法 JSON：%v", err)
			return
		}
		if got := f.revisionOf(t, id); got != d.Revision {
			t.Errorf("推事件时库里 revision 是 %d，事件说 %d —— 还没提交就推了？", got, d.Revision)
		}
		if g := f.stateGame(t, id); len(g.Moves()) < d.Seq {
			t.Errorf("推事件时库里只有 %d 手，事件 seq=%d —— 还没提交就推了？", len(g.Moves()), d.Seq)
		}
		checked++
	}

	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}
	if r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), nil); r.status != http.StatusOK {
		t.Fatalf("出一手：%d %s", r.status, r.raw)
	}
	if checked == 0 {
		t.Fatal("一条 practice.played 都没触发 after-commit 检查")
	}
}

// 接口 JSON 和事件 JSON 都只能出现自己的手牌 / 刚打出来的牌 —— 别家的手牌一条都不能有。
func TestPracticePlayHidesOtherHandsEverywhere(t *testing.T) {
	pub := &recordPublisher{}
	f := newServerWithPublisher(t, pub)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))

	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}
	r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), nil)
	if r.status != http.StatusOK {
		t.Fatalf("出一手：%d %s", r.status, r.raw)
	}

	// 接口 JSON：手牌必须**正好是引擎里我那一手** —— 不能拿「接口自己报的 hand」当基准，
	// 那样双方一起膨胀也会「自洽」。
	gAfter := f.stateGame(t, id)
	wantHand := len(gAfter.Hand(seat))
	if got := len(toStrings(r.field("hand"))); got != wantHand {
		t.Fatalf("接口给了 %d 张手牌，引擎里我只有 %d 张 —— 别家的牌漏了：%s", got, wantHand, r.raw)
	}
	if got := len(cardKey.FindAllString(r.raw, -1)); got != wantHand {
		t.Fatalf("接口 JSON 里有 %d 个牌面 key，引擎里我只有 %d 张 —— 别家的牌漏了：%s", got, wantHand, r.raw)
	}

	// 事件 JSON：每条 played 的 cards 必须等于引擎动作序列里的那一手（seq-1），不能夹带别家的牌。
	moves := gAfter.Moves()
	for _, ev := range pub.ofType("practice.played") {
		var d struct {
			Seq   int      `json:"seq"`
			Pass  bool     `json:"pass"`
			Cards []string `json:"cards"`
		}
		if err := json.Unmarshal([]byte(ev.raw), &d); err != nil {
			t.Fatalf("事件 data 不是合法 JSON：%v", err)
		}
		if d.Seq < 1 || d.Seq > len(moves) {
			t.Fatalf("事件的 seq=%d 超出动作序列（%d 手）：%s", d.Seq, len(moves), ev.raw)
		}
		m := moves[d.Seq-1]
		if m.Pass != d.Pass {
			t.Fatalf("事件 seq=%d 的 pass 和引擎动作不一致", d.Seq)
		}
		want := keysOfCards(m.Combo.Cards)
		if !equalStringSets(d.Cards, want) {
			t.Fatalf("事件 seq=%d 的 cards 和引擎动作不一致：%v vs %v", d.Seq, d.Cards, want)
		}
		if got := len(cardKey.FindAllString(ev.raw, -1)); got != len(want) {
			t.Fatalf("事件里有 %d 个牌面 key，这一手只打了 %d 张 —— 别家的牌漏了：%s", got, len(want), ev.raw)
		}
	}
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
		if m[s] < 0 {
			return false
		}
	}
	return true
}

// AI 循环失败 → 整个请求回滚：我这一手也不落库，幂等键也不落。
func TestPracticePlayRollsBackWhenAIFails(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))
	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}

	f.practice.SetDecider(func(ai.View, ai.Config) (ai.Decision, error) {
		return ai.Decision{}, errors.New("测试：AI 炸了")
	})
	key := map[string]string{"Idempotency-Key": "AI失败-1"}
	r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), key)
	if r.status != http.StatusServiceUnavailable || r.errCode() != "unavailable" {
		t.Fatalf("AI 失败该 503 unavailable，实际 %d %s", r.status, r.raw)
	}
	if rev := f.revisionOf(t, id); rev != 1 {
		t.Fatalf("回滚之后 revision 该还是 1，实际 %d", rev)
	}
	if g := f.stateGame(t, id); len(g.Moves()) != 0 {
		t.Fatalf("回滚之后存档里不该有动作，实际 %d 手", len(g.Moves()))
	}
	var n int
	if err := f.st.Read().QueryRowContext(context.Background(),
		`SELECT count(*) FROM idempotency_keys WHERE key = ?`, "AI失败-1").Scan(&n); err != nil {
		t.Fatalf("查幂等键：%v", err)
	}
	if n != 0 {
		t.Fatalf("被回滚的请求不该留下幂等键，实际 %d 条", n)
	}

	// 换回真的 AI，同一个键重发 → 正常出牌（说明上面那次整笔都没发生）。
	f.practice.SetDecider(ai.Decide)
	if r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), key); r.status != http.StatusOK {
		t.Fatalf("重试该成功：%d %s", r.status, r.raw)
	}
}

// 超过 AI 循环上限 → 整个请求回滚（上限用 stub 调到 1，真的走到那条分支）。
func TestPracticePlayStopsAtAILoopCap(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))
	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}

	f.practice.SetMaxAILoop(1)
	r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), nil)
	if r.status != http.StatusServiceUnavailable {
		t.Fatalf("超过 AI 上限该 503，实际 %d %s", r.status, r.raw)
	}
	if rev := f.revisionOf(t, id); rev != 1 {
		t.Fatalf("回滚之后 revision 该还是 1，实际 %d", rev)
	}
	if g := f.stateGame(t, id); len(g.Moves()) != 0 {
		t.Fatalf("回滚之后存档里不该有动作，实际 %d 手", len(g.Moves()))
	}
}

// 幂等重放不重发事件（重发不是新动作）。
func TestPracticePlayReplayDoesNotRepublish(t *testing.T) {
	pub := &recordPublisher{}
	f := newServerWithPublisher(t, pub)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	seat := int(created.field("seat").(float64))
	g := f.stateGame(t, id)
	cands, err := rules.Moves(g.Hand(seat), g.Level(), nil)
	if err != nil || len(cands) == 0 {
		t.Fatalf("算我领出的合法出法：%v", err)
	}

	key := map[string]string{"Idempotency-Key": "重放-1"}
	first := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), key)
	if first.status != http.StatusOK {
		t.Fatalf("第一次：%d %s", first.status, first.raw)
	}
	n := len(pub.ofType("practice.played"))
	if n == 0 {
		t.Fatal("第一次至少该有一条 practice.played")
	}
	replay := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(cands[0], 1), key)
	if replay.status != http.StatusOK || replay.raw != first.raw {
		t.Fatalf("重放该返回同一个局面：%d %s / %s", replay.status, replay.raw, first.raw)
	}
	if got := len(pub.ofType("practice.played")); got != n {
		t.Fatalf("重放不该再推事件：之前 %d 条，现在 %d 条", n, got)
	}
}
