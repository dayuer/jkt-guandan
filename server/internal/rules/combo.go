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

// String 是牌型的名字，牌例里也用它（combos.json 的 kind、compare.json 的 kind）。
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

// label 是牌型的中文名，报错和日志里用。
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

// Wildcard 是逢人配顶替的那张牌：手里的红桃级牌当成了什么用（「♥7 当 ♠A」）。
// 给界面显示和复盘看；比大小不看它，看 Combo.Rank / Value。
type Wildcard struct {
	Card Card // 手里的那张红桃级牌
	As   Card // 它顶的那张牌（不含大小王）
}

// Combo 是一手牌的一种解读：这手牌能当成什么牌型、什么牌面打出去。
//
// 同一手牌可能有多种解读：同花色的 34567 既能当顺子跟牌，也能当同花顺压炸弹；带逢人配时
// 解读更多（单张红桃级牌能当 2 到 A 任何一张）。所以出牌动作要带上**声明的解读**，引擎只校验
// 声明成不成立，不替出牌的人挑一种（#204、#87 的口径）。
type Combo struct {
	Type ComboType
	// Cards 是打出去的牌，按牌点从小到大排好（王在最后）。
	Cards []Card
	// Wilds 是这批牌里的逢人配各顶了什么（没有逢人配时是空的）。
	Wilds []Wildcard
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
	//     打 7 的时候 34567 的顶张还是 7。
	Value int
}

