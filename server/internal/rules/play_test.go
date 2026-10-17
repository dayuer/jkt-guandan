package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// 按牌例写法拿牌：「♠7/0」「大王/1」。
func mustCards(t *testing.T, keys ...string) []Card {
	t.Helper()
	out := make([]Card, len(keys))
	for i, k := range keys {
		c, err := ParseKey(k)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = c
	}
	return out
}

// newTestGame 开一副小残局：hands 是 1–4 号座的手牌（牌例写法，空格分开）。
func newTestGame(t *testing.T, level Rank, leader int, hands ...string) *Game {
	t.Helper()
	var hs [Seats][]Card
	for i, h := range hands {
		hs[i] = mustCards(t, strings.Fields(h)...)
	}
	g, err := NewGame(level, hs, leader)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mustPlay(t *testing.T, g *Game, seat int, typ ComboType, rank Rank, keys ...string) {
	t.Helper()
	if err := g.Play(seat, mustCards(t, keys...), typ, rank); err != nil {
		t.Fatalf("%d 号座出 %v：%v", seat, keys, err)
	}
}

func mustPass(t *testing.T, g *Game, seat int) {
	t.Helper()
	if err := g.Pass(seat); err != nil {
		t.Fatalf("%d 号座过牌：%v", seat, err)
	}
}

func wantTurn(t *testing.T, g *Game, seat int, why string) {
	t.Helper()
	if g.Turn() != seat {
		t.Fatalf("%s：应该轮到 %d 号座，实际轮到 %d 号座", why, seat, g.Turn())
	}
}

func TestSeats(t *testing.T) {
	// 逆时针 1 → 2 → 3 → 4 → 1；1、3 对家，2、4 对家。
	for s, want := range map[int]int{1: 2, 2: 3, 3: 4, 4: 1} {
		if got := NextSeat(s); got != want {
			t.Errorf("NextSeat(%d) = %d，应该是 %d", s, got, want)
		}
	}
	for s, want := range map[int]int{1: 3, 2: 4, 3: 1, 4: 2} {
		if got := PartnerSeat(s); got != want {
			t.Errorf("PartnerSeat(%d) = %d，应该是 %d", s, got, want)
		}
	}
}

// 第八条 一、1 和 一、3：逆时针出牌，其余三家都过，最后出牌的人领出下一圈。
func TestTrickEndsWhenOthersPass(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠3/0 ♠9/0", "♠4/0 ♠8/0", "♠5/0 ♠7/0", "♠6/0 ♠10/0")
	if !g.Leading() {
		t.Fatal("开局应该是领出")
	}
	mustPlay(t, g, 1, TypeSingle, Three, "♠3/0")
	wantTurn(t, g, 2, "1 号座出完牌")
	mustPlay(t, g, 2, TypeSingle, Four, "♠4/0")
	mustPass(t, g, 3)
	mustPass(t, g, 4)
	wantTurn(t, g, 1, "3、4 号座过了")
	if g.Leading() {
		t.Fatal("只过了两家，这一圈还没结束")
	}
	mustPass(t, g, 1)
	wantTurn(t, g, 2, "其余三家都过了，最后出牌的 2 号座领出")
	if !g.Leading() {
		t.Fatal("新的一圈应该是领出")
	}
}

func TestIllegalMovesLeaveStateUnchanged(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠9/0 ♣9/0 ♠3/0", "♠4/0 ♠K/0", "♠5/0 ♠7/0", "♠6/0 ♠10/0")
	before, _ := json.Marshal(g)

	cases := []struct {
		name string
		do   func() error
		want string
	}{
		{"领出不能过牌", func() error { return g.Pass(1) }, "不能过牌"},
		{"没轮到不能出", func() error { return g.Play(2, mustCards(t, "♠4/0"), TypeSingle, Four) }, "没轮到"},
		{"手里没有的牌", func() error { return g.Play(1, mustCards(t, "♠4/0"), TypeSingle, Four) }, "手里没有"},
		{"声明的牌型不成立", func() error { return g.Play(1, mustCards(t, "♠9/0", "♠3/0"), TypePair, 0) }, "当不成"},
		{"声明的牌面不对", func() error { return g.Play(1, mustCards(t, "♠9/0", "♣9/0"), TypePair, Ten) }, "当不成"},
		{"空手", func() error { return g.Play(1, nil, TypeSingle, 0) }, "没有牌"},
	}
	for _, tc := range cases {
		err := tc.do()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s：应该报「%s」，实际 %v", tc.name, tc.want, err)
		}
	}
	// 不合法的动作不改局面：存档（含手牌、动作、轮到谁）一字不差。
	if after, _ := json.Marshal(g); string(before) != string(after) {
		t.Fatalf("不合法的动作改了局面：\n前 %s\n后 %s", before, after)
	}

	// 压不住上家。
	mustPlay(t, g, 1, TypePair, Nine, "♠9/0", "♣9/0")
	before, _ = json.Marshal(g)
	if err := g.Play(2, mustCards(t, "♠4/0"), TypeSingle, Four); err == nil || !strings.Contains(err.Error(), "压不住") {
		t.Fatalf("单张压对子应该报「压不住」，实际 %v", err)
	}
	if after, _ := json.Marshal(g); string(before) != string(after) {
		t.Fatalf("压不住时改了局面：\n前 %s\n后 %s", before, after)
	}
}

