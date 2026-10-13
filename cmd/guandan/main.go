// Command guandan 是「随时掼掼蛋」的唯一进程：HTTP 服务、内嵌的前端页面、SQLite、
// 定时提醒和推送都在这里（docs/server-design.md §1、§3）。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	// 把时区数据编进二进制，不依赖镜像里有没有 tzdata（§13.2）。
	_ "time/tzdata"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/internal/supervise"
)

// minFreeBytes 是启动时要求的数据目录剩余空间（§12.1 第 3 步）。
const minFreeBytes = 500 << 20

func main() {
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
	slog.Info("启动", "data", cfg.DataDir, "addr", cfg.Addr, "dev", cfg.Dev, "tls", cfg.TLSEnabled())

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

	// §12.1 第 4 步：quick_check。失败就提示从备份恢复（§13.3）。
	checkCtx, cancelCheck := context.WithTimeout(context.Background(), 30*time.Second)
	err = st.QuickCheck(checkCtx)
	cancelCheck()
	if err != nil {
		return fmt.Errorf("%w（考虑从备份恢复，见 docs/server-design.md §13.3）", err)
	}
	slog.Info("数据库就绪", "path", st.Path())

	// 根 ctx：收到信号就取消，所有后台任务据此退出（§12.4）。
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 后台任务都从这里启动：#18 的 WebSocket Hub、#20 的备份、§13.1 的清理任务。
	group := supervise.New(clk)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(httpapi.Options{
			Config: cfg,
			Store:  st,
			Group:  group,
			Clock:  clk,
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
	case <-ctx.Done():
	}

	// 优雅关闭（§12.4）。第 1、2 步（readyz 立刻返回 503、给 WebSocket 发关闭码 1012）
	// 由 #18 补上，这里先做 3–6：停止收新连接 → 等请求做完 → 停后台任务 → checkpoint → 释放锁。
	slog.Info("收到停止信号，开始优雅关闭", "uptime_ms", time.Since(started).Milliseconds())

	drainCtx, cancelDrain := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancelDrain()
	if err := srv.Shutdown(drainCtx); err != nil {
		slog.Warn("等待进行中的请求超时", "err", err)
	}

	stop()
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

	slog.Info("已停止")
	return nil
}

// checkDataDir 确认数据目录可写、剩余空间够用（§12.1 第 3 步）。
func checkDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录 %s: %w", dir, err)
	}
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
