package practice_test

// 出牌接口的验收（#88 第 3 件）：合法推进、非法返回**引擎的原因码**且局面不动、
// `expected_revision` 对不上 409、`Idempotency-Key` 重发不落两次。
//
// 这一件**不替 AI 走棋**：我出完手之后 turn 落在 AI 座位上（见 TestPracticePlayLeavesTurnToAI）。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/rules"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// handCards 把响应里的手牌（`♠K/0` 这种 key）还原成引擎的牌。
func handCards(t *testing.T, r resp) []rules.Card {
	t.Helper()
	out := make([]rules.Card, 0, 27)
	for _, k := range toStrings(r.field("hand")) {
		c, err := rules.ParseKey(k)
		if err != nil {
			t.Fatalf("手牌 key %q：%v", k, err)
		}
		out = append(out, c)
	}
	return out
}

// leadingMove 挑一个「领出时一定合法」的走法：手里最小的一张单张。
// 期望值来自引擎的 `Moves`（这正是「客户端和 AI 共用同一套出法」的意思），不是手写死的。
func leadingMove(t *testing.T, hand []rules.Card, level rules.Rank) rules.Combo {
	t.Helper()
	cs, err := rules.Moves(hand, level, nil)
	if err != nil {
		t.Fatalf("算合法出法：%v", err)
	}
	if len(cs) == 0 {
		t.Fatal("领出时一个出法都没有")
	}
	return cs[0]
}

func playBody(mv rules.Combo, revision int64) map[string]any {
	keys := make([]string, len(mv.Cards))
	for i, c := range mv.Cards {
		keys[i] = c.Key()
	}
	return map[string]any{
		"cards": keys, "kind": mv.Type.String(), "rank": mv.Rank.RankName(),
		"expected_revision": revision,
	}
}

func TestPracticePlayAdvances(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	level := rules.Rank(created.field("level").(float64))
	seat := int(created.field("seat").(float64))
	before := handCards(t, created)

	mv := leadingMove(t, before, level)
	played := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), nil)
	if played.status != http.StatusOK {
		t.Fatalf("合法出牌该过，实际 %d %s", played.status, played.raw)
	}
	if got := played.field("revision").(float64); got != 2 {
		t.Fatalf("出完一手 revision 该是 2，实际 %v", got)
	}
	if n := len(toStrings(played.field("hand"))); n != 27-len(mv.Cards) {
		t.Fatalf("出完 %d 张，手里该剩 %d 张，实际 %d", len(mv.Cards), 27-len(mv.Cards), n)
	}
	// 局面和库里的一致（GET 回来逐字相同）。
	again := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if again.raw != played.raw {
		t.Fatalf("出牌响应和 GET 不一致：\n%s\n%s", played.raw, again.raw)
	}
	// 这一件不替 AI 走棋：turn 落在别人身上，但**局面已经落了库**。
	if got := int(played.field("turn").(float64)); got == seat {
		t.Fatalf("出完一手不该还轮到我（这一件不替 AI 走棋），turn=%d，我坐 %d", got, seat)
	}
}

func TestPracticePlayRejectsAndKeepsState(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	level := rules.Rank(created.field("level").(float64))
	hand := handCards(t, created)

	// ① 领出时过牌 → leading-cannot-pass（第八条 一、1）
	// ② 声明成对子但只给一张 → bad-combo
	one := hand[0]
	for _, tc := range []struct {
		name string
		body map[string]any
		want string
	}{
		{"领出时过牌", map[string]any{"pass": true, "expected_revision": 1}, "leading-cannot-pass"},
		{"一张牌说成对子", map[string]any{
			"cards": []string{one.Key()}, "kind": "pair", "rank": one.Rank.RankName(),
			"expected_revision": 1}, "bad-combo"},
	} {
		got := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", tc.body, nil)
		if got.status != http.StatusConflict || got.errCode() != tc.want {
			t.Fatalf("%s：该 409 %s，实际 %d %s", tc.name, tc.want, got.status, got.raw)
		}
		// 被拒之后局面**一点没动**（和建局时逐字相同）。
		now := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
		if now.raw != created.raw {
			t.Fatalf("%s：被拒之后局面不该变：\n%s\n%s", tc.name, created.raw, now.raw)
		}
	}

	// ③ 出一手合法的，然后再出 → not-your-turn（这一件不替 AI 走棋，所以还轮不到我）
	mv := leadingMove(t, hand, level)
	if r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), nil); r.status != http.StatusOK {
		t.Fatalf("先出一手合法的：%d %s", r.status, r.raw)
	}
	second := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play",
		map[string]any{"pass": true, "expected_revision": 2}, nil)
	if second.status != http.StatusConflict || second.errCode() != "not-your-turn" {
		t.Fatalf("没轮到我时该 409 not-your-turn，实际 %d %s", second.status, second.raw)
	}
}

