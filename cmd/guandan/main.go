// Command guandan 是「随时掼掼蛋」的唯一进程：HTTP 服务、内嵌的前端页面、SQLite、
// 定时提醒和推送都在这里（docs/server-design.md §1、§3）。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	// 把时区数据编进二进制，不依赖镜像里有没有 tzdata（§13.2）。
	_ "time/tzdata"

	"github.com/dayuer/jkt-guandan/internal/agent"
	"github.com/dayuer/jkt-guandan/internal/backup"
	"github.com/dayuer/jkt-guandan/internal/buildinfo"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/janitor"
	"github.com/dayuer/jkt-guandan/internal/notify"
	"github.com/dayuer/jkt-guandan/internal/realtime"
	"github.com/dayuer/jkt-guandan/internal/route"
	"github.com/dayuer/jkt-guandan/internal/share"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/internal/supervise"
	"github.com/dayuer/jkt-guandan/web"
)

// minFreeBytes 是启动时要求的数据目录剩余空间（§12.1 第 3 步）。
const minFreeBytes = 500 << 20

// drainTimeout 是优雅关闭时等进行中请求的上限（§12.4 第 3 步）。
const drainTimeout = 20 * time.Second

func main() {
	// 子命令见 commands.go：healthcheck、restore、backup、seed、admin（§12.5、§13.3、§13.5、§13.6）。
	if len(os.Args) > 1 {
		os.Exit(runCommand(os.Args[1], os.Args[2:]))
	}
	if err := run(); err != nil {
		slog.Error("启动失败", "err", err)
		os.Exit(1)
	}
}

