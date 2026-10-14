package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/dayuer/jkt-guandan/web"
)

// assets 提供内嵌的前端页面。未知路径回落到 index.html，交给前端路由（§6）。
// /api 下没有匹配的路径不回落：返回 404 JSON（#119），前端和脚本拿到的是统一的错误格式，
// 不是一个 200 的 HTML 页面。路径存在、只是方法不对的返回 405 并带 Allow。
func (s *Server) assets() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		// embed 保证 dist 一定存在，这里只在构建产物异常时触发。
		panic(err)
	}
	index, indexErr := fs.ReadFile(dist, "index.html")
	files := http.FileServer(http.FS(dist))

	return referrerPolicyForClaim(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := path.Clean(r.URL.Path); p == "/api" || strings.HasPrefix(p, "/api/") {
			if allow := s.allowedMethods(r); len(allow) > 0 {
				w.Header().Set("Allow", strings.Join(allow, ", "))
				writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "这个接口不支持 "+r.Method)
				return
			}
			writeError(w, http.StatusNotFound, "not_found", "没有这个接口")
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "." || name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			// 单页应用回落到 index.html；连 index.html 都没有说明前端没构建进二进制，
			// 这时要响亮地失败，不能假装 200。
			if indexErr != nil {
				http.Error(w, "前端未构建：web/dist/index.html 不存在，先执行 make web-build", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(index)
			return
		}
		files.ServeHTTP(w, r)
	}))
}

// allowedMethods 返回这个路径上注册了哪些方法（不算前端兜底的 "/"）。
func (s *Server) allowedMethods(r *http.Request) []string {
	if s.mux == nil {
		return nil
	}
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := s.mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, m)
		}
	}
	return allow
}
