// Package httpapi 是 HTTP 入口：路由、中间件和静态页面（docs/server-design.md §6）。
// 用标准库的 net/http 和 Go 1.22 起的 ServeMux 模式匹配，不引入 Web 框架。
package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/web"
)

type Server struct {
	cfg config.Config
	st  *store.Store
}

// New 组装路由。以后各包的 handler 都挂到这个 mux 上。
func New(cfg config.Config, st *store.Store) http.Handler {
	s := &Server{cfg: cfg, st: st}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("/", s.assets())

	return s.logged(mux)
}

// healthz 存活检查：会真的执行一次 SELECT 1，数据库不可用就返回 503。
func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.st.Ping(ctx); err != nil {
		slog.Error("健康检查失败", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// assets 提供内嵌的前端页面。未知路径回落到 index.html，交给前端路由（§6）。
func (s *Server) assets() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		// embed 保证 dist 一定存在，这里只在构建产物异常时触发。
		panic(err)
	}
	index, indexErr := fs.ReadFile(dist, "index.html")
	files := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			// 没有这个文件。单页应用回落到 index.html；如果连 index.html 都没有，
			// 说明前端没构建进二进制，这时要响亮地失败，不能假装 200。
			if indexErr != nil {
				http.Error(w, "前端未构建：web/dist/index.html 不存在，先执行 make web-build", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	})
}

// logged 记录访问日志（JSON 到 stdout，用 docker logs 就能看，§2）。
func (s *Server) logged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"ms", time.Since(start).Milliseconds(),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("写响应失败", "err", err)
	}
}
