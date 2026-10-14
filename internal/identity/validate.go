package identity

import (
	"errors"

	"github.com/dayuer/jkt-guandan/internal/textclean"
)

// NicknameMax 是称呼的最大长度（§12.2：1–12 个字符）。
const NicknameMax = 12

// ErrNicknameInvalid 表示称呼不合法。
var ErrNicknameInvalid = errors.New("称呼需要 1–12 个字符，不能只有空格")

// NormalizeNickname 去掉首尾空白、控制字符和零宽/BiDi 格式字符，并检查长度。
// 返回值是入库的形式；不合法时返回 ErrNicknameInvalid。
func NormalizeNickname(raw string) (string, error) {
	cleaned := textclean.Clean(raw)

	// 按字符数算，不是字节数：一个汉字算一个字符。
	if n := len([]rune(cleaned)); n < 1 || n > NicknameMax {
		return "", ErrNicknameInvalid
	}
	return cleaned, nil
}
