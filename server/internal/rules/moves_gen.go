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

// candidate 是一个候选牌组：打出去的这几张牌，外加**每张逢人配顶成了什么**。
//
// 为什么要把顶法一路带出来（#241）：牌组是按槽位挑的，挑的时候就已经知道每张逢人配
// 补的是哪个点位，顶法其实是**定死的**。如果只把牌组交给 Recognize，它会再把所有顶法
// 试一遍、挑最小的那一种 —— 两张逢人配就是 52² 种试法，领出一次上百个候选，
// 一个候选就白试几万次（实测领出 167ms，见 moves_bench_test.go 的基准）。
// 带上顶法就能直接照这一种认，identified 的顶法也一样（顶法由槽位决定，本来就只有一种）。
//
// known 为 false 表示「这张候选的顶法不是按槽位定的」：手里全是逢人配时的那一两张候选，
// 口径是「按它本来的点数算」（见 Recognize），交给 Recognize 定。
type candidate struct {
	cards []Card     // 打出去的牌（实牌 + 逢人配）
	subs  []Wildcard // 每张逢人配顶成的那张牌（按牌 ID 排，和 Recognize 给出的顺序一致）
	known bool       // true 表示 subs 是确定的，可以直接照它认
	suit  *Suit      // 同花顺候选带的花色（其余牌型不挑花色）；见 combosOf 里的用法
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
func candidateSets(solids, wilds []Card, want shapeMask) []candidate {
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
	var out []candidate
	for _, w := range wilds {
		out = append(out, candidate{cards: []Card{w}}) // 顶法按它本来的点数算，交给 Recognize
	}
	if len(wilds) >= 2 {
		out = append(out, candidate{cards: []Card{wilds[0], wilds[1]}})
	}
	if len(ranks) == 0 {
		return out
	}

	// 同点数那几种（单张、对子、三同张、炸弹、三带对）：逢人配可以顶成这个点数、
	// 也可以顶成另一个点数 —— 每一种顶法都是不同的打法，全枚举。
	//
	// 顺子类那几种（三连对、三同连张、顺子）用的是同一个做法：按「连续点数各一张/两张」
	// 摆好槽位，逢人配补到**哪个槽位**都是不同的打法 —— 补在有实牌的点位上也合法，
	// 那张实牌不用就是了（README「逢人配」第 1 条）。
	// free 是「哪条槽位的点数不出现在牌型里」的下标（只有三带对的副对是这样，其余传 -1）：
	// 那条槽位如果整条都用逢人配补出来，顶成哪个点数都成，顶法就不是唯一一种了（见 slotVariants）。
	add := func(free int, slots ...slot) {
		out = append(out, slotVariants(slots, byRank, wilds, nil, free)...)
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
				add(-1, slot{r, s.n})
			}
		}
		if want[TypeBomb] {
			for n := 4; n <= 10; n++ {
				add(-1, slot{r, n})
			}
		}
	}

	// 四大天王：大小王各两张。逢人配顶不了王，这里不试「用逢人配」那种取法。
	if want[TypeJokerBomb] {
		// 逢人配顶不了王（第二条 14），所以这里不试「用逢人配」那种取法，顶法自然是空的。
		if set, _, ok := fillSlots([]slot{{BigJoker, 2}, {SmallJoker, 2}}, byRank, nil, []int{0, 0}, nil); ok {
			out = append(out, candidate{cards: set, known: true})
		}
	}

	// 三带对：一个三同张 + 另一个点数的对子。
	if want[TypeTripleWithPair] {
		for _, a := range ranks {
			for _, b := range ranks {
				if a != b {
					add(1, slot{a, 3}, slot{b, 2})
				}
			}
		}
	}

	// 三连对、三同连张、顺子、同花顺：按窗口挑。
	if want[TypeTube] {
		for _, w := range rankWindows(3) {
			add(-1, slot{w[0], 2}, slot{w[1], 2}, slot{w[2], 2})
		}
	}
	if want[TypePlate] {
		for _, w := range rankWindows(2) {
			add(-1, slot{w[0], 3}, slot{w[1], 3})
		}
	}
	if want[TypeStraight] || want[TypeStraightFlush] {
		for _, w := range rankWindows(5) {
			slots := []slot{{w[0], 1}, {w[1], 1}, {w[2], 1}, {w[3], 1}, {w[4], 1}}
			if want[TypeStraight] {
				add(-1, slots...)
			}
			// 同花顺：同一个窗口，四个花色各挑一遍。
			if want[TypeStraightFlush] {
				for _, suit := range []Suit{Spade, Heart, Club, Diamond} {
					sc := suit
					out = append(out, slotVariants(slots, byRank, wilds, &sc, -1)...)
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
func slotVariants(slots []slot, byRank map[Rank][]Card, wilds []Card, suit *Suit, freeSlot int) []candidate {
	var out []candidate
	for mask := 0; mask < 1<<len(wilds); mask++ {
		var chosen []Card
		for i, w := range wilds {
			if mask&(1<<i) != 0 {
				chosen = append(chosen, w)
			}
		}
		for _, counts := range assignments(len(chosen), slots) {
			if set, subs, ok := fillSlots(slots, byRank, chosen, counts, suit); ok {
				cand := candidate{cards: set, subs: subs, known: true, suit: suit}
				// 三带对的副对整条都用逢人配补：那张逢人配顶成 2、3……哪个点数都成
				// （牌型只认「三同张的点数」，副对的点数不影响它）—— 顶法不唯一，
				// 得让 Recognize 按「顶法最小」挑一种（顶成 2 的那一种），不能照槽位点数算。
				if freeSlot >= 0 && counts[freeSlot] == slots[freeSlot].n {
					cand.known = false
				}
				out = append(out, cand)
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
//
// 除了牌组，还给出**每张逢人配顶成了什么**：它补在哪个槽位上，就顶成那个点数的牌。
// 花色只有同花顺才管（那时必须是这个花色），其余牌型按最小花色取 —— 和 Recognize 的
// lessWilds 一个口径（同一个解读留顶法最小的那一种），所以两端和暴力枚举对拍都对得上。
func fillSlots(slots []slot, byRank map[Rank][]Card, wilds []Card, counts []int, suit *Suit) ([]Card, []Wildcard, bool) {
	used := map[int]bool{}
	out := make([]Card, 0, 10)
	usedWilds := make([]Card, 0, len(wilds))
	asList := make([]Card, 0, len(wilds))
	next := 0 // 下一张还没用的逢人配
	for i, sl := range slots {
		need := sl.n
		// 逢人配顶不了王（第二条 14）：王那两个点位只能用实打实的王来凑。
		// 少了这条，「一张小王 + 一张逢人配」会被当成一对小王 —— 正是把顶法直接算出来的
		// 时候最容易漏的一处（Recognize 是从「能顶成哪些牌」那张表里挑的，天然不含王）。
		if counts[i] > 0 && sl.rank.IsJoker() {
			return nil, nil, false
		}
		for k := 0; k < counts[i]; k++ {
			if next >= len(wilds) {
				return nil, nil, false
			}
			w := wilds[next]
			next++
			used[w.ID] = true
			out = append(out, w)
			as := Card{Rank: sl.rank, Suit: Spade, Deck: w.Deck, ID: w.ID}
			if suit != nil {
				as.Suit = *suit // 同花顺：顶出来的那张必须是这个花色
			}
			usedWilds = append(usedWilds, w)
			asList = append(asList, as)
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
			return nil, nil, false // 这个点数凑不齐（实牌不够，逢人配也用完了）
		}
	}
	if next != len(wilds) {
		return nil, nil, false // 还有逢人配没地方放
	}
	// 顶法按「最小的那一种」配对：**顶成的牌升序**（点数、花色，和 Recognize 的 lessWilds
	// 一个口径）配**逢人配按牌 ID 升序**。
	//
	// 为什么不是直接按槽位顺序记：同一个打法里几张逢人配互换顶法还是同一手（用的牌
	// 一模一样），Recognize 会枚举全部顶法、留最小的那一种 —— 也就是顶成的牌升序的那种。
	// 按槽位顺序记会得到「同一手、顶法排列不同」的另一种写法：牌例按集合比所以看不出来，
	// 但把新老两份实现摆在一起逐行对拍会全都对不上（#241 就是这么发现的）。
	sort.Slice(usedWilds, func(i, j int) bool { return usedWilds[i].ID < usedWilds[j].ID })
	sort.Slice(asList, func(i, j int) bool {
		if asList[i].Rank != asList[j].Rank {
			return asList[i].Rank < asList[j].Rank
		}
		return asList[i].Suit < asList[j].Suit
	})
	subs := make([]Wildcard, len(usedWilds))
	for i := range usedWilds {
		subs[i] = Wildcard{Card: usedWilds[i], As: asList[i]}
	}
	return sortCards(out), subs, true
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
