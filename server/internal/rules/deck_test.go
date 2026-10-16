package rules

import "testing"

// 两副牌 108 张：52 张点数牌 ×2 副 + 大小王各 2 张（第二条 5：共有 108 张）。
func TestNewDeck(t *testing.T) {
	deck := NewDeck()
	if len(deck) != 108 {
		t.Fatalf("整副牌 %d 张，应该 108 张", len(deck))
	}
	counts := map[Rank]int{}
	seen := map[int]bool{}
	for i, c := range deck {
		if c.ID != i {
			t.Fatalf("第 %d 张的 ID 是 %d：ID 就是整副牌里的位置", i, c.ID)
		}
		if seen[c.ID] {
			t.Fatalf("ID %d 出现了两次", c.ID)
		}
		seen[c.ID] = true
		counts[c.Rank]++
		if c.Rank.IsJoker() {
			if c.Suit != NoSuit {
				t.Fatalf("%s 是王，不该有花色 %s", c.Key(), c.Suit.SuitName())
			}
			continue
		}
		if c.Suit == NoSuit {
			t.Fatalf("%s 没有花色", c.Key())
		}
	}
	// 每个牌点 8 张：两副牌四个花色（第二条 14 里「共 8 张」的级牌就是这么来的）。
	for r := Two; r <= Ace; r++ {
		if counts[r] != 8 {
			t.Fatalf("牌点 %s 有 %d 张，应该 8 张", r.RankName(), counts[r])
		}
	}
	if counts[SmallJoker] != 2 || counts[BigJoker] != 2 {
		t.Fatalf("小王 %d 张、大王 %d 张，各应该 2 张", counts[SmallJoker], counts[BigJoker])
	}
}

// Key 写法要能一来一回：牌例两端都靠它指牌。
func TestCardKeyRoundTrip(t *testing.T) {
	for _, c := range NewDeck() {
		back, err := ParseKey(c.Key())
		if err != nil {
			t.Fatalf("%s：%v", c.Key(), err)
		}
		if back.Rank != c.Rank || back.Suit != c.Suit || back.Deck != c.Deck {
			t.Fatalf("%s 读回来是 %s", c.Key(), back.Key())
		}
	}
}

// 认不出来的写法。最后一档是**非规范但读得出**的：小写牌面、补零的副号、花色带变体选择符
// U+FE0F —— 这些也一律拒绝（#213 复审：Go 端松一点，严格比对的 TS 端就会挂）。
func TestParseKeyErrors(t *testing.T) {
	for _, bad := range []string{"", "♠7", "♠7/2", "♠7/x", "7/0", "♠X/0", "大王♠/0", "A/1",
		"♠j/0", "♠07/0", "♠7/00", "♥\ufe0f7/0", "小王\u2660/0"} {
		if got, err := ParseKey(bad); err == nil {
			t.Errorf("%q 应当认不出来，得到了 %s", bad, got.Key())
		}
	}
}

// 同一个牌点、不同花色/副号的牌牌点相同；大小不受花色影响（第四条 二、（一）4：均不比较花色）。
func TestPowerIgnoresSuit(t *testing.T) {
	deck := NewDeck()
	for r := Two; r <= Ace; r++ {
		var want, n int
		for _, c := range deck {
			if c.Rank != r {
				continue
			}
			n++
			if n == 1 {
				want = c.Power(Seven)
				continue
			}
			if got := c.Power(Seven); got != want {
				t.Fatalf("%s 和同点数的牌牌点不同：%d vs %d", c.Key(), got, want)
			}
		}
		if n != 8 {
			t.Fatalf("牌点 %s 有 %d 张牌", r.RankName(), n)
		}
	}
}

// 级牌提到 A 之上、小王之下：打 7 时四张 7 的牌点一样，而且都比 A 大（第四条 一）。
func TestPowerLevelRankAboveAce(t *testing.T) {
	deck := NewDeck()
	ace := Ace.Power(Seven)
	small := SmallJoker.Power(Seven)
	n := 0
	for _, c := range deck {
		if c.Rank != Seven {
			continue
		}
		n++
		if p := c.Power(Seven); p <= ace || p >= small {
			t.Fatalf("%s 的牌点 %d 应该在 A（%d）之上、小王（%d）之下", c.Key(), p, ace, small)
		}
	}
	if n != 8 {
		t.Fatalf("级牌 8 张，实际 %d 张", n)
	}
	// 打 A 时 A 自己是级牌：还是排在 K 之上、小王之下，头上没有另一档。
	if got, k := Ace.Power(Ace), King.Power(Ace); got <= k || got >= SmallJoker.Power(Ace) {
		t.Fatalf("打 A 时 A 的牌点 %d 不对（K 是 %d）", got, k)
	}
}

// 逢人配是红桃级牌，两副牌一共 2 张（第二条 14）。
func TestIsWild(t *testing.T) {
	n := 0
	for _, c := range NewDeck() {
		if c.IsWild(Seven) {
			n++
			if c.Rank != Seven || c.Suit != Heart {
				t.Fatalf("逢人配应当是红桃级牌：%s", c.Key())
			}
		}
	}
	if n != 2 {
		t.Fatalf("逢人配 2 张，实际 %d 张", n)
	}
	// 打成别的级数，红桃 7 就不是逢人配了。
	if NewDeck()[0].IsWild(Two) {
		t.Fatal("黑桃 2 不该是逢人配")
	}
}

// 只认规范写法（#213 复审）：大小写、前导零、正号、别的写法都拒绝 ——
// 牌例是两端逐字符串比对的，Go 端松一点，严格比对的 TS 端就会挂。
func TestParseRankCanonical(t *testing.T) {
	good := []string{"2", "3", "9", "10", "J", "Q", "K", "A", "小王", "大王"}
	for _, s := range good {
		if _, err := ParseRank(s); err != nil {
			t.Errorf("%q 应当认得出来：%v", s, err)
		}
	}
	// 反面：能读成同一个牌点、但不是规范写法的那些。
	bad := []string{"j", "q", "k", "a", "05", "+5", "5 ", " 5", "010", "1", "11", "0", "-2", "小王 ", "王", "", "十"}
	for _, s := range bad {
		if got, err := ParseRank(s); err == nil {
			t.Errorf("%q 应当认不出来，得到了 %s", s, got.RankName())
		}
	}
}
