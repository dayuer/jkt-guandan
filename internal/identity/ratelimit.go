package identity

import (
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
)

// Limiter 是按 key（这里是 IP）计数的内存令牌桶。
//
// 单实例部署，所以内存限流就够用；用不着 Redis（§12.3）。
// §11.5 要求内存里的结构有容量上限并定期清扫，所以桶数超过 maxBuckets 时会丢掉
// 最久没用过的那些。
type Limiter struct {
	mu         sync.Mutex
	buckets    map[string]*bucket
	refill     float64 // 每秒补多少令牌
	burst      float64 // 桶容量
	maxBuckets int
	clk        clock.Clock
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter 建一个限流器：每分钟允许 perMinute 次，桶容量是 burst。
func NewLimiter(perMinute float64, burst int, clk clock.Clock) *Limiter {
	if clk == nil {
		clk = clock.System{}
	}
	return &Limiter{
		buckets:    make(map[string]*bucket),
		refill:     perMinute / 60,
		burst:      float64(burst),
		maxBuckets: 10000,
		clk:        clk,
	}
}

// Allow 判断这次请求能不能放行。
func (l *Limiter) Allow(key string) bool {
	now := l.clk.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		l.evictLocked(now)
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = min(l.burst, b.tokens+elapsed*l.refill)
			b.last = now
		}
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictLocked 在桶太多时丢掉最久没用过的，避免内存无限增长。
func (l *Limiter) evictLocked(now time.Time) {
	if len(l.buckets) < l.maxBuckets {
		return
	}
	cutoff := now.Add(-10 * time.Minute)
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
	// 还是太多就直接清空：宁可短时间放松限流，也不要让内存涨上去。
	if len(l.buckets) >= l.maxBuckets {
		l.buckets = make(map[string]*bucket)
	}
}
