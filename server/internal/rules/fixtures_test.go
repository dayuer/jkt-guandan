package rules

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 牌例在仓库根的 testdata/rules/（#204 第 1 步）：Go 和客户端 TS 版（#87）读同一批文件，
// 格式见那里的 README.md（那份文档是两端的接口说明）。
//
// 改输入之后跑 `go test ./server/internal/rules/ -update` 重新生成 deal.json 的 hands，
// 再逐条核对改了什么 —— 生成器只会照着现在的实现写，看不出规则错。
//
// **deck.json 的 order 不由 -update 生成**：它是两端的约定，不是期望值（#213 复审）。
// 要是让它跟着实现走，改一次 NewDeck 的顺序、跑一次 -update，约定就被悄悄改掉了。
//
// 每条用例都要有 rule 字段（条款号数组，写法见 README）：这里会检查它非空，
// 免得「依据」只写在 name 的文字里、TS 端没法校验。
var update = flag.Bool("update", false, "按现在的实现重新生成 testdata/rules 里的期望值")

const fixturesDir = "../../../testdata/rules"

func loadFixture(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixturesDir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s：%v", name, err)
	}
}

func saveFixture(t *testing.T, name string, v any) {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixturesDir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("已重写 %s", name)
}

// 条款号的写法（README「条款号的写法」一节那张表，六行一一对应）：
//
//	第二条 14            条 + 条目
//	第四条 一             条 + 款
//	第四条 一、2          条 + 款 + 条目
//	第四条 二、（一）      条 + 款 +（乙）（只引到「（乙）」）
//	第四条 二、（一）4     条 + 款 +（乙）+ 条目
//	第七条                整条
//
// 校验是为了挡住手写时的笔误：写错一个字的条款号比不写更糟 —— 看着像有依据，
// 翻原文找不到（#213 复审：原来只查了数组非空，`[""]` 照样过）。
var ruleRe = regexp.MustCompile(
	`^第[一二三四五六七八九十]+条( [一二三四五六七八九十]+(、（[一二三四五六七八九十]+）[0-9]*|、[0-9]+)?| [0-9]+)?$`)

// checkRules 查一条用例的 rule 字段：至少一条、每条非空、写法对。
func checkRules(t *testing.T, what string, rules []string) {
	t.Helper()
	if len(rules) == 0 {
		t.Fatalf("%s：没写 rule（条款号）", what)
	}
	for _, r := range rules {
		if strings.TrimSpace(r) == "" {
			t.Fatalf("%s：rule 里有空字符串 —— 要么写条款号，要么整条不要写", what)
		}
		if !ruleRe.MatchString(r) {
			t.Fatalf("%s：条款号 %q 的写法不对（见 testdata/rules/README.md 的「条款号的写法」）", what, r)
		}
	}
}

// 整副牌的顺序：两端必须一样，否则同一串牌例在两边指的不是同一张牌。
type deckFixture struct {
	Comment []string `json:"_comment"`
	Rule    []string `json:"rule"`
	Order   []string `json:"order"`
}

func deckKeys(deck []Card) []string {
	keys := make([]string, len(deck))
	for i, c := range deck {
		keys[i] = c.Key()
	}
	return keys
}

func TestDeckFixture(t *testing.T) {
	var f deckFixture
	loadFixture(t, "deck.json", &f)
	deck := NewDeck()
	want := deckKeys(deck)

	checkRules(t, "deck.json", f.Rule)
	if len(f.Order) != len(want) {
		t.Fatalf("牌例写了 %d 张，整副牌有 %d 张", len(f.Order), len(want))
	}
	for i := range want {
		if f.Order[i] != want[i] {
			t.Fatalf("第 %d 张：牌例是 %s，实现是 %s", i, f.Order[i], want[i])
		}
	}
	// 牌例里的写法要能读回同一张牌（ID 由位置定，不写在 Key 里）。
	for i, key := range f.Order {
		c, err := ParseKey(key)
		if err != nil {
			t.Fatalf("第 %d 张 %s：%v", i, key, err)
		}
		if c.Rank != deck[i].Rank || c.Suit != deck[i].Suit || c.Deck != deck[i].Deck {
			t.Fatalf("第 %d 张 %s 读回来是 %s", i, key, c.Key())
		}
	}
}

