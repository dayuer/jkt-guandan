package ai

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/rules"
)

const rulesPkg = "github.com/dayuer/jkt-guandan/server/internal/rules"

// viewFields 是 `View` 允许有的字段，一个不多一个不少。
//
// 加字段必须**有意识地**改这里并说清理由 —— AI 的输入形状就是「它看得见什么」，随手加一个字段
// 可能就把别家的牌塞进去了（那种洞从外面看不出来：AI 照样能跑、照样能过其它测试，
// 只是它每一手都打得「恰好」正确）。
var viewFields = []string{"Level", "Seat", "Hand", "Counts", "Done", "Leading", "LastSeat", "LastCombo"}

// complainAbout 递归走一个类型的每个字段，报出「能把别家手牌端过来」的地方。
//
// **递归**是后补的：原来只看了 View 这一层，所以 `struct{ G *rules.Game }` 套一层、
// 或者 `map[int][]rules.Card`、`[][]rules.Card` 都拦不住（#259 复审第 4 条）。
//
// 规矩三条：
//
//   - 只有 `View.Hand` 可以是 `[]rules.Card`（自己的手牌）；
//   - `rules.Combo` 放行 —— 它是「已经打出去的那一手」，是牌桌上人人看得见的信息
//     （它的 `Cards` 不再往里走，所以上面那条「只有 Hand 是手牌」不会被它破例）；
//   - 其余的具名规则类型（`rules.Game`、`rules.Tribute`、`rules.Deck`……）一律拒 ——
//     `Game.Hand(seat)` 谁的手牌都给得出来，`Tribute` 手里也攥着四家牌。
//
// 还有：map 一律拒（拆开看也说不清它装的是什么），**func / interface / chan / 裸指针**也一律拒
// （它们什么都装得下，静态看不出里面是什么），指针、切片、数组、嵌套结构体一路走到底。
func complainAbout(t reflect.Type, path string, seen map[reflect.Type]bool) []string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if seen[t] {
		return nil
	}
	seen[t] = true

	var out []string
	switch t.Kind() {
	case reflect.Func, reflect.Interface, reflect.Chan, reflect.UnsafePointer:
		out = append(out, fmt.Sprintf("%s 是 %s —— 这种类型什么都装得下（包括别家的手牌），不许出现在 AI 的输入里", path, t))
	case reflect.Map:
		out = append(out, fmt.Sprintf("%s 是 map（%s）—— 不许用映射装牌", path, t))
		out = append(out, complainAbout(t.Elem(), path+"[值]", seen)...)
	case reflect.Slice, reflect.Array:
		e := t.Elem()
		for e.Kind() == reflect.Ptr {
			e = e.Elem()
		}
		if e.Kind() == reflect.Struct && e.PkgPath() == rulesPkg && e.Name() == "Card" {
			// 只放行 `View.Hand` 这一条路（检查真类型时根路径是 "View"；反例一律按违反算）。
			if path != "View.Hand" {
				out = append(out, fmt.Sprintf("%s 是 %s —— 手牌形状的字段只许有 View.Hand", path, t))
			}
			return out
		}
		out = append(out, complainAbout(e, path+"[]", seen)...)
	case reflect.Struct:
		if t.PkgPath() == rulesPkg {
			if t.Name() != "Combo" {
				out = append(out, fmt.Sprintf("%s 是 rules.%s —— 它能把别家的手牌端过来", path, t.Name()))
			}
			return out
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			// 未导出的字段也走一遍 —— 同包里「顺手」加个私有字段一样能偷看。
			out = append(out, complainAbout(f.Type, path+"."+f.Name, seen)...)
		}
	}
	return out
}

// AI **不许**看得见别家的手牌 —— 用反射把这条钉死。
func TestViewExposesNoOtherHands(t *testing.T) {
	typ := reflect.TypeOf(View{})

	// 自己的手牌要有 —— 不然这条测试是空转（把所有手牌形状都禁掉也能「通过」）。
	if _, ok := typ.FieldByName("Hand"); !ok {
		t.Fatal("View 里没有 Hand —— AI 没法打牌，这条测试也就没意义了")
	}

	// 字段集合钉死。
	var got []string
	for i := 0; i < typ.NumField(); i++ {
		got = append(got, typ.Field(i).Name)
	}
	sorted := append([]string(nil), got...)
	sort.Strings(sorted)
	want := append([]string(nil), viewFields...)
	sort.Strings(want)
	if strings.Join(sorted, ",") != strings.Join(want, ",") {
		t.Fatalf("View 的字段变成了 %v，白名单是 %v —— 加字段要先想清楚「它让 AI 多看见了什么」"+
			"（别家的牌一律不许），再改 viewFields", got, viewFields)
	}

	if out := complainAbout(typ, "View", map[reflect.Type]bool{}); len(out) > 0 {
		t.Fatalf("View 里出现了能装别家手牌的东西：\n  %s", strings.Join(out, "\n  "))
	}
}