// 第八条 一、1：跟牌要用「更大」的一手压上去：同牌型同张数比牌面，炸弹压普通牌型。
func TestFollowMustBeat(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠K/0 ♠3/0", "♠A/0 ♣A/0", "♠Q/0 ♠5/0", "♠6/0 ♣6/0 ♦6/0 ♥6/0 ♠8/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	mustPlay(t, g, 2, TypeSingle, Ace, "♠A/0") // A 压 K
	if err := g.Play(3, mustCards(t, "♠Q/0"), TypeSingle, Queen); err == nil {
		t.Fatal("Q 压不住 A")
	}
	mustPass(t, g, 3)
	mustPlay(t, g, 4, TypeBomb, Six, "♠6/0", "♣6/0", "♦6/0", "♥6/0") // 炸弹压单张
	if err := g.Play(1, mustCards(t, "♠3/0"), TypeSingle, Three); err == nil {
		t.Fatal("单张压不住炸弹")
	}
}

// 第九条（借风）：上游打完最后一手牌，余下各家都不压，由他的搭档领出。
func TestBorrowLeadAfterFinishing(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠K/0", "♠3/0 ♠4/0", "♠5/0 ♠6/0", "♠7/0 ♠8/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	if got := g.Finished(); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("1 号座出完了，Finished 应该是 [1]，实际 %v", got)
	}
	wantTurn(t, g, 2, "1 号座出完后轮到下家")
	mustPass(t, g, 2)
	mustPass(t, g, 3)
	mustPass(t, g, 4)
	wantTurn(t, g, 3, "1 号座出完、余下三家都过：搭档 3 号座借风领出")
	if !g.Leading() {
		t.Fatal("借风之后是领出")
	}
}

// 借风只在余下各家都不压时才有：有人压上去，就照常由最后出牌的人领出。
func TestNoBorrowLeadWhenSomeoneBeats(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠K/0", "♠A/0 ♠4/0", "♠5/0 ♠6/0", "♠7/0 ♠8/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	mustPlay(t, g, 2, TypeSingle, Ace, "♠A/0")
	mustPass(t, g, 3)
	mustPass(t, g, 4)
	wantTurn(t, g, 2, "2 号座压了上游的最后一手，3、4 号座都过（1 号座已出完不用表态）")
}

// 出完的人从出牌顺序里跳过；二游打完最后一手牌也能借风。
func TestSkipFinishedAndSecondPlaceBorrowsLead(t *testing.T) {
	// 1 号座先出完（上游），然后 2 号座出完（二游，和 1 号座不是一方，这一副还没完）。
	g := newTestGame(t, Two, 1,
		"♠K/0", "♠A/0", "♠5/0 ♠6/0", "♠7/0 ♠8/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	mustPlay(t, g, 2, TypeSingle, Ace, "♠A/0")
	if got := g.Finished(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("Finished 应该是 [1 2]，实际 %v", got)
	}
	if g.Over() {
		t.Fatal("上游、二游不是同一方，这一副还没结束")
	}
	wantTurn(t, g, 3, "1、2 号座出完了")
	mustPass(t, g, 3)
	mustPass(t, g, 4)
	wantTurn(t, g, 4, "二游 2 号座打完最后一手、余下都不压：搭档 4 号座借风")
	mustPlay(t, g, 4, TypeSingle, Seven, "♠7/0")
	wantTurn(t, g, 3, "4 号座之后跳过已出完的 1 号座，轮到 3 号座")
}

// 第八条 一、4：同一方拿了上游和二游，这一副结束（对方双下）。
func TestDoubleWinEndsGame(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠K/0", "♠3/0 ♠4/0", "♠A/0", "♠7/0 ♠8/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	mustPass(t, g, 2)
	mustPlay(t, g, 3, TypeSingle, Ace, "♠A/0")
	if !g.Over() {
		t.Fatal("1、3 号座是一方，上游、二游都拿了，这一副应该结束")
	}
	res, ok, err := g.Result()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("结束了应该有结果")
	}
	want := Result{Order: []int{1, 3, 2, 4}, HeadSeat: 1, PartnerPlace: 2}
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("结果 %+v，应该是 %+v", res, want)
	}
	if err := g.Pass(2); err == nil {
		t.Fatal("结束之后不能再出牌")
	}
}

