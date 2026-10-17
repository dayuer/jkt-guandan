package moderate

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckBlocksGamblingAndMoney(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // 期望命中的词，空表示不该命中
	}{
		// §14 举的例子。
		{"设计稿的例子", "一把 50 块钱，输了请宵夜", "钱"},

		{"中文赌博", "周末打个小赌", "赌"},
		{"筹码", "输的人买筹码", "筹码"},
		{"彩头", "带点彩头好玩", "彩头"},
		{"下注", "可以下注吗", "下注"},

		{"印尼语", "judi online santai", "judi"},
		{"马来语", "taruhan kecil aja", "taruhan"},
		{"英语整词", "small bet ok?", "bet"},

		// 繁体输入法（雅加达华人里不少）：同一个词换个字形不能就绕过去。
		{"繁体钱", "一把50塊錢，輸了請宵夜", "钱"},
		{"繁体赌", "賭博不好", "赌"},
		{"繁体加空格", "賭 博不好", "赌"},
		{"繁体筹码", "輸的人買籌碼", "筹码"},
		{"繁体彩头", "帶點彩頭好玩", "彩头"},
		{"繁体赢钱", "贏錢請宵夜", "钱"},
		{"全角拉丁", "ｓｍａｌｌ ｂｅｔ ｏｋ？", "bet"},

		// 绕过写法：加空格、加连字符、加零宽字符。
		{"中间加空格", "赌 博不好", "赌"},
		{"中间加连字符", "赌-博不好", "赌"},
		{"零宽字符", "赌\u200b博不好", "赌"},

		// #130：拉丁词紧贴着汉字（中文里夹外文不加空格），汉字算词边界。
		{"汉字在前", "测试judi", "judi"},
		{"汉字在后", "judi一下", "judi"},
		{"两边都是汉字", "带点taruhan好玩", "taruhan"},
		{"汉字夹英语", "今晚bet一把", "bet"},
		{"繁体紧贴", "帶點taruhan", "taruhan"},
		{"汉字和全角拉丁紧贴", "来点ｂｅｔ", "bet"},
		{"有空格的照旧", "main judi", "judi"},
		{"日文假名紧贴", "これはjudiです", "judi"},

		// 正常内容不该命中——误伤的代价比漏掉高。
		{"正常地点", "Pacific Place · 鼎泰丰包间", ""},
		{"正常备注", "新手局，随便打打，教规则", ""},
		{"海鲜餐厅", "Bandar Djakarta 二楼包间", ""},
		{"better 里没有 bet", "better safe than sorry", ""},
		{"alphabet 里没有 bet", "alphabet street", ""},
		{"拉丁词不做子串匹配", "betsy's place", ""},
		// #130 之后拉丁字母、数字仍然算词的一部分：英语、印尼语里含敏感词子串的正常词不误伤，
		// 紧贴着汉字时也一样。
		{"Betawi 菜", "Betawi菜 不错", ""},
		{"Tibet", "Tibet 餐厅", ""},
		{"judicial", "judicial review 之后再约", ""},
		{"chipset", "聊chipset的局", ""},
		// chips 不在词表里：英语里也是「薯片」（#130）。中文「筹码」照样拦（见上面）。
		{"薯片", "买点chips", ""},
		{"薯片带空格", "bring chips and drinks", ""},
		{"gambles 之外的 gambit", "开局gambit教学", ""},
		{"wagering 之外的 swagger", "swagger 风格", ""},
		{"数字紧贴", "bet365", ""},
		{"alphabet 紧贴汉字", "alphabet街二楼", ""},
		{"放哪儿", "taruh di mana?", ""},
		// 口径：宁可漏拦不可误拦。这几个词都有人问过要不要加，裁定是不加。
		{"红包", "打完了发个红包，图个乐", ""},
		{"押金", "场地要押金，人均大概 200", ""},
		{"空", "   ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit := Check("note", tc.text)
			if tc.want == "" {
				if hit != nil {
					t.Fatalf("%q 不该命中，却命中了 %q", tc.text, hit.Word)
				}
				return
			}
			if hit == nil {
				t.Fatalf("%q 应当命中 %q", tc.text, tc.want)
			}
			if hit.Word != tc.want {
				t.Fatalf("命中的词是 %q，期望 %q", hit.Word, tc.want)
			}
			if hit.Field != "note" {
				t.Fatalf("字段名是 %q，期望 note", hit.Field)
			}
		})
	}
}

