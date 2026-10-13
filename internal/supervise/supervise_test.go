package supervise_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/supervise"
)

// newGroup 用假时钟建组，心跳和过期判断都能快进（§9）。
func newGroup() (*supervise.Group, *clock.Fake) {
	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	return supervise.New(clk), clk
}

// panic 之后要按退避重启，而不是把进程带走（§11.5）。
func TestRestartsAfterPanic(t *testing.T) {
	g, _ := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	recovered := make(chan struct{})
	g.Go(ctx, "flaky", time.Millisecond, func(_ context.Context, beat func()) error {
		beat()
		if calls.Add(1) == 1 {
			panic("第一次就炸")
		}
		close(recovered)
		return nil
	})

	select {
	case <-recovered:
	case <-time.After(5 * time.Second):
		t.Fatal("panic 之后没有重启")
	}
	g.Wait()

	if got := calls.Load(); got < 2 {
		t.Fatalf("应该被重启过，实际只执行了 %d 次", got)
	}
}

// 返回 error 也要重启。
func TestRestartsAfterError(t *testing.T) {
	g, _ := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	done := make(chan struct{})
	g.Go(ctx, "erroring", time.Millisecond, func(_ context.Context, beat func()) error {
		beat()
		if calls.Add(1) == 1 {
			return context.DeadlineExceeded
		}
		close(done)
		return nil
	})

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("出错之后没有重启")
	}
	g.Wait()
}

// 返回 nil 表示任务自己结束，不再重启。
func TestNormalReturnStopsTask(t *testing.T) {
	g, _ := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var calls atomic.Int32
	finished := make(chan struct{})
	g.Go(ctx, "once", time.Millisecond, func(_ context.Context, beat func()) error {
		beat()
		calls.Add(1)
		close(finished)
		return nil
	})

	<-finished
	g.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("正常返回的任务只该跑一次，实际 %d 次", got)
	}
}

// ctx 取消后任务应当退出，Wait 不再阻塞。
func TestWaitReturnsAfterCancel(t *testing.T) {
	g, _ := newGroup()
	ctx, cancel := context.WithCancel(context.Background())

	g.Go(ctx, "waiter", time.Millisecond, func(ctx context.Context, beat func()) error {
		beat()
		<-ctx.Done()
		return nil
	})
	time.Sleep(20 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { g.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("取消之后 Wait 应当返回")
	}
}

// 心跳超过 2 个周期就算过期，/readyz 靠这个判断（§12.5）。
func TestStaleDetectsOldHeartbeat(t *testing.T) {
	g, clk := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g.Go(ctx, "slow", time.Second, func(ctx context.Context, beat func()) error {
		beat()
		<-ctx.Done()
		return nil
	})

	if stale := g.Stale(clk.Now()); len(stale) != 0 {
		t.Fatalf("刚开始不该过期：%v", stale)
	}

	// 一个周期之内还不算过期，超过 2 个周期才算。
	clk.Advance(time.Second)
	if stale := g.Stale(clk.Now()); len(stale) != 0 {
		t.Fatalf("一个周期内不该过期：%v", stale)
	}
	clk.Advance(2 * time.Second)
	if stale := g.Stale(clk.Now()); len(stale) != 1 || stale[0] != "slow" {
		t.Fatalf("3 秒没心跳应当过期，实际 %v", stale)
	}
}

// 已经结束的任务不算过期，否则 /readyz 会被一次性任务卡住。
func TestStaleIgnoresFinishedTask(t *testing.T) {
	g, clk := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	finished := make(chan struct{})
	g.Go(ctx, "once", time.Second, func(_ context.Context, beat func()) error {
		beat()
		close(finished)
		return nil
	})
	<-finished
	g.Wait()

	if stale := g.Stale(clk.Now().Add(time.Hour)); len(stale) != 0 {
		t.Fatalf("结束的任务不该算过期：%v", stale)
	}
}

func TestStatusesReportsRestarts(t *testing.T) {
	g, _ := newGroup()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	var calls atomic.Int32
	g.Go(ctx, "flaky", time.Millisecond, func(_ context.Context, beat func()) error {
		beat()
		if calls.Add(1) == 1 {
			panic("炸")
		}
		close(done)
		return nil
	})
	<-done
	g.Wait()

	statuses := g.Statuses()
	st, ok := statuses["flaky"]
	if !ok {
		t.Fatalf("缺少任务状态：%v", statuses)
	}
	if st.Restarts != 1 {
		t.Fatalf("重启次数应为 1，实际 %d", st.Restarts)
	}
	if st.Period != time.Millisecond {
		t.Fatalf("周期应为 1ms，实际 %v", st.Period)
	}
	if st.Running {
		t.Fatal("任务已经结束，Running 应为 false")
	}
}