// 第八条 一、4：三人出完，这一副结束；搭档名次按出完先后算。
func TestThreeFinishedEndsGame(t *testing.T) {
	g := newTestGame(t, Two, 1,
		"♠K/0", "♠A/0", "♠5/0 ♠6/0", "大王/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")     // 上游 1
	mustPlay(t, g, 2, TypeSingle, Ace, "♠A/0")      // 二游 2
	mustPass(t, g, 3)                               //
	mustPlay(t, g, 4, TypeSingle, BigJoker, "大王/0") // 三游 4
	if !g.Over() {
		t.Fatal("三人出完，这一副应该结束")
	}
	res, _, err := g.Result()
	if err != nil {
		t.Fatal(err)
	}
	want := Result{Order: []int{1, 2, 4, 3}, HeadSeat: 1, PartnerPlace: 4} // 1 号座的搭档 3 号座是下游
	if !reflect.DeepEqual(res, want) {
		t.Fatalf("结果 %+v，应该是 %+v", res, want)
	}
}

func TestResultBeforeOver(t *testing.T) {
	g := newTestGame(t, Two, 1, "♠K/0", "♠A/0", "♠5/0", "♠7/0")
	if _, ok, err := g.Result(); err != nil || ok {
		t.Fatalf("还没结束不该有结果：ok=%v err=%v", ok, err)
	}
}

// #239 复审 B1：Play 原来只核 Card.ID，拿手里一张牌的 ID、把 Rank 或 Suit 改掉就能当成别的牌
// 打出去，引擎照收 —— 而且存出来的档自己都读不回（写的是 ♠A/0，手里其实没有）。
// 现在牌的**每个字段**都得和手里那张一模一样。
func TestPlayRejectsTamperedCard(t *testing.T) {
	// 手里那张原样的牌照常能出 —— 别把好的也拦了。
	good := newTestGame(t, Two, 1, "♠3/0 ♠4/0", "♠5/0", "♠6/0", "♠7/0")
	mustPlay(t, good, 1, TypeSingle, Three, "♠3/0")

	cases := []struct {
		name   string
		mutate func(c *Card)
		rank   Rank
	}{
		{"改点数", func(c *Card) { c.Rank = Ace }, Ace},
		{"改花色", func(c *Card) { c.Suit = Heart }, Three},
		{"改副号", func(c *Card) { c.Deck = 1 }, Three},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newTestGame(t, Two, 1, "♠3/0 ♠4/0", "♠5/0", "♠6/0", "♠7/0")
			before, _ := json.Marshal(g)

			c := mustCards(t, "♠3/0")[0] // ID 是手里那张 ♠3/0 的
			tc.mutate(&c)
			err := g.Play(1, []Card{c}, TypeSingle, tc.rank)
			if !errors.Is(err, ErrCardsNotInHand) {
				t.Fatalf("改了牌的字段照样出了：err = %v", err)
			}
			if after, _ := json.Marshal(g); string(before) != string(after) {
				t.Fatal("被拒之后局面变了")
			}
		})
	}
}

// NewGame 也拦住「ID 和牌的字段对不上」的手牌：不拦的话 Play 按 ID 认牌就会认错。
func TestNewGameRejectsMismatchedCard(t *testing.T) {
	bad := mustCards(t, "♠3/0")[0]
	bad.Rank = Ace // ID 还是 ♠3/0 那张
	hands := [Seats][]Card{{bad}, mustCards(t, "♠4/0"), mustCards(t, "♠5/0"), mustCards(t, "♠6/0")}
	if _, err := NewGame(Two, hands, 1); err == nil {
		t.Fatal("ID 和字段对不上的手牌该被拒")
	}
}