func run() error {
	started := time.Now()
	clk := clock.System{}

	// §12.1 第 1 步：配置不合法就直接退出。
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	setupLogging(cfg)
	slog.Info("启动", "data", cfg.DataDir, "addr", cfg.Addr, "dev", cfg.Dev, "tls", cfg.TLSEnabled(),
		"trusted_proxies", cfg.TrustedProxies.String())

	// 锁文件放在数据目录里，所以目录要先存在。
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", cfg.DataDir, err)
	}

	// §12.1 第 2 步：独占文件锁。两个容器挂同一个卷时，第二个起不来。
	lock, err := store.LockDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	// §12.1 第 3 步：数据目录可写，并且剩余空间够用。
	if err := checkDataDir(cfg.DataDir); err != nil {
		return err
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	// §12.1 第 4 步：先 quick_check，库已经损坏就不要再往上跑迁移。失败就提示从备份恢复（§13.3）。
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 30*time.Second)
	err = st.QuickCheck(checkCtx)
	cancelCheck()
	if err != nil {
		return fmt.Errorf("%w（考虑从备份恢复，见 docs/server-design.md §13.3）", err)
	}

	// §12.1 第 5 步：迁移。
	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 2*time.Minute)
	err = st.Migrate(migrateCtx)
	cancelMigrate()
	if err != nil {
		return err
	}
	slog.Info("数据库就绪", "path", st.Path())

	// 信号只用来触发关闭。后台任务用自己的 ctx，等 HTTP 排空之后才取消（§12.4 第 3、4 步），
	// 否则收到信号的同一刻后台任务就停了，还在处理的请求依赖的东西（比如提醒 worker）先没了。
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	bgCtx, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()

	// 后台任务都用 group.Go(bgCtx, …) 启动：#20 的备份、§13.1 的清理任务。
	group := supervise.New(clk)
	reg := newMetrics(st)

	// §13.3 备份：每小时本地快照，配置了异地就加密上传；/readyz 检查备份是否新鲜（§12.5）。
	backups, err := newBackupRunner(cfg, st, clk)
	if err != nil {
		return err
	}
	registerBackupMetrics(reg, backups)
	group.Go(bgCtx, "backup", backup.Interval, backups.Task)

	// §13.1 清理任务：每 10 分钟一次。
	group.Go(bgCtx, "cleanup", janitor.Interval, janitor.New(st, clk).Task)

	checks := []httpapi.ReadinessCheck{{Name: "备份", Fn: backups.Check}}

	// §5.5 实时：进程内的 Hub。事件只在写事务提交后发布（store.Tx.AfterCommit）。
	hub := realtime.NewHub()
	identitySvc := identity.NewService(st, clk, cfg.Dev)
	identitySvc.SetPublisher(hubPublisher{hub})
	wsHandler := realtime.Handler(realtime.Options{
		Hub:         hub,
		AllowOrigin: cfg.AllowsOrigin,
		// 身份由 httpapi 的身份中间件放进 ctx（#41）。被 guandan admin ban 封禁的用户或设备握手 403
		// （先查 Origin 和连接数，过了才识别身份，所以这条路由是 DeferIdentity）；连上之后被封禁的，下一次心跳断开（#43）。
		Identify: realtimeIdentify,
		Revalidate: func(ctx context.Context, p realtime.Principal) bool {
			return identitySvc.StillValid(ctx, p.UserID, p.DeviceID)
		},
		Build: buildinfo.Get(),
	})
	gamesSvc := games.NewService(st, clk, gamesPublisher{hub})

	// §5.3 Web Push：VAPID 密钥第一次启动时生成，存在 kv 表里（跟着备份走）。
	notifyCtx, cancelNotify := context.WithTimeout(context.Background(), 10*time.Second)
	notifySvc, err := notify.New(notifyCtx, st, clk, vapidSubject(cfg))
	cancelNotify()
	if err != nil {
		return err
	}
	// 发送 worker 每 15 秒一轮；心跳周期报 2 分钟：一轮最多 100 条、每条 10 秒超时、8 个并发，
	// 慢的时候一轮要一两分钟，按 15 秒算会被 /readyz 误判为卡死。
	group.Go(bgCtx, "push", 2*time.Minute, notify.NewWorker(notifySvc, notifySvc.NewSender(), clk).Task)

	// §12.4 第 1 步：开始关闭后 /readyz 立刻返回 503。
	checks = append(checks, httpapi.ReadinessCheck{Name: "关闭中", Fn: func(context.Context) error {
		if hub.Closing() {
			return errors.New("服务正在关闭")
		}
		return nil
	}})
	// §13.7：WebSocket 连接数。
	reg.Gauge("guandan_websocket_connections", "当前的 WebSocket 连接数。",
		func(context.Context) (float64, error) { return float64(hub.Conns()), nil })

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Options{
			Config:   cfg,
			Store:    st,
			Group:    group,
			Clock:    clk,
			Checks:   checks,
			Build:    buildinfo.Get(),
			Metrics:  reg.http,
			Identity: identitySvc,
			Routes: appRoutes(appDeps{
				identity: identitySvc, games: gamesSvc, notify: notifySvc, ws: wsHandler,
				share: share.Options{Games: gamesSvc, PublicURL: cfg.PublicURL, Clock: clk, Dist: webDist()},
				agent: agent.Options{Model: agentModel(cfg), Clock: clk},
			}),
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	// /metrics 单独监听（默认 127.0.0.1:9090），不经过对外的端口（§13.7）。
	servers := []*http.Server{srv}
	if cfg.MetricsAddr != "" {
		msrv := &http.Server{Addr: cfg.MetricsAddr, Handler: reg.Handler(), ReadHeaderTimeout: 5 * time.Second}
		servers = append(servers, msrv)
		go func() {
			slog.Info("/metrics 开始监听", "addr", cfg.MetricsAddr)
			if err := msrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("/metrics 监听失败，服务照常运行", "err", err)
			}
		}()
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("开始监听", "addr", cfg.Addr, "build", buildinfo.Get())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-sigCtx.Done():
	}
	// 再收到一次信号就按默认行为直接退出，给卡住的关闭留一个出口。
	stopSignals()

	slog.Info("收到停止信号，开始优雅关闭", "uptime_ms", time.Since(started).Milliseconds())
	// §12.4 第 1、2 步：Hub 标记为关闭（/readyz 随即 503、拒绝新的 WebSocket），
	// 给所有连接发 1012，客户端随后重连到新进程。最多等 5 秒，再进入 HTTP 排空。
	hubCtx, cancelHub := context.WithTimeout(context.Background(), 5*time.Second)
	if err := hub.Shutdown(hubCtx); err != nil {
		slog.Warn("等待 WebSocket 连接关闭超时", "err", err)
	}
	cancelHub()
	shutdown(servers, stopBackground, group, st, lock)
	slog.Info("已停止")
	return nil
}

// shutdown 按 §12.4 的顺序停机。第 1、2 步（/readyz 立刻返回 503、给 WebSocket 发关闭码 1012）
// 由调用方先做（hub.Shutdown），这里做 3–6，每一步都等上一步做完：
//
//  3. 各 http.Server 的 Shutdown：停止收新连接，等进行中的请求做完（最多 drainTimeout）；
//  4. 取消后台任务的 ctx，等它们退出——一定在第 3 步之后，请求排空期间后台任务还在跑；
//  5. WAL checkpoint，关闭数据库；
//  6. 释放数据目录的文件锁。
//
// 顺序由 TestShutdownDrainsHTTPBeforeStoppingBackground 验证。
func shutdown(servers []*http.Server, stopBackground context.CancelFunc, group *supervise.Group, st *store.Store, lock *store.DirLock) {
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainTimeout)
	defer cancelDrain()
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(drainCtx); err != nil {
				slog.Warn("等待进行中的请求超时", "addr", srv.Addr, "err", err)
			}
		}()
	}
	wg.Wait()

	stopBackground()
	group.Wait()

	closeCtx, cancelClose := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelClose()
	if err := st.Checkpoint(closeCtx); err != nil {
		slog.Warn("wal_checkpoint 失败", "err", err)
	}
	if err := st.Close(); err != nil {
		slog.Warn("关闭数据库出错", "err", err)
	}
	if err := lock.Release(); err != nil {
		slog.Warn("释放数据目录锁出错", "err", err)
	}
}

