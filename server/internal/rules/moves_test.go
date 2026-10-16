package rules

import (
	"math/rand"
	"testing"
	"time"
)

// 合法出法（#204 第 4 步）的性质测试 + 和暴力枚举的对拍。
//
// 性质：
//   - 领出时合法出法集合**不为空**（手里有牌就至少能出单张）；
//   - 每一手用的牌都是手里的牌、不重复，张数符合牌型（牌数守恒）；
//   - 同一批出法里没有两手互相压得住（beats 反对称）；
//   - 跟牌时每一手都**真的**压得住上一手。
//
// 对拍：手牌小的时候（≤ 9 张）把全部子集枚举一遍，用 Recognize 认出来，
// 和 Moves 的结果逐条比 —— 这是独立于候选生成器的第二条路，能查出「漏了某种牌型」。

// randomHand 随机抓 n 张不重复的牌。级数从 levels 里挑，手牌偏向中间点数、常带逢人配，
// 不然随机到的牌既连不成顺子也没有逢人配，覆盖不到有意义的路径。
func randomHand(rnd *rand.Rand, deck []Card, n int, level Rank) []Card {
	hand := make([]Card, 0, n)
	seen := map[int]bool{}
	guard := 0
	for len(hand) < n && guard < 10000 {
		guard++
		c := deck[rnd.Intn(len(deck))]
		if seen[c.ID] {
			continue
		}
		if rnd.Intn(4) != 0 && !c.IsWild(level) {
			if c.Rank.IsJoker() || c.Rank < Two+1 || c.Rank > Ace-2 {
				continue
			}
		}
		seen[c.ID] = true
		hand = append(hand, c)
	}
	return hand
}

func levelFor(i int) Rank {
	levels := []Rank{Two, Three, Five, Seven, Nine, Jack, Ace}
	return levels[i%len(levels)]
}

func TestMovesLeadNonEmptyAndConserved(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20261))
	for i := 0; i < 2000; i++ {
		level := levelFor(i)
		hand := randomHand(rnd, deck, 1+rnd.Intn(27), level)
		moves, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatalf("手牌 %s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		if len(moves) == 0 {
			t.Fatalf("手牌 %s 打 %s：领出时合法出法不该为空（至少能出单张）", cardsText(hand), level.RankName())
		}
		checkMovesBelongToHand(t, hand, level, moves)
	}
}

func TestMovesFollowBeats(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20262))
	for i := 0; i < 2000; i++ {
		level := levelFor(i)
		hand := randomHand(rnd, deck, 2+rnd.Intn(26), level)
		other := randomHand(rnd, deck, 1+rnd.Intn(10), level)
		lead, ok, err := FindCombo(other, level, TypeSingle)
		if err != nil || !ok {
			continue // 上一手认不出来就跳过，这里只测跟牌
		}
		moves, err := Moves(hand, level, &lead)
		if err != nil {
			t.Fatalf("手牌 %s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		checkMovesBelongToHand(t, hand, level, moves)
		for _, m := range moves {
			if m.Compare(lead) != Greater {
				t.Fatalf("手牌 %s 打 %s：跟牌集合里有压不住上一手 %s 的：%s",
					cardsText(hand), level.RankName(), lead.String(), m.String())
			}
		}
	}
}

func TestMovesAntisymmetric(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20263))
	for i := 0; i < 500; i++ {
		level := levelFor(i)
		hand := randomHand(rnd, deck, 4+rnd.Intn(23), level)
		moves, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatal(err)
		}
		for a := range moves {
			for b := range moves {
				if a == b {
					continue
				}
				if moves[a].Compare(moves[b]) == Greater && moves[b].Compare(moves[a]) == Greater {
					t.Fatalf("手牌 %s 打 %s：%s 和 %s 互相压得住",
						cardsText(hand), level.RankName(), moves[a].String(), moves[b].String())
				}
			}
		}
	}
}