// 拒绝的原因是**可判别的封闭取值**：调用方拿哨兵做 errors.Is，不用把句子拿去比字符串。
// 取值和 testdata/rules/play.json 里写的那些字面量一一对应。
func TestRejectReasons(t *testing.T) {
	// 字面量对得上牌例里写的那些（改这里就是改两端的格式，要一起改）
	for _, c := range []struct {
		reason RejectReason
		want   string
	}{
		{ReasonGameOver, "game-over"},
		{ReasonNotYourTurn, "not-your-turn"},
		{ReasonLeadingCannotPass, "leading-cannot-pass"},
		{ReasonNoCards, "no-cards"},
		{ReasonCardsNotInHand, "cards-not-in-hand"},
		{ReasonBadCombo, "bad-combo"},
		{ReasonDoesNotBeat, "does-not-beat"},
	} {
		if string(c.reason) != c.want {
			t.Fatalf("原因码 %q 应该是 %q", c.reason, c.want)
		}
	}

	g := newTestGame(t, Two, 1, "♠9/0 ♠3/0", "♠4/0", "♠5/0", "♠6/0")
	for _, tc := range []struct {
		name string
		want *RuleError
		do   func() error
	}{
		{"领出不能过牌", ErrLeadingCannotPass, func() error { return g.Pass(1) }},
		{"没轮到", ErrNotYourTurn, func() error { return g.Play(2, mustCards(t, "♠4/0"), TypeSingle, Four) }},
		{"没给牌", ErrNoCards, func() error { return g.Play(1, nil, TypeSingle, 0) }},
		{"手里没有", ErrCardsNotInHand, func() error { return g.Play(1, mustCards(t, "♠4/0"), TypeSingle, Four) }},
		{"声明不成立", ErrBadCombo, func() error { return g.Play(1, mustCards(t, "♠9/0", "♠3/0"), TypePair, 0) }},
	} {
		if err := tc.do(); !errors.Is(err, tc.want) {
			t.Fatalf("%s：应该能 errors.Is 判出 %q，实际 %v", tc.name, tc.want.Reason, err)
		}
	}

	// 压不住上家
	mustPlay(t, g, 1, TypeSingle, Nine, "♠9/0")
	if err := g.Play(2, mustCards(t, "♠4/0"), TypeSingle, Four); !errors.Is(err, ErrDoesNotBeat) {
		t.Fatalf("压不住：%v", err)
	}

	// 这一副已经结束
	over := newTestGame(t, Two, 1, "♠K/0", "♠3/0 ♠4/0", "♠A/0", "♠7/0 ♠8/0")
	mustPlay(t, over, 1, TypeSingle, King, "♠K/0")
	mustPass(t, over, 2)
	mustPlay(t, over, 3, TypeSingle, Ace, "♠A/0")
	if err := over.Play(4, mustCards(t, "♠7/0"), TypeSingle, Seven); !errors.Is(err, ErrGameOver) {
		t.Fatalf("结束了还在动：%v", err)
	}
}

// Result 在「结束了却没有出完记录」的矛盾状态返回错误，不 panic —— 服务端一次 panic 会带下一整局。
func TestResultRejectsContradictoryState(t *testing.T) {
	g := newTestGame(t, Two, 1, "♠K/0", "♠A/0", "♠5/0", "♠7/0")
	g.turn = 0 // 造出矛盾状态：Over() 为真、finished 却是空的
	if !g.Over() {
		t.Fatal("摆出来的应该就是「结束了」的样子")
	}
	if _, _, err := g.Result(); err == nil {
		t.Fatal("矛盾状态该返回错误，不能 panic")
	}
}

func TestNewGameRejectsBadSetup(t *testing.T) {
	var hs [Seats][]Card
	for i := range hs {
		hs[i] = mustCards(t, fmt.Sprintf("♠%d/0", i+3))
	}
	if _, err := NewGame(Two, hs, 0); err == nil {
		t.Fatal("领出座位 0 应该报错")
	}
	if _, err := NewGame(Rank(1), hs, 1); err == nil {
		t.Fatal("级数 1 应该报错")
	}
	dup := hs
	dup[1] = mustCards(t, "♠3/0")
	if _, err := NewGame(Two, dup, 1); err == nil || !strings.Contains(err.Error(), "同时在") {
		t.Fatalf("同一张牌在两家手里应该报错，实际 %v", err)
	}
	empty := hs
	empty[2] = nil
	if _, err := NewGame(Two, empty, 1); err == nil {
		t.Fatal("有人没牌应该报错")
	}
}