// 牌点大小（第四条 一）：一条用例一个级数，order 是从大到小的 15 个牌点。
type powerVector struct {
	Name  string   `json:"name"`
	Rule  []string `json:"rule"`
	Level string   `json:"level"`
	Order []string `json:"order"`
}

type powerFixture struct {
	Comment []string      `json:"_comment"`
	Vectors []powerVector `json:"vectors"`
}

func TestPowerFixture(t *testing.T) {
	var f powerFixture
	loadFixture(t, "power.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	for _, v := range f.Vectors {
		checkRules(t, v.Name, v.Rule)
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		if len(v.Order) != 15 {
			t.Fatalf("%s：列了 %d 个牌点，应该是 15 个（2–A + 大小王）", v.Name, len(v.Order))
		}
		seen := map[Rank]bool{}
		for i, name := range v.Order {
			r, err := ParseRank(name)
			if err != nil {
				t.Fatalf("%s：%v", v.Name, err)
			}
			if seen[r] {
				t.Fatalf("%s：牌点 %s 出现了两次", v.Name, name)
			}
			seen[r] = true
			if i == 0 {
				continue
			}
			prev, _ := ParseRank(v.Order[i-1])
			if prev.Power(level) <= r.Power(level) {
				t.Fatalf("%s：打 %s 时 %s 应比 %s 大（第四条 一）", v.Name, v.Level, v.Order[i-1], name)
			}
		}
	}
}

// 发牌（第七条 一、5）：shuffle 是发牌顺序，hands 是从首抓者起逆时针的四手牌。
type dealVector struct {
	Name    string     `json:"name"`
	Rule    []string   `json:"rule"`
	Shuffle []int      `json:"shuffle"`
	Hands   [][]string `json:"hands"`
}

type dealFixture struct {
	Comment []string     `json:"_comment"`
	Vectors []dealVector `json:"vectors"`
}

func TestDealFixture(t *testing.T) {
	var f dealFixture
	loadFixture(t, "deal.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	for i := range f.Vectors {
		v := &f.Vectors[i]
		checkRules(t, v.Name, v.Rule)
		hands, err := Deal(PermShuffler(v.Shuffle))
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}

		// 结构上的硬条件，和牌例写的是什么无关：每人 27 张、四手合起来正好是整副 108 张、
		// 没有一张重复（第二条 6：每名运动员 27 张）。
		all := map[int]bool{}
		for seat, hand := range hands {
			if len(hand) != 27 {
				t.Fatalf("%s：从首抓者起第 %d 家发了 %d 张，应该 27 张", v.Name, seat+1, len(hand))
			}
			for _, c := range hand {
				if all[c.ID] {
					t.Fatalf("%s：第 %d 张牌发了两次", v.Name, c.ID)
				}
				all[c.ID] = true
			}
		}
		if len(all) != 108 {
			t.Fatalf("%s：四手牌合起来 %d 张，应该 108 张", v.Name, len(all))
		}

		got := make([][]string, len(hands))
		for seat, hand := range hands {
			got[seat] = deckKeys(hand)
		}
		if *update {
			v.Hands = got
			continue
		}
		if !reflect.DeepEqual(v.Hands, got) {
			t.Fatalf("%s：发到的牌和牌例不一样\n算出 %s\n牌例 %s", v.Name, compact(t, got), compact(t, v.Hands))
		}
	}
	if *update {
		saveFixture(t, "deal.json", f)
	}
}

// 合法出法（#204 第 4 步）：一手牌 + 级数（+ 上一手）→ 全部能打出去的打法。
//
// 牌例里每一手写成 {kind, rank, size}（+ 有逢人配时带 wilds），比 Moves 算出来的**顺序也一致**
// （和 combos.json 一个规矩：先 kind、再 rank、再 size、再 wilds）。
type moveExpect struct {
	Kind  string     `json:"kind"`
	Rank  string     `json:"rank"`
	Size  int        `json:"size"`
	Wilds []wildDecl `json:"wilds,omitempty"`
}

