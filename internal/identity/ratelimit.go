package identity

import (
	"log/slog"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
)

// codeFailureBudget 是全站的凭码兑换失败预算（§5.2 修订第 5 条）。
//
// 「每个码最多试 5 次」挡不住换着码撞库，按 IP 的限流换个 IP 也能绕。所以再加一层：
// 全站每分钟的失败次数超过 codeFailureLimit，就整体暂停凭码兑换一段时间，
// 期间一律返回 claim_invalid。凭 token 兑换不受这个限制。
type codeFailureBudget struct {
	mu     sync.Mutex
	clk    clock.Clock
	window time.Time
	count  int
	until  time.Time
}

const (
	codeFailureLimit    = 20
	codeFailureWindow   = time.Minute
	codeFailureCooldown = 5 * time.Minute
)

func newCodeFailureBudget(clk clock.Clock) *codeFailureBudget {
	return &codeFailureBudget{clk: clk, window: clk.Now()}
}

// suspended 报告现在是不是处在暂停期。
func (b *codeFailureBudget) suspended() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.clk.Now().Before(b.until)
}

// recordFailure 记一次失败，超出预算就进入暂停期。
func (b *codeFailureBudget) recordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.clk.Now()
	if now.Sub(b.window) >= codeFailureWindow {
		b.window = now
		b.count = 0
	}
	b.count++

	if b.count > codeFailureLimit {
		b.until = now.Add(codeFailureCooldown)
		slog.Warn("凭码兑换失败过多，暂时停用凭码兑换",
			"failures", b.count, "suspend_until", b.until.Unix())
		b.count = 0
		b.window = now
	}
}

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