// 逢人配要按声明认：手里有 ♥2（打 2）时，♥2 + ♠9 声明成「对子 9」。
func TestPlayWithWildcardDeclaration(t *testing.T) {
	g := newTestGame(t, Two, 1, "♥2/0 ♠9/0 ♠3/0", "♠4/0", "♠5/0", "♠7/0")
	mustPlay(t, g, 1, TypePair, Nine, "♥2/0", "♠9/0")
	_, combo, ok := g.LastPlay()
	if !ok || combo.Type != TypePair || combo.Rank != Nine || len(combo.Wilds) != 1 {
		t.Fatalf("应该认成带一张逢人配的对子 9，实际 %+v", combo)
	}
}

// ---------------------------------------------------------------------------
// 序列化
// ---------------------------------------------------------------------------

func TestJSONRoundTripMidGame(t *testing.T) {
	g := newTestGame(t, Seven, 2,
		"♠K/0 ♠3/0", "♥7/0 ♠9/0 ♠4/0", "♠5/0 ♠6/0", "♠8/0 大王/0")
	mustPlay(t, g, 2, TypePair, Nine, "♥7/0", "♠9/0")
	mustPass(t, g, 3)

	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	// 牌按 Key 写，不出现 Go 的枚举序号（testdata/rules/README.md「还没定的」）。
	for _, bad := range []string{`"Suit"`, `"Rank":`, `"ID"`} {
		if strings.Contains(string(data), bad) {
			t.Fatalf("存档里不该有 %s：%s", bad, data)
		}
	}
	var back Game
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("读回来：%v\n%s", err, data)
	}
	again, _ := json.Marshal(&back)
	if string(again) != string(data) {
		t.Fatalf("读回来再写出去不一样：\n第一次 %s\n第二次 %s", data, again)
	}
	if back.Turn() != g.Turn() || back.Leading() != g.Leading() {
		t.Fatalf("读回来的局面不一样：轮到 %d / %d", back.Turn(), g.Turn())
	}
	// 读回来的局面能接着打，和原来的走出一样的结果。
	for _, gg := range []*Game{g, &back} {
		mustPass(t, gg, 4)
		mustPass(t, gg, 1)
		wantTurn(t, gg, 2, "其余三家都过")
	}
}

func TestUnmarshalRejectsBadSave(t *testing.T) {
	g := newTestGame(t, Two, 1, "♠K/0 ♠3/0", "♠A/0 ♠4/0", "♠5/0", "♠7/0")
	mustPlay(t, g, 1, TypeSingle, King, "♠K/0")
	data, _ := json.Marshal(g)

	cases := map[string]string{
		"动作不合法（改成出别人手里的牌）": strings.Replace(string(data), `"cards":["♠K/0"]`, `"cards":["♠A/0"]`, 1),
		"轮到谁对不上":  strings.Replace(string(data), `"turn":2`, `"turn":3`, 1),
		"出完的人对不上": strings.Replace(string(data), `"finished":[]`, `"finished":[1]`, 1),
		"级数写错":    strings.Replace(string(data), `"level":"2"`, `"level":"1"`, 1),
	}
	for name, bad := range cases {
		if bad == string(data) {
			t.Fatalf("%s：替换没生效，检查存档格式：%s", name, data)
		}
		var back Game
		if err := json.Unmarshal([]byte(bad), &back); err == nil {
			t.Fatalf("%s：坏存档应该读不进来", name)
		}
	}
}

// ---------------------------------------------------------------------------
// 随机整局：只为验状态机的不变式，出牌策略是测试里写死的笨办法（只出单张、对子或过），
// 不是合法出法生成（那是第 4 步）。
// ---------------------------------------------------------------------------

