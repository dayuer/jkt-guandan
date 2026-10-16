package rules

import (
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 空手牌、同一张牌写两遍、级数不对：都是调用方写错了，要报错，不能当成「打不出去」。
func TestRecognizeRejectsBadInput(t *testing.T) {
	deck := NewDeck()
	if _, err := Recognize(nil, Seven); err == nil {
		t.Error("没有牌应当报错")
	}
	if _, err := Recognize([]Card{deck[0], deck[0]}, Seven); err == nil {
		t.Error("同一张牌写两遍应当报错")
	}
	if _, err := Recognize([]Card{deck[0]}, BigJoker); err == nil {
		t.Error("级数是大王应当报错")
	}
	if _, err := Recognize([]Card{deck[0]}, Two-1); err == nil {
		t.Error("级数小于 2 应当报错")
	}
	// 同一手牌里两张一样的点数（不同副）不算重复：那是真的两张牌。
	if _, err := Recognize([]Card{deck[0], deck[54]}, Seven); err != nil {
		t.Errorf("两张黑桃 2 是合法的对子：%v", err)
	}
}

// 声明的牌型认不出来时返回 ok=false，不是错误（牌局里就是「这手牌不能这么打」）。
func TestFindCombo(t *testing.T) {
	cards := cardsOf(t, []string{"♠3/0", "♠4/0", "♠5/0", "♠6/0", "♠7/0"})

	c, ok, err := FindCombo(cards, Seven, TypeStraight)
	if err != nil || !ok || c.Value != 7 {
		t.Fatalf("同花色的 34567 能当顺子打：%+v %v %v", c, ok, err)
	}
	// 同一手牌当同花顺打：牌型不同，大小也不同（同花顺排在五张炸之上）。
	c, ok, err = FindCombo(cards, Seven, TypeStraightFlush)
	if err != nil || !ok || !c.IsBomb() {
		t.Fatalf("同一手牌也能当同花顺打：%+v %v %v", c, ok, err)
	}
	if _, ok, err := FindCombo(cards, Seven, TypeBomb); err != nil || ok {
		t.Fatalf("这手牌不是炸弹：%v %v", ok, err)
	}
	if _, ok, err := FindCombo(cards, Seven, TypeTube); err != nil || ok {
		t.Fatalf("这手牌不是三连对：%v %v", ok, err)
	}
}

// 顺子类按牌面原位置比大小，其余按级数下的大小（第五条 1、第四条一）。
func TestComboValue(t *testing.T) {
	// 打 7：单张 7 是级牌，排在 A 之上。
	seven, ok, err := FindCombo(cardsOf(t, []string{"♠7/0"}), Seven, TypeSingle)
	if err != nil || !ok {
		t.Fatal(err)
	}
	ace, ok, _ := FindCombo(cardsOf(t, []string{"♠A/0"}), Seven, TypeSingle)
	if !seven.Beats(ace) {
		t.Fatalf("打 7 时单张 7 要压单张 A：%d vs %d", seven.Value, ace.Value)
	}
	// 顺子里的 7 还是 7：34567 压不过 45678。
	low, ok, _ := FindCombo(cardsOf(t, []string{"♠3/0", "♠4/0", "♠5/0", "♠6/0", "♠7/0"}), Seven, TypeStraight)
	high, ok2, _ := FindCombo(cardsOf(t, []string{"♠4/0", "♠5/0", "♠6/0", "♠7/0", "♠8/0"}), Seven, TypeStraight)
	if !ok || !ok2 {
		t.Fatal("两条顺子都该认得出来")
	}
	if low.Value != 7 || high.Value != 8 || low.Beats(high) {
		t.Fatalf("顺子按牌面比：%d vs %d", low.Value, high.Value)
	}
	// A2345 比 23456 小。
	aceLow, ok, _ := FindCombo(cardsOf(t, []string{"♠A/0", "♠2/0", "♠3/0", "♠4/0", "♠5/0"}), Seven, TypeStraight)
	two, _, _ := FindCombo(cardsOf(t, []string{"♠2/0", "♠3/0", "♠4/0", "♠5/0", "♠6/0"}), Seven, TypeStraight)
	if !ok || !two.Beats(aceLow) || aceLow.Value != 5 {
		t.Fatalf("A2345 的顶张是 5：%d", aceLow.Value)
	}
}

// 反对称：a 能压 b 的时候，b 一定压不了 a。牌例里的每一手牌都拿来自比一遍。
func TestBeatsAntiSymmetric(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixturesDir, "combos.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f comboFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	var all []Combo
	for _, v := range f.Vectors {
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatal(err)
		}
		combos, err := Recognize(cardsOf(t, v.Cards), level)
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		// 只看打过 7 的那些：Level 不同的话 Value 不可比（牌局里同一手牌只在一个级数下比）。
		if level == Seven {
			all = append(all, combos...)
		}
	}
	if len(all) < 10 {
		t.Fatalf("拿来比的解读只有 %d 个", len(all))
	}
	for _, a := range all {
		for _, b := range all {
			if a.Beats(b) && b.Beats(a) {
				t.Fatalf("%s 和 %s 互相压", a, b)
			}
		}
	}
}

