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
