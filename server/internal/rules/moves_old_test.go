package rules

import (
	"fmt"
	"math/rand"
	"testing"
)

// 老实现（走 Recognize 的慢路径）留成 test-only 的参照，守「优化之后行为一模一样」（#241）。
//
// 为什么要有这条：moves.json 的牌例按**集合**比顶法（README：哪些逢人配顶成什么，
// 顺序和「用的是哪一张」不进判定），所以有两类差别牌例抓不到 ——
//   - 顶法分给哪一张逢人配（「点数小的顶法配 ID 小的那张」，老实现和 TS 都是这么分的）；
//   - As 这张「说明用的牌」上的 ID/副号（老实现留 0）。
//
// 这两类在共享牌例里看不出来，但会写进局面、日志、两端的对拍里。所以这里拿同一个
// 候选生成器 + 老那条「交给 Recognize 枚举」的认法当参照，逐字段比 ——
// auditor-242 就是靠这个发现两处不一致的，现在把它固化下来当护栏。
//
// 语料固定种子（红了能原样复现）。老路径慢，所以满手牌只放几副、其余用中小手牌配：
// 慢的是「两张逢人配 + 满手牌」那一档，那档单独留了几副。

// movesOld 是优化之前的做法：每个候选都交给 Recognize（它会枚举所有顶法、挑最小的那一种）。
func movesOld(hand []Card, level Rank, lead *Combo) ([]Combo, error) {
	return movesWith(hand, level, lead, func(cand candidate, level Rank) ([]Combo, error) {
		return Recognize(cand.cards, level)
	})
}

// oldPathEdgeHands 是随机手牌抽不到、但新老路径差别最可能出现在这里的几种形态。
var oldPathEdgeHands = []struct {
	name  string
	level string
	cards []string
}{
	{"两张逢人配 + 同一门三张实牌", "4", []string{"♥4/0", "♥4/1", "♣7/0", "♦7/0", "♦7/1"}},
	{"两张逢人配 + 2/4/5（A 当 1 的窗口）", "6", []string{"♥6/0", "♥6/1", "♠2/0", "♠4/0", "♣5/0"}},
	{"两张逢人配 + 三个 9 一个 5", "7", []string{"♥7/0", "♥7/1", "♠9/0", "♥9/0", "♣9/0", "♠5/0"}},
	{"两张逢人配 + 同花顺那一路", "7", []string{"♥7/0", "♥7/1", "♠3/0", "♠4/0", "♠5/0"}},
	{"两张逢人配 + 四张同点数", "7", []string{"♥7/0", "♥7/1", "♠3/0", "♥3/0", "♣3/0", "♦3/0"}},
	{"两张逢人配 + A2345 的实牌", "3", []string{"♥3/0", "♥3/1", "♠A/0", "♠2/0", "♠4/0", "♠5/0"}},
	{"一张逢人配 + 满手牌（27 张）", "7", []string{
		"♥7/0", "♠2/0", "♣2/0", "♦3/0", "♠3/0", "♥3/1", "♣4/0", "♦4/0", "♠5/0", "♥5/0",
		"♣5/1", "♦6/0", "♠6/0", "♥6/1", "♣8/0", "♦8/0", "♠8/1", "♥9/0", "♣9/0", "♦9/1",
		"♠10/0", "♥10/0", "♣J/0", "♦J/1", "♠Q/0", "♣K/0", "♦A/0"}},
}

