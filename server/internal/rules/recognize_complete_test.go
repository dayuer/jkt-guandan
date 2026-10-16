package rules

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

// 「完整性」交叉验证（#223）：Recognize 该给的解读一个不少、也不多。
//
// TestWildReadingsRestore 验的是「给出来的解读都成立」（按声明的顶法还原后牌型牌面对得上），
// 漏掉一种解读它查不出来。这里补另一半：照规则原文另写一份**独立的**判定 naiveShapes ——
// 从「这种牌型需要哪些牌点、各几张」出发，看手里实打实的牌能不能放进去、剩下的空位是不是
// 正好由逢人配补齐 —— 不调用 recognizeCards / seqTop，也不枚举顶法。两边对同一手牌给出的
// （牌型, 牌面）集合必须完全相同。两份实现思路不同，同时在同一处犯错的概率很小。
//
// 规则依据（2022《竞技掼蛋竞赛规则（试行）》）：
//   - 第三条：十种牌型的构成；
//   - 第四条 一、2：A 在三连对、三同连张、顺子、同花顺里可以当 1（但不能首尾相接）；
//   - 第二条 14：红心级牌能替代除大小王以外的任意牌张参与组合；
//   - #217 定的口径：手里全是逢人配时（单出一张、两张一起出），只按级牌本身算。

// need 是一种解读需要的牌：每个点数几张；flush 表示还要求同一花色（同花顺）。
type need struct {
	typ   ComboType
	rank  Rank // 这种解读的牌面（顺子类是顶张，四大天王是 0）
	cards map[Rank]int
	flush bool
}

func (n need) size() int {
	t := 0
	for _, k := range n.cards {
		t += k
	}
	return t
}

// seqRank 把「A 当 1」的 1 换回 A，别的照原样。
func seqRank(v int) Rank {
	if v == 1 {
		return Ace
	}
	return Rank(v)
}

// allNeeds 按第三条把所有可能的（牌型, 牌面）和它们要的牌列出来。只看张数 n，跟手里是什么牌无关。
func allNeeds(n int) []need {
	var out []need
	ranks := []Rank{Two, Three, Four, Five, Six, Seven, Eight, Nine, Ten, Jack, Queen, King, Ace, SmallJoker, BigJoker}
	for _, r := range ranks {
		switch {
		case n == 1:
			out = append(out, need{TypeSingle, r, map[Rank]int{r: 1}, false})
		case n == 2:
			out = append(out, need{TypePair, r, map[Rank]int{r: 2}, false}) // 两张大王、两张小王也是对子（第三条 2）
		case n == 3:
			out = append(out, need{TypeTriple, r, map[Rank]int{r: 3}, false})
		case n >= 4 && n <= 10:
			out = append(out, need{TypeBomb, r, map[Rank]int{r: n}, false})
		}
	}
	if n == 4 {
		out = append(out, need{TypeJokerBomb, 0, map[Rank]int{SmallJoker: 2, BigJoker: 2}, false})
	}
	if n == 5 {
		for _, t := range ranks {
			for _, p := range ranks {
				if t != p {
					out = append(out, need{TypeTripleWithPair, t, map[Rank]int{t: 3, p: 2}, false})
				}
			}
		}
	}
	// 顺子类：连着的牌点，从「A 当 1」的 1 起，到 A 当 14 为止，不首尾相接。
	seq := func(typ ComboType, width, each int, flush bool) {
		for lo := 1; lo+width-1 <= 14; lo++ {
			m := map[Rank]int{}
			for v := lo; v < lo+width; v++ {
				m[seqRank(v)] = each
			}
			out = append(out, need{typ, Rank(lo + width - 1), m, flush})
		}
	}
	switch n {
	case 6:
		seq(TypeTube, 3, 2, false)
		seq(TypePlate, 2, 3, false)
	case 5:
		seq(TypeStraight, 5, 1, false)
		seq(TypeStraightFlush, 5, 1, true)
	}
	return out
}

