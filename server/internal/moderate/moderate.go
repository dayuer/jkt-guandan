// Package moderate 是 §14 的敏感词过滤：称呼、牌局的备注和地点命中词表时拒绝提交。
//
// 印尼对赌博管得严（UU 7/1974、KUHP），页面上虽然已经写了「不设彩头、不涉及任何金钱
// 输赢」，但用户还是可能在备注里写「一把 50 块钱」「judi santai」。这里挡住的是
// **明显**的金钱和赌博字眼，不是安全边界：它降低的是「这个站是赌局」的印象和举报风险，
// 挡不住存心绕的人，也不该挡到正常约局（词表里因此不放 bandar 这类既是地名又是
// 「庄家」的词——「Bandar Djakarta」是雅加达有名的海鲜餐厅）。
//
// 词表在 words.txt，改词不用改代码、不用发版：一行一个词，`#` 开头是注释。
package moderate

import (
	_ "embed"
	"errors"
	"strings"
	"unicode"
)

// ErrSensitive 是这一类错误的哨兵，调用方用 errors.Is 判断。
var ErrSensitive = errors.New("moderate: 内容涉及金钱或赌博")

// Message 是命中的时候给用户看的那句话。命中哪个词不告诉用户，只进日志（和微信那版一样）：
// 说了反而等于教人怎么绕。牌局的地点、备注和称呼用的是同一句，前端不用区分来源。
const Message = "请勿涉及金钱：这里只约纯娱乐局"

// Hit 说明是哪个字段命中了哪个词。字段名用请求体里的 JSON 名（venue / note / nickname），
// 前端据此把提示放在对应的输入框下面（原型 X1）。
type Hit struct {
	Field string
	Word  string
}

func (h *Hit) Error() string {
	return "「" + h.Field + "」命中敏感词「" + h.Word + "」"
}

// Unwrap 让 errors.Is(err, ErrSensitive) 成立。
func (h *Hit) Unwrap() error { return ErrSensitive }

// fieldExcept 是「某个词在某个字段里不查」的例外表（#147）。
//
// 目前只有一条：称呼里的单字「钱」。「钱」是百家姓里的常见姓，「老钱」「钱哥」
// 「钱小姐」都是真实的人名，出现在称呼里远比出现在地点、备注里常见，误伤的代价
// 也大得多——被拦的人不会再解释一遍，他直接换个名字走。
//
// 只跳过**这一个词条**：词表里「赌钱」「赢钱」「输钱」「赌」这些都照旧拦，
// 所以「赌神」「judi 50rb」在称呼里照样过不去；地点、备注完全不受影响。
var fieldExcept = map[string]map[string]bool{
	"nickname": {"钱": true},
}

// Check 检查一个字段。没命中时返回 nil。
func Check(field, text string) *Hit {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	// folded：小写 + 繁体转简体 + 全角转半角。
	folded := fold(text)
	// 汉字没有词边界，而且有人会加空格、加连字符来绕（「赌 博」「赌-博」），
	// 所以去掉这些分隔符之后再比子串。
	squeezed := squeeze(folded)

	except := fieldExcept[field]
	for _, w := range words {
		if except[w.text] {
			continue
		}
		if w.ascii {
			// 拉丁词按整词比：「bet」不该命中「better」，也不该命中「alphabet」。
			if hasWord(folded, w.text) {
				return &Hit{Field: field, Word: w.text}
			}
			continue
		}
		if strings.Contains(squeezed, w.text) {
			return &Hit{Field: field, Word: w.text}
		}
	}
	return nil
}

// TraditionalPairs 返回繁简映射的条目数，给测试用（确认 traditional.txt 被读进来了）。
func TraditionalPairs() int { return len(tradToSimp) }

// Text 返回词表里所有的词（按文件顺序），给测试和排查用。
func Text() []string {
	out := make([]string, 0, len(words))
	for _, w := range words {
		out = append(out, w.text)
	}
	return out
}

//go:embed words.txt
var rawList string