// 和暴力枚举对拍：小手里把**全部子集**认一遍，结果必须和 Moves 一模一样
// （每一条解读、每一套牌都一样，连顺序也一样）。
func TestMovesMatchesBruteForce(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20264))
	checked := 0
	for i := 0; i < 400; i++ {
		level := levelFor(i)
		n := 4 + rnd.Intn(6) // 4–9 张：2^9 = 512 个子集，跑得动
		hand := randomHand(rnd, deck, n, level)

		want := bruteForceMoves(t, hand, level)
		got, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatalf("手牌 %s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		checked++
		if len(got) != len(want) {
			t.Fatalf("手牌 %s 打 %s：Moves 给了 %d 手，暴力枚举 %d 手\nMoves：%s\n暴力：%s",
				cardsText(hand), level.RankName(), len(got), len(want),
				comboListText(got), comboListText(want))
		}
		for k := range want {
			if comboKey(got[k]) != comboKey(want[k]) || !sameCards(got[k].Cards, want[k].Cards) {
				t.Fatalf("手牌 %s 打 %s：第 %d 手不一样\nMoves：%s\n暴力：%s",
					cardsText(hand), level.RankName(), k+1, comboListText(got), comboListText(want))
			}
		}
	}
	if checked == 0 {
		t.Fatal("一手都没对拍，用例没起作用")
	}
	t.Logf("对拍了 %d 手牌", checked)
}

