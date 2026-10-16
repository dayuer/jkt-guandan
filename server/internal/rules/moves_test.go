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
// 数字（本机、2026-10）：跟一对 9 不到 1ms；领出最重（要造全部牌型）最慢一次约 140ms、最多列出约 500 手。
// 上限取得比实测宽一个量级 —— 它要守的是「不能退化成枚举子集那种做法」（那会从毫秒掉到秒级），
// 不是抠具体毫秒，也免得机器 load 高时偶尔变红。
func TestMoves27CardsPerformance(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20265))
	const followBudget = 100 * time.Millisecond
	const leadBudget = 500 * time.Millisecond

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
		if i == 0 {
			t.Logf("27 张手牌跟一对 9：%v", elapsed)
		}
		if elapsed > followBudget {
			t.Fatalf("27 张手牌求跟牌用了 %v，超过上限 %v", elapsed, followBudget)
		}
	}

	// 领出：要造全部牌型，是最重的一条路。
	worstMoves, worstElapsed := 0, time.Duration(0)
	for i := 0; i < 20; i++ {
		level := Seven
		hand := randomHand(rnd, deck, 27, level)
		start := time.Now()
		moves, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatal(err)
		}
		elapsed := time.Since(start)
		if len(moves) > worstMoves {
			worstMoves = len(moves)
		}
		if elapsed > worstElapsed {
			worstElapsed = elapsed
		}
		if elapsed > leadBudget {
			t.Fatalf("27 张手牌领出用了 %v，超过上限 %v", elapsed, leadBudget)
		}
	}
	t.Logf("27 张手牌领出：最慢一次 %v、最多列出 %d 手", worstElapsed, worstMoves)
}
