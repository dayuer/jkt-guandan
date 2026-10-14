// Package textclean 清理用户输入的自由文本：控制字符、零宽字符、BiDi 覆盖符。
package textclean

import (
	"strings"
	"unicode"
)

const (
	zwj      = '\u200d'
	vs15     = '\ufe0e' // 变体选择符（Mn，不是 Cf，本来就保留）
	vs16     = '\ufe0f'
	tagFirst = '\U000e0020' // 标签字符，子区域旗（如苏格兰旗）靠它拼出来
	tagLast  = '\U000e007f'
)

func isTag(r rune) bool { return r >= tagFirst && r <= tagLast }

// isEmojiish 是 emoji 序列里能接在 ZWJ 前后的字符：符号（含肤色修饰符）和变体选择符。
func isEmojiish(r rune) bool { return unicode.IsSymbol(r) || r == vs15 || r == vs16 }

// Clean 去掉控制字符（Cc）和格式字符（Cf，含零宽字符、BiDi 覆盖符、BOM），再去首尾空白。
// emoji 序列要用到的两种格式字符例外：夹在两个 emoji 之间的零宽连接符（U+200D），
// 以及接在 emoji 后面的标签字符（U+E0020–E007F）。
func Clean(s string) string {
	rs := []rune(s)
	var b strings.Builder
	var prev rune // 上一个保留下来的字符
	for i, r := range rs {
		switch {
		case r == zwj:
			if !(isEmojiish(prev) && i+1 < len(rs) && unicode.IsSymbol(rs[i+1])) {
				continue
			}
		case isTag(r):
			if !(unicode.IsSymbol(prev) || isTag(prev)) {
				continue
			}
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return strings.TrimSpace(b.String())
}