type movesVector struct {
	Name  string       `json:"name"`
	Rule  []string     `json:"rule"`
	Level string       `json:"level"`
	Hand  []string     `json:"hand"`
	Lead  *compareSide `json:"lead"`
	Moves []moveExpect `json:"moves"`
}

type movesFixture struct {
	Comment []string      `json:"_comment"`
	Vectors []movesVector `json:"vectors"`
}

// leadCombo 读出牌例里写的「上一手」。牌型一定对得上，牌面写了就一起比（和 compare.json 一样）。
func leadCombo(t *testing.T, v movesVector, lead compareSide, level Rank) Combo {
	t.Helper()
	kind, err := ParseComboType(lead.Kind)
	if err != nil {
		t.Fatalf("%s：上一手 %v", v.Name, err)
	}
	rank := Rank(0)
	if lead.Rank != "" {
		if rank, err = ParseRank(lead.Rank); err != nil {
			t.Fatalf("%s：上一手 %v", v.Name, err)
		}
	}
	combos, err := Recognize(cardsOf(t, lead.Cards), level)
	if err != nil {
		t.Fatalf("%s：上一手：%v", v.Name, err)
	}
	c, ok := Pick(combos, kind, rank)
	if !ok {
		t.Fatalf("%s：上一手认不出来（%s%s，%v）—— 实际有 %s", v.Name, lead.Kind, lead.Rank, lead.Cards, ComboSummary(combos))
	}
	return c
}

func TestMovesFixture(t *testing.T) {
	var f movesFixture
	loadFixture(t, "moves.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	for _, v := range f.Vectors {
		checkRules(t, v.Name, v.Rule)
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		hand := cardsOf(t, v.Hand)
		var lead *Combo
		if v.Lead != nil {
			lc := leadCombo(t, v, *v.Lead, level)
			lead = &lc
		}
		moves, err := Moves(hand, level, lead)
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		if len(moves) != len(v.Moves) {
			t.Fatalf("%s（打 %s，%v）：算出 %d 手，牌例 %d 手\n算出 %s\n牌例 %s",
				v.Name, v.Level, v.Hand, len(moves), len(v.Moves), movesText(moves), wantsText(v.Moves))
		}
		for i, want := range v.Moves {
			got := moves[i]
			if got.Type.String() != want.Kind || got.Rank.RankName() != want.Rank || len(got.Cards) != want.Size {
				t.Fatalf("%s：第 %d 手不一样\n算出 %s\n牌例 %s\n（全部）算出 %s\n（全部）牌例 %s",
					v.Name, i+1, moveText(got), wantText(want), movesText(moves), wantsText(v.Moves))
			}
			if len(got.Wilds) != len(want.Wilds) {
				t.Fatalf("%s：第 %d 手的顶法不一样\n算出 %s\n牌例 %s", v.Name, i+1, moveText(got), wantText(want))
			}
			for k, w := range want.Wilds {
				if got.Wilds[k].Card.Key() != w.Card || asKey(got.Wilds[k].As) != w.As {
					t.Fatalf("%s：第 %d 手的顶法不一样\n算出 %s\n牌例 %s", v.Name, i+1, moveText(got), wantText(want))
				}
			}
		}
	}
}

func moveText(c Combo) string {
	out := c.Type.String() + " " + c.Rank.RankName() + " " + strconv.Itoa(len(c.Cards)) + " 张"
	for _, w := range c.Wilds {
		out += "（" + w.Card.Key() + " 当 " + asKey(w.As) + "）"
	}
	return out
}

func wantText(w moveExpect) string {
	out := w.Kind + " " + w.Rank + " " + strconv.Itoa(w.Size) + " 张"
	for _, d := range w.Wilds {
		out += "（" + d.Card + " 当 " + d.As + "）"
	}
	return out
}

func movesText(moves []Combo) string {
	out := ""
	for i, c := range moves {
		if i > 0 {
			out += "；"
		}
		out += moveText(c)
	}
	if out == "" {
		out = "（空）"
	}
	return out
}

