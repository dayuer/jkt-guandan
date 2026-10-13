package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/dayuer/jkt-guandan/web"
)

// assets 提供内嵌的前端页面。未知路径回落到 index.html，交给前端路由（§6）。
func (s *Server) assets() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		// embed 保证 dist 一定存在，这里只在构建产物异常时触发。
		panic(err)
	}
	index, indexErr := fs.ReadFile(dist, "index.html")
	files := http.FileServer(http.FS(dist))

	return referrerPolicyForClaim(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
