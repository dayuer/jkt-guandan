package identity

import (
	"net/http"
	"time"
)

// CookieMaxAge 是设备 cookie 的有效期（§5.2：400 天）。
const CookieMaxAge = 400 * 24 * time.Hour

// CookiePolicy 决定 cookie 叫什么名字、要不要带 Secure。
//
// 只有这一个地方做这个判断，读 cookie 和写 cookie 都用它（§10.3）。
type CookiePolicy struct {
	Name   string
	Secure bool
}

// CookiePolicyFor 按运行模式给出策略。
//
// 生产环境用 `__Host-` 前缀：浏览器强制要求这类 cookie 必须带 Secure、Path=/、不带 Domain，
// 所以子域名或非 HTTPS 页面写不进去。
//
// 开发模式不能用 `__Host-`：本地测试要用真手机连 `http://192.168.x.x:8080`，
// 局域网 IP 不算安全上下文，带 Secure 的 cookie 会被浏览器静默丢掉，
// 于是开发时永远认不出人。所以开发模式换成不带前缀的名字、也不带 Secure。
func CookiePolicyFor(dev bool) CookiePolicy {
	if dev {
		return CookiePolicy{Name: DevCookieName, Secure: false}
	}
	return CookiePolicy{Name: ProdCookieName, Secure: true}
}

const (
	// ProdCookieName 是生产环境的 cookie 名（§10.3）。
	ProdCookieName = "__Host-gd_dev"
	// DevCookieName 只在 GUANDAN_DEV=1 时使用。
	DevCookieName = "gd_dev"
)

// Set 下发设备 cookie。token 是明文 device_token，库里只存它的 sha256。
func (p CookiePolicy) Set(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     p.Name,
		Value:    token,
		Path:     "/", // __Host- 要求 Path=/，且不能设 Domain
		HttpOnly: true,
		Secure:   p.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	})
}

// Clear 让浏览器立刻丢掉这个 cookie。
func (p CookiePolicy) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     p.Name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   p.Secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Read 从请求里取出设备 token。没有 cookie 时返回 false。
func (p CookiePolicy) Read(r *http.Request) (string, bool) {
	c, err := r.Cookie(p.Name)
	if err != nil || c.Value == "" {
		return "", false
	}
	return c.Value, true
}