func wantsText(moves []moveExpect) string {
	out := ""
	for i, c := range moves {
		if i > 0 {
			out += "；"
		}
		out += wantText(c)
	}
	if out == "" {
		out = "（空）"
	}
	return out
}

func compact(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%.200s", b)
}

// 牌型识别（第三条）：一手牌 + 级数 → 全部合法解读。
type interpretation struct {
	Kind string `json:"kind"`
	Rank string `json:"rank"`
	// Wilds 是可选的：这几种解读里，哪张逢人配顶成了什么（`as` 不带副号，见 README）。
	// 写了就要求引擎选的正是这一种。
	Wilds []wildDecl `json:"wilds,omitempty"`
}

type wildDecl struct {
	Card string `json:"card"`
	As   string `json:"as"`
}

type comboVector struct {
	Name   string           `json:"name"`
	Rule   []string         `json:"rule"`
	Level  string           `json:"level"`
	Cards  []string         `json:"cards"`
	Combos []interpretation `json:"combos"`
}

// checkWildDecls 查牌例里声明的「哪张逢人配顶成了什么」：写了就要求引擎选的正是这一种
// （两端必须给出同一个答案，不能一边挑点数最小的、一边随便挑）。
func checkWildDecls(t *testing.T, v comboVector, combos []Combo) {
	t.Helper()
	for _, want := range v.Combos {
		if len(want.Wilds) == 0 {
			continue // 这条解读没声明顶法，不查（大多数用例顶法是唯一的，没必要每条都写）
		}
		var found *Combo
		for i := range combos {
			if combos[i].Type.String() == want.Kind && combos[i].Rank.RankName() == want.Rank {
				found = &combos[i]
				break
			}
		}
		if found == nil {
			continue // 牌型/牌面对不上由主断言报，这里只管顶法
		}
		got := make([]string, len(found.Wilds))
		for i, w := range found.Wilds {
			got[i] = w.Card.Key() + " 当 " + asKey(w.As)
		}
		sort.Strings(got)
		decl := make([]string, len(want.Wilds))
		for i, w := range want.Wilds {
			decl[i] = w.Card + " 当 " + w.As
		}
		sort.Strings(decl)
		if !reflect.DeepEqual(got, decl) {
			t.Fatalf("%s：%s %s 的顶法不一样\n算出 %v\n牌例 %v", v.Name, want.Kind, want.Rank, got, decl)
		}
	}
}

type comboFixture struct {
	Comment []string      `json:"_comment"`
	Vectors []comboVector `json:"vectors"`
}

// cardsOf 按牌例的写法取牌（带 ID，见 ParseKey）。
func cardsOf(t *testing.T, keys []string) []Card {
	t.Helper()
	out := make([]Card, len(keys))
	for i, k := range keys {
		c, err := ParseKey(k)
		if err != nil {
			t.Fatalf("牌 %q：%v", k, err)
		}
		out[i] = c
	}
	return out
}

// interpretations 把解读写成牌例的样子（四大天王没有点数，rank 是空串）。
func interpretations(combos []Combo) []interpretation {
	out := make([]interpretation, len(combos))
	for i, c := range combos {
		rank := ""
		if c.Rank != 0 {
			rank = c.Rank.RankName()
		}
		out[i] = interpretation{Kind: c.Type.String(), Rank: rank}
	}
	sortInterpretations(out)
	return out
}

// 比对前按 kind、rank 排一遍：两端只约定「有哪些解读」，不约定顺序。
func sortInterpretations(list []interpretation) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Kind != list[j].Kind {
			return list[i].Kind < list[j].Kind
		}
		return list[i].Rank < list[j].Rank
	})
}

func interpretText(list []interpretation) string {
	if len(list) == 0 {
		return "打不出去"
	}
	parts := make([]string, len(list))
	for i, x := range list {
		parts[i] = x.Kind + " " + x.Rank
	}
	return strings.Join(parts, "、")
}

