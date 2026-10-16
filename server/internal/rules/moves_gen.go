package rules

import "sort"

// 按牌型造候选牌组（#204 第 4 步）。每个候选都是「手里可能打得出去的一小组牌」，
// 交给 Recognize 认成解读 —— 这里只负责**挑牌**，不判合不合法（那是 Recognize 的事）。
//
// 逐牌型的槽位（第三条 1–10）：
//
//	单张   [X,1] × 手里每个点数
//	对子   [X,2]
//	三同张 [X,3]
//	炸弹   [X,n] n=4..10（同一点数最多 8 张 + 2 张逢人配）
//	三带对 [A,3] + [B,2]，A≠B
//	三连对 [X,2]+[X+1,2]+[X+2,2]（含 A 当 1 的绕圈窗口）
//	三同连张 [X,3]+[X+1,3]
//	顺子   [X,1]…[X+4,1]
//	同花顺 同上，但只从同一个花色里挑
//	四大天王 大小王各两张（逢人配顶不了王，所以只从实打实的牌里挑）
//
// 一组槽位不止一种挑法：同样的 [9,2]（凑一对 9），可以两张实牌，也可以一张实牌 + 一张逢人配 ——
// 后者是**另一种打法**（用了逢人配），两头都要给出来。所以这里把逢人配的**每一种取用方式**
// 都试一遍（逢人配最多两张 → 至多 4 种），每种里再「先用手里的实牌、不够的用逢人配补」。
//
// 为什么不直接枚举子集：27 张手牌的子集有 2^27 个，光是 5 张以内的组合就有十几万，
// 每走一步都跑一遍不现实。按牌型造候选把规模压到几百个（见 moves_test.go 的耗时上限那条）。

type slot struct {
	rank Rank
	n    int
}

// shapeMask 是「要生成哪些牌型」的开关。领出时全开；跟牌时只开需要的那些 ——
// 要不起的牌型没必要造（见 shapesBeating）：这一下省掉一大半工作，
// 而且是**跟牌这条最常见的路径**。
type shapeMask map[ComboType]bool

func allShapes() shapeMask {
	m := shapeMask{}
	for t := TypeSingle; t <= TypeJokerBomb; t++ {
		m[t] = true
	}
	return m
}

// shapesBeating 是「能压住 lead 的牌型」：同牌型（跟牌必须同牌型同张数，或者炸弹），
// 加上炸弹、同花顺、四大天王（第四条 二、（二））。跟牌时只生成这些。
func shapesBeating(lead Combo) shapeMask {
	m := shapeMask{TypeBomb: true, TypeStraightFlush: true, TypeJokerBomb: true}
	if !lead.IsBomb() {
		m[lead.Type] = true
	}
	return m
}

// candidateSets 造出候选牌组；want 决定生成哪些牌型。
func candidateSets(solids, wilds []Card, want shapeMask) [][]Card {
	byRank := map[Rank][]Card{}
	for _, c := range solids {
		byRank[c.Rank] = append(byRank[c.Rank], c)
	}
	// 同一个点数的实牌按 **ID** 排（不是按花色）—— 挑牌时取 ID 最小的那几张，
	// 这样「同一个打法留哪一套牌」和 Moves 的说明一致，也和暴力枚举的结果一样。
	for r := range byRank {
		byID := byRank[r]
		sort.Slice(byID, func(i, j int) bool { return byID[i].ID < byID[j].ID })
	}
	ranks := make([]Rank, 0, len(byRank))
	for r := range byRank {
		ranks = append(ranks, r)
	}
	sort.Slice(ranks, func(i, j int) bool { return ranks[i] < ranks[j] })

	// 逢人配自己也是牌：一张单出、两张一起出（只按它本来的点数算 —— 和 Recognize 一个口径）。
	// 手里只有逢人配、没有实牌时，候选就这些。
	var out [][]Card
	for _, w := range wilds {
		out = append(out, []Card{w})
	}
	if len(wilds) >= 2 {
		out = append(out, []Card{wilds[0], wilds[1]})
	}
	if len(ranks) == 0 {
		return out
	}

	// 同点数那几种（单张、对子、三同张、炸弹、三带对）：逢人配可以顶成这个点数、
	// 也可以顶成另一个点数 —— 每一种顶法都是不同的打法，全枚举。
	addSameRank := func(slots ...slot) {
		out = append(out, slotVariants(slots, byRank, wilds, nil)...)
	}
	// 顺子类那几种：牌型要求**连续的点数各一张/两张**，逢人配只能补在缺的那个点位上
	// （摆到已经有实牌的点位上只会多出一张重复的点数，反而不成牌型）。
	addSequence := func(slots ...slot) {
		out = append(out, slotVariants(slots, byRank, wilds, nil)...)
	}

	// 单张、对子、三同张、炸弹（含用逢人配补齐的同点数牌）。
	for _, r := range ranks {
		// ⚠️ 别拿 ComboType 的 iota 当张数用：Single=0、Pair=1、Triple=2，
		// 和「单张 1 张、对子 2 张」正好差一（#204 第 4 步自己踩过）。
		for _, s := range []struct {
			t ComboType
			n int
		}{{TypeSingle, 1}, {TypePair, 2}, {TypeTriple, 3}} {
			if want[s.t] {
				addSameRank(slot{r, s.n})
			}
		}
		if want[TypeBomb] {
			for n := 4; n <= 10; n++ {
				addSameRank(slot{r, n})
			}
		}
	}

	// 四大天王：大小王各两张。逢人配顶不了王，这里不试「用逢人配」那种取法。
	if want[TypeJokerBomb] {
		if set, ok := fillSlots([]slot{{BigJoker, 2}, {SmallJoker, 2}}, byRank, nil, []int{0, 0}, nil); ok {
			out = append(out, set)
		}
	}

	// 三带对：一个三同张 + 另一个点数的对子。
	if want[TypeTripleWithPair] {
		for _, a := range ranks {
			for _, b := range ranks {
				if a != b {
					addSameRank(slot{a, 3}, slot{b, 2})
				}
			}
		}
	}

	// 三连对、三同连张、顺子、同花顺：按窗口挑。
	if want[TypeTube] {
		for _, w := range rankWindows(3) {
			addSequence(slot{w[0], 2}, slot{w[1], 2}, slot{w[2], 2})
		}
	}
	if want[TypePlate] {
		for _, w := range rankWindows(2) {
			addSequence(slot{w[0], 3}, slot{w[1], 3})
		}
	}
	if want[TypeStraight] || want[TypeStraightFlush] {
		for _, w := range rankWindows(5) {
			slots := []slot{{w[0], 1}, {w[1], 1}, {w[2], 1}, {w[3], 1}, {w[4], 1}}
			if want[TypeStraight] {
				addSequence(slots...)
			}
			// 同花顺：同一个窗口，四个花色各挑一遍。
			if want[TypeStraightFlush] {
				for _, suit := range []Suit{Spade, Heart, Club, Diamond} {
					sc := suit
					out = append(out, slotVariants(slots, byRank, wilds, &sc)...)
				}
			}
		}
	}
	return out
}

