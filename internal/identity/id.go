package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"time"
)

// crockford 是 Crockford base32 字母表（去掉了 I、L、O、U）。
// ULID 用它编码，26 个字符表示 16 个字节。
var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// newID 生成 26 个字符的 ULID 形状标识：前 6 个字节是毫秒时间戳，
// 后面 10 个字节是随机数。所以它按时间单调递增，适合做数据库主键。
func newID(now time.Time) string {
	var b [16]byte

	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)

	if _, err := rand.Read(b[6:]); err != nil {
		// crypto/rand 读失败时进程已经不可信，这里没有可恢复的余地。
		panic(fmt.Sprintf("生成标识失败: %v", err))
	}
	return crockford.EncodeToString(b[:])
}

// newToken 生成 n 字节随机数，用 URL 安全的 base64 表示，适合放进 URL 或 cookie。
func newToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("生成随机串失败: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// hashToken 是 token 的入库形式：库里从不存明文（§5.2、§10.3）。
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// newClaimCode 生成 4 位数字认领码，作为 token 的兜底（§5.2）。
func newClaimCode() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("生成认领码失败: %v", err))
	}
	// 每个字节取模 10 会有轻微偏差，但码只有 4 位、5 次尝试上限，足够用。
	return string([]byte{
		'0' + b[0]%10,
		'0' + b[1]%10,
		'0' + b[2]%10,
		'0' + b[3]%10,
	})
}
