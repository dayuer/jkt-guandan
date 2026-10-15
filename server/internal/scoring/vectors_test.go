package scoring

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"reflect"
	"testing"
)

// testdata/vectors.json 是两端共用的规则对照用例（#174）：小游戏的预览照抄了这份规则
// （app/minigame/src/lib/scoring.ts），vitest 读同一个文件。哪边改了规则对不上，CI 就红。
//
// 改规则时：先改 input，再 `go test ./server/internal/scoring/ -run TestVectors -update`
// 重新生成 expect，逐条核对 diff。
var updateVectors = flag.Bool("update", false, "按 Compute 重新生成 testdata/vectors.json 里的 expect")

const vectorsPath = "testdata/vectors.json"

type vectorEdit struct {
	RoundNo int `json:"round_no"`
	Round
}

type vector struct {
	Name     string          `json:"name"`
	Settings Settings        `json:"settings"`
	Rounds   []Round         `json:"rounds"`
	Edit     *vectorEdit     `json:"edit,omitempty"`
	Expect   json.RawMessage `json:"expect"`
}

type vectorFile struct {
	Comment []string `json:"_comment"`
	Vectors []vector `json:"vectors"`
}

// facts 是这条用例最终要算的每局事实：有 edit 就把那一局换掉。
func (v vector) facts(t *testing.T) []Round {
	facts := append([]Round{}, v.Rounds...)
	if v.Edit != nil {
		if v.Edit.RoundNo < 1 || v.Edit.RoundNo > len(facts) {
			t.Fatalf("%s：edit.round_no %d 超出 1–%d", v.Name, v.Edit.RoundNo, len(facts))
		}
		facts[v.Edit.RoundNo-1] = v.Edit.Round
	}
	return facts
}

// canonical 把 JSON 解开再比，和字段顺序、空白无关；多一个或少一个字段都算不同。
func canonical(t *testing.T, b []byte) any {
	t.Helper()
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		t.Fatalf("解析 JSON：%v", err)
	}
	return x
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var vf vectorFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&vf); err != nil {
		t.Fatalf("%s：%v", vectorsPath, err)
	}
	if len(vf.Vectors) == 0 {
		t.Fatal("没有用例")
	}
	names := map[string]bool{}
	for i := range vf.Vectors {
		v := &vf.Vectors[i]
		if names[v.Name] {
			t.Fatalf("用例重名：%s", v.Name)
		}
		names[v.Name] = true
		res, err := Compute(v.Settings, v.facts(t))
		if err != nil {
			t.Fatalf("%s：%v", v.Name, err)
		}
		got, err := json.Marshal(res)
		if err != nil {
			t.Fatal(err)
		}
		if *updateVectors {
			v.Expect = got
			continue
		}
		if len(v.Expect) == 0 || string(v.Expect) == "null" {
			t.Errorf("%s：没有 expect，先跑 -update", v.Name)
			continue
		}
		if !reflect.DeepEqual(canonical(t, got), canonical(t, v.Expect)) {
			t.Errorf("%s：\n算出 %s\n应为 %s", v.Name, got, compact(t, v.Expect))
		}
	}
	if *updateVectors {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", " ")
		if err := enc.Encode(vf); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(vectorsPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("已重写 %s（%d 条）", vectorsPath, len(vf.Vectors))
	}
}

func compact(t *testing.T, b []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