//go:embed traditional.txt
var rawTraditional string

// tradToSimp 是繁体 → 简体的字形映射，见 traditional.txt。
var tradToSimp = parsePairs(rawTraditional)

// parsePairs 读 `繁=简` 的行，空行和 `#` 开头的行忽略。
func parsePairs(raw string) map[rune]rune {
	out := make(map[rune]rune)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		from := []rune(strings.TrimSpace(parts[0]))
		to := []rune(strings.TrimSpace(parts[1]))
		if len(from) != 1 || len(to) != 1 {
			continue
		}
		out[from[0]] = to[0]
	}
	return out
}

// simplify 把繁体字换成简体、全角换成半角。
func simplify(s string) string {
	return strings.Map(func(r rune) rune {
		if simp, ok := tradToSimp[r]; ok {
			return simp
		}
		// 全角拉丁字母和数字（ＡＢＣ１２３）：有人拿它绕整词匹配。
		if r >= '\uFF01' && r <= '\uFF5E' {
			return r - 0xFEE0
		}
		return r
	}, s)
}

type word struct {
	text  string
	ascii bool
}

var words = parse(rawList)

// fold 把文本拉回「词表写在里面的那个形状」：小写、繁体转简体、全角转半角。
//
// 少了这一步，同一个词换个字形就能绕过过滤——「賭博」挡住不了「赌」，
// 尽管它们对用户来说是同一个词（雅加达用繁体输入法的人不少）。
func fold(s string) string {
	return simplify(strings.ToLower(s))
}

// parse 读词表：一行一个词，空行和 `#` 开头的行忽略。
// 纯 ASCII 的词按整词匹配，含其他字符的（汉字等）按子串匹配。
func parse(raw string) []word {
	var out []word
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// 词表里的词也过一遍 fold：有人直接写繁体进来也照样能匹配。
		lowered := fold(line)
		out = append(out, word{text: lowered, ascii: isASCII(lowered)})
	}
	return out
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

// squeeze 去掉空白、常见分隔符和零宽字符。只是为了比对，不改动用户的原话
// （提示里显示的还是他自己写的内容）。
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		// **所有** Unicode 空白都去掉，不只是 ASCII 空格和 \t\n\r：中文输入法里最顺手的是
		// 全角空格 U+3000，微信里复制出来的还有不换行空格 U+00A0 —— 留一个在中间，
		// 「赢　钱」就绕过了「赢钱」这一条（#147 复审的 B1）。
		if unicode.IsSpace(r) {
			return -1
		}
		switch r {
		case '-', '_', '.', ',', '/', '\\', '|', '+', '*', '·', '、', '，':
			return -1
		case '\u200b', '\u200c', '\u200d', '\ufeff': // 零宽字符（含 BOM）
			return -1
		}
		return r
	}, s)
}

// hasWord 判断 sub 是否作为整词出现在 s 里：两边紧挨着的字符不是「词里的字」就算词边界
// （见 isWordRune：汉字、假名、谚文都算边界）。
func hasWord(s, sub string) bool {
	rs, rsub := []rune(s), []rune(sub)
	if len(rsub) == 0 || len(rsub) > len(rs) {
		return false
	}
	for i := 0; i+len(rsub) <= len(rs); i++ {
		if string(rs[i:i+len(rsub)]) != sub {
			continue
		}
		if i > 0 && isWordRune(rs[i-1]) {
			continue
		}
		if i+len(rsub) < len(rs) && isWordRune(rs[i+len(rsub)]) {
			continue
		}
		return true
	}
	return false
}

// isWordRune 判断 r 会不会和旁边的拉丁字母连成一个词。汉字、日文假名、韩文谚文不会：
// 中文里夹拉丁词不加空格（「测试judi」「带点taruhan」），把汉字当成词的一部分，整词匹配就永远
// 匹配不上（#130）。拉丁字母和数字照旧算词的一部分，所以 bet 仍然不会命中 better、alphabet、Betawi。
func isWordRune(r rune) bool {
	if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
		return false
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
