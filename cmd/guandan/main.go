// Command guandan 是「随时掼掼蛋」的唯一进程：HTTP 服务、内嵌的前端页面、SQLite、
// 定时提醒和推送都在这里（docs/server-design.md §1、§3）。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	// 把时区数据编进二进制，不依赖镜像里有没有 tzdata（§13.2）。
	_ "time/tzdata"

	"github.com/dayuer/jkt-guandan/internal/buildinfo"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/realtime"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/internal/supervise"
)

// minFreeBytes 是启动时要求的数据目录剩余空间（§12.1 第 3 步）。
const minFreeBytes = 500 << 20

// drainTimeout 是优雅关闭时等进行中请求的上限（§12.4 第 3 步）。
const drainTimeout = 20 * time.Second

func main() {
	// distroless 镜像里没有 curl，Docker 的 HEALTHCHECK 调这个子命令（§12.5）。
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(os.Getenv("GUANDAN_ADDR")))
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
	_ = bgCtx

	// §5.5 实时：进程内的 Hub。事件只在写事务提交后发布（store.Tx.AfterCommit）。
	hub := realtime.NewHub()
	identitySvc := identity.NewService(st, clk, cfg.Dev)
	identitySvc.SetPublisher(hubPublisher{hub})
	wsHandler := realtime.Handler(realtime.Options{
		Hub:         hub,
		AllowOrigin: cfg.AllowsOrigin,
		Identify: func(r *http.Request) (string, error) {
			if ident := identitySvc.FromRequest(r); ident != nil {
				return ident.User.ID, nil
			}
			return "", nil
		},
		Build: buildinfo.Get(),
	})

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Options{
			Config: cfg,
			Store:  st,
			Group:  group,
			Clock:  clk,
			// §12.4 第 1 步：开始关闭后 /readyz 立刻返回 503。
			Checks: []httpapi.ReadinessCheck{{Name: "关闭中", Fn: func(context.Context) error {
				if hub.Closing() {
					return errors.New("服务正在关闭")
				}
				return nil
			}}},
			RegisterRoutes: func(mux *http.ServeMux) {
				identity.Register(mux, identitySvc)
				mux.Handle("GET /ws", wsHandler)
			},
		}),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	serveErr := make(chan error, 1)
	go func() {
		slog.Info("开始监听", "addr", cfg.Addr)
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
	shutdown(srv, stopBackground, group, st, lock)
	slog.Info("已停止")
	return nil
}

// shutdown 按 §12.4 的顺序停机。第 1、2 步（/readyz 立刻返回 503、给 WebSocket 发关闭码 1012）
// 由调用方先做（hub.Shutdown），这里做 3–6，每一步都等上一步做完：
//
//  3. srv.Shutdown：停止收新连接，等进行中的请求做完（最多 drainTimeout）；
//  4. 取消后台任务的 ctx，等它们退出——一定在第 3 步之后，请求排空期间后台任务还在跑；
//  5. WAL checkpoint，关闭数据库；
//  6. 释放数据目录的文件锁。
//
// 顺序由 TestShutdownDrainsHTTPBeforeStoppingBackground 验证。
func shutdown(srv *http.Server, stopBackground context.CancelFunc, group *supervise.Group, st *store.Store, lock *store.DirLock) {
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drainTimeout)
	defer cancelDrain()
	if err := srv.Shutdown(drainCtx); err != nil {
		slog.Warn("等待进行中的请求超时", "err", err)
	}

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

// healthcheck 请求本机的 /healthz，200 时返回 0，否则返回 1。
// 给 Dockerfile 的 HEALTHCHECK 用：distroless 镜像里没有 curl（§12.5）。
func healthcheck(addr string) int {
	url, err := healthzURL(addr)
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
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: /healthz 返回", resp.StatusCode)
		return 1
	}
	return 0
}

// healthzURL 把监听地址（GUANDAN_ADDR，默认 :8080）换成本机能访问的 /healthz 地址。
// 监听在所有网卡（空主机名、0.0.0.0、::）时走 127.0.0.1。
func healthzURL(addr string) (string, error) {
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
	return "http://" + net.JoinHostPort(host, port) + "/healthz", nil
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