func TestPracticePlayIsIdempotent(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	level := rules.Rank(created.field("level").(float64))
	mv := leadingMove(t, handCards(t, created), level)

	key := map[string]string{"Idempotency-Key": "弱网重发-1"}
	first := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), key)
	second := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), key)
	if first.status != http.StatusOK || second.status != http.StatusOK {
		t.Fatalf("重发该都成功：%d %s / %d %s", first.status, first.raw, second.status, second.raw)
	}
	if first.raw != second.raw {
		t.Fatalf("重发该返回同一个局面：\n%s\n%s", first.raw, second.raw)
	}
	// revision 只 +1 一次、手牌只少一次（**没有出两次牌**）。
	if got := second.field("revision").(float64); got != 2 {
		t.Fatalf("重发之后 revision 该还是 2（没出第二次），实际 %v", got)
	}
	if n := len(toStrings(second.field("hand"))); n != 27-len(mv.Cards) {
		t.Fatalf("重发不该再少牌，实际手里 %d 张", n)
	}
	// 同一个键用在**别的**请求上 → 422（跨接口的幂等键复用）。
	other := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play",
		map[string]any{"pass": true, "expected_revision": 2}, key)
	if other.status != http.StatusUnprocessableEntity || other.errCode() != "idempotency_key_reused" {
		t.Fatalf("同一个键用在别处该 422 idempotency_key_reused，实际 %d %s", other.status, other.raw)
	}
}

func TestPracticePlayRejectsStaleRevision(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	level := rules.Rank(created.field("level").(float64))
	mv := leadingMove(t, handCards(t, created), level)

	if r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), nil); r.status != http.StatusOK {
		t.Fatalf("先出一手：%d %s", r.status, r.raw)
	}
	// 拿着旧 revision 再来一次 → 409 version_conflict + **最新局面**（客户端直接刷新）。
	stale := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play",
		map[string]any{"pass": true, "expected_revision": 1}, nil)
	if stale.status != http.StatusConflict || stale.errCode() != "version_conflict" {
		t.Fatalf("旧 revision 该 409 version_conflict，实际 %d %s", stale.status, stale.raw)
	}
	latest, _ := stale.body["practice"].(map[string]any)
	if latest == nil || latest["revision"].(float64) != 2 {
		t.Fatalf("409 里该带最新局面（revision 2）：%s", stale.raw)
	}

	// 少了 expected_revision → 400（和 games 的 If-Match 一样，宁可不做）。
	none := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", map[string]any{"pass": true}, nil)
	if none.status != http.StatusBadRequest {
		t.Fatalf("缺 expected_revision 该 400，实际 %d %s", none.status, none.raw)
	}
}

func TestPracticePlayOthersAndAnonymous(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "开的人")
	other := f.newBrowser()
	f.signUp(other, "别人")
	anon := f.newBrowser()

	id, _ := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil).field("id").(string)
	body := map[string]any{"pass": true, "expected_revision": 1}

	if got := f.do(other, http.MethodPost, "/api/practice/"+id+"/play", body, nil); got.status != http.StatusNotFound {
		t.Fatalf("别人的局该 404（和 GET 一致），实际 %d %s", got.status, got.raw)
	}
	if got := f.do(anon, http.MethodPost, "/api/practice/"+id+"/play", body, nil); got.status != http.StatusUnauthorized {
		t.Fatalf("没身份该 401，实际 %d %s", got.status, got.raw)
	}
}

