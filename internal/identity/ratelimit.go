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