// fits 判断手里实打实的牌加上 wilds 张逢人配，能不能凑成 nd。
func fits(nd need, solids []Card, wilds int) bool {
	if nd.size() != len(solids)+wilds {
		return false
	}
	have := map[Rank]int{}
	for _, c := range solids {
		have[c.Rank]++
	}
	gap := 0
	for r, k := range have {
		if k > nd.cards[r] {
			return false // 手里这个点数比要的多，或者根本不要这个点数
		}
	}
	for r, k := range nd.cards {
		miss := k - have[r]
		if miss > 0 && r.IsJoker() {
			return false // 逢人配不能顶王（第二条 14）
		}
		gap += miss
	}
	if gap != wilds {
		return false
	}
	if nd.flush {
		for _, c := range solids[1:] {
			if c.Suit != solids[0].Suit {
				return false // 实牌花色不一样；逢人配能顶任何花色，所以只看实牌
			}
		}
	}
	return true
}

// naiveShapes 是独立的判定：这手牌在这个级数下能当成哪些（牌型, 牌面）。
func naiveShapes(hand []Card, level Rank) map[string]bool {
	var solids []Card
	wilds := 0
	for _, c := range hand {
		if c.IsWild(level) {
			wilds++
			continue
		}
		solids = append(solids, c)
	}
	if len(solids) == 0 {
		// 全是逢人配：只按级牌本身算（#217 的口径），也就是把它们当成实牌、不再顶替。
		solids, wilds = hand, 0
	}
	out := map[string]bool{}
	for _, nd := range allNeeds(len(hand)) {
		if fits(nd, solids, wilds) {
			out[shapeKey(nd.typ, nd.rank)] = true
		}
	}
	return out
}

func shapeKey(t ComboType, r Rank) string {
	if t == TypeJokerBomb {
		return t.String()
	}
	return t.String() + "/" + r.RankName()
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// completenessHand 造一手随机牌：偏向几个相邻的点数（不然随机到顺子类的太少），
// 常带逢人配和王（这两样是最容易出错的地方）。
func completenessHand(rnd *rand.Rand, deck []Card, level Rank) []Card {
	n := 1 + rnd.Intn(10)
	lo := Two + Rank(rnd.Intn(10)) // 一个 4–5 个点数宽的窗口
	width := 4 + rnd.Intn(2)
	hand := make([]Card, 0, n)
	seen := map[int]bool{}
	for tries := 0; len(hand) < n && tries < 1000; tries++ {
		c := deck[rnd.Intn(len(deck))]
		if seen[c.ID] {
			continue
		}
		switch {
		case c.IsWild(level):
			// 逢人配总是收（两副牌只有两张，收下来概率也不高）
		case c.Rank.IsJoker():
			if rnd.Intn(3) != 0 {
				continue // 王收一部分
			}
		case c.Rank < lo || c.Rank >= lo+Rank(width):
			if rnd.Intn(8) != 0 {
				continue // 窗口外的点数大多不要
			}
		}
		seen[c.ID] = true
		hand = append(hand, c)
	}
	// 再随手塞一张逢人配进去，让带逢人配的手牌占到一半上下。
	if len(hand) < 10 && rnd.Intn(2) == 0 {
		for _, c := range deck {
			if c.IsWild(level) && !seen[c.ID] {
				hand = append(hand, c)
				break
			}
		}
	}
	return hand
}

// templateHand 按一种随机挑的牌型发牌：先照 allNeeds 里的要求凑齐，再随机把几张换成逢人配，
// 或者把一张改成别的点数（造「差一点就成」的反例）。纯随机发牌大多什么也凑不成，测不狠。
func templateHand(rnd *rand.Rand, deck []Card, level Rank) []Card {
	// 先在十种牌型里均匀挑一种，再挑它的一个牌面：直接按张数挑的话，同花顺、四大天王太少碰到。
	typ := ComboType(rnd.Intn(int(TypeJokerBomb) + 1))
	sizes := map[ComboType][]int{
		TypeSingle: {1}, TypePair: {2}, TypeTriple: {3}, TypeTripleWithPair: {5},
		TypeTube: {6}, TypePlate: {6}, TypeStraight: {5}, TypeStraightFlush: {5},
		TypeBomb: {4, 5, 6, 7, 8, 9, 10}, TypeJokerBomb: {4},
	}[typ]
	var needs []need
	for _, nd := range allNeeds(sizes[rnd.Intn(len(sizes))]) {
		if nd.typ == typ {
			needs = append(needs, nd)
		}
	}
	nd := needs[rnd.Intn(len(needs))]
	byRank := map[Rank][]Card{}
	for _, c := range deck {
		if !c.IsWild(level) {
			byRank[c.Rank] = append(byRank[c.Rank], c)
		}
	}
	suit := Suit(rnd.Intn(4)) // 同花顺要的那个花色
	seen := map[int]bool{}
	var hand []Card
	// 按点数排好序再发：直接 range map 的话顺序每次不同，随机数的消耗顺序跟着变，
	// 同一个种子发出来的手牌就不一样了 —— 失败时没法复现。
	ranks := make([]Rank, 0, len(nd.cards))
	for r := range nd.cards {
		ranks = append(ranks, r)
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i] < ranks[j] })
	for _, r := range ranks {
		k := nd.cards[r]
		pool := byRank[r]
		rnd.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
		for _, c := range pool {
			if k == 0 {
				break
			}
			if seen[c.ID] || (nd.flush && c.Suit != suit) {
				continue
			}
			seen[c.ID] = true
			hand = append(hand, c)
			k--
		}
	}
	// 换成逢人配（最多两张，只换非王）。
	wilds := []Card{}
	for _, c := range deck {
		if c.IsWild(level) {
			wilds = append(wilds, c)
		}
	}
	for _, w := range wilds {
		if len(hand) == 0 || rnd.Intn(2) == 0 {
			continue
		}
		i := rnd.Intn(len(hand))
		if hand[i].Rank.IsJoker() {
			continue
		}
		hand[i] = w
	}
	// 三分之一的手牌改坏一张：换成牌堆里随便一张没用过的牌。
	if len(hand) > 0 && rnd.Intn(3) == 0 {
		for {
			c := deck[rnd.Intn(len(deck))]
			used := false
			for _, h := range hand {
				if h.ID == c.ID {
					used = true
					break
				}
			}
			if !used {
				hand[rnd.Intn(len(hand))] = c
				break
			}
		}
	}
	return hand
}

