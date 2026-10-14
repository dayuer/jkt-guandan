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

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/metrics"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/supervise"
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
	// Build 是构建号，非空时每个响应都带 X-App-Build，前端据此提示刷新（§13.4）。
	Build string
	// Metrics 收集请求数和耗时，供 /metrics 输出（§13.7）。可以为 nil。
	Metrics *metrics.HTTP
	// Identity 是唯一的身份入口用的（#41）：路由表里有要识别身份的路由时必须提供。
	Identity *identity.Service
	// Routes 是各业务包的路由（identity.Routes、games.Routes……），和基础设施的路由一起组成
	// 整张路由表（Table）。它们挂在套好中间件的 mux 上，不用自己处理 CSRF、限流、日志和身份。
	Routes []route.Route
}

type Server struct {
	cfg    config.Config
	st     *store.Store
	group  *supervise.Group
	clk    clock.Clock
	checks []ReadinessCheck
	sem    chan struct{}
	build  string
	stats  *metrics.HTTP
	// ident 给 CSRF 校验判断「只带 Bearer、不带 cookie」的小游戏请求（#84）。可以为 nil。
	ident *identity.Service
	// mux 是注册好路由表的 ServeMux。前端兜底要用它判断 /api 下的路径是「不存在」（404）
	// 还是「方法不对」（405）：兜底的 "/" 不限方法，ServeMux 自己永远不会回 405。
	mux *http.ServeMux
}

// New 组装路由和中间件：按路由表（Table）注册，每条路由套上身份中间件，外面再套通用中间件。
func New(opts Options) http.Handler {
	s := newServer(opts)
	s.mux = http.NewServeMux()
	mount(s.mux, opts.Identity, s.table(opts.Routes))
	return s.middleware(s.mux)
}

func newServer(opts Options) *Server {
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	return &Server{
		cfg:    opts.Config,
		st:     opts.Store,
		group:  opts.Group,
		clk:    opts.Clock,
		checks: opts.Checks,
		sem:    make(chan struct{}, maxConcurrentRequests),
		build:  opts.Build,
		stats:  opts.Metrics,
		ident:  opts.Identity,
	}
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

// publicConfig 返回前端要的、由部署方配置的公开信息（#108）：09 牌友顶部「雅加达掼蛋群」横幅的
// 微信号和 WhatsApp 群链接，没配置就是空串，前端据此隐藏横幅。不识别身份，可以缓存一会儿。
func (s *Server) publicConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, http.StatusOK, map[string]any{
		"group": map[string]string{
			"wechat_id":          s.cfg.GroupWeChatID,
			"whatsapp_group_url": s.cfg.GroupWhatsAppURL,
		},
	})
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
