// Package supervise 监管进程内的后台 goroutine（docs/server-design.md §11.5）。
//
// 每个后台任务都用 Group.Go 启动，拿到三件事：
//   - ctx：关闭时统一取消（§12.4），任务看到取消就正常返回；
//   - beat：每转一圈调用一次，记录心跳，/readyz 靠它判断任务有没有卡死（§12.5）；
//   - 返回值：返回 nil 表示任务自己决定结束，不再重启；返回 error 或 panic 会记日志，
//     按退避时间重启。一个任务挂了不会带走进程。
package supervise

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
)

// Task 是一次后台循环。beat 每转一圈调用一次；返回 nil 即正常结束，不再重启。
type Task func(ctx context.Context, beat func()) error

// errPanic 只用来把 panic 变成可以走同一条重启路径的 error。
var errPanic = errors.New("panic")

// Group 监管一组后台任务。
type Group struct {
	mu       sync.Mutex
	tasks    map[string]*state
	order    []string
	wg       sync.WaitGroup
	clk      clock.Clock
	backoff  time.Duration
	maxDelay time.Duration
}

type state struct {
	period   time.Duration
	beat     time.Time
	restarts int
	running  bool
}

// Status 是给 /readyz 看的任务状态快照。
type Status struct {
	Period   time.Duration
	LastBeat time.Time
	Restarts int
	Running  bool
}

// New 建一个监管组。心跳时间取自 clk，所以测试里可以用假时钟快进（§9）。
func New(clk clock.Clock) *Group {
	if clk == nil {
		clk = clock.System{}
	}
	return &Group{
		tasks:    make(map[string]*state),
		clk:      clk,
		backoff:  50 * time.Millisecond,
		maxDelay: 30 * time.Second,
	}
}

// Go 启动一个后台任务。period 是它期望的循环周期，/readyz 用 2 个周期判断心跳是否新鲜。
func (g *Group) Go(ctx context.Context, name string, period time.Duration, task Task) {
	g.mu.Lock()
	g.tasks[name] = &state{period: period, beat: g.clk.Now(), running: true}
	g.order = append(g.order, name)
	g.mu.Unlock()

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		delay := g.backoff
		for {
			err := g.runOnce(ctx, name, task)
			if err == nil {
				g.setRunning(name, false)
				return
			}
			select {
			case <-ctx.Done():
				g.setRunning(name, false)
				return
			case <-time.After(delay):
			}
			delay *= 2
			if delay > g.maxDelay {
				delay = g.maxDelay
			}
			g.incRestarts(name)
		}
	}()
}

// runOnce 跑一轮任务，panic 在这里被拦下，转成走重启路径的 error。
func (g *Group) runOnce(ctx context.Context, name string, task Task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("后台任务 panic，将重启", "task", name, "panic", r)
			err = errPanic
		}
	}()

	beat := func() { g.beat(name) }
	if e := task(ctx, beat); e != nil {
		slog.Error("后台任务出错，将重启", "task", name, "err", e)
		return e
	}
	return nil
}

// Wait 等所有任务退出。调用前要先取消传给 Go 的 ctx。
func (g *Group) Wait() { g.wg.Wait() }

func (g *Group) beat(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.tasks[name]; ok {
		st.beat = g.clk.Now()
	}
}

func (g *Group) incRestarts(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.tasks[name]; ok {
		st.restarts++
	}
}

func (g *Group) setRunning(name string, running bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.tasks[name]; ok {
		st.running = running
	}
}

// Statuses 返回各任务的状态快照。
func (g *Group) Statuses() map[string]Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]Status, len(g.tasks))
	for name, st := range g.tasks {
		out[name] = Status{
			Period:   st.period,
			LastBeat: st.beat,
			Restarts: st.restarts,
			Running:  st.running,
		}
	}
	return out
}

// Stale 返回心跳超过 2 个周期的任务名，按启动顺序排列。没有任务时返回 nil。
func (g *Group) Stale(now time.Time) []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	var stale []string
	for _, name := range g.order {
		st := g.tasks[name]
		if st == nil || !st.running || st.period <= 0 {
			continue
		}
		if now.Sub(st.beat) > 2*st.period {
			stale = append(stale, name)
		}
	}
	return stale
}
