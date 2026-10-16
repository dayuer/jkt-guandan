package rules

import (
	"encoding/json"
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
