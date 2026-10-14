package notify_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/notify"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

type recordingSender struct {
	mu   sync.Mutex
	sent []string // endpoint
}

func (r *recordingSender) Send(_ context.Context, s notify.Subscription, _ []byte, _ notify.SendOptions) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, s.Endpoint)
	return 201, nil
}

// 端到端：games 写进 outbox 的提醒（有人报名 → 通知局头），由 worker 发到局头的订阅上。
// 确认 notify 认得 games 写的提醒类型和 dedupe_key。
func TestGamesReminderIsDelivered(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.do("POST", "/api/me", map[string]string{"nickname": "老王"}) // 局头
	var host string
	_ = f.st.Read().QueryRow(`SELECT id FROM users`).Scan(&host)
	const ep = "https://fcm.googleapis.com/fcm/send/host"
	if code, _ := f.do("POST", "/api/push/subscribe", subscription(ep)); code != 204 {
		t.Fatalf("订阅：%d", code)
	}
	if err := f.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, nickname, created_at) VALUES ('guest', 'Lily', 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	g := games.NewService(f.st, f.clk, nil)
	d, err := g.Create(ctx, host, games.CreateInput{
		StartsAt: f.clk.Now().Add(26 * time.Hour), Area: "SCBD", Venue: "Pacific Place", Kind: "classic",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Join(ctx, d.Game.ShortCode, "guest"); err != nil {
		t.Fatal(err)
	}

	svc, err := notify.New(ctx, f.st, f.clk, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	res, err := notify.NewWorker(svc, sender, f.clk).RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Sent < 1 || len(sender.sent) < 1 || sender.sent[0] != ep {
		t.Fatalf("局头应收到「有人报名了你的局」：%+v %v", res, sender.sent)
	}
	var kind string
	_ = f.st.Read().QueryRow(`SELECT kind FROM outbox WHERE sent_at IS NOT NULL AND user_id = ?`, host).Scan(&kind)
	if kind != games.ReminderHostSeatTaken {
		t.Fatalf("发出去的是 %q", kind)
	}
	// 开局前 1 小时的提醒还没到时间，没动。
	var pending int
	_ = f.st.Read().QueryRow(`SELECT count(*) FROM outbox WHERE kind = 't_minus_1h' AND sent_at IS NULL AND dropped_at IS NULL`).Scan(&pending)
	if pending == 0 {
		t.Fatal("t_minus_1h 应还在等")
	}
}