// 跟牌那条路也要对拍：上一手摆在面前时，Moves 只该给「压得住」的那些。
// （跟牌走的是另一条代码路径 —— 它按上一手的牌型只生成需要的牌型，漏了的话会报「无牌可跟」。）
func TestMovesFollowMatchesBruteForce(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20266))
	checked := 0
	for i := 0; i < 1500; i++ {
		level := levelFor(i)
		hand := randomHand(rnd, deck, 3+rnd.Intn(7), level)
		other := randomHand(rnd, deck, 1+rnd.Intn(5), level)
		combos, err := Recognize(other, level)
		if err != nil || len(combos) == 0 {
			continue // 上一手得是一手真牌
		}
		lead := combos[rnd.Intn(len(combos))]

		var want []Combo
		for _, c := range bruteForceMoves(t, hand, level) {
			if c.Compare(lead) == Greater {
				want = append(want, c)
			}
		}
		got, err := Moves(hand, level, &lead)
		if err != nil {
			t.Fatalf("手牌 %s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		checked++
		if len(got) != len(want) {
			t.Fatalf("手牌 %s 打 %s，上一手 %s：Moves 给了 %d 手，暴力枚举 %d 手\nMoves：%s\n暴力：%s",
				cardsText(hand), level.RankName(), lead.String(), len(got), len(want),
				comboListText(got), comboListText(want))
		}
		for k := range want {
			if comboKey(got[k]) != comboKey(want[k]) || !sameCards(got[k].Cards, want[k].Cards) {
				t.Fatalf("手牌 %s 打 %s，上一手 %s：第 %d 手不一样\nMoves：%s\n暴力：%s",
					cardsText(hand), level.RankName(), lead.String(), k+1, comboListText(got), comboListText(want))
			}
		}
	}
	if checked == 0 {
		t.Fatal("一手都没对拍，用例没起作用")
	}
	t.Logf("跟牌对拍了 %d 手牌", checked)
}

// edgeHands 是**随机手牌抽不到**的边界牌型：A 当 1 的三种窗口、绕圈边界的 QKA、
// 两张逢人配、七个 3（炸弹张数）。随机手牌被 randomHand 压在 3..K，A2345 这种一次也抽不出来，
// 于是「A 当 1」的窗口整段漏掉它也是绿的 —— jkt-2 在 TS 端把 rankWindows 里的 A 当 1 删干净，
// 全部测试照样过。这里的每一手都写死，并且和暴力枚举对拍（对拍用的是 Recognize，独立于生成器）。
var edgeHands = []struct {
	name  string
	cards []string
	level string
}{
	{"A 当 1 的顺子", []string{"♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"}, "7"},
	{"A 当 1 的顺子（两个窗口挨着）", []string{"♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0", "♠6/0"}, "7"},
	{"A 当 1 的顺子 + 逢人配", []string{"♠2/0", "♠3/0", "♠4/0", "♠5/0", "♥7/0"}, "7"},
	{"A 当 1 的三连对", []string{"♠A/0", "♥A/0", "♠2/0", "♥2/0", "♠3/0", "♥3/0"}, "7"},
	{"A 当 1 的三同连张", []string{"♠A/0", "♥A/0", "♦A/0", "♠2/0", "♥2/0", "♦2/0"}, "7"},
	{"绕圈另一头：三连对 QKA", []string{"♠Q/0", "♥Q/0", "♠K/0", "♥K/0", "♠A/0", "♥A/0"}, "7"},
	{"绕圈另一头：三同连张 KKA（不连着，不该有）", []string{"♠Q/0", "♥Q/0", "♦Q/0", "♠A/0", "♥A/0", "♦A/0"}, "7"},
	{"两张逢人配", []string{"♥7/0", "♥7/1"}, "7"},
	{"两张逢人配 + 顺子", []string{"♠3/0", "♠4/0", "♠5/0", "♥7/0", "♥7/1"}, "7"},
	{"七个 3：炸弹张数", []string{"♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1"}, "7"},
	{"七个 3 + 一张逢人配：炸弹张数×顶法", []string{"♠3/0", "♥3/0", "♣3/0", "♦3/0", "♠3/1", "♥3/1", "♣3/1", "♥7/0"}, "7"},
	{"四大天王", []string{"大王/0", "大王/1", "小王/0", "小王/1"}, "7"},
	{"两张逢人配都要用上：三带对 9 的三种打法", []string{"♠9/0", "♥9/0", "♣9/0", "♠5/0", "♥7/0", "♥7/1"}, "7"},
	{"级牌自己：2 是级数时的 A 当 1", []string{"♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"}, "2"},
}

// 边界手牌也要和暴力枚举对拍：领出一次，再拿手里每种解读当上一手各对一次（顺带压到跟牌那条路）。
func TestMovesEdgeHandsMatchBruteForce(t *testing.T) {
	cases := 0
	for _, h := range edgeHands {
		level, err := ParseRank(h.level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", h.name, h.level, err)
		}
		hand := cardsOf(t, h.cards)
		want := bruteForceMoves(t, hand, level)
		got, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatalf("%s：%v", h.name, err)
		}
		compareMoves(t, h.name+"（领出）", hand, level, nil, got, want)
		cases++

		for _, lead := range want {
			var wantFollow []Combo
			for _, c := range want {
				if c.Compare(lead) == Greater {
					wantFollow = append(wantFollow, c)
				}
			}
			gotFollow, err := Moves(hand, level, &lead)
			if err != nil {
				t.Fatalf("%s：%v", h.name, err)
			}
			compareMoves(t, h.name+"（跟 "+lead.String()+"）", hand, level, &lead, gotFollow, wantFollow)
			cases++
		}
	}
	if cases == 0 {
		t.Fatal("一手都没对拍，用例没起作用")
	}
	t.Logf("边界手牌对拍了 %d 次（%d 手牌 × 领出 + 每种解读当上一手）", cases, len(edgeHands))
}

// compareMoves 逐条比两批出法（连顺序一起）。
func compareMoves(t *testing.T, what string, hand []Card, level Rank, lead *Combo, got, want []Combo) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s：Moves 给了 %d 手，暴力枚举 %d 手\nMoves：%s\n暴力：%s",
			what, len(got), len(want), comboListText(got), comboListText(want))
	}
	for k := range want {
		if comboKey(got[k]) != comboKey(want[k]) || !sameCards(got[k].Cards, want[k].Cards) {
			t.Fatalf("%s：第 %d 手不一样\nMoves：%s\n暴力：%s", what, k+1, comboListText(got), comboListText(want))
		}
	}
}