// 出牌之后存档里记的是「初始局面 + 动作序列」：把这一局取回来，重放一遍得到同一个局面。
// （这一条同时是第 6 件「复盘」的地基：动作序列就是记录。）
func TestPracticeStateRoundTripsMoves(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	level := rules.Rank(created.field("level").(float64))
	mv := leadingMove(t, handCards(t, created), level)
	if r := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), nil); r.status != http.StatusOK {
		t.Fatalf("出一手：%d %s", r.status, r.raw)
	}

	// 直接从库里把存档拿出来，两件事都要成立：能反序列化（会重放并对账），
	// 而且重放之后的轮次/张数和接口看到的一致。
	var state string
	if err := f.st.Read().QueryRowContext(context.Background(),
		`SELECT state FROM practice_games WHERE id = ?`, id).Scan(&state); err != nil {
		t.Fatalf("读存档：%v", err)
	}
	// 存档里是「初始局面 + 一手动作」的形状：动作序列非空。
	var wire struct {
		Moves []json.RawMessage `json:"moves"`
	}
	if err := json.Unmarshal([]byte(state), &wire); err != nil {
		t.Fatalf("存档不是合法 JSON：%v", err)
	}
	if len(wire.Moves) != 1 {
		t.Fatalf("存档里该正好记了 1 手，实际 %d", len(wire.Moves))
	}
	var g rules.Game
	if err := json.Unmarshal([]byte(state), &g); err != nil {
		t.Fatalf("存档读不回来（对账失败？）：%v", err)
	}
	view := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if got := int(view.field("turn").(float64)); got != g.Turn() {
		t.Fatalf("重放出来的轮次该和接口一致：%d vs %d", g.Turn(), got)
	}
	if counts := toStrings(view.field("counts")); len(counts) != 4 {
		t.Fatalf("四家张数该有 4 个：%v", counts)
	}
	// 手里的张数 = 重放之后我那一手（出过一手，少几张）。
	if n := len(toStrings(view.field("hand"))); n == 0 || n >= 27 {
		t.Fatalf("出过一手之后手里该少于 27 张，实际 %d", n)
	}
}

