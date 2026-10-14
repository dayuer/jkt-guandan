package notify_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/notify"
	"github.com/dayuer/jkt-guandan/internal/store"
)

const origin = "https://guandan.test"

type fixture struct {
	t      *testing.T
	srv    *httptest.Server
	st     *store.Store
	client *http.Client
	clk    *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC))
	ident := identity.NewService(st, clk, false)
	svc, err := notify.New(context.Background(), st, clk, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	// TLS：生产 cookie 带 __Host- 前缀和 Secure（见 identity 测试里的说明）。
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config: config.Config{DataDir: dir, PublicURL: origin},
		Store:  st,
		Clock:  clk,
		RegisterRoutes: func(mux *http.ServeMux) {
			identity.Register(mux, ident)
			notify.Register(mux, svc, ident)
		},
	}))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &fixture{t: t, srv: srv, st: st, clk: clk,
		client: &http.Client{Transport: srv.Client().Transport, Jar: jar}}
}

func (f *fixture) do(method, path string, body any) (int, map[string]any) {
	f.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, r)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func subscription(endpoint string) map[string]any {
	return map[string]any{"endpoint": endpoint, "keys": map[string]string{
		"p256dh": "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM",
		"auth":   "tBHItJI5svbpez7KI4CCXg",
	}}
}

func TestPushEndpoints(t *testing.T) {
	f := newFixture(t)
	const ep = "https://fcm.googleapis.com/fcm/send/abc"

	// 公钥是公开的，不需要身份。
	if code, body := f.do(http.MethodGet, "/api/push/key", nil); code != 200 || len(body["key"].(string)) != 87 {
		t.Fatalf("GET /api/push/key：%d %v", code, body)
	}
	// 没有身份不能订阅。
	if code, _ := f.do(http.MethodPost, "/api/push/subscribe", subscription(ep)); code != 401 {
		t.Fatalf("没有身份应 401，实际 %d", code)
	}

	if code, _ := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); code != 200 {
		t.Fatalf("建身份：%d", code)
	}
	// 不是推送服务的地址（SSRF）被拒。
	if code, body := f.do(http.MethodPost, "/api/push/subscribe", subscription("https://127.0.0.1:9090/metrics")); code != 400 {
		t.Fatalf("内网地址应 400，实际 %d %v", code, body)
	}
	if code, _ := f.do(http.MethodPost, "/api/push/subscribe", subscription(ep)); code != 204 {
		t.Fatalf("订阅应 204，实际 %d", code)
	}
	var n int
	_ = f.st.Read().QueryRow(`SELECT count(*) FROM push_subscriptions`).Scan(&n)
	if n != 1 {
		t.Fatalf("应有 1 条订阅，实际 %d", n)
	}
	if code, _ := f.do(http.MethodDelete, "/api/push/subscribe", map[string]string{"endpoint": ep}); code != 204 {
		t.Fatalf("取消订阅应 204，实际 %d", code)
	}
	if code, _ := f.do(http.MethodDelete, "/api/push/subscribe", map[string]string{"endpoint": ep}); code != 404 {
		t.Fatalf("再删一次应 404，实际 %d", code)
	}

	// 被封禁之后不能再订阅。
	var userID string
	_ = f.st.Read().QueryRow(`SELECT id FROM users`).Scan(&userID)
	if _, err := f.st.Ban(context.Background(), userID, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _ := f.do(http.MethodPost, "/api/push/subscribe", subscription(ep)); code != 403 {
		t.Fatalf("被封禁应 403，实际 %d", code)
	}
}

// #48 复审第 1 条（auditor 的 PoC）：订阅过推送的设备被「退出」，应当 204，并且它的订阅跟着删掉。
func TestSignOutDeviceWithSubscription(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); code != 200 {
		t.Fatal(code)
	}
	if code, b := f.do(http.MethodPost, "/api/push/subscribe", subscription("https://fcm.googleapis.com/fcm/send/abc")); code/100 != 2 {
		t.Fatalf("subscribe %d %v", code, b)
	}
	_, me := f.do(http.MethodGet, "/api/me", nil)
	devs := me["devices"].([]any)
	id := devs[0].(map[string]any)["id"].(string)
	code, body := f.do(http.MethodDelete, "/api/me/devices/"+id, nil)
	var subs int
	_ = f.st.Read().QueryRow(`SELECT count(*) FROM push_subscriptions`).Scan(&subs)
	if code != http.StatusNoContent || subs != 0 {
		t.Fatalf("退出设备应 204 且订阅被删，实际 %d %v、剩 %d 条", code, body, subs)
	}
}

// #48 复审第 1 条：设备确认切换到别的身份后，旧身份的提醒不再发到这台设备上。
func TestSwitchedDeviceNoLongerReceivesOldIdentityPush(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// 这台浏览器原来是 Lily，开了推送。
	if code, _ := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "Lily"}); code != 200 {
		t.Fatal(code)
	}
	const ep = "https://fcm.googleapis.com/fcm/send/lily-phone"
	if code, _ := f.do(http.MethodPost, "/api/push/subscribe", subscription(ep)); code != 204 {
		t.Fatal(code)
	}
	var lily string
	_ = f.st.Read().QueryRow(`SELECT id FROM users WHERE nickname = 'Lily'`).Scan(&lily)

	// 另一个浏览器里的老王签发认领链接，这台浏览器确认切换成老王。
	jar, _ := cookiejar.New(nil)
	other := &fixture{t: t, srv: f.srv, st: f.st, clk: f.clk, client: &http.Client{Transport: f.srv.Client().Transport, Jar: jar}}
	if code, _ := other.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); code != 200 {
		t.Fatal(code)
	}
	_, claim := other.do(http.MethodPost, "/api/claim", nil)
	if code, body := f.do(http.MethodPost, "/api/claim/redeem", map[string]any{"token": claim["claim_token"], "confirm": true}); code != 200 {
		t.Fatalf("确认切换：%d %v", code, body)
	}

	// 发给 Lily 的提醒：这台设备已经不是 Lily 了，不能再收到。
	if err := f.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
			VALUES ('g1', 'SEED01', ?, ?, 'SCBD', 'v', 'classic', 'open', 0)`, lily, f.clk.Now().Add(26*time.Hour).Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
			VALUES ('g1', ?, 'host_seat_taken', ?, ?, 'k1')`, lily, f.clk.Now().Unix(), f.clk.Now().Unix())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	svc, err := notify.New(ctx, f.st, f.clk, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{}
	if _, err := notify.NewWorker(svc, sender, f.clk).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("旧身份的提醒发到了已经切换的设备上：%v", sender.sent)
	}
	var subs int
	_ = f.st.Read().QueryRow(`SELECT count(*) FROM push_subscriptions WHERE endpoint = ?`, ep).Scan(&subs)
	if subs != 0 {
		t.Fatal("切换身份后，这台设备以旧身份开的订阅应被删掉")
	}
}