// bruteForceMoves 把全部子集（1–10 张）认一遍，按和 Moves 一样的规矩去重排序。
// 它只用来对拍，不追求性能。
func bruteForceMoves(t *testing.T, hand []Card, level Rank) []Combo {
	t.Helper()
	cards := sortCards(hand)
	best := map[string]Combo{}
	for mask := 1; mask < 1<<len(cards); mask++ {
		var set []Card
		for i := range cards {
			if mask&(1<<i) != 0 {
				set = append(set, cards[i])
			}
		}
		if len(set) > 10 {
			continue // 一手最多 10 张
		}
		combos, err := Recognize(set, level)
		if err != nil {
			t.Fatalf("暴力枚举 %s：%v", cardsText(set), err)
		}
		for _, c := range combos {
			key := comboKey(c)
			if old, ok := best[key]; !ok || lessCards(c.Cards, old.Cards) {
				best[key] = c
			}
		}
	}
	out := make([]Combo, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	return sortCombos2(out)
}

func sameCards(a, b []Card) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func comboListText(combos []Combo) string {
	out := ""
	for i, c := range combos {
		if i > 0 {
			out += "；"
		}
		out += c.String() + "[" + cardsText(c.Cards) + "]"
	}
	if out == "" {
		return "（空）"
	}
	return out
}

// checkMovesBelongToHand 查「牌数守恒」：每一手的牌都在手里、不重复，张数符合牌型。
func checkMovesBelongToHand(t *testing.T, hand []Card, level Rank, moves []Combo) {
	t.Helper()
	inHand := map[int]bool{}
	for _, c := range hand {
		inHand[c.ID] = true
	}
	for _, m := range moves {
		if len(m.Cards) == 0 {
			t.Fatalf("有一手没有牌：%s", m.String())
		}
		seen := map[int]bool{}
		for _, c := range m.Cards {
			if !inHand[c.ID] {
				t.Fatalf("手牌 %s 打 %s：出法 %s 用了不在手里的牌 %s",
					cardsText(hand), level.RankName(), m.String(), c.Key())
			}
			if seen[c.ID] {
				t.Fatalf("手牌 %s 打 %s：出法 %s 把 %s 用了两次",
					cardsText(hand), level.RankName(), m.String(), c.Key())
			}
			seen[c.ID] = true
		}
		if want := comboSize(m.Type); want != 0 && len(m.Cards) != want {
			t.Fatalf("手牌 %s 打 %s：%s 有 %d 张，%s 应该是 %d 张",
				cardsText(hand), level.RankName(), m.String(), len(m.Cards), m.Type.label(), want)
		}
		if m.Type == TypeBomb && (len(m.Cards) < 4 || len(m.Cards) > 10) {
			t.Fatalf("炸弹张数 %d 不对：%s", len(m.Cards), m.String())
		}
	}
}

// comboSize 是每种牌型的固定张数（炸弹不固定，返回 0）。
func comboSize(t ComboType) int {
	switch t {
	case TypeSingle:
		return 1
	case TypePair:
		return 2
	case TypeTriple:
		return 3
	case TypeTripleWithPair:
		return 5
	case TypeTube, TypePlate:
		return 6
	case TypeStraight, TypeStraightFlush:
		return 5
	case TypeJokerBomb:
		return 4
	}
	return 0 // 炸弹：4–10 张，单独查
}

// 27 张手牌的耗时上限（#204 第 4 步的性能要求）。
//
// 真机上的用途：练习局里每走一步，服务端都要算一遍「能跟哪些牌」（客户端提示走同一条路）。
// 27 张手牌 + 2 张逢人配是最坏的一档。
//
// 实测（本机、2026-10，27 张手牌）：
//
//	跟一对 9：最快 21µs、最慢 13ms
//	领出：    最快 75µs、最慢 89ms（最多一次列出 498 手）
//
// 快慢差三个量级，不是机器 load，是**手牌本身**：手里有 2 张逢人配时，每个候选都要
// 让 Recognize 把 13² 种顶法试一遍，慢的就是这一档。所以：
//
//   - 用「最快一次」当主断言 —— 它跟 load 无关，守的是「不能退化成枚举子集」这类
//     数量级退化（那会从 µs 掉到秒级），不会因为机器忙就乱红；
//   - 「最慢一次」只挂一个大到不会误伤的上限，当「别挂住」的哨兵（load 高时最慢那条
//     在 CI 上量到过 690ms，是同一份代码）。
//
// 精确的行为契约不靠时间：moves.json 的 8 条牌例把「这一手牌该有哪些打法」逐条钉死了，
// 还有和暴力枚举的对拍，都跟机器快慢无关。
func TestMoves27CardsPerformance(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20265))
	// 上限按 `-race` 实测重定：本地 CI 跑的是 `go test -race`，插桩后实测慢 ~5 倍
	// （同一手牌：不带 -race 领出最慢 138ms，带 -race 582ms），倍数放在 perfScale 里，
	// 由 build tag 二选一（perf_race_on_test.go / perf_race_off_test.go）。
	const followFastBudget = 5 * time.Millisecond * perfScale
	const leadFastBudget = 20 * time.Millisecond * perfScale
	const followSlowBudget = 500 * time.Millisecond * perfScale
	const leadSlowBudget = 3 * time.Second * perfScale

	followFast, followSlow := time.Hour, time.Duration(0)
	for i := 0; i < 50; i++ {
		level := Seven
		hand := randomHand(rnd, deck, 27, level)
		// 跟一对 9：最常见的跟牌路径。
		lead := Combo{Type: TypePair, Rank: Nine, Cards: []Card{deck[0], deck[1]}, Value: comboValue(TypePair, Nine, level)}
		start := time.Now()
		if _, err := Moves(hand, level, &lead); err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if elapsed < followFast {
			followFast = elapsed
		}
		if elapsed > followSlow {
			followSlow = elapsed
		}
	}

	// 领出：要造全部牌型，是最重的一条路。
	leadFast, leadSlow, worstMoves := time.Hour, time.Duration(0), 0
	for i := 0; i < 20; i++ {
		level := Seven
		hand := randomHand(rnd, deck, 27, level)
		start := time.Now()
		moves, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if elapsed < leadFast {
			leadFast = elapsed
		}
		if elapsed > leadSlow {
			leadSlow = elapsed
		}
		if len(moves) > worstMoves {
			worstMoves = len(moves)
		}
	}
	t.Logf("27 张手牌：跟一对 9 最快 %v／最慢 %v；领出最快 %v／最慢 %v，最多列出 %d 手",
		followFast, followSlow, leadFast, leadSlow, worstMoves)

	if followFast > followFastBudget {
		t.Fatalf("27 张手牌求跟牌，最快一次也要 %v，超过 %v —— 像是退化成枚举子集了", followFast, followFastBudget)
	}
	if leadFast > leadFastBudget {
		t.Fatalf("27 张手牌领出，最快一次也要 %v，超过 %v —— 像是退化成枚举子集了", leadFast, leadFastBudget)
	}
	if followSlow > followSlowBudget {
		t.Fatalf("27 张手牌求跟牌，最慢一次 %v，超过哨兵上限 %v", followSlow, followSlowBudget)
	}
	if leadSlow > leadSlowBudget {
		t.Fatalf("27 张手牌领出，最慢一次 %v，超过哨兵上限 %v", leadSlow, leadSlowBudget)
	}
}
