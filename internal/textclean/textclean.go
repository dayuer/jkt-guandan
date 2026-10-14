// Package textclean 清理用户输入的自由文本：控制字符、零宽字符、BiDi 覆盖符。
package textclean

import (
	"strings"
	"unicode"
)

const zwj = '\u200d'

// Clean 去掉控制字符（Cc）和格式字符（Cf，含零宽字符、BiDi 覆盖符、BOM），再去首尾空白。
// 唯一例外：夹在两个 emoji 符号之间的零宽连接符（U+200D）保留，免得家庭、职业类 emoji 被拆开。
func Clean(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		switch {
		case r == zwj:
			if i > 0 && i+1 < len(rs) && unicode.IsSymbol(rs[i-1]) && unicode.IsSymbol(rs[i+1]) {
				b.WriteRune(r)
			}
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
