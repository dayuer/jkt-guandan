package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
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

// ClaimCodeDigits 是认领码的位数（§5.2 修订：从 4 位改成 6 位）。
const ClaimCodeDigits = 6

// newClaimCode 生成 6 位数字认领码，作为 token 的兜底。
//
// 用 rand.Int 在 [0, 10^6) 上均匀取值，不再对随机字节取模——取模会让小的数字略微偏多。
func newClaimCode() (string, error) {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(ClaimCodeDigits), nil)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", fmt.Errorf("生成认领码: %w", err)
	}
	return fmt.Sprintf("%0*d", ClaimCodeDigits, n.Int64()), nil
}

// netPrefix 把客户端 IP 归一到「网络前缀」：IPv4 取前 24 位，IPv6 取前 64 位。
//
// 只用来判断「兑换和签发是不是同一个网络」，不当作安全边界——印尼大量使用
// 运营商级 NAT，同一个出口后面有很多互不相干的用户（§12.3）。
func netPrefix(ipStr string) string {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return ""
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String() + "/24"
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
