// Package httpapi 是 HTTP 入口：路由、中间件和静态页面（docs/server-design.md §6、§10、§12）。
// 用标准库的 net/http 和 Go 1.22 起的 ServeMux 模式匹配，不引入 Web 框架。
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/internal/supervise"
)

const (
	// 同时处理的请求数上限（§12.3）。
	maxConcurrentRequests = 256
	// 请求体上限 64 KB（§12.2）。
	maxRequestBodyBytes = 64 << 10
	// 就绪检查要求数据目录至少留这么多空间（§12.1、§12.5）。
	minFreeBytes = 500 << 20
	// 就绪检查自己的超时。
	readyzTimeout = 3 * time.Second
)

// ReadinessCheck 是可由其他任务注册的就绪检查。
// 例如备份是否新鲜由 #20 注册进来，否则没有备份任务时容器永远不 ready（§12.5）。
type ReadinessCheck struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Options 组装 HTTP 层需要的东西。
type Options struct {
	Config config.Config
	Store  *store.Store
	Group  *supervise.Group
	Clock  clock.Clock
	Checks []ReadinessCheck
	// RegisterRoutes 让 identity、games 等包把自己的 handler 挂到主 mux 上。
	// 它们拿到的是已经套好中间件的 mux，所以不用自己处理 CSRF、限流和日志。
	RegisterRoutes func(mux *http.ServeMux)
}

type Server struct {
	cfg    config.Config
	st     *store.Store
	group  *supervise.Group
	clk    clock.Clock
	checks []ReadinessCheck
	sem    chan struct{}
}

// New 组装路由和中间件。以后各包的 handler 都挂到这个 mux 上。
func New(opts Options) http.Handler {
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	s := &Server{
		cfg:    opts.Config,
		st:     opts.Store,
		group:  opts.Group,
		clk:    opts.Clock,
		checks: opts.Checks,
		sem:    make(chan struct{}, maxConcurrentRequests),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	if opts.RegisterRoutes != nil {
		opts.RegisterRoutes(mux)
	}
	// 兜底放在最后注册：它匹配所有剩下的路径，交给内嵌的前端。
	mux.Handle("/", s.assets())

	return s.middleware(mux)
}

// healthz 是存活检查：进程能响应就 200，供 Docker 的 HEALTHCHECK 和重启策略使用（§12.5）。
func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz 是就绪检查：写事务能提交、后台任务心跳新鲜、磁盘空间够、注册的检查都通过（§12.5）。
func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
	defer cancel()

	var problems []string

	if err := s.st.Heartbeat(ctx, s.clk.Now()); err != nil {
		problems = append(problems, "写事务不可用："+err.Error())
	}
	if s.group != nil {
		if stale := s.group.Stale(s.clk.Now()); len(stale) > 0 {
			problems = append(problems, "后台任务心跳过期："+strings.Join(stale, "、"))
		}
	}
	if free, err := store.FreeBytes(s.cfg.DataDir); err != nil {
		problems = append(problems, "读磁盘空间失败："+err.Error())
	} else if free < minFreeBytes {
		problems = append(problems, fmt.Sprintf("剩余空间不足：%d MB", free>>20))
	}
	for _, c := range s.checks {
		if err := c.Fn(ctx); err != nil {
			problems = append(problems, c.Name+"："+err.Error())
		}
	}

	if len(problems) > 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "not_ready",
			"problems": problems,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeJSON 写出 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("写响应失败", "err", err)
	}
}

// ErrorBody 是统一的错误格式：{"error":{"code":"...","message":"..."}}（§6）。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]ErrorBody{"error": {Code: code, Message: message}})
}

// writeWriteError 把 store 的错误映射成 HTTP 状态：
// 等写锁超时返回 503 busy 并带 Retry-After，其他写失败也是 503（§11.1、§12.5）。
func writeWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "系统繁忙，请稍后再试")
	default:
		slog.Error("写操作失败", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法写入，请稍后再试")
	}
}