func TestRecognizeComplete(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20261016))
	levels := []Rank{Two, Five, Seven, Ten, King, Ace}

	start := time.Now()
	var hands, withWild, allWild, readings, wildReadings int
	byType := map[ComboType]int{}
	for i := 0; i < 8000; i++ {
		level := levels[rnd.Intn(len(levels))]
		var hand []Card
		if i%2 == 0 {
			hand = templateHand(rnd, deck, level)
		} else {
			hand = completenessHand(rnd, deck, level)
		}
		if len(hand) == 0 {
			continue // 牌型凑不出来（比如四大天王里的王被换掉后又凑不齐），跳过
		}

		combos, err := Recognize(hand, level)
		if err != nil {
			t.Fatalf("认牌型：%v", err)
		}
		got := map[string]bool{}
		for _, c := range combos {
			got[shapeKey(c.Type, c.Rank)] = true
		}
		want := naiveShapes(hand, level)

		var missing, extra []string
		for k := range want {
			if !got[k] {
				missing = append(missing, k)
			}
		}
		for k := range got {
			if !want[k] {
				extra = append(extra, k)
			}
		}
		if len(missing) > 0 || len(extra) > 0 {
			sort.Strings(missing)
			sort.Strings(extra)
			t.Fatalf("手牌 %s 打 %s：\n  Recognize 少了 %v\n  Recognize 多了 %v\n  Recognize 给的：%s\n  照规则应有：%v",
				handText(hand), level.RankName(), missing, extra, ComboSummary(combos), keysOf(want))
		}

		hands++
		readings += len(combos)
		for _, c := range combos {
			byType[c.Type]++
			if len(c.Wilds) > 0 && len(c.Wilds) < len(c.Cards) {
				wildReadings++ // 逢人配真的顶替了别的牌（不算「全是逢人配」那种）
			}
		}
		w := 0
		for _, c := range hand {
			if c.IsWild(level) {
				w++
			}
		}
		if w > 0 {
			withWild++
		}
		if w == len(hand) {
			allWild++
		}
	}
	t.Logf("%d 手牌（带逢人配 %d 手，全是逢人配 %d 手），共 %d 种解读（逢人配顶替的 %d 种），用时 %v",
		hands, withWild, allWild, readings, wildReadings, time.Since(start).Round(time.Millisecond))
	parts := []string{}
	for typ := TypeSingle; typ <= TypeJokerBomb; typ++ {
		parts = append(parts, fmt.Sprintf("%s %d", typ, byType[typ]))
	}
	t.Logf("各牌型验到的次数：%s", strings.Join(parts, "、"))

	// 随机牌要真的覆盖到要紧的情形，否则「全过」不说明什么：十种牌型每种都要验到几十次，
	// 逢人配真的顶替了别的牌的解读要上百，全是逢人配的手牌也要有。
	for typ := TypeSingle; typ <= TypeJokerBomb; typ++ {
		if byType[typ] < 20 {
			t.Fatalf("随机牌覆盖不够：%s 只验到 %d 次", typ, byType[typ])
		}
	}
	if wildReadings < 300 || allWild < 20 {
		t.Fatalf("随机牌覆盖不够：逢人配顶替的解读 %d 种，全是逢人配的手牌 %d 手", wildReadings, allWild)
	}
}

