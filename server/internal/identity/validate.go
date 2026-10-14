package identity

import (
	"errors"

	"github.com/dayuer/jkt-guandan/server/internal/moderate"
	"github.com/dayuer/jkt-guandan/server/internal/textclean"
)

// NicknameMax 是称呼的最大长度（§12.2：1–12 个字符）。
const NicknameMax = 12

// ErrNicknameInvalid 表示称呼不合法。
var ErrNicknameInvalid = errors.New("称呼需要 1–12 个字符，不能只有空格")

// NormalizeNickname 去掉首尾空白、控制字符和零宽/BiDi 格式字符，检查长度和敏感词。
// 返回值是入库的形式；长度不合法时返回 ErrNicknameInvalid，命中敏感词时返回 field 为 "nickname" 的
// *moderate.Hit（errors.Is(err, moderate.ErrSensitive) 成立）。
//
// 称呼和地点、备注一样会公开展示，还会画进小游戏的转发卡片和海报，所以过同一套词表（§14，#147）。
func NormalizeNickname(raw string) (string, error) {
	cleaned := textclean.Clean(raw)

	// 按字符数算，不是字节数：一个汉字算一个字符。
	if n := len([]rune(cleaned)); n < 1 || n > NicknameMax {
		return "", ErrNicknameInvalid
	}
	// 和牌局一样放在长度检查之后：先说「太长了」比先说「别写钱」更贴切。
	if hit := moderate.Check("nickname", cleaned); hit != nil {
		return "", hit
	}
	return cleaned, nil
}