func (c Combo) String() string {
	s := fmt.Sprintf("%s（%d 张）", c.Type.label(), len(c.Cards))
	if c.Type != TypeJokerBomb {
		s = fmt.Sprintf("%s %s（%d 张）", c.Type.label(), c.Rank.RankName(), len(c.Cards))
	}
	if len(c.Wilds) > 0 {
		parts := make([]string, len(c.Wilds))
		for i, w := range c.Wilds {
			parts[i] = fmt.Sprintf("%s 当 %s", w.Card.Key(), w.As.Key())
		}
		s += "（" + strings.Join(parts, "、") + "）"
	}
	return s
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

// comboValue 是比大小用的数值（见 Combo.Value 的说明）。
func comboValue(t ComboType, rank Rank, level Rank) int {
	if isSeqType(t) {
		return int(rank)
	}
	return rank.Power(level)
}

// Recognize 列出这手牌全部合法的解读，按牌型、牌面排好（两端比对时顺序一致）。
//
// 一手牌可能有好几种打法：
//   - 同花色的 34567 既是顺子又是同花顺：当顺子能跟牌，当同花顺能压炸弹；
//   - 带逢人配时更多：单张红桃级牌能当 2 到 A 里的任何一张，五张牌里有一张逢人配时顺子和
//     同花顺可能各有两种顶张。
//
// 逢人配（红桃级牌）除了大小王什么都能顶（第二条 14）：这里把每张逢人配顶成什么枚举一遍
// （一张 52 种、两张还是很小），顶法不同但牌型牌面一样的算同一种解读，留下顶法「最小」的那一种 ——
// 界面上要显示「♥7 当 ♠A」，两端要给出同一个答案。
//
// 这手牌什么牌型都不是时返回空切片，不是错误 —— 调用方按「这手牌打不出去」处理。
// 空手牌、同一张牌写了两次、级数不对是调用方的错，返回错误。
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
	played := sortCards(cards)

	var wilds, solids []Card
	for _, c := range played {
		if c.IsWild(level) {
			wilds = append(wilds, c)
			continue
		}
		solids = append(solids, c)
	}

	// 没有逢人配：直接认。
	if len(wilds) == 0 {
		out := make([]Combo, 0, 4)
		for _, sh := range recognizeCards(solids) {
			out = append(out, Combo{
				Type: sh.typ, Cards: played, Rank: sh.rank,
				Value: comboValue(sh.typ, sh.rank, level),
			})
		}
		return sortCombos(out), nil
	}

	// 有逢人配：把每张顶成什么枚举一遍，牌型牌面同样的解读只留顶法最小的那一种。
	best := map[shape]Combo{}
	eachSubstitution(len(wilds), func(as []Card) bool {
		subs := make([]Wildcard, len(wilds))
		trial := make([]Card, 0, len(played))
		trial = append(trial, solids...)
		for i := range wilds {
			subs[i] = Wildcard{Card: wilds[i], As: as[i]}
			trial = append(trial, Card{Rank: as[i].Rank, Suit: as[i].Suit, Deck: wilds[i].Deck, ID: wilds[i].ID})
		}
		for _, sh := range recognizeCards(trial) {
			c := Combo{
				Type: sh.typ, Cards: played, Wilds: subs, Rank: sh.rank,
				Value: comboValue(sh.typ, sh.rank, level),
			}
			if old, ok := best[sh]; !ok || lessWilds(c.Wilds, old.Wilds) {
				best[sh] = c
			}
		}
		return false
	})

	out := make([]Combo, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	return sortCombos(out), nil
}

// FindCombo 按声明的牌型找解读：这一手牌里这个牌型**只有一种**时返回它。有几种
// （逢人配顶法不同、点数也不同，或者同花色的顺子当顺子还是当同花顺）时不猜，返回 ok=false，
// 让调用方去问出牌的人 —— 出牌的动作里要带完整的声明（见 Pick）。
func FindCombo(cards []Card, level Rank, t ComboType) (Combo, bool, error) {
	combos, err := Recognize(cards, level)
	if err != nil {
		return Combo{}, false, err
	}
	c, ok := Pick(combos, t, 0)
	return c, ok, nil
}

// Pick 从解读里挑出声明的这一种：牌型 + 牌面。rank 写 0 表示「只看牌型，而且只有一种」。
func Pick(combos []Combo, t ComboType, rank Rank) (Combo, bool) {
	var found []Combo
	for _, c := range combos {
		if c.Type == t && (rank == 0 || c.Rank == rank) {
			found = append(found, c)
		}
	}
	if len(found) != 1 {
		return Combo{}, false
	}
	return found[0], true
}

// ---------------------------------------------------------------------------
// 认牌型的内核（不看牌的身份，只看点数和花色）
// ---------------------------------------------------------------------------

// shape 是一种解读的形状：牌型 + 牌面（顺子类是顶张，四大天王是 0）。
type shape struct {
	typ  ComboType
	rank Rank
}

// recognizeCards 是第三、四条那套判断：这手牌（可以带逢人配顶替出来的牌）能当成什么打。
// 只看点数、花色和张数；牌的身份、级数都不看（级数只影响顺子类顶张的算法，见 comboValue）。
func recognizeCards(cards []Card) []shape {
	counts := rankCounts(cards)
	distinct := make([]Rank, 0, len(counts))
	for r := range counts {
		distinct = append(distinct, r)
	}
	sort.Slice(distinct, func(i, j int) bool { return distinct[i] < distinct[j] })

	n := len(cards)
	var out []shape

	// 同点数的：单张、对子、三同张、炸弹（第三条 1、2、4、8）。
	if len(distinct) == 1 {
		switch {
		case n == 1:
			out = append(out, shape{TypeSingle, distinct[0]})
		case n == 2:
			out = append(out, shape{TypePair, distinct[0]})
		case n == 3:
			out = append(out, shape{TypeTriple, distinct[0]})
		case n >= 4 && n <= 10:
			out = append(out, shape{TypeBomb, distinct[0]})
		}
	}

	// 四大天王：大小王各两张（第三条 10）。它不是同点数的炸弹，单独认。
	if n == 4 && counts[BigJoker] == 2 && counts[SmallJoker] == 2 {
		out = append(out, shape{TypeJokerBomb, 0})
	}

	// 三带对：一个三同张带一个对子（第三条 6）。
	if n == 5 && len(distinct) == 2 {
		for _, r := range distinct {
			if counts[r] == 3 {
				out = append(out, shape{TypeTripleWithPair, r})
			}
		}
	}

	// 三连对：三个牌点相邻的对子（第三条 3）。
	if n == 6 && len(distinct) == 3 && allCounts(counts, 2) {
		if top, ok := seqTop(distinct, 3); ok {
			out = append(out, shape{TypeTube, top})
		}
	}

	// 三同连张：两个牌点相邻的三同张（第三条 5）。
	if n == 6 && len(distinct) == 2 && allCounts(counts, 3) {
		if top, ok := seqTop(distinct, 2); ok {
			out = append(out, shape{TypePlate, top})
		}
	}

	// 顺子：五个牌点相邻的单张，花色不限（第三条 7）。王不参与。
	if n == 5 && len(distinct) == 5 && allCounts(counts, 1) {
		if top, ok := seqTop(distinct, 5); ok {
			out = append(out, shape{TypeStraight, top})
			// 同一个顺子还是同一个花色时，它也是同花顺（第三条 9）：两种解读都给出来。
			if sameSuit(cards) {
				out = append(out, shape{TypeStraightFlush, top})
			}
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// 逢人配的枚举
// ---------------------------------------------------------------------------

// wildCards 是逢人配能顶替的牌：每个点数的四个花色，共 52 张（第二条 14：不含大、小王）。
var wildCards = func() []Card {
	out := make([]Card, 0, 52)
	for _, s := range []Suit{Spade, Heart, Club, Diamond} {
		for r := Two; r <= Ace; r++ {
			out = append(out, Card{Rank: r, Suit: s})
		}
	}
	return out
}()

// eachSubstitution 枚举 n 张逢人配各顶成什么（不排序、不看名额：顶替出来的牌不占真牌的位置，
// 所以五张 8 加两张逢人配能凑出十张炸）。顶法按点数、花色从小到大枚举，同一组顶法只走一遍。
// visit 返回 true 时提前停下。
func eachSubstitution(n int, visit func(as []Card) bool) {
	chosen := make([]Card, 0, n)
	var walk func(start int) bool
	walk = func(start int) bool {
		if len(chosen) == n {
			return visit(chosen)
		}
		for i := start; i < len(wildCards); i++ {
			chosen = append(chosen, wildCards[i])
			stop := walk(i)
			chosen = chosen[:len(chosen)-1]
			if stop {
				return true
			}
		}
		return false
	}
	walk(0)
}

// lessWilds 比两串顶法：点数、花色小的算小。同一种解读有多种顶法时留下小的那一种，
// 两端（Go、TS）才会给出同一个答案。
func lessWilds(a, b []Wildcard) bool {
	for i := range a {
		if a[i].As.Rank != b[i].As.Rank {
			return a[i].As.Rank < b[i].As.Rank
		}
		if a[i].As.Suit != b[i].As.Suit {
			return a[i].As.Suit < b[i].As.Suit
		}
	}
	return false
}

func sortCombos(combos []Combo) []Combo {
	sort.SliceStable(combos, func(i, j int) bool {
		if combos[i].Type != combos[j].Type {
			return combos[i].Type < combos[j].Type
		}
		return combos[i].Rank < combos[j].Rank
	})
	return combos
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
	// 两种读法：A 当 14，或者 A 当 1。剩下的都是 2–14。
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
