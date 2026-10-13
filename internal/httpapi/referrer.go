package httpapi

import "net/http"

// referrerPolicyForClaim 给带 `?claim=` 的页面加 `Referrer-Policy: no-referrer`（§10.3）。
//
// 跳出微信的链接里带着一次性 claim token，如果页面再往外发请求（图片、字体、分享），
// Referer 头会把整个 URL 带出去。默认的 strict-origin-when-cross-origin 只发 origin，
// 但同源请求仍然会带上完整 URL，所以这里对这类页面单独收紧。
//
// 它套在静态处理器外面，在 securityHeaders 之后执行，所以能覆盖掉默认值。
func referrerPolicyForClaim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("claim") {
			w.Header().Set("Referrer-Policy", "no-referrer")
		}
		next.ServeHTTP(w, r)
	})
}
