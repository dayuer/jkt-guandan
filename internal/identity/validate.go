package identity

import (
	"errors"
	"strings"
	"unicode"
)

// NicknameMax 是称呼的最大长度（§12.2：1–12 个字符）。
const NicknameMax = 12

// ErrNicknameInvalid 表示称呼不合法。
var ErrNicknameInvalid = errors.New("称呼需要 1–12 个字符，不能只有空格")

// NormalizeNickname 去掉首尾空白和控制字符，并检查长度。
// 返回值是入库的形式；不合法时返回 ErrNicknameInvalid。
func NormalizeNickname(raw string) (string, error) {
	cleaned := strings.Map(func(r rune) rune {
		// 控制字符（含换行、制表符）一律去掉，免得日志和推送里被注入。
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.TrimSpace(cleaned)

	// 按字符数算，不是字节数：一个汉字算一个字符。
	if n := len([]rune(cleaned)); n < 1 || n > NicknameMax {
		return "", ErrNicknameInvalid
	}
	return cleaned, nil
}