func TestMovesMatchesOldSlowPath(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20411))

	leads, follows, full := 0, 0, 0
	for i := 0; i < 5000; i++ {
		level := levelFor(i)
		n := 3 + rnd.Intn(10)
		if i%400 == 0 {
			n = 27 // 满手牌（老路径最慢那一档，数量控制住）
			full++
		}
		hand := randomHand(rnd, deck, n, level)

		want, err := movesOld(hand, level, nil)
		if err != nil {
			t.Fatalf("老实现（领出）%s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		got, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatalf("新实现（领出）%s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		sameMoveLists(t, "（领出）", hand, level, want, got)
		leads++

		// 跟牌那一手的「上一手」从**这手牌自己的出法**里挑（#243）。
		//
		// 原来是另抓一手随机牌当上一手，结果是：多数时候认不出牌型、或者手里根本压不住 ——
		// 那样比的是两个空集合，等于没比（5000 手里真正比到跟牌的只有 1272 个）。
		// 改成从自己的出法里挑以后，上一手一定是合法出法，而且多半手里真有能压的东西，
		// 跟牌局面 1272 → 5291（其中「手里真有能压的」从 758 涨到 3577）；代价是这条测试从 1.2s 涨到 1.8s 上下（慢在老实现跑跟牌上）。
		if len(got) == 0 {
			continue
		}
		lead := got[rnd.Intn(len(got))]

		want, err = movesOld(hand, level, &lead)
		if err != nil {
			t.Fatalf("老实现（跟牌）%s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		got, err = Moves(hand, level, &lead)
		if err != nil {
			t.Fatalf("新实现（跟牌）%s 打 %s：%v", cardsText(hand), level.RankName(), err)
		}
		sameMoveLists(t, "（跟 "+lead.String()+"）", hand, level, want, got)
		follows++
	}
	if leads == 0 || follows == 0 {
		t.Fatalf("一条都没比到：领出 %d、跟牌 %d", leads, follows)
	}

	// 随机手牌抽不到的几种形态，写死再各比一遍（随机的几千手牌里「两张逢人配 + 同一个点数
	// 三张实牌」这种组合太少，漏了它这条护栏就守不住「五张槽位那一路」）。
	for _, h := range oldPathEdgeHands {
		level, err := ParseRank(h.level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", h.name, h.level, err)
		}
		hand := cardsOf(t, h.cards)
		want, err := movesOld(hand, level, nil)
		if err != nil {
			t.Fatalf("%s（领出）老实现：%v", h.name, err)
		}
		got, err := Moves(hand, level, nil)
		if err != nil {
			t.Fatalf("%s（领出）新实现：%v", h.name, err)
		}
		sameMoveLists(t, "（"+h.name+" 领出）", hand, level, want, got)
		leads++

		for _, combos := range [][]Combo{want} {
			for _, lead := range combos {
				wantF, err := movesOld(hand, level, &lead)
				if err != nil {
					t.Fatalf("%s（跟 %s）老实现：%v", h.name, lead.String(), err)
				}
				gotF, err := Moves(hand, level, &lead)
				if err != nil {
					t.Fatalf("%s（跟 %s）新实现：%v", h.name, lead.String(), err)
				}
				sameMoveLists(t, "（"+h.name+" 跟 "+lead.String()+"）", hand, level, wantF, gotF)
				follows++
			}
		}
	}
	t.Logf("新老两份实现在同一批局面上逐字段一致：领出 %d 个局面、跟牌 %d 个（其中 %d 副满手牌）",
		leads, follows, full)
}

// sameMoveLists 逐字段比两批出法：牌型、牌面、张数、**具体的牌（ID 序列）**、
// 顶法（逐张：哪张逢人配、顶成什么，连 As 的 ID/副号一起）。
func sameMoveLists(t *testing.T, what string, hand []Card, level Rank, want, got []Combo) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s 手牌 %s 打 %s：老实现 %d 手，新实现 %d 手\n老：%s\n新：%s",
			what, cardsText(hand), level.RankName(), len(want), len(got),
			comboListText(want), comboListText(got))
	}
	for i := range want {
		if d := moveDiff(want[i], got[i]); d != "" {
			t.Fatalf("%s 手牌 %s 打 %s：第 %d 手不一样（%s）\n老：%s\n新：%s",
				what, cardsText(hand), level.RankName(), i+1, d, moveFullText(want[i]), moveFullText(got[i]))
		}
	}
}

// moveDiff 说这一手哪里不一样；一样就返回空串。
func moveDiff(want, got Combo) string {
	if want.Type != got.Type {
		return fmt.Sprintf("牌型 %s ≠ %s", want.Type.String(), got.Type.String())
	}
	if want.Rank != got.Rank {
		return fmt.Sprintf("牌面 %s ≠ %s", want.Rank.RankName(), got.Rank.RankName())
	}
	if want.Value != got.Value {
		return fmt.Sprintf("比大小的值 %d ≠ %d", want.Value, got.Value)
	}
	if len(want.Cards) != len(got.Cards) {
		return fmt.Sprintf("张数 %d ≠ %d", len(want.Cards), len(got.Cards))
	}
	for i := range want.Cards {
		if want.Cards[i].ID != got.Cards[i].ID {
			return fmt.Sprintf("第 %d 张牌不一样（老 %s，新 %s）", i+1, want.Cards[i].Key(), got.Cards[i].Key())
		}
	}
	if len(want.Wilds) != len(got.Wilds) {
		return fmt.Sprintf("顶法条数 %d ≠ %d", len(want.Wilds), len(got.Wilds))
	}
	for i := range want.Wilds {
		a, b := want.Wilds[i], got.Wilds[i]
		if a.Card.ID != b.Card.ID {
			return fmt.Sprintf("第 %d 条顶法用的是不同的逢人配（老 %s，新 %s）", i+1, a.Card.Key(), b.Card.Key())
		}
		if a.As.Rank != b.As.Rank || a.As.Suit != b.As.Suit {
			return fmt.Sprintf("第 %d 条顶法顶成的不一样（老 %s，新 %s，同一张 %s）",
				i+1, asKey(a.As), asKey(b.As), a.Card.Key())
		}
		if a.As.ID != b.As.ID || a.As.Deck != b.As.Deck {
			return fmt.Sprintf("第 %d 条顶法的说明牌 ID/副号不一样（老 %d/%d，新 %d/%d）",
				i+1, a.As.ID, a.As.Deck, b.As.ID, b.As.Deck)
		}
	}
	return ""
}

func moveFullText(c Combo) string {
	out := moveText(c) + "[" + cardsText(c.Cards) + "]"
	for _, w := range c.Wilds {
		out += fmt.Sprintf("（%s 当 %s id=%d/%d）", w.Card.Key(), asKey(w.As), w.As.ID, w.As.Deck)
	}
	return out
}