// slotVariants 枚举「同点数」牌型的每一种打法：逢人配的每个子集 × 每张逢人配补到哪个槽位。
//
// 为什么要把「用几张逢人配、补到哪个槽位」都枚举一遍：同样凑一对 9，可以是两张实牌 9，
// 也可以是「一张 9 + 一张逢人配当 9」；三带对更是可以「逢人配当三同张那一档」或者
// 「当对子那一档」—— 这些都是**不同的打法**（用的牌不同、Wilds 也不同），牌例里要逐条列出来。
//
// 槽位最多 2 个（三带对）、逢人配最多 2 张，所以规模很小。
func slotVariants(slots []slot, byRank map[Rank][]Card, wilds []Card, suit *Suit) [][]Card {
	var out [][]Card
	for mask := 0; mask < 1<<len(wilds); mask++ {
		var chosen []Card
		for i, w := range wilds {
			if mask&(1<<i) != 0 {
				chosen = append(chosen, w)
			}
		}
		for _, counts := range assignments(len(chosen), slots) {
			if set, ok := fillSlots(slots, byRank, chosen, counts, suit); ok {
				out = append(out, set)
			}
		}
	}
	return out
}

// assignments 把 n 张逢人配分到每个槽位（每槽最多和它要的张数一样多），列出全部分法。
func assignments(n int, slots []slot) [][]int {
	if len(slots) == 0 {
		if n == 0 {
			return [][]int{{}}
		}
		return nil
	}
	var out [][]int
	head := slots[0]
	for k := 0; k <= n && k <= head.n; k++ {
		for _, rest := range assignments(n-k, slots[1:]) {
			out = append(out, append([]int{k}, rest...))
		}
	}
	return out
}

// fillSlots 按槽位挑牌：第 i 个槽位先放 counts[i] 张给定的逢人配（它们是这次打法点名要用的），
// 剩下的用手里这个点数的实牌补（按 ID 从小到大、够用就停）。同一张牌不能用两次；
// 凑不齐返回 false；counts 里的逢人配没用完也算凑不齐（那是另一种打法，由别的 counts 表示）。
func fillSlots(slots []slot, byRank map[Rank][]Card, wilds []Card, counts []int, suit *Suit) ([]Card, bool) {
	used := map[int]bool{}
	out := make([]Card, 0, 10)
	next := 0 // 下一张还没用的逢人配
	for i, sl := range slots {
		need := sl.n
		for k := 0; k < counts[i]; k++ {
			if next >= len(wilds) {
				return nil, false
			}
			w := wilds[next]
			next++
			used[w.ID] = true
			out = append(out, w)
			need--
		}
		for _, c := range byRank[sl.rank] {
			if need == 0 {
				break
			}
			if used[c.ID] {
				continue
			}
			if suit != nil && c.Suit != *suit {
				continue
			}
			used[c.ID] = true
			out = append(out, c)
			need--
		}
		if need > 0 {
			return nil, false // 这个点数凑不齐（实牌不够，逢人配也用完了）
		}
	}
	if next != len(wilds) {
		return nil, false // 还有逢人配没地方放
	}
	return sortCards(out), true
}

// rankWindows 生成 count 个连续点数的窗口（第三条 7 那些），含「A 当 1」绕一圈的：
// count=5 时是 23456…10JQKA 共 10 个，再加上 A2345；count=3 时是 234…QKA 再加上 A23。
func rankWindows(count int) [][]Rank {
	var out [][]Rank
	for start := Two; int(start)+count-1 <= int(Ace); start++ {
		w := make([]Rank, 0, count)
		for i := 0; i < count; i++ {
			w = append(w, start+Rank(i))
		}
		out = append(out, w)
	}
	// A 当 1：A + 2…count（count=2 时是 A2 —— 三同连张 AA222 在规则里是有的）。
	if count >= 2 {
		w := make([]Rank, 0, count)
		w = append(w, Ace)
		for v := Two; v <= Rank(count); v++ {
			w = append(w, v)
		}
		out = append(out, w)
	}
	return out
}
