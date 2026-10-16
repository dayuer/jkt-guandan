package rules

import (
	"fmt"
	"sort"
	"strings"
)

// ComboType 是牌型（第三条 1–10）。
//
// 顺子类的牌型（三连对、三同连张、顺子、同花顺）里，级牌按牌面原位置算，不会因为「打几」
// 就变成顺子里最大的一张（第五条 1）；所以认牌型基本不看级数，级数只在逢人配和比大小里有用。
type ComboType int

const (
	TypeSingle         ComboType = iota // 单张（第三条 1）
	TypePair                            // 对子（第三条 2）
	TypeTriple                          // 三同张（第三条 4）
	TypeTripleWithPair                  // 三带对（第三条 6）
	TypeTube                            // 三连对，俗称木板（第三条 3）
	TypePlate                           // 三同连张，俗称钢板（第三条 5）
	TypeStraight                        // 顺子，正好 5 张（第三条 7）
	TypeStraightFlush                   // 同花顺（第三条 9）
	TypeBomb                            // 炸弹：四张及以上同点数（第三条 8）
	TypeJokerBomb                       // 四大天王：大小王各两张（第三条 10）
)

// String 是牌型的名字，牌例里也用它（combos.json 的 kind、compare.json 的说明）。
func (t ComboType) String() string {
	switch t {
	case TypeSingle:
		return "single"
	case TypePair:
		return "pair"
	case TypeTriple:
		return "triple"
	case TypeTripleWithPair:
		return "triple-pair"
	case TypeTube:
		return "tube"
	case TypePlate:
		return "plate"
	case TypeStraight:
		return "straight"
	case TypeStraightFlush:
		return "straight-flush"
	case TypeBomb:
		return "bomb"
	case TypeJokerBomb:
		return "joker-bomb"
	}
	return fmt.Sprintf("ComboType(%d)", int(t))
}

// ParseComboType 认牌型的名字（String 写出来的那些）。
func ParseComboType(s string) (ComboType, error) {
	for t := TypeSingle; t <= TypeJokerBomb; t++ {
		if t.String() == s {
			return t, nil
		}
	}
	return 0, fmt.Errorf("认不出牌型 %q", s)
}

// 牌型的中文名，报错和日志里用。
func (t ComboType) label() string {
	switch t {
	case TypeSingle:
		return "单张"
	case TypePair:
		return "对子"
	case TypeTriple:
		return "三同张"
	case TypeTripleWithPair:
		return "三带对"
	case TypeTube:
		return "三连对"
	case TypePlate:
		return "三同连张"
	case TypeStraight:
		return "顺子"
	case TypeStraightFlush:
		return "同花顺"
	case TypeBomb:
		return "炸弹"
	case TypeJokerBomb:
		return "四大天王"
	}
	return t.String()
}

// Combo 是一手牌的一种解读：这手牌能当成什么牌型打出去。
//
// 同一手牌可能有多种解读：同花色的 34567 既能当顺子跟牌，也能当同花顺压炸弹。所以出牌动作
// 要带上**声明的牌型**（#204、#87 的口径），引擎只校验声明成不成立，不替出牌的人挑一种。
type Combo struct {
	Type ComboType
	// Cards 是这手牌，按牌点从小到大排好（王在最后）。
	Cards []Card
	// Rank 是这手牌的牌面：单张、对子、三同张是那个点数；三带对是三同张的点数
	// （第四条二（一）2：不比较所带对子）；顺子类是顶张（A2345 的顶张是 5）；
	// 炸弹是点数；四大天王没有点数（0）。
	Rank Rank
	// Value 是比大小用的数值，只在同一牌型之间比（第四条二（一））。
	//
	// 两种算法，差别就在级牌上：
	//   - 单张、对子、三同张、三带对、炸弹：按牌点在当前级数下的大小（第四条一），
	//     所以打 7 的时候，单张 7 压单张 A；
	//   - 顺子类（三连对、三同连张、顺子、同花顺）：按牌面原位置算顶张（第五条 1），
	//     打 7 的时候 34567 的顶张还是 7，不会变成最大的一张。
	Value int
}

func (c Combo) String() string {
	if c.Type == TypeJokerBomb {
		return fmt.Sprintf("%s（%d 张）", c.Type.label(), len(c.Cards))
	}
	return fmt.Sprintf("%s %s（%d 张）", c.Type.label(), c.Rank.RankName(), len(c.Cards))
}

// IsBomb 判断这手牌算不算炸弹：炸弹、同花顺、四大天王都能压普通牌型（第四条二（二））。
func (c Combo) IsBomb() bool {
	switch c.Type {
	case TypeBomb, TypeStraightFlush, TypeJokerBomb:
		return true
	}
	return false
}

