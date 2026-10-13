package clock_test

import (
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
)

func TestFakeAdvanceTriggersAfter(t *testing.T) {
	start := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := clock.NewFake(start)

	ch := f.After(30 * time.Minute)
	select {
	case <-ch:
		t.Fatal("还没推进时间就触发了")
	default:
	}

	f.Advance(29 * time.Minute)
	select {
	case <-ch:
		t.Fatal("还差一分钟就触发了")
	default:
	}

	f.Advance(time.Minute)
	select {
	case got := <-ch:
		if !got.Equal(start.Add(30 * time.Minute)) {
			t.Fatalf("触发时间 %v，期望 %v", got, start.Add(30*time.Minute))
		}
	default:
		t.Fatal("到点了应该触发")
	}
}

func TestFakeAfterZeroFiresImmediately(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	select {
	case <-f.After(0):
	default:
		t.Fatal("0 延迟应当立即触发")
	}
}

func TestFakeTickerFires(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(10 * time.Second)
	defer ticker.Stop()

	f.Advance(35 * time.Second)
	select {
	case <-ticker.C():
	default:
		t.Fatal("跨过 3 个周期后 ticker 应该触发过")
	}
}

func TestFakeTickerStop(t *testing.T) {
	f := clock.NewFake(time.Unix(0, 0))
	ticker := f.NewTicker(time.Second)
	ticker.Stop()

	f.Advance(time.Minute)
	select {
	case <-ticker.C():
		t.Fatal("停掉之后不该再触发")
	default:
	}
}

func TestFakeNowSinceUntil(t *testing.T) {
	start := time.Unix(1000, 0)
	f := clock.NewFake(start)

	if !f.Now().Equal(start) {
		t.Fatalf("Now = %v", f.Now())
	}
	f.Advance(5 * time.Second)
	if got := f.Since(start); got != 5*time.Second {
		t.Fatalf("Since = %v", got)
	}
	if got := f.Until(start.Add(10 * time.Second)); got != 5*time.Second {
		t.Fatalf("Until = %v", got)
	}
}

func TestSystemClockWorks(t *testing.T) {
	c := clock.System{}
	if c.Now().IsZero() {
		t.Fatal("System.Now 不该是零值")
	}
	if c.Since(c.Now()) < 0 {
		t.Fatal("System.Since 不该是负数")
	}
	select {
	case <-c.After(time.Nanosecond):
	case <-time.After(time.Second):
		t.Fatal("System.After 没有触发")
	}
}