func TestCombosFixture(t *testing.T) {
	var f comboFixture
	loadFixture(t, "combos.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	for _, v := range f.Vectors {
		checkRules(t, v.Name, v.Rule)
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		combos, err := Recognize(cardsOf(t, v.Cards), level)
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		checkWildDecls(t, v, combos)
		got := interpretations(combos)
		// 主断言只比牌型和牌面：`wilds`（哪张顶成了什么）是可选的，由 checkWildDecls 单独查 ——
		// 大多数用例的顶法是唯一的，没必要每条都写一遍。
		want := append([]interpretation{}, v.Combos...)
		for i := range want {
			want[i].Wilds = nil
		}
		sortInterpretations(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s（%v，打 %s）：\n算出 %s\n牌例 %s", v.Name, v.Cards, v.Level, interpretText(got), interpretText(want))
		}
	}
}

// 比较（第四条二）：两边都写完整的一手牌 + 声明的牌型（带逢人配时要连牌面一起声明）。
type compareSide struct {
	Kind  string   `json:"kind"`
	Cards []string `json:"cards"`
	// Rank 是声明的牌面，只有带逢人配、同一种牌型有好几种点数时才要写。
	Rank string `json:"rank"`
}

type compareVector struct {
	Name  string      `json:"name"`
	Rule  []string    `json:"rule"`
	Level string      `json:"level"`
	A     compareSide `json:"a"`
	B     compareSide `json:"b"`
	// Result 是 a 和 b 的关系：a / b / equal / incomparable（#216 复审：布尔值分不出
	// 「一样大」和「牌型不同没得比」）。两个方向都要成立，下面两个断言各测一遍。
	Result string `json:"result"`
}

type compareFixture struct {
	Comment []string        `json:"_comment"`
	Vectors []compareVector `json:"vectors"`
}

// sideCombo 按声明的解读认这手牌：牌型一定对得上，牌面写了就一起比。认不出来就是牌例自己写错了。
func sideCombo(t *testing.T, v compareVector, which string, side compareSide, level Rank) Combo {
	t.Helper()
	kind, err := ParseComboType(side.Kind)
	if err != nil {
		t.Fatalf("%s：%s：%v", v.Name, which, err)
	}
	rank := Rank(0)
	if side.Rank != "" {
		if rank, err = ParseRank(side.Rank); err != nil {
			t.Fatalf("%s：%s：%v", v.Name, which, err)
		}
	}
	combos, err := Recognize(cardsOf(t, side.Cards), level)
	if err != nil {
		t.Fatalf("%s：%s：%v", v.Name, which, err)
	}
	c, ok := Pick(combos, kind, rank)
	if !ok {
		t.Fatalf("%s：%s 这手牌声明的解读认不出来（%s%s，%v）—— 实际有 %s", v.Name, which,
			side.Kind, side.Rank, side.Cards, ComboSummary(combos))
	}
	return c
}

func TestCompareFixture(t *testing.T) {
	var f compareFixture
	loadFixture(t, "compare.json", &f)
	if len(f.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	for _, v := range f.Vectors {
		checkRules(t, v.Name, v.Rule)
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		want, err := parseOrder(v.Result)
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		a := sideCombo(t, v, "a", v.A, level)
		b := sideCombo(t, v, "b", v.B, level)
		if got := a.Compare(b); got != want {
			t.Fatalf("%s：a=%s b=%s，算出 %s，牌例是 %s", v.Name, a, b, got, want)
		}
		// 反方向也要对：a 大就 b 小，一样大就还是一样大，没得比就还是没得比。
		// 这样「压不了」的三种情况各自都有两个方向的用例，不会只测一个方向。
		if got := b.Compare(a); got != reverseOrder(want) {
			t.Fatalf("%s（反方向）：b=%s a=%s，算出 %s，应当是 %s", v.Name, b, a, got, reverseOrder(want))
		}
	}
}

// parseOrder 读牌例里的 result。
func parseOrder(s string) (Order, error) {
	switch s {
	case "a":
		return Greater, nil
	case "b":
		return Less, nil
	case "equal":
		return Equal, nil
	case "incomparable":
		return Incomparable, nil
	}
	return Incomparable, fmt.Errorf("result 只能是 a / b / equal / incomparable，写的是 %q", s)
}

func reverseOrder(o Order) Order {
	switch o {
	case Greater:
		return Less
	case Less:
		return Greater
	}
	return o
}
