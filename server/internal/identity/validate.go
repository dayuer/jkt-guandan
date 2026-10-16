package identity

import (
	"errors"

	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/textclean"
)

// NicknameMax 是称呼的最大长度（§12.2：1–12 个字符）。
const NicknameMax = 12

// ErrNicknameInvalid 表示称呼不合法。
var ErrNicknameInvalid = errors.New("称呼需要 1–12 个字符，不能只有空格")

// ErrNicknameReserved 表示用了保留的称呼。
var ErrNicknameReserved = errors.New("这个称呼不能用")

// NormalizeNickname 去掉首尾空白、控制字符和零宽/BiDi 格式字符，再检查长度和保留名。
// 返回值是入库的形式；不合法时返回 ErrNicknameInvalid，是保留名时返回 ErrNicknameReserved。
//
// 保留名检查放在这里（#221），因为它对**三条路**都成立：H5 新建（CreateOrGet）、
// 小游戏新建（WxLogin）、改称呼（ChangeNickname）。之前只有改称呼那一支挡了，
// 于是可以新建一个叫「已注销牌友」的身份去冒充注销的人。
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
	return cleaned, nil
}