// 上面那台检查器自己也要有**反例**兜着 —— 不然「没报错」可能只是它什么都没查。
//
// 每个反例都是审计 #259 第 4 条点出来的那种写法：套一层结构体、用 map、[][]Card、直接塞引擎类型。
func TestViewCheckerCatchesDecoys(t *testing.T) {
	type decoyGame struct{ G *rules.Game }
	type decoyTribute struct{ T *rules.Tribute }
	type decoyMap struct{ Others map[int][]rules.Card }
	type decoyNested struct {
		Info struct{ Hand []rules.Card }
	}
	type decoySliceOfHands struct{ Hands [][]rules.Card }
	type decoyPtrToHands struct{ Hands *[]rules.Card }
	type decoyTributeByValue struct{ T rules.Tribute }
	type decoyFunc struct{ F func() []rules.Card }
	type decoyIface struct{ X any }
	type decoyChan struct{ C chan rules.Card }

	for _, d := range []struct {
		name string
		typ  reflect.Type
	}{
		{"套一层结构体藏 *rules.Game", reflect.TypeOf(decoyGame{})},
		{"套一层结构体藏 *rules.Tribute", reflect.TypeOf(decoyTribute{})},
		{"map[int][]rules.Card", reflect.TypeOf(decoyMap{})},
		{"嵌套结构体里的 []rules.Card", reflect.TypeOf(decoyNested{})},
		{"[][]rules.Card", reflect.TypeOf(decoySliceOfHands{})},
		{"*[]rules.Card", reflect.TypeOf(decoyPtrToHands{})},
		{"Tribute 按值放进来", reflect.TypeOf(decoyTributeByValue{})},
		{"func() []rules.Card", reflect.TypeOf(decoyFunc{})},
		{"any（接口什么都能装）", reflect.TypeOf(decoyIface{})},
		{"chan rules.Card", reflect.TypeOf(decoyChan{})},
	} {
		if out := complainAbout(d.typ, "Decoy", map[reflect.Type]bool{}); len(out) == 0 {
			t.Errorf("%s 都没被检查器认出来 —— 这台检查器是空转的", d.name)
		}
	}
}

// ViewOf 是 View 那条红线的生产实现：给一个座位，只能拿到自己的手牌、各家**张数**和公开信息。
// 结构上的白名单由 TestViewExposesNoOtherHands 钉；这条钉**填进去的值**。
func TestViewOfExposesOnlyPublicInfo(t *testing.T) {
	var hands [rules.Seats][]rules.Card
	for s := 1; s <= rules.Seats; s++ {
		c, err := rules.ParseKey(fmt.Sprintf("♠%d/0", s+2)) // 3、4、5、6 各一张
		if err != nil {
			t.Fatal(err)
		}
		hands[s-1] = []rules.Card{c}
	}
	g, err := rules.NewGame(rules.Two, hands, 1)
	if err != nil {
		t.Fatalf("摆一局：%v", err)
	}

	for seat := 1; seat <= rules.Seats; seat++ {
		v := ViewOf(g, seat)
		if !reflect.DeepEqual(v.Hand, hands[seat-1]) {
			t.Errorf("%d 号座拿到的手牌不是自己的：%v", seat, v.Hand)
		}
		for s := 1; s <= rules.Seats; s++ {
			if v.Counts[s] != len(hands[s-1]) {
				t.Errorf("%d 号座看 %d 号座：张数该是 %d，得到 %d", seat, s, len(hands[s-1]), v.Counts[s])
			}
		}
		if v.Seat != seat || v.Level != rules.Two {
			t.Errorf("%d 号座的 View 元信息不对：seat=%d level=%v", seat, v.Seat, v.Level)
		}
	}

	// 公开信息：1 号座领出一张之后，别的座位能看到「1 号座出了什么」，但看不到别人的手牌。
	if err := g.Play(1, hands[0], rules.TypeSingle, hands[0][0].Rank); err != nil {
		t.Fatalf("1 号座领出：%v", err)
	}
	v := ViewOf(g, 2)
	if v.Leading || v.LastSeat != 1 || !reflect.DeepEqual(v.LastCombo.Cards, hands[0]) {
		t.Errorf("2 号座该看到 1 号座刚出的那一手：leading=%v lastSeat=%d lastCombo=%v", v.Leading, v.LastSeat, v.LastCombo)
	}
	if !reflect.DeepEqual(v.Hand, hands[1]) {
		t.Errorf("2 号座的手牌被带上了别人的牌：%v", v.Hand)
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