// bombTier 是炸弹之间的档次，越大越强（第四条二（二）：四大天王 ＞ 超过五张数的炸弹 ＞
// 同花顺 ＞ 五张数炸弹 ＞ 四张数炸弹）。非炸弹是 0。
func (c Combo) bombTier() int {
	switch {
	case c.Type == TypeJokerBomb:
		return 5
	case c.Type == TypeBomb && len(c.Cards) > 5:
		return 4
	case c.Type == TypeStraightFlush:
		return 3
	case c.Type == TypeBomb && len(c.Cards) == 5:
		return 2
	case c.Type == TypeBomb:
		return 1
	}
	return 0
}

// Beats 判断 a 能不能压 b（第四条二）。两手的解读要在同一个级数下认出来（Value 才可比）。
//
//   - 都不是炸弹：牌型和张数都得一样，再比 Value（第四条二（一）：三带对比三同张，其余比牌点）；
//   - 炸弹压任何普通牌型；普通牌型压不了炸弹；
//   - 都是炸弹：先比档次（四大天王 ＞ 超过五张数的炸弹 ＞ 同花顺 ＞ 五张数炸弹 ＞ 四张数炸弹），
//     同档再比：超过五张的比张数、张数相同比 Value，同花顺比顶张。
//
// 一样大时返回 false：接不下去就是接不下去，压不了自己。
func (a Combo) Beats(b Combo) bool {
	switch {
	case !a.IsBomb() && !b.IsBomb():
		return a.Type == b.Type && len(a.Cards) == len(b.Cards) && a.Value > b.Value
	case !a.IsBomb():
		return false
	case !b.IsBomb():
		return true
	}
	if ta, tb := a.bombTier(), b.bombTier(); ta != tb {
		return ta > tb
	}
	if a.Type != b.Type {
		return false // 同档次必定同牌型，这里是防守
	}
	if a.Type == TypeJokerBomb {
		return false // 四大天王只有一副，碰上了就是一样大
	}
	if len(a.Cards) != len(b.Cards) {
		return len(a.Cards) > len(b.Cards) // 张数多者大（第四条二（一）3）
	}
	return a.Value > b.Value
}

// isSeqType 判断是不是顺子类的牌型：这几种按牌面原位置比大小，不按级数（第五条 1）。
func isSeqType(t ComboType) bool {
	switch t {
	case TypeTube, TypePlate, TypeStraight, TypeStraightFlush:
		return true
	}
	return false
}

