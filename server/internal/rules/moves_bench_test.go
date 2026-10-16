package rules

import (
	"fmt"
	"testing"
)

// Moves 的基准（#241）。为什么要有它：随机对局里量到「一副牌的开销几乎全在领出的 Moves 上」，
// 生产上「提示能出哪些牌」的延迟也在同一处；而且 #204 的性能用例守的是**数量级**
// （还特意用「最快一次」当主断言、不受机器负载影响），常数级的变化它看不出来 ——
// 那种变化用基准来守。
//
// 三档由**手里有几张逢人配**分开：顶法试探的规模直接由它决定（每多一张逢人配，
// 每个候选要多试一圈 13 种点数），所以 0/1/2 张的量级不一样，混在一起会被平均掉。
//
// 跑法：go test ./internal/rules/ -run '^$' -bench BenchmarkMoves -benchtime=20x

// benchHand 是基准用的 27 张手牌：级别 7，先按需要的张数放进逢人配，
// 再「每隔四张取一张」把点数和花色摊开，凑满 27 张 —— 一手牌里点子散得开，
// 顺子、连对、炸弹这些路径都能走到，不是一个只会出单张的退化手牌。
func benchHand(wilds int) []Card {
	deck := NewDeck()
	hand := make([]Card, 0, handsEach)
	used := map[int]bool{}

	for _, c := range deck {
		if len(hand) >= wilds {
			break
		}
		if c.IsWild(Seven) {
			hand = append(hand, c)
			used[c.ID] = true
		}
	}
	for i := 0; len(hand) < handsEach && i < len(deck); i += 4 {
		if c := deck[i]; !used[c.ID] && !c.IsWild(Seven) {
			hand = append(hand, c)
			used[c.ID] = true
		}
	}
	for i := 0; len(hand) < handsEach && i < len(deck); i++ {
		if c := deck[i]; !used[c.ID] && !c.IsWild(Seven) {
			hand = append(hand, c)
			used[c.ID] = true
		}
	}
	if len(hand) != handsEach {
		panic(fmt.Sprintf("基准手牌只凑到 %d 张，应该是 %d 张", len(hand), handsEach))
	}
	got := 0
	for _, c := range hand {
		if c.IsWild(Seven) {
			got++
		}
	}
	if got != wilds {
		panic(fmt.Sprintf("基准手牌里有 %d 张逢人配，要的是 %d 张", got, wilds))
	}
	return hand
}

// benchLead 是基准里用的几种「上一手」（跟牌路径）。
func benchLead(tb testing.TB, kind string, cards []string) *Combo {
	tb.Helper()
	c := cardsOf(tb, cards)
	combos, err := Recognize(c, Seven)
	if err != nil {
		tb.Fatalf("上一手 %v：%v", cards, err)
	}
	for i := range combos {
		if combos[i].Type.String() == kind {
			return &combos[i]
		}
	}
	tb.Fatalf("%v 认不出 %s（认出来的是 %s）", cards, kind, ComboSummary(combos))
	return nil
}

// BenchmarkMoves27Lead 是领出：要造全部牌型，最重的一条路。
func BenchmarkMoves27Lead(b *testing.B) {
	for _, wilds := range []int{0, 1, 2} {
		b.Run(fmt.Sprintf("wilds=%d", wilds), func(b *testing.B) {
			hand := benchHand(wilds)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := Moves(hand, Seven, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkMoves27Follow 是跟牌：只造能压住上一手的牌型（生产上最常见的一条）。
func BenchmarkMoves27Follow(b *testing.B) {
	cases := []struct {
		name  string
		kind  string
		cards []string
	}{
		{"对子9", "pair", []string{"♠9/0", "♥9/0"}},
		{"顺子", "straight", []string{"♦5/0", "♣6/0", "♦7/0", "♣8/0", "♦9/0"}},
		{"炸弹8", "bomb", []string{"♠8/0", "♥8/0", "♣8/0", "♦8/0"}},
	}
	for _, tc := range cases {
		for _, wilds := range []int{0, 2} {
			b.Run(fmt.Sprintf("%s/wilds=%d", tc.name, wilds), func(b *testing.B) {
				hand := benchHand(wilds)
				lead := benchLead(b, tc.kind, tc.cards)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := Moves(hand, Seven, lead); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
