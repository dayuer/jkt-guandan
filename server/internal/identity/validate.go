package identity

import (
	"errors"

	"github.com/dayuer/jkt-guandan/server/internal/moderate"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/textclean"
)

// NicknameMax 是称呼的最大长度（§12.2：1–12 个字符）。
const NicknameMax = 12

// ErrNicknameInvalid 表示称呼不合法。
var ErrNicknameInvalid = errors.New("称呼需要 1–12 个字符，不能只有空格")

// ErrNicknameReserved 表示用了保留的称呼。
var ErrNicknameReserved = errors.New("这个称呼不能用")

// NormalizeNickname 去掉首尾空白、控制字符和零宽/BiDi 格式字符，再检查长度、保留名和敏感词。
// 返回值是入库的形式；不合法时返回 ErrNicknameInvalid，是保留名时返回 ErrNicknameReserved，
// 命中敏感词时返回 *moderate.Hit（errors.Is(err, moderate.ErrSensitive) 成立，Field 是 nickname）。
//
// 这几项检查都放在这里，因为它们对**三条路**都成立：H5 新建（CreateOrGet）、
// 小游戏新建（WxLogin）、改称呼（ChangeNickname）。保留名是 #221 挪进来的
// （之前只有改称呼那一支挡了，于是可以新建一个叫「已注销牌友」的身份去冒充注销的人）；
// 敏感词是 #147 加上的，之前只有地点和备注过词表，称呼能直接写上「judi 50rb」，
// 再被画进转发卡片和海报。
func NormalizeNickname(raw string) (string, error) {
	cleaned := textclean.Clean(raw)

	// 按字符数算，不是字节数：一个汉字算一个字符。
	if n := len([]rune(cleaned)); n < 1 || n > NicknameMax {
		return "", ErrNicknameInvalid
	}
	// 注销之后座位显示成这个称呼（store.DeletedNickname）。先去零宽字符再比，绕不过去。
	if cleaned == store.DeletedNickname {
		return "", ErrNicknameReserved
	}
	// 和地点、备注同一套词表。放在长度检查之后：先报「太长」，再报「别写钱」，
	// 和 games 那边的顺序一致。称呼里只跳过单字「钱」（百家姓里的常见姓，见 moderate.Check）。
	if hit := moderate.Check("nickname", cleaned); hit != nil {
		return "", hit
	}
	return cleaned, nil
}
