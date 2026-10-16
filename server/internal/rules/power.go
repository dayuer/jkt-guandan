package rules

// Power 是这张牌在当前级数（打几）下的牌点大小。第四条 一：牌点由大到小是
// 大王、小王、级牌、A、K、Q、J、10、9、8、7、6、5、4、3、2。
//
// 也就是说，打几的时候，那个点数的 8 张牌（两副牌四个花色）一起从原来的位置提到 A 之上；
// 红桃的那两张还是逢人配，除了大小王什么都能顶替（第二条 14）。
// 打 A 的时候 A 自己就是级牌，没有「A 之上还压着一档」的牌 —— 返回值只是用来比大小，
// 具体数值不写进牌例（牌例比的是顺序，不是这两个数）。
//
// 顺子、三连对、三同连张里级牌按牌面原位置算，不用这里的大小（第五条 1：
// 那几个牌型里「A 可以当 1」之外，级牌没有特殊地位，点数就按牌面走）。
func (r Rank) Power(level Rank) int {
	switch {
	case r == BigJoker:
		return 17 // 大王最大
	case r == SmallJoker:
		return 16
	case r == level:
		return 15 // 级牌：A 之上、小王之下
	case r == Ace:
		return 14
	}
	return int(r)
}

// Power 是这张牌在当前级数下的牌点大小（见 Rank.Power）。
func (c Card) Power(level Rank) int { return c.Rank.Power(level) }

// IsWild 判断这张牌是不是逢人配：红桃级牌（第二条 14）。
// 它能当除大小王以外的任意一张牌用，牌型识别里要枚举它顶替成什么（#87 的同一套口径）。
func (c Card) IsWild(level Rank) bool { return c.Suit == Heart && c.Rank == level }