// 繁简映射表本身要能被读进来；词表里写繁体也应当照样匹配。
func TestTraditionalMapIsLoaded(t *testing.T) {
	if n := TraditionalPairs(); n < 8 {
		t.Fatalf("繁简映射只读到 %d 条，traditional.txt 八成没读进来", n)
	}
	// 繁体写法直接用一次：Check 会 fold，映射表和词表都过一遍。
	for _, text := range []string{"賭錢", "籌碼", "贏錢", "抽頭"} {
		if hit := Check("note", text); hit == nil {
			t.Errorf("%q 应当命中", text)
		}
	}
}

// 词表挡的是明显的字眼，不是金额本身：「一把 200」这种没有敏感词、只写数字的写法
// 挡不住，也不打算挡（写「人均 200」是正常需求）。这一点在 words.txt 里也写了。

// 前端要靠字段名把提示放到对应的输入框下面（原型 X1）。
func TestHitCarriesFieldAndUnwraps(t *testing.T) {
	hit := Check("venue", "钱柜包厢")
	if hit == nil {
		t.Fatal("应当命中")
	}
	if hit.Field != "venue" {
		t.Fatalf("字段名是 %q", hit.Field)
	}
	if !errors.Is(hit, ErrSensitive) {
		t.Fatal("errors.Is(err, ErrSensitive) 应当成立")
	}
	if !strings.Contains(hit.Error(), "venue") || !strings.Contains(hit.Error(), hit.Word) {
		t.Fatalf("错误信息里应当有字段名和词：%s", hit.Error())
	}
}

// 词表本身要能被读出来，而且没有空行、没有重复——改词表改错了启动就该知道。
func TestWordListIsSane(t *testing.T) {
	list := Text()
	if len(list) < 10 {
		t.Fatalf("词表只有 %d 个词，八成是没读进来", len(list))
	}
	seen := make(map[string]bool, len(list))
	for _, w := range list {
		if w == "" {
			t.Fatal("词表里有空词")
		}
		if strings.HasPrefix(w, "#") {
			t.Fatalf("注释被当成词了：%q", w)
		}
		if w != strings.ToLower(w) {
			t.Fatalf("词表里的词应当是小写：%q", w)
		}
		if seen[w] {
			t.Fatalf("词表里有重复：%q", w)
		}
		seen[w] = true
	}
}

// #147：称呼里的单字「钱」不查——它是百家姓里的常见姓（老钱、钱哥、钱小姐），
// 在称呼里误伤的代价比地点、备注大得多。只跳过这一个词条，含「钱」的别的词照拦。
func TestCheckFieldExceptionForSurnameQian(t *testing.T) {
	// 称呼里放行：「钱」是姓，不是彩头。
	for _, text := range []string{"钱", "钱哥", "老钱", "钱小姐", "钱明"} {
		if hit := Check("nickname", text); hit != nil {
			t.Errorf("称呼 %q 不该命中，却命中了 %q", text, hit.Word)
		}
	}

	// 例外只针对单字「钱」：词表里其它词在称呼里照拦。
	for _, tc := range []struct{ text, want string }{
		{"赌钱", "赌"},   // 含「钱」，但不是单字那一条
		{"赢钱", "赢钱"},  // 同上
		{"赌神", "赌"},
		{"筹码", "筹码"},
		{"judi 50rb", "judi"},
	} {
		hit := Check("nickname", tc.text)
		if hit == nil {
			t.Errorf("称呼 %q 应当命中 %q", tc.text, tc.want)
			continue
		}
		if hit.Word != tc.want {
			t.Errorf("称呼 %q 命中的是 %q，期望 %q", tc.text, hit.Word, tc.want)
		}
		if hit.Field != "nickname" {
			t.Errorf("字段名是 %q，期望 nickname", hit.Field)
		}
	}

	// 例外不外溢：地点、备注里的单字「钱」照旧拦。
	for _, tc := range []struct{ field, text string }{
		{"venue", "钱柜包厢"},
		{"note", "钱哥定的场子"},
	} {
		if hit := Check(tc.field, tc.text); hit == nil || hit.Word != "钱" {
			t.Errorf("%s %q 应当命中「钱」：%v", tc.field, tc.text, hit)
		}
	}
}
