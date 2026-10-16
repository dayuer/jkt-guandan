package rules

import (
	"fmt"
	"sort"
)

// 合法出法（#204 第 4 步）。练习局由服务端主持（#88），每一步都要回答两个问题：
//
//   - **领出**：手里的牌能打出哪些牌型？（第三条 1–10 的全部合法解读）
//   - **跟牌**：上一手是谁的什么牌，我能不能压住？能压住的都有哪些打法？
//
// 这里的实现是「按牌型造候选 → 交给 Recognize 认一遍」：先按牌型（单张、对子、三同张、
// 三带对、三连对、三同连张、顺子、同花顺、炸弹、四大天王）在手里挑出**可能的牌组**，
// 每个候选都是一小组具体的牌（≤ 10 张），再交给 Recognize 得出解读（含逢人配的顶法枚举）。
//
// 为什么不直接枚举子集：27 张手牌的子集有 2^27 个，光是 5 张以内的组合就有十几万，
// 每题都跑一遍不现实。按牌型造候选把规模压到几百个（见 moves_test.go 的耗时上限那条）。

// Moves 列出一手牌的合法出法。
//
// lead 为 nil 表示领出（列出全部能打的牌型）；非 nil 表示跟牌，只留能压住 lead 的
// （第四条 二：同牌型同张数比大小；炸弹、同花顺、四大天王压普通牌型；炸弹之间按档次和张数）。
//
// 每一手都是**具体的牌**（Cards 是打出去的牌、Wilds 是逢人配顶成了什么），调用方拿起来
// 就能打出去。同一个（牌型、牌面、顶法）只留一套牌 —— 取牌 ID 最小的那一套，
// 所以两端的集合能逐条比（见 testdata/rules/moves.json）。
//
// 「要不起就过」不在这个集合里：跟牌时过牌永远合法，由调用方自己加。
func Moves(hand []Card, level Rank, lead *Combo) ([]Combo, error) {
	if len(hand) == 0 {
		return nil, fmt.Errorf("求合法出法：手里没有牌")
	}
	if level < Two || level > Ace {
		return nil, fmt.Errorf("求合法出法：级数 %d 超出 2–A", level)
	}
	if err := checkDistinctCards(hand); err != nil {
		return nil, err
	}
	if lead != nil && len(lead.Cards) == 0 {
		return nil, fmt.Errorf("求合法出法：上一手没有牌")
	}

	played := sortCards(hand)
	wilds, solids := splitWilds(played, level)

	// 跟牌时只造「压得住上一手」的牌型：要不起的牌型不用生成（跟牌是最常见的路径）。
	want := allShapes()
	if lead != nil {
		want = shapesBeating(*lead)
	}

	// 每个（牌型、牌面、顶法）只留牌 ID 最小的那一套。
	best := map[string]Combo{}
	for _, cand := range candidateSets(solids, wilds, want) {
		combos, err := combosOf(cand, level)
		if err != nil {
			return nil, fmt.Errorf("求合法出法：候选 %s：%w", cardsText(cand.cards), err)
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
		if lead != nil && c.Compare(*lead) != Greater {
			continue
		}
		out = append(out, c)
	}
	return sortCombos2(out), nil
}

// combosOf 把一个候选认成解读（#241：这条路是热点，别再让 Recognize 枚举顶法）。
//
// 顶法已知的候选**直接照它的顶法认**：把逢人配换成它顶成的那张牌，再调 recognizeCards ——
// 和 Recognize 内部那条路一模一样，只是省掉了「把所有顶法试一遍、每种都认一遍、再挑最小的」
// 那一步。那一步对两张逢人配就是 52² 种试法，而领出一次要跑上百个候选（基准里 167ms 就是它）。
//
// 口径不变：顶法由槽位决定，本来就只有一种；同花顺的花色由槽位那边带过来，
// 其余的按最小花色（和 Recognize 的 lessWilds 同一口径）。
//
// 「手里全是逢人配」的那一两张候选（逢人配本身的点数怎么算由 Recognize 定）交给 Recognize。
func combosOf(cand candidate, level Rank) ([]Combo, error) {
	if !cand.known {
		return Recognize(cand.cards, level)
	}
	played := sortCards(cand.cards)
	_, solids := splitWilds(played, level)
	// 候选里全是逢人配（一张单出、两张一起出）：口径是「只按它本来的点数算」，
	// 不枚举顶成 2–A 里的哪一张（第二条 14、第五条 2，见 Recognize 里那段）。
	// 按槽位造出来的候选也会落到这里（比如「用两张逢人配凑一对 2」那种取法），
	// 它们的口径和「两张逢人配就是一对级牌」是同一个，交给 Recognize 定。
	if len(solids) == 0 {
		return Recognize(cand.cards, level)
	}
	// 和 Recognize 一样的剪枝：实牌有 6 个以上点数，怎么顶都凑不成任何一种牌型。
	if len(rankCounts(solids)) > 5 {
		return nil, nil
	}
	trial := make([]Card, 0, len(played))
	trial = append(trial, solids...)
	for _, w := range cand.subs {
		trial = append(trial, w.As)
	}
	out := make([]Combo, 0, 2)
	for _, sh := range recognizeCards(trial) {
		// 同花顺候选（挑过花色的那种）只留同花顺：这一手牌如果还能当普通顺子打，
		// 那个解读的顶法里花色该是最小的（♠，见 Recognize 的 lessWilds），
		// 而这里的逢人配被顶成了这个花色 —— 那条「顺子」另有一个不挑花色的候选会给出
		// （顶法才是对的），这里丢掉，免得同一个牌型多出一条顶法不同的条目。
		if cand.suit != nil && sh.typ != TypeStraightFlush {
			continue
		}
		out = append(out, Combo{
			Type: sh.typ, Cards: played, Wilds: cand.subs, Rank: sh.rank,
			Value: comboValue(sh.typ, sh.rank, level),
		})
	}
	return out, nil
}

// splitWilds 把手牌分成逢人配和实打实的牌（和 Recognize 里同一套切法）。
func splitWilds(cards []Card, level Rank) (wilds, solids []Card) {
	for _, c := range cards {
		if c.IsWild(level) {
			wilds = append(wilds, c)
			continue
		}
		solids = append(solids, c)
	}
	return wilds, solids
}

// comboKey 是「同一个打法」的判据：牌型 + 牌面 + **张数** + 顶法（不看具体是哪几张牌）。
//
// 张数也要算进去：炸弹 4 张、5 张、7 张是**三种打法**（比大小的档次都不一样），
// 少了这一项它们会被当成同一条、只留下最小的那种 —— 手里七个 3 就只剩「4 张炸弹」，
// 跟别人 5 张炸弹的时候明明有 6 张、7 张能压，却报「无牌可跟」（#204 第 4 步踩过）。
// 顶法那一项**不比副号**（用 `asKey` 而不是 `w.Card.ID`）：手里两张逢人配 ♥7/0、♥7/1
// 单出一张都是「单张 7」，按 README 的比对规矩（顶法只比点数花色、逢人配用哪一张算
// 「用哪几张牌」）这两条相等，就该只列一条 —— 用 ID 会并列两条（jkt-2 在 TS 端先撞上的）。
//
// 片段排一下序：键的定义不该依赖生成器枚举逢人配的内部顺序。现在生成器本来就是按槽位顺序
// 给的，所以**没有用例能区分有没有这行**（去掉它全部测试照样绿）—— 留着是兜底，不是为了
// 挡某个已复现的 bug，写清楚免得有人以为是死代码或者反过来以为它有牌例守着。
func comboKey(c Combo) string {
	key := fmt.Sprintf("%s/%s/%d", c.Type.String(), c.Rank.RankName(), len(c.Cards))
	if len(c.Wilds) == 0 {
		return key
	}
	parts := make([]string, 0, len(c.Wilds))
	for _, w := range c.Wilds {
		parts = append(parts, asKey(w.As)+"←"+asKey(w.Card))
	}
	sort.Strings(parts)
	for _, p := range parts {
		key += "/" + p
	}
	return key
}

// lessCards 比两套牌，用来在同一个打法里挑「牌 ID 最小的那一套」。
//
// ⚠️ 比的是**按 ID 排好之后**的列表，不是 Cards 原本的顺序（那是按点数、花色排的）：
// 不然「最小的那一套」会取决于排序习惯 —— 例如同样是对子 Q，♣Q/0+♥Q/1（ID 37、78）
// 和 ♣Q/0+♣Q/1（ID 37、91）哪个小，两种排法给出的答案不一样。
func lessCards(a, b []Card) bool {
	ai, bi := idsAsc(a), idsAsc(b)
	for i := range ai {
		if i >= len(bi) {
			return false
		}
		if ai[i] != bi[i] {
			return ai[i] < bi[i]
		}
	}
	return len(ai) < len(bi)
}

// idsAsc 把一套牌的 ID 从小到大排出来。
func idsAsc(cards []Card) []int {
	ids := make([]int, len(cards))
	for i, c := range cards {
		ids[i] = c.ID
	}
	sort.Ints(ids)
	return ids
}

// cardsText 把一组牌写成一行，报错用。
func cardsText(cards []Card) string {
	out := ""
	for i, c := range cards {
		if i > 0 {
			out += " "
		}
		out += c.Key()
	}
	return out
}

// sortCombos2 排出确定的顺序：先牌型、再牌面，然后按顶法，最后按牌 ID。
// （`sortCombos` 只管前两层，这里是它的加强版；两端比对不能靠 map 的遍历顺序。）
func sortCombos2(combos []Combo) []Combo {
	sort.SliceStable(combos, func(i, j int) bool {
		a, b := combos[i], combos[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Rank != b.Rank {
			return a.Rank < b.Rank
		}
		// **张数**排在顶法前面（格式说明：牌型 → 牌面 → 张数 → 顶法）：炸弹 4、5、6 张是
		// 三个不同的档次，少了这一项就会把「4 张带逢人配」排到「5 张实牌」前面去。
		if len(a.Cards) != len(b.Cards) {
			return len(a.Cards) < len(b.Cards)
		}
		if len(a.Wilds) != len(b.Wilds) {
			return len(a.Wilds) < len(b.Wilds)
		}
		for k := range a.Wilds {
			if a.Wilds[k].As.Rank != b.Wilds[k].As.Rank {
				return a.Wilds[k].As.Rank < b.Wilds[k].As.Rank
			}
			if a.Wilds[k].As.Suit != b.Wilds[k].As.Suit {
				return a.Wilds[k].As.Suit < b.Wilds[k].As.Suit
			}
			if a.Wilds[k].Card.ID != b.Wilds[k].Card.ID {
				return a.Wilds[k].Card.ID < b.Wilds[k].Card.ID
			}
		}
		return lessCards(a.Cards, b.Cards)
	})
	return combos
}
