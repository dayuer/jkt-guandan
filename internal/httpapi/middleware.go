package httpapi

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"mime"
	"net"
	"net/http"

	"github.com/dayuer/jkt-guandan/internal/store"
)

// contentSecurityPolicy 见 §10.6。前端不加载任何第三方脚本。
const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; connect-src 'self' wss:; font-src 'self'; " +
	"object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

// middleware 按从外到里的顺序套上各层。
func (s *Server) middleware(next http.Handler) http.Handler {
	h := next
	h = s.limitBody(h)
	h = s.csrf(h)
	h = s.limitConcurrency(h)
	h = s.securityHeaders(h)
	h = s.recoverAndLog(h)
	return h
}

// recoverAndLog 生成 request_id、兜住 panic、并记录访问日志。
// 它是最外层，所以后面任何一层的 panic 都会变成 500，不影响其他请求（§12.2）。
func (s *Server) recoverAndLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := s.clk.Now()
		requestID := newRequestID()

		ctx, stats := store.WithWriteStats(r.Context())
		r = r.WithContext(ctx)

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		defer func() {
			if rv := recover(); rv != nil {
				slog.Error("请求 panic", "request_id", requestID, "path", r.URL.Path, "panic", rv)
				if !rec.wrote {
					writeError(rec, http.StatusInternalServerError, "internal", "服务器内部错误")
				}
			}
			elapsed := s.clk.Since(start)
			s.stats.Observe(rec.status, elapsed)
			// 只记 path，不记查询串：claim token 会出现在 ?claim= 里（§10.11）。
			slog.Info("http",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"ms", elapsed.Milliseconds(),
				"write_wait_ms", stats.Total().Milliseconds(),
			)
		}()

		next.ServeHTTP(rec, r)
	})
}

// securityHeaders 给所有响应加上安全头（§10.6、§10.7）。
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// 先不加 includeSubDomains 和 preload，等域名定下来再说（§10.7）。
		if s.cfg.TLSEnabled() {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		// 前端发现构建号和自己的不一致时，提示「有新版本，点我刷新」（§13.4）。
		if s.build != "" {
			h.Set("X-App-Build", s.build)
		}
		next.ServeHTTP(w, r)
	})
}

// limitConcurrency 限制同时处理的请求数，超出直接 503（§12.3）。
// /healthz 和 /readyz 不占名额：过载时存活检查也返回 503 的话，
// Docker 会把一个其实健康、只是忙的进程重启掉（§12.5）。
func (s *Server) limitConcurrency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			writeError(w, http.StatusServiceUnavailable, "overloaded", "服务繁忙，请稍后再试")
		}
	})
}

// csrf 对会改数据的请求做 CSRF 校验（§10.5）：
// 必须是 JSON，并且 Origin 等于本站的某个域名。cookie 本身的 SameSite=Lax 是第一道防线。
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
			writeError(w, http.StatusForbidden, "csrf", "会改数据的请求必须用 Content-Type: application/json")
			return
		}
		if !s.cfg.AllowsOrigin(r.Header.Get("Origin")) {
			writeError(w, http.StatusForbidden, "csrf", "请求来源不被接受")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// limitBody 把请求体限制在 64 KB 以内（§12.2）。超出时 handler 读 body 会拿到错误。
func (s *Server) limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodHead {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder 记住写出去的状态码，供日志和 panic 兜底使用。
// 它透传 Hijack 和 Flush，并实现 Unwrap 供 http.ResponseController 使用，
// 否则包了这一层之后 WebSocket 升级（#18）和流式响应都会失败。
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wrote {
		return
	}
	r.status = code
	r.wrote = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(b)
}

// Unwrap 让 http.ResponseController 能找到底层的 ResponseWriter。
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush 透传给底层的 http.Flusher。
func (r *statusRecorder) Flush() {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(r.ResponseWriter).Flush()
}

// Hijack 透传给底层连接。接管之后响应不再经过这里，
// 日志记 101，panic 兜底也不能再往连接上写 500。
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(r.ResponseWriter).Hijack()
	if err == nil && !r.wrote {
		r.status = http.StatusSwitchingProtocols
		r.wrote = true
	}
	return conn, rw, err
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
