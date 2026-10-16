package rules

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 牌例在仓库根的 testdata/rules/（#204 第 1 步）：Go 和客户端 TS 版（#87）读同一批文件，
// 格式见那里的 README.md。
//
// 改输入之后跑 `go test ./server/internal/rules/ -update` 重新生成写死的期望值
// （deck.json 的 order、deal.json 的 hands），再逐条核对 git diff —— 生成器只会照着现在的
// 实现写，看不出规则错。
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

// 整副牌的顺序：两端必须一样，否则同一串牌例在两边指的不是同一张牌。
type deckFixture struct {
	Comment []string `json:"_comment"`
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

	if *update {
		f.Order = want
		saveFixture(t, "deck.json", f)
		return
	}
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

// 牌点大小（第四条一）：一条用例一个级数，order 是从大到小的 15 个牌点。
type powerVector struct {
	Name  string   `json:"name"`
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
				t.Fatalf("%s：打 %s 时 %s 应比 %s 大（第四条一）", v.Name, v.Level, v.Order[i-1], name)
			}
		}
	}
}

// 发牌（第七条一）：shuffle 是出牌顺序，hands 是按座位发的四手牌。
type dealVector struct {
	Name    string     `json:"name"`
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
		hands, err := Deal(PermShuffler(v.Shuffle))
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}

		// 结构上的硬条件，和牌例写的是什么无关：每人 27 张、四手合起来正好是整副 108 张、
		// 没有一张重复（第二条 6：每名运动员 27 张）。
		all := map[int]bool{}
		for seat, hand := range hands {
			if len(hand) != 27 {
				t.Fatalf("%s：%d 号座发了 %d 张，应该 27 张", v.Name, seat+1, len(hand))
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
}

type comboVector struct {
	Name   string           `json:"name"`
	Level  string           `json:"level"`
	Cards  []string         `json:"cards"`
	Combos []interpretation `json:"combos"`
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
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		combos, err := Recognize(cardsOf(t, v.Cards), level)
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		got := interpretations(combos)
		want := append([]interpretation{}, v.Combos...)
		sortInterpretations(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s（%v，打 %s）：\n算出 %s\n牌例 %s", v.Name, v.Cards, v.Level, interpretText(got), interpretText(want))
		}
	}
}

// 比较（第四条二）：两边都写完整的一手牌 + 声明的牌型。
type compareSide struct {
	Kind  string   `json:"kind"`
	Cards []string `json:"cards"`
}

type compareVector struct {
	Name  string      `json:"name"`
	Level string      `json:"level"`
	A     compareSide `json:"a"`
	B     compareSide `json:"b"`
	Beats bool        `json:"beats"`
}

type compareFixture struct {
	Comment []string        `json:"_comment"`
	Vectors []compareVector `json:"vectors"`
}

// sideCombo 按声明的牌型认这手牌：认不出来就是牌例自己写错了。
func sideCombo(t *testing.T, v compareVector, which string, side compareSide, level Rank) Combo {
	t.Helper()
	kind, err := ParseComboType(side.Kind)
	if err != nil {
		t.Fatalf("%s：%s：%v", v.Name, which, err)
	}
	c, ok, err := FindCombo(cardsOf(t, side.Cards), level, kind)
	if err != nil {
		t.Fatalf("%s：%s：%v", v.Name, which, err)
	}
	if !ok {
		t.Fatalf("%s：%s 这手牌打不出声明的 %s（%v）", v.Name, which, side.Kind, side.Cards)
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
		level, err := ParseRank(v.Level)
		if err != nil {
			t.Fatalf("%s：级数 %q：%v", v.Name, v.Level, err)
		}
		a := sideCombo(t, v, "a", v.A, level)
		b := sideCombo(t, v, "b", v.B, level)
		if got := a.Beats(b); got != v.Beats {
			t.Fatalf("%s：a=%s b=%s，算出 %v，牌例是 %v", v.Name, a, b, got, v.Beats)
		}
	}
}