// 逢人配：同一手牌有好几种解读，顶法统一取点数、花色最小的那一种，两端才会给出同一个答案
// （界面要显示「♥7 当 ♠A」）。
func TestRecognizeWildsSubstitution(t *testing.T) {
	level := Seven
	cards := cardsOf(t, []string{"♠3/0", "♠4/0", "♠5/0", "♠6/0", "♥7/0"})
	combos, err := Recognize(cards, level)
	if err != nil {
		t.Fatal(err)
	}
	// 顺子 23456：逢人配当 2，花色随便（不是同花顺），取最小的黑桃 2。
	straight6, ok := Pick(combos, TypeStraight, Six)
	if !ok {
		t.Fatalf("应当能当 23456 的顺子打：%s", ComboSummary(combos))
	}
	if len(straight6.Wilds) != 1 || straight6.Wilds[0].Card.Key() != "♥7/0" || straight6.Wilds[0].As.Key() != "♠2/0" {
		t.Fatalf("顶法应当是 ♥7 当 ♠2：%+v", straight6.Wilds)
	}
	// 同花顺 23456：顶成 ♠2 才凑得成同花，花色是定死的。
	flush6, ok := Pick(combos, TypeStraightFlush, Six)
	if !ok || flush6.Wilds[0].As.Key() != "♠2/0" {
		t.Fatalf("同花顺 23456 要顶成 ♠2：%+v %v", flush6.Wilds, ok)
	}
	// 不是逢人配的牌不会出现在 Wilds 里。
	for _, c := range combos {
		if c.Type == TypeStraight && c.Rank == Seven && len(c.Wilds) != 1 {
			t.Fatalf("34567 只有一张逢人配：%+v", c.Wilds)
		}
	}
	// 打别的级数时 ♥7 就是一张普通的 7：只有一种解读，也没有顶法。
	plain, err := Recognize(cards, Five)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) != 1 || len(plain[0].Wilds) != 0 || plain[0].Type != TypeStraight {
		t.Fatalf("打 5 时这手牌就是一条顺子：%s", ComboSummary(plain))
	}
}

// 三带对带的是王对：牌例里有，这里再确认一次王对是合法的对子（第三条 2、6）。
func TestTripleWithJokerPair(t *testing.T) {
	cards := cardsOf(t, []string{"♠9/0", "♥9/0", "♦9/1", "大王/0", "大王/1"})
	c, ok, err := FindCombo(cards, Seven, TypeTripleWithPair)
	if err != nil || !ok || c.Rank != Nine || c.Value != Nine.Power(Seven) {
		t.Fatalf("999 带一对大王：%+v %v %v", c, ok, err)
	}
}

// 两张逢人配：枚举 52×53/2 种顶法也要很快（跟牌生成会反复用到）。
func BenchmarkRecognizeTwoWilds(b *testing.B) {
	cards := cardsOf2(b, []string{"♠3/0", "♠4/0", "♠5/0", "♠6/0", "♥7/0", "♥7/1"})
	for i := 0; i < b.N; i++ {
		if _, err := Recognize(cards, Seven); err != nil {
			b.Fatal(err)
		}
	}
}

func cardsOf2(b *testing.B, keys []string) []Card {
	b.Helper()
	out := make([]Card, len(keys))
	for i, k := range keys {
		c, err := ParseKey(k)
		if err != nil {
			b.Fatalf("牌 %q：%v", k, err)
		}
		out[i] = c
	}
	return out
}

// 牌型名要能一来一回：牌例里写的就是这些名字。
func TestComboTypeNames(t *testing.T) {
	want := map[ComboType]string{
		TypeSingle: "single", TypePair: "pair", TypeTriple: "triple",
		TypeTripleWithPair: "triple-pair", TypeTube: "tube", TypePlate: "plate",
		TypeStraight: "straight", TypeStraightFlush: "straight-flush",
		TypeBomb: "bomb", TypeJokerBomb: "joker-bomb",
	}
	for t2, name := range want {
		if t2.String() != name {
			t.Errorf("%d 的名字是 %q，应该是 %q", t2, t2.String(), name)
		}
		got, err := ParseComboType(name)
		if err != nil || got != t2 {
			t.Errorf("ParseComboType(%q) = %v %v", name, got, err)
		}
	}
	if _, err := ParseComboType("王炸"); err == nil {
		t.Error("认不出的名字应当报错")
	}
	if !strings.Contains(ComboSummary(nil), "打不出去") {
		t.Error("空解读的说明不对")
	}
}

