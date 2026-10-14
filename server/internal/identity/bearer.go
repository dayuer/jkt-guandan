package identity

import (
	"net/http"
	"strings"
)

// 小游戏通道的凭证（#84、§10.3）。
//
// H5 的 device_token 只走 HttpOnly cookie。小游戏没有 cookie，wx.request / wx.connectSocket 只能带自定义头，
// 所以这是「token 只走 cookie」的唯一例外：POST /api/wx/login 在响应体里返回 device_token，
// 小游戏存在 wx.setStorage 里，之后每个请求带 `Authorization: Bearer <device_token>`。
//
// 只在 /api/ 下和 /ws 上接受 Bearer：分享页、静态资源这些地方没有理由带凭证，接受了只会扩大 token 的暴露面。
// 不接受放在查询串里的 token：查询串会进访问日志、Referer 和代理日志。

// bearerPathAllowed 说明这个路径能不能用 Bearer 认身份。
func bearerPathAllowed(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/ws"
}

// bearerToken 取出 `Authorization: Bearer <token>`；路径不允许或者没带时返回 false。
func bearerToken(r *http.Request) (string, bool) {
	if !bearerPathAllowed(r.URL.Path) {
		return "", false
	}
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(h[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// credential 选出这个请求用的设备 token：cookie 优先（两者都有时只看 cookie），没有 cookie 才看 Bearer。
func (s *Service) credential(r *http.Request) (string, bool) {
	if token, ok := s.cookies.Read(r); ok {
		return token, true
	}
	return bearerToken(r)
}

// CookieFree 说明这个请求没带设备 cookie（/ws 握手用）。
//
// 浏览器发起 WebSocket 握手一定带 Origin；没有 Origin 的握手只能来自非浏览器客户端（小游戏的 wx.connectSocket）。
// 跨站 WebSocket 劫持靠的是浏览器自动附上 cookie，所以「没有 Origin、也没有 cookie」的握手可以放行：
// 身份只从 Bearer 头认，认不出就是访客（只能收到公开的牌局变化）。带着 cookie 却没有 Origin 的照常拒绝。
func (s *Service) CookieFree(r *http.Request) bool {
	_, ok := s.cookies.Read(r)
	return !ok
}

// BearerOnly 说明这个请求只用 Bearer 头带凭证、没有设备 cookie（CSRF 中间件和 /ws 握手用）。
//
// 浏览器跨站发请求时会自动带上 cookie，但带不上 Authorization 头（要过 CORS 预检，我们不放行任何跨站来源），
// 所以只带 Bearer、不带 cookie 的请求不可能是 CSRF。这里只看头，不查库：token 是否有效由身份中间件判断。
func (s *Service) BearerOnly(r *http.Request) bool {
	if _, ok := s.cookies.Read(r); ok {
		return false
	}
	_, ok := bearerToken(r)
	return ok
}