// 「这一副已经打完了还出牌」→ 409 `deal_over`。
//
// 要造一个**真打完**的存档：用引擎自己把一副「每家一张」的牌打到底（四家都出完就结束），
// 把它写回那一行。直接编一个 `turn:0` 的假 JSON 不行 —— `UnmarshalJSON` 会把动作重放一遍并对账
// （轮到谁、谁出完了都要对得上），自相矛盾的存档根本读不回来（那样报的会是 500，不是 409）。
func TestPracticePlayRejectsFinishedDeal(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)

	// 一副「每家一张」的牌：3、4、5、6，首抓者 1 号座。一路打到底。
	var hands [rules.Seats][]rules.Card
	for s := 1; s <= rules.Seats; s++ {
		c, err := rules.ParseKey(fmt.Sprintf("♠%d/0", s+2))
		if err != nil {
			t.Fatal(err)
		}
		hands[s-1] = []rules.Card{c}
	}
	g, err := rules.NewGame(rules.Two, hands, 1)
	if err != nil {
		t.Fatalf("摆一副小牌：%v", err)
	}
	for i := 0; !g.Over(); i++ {
		if i > 32 {
			t.Fatal("这副小牌打不完")
		}
		seat := g.Turn()
		c := g.Hand(seat)[0]
		if err := g.Play(seat, []rules.Card{c}, rules.TypeSingle, c.Rank); err != nil {
			if err := g.Pass(seat); err != nil {
				t.Fatalf("打到底：%v", err)
			}
		}
	}
	if !g.Over() {
		t.Fatal("这副该打完了")
	}

	// 把打完的存档塞回去，然后试着再出一手。
	state, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE practice_games SET state = ? WHERE id = ?`, string(state), id)
		return err
	}); err != nil {
		t.Fatalf("写回存档：%v", err)
	}

	got := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play",
		map[string]any{"pass": true, "expected_revision": 1}, nil)
	if got.status != http.StatusConflict || got.errCode() != "deal_over" {
		t.Fatalf("这一副打完了还出牌该 409 deal_over，实际 %d %s", got.status, got.raw)
	}
	// 打完的那一副取回来还是能取（复盘要用），而且**明说这一副完了** ——
	// 客户端靠 `deal_over` 决定弹结算还是继续出牌，不用自己去猜 `turn == 0`。
	view := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if view.status != http.StatusOK {
		t.Fatalf("打完的局该还能取（复盘），实际 %d", view.status)
	}
	if !view.field("deal_over").(bool) {
		t.Fatalf("这一副打完了，`deal_over` 该为真：%s", view.raw)
	}
	if view.field("ended").(bool) {
		t.Fatalf("`ended` 是「这一局练习局收掉了」，打完一副不该置位：%s", view.raw)
	}
	// 建局时那一副没打完。
	if created.field("deal_over").(bool) {
		t.Fatal("刚建好的局不该是 deal_over")
	}
}

// 已经收掉的练习局不能再出牌（#88 第 3 件复审 B1）：建新局会把旧局的 ended_at 写上，
// 旧局只能取来复盘，不能改。检查放在幂等重放之后（重发同一手仍然是 200）。
func TestPracticePlayRejectsEndedGame(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	first := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := first.field("id").(string)
	level := rules.Rank(first.field("level").(float64))
	mv := leadingMove(t, handCards(t, first), level)

	// 建第二局 → 第一局被收掉。
	if r := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil); r.status != http.StatusOK {
		t.Fatalf("建第二局：%d %s", r.status, r.raw)
	}
	ended := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if !ended.field("ended").(bool) {
		t.Fatalf("第一局该被收掉：%s", ended.raw)
	}

	// 拿第一局的合法手牌和 revision 再出 → 409 practice_ended，局面不动。
	got := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", playBody(mv, 1), nil)
	if got.status != http.StatusConflict || got.errCode() != "practice_ended" {
		t.Fatalf("已结束的局该 409 practice_ended，实际 %d %s", got.status, got.raw)
	}
	now := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if now.raw != ended.raw {
		t.Fatalf("被拒之后局面不该变：\n%s\n%s", ended.raw, now.raw)
	}
}

// 同一张牌提交两次是**调用方的错**：必须 400，不能落进 default 变成 503 + ERROR 日志
// （#88 第 3 件复审 B2）。引擎的「手里有没有」按 ID 查 map、不数次数，所以在 HTTP 层就挡。
func TestPracticePlayRejectsDuplicateCards(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	c := handCards(t, created)[0]

	got := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", map[string]any{
		"cards": []string{c.Key(), c.Key()}, "kind": "pair", "rank": c.Rank.RankName(),
		"expected_revision": 1,
	}, nil)
	if got.status != http.StatusBadRequest || got.errCode() != "bad_request" {
		t.Fatalf("重复的牌该 400 bad_request，实际 %d %s", got.status, got.raw)
	}
	now := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if now.raw != created.raw {
		t.Fatalf("被拒之后局面不该变：\n%s\n%s", created.raw, now.raw)
	}
}

// 交一张**不在手里**的牌 → 409 cards-not-in-hand，局面不动（#88 第 3 件复审 N1）。
// 这是这个接口最要紧的防作弊口径：引擎的在手检查要是被放宽，这条必须红。
func TestPracticePlayRejectsCardsNotInHand(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "打牌的人")
	created := f.do(f.http, http.MethodPost, "/api/practice", map[string]any{}, nil)
	id, _ := created.field("id").(string)
	inHand := map[string]bool{}
	for _, k := range toStrings(created.field("hand")) {
		inHand[k] = true
	}
	var outside rules.Card
	found := false
	for _, c := range rules.NewDeck() {
		if !inHand[c.Key()] {
			outside, found = c, true
			break
		}
	}
	if !found {
		t.Fatal("27 张手牌之外应该有牌")
	}

	got := f.do(f.http, http.MethodPost, "/api/practice/"+id+"/play", map[string]any{
		"cards": []string{outside.Key()}, "kind": "single", "rank": outside.Rank.RankName(),
		"expected_revision": 1,
	}, nil)
	if got.status != http.StatusConflict || got.errCode() != "cards-not-in-hand" {
		t.Fatalf("不在手里的牌该 409 cards-not-in-hand，实际 %d %s", got.status, got.raw)
	}
	now := f.do(f.http, http.MethodGet, "/api/practice/"+id, nil, nil)
	if now.raw != created.raw {
		t.Fatalf("被拒之后局面不该变：\n%s\n%s", created.raw, now.raw)
	}
}
