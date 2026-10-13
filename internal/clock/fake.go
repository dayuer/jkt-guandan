package clock

import (
	"sync"
	"time"
)

// Fake 是测试用的时钟，时间只在 Advance 时前进。
type Fake struct {
	mu      sync.Mutex
	now     time.Time
	waiters []*waiter
	tickers []*fakeTicker
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Since(t time.Time) time.Duration { return f.Now().Sub(t) }
func (f *Fake) Until(t time.Time) time.Duration { return t.Sub(f.Now()) }

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, &waiter{deadline: f.now.Add(d), ch: ch})
	return ch
}

func (f *Fake) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{period: d, next: f.now.Add(d), ch: make(chan time.Time, 1)}
	f.tickers = append(f.tickers, t)
	return t
}

// Advance 把时间往前推，并按顺序触发到期的 After 和 Ticker。
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	f.now = f.now.Add(d)
	now := f.now

	var due []*waiter
	kept := f.waiters[:0]
	for _, w := range f.waiters {
		if !w.deadline.After(now) {
			due = append(due, w)
			continue
		}
		kept = append(kept, w)
	}
	f.waiters = kept
	tickers := append([]*fakeTicker(nil), f.tickers...)
	f.mu.Unlock()

	for _, w := range due {
		select {
		case w.ch <- now:
		default:
		}
	}
	for _, t := range tickers {
		t.fire(now)
	}
}

type waiter struct {
	deadline time.Time
	ch       chan time.Time
}

type fakeTicker struct {
	mu      sync.Mutex
	period  time.Duration
	next    time.Time
	ch      chan time.Time
	stopped bool
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func (t *fakeTicker) fire(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped || t.period <= 0 {
		return
	}
	for !t.next.After(now) {
		select {
		case t.ch <- t.next:
		default:
		}
		t.next = t.next.Add(t.period)
	}
}
