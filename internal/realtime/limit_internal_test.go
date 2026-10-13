package realtime

import (
	"testing"
	"time"
)

// §12.3：同一 IP 最多 20 条连接；新连接的令牌每秒回 5 个；断开后名额释放；空闲的 IP 不再记。
func TestIPLimiter(t *testing.T) {
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	l := newIPLimiter(func() time.Time { return now })

	opened := 0
	for i := 0; i < 100 && opened < MaxConnsPerIP; i++ {
		if l.acquire("1.2.3.4") {
			opened++
		} else {
			now = now.Add(200 * time.Millisecond) // 等一个令牌
		}
	}
	if opened != MaxConnsPerIP {
		t.Fatalf("应能开满 %d 条，实际 %d", MaxConnsPerIP, opened)
	}
	now = now.Add(10 * time.Second) // 令牌早已回满
	if l.acquire("1.2.3.4") {
		t.Fatal("第 21 条应被拒")
	}
	if !l.acquire("5.6.7.8") {
		t.Fatal("别的 IP 不受影响")
	}

	l.release("1.2.3.4")
	if !l.acquire("1.2.3.4") {
		t.Fatal("断开一条后应能再连")
	}

	for i := 0; i < MaxConnsPerIP; i++ {
		l.release("1.2.3.4")
	}
	l.release("5.6.7.8")
	now = now.Add(time.Second)
	l.sweepLocked(now)
	if len(l.state) != 0 {
		t.Fatalf("空闲的 IP 应被清掉，还剩 %d 个", len(l.state))
	}
}

func TestCheckTopic(t *testing.T) {
	for _, c := range []struct {
		topic, user string
		want        error
	}{
		{"game:8K2", "", nil},
		{"feed:SCBD", "", nil},
		{"feed:Kelapa Gading", "", nil},
		{"user:u1", "u1", nil},
		{"user:u1", "u2", errForbiddenTopic},
		{"user:u1", "", errForbiddenTopic},
		{"game:", "", errBadTopic},
		{"game:../x", "", errBadTopic},
		{"chat:x", "", errBadTopic},
	} {
		if got := checkTopic(c.topic, c.user); got != c.want {
			t.Errorf("checkTopic(%q, %q) = %v，期望 %v", c.topic, c.user, got, c.want)
		}
	}
}
