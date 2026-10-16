package ai

import (
	"reflect"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/rules"
)

// AI **不许**看得见别家的手牌 —— 用反射把这条钉死，免得以后有人「顺手」加个字段。
//
// 为什么值得一条测试：这种洞从外面看不出来 —— AI 照样能跑、照样能过其它所有测试，
// 只是它每一手都打得「恰好」正确（因为它在偷看）。而它又很容易加进来：只要有人图省事
// 把别家的手牌、或者整个 `*rules.Game`（`Game.Hand(seat)` 谁的手牌都给得出来）放进 View 就行。
func TestViewExposesNoOtherHands(t *testing.T) {
	typ := reflect.TypeOf(View{})
	oneHand := reflect.TypeOf([]rules.Card{})
	manyHands := reflect.TypeOf([][]rules.Card{})
	game := reflect.TypeOf(&rules.Game{})

	// 自己的手牌要有 —— 不然这条测试是空转（把所有手牌形状都禁掉也能「通过」）。
	if _, ok := typ.FieldByName("Hand"); !ok {
		t.Fatal("View 里没有 Hand —— AI 没法打牌，这条测试也就没意义了")
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		switch {
		case f.Type == oneHand && f.Name != "Hand":
			t.Fatalf("View.%s 是手牌形状的字段 —— AI 只该看得见自己的手牌（别家只给张数 Counts）", f.Name)
		case f.Type == manyHands:
			t.Fatalf("View.%s 是四家手牌 —— AI 只该看得见自己的手牌", f.Name)
		case f.Type == game:
			t.Fatalf("View.%s 是 *rules.Game —— 它把手牌全暴露给 AI 了", f.Name)
		}
	}
}

func TestViewPartnerAndOpponents(t *testing.T) {
	cases := []struct {
		seat      int
		partner   int
		opponents []int
	}{
		{1, 3, []int{2, 4}},
		{2, 4, []int{1, 3}},
		{3, 1, []int{2, 4}},
		{4, 2, []int{1, 3}},
	}
	for _, c := range cases {
		v := View{Seat: c.seat}
		if got := v.Partner(); got != c.partner {
			t.Errorf("%d 号座的对家该是 %d，得到 %d", c.seat, c.partner, got)
		}
		if got := v.Opponents(); !reflect.DeepEqual(got, c.opponents) {
			t.Errorf("%d 号座的对方该是 %v，得到 %v", c.seat, c.opponents, got)
		}
		if v.LastFromPartner() {
			t.Errorf("%d 号座：还没人出牌时不该说「队友出的」", c.seat)
		}
	}
}