// Recognize 列出这手牌全部合法的解读。level 是当前级数（打几）。
//
// 一手牌可能有好几种打法，比如同花色的 34567 既是顺子又是同花顺；返回的顺序固定
// （先按牌型，再按牌点），两端（Go、TS）比对时不会因为顺序不同而不一致。
//
// 这手牌什么牌型都不是时返回空切片，不是错误 —— 调用方按「这手牌打不出去」处理。
// 空手牌或者同一张牌写了两次是调用方的错，返回错误。
//
// 逢人配（红桃级牌）的枚举是下一步的事：这一步只认实打实的牌。
func Recognize(cards []Card, level Rank) ([]Combo, error) {
	if len(cards) == 0 {
		return nil, fmt.Errorf("认牌型：没有牌")
	}
	if level < Two || level > Ace {
		return nil, fmt.Errorf("认牌型：级数 %d 超出 2–A", level)
	}
	if err := checkDistinctCards(cards); err != nil {
		return nil, err
	}
	sorted := sortCards(cards)
	counts := rankCounts(sorted) // 点数 → 张数
	distinct := make([]Rank, 0, len(counts))
	for r := range counts {
		distinct = append(distinct, r)
	}
	sort.Slice(distinct, func(i, j int) bool { return distinct[i] < distinct[j] })

	var out []Combo
	add := func(t ComboType, rank Rank) {
		value := rank.Power(level)
		if isSeqType(t) {
			value = int(rank) // 顺子类按牌面原位置算（第五条 1）
		}
		out = append(out, Combo{Type: t, Cards: sorted, Rank: rank, Value: value})
	}

	// 同点数的：单张、对子、三同张、炸弹（第三条 1、2、4、8）。
	if len(distinct) == 1 {
		switch n := len(sorted); {
		case n == 1:
			add(TypeSingle, distinct[0])
		case n == 2:
			add(TypePair, distinct[0])
		case n == 3:
			add(TypeTriple, distinct[0])
		case n >= 4 && n <= 10:
			add(TypeBomb, distinct[0])
		}
	}

	// 四大天王：大小王各两张（第三条 10）。它不是同点数的炸弹，单独认；没有点数可标。
	if len(sorted) == 4 && counts[BigJoker] == 2 && counts[SmallJoker] == 2 {
		add(TypeJokerBomb, 0)
	}

	// 三带对：一个三同张带一个对子（第三条 6）。
	if len(sorted) == 5 && len(distinct) == 2 {
		for _, r := range distinct {
			if counts[r] == 3 {
				add(TypeTripleWithPair, r)
			}
		}
	}

	// 三连对：三个牌点相邻的对子（第三条 3）。
	if len(sorted) == 6 && len(distinct) == 3 && allCounts(counts, 2) {
		if top, ok := seqTop(distinct, 3); ok {
			add(TypeTube, top)
		}
	}

	// 三同连张：两个牌点相邻的三同张（第三条 5）。
	if len(sorted) == 6 && len(distinct) == 2 && allCounts(counts, 3) {
		if top, ok := seqTop(distinct, 2); ok {
			add(TypePlate, top)
		}
	}

	// 顺子：五个牌点相邻的单张，花色不限（第三条 7）。王不参与。
	if len(sorted) == 5 && len(distinct) == 5 && allCounts(counts, 1) {
		if top, ok := seqTop(distinct, 5); ok {
			add(TypeStraight, top)
			// 同一个顺子还是同一个花色时，它也是同花顺（第三条 9）：两种解读都给出来，
			// 出牌的人自己声明打哪一种 —— 当顺子能跟牌，当同花顺能压炸弹。
			if sameSuit(sorted) {
				add(TypeStraightFlush, top)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Rank < out[j].Rank
	})
	return out, nil
}

// FindCombo 在这手牌的全部解读里找声明的那一种：找到了返回它，没有就是这手牌打不出这个牌型。
func FindCombo(cards []Card, level Rank, t ComboType) (Combo, bool, error) {
	combos, err := Recognize(cards, level)
	if err != nil {
		return Combo{}, false, err
	}
	for _, c := range combos {
		if c.Type == t {
			return c, true, nil
		}
	}
	return Combo{}, false, nil
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func checkDistinctCards(cards []Card) error {
	seen := map[int]bool{}
	for _, c := range cards {
		if seen[c.ID] {
			return fmt.Errorf("认牌型：%s 出现了两次", c.Key())
		}
		seen[c.ID] = true
	}
	return nil
}

func sortCards(cards []Card) []Card {
	out := append([]Card{}, cards...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		if out[i].Suit != out[j].Suit {
			return out[i].Suit < out[j].Suit
		}
		return out[i].Deck < out[j].Deck
	})
	return out
}

func rankCounts(cards []Card) map[Rank]int {
	counts := map[Rank]int{}
	for _, c := range cards {
		counts[c.Rank]++
	}
	return counts
}

func allCounts(counts map[Rank]int, want int) bool {
	for _, n := range counts {
		if n != want {
			return false
		}
	}
	return true
}

func sameSuit(cards []Card) bool {
	for _, c := range cards[1:] {
		if c.Suit != cards[0].Suit {
			return false
		}
	}
	return true
}

// seqTop 判断这组点数能不能排成 count 个连着的牌点，返回顶张的牌面。
//
// A 在顺子类牌型里可以当最小的 1 用（A2345、AA2233），也可以当最大的 14 用（10JQKA、
// QQKKAA），但不能两头都接（第四条二（二）2）。王不参与顺子类牌型。
func seqTop(ranks []Rank, count int) (Rank, bool) {
	if len(ranks) != count {
		return 0, false
	}
	for _, r := range ranks {
		if r.IsJoker() {
			return 0, false
		}
	}
	// 两种读法：A 当 14，或者 A 当 1。王已经排除了，剩下的都是 2–14。
	for _, aceLow := range []bool{false, true} {
		values := make([]int, 0, len(ranks))
		hasAce := false
		for _, r := range ranks {
			if r == Ace {
				hasAce = true
				if aceLow {
					values = append(values, 1)
					continue
				}
			}
			values = append(values, int(r))
		}
		if aceLow && !hasAce {
			continue // 没 A 的话两种读法一样，别重复判
		}
		sort.Ints(values)
		ok := true
		for i := 1; i < len(values); i++ {
			if values[i] != values[i-1]+1 {
				ok = false
				break
			}
		}
		if ok {
			return Rank(values[len(values)-1]), true
		}
	}
	return 0, false
}

// ComboSummary 把一组解读写成给人看的一行（日志、报错、测试消息里用）。
func ComboSummary(combos []Combo) string {
	if len(combos) == 0 {
		return "打不出去"
	}
	parts := make([]string, len(combos))
	for i, c := range combos {
		parts[i] = c.String()
	}
	return strings.Join(parts, "、")
}
