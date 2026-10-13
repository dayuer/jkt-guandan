package janitor_test

import (
	"context"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/janitor"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// 起任务后立刻清理一次；每过 10 分钟再清理一次，把到点的局改成 done（§13.1）。
func TestTaskEndsGamesEveryTenMinutes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if _, err := st.Seed(ctx, start); err != nil {
		t.Fatal(err)
	}
	var firstStart int64
	if err := st.Read().QueryRow(`SELECT min(starts_at) FROM games`).Scan(&firstStart); err != nil {
		t.Fatal(err)
	}

	// 时钟放在第一局开局 6 小时后差 5 分钟。
	clk := clock.NewFake(time.Unix(firstStart, 0).Add(store.GameEndAfterStart - 5*time.Minute))
	j := janitor.New(st, clk)
	runCtx, cancel := context.WithCancel(ctx)
	beats := make(chan struct{}, 10)
	done := make(chan error, 1)
	go func() { done <- j.Task(runCtx, func() { beats <- struct{}{} }) }()

	wait := func() {
		t.Helper()
		select {
		case <-beats:
		case <-time.After(5 * time.Second):
			t.Fatal("没有等到清理")
		}
	}
	ended := func() int {
		var n int
		if err := st.Read().QueryRow(`SELECT count(*) FROM games WHERE status IN ('done','cancelled')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	wait() // 启动时的一轮
	if n := ended(); n != 0 {
		t.Fatalf("还没到 6 小时，不该结束任何局，实际 %d", n)
	}
	clk.Advance(janitor.Interval)
	wait()
	if n := ended(); n < 1 {
		t.Fatal("过了 6 小时，第一局应被结束")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
