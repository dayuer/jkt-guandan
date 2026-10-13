package store

import (
	"context"
	"sync"
	"time"
)

// WriteStats 收集一个请求里所有写事务的等待时间。
// §13.7 要求每个请求的日志里带上写锁等待时间，所以在 handler 里创建一份放进 ctx。
type WriteStats struct {
	mu    sync.Mutex
	total time.Duration
	max   time.Duration
	count int
}

// Record 记下一次写事务拿到写锁所花的时间。
func (s *WriteStats) Record(d time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total += d
	s.count++
	if d > s.max {
		s.max = d
	}
}

// Total 是累计等待时间，Count 是写事务次数，Max 是最长的一次。
func (s *WriteStats) Total() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.total
}

func (s *WriteStats) Max() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

func (s *WriteStats) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

type writeStatsKey struct{}

// WithWriteStats 把统计器放进 ctx，之后的 Write 会把等待时间记到它上面。
func WithWriteStats(ctx context.Context) (context.Context, *WriteStats) {
	stats := &WriteStats{}
	return context.WithValue(ctx, writeStatsKey{}, stats), stats
}

func writeStatsFrom(ctx context.Context) *WriteStats {
	stats, _ := ctx.Value(writeStatsKey{}).(*WriteStats)
	return stats
}