// 逢人配枚举的性质测试（#217 复审建议）：随机手牌上，每一条解读都要能「还原」——
// 把逢人配按它声明的顶法换成顶成的那张牌，得到的牌型必须真的是那个牌型、那个牌面。
//
// 这是独立于 Recognize 的第二条路：Recognize 是「枚举顶法 → 认牌型」，
// 这里反过来「按声明还原 → 单独认一次」，两边对不上就说明枚举或挑选的规矩有问题。
// 随机数用固定种子：跑起来确定，不会今天绿明天红。
func TestWildReadingsRestore(t *testing.T) {
	deck := NewDeck()
	rnd := rand.New(rand.NewSource(20261016))
	levels := []Rank{Two, Five, Seven, Ten, Ace}

	hands := 0
	readings := 0
	for i := 0; i < 20000; i++ {
		level := levels[rnd.Intn(len(levels))]
		n := 1 + rnd.Intn(10)
		hand := make([]Card, 0, n)
		seen := map[int]bool{}
		for len(hand) < n {
			c := deck[rnd.Intn(len(deck))]
			if seen[c.ID] {
				continue
			}
			// 偏向「点数离两头远一点」的牌：不然随机到连着的顺子太少，测不到顺子类的顶法。
			if rnd.Intn(3) != 0 && !c.IsWild(level) {
				if c.Rank < Two+2 || c.Rank > Ace-2 {
					continue
				}
			}
			seen[c.ID] = true
			hand = append(hand, c)
		}

		combos, err := Recognize(hand, level)
		if err != nil {
			t.Fatalf("认牌型：%v", err)
		}
		hands++
		seenReading := map[string]bool{}
		for _, c := range combos {
			readings++

			// ① 没有重复解读。
			key := c.Type.String() + "/" + c.Rank.RankName() + "/" + wildText(c.Wilds)
			if seenReading[key] {
				t.Fatalf("手牌 %s 打 %s：解读 %s 出现了两次", handText(hand), level.RankName(), key)
			}
			seenReading[key] = true

			// ② 逢人配的条数和手里的一样，而且顶的都不是王。
			want := 0
			for _, h := range hand {
				if h.IsWild(level) {
					want++
				}
			}
			if len(c.Wilds) != want {
				t.Fatalf("手牌 %s 打 %s：解读 %s 只写了 %d 张逢人配，手里有 %d 张",
					handText(hand), level.RankName(), key, len(c.Wilds), want)
			}

			// ③ 按声明还原之后，牌型牌面要对得上。
			trial := make([]Card, 0, len(c.Cards))
			byID := map[int]Card{}
			for _, w := range c.Wilds {
				if w.As.Rank.IsJoker() {
					t.Fatalf("手牌 %s 打 %s：逢人配顶成了王 %s", handText(hand), level.RankName(), asKey(w.As))
				}
				byID[w.Card.ID] = w.As
			}
			for _, card := range c.Cards {
				if as, ok := byID[card.ID]; ok {
					trial = append(trial, Card{Rank: as.Rank, Suit: as.Suit, Deck: card.Deck, ID: card.ID})
					continue
				}
				trial = append(trial, card)
			}
			hit := false
			for _, sh := range recognizeCards(sortCards(trial)) {
				if sh.typ == c.Type && sh.rank == c.Rank {
					hit = true
					break
				}
			}
			if !hit {
				t.Fatalf("手牌 %s 打 %s：解读 %s 还原成 %s 之后认不出来",
					handText(hand), level.RankName(), key, handText(trial))
			}
		}
	}
	if hands == 0 || readings == 0 {
		t.Fatalf("没跑出东西：%d 手牌、%d 条解读", hands, readings)
	}
	t.Logf("跑了 %d 手牌、%d 条解读", hands, readings)
}

func wildText(ws []Wildcard) string {
	parts := make([]string, len(ws))
	for i, w := range ws {
		parts[i] = w.Card.Key() + "→" + asKey(w.As)
	}
	return strings.Join(parts, ",")
}

func handText(cards []Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Key()
	}
	return strings.Join(parts, " ")
}
