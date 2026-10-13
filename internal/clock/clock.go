// Package clock 把所有取时间、等时间的地方收敛到一个接口，测试里可以快进
// （docs/server-design.md §9、§13.2）。
//
// 业务代码不要直接调 time.Now()：定时任务、提示时间、限流窗口都要从时钟读，
// 这样测试里不用真的等一小时。
package clock

import "time"

// Clock 是生产代码用到的全部时间能力。
type Clock interface {
	Now() time.Time
	Since(t time.Time) time.Duration
	Until(t time.Time) time.Duration
	After(d time.Duration) <-chan time.Time
	NewTicker(d time.Duration) Ticker
}

// Ticker 只保留 time.Ticker 里我们用得到的两件事。
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// System 是真实的时钟。
type System struct{}

func (System) Now() time.Time                         { return time.Now() }
func (System) Since(t time.Time) time.Duration        { return time.Since(t) }
func (System) Until(t time.Time) time.Duration        { return time.Until(t) }
func (System) After(d time.Duration) <-chan time.Time { return time.After(d) }

func (System) NewTicker(d time.Duration) Ticker { return &systemTicker{t: time.NewTicker(d)} }

type systemTicker struct{ t *time.Ticker }

func (s *systemTicker) C() <-chan time.Time { return s.t.C }
func (s *systemTicker) Stop()               { s.t.Stop() }