// dumbMove 给轮到的人挑一手：领出时出最小的单张；跟牌时找一张能压上的单张或一对，找不到就过。
func dumbMove(t *testing.T, rnd *rand.Rand, g *Game) {
	t.Helper()
	seat := g.Turn()
	hand := g.Hand(seat)
	try := func(cards []Card) bool {
		combos, err := Recognize(cards, g.Level())
		if err != nil || len(combos) == 0 {
			return false
		}
		for _, c := range combos {
			if g.Play(seat, cards, c.Type, c.Rank) == nil {
				return true
			}
		}
		return false
	}
	if g.Leading() {
		order := rnd.Perm(len(hand))
		for _, i := range order {
			if try([]Card{hand[i]}) {
				return
			}
		}
		t.Fatalf("%d 号座领出时一张单张都出不了：%s", seat, handText(hand))
	}
	if rnd.Intn(4) == 0 { // 有时候能压也不压
		mustPass(t, g, seat)
		return
	}
	for _, i := range rnd.Perm(len(hand)) {
		if try([]Card{hand[i]}) {
			return
		}
		for j := i + 1; j < len(hand); j++ {
			if try([]Card{hand[i], hand[j]}) {
				return
			}
		}
	}
	mustPass(t, g, seat)
}

func TestRandomGamesInvariants(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261016))
	levels := []Rank{Two, Seven, Ace}
	places := map[int]int{} // 搭档名次 → 局数
	borrowed := 0           // 借风了几次
	for game := 0; game < 60; game++ {
		dealt, err := Deal(PermShuffler(rnd.Perm(108)))
		if err != nil {
			t.Fatal(err)
		}
		g, err := NewGame(levels[game%len(levels)], dealt, 1+rnd.Intn(Seats))
		if err != nil {
			t.Fatal(err)
		}
		for steps := 0; !g.Over(); steps++ {
			if steps > 5000 {
				t.Fatalf("第 %d 局走了 5000 步还没结束", game)
			}
			lastSeat, _, wasFollowing := g.LastPlay()
			dumbMove(t, rnd, g)
			// 一圈刚结束、领出的不是最后出牌的人：那就是借风（最后出牌的人已经出完）。
			if wasFollowing && g.Leading() && !g.Over() && g.Turn() != lastSeat {
				if g.Turn() != PartnerSeat(lastSeat) || len(g.Hand(lastSeat)) != 0 {
					t.Fatalf("第 %d 局：一圈结束后由 %d 号座领出，最后出牌的是 %d 号座（还剩 %d 张）",
						game, g.Turn(), lastSeat, len(g.Hand(lastSeat)))
				}
				borrowed++
			}

			// 牌数守恒：手里的 + 打出去的 = 108，且没有重复。
			seen := map[int]bool{}
			for s := 1; s <= Seats; s++ {
				for _, c := range g.Hand(s) {
					seen[c.ID] = true
				}
			}
			for _, m := range g.Moves() {
				for _, c := range m.Combo.Cards {
					if seen[c.ID] {
						t.Fatalf("第 %d 局：%s 同时在手里和打出去的牌里", game, c.Key())
					}
					seen[c.ID] = true
				}
			}
			if len(seen) != 108 {
				t.Fatalf("第 %d 局：牌数不守恒，手里 + 打出去的一共 %d 张", game, len(seen))
			}
			// 轮到的人一定还没出完。
			if !g.Over() && len(g.Hand(g.Turn())) == 0 {
				t.Fatalf("第 %d 局：轮到了已经出完的 %d 号座", game, g.Turn())
			}
			// 每隔几步存一次、读回来再存，一字不差（读档要从头重放，存得太勤会拖慢测试）。
			if steps%25 == 0 {
				data, err := json.Marshal(g)
				if err != nil {
					t.Fatal(err)
				}
				var back Game
				if err := json.Unmarshal(data, &back); err != nil {
					t.Fatalf("第 %d 局第 %d 步读回来：%v", game, steps, err)
				}
				if again, _ := json.Marshal(&back); string(again) != string(data) {
					t.Fatalf("第 %d 局第 %d 步读回来再写出去不一样", game, steps)
				}
			}
		}
		res, ok, err := g.Result()
		if err != nil {
			t.Fatalf("第 %d 局结果报错：%v", game, err)
		}
		if !ok || len(res.Order) != Seats || res.PartnerPlace < 2 || res.PartnerPlace > 4 {
			t.Fatalf("第 %d 局结果不对：%+v", game, res)
		}
		places[res.PartnerPlace]++
	}
	t.Logf("搭档名次：二游 %d 局、三游 %d 局、下游 %d 局；借风 %d 次", places[2], places[3], places[4], borrowed)
	// 三种结局和借风都要真的出现过，否则「全过」不说明什么。
	if places[2] == 0 || places[3] == 0 || places[4] == 0 || borrowed == 0 {
		t.Fatalf("随机整局覆盖不够：搭档名次 %v，借风 %d 次", places, borrowed)
	}
}