// 判定器自己的几条单测：手写的典型手牌，判定器必须给出规则上该有的那些解读。
// 判定器错了，上面的交叉验证就失去意义。
func TestNaiveShapesKnownHands(t *testing.T) {
	card := func(key string) Card {
		t.Helper()
		c, err := ParseKey(key) // 带上了它在整副牌里的 ID
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := []struct {
		name  string
		level Rank
		hand  []string
		want  []string
	}{
		{"单张逢人配只算级牌", Seven, []string{"♥7/0"}, []string{"single/7"}},
		{"两张逢人配只算一对级牌", Seven, []string{"♥7/0", "♥7/1"}, []string{"pair/7"}},
		{"一对大王是对子", Two, []string{"大王/0", "大王/1"}, []string{"pair/大王"}},
		{"一大一小王不是对子", Two, []string{"大王/0", "小王/0"}, nil},
		{"逢人配不能顶王", Seven, []string{"大王/0", "♥7/0"}, nil},
		{"A2345 顶张是 5", Seven, []string{"♠A/0", "♣2/0", "♦3/0", "♠4/0", "♣5/0"}, []string{"straight/5"}},
		{"JQKA2 不能首尾相接", Seven, []string{"♠J/0", "♣Q/0", "♦K/0", "♠A/0", "♣2/0"}, nil},
		{"同花的 34567 既是顺子也是同花顺", Two, []string{"♣3/0", "♣4/0", "♣5/0", "♣6/0", "♣7/0"},
			[]string{"straight-flush/7", "straight/7"}},
		{"梅花缺一张由逢人配补成同花顺", Two, []string{"♣3/0", "♣4/0", "♣6/0", "♣7/0", "♥2/0"},
			[]string{"straight-flush/7", "straight/7"}},
		{"999 加两张逢人配：炸弹或三带对", Seven, []string{"♠9/0", "♣9/0", "♦9/0", "♥7/0", "♥7/1"},
			[]string{"bomb/9", "triple-pair/9"}},
		{"AA2233 是三连对，顶张 3", Seven, []string{"♠A/0", "♣A/0", "♠2/0", "♣2/0", "♠3/0", "♣3/0"}, []string{"tube/3"}},
		{"四大天王", Seven, []string{"大王/0", "大王/1", "小王/0", "小王/1"}, []string{"joker-bomb"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hand := make([]Card, len(tc.hand))
			for i, k := range tc.hand {
				hand[i] = card(k)
			}
			got := keysOf(naiveShapes(hand, tc.level))
			want := append([]string{}, tc.want...)
			sort.Strings(want)
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("判定器给出 %v，规则上应该是 %v", got, want)
			}
		})
	}
}