// healthcheck 请求本机的 path（/healthz 或 /readyz），200 时返回 0，否则返回 1。
// 给 Dockerfile 的 HEALTHCHECK 和 scripts/deploy.sh 用：distroless 镜像里没有 curl（§12.5、§13.4）。
func healthcheck(addr, path string) int {
	url, err := probeURL(addr, path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		fmt.Fprintln(os.Stderr, "healthcheck:", path, "返回", resp.StatusCode, strings.TrimSpace(string(body)))
		return 1
	}
	return 0
}

// probeURL 把监听地址（GUANDAN_ADDR，默认 :8080）换成本机能访问的探针地址。
// 监听在所有网卡（空主机名、0.0.0.0、::）时走 127.0.0.1。
func probeURL(addr, path string) (string, error) {
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("GUANDAN_ADDR=%q 不是 host:port: %w", addr, err)
	}
	switch host {
	case "", "0.0.0.0", "::":
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path, nil
}

// checkDataDir 确认数据目录可写、剩余空间够用（§12.1 第 3 步）。
func checkDataDir(dir string) error {
	probe := filepath.Join(dir, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return fmt.Errorf("数据目录 %s 不可写: %w", dir, err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("清理探测文件 %s: %w", probe, err)
	}
	free, err := store.FreeBytes(dir)
	if err != nil {
		return err
	}
	if free < minFreeBytes {
		return fmt.Errorf("数据目录 %s 只剩 %d MB 空间，少于 500 MB", dir, free>>20)
	}
	return nil
}

// setupLogging 见 §7：开发模式用文本日志，生产用 JSON 到 stdout。
func setupLogging(cfg config.Config) {
	var handler slog.Handler
	if cfg.Dev {
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, nil)
	}
	slog.SetDefault(slog.New(handler))
}

// hubPublisher 把 identity 等业务包的 Publisher 接口接到 realtime.Hub 上，
// 业务包因此不用依赖 realtime。
type hubPublisher struct{ hub *realtime.Hub }

func (p hubPublisher) Publish(topic, eventType string, data any) {
	p.hub.Publish(realtime.Event{Topic: topic, Type: eventType, Data: data})
}

func (p hubPublisher) KickDevice(deviceID string) { p.hub.KickDevice(deviceID) }

// gamesPublisher 把 games 的事件（字段和 realtime.Event 一一对应）接到 Hub 上。
type gamesPublisher struct{ hub *realtime.Hub }

func (p gamesPublisher) Publish(ev games.Event) {
	p.hub.Publish(realtime.Event{Topic: ev.Topic, Type: ev.Type, V: ev.V, Data: ev.Data})
}

// webDist 是内嵌的前端构建产物，/g/{code} 要往它的 index.html 里注入 OG 标签。
func webDist() fs.FS {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(err)
	}
	return dist
}

// vapidSubject 是 VAPID 的联系方式（RFC 8292）：推送服务出问题时用它联系我们。
func vapidSubject(cfg config.Config) string {
	switch {
	case cfg.AdminEmail != "":
		return "mailto:" + cfg.AdminEmail
	case cfg.PublicURL != "":
		return cfg.PublicURL
	default:
		return "mailto:dev@localhost"
	}
}

// agentModel 是蛋蛋背后的模型：没配 ANTHROPIC_API_KEY 时为 nil，POST /api/agent 返回 503 agent_disabled。
func agentModel(cfg config.Config) agent.Model {
	if cfg.AnthropicKey == "" {
		slog.Info("没有配置 ANTHROPIC_API_KEY，蛋蛋不可用，前端用开局表单")
		return nil
	}
	return agent.NewClaude(cfg.AnthropicKey, cfg.AgentModel)
}

// appDeps 是组装路由表要用到的各个服务。
type appDeps struct {
	identity *identity.Service
	games    *games.Service
	notify   *notify.Service
	ws       http.Handler
	share    share.Options
	agent    agent.Options
}

// appRoutes 是各业务包的路由，和 httpapi 的基础设施路由一起组成整张路由表（#41）。
// 授权要求写在各包的 Routes 里；cmd/guandan 的路由表测试把整张表逐条列出来核对。
func appRoutes(d appDeps) []route.Route {
	return slices.Concat(
		identity.Routes(d.identity),
		games.Routes(d.games),
		// 没有身份也能连（只能订阅公开主题）。
		[]route.Route{{Method: http.MethodGet, Pattern: "/ws", Access: route.Public, DeferIdentity: true, Handler: d.ws}},
		share.Routes(d.share),
		notify.Routes(d.notify),
		agent.Routes(d.agent),
	)
}

// realtimeIdentify 从身份中间件放进 ctx 的身份里取出 WebSocket 的身份；被封禁时返回错误，握手 403。
func realtimeIdentify(r *http.Request) (realtime.Principal, error) {
	userID, deviceID, err := identity.ForRealtime(r)
	return realtime.Principal{UserID: userID, DeviceID: deviceID}, err
}
