// Package janitor 是 §13.1 的清理任务：每 10 分钟一次，每天执行一次 PRAGMA optimize。
package janitor

import (
	"context"
	"log/slog"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

const (
	// Interval 是清理周期，也是给 supervise 的心跳周期。
	Interval = 10 * time.Minute
	// OptimizeInterval 是 PRAGMA optimize 的周期。
	OptimizeInterval = 24 * time.Hour
)

// Janitor 用 New 创建，把 Task 交给 supervise.Group。
type Janitor struct {
	st           *store.Store
	clk          clock.Clock
	lastOptimize time.Time
}

func New(st *store.Store, clk clock.Clock) *Janitor {
	if clk == nil {
		clk = clock.System{}
	}
	return &Janitor{st: st, clk: clk}
}

// Task 启动后立刻清理一次，之后每 10 分钟一次。单轮出错只记日志，下一轮照常；
// 不返回错误，免得 supervise 按退避重启，把清理节奏打乱。
func (j *Janitor) Task(ctx context.Context, beat func()) error {
	ticker := j.clk.NewTicker(Interval)
	defer ticker.Stop()
	for {
		j.RunOnce(ctx)
		beat()
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C():
		}
	}
}

// RunOnce 执行一轮清理。距上次 optimize 满一天（或者从没跑过）时顺带执行 PRAGMA optimize。
func (j *Janitor) RunOnce(ctx context.Context) {
	now := j.clk.Now()
	res, err := j.st.Cleanup(ctx, now)
	if err != nil {
		slog.Error("清理任务失败", "err", err)
	} else if res != (store.CleanupResult{}) {
		slog.Info("清理完成",
			"games_done", res.GamesDone, "games_cancelled", res.GamesCancelled,
			"claim_tokens", res.ClaimTokens, "idempotency_keys", res.IdempotencyKeys,
			"outbox", res.Outbox, "devices", res.Devices,
			"push_subscriptions", res.PushSubscriptions, "events", res.Events)
	}

	if j.lastOptimize.IsZero() || now.Sub(j.lastOptimize) >= OptimizeInterval {
		if err := j.st.Optimize(ctx); err != nil {
			slog.Error("PRAGMA optimize 失败", "err", err)
			return
		}
		j.lastOptimize = now
		slog.Info("PRAGMA optimize 完成")
	}
}
