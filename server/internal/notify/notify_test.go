package notify

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC) // 雅加达 17:00

type env struct {
	t   *testing.T
	st  *store.Store
	svc *Service
	clk *clock.Fake
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(t0)
	svc, err := New(context.Background(), st, clk, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, st: st, svc: svc, clk: clk}
	e.exec(`INSERT INTO users (id, nickname, created_at) VALUES ('u1', '老王', 0), ('u2', 'Lily', 0)`)
	e.exec(`INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', 'u1', x'01', 0), ('d2', 'u2', x'02', 0)`)
	// 一局：两小时后开局，两人在座。
	e.exec(`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
		VALUES ('g1', 'SEED01', 'u1', ?, 'SCBD', 'Pacific Place', 'classic', 'open', 0)`, t0.Add(2*time.Hour).Unix())
	e.exec(`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g1', 1, 'u1', 0), ('g1', 2, 'u2', 0)`)
	return e
}

func (e *env) exec(q string, args ...any) {
	e.t.Helper()
	if err := e.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
}

func (e *env) scalar(q string, args ...any) string {
	e.t.Helper()
	var v any
	if err := e.st.Read().QueryRow(q, args...).Scan(&v); err != nil {
		e.t.Fatalf("%v\n%s", err, q)
	}
	switch x := v.(type) {
	case nil:
		return "NULL"
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// reminder 往 outbox 写一条现在就到期的提醒，返回 id。
func (e *env) reminder(userID, kind string) string {
	e.t.Helper()
	key := "g1|" + userID + "|" + kind + "|1"
	e.exec(`INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key) VALUES ('g1', ?, ?, ?, ?, ?)`,
		userID, kind, e.clk.Now().Unix(), e.clk.Now().Unix(), key)
	return e.scalar(`SELECT id FROM outbox WHERE dedupe_key = ?`, key)
}

const (
	fcm     = "https://fcm.googleapis.com/fcm/send/abc"
	mozilla = "https://updates.push.services.mozilla.com/wpush/v2/xyz"
	p256    = "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM"
	auth16  = "tBHItJI5svbpez7KI4CCXg"
)

func sub(endpoint string) Subscription {
	var s Subscription
	s.Endpoint = endpoint
	s.Keys.P256dh = p256
	s.Keys.Auth = auth16
	return s
}

// ---- VAPID 和订阅 ----

// §5.3：密钥第一次启动时生成，之后不变——变了所有订阅都会失效。
func TestVAPIDKeysPersist(t *testing.T) {
	e := newEnv(t)
	again, err := New(context.Background(), e.st, e.clk, "mailto:x@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if again.PublicKey() != e.svc.PublicKey() || again.privateKey != e.svc.privateKey {
		t.Fatal("重启后 VAPID 密钥变了")
	}
	raw, err := base64.RawURLEncoding.DecodeString(e.svc.PublicKey())
	if err != nil || len(raw) != 65 || raw[0] != 0x04 {
		t.Fatalf("公钥应是 base64url 的 65 字节未压缩 P-256 点：%v %d", err, len(raw))
	}
}

// 服务端会往 endpoint 发请求：只接受主流推送服务，防 SSRF。
func TestAllowedEndpoint(t *testing.T) {
	for raw, want := range map[string]bool{
		fcm:                                     true,
		mozilla:                                 true,
		"https://web.push.apple.com/QGuQyavXu":  true,
		"https://api.push.apple.com/3/device/x": true,
		"https://wns2-par02p.notify.windows.com/w/?token=x":       true,
		"http://fcm.googleapis.com/fcm/send/abc":                  false, // 不是 https
		"https://fcm.googleapis.com:8443/fcm/send/abc":            false, // 指定端口
		"https://user@fcm.googleapis.com/fcm/send/abc":            false,
		"https://fcm.googleapis.com.evil.example/x":               false,
		"https://evil.example/fcm.googleapis.com":                 false,
		"https://127.0.0.1/metrics":                               false,
		"https://[::1]/x":                                         false,
		"https://localhost/x":                                     false,
		"https://pushapple.com/x":                                 false,
		"https://fcm.googleapis.com/" + strings.Repeat("a", 2100): false,
	} {
		if got := allowedEndpoint(raw); got != want {
			t.Errorf("allowedEndpoint(%.60q) = %v，期望 %v", raw, got, want)
		}
	}
}

func TestSubscribeAndUnsubscribe(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.svc.Subscribe(ctx, "u1", "d1", sub(fcm)); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM events WHERE name = 'push_enabled' AND user_id = 'u1'`); got != "1" {
		t.Fatalf("应记一条 push_enabled 埋点，实际 %s", got)
	}
	// 同一个浏览器换了身份再订阅：同一个 endpoint 挂到新的用户和设备上，不重复。
	if err := e.svc.Subscribe(ctx, "u2", "d2", sub(fcm)); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT user_id || '/' || device_id || '/' || (SELECT count(*) FROM push_subscriptions) FROM push_subscriptions`); got != "u2/d2/1" {
		t.Fatalf("订阅应移到新身份：%s", got)
	}
	// 别人删不了。
	if err := e.svc.Unsubscribe(ctx, "u1", fcm); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不是自己的订阅应 ErrNotFound，实际 %v", err)
	}
	if err := e.svc.Unsubscribe(ctx, "u2", fcm); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT count(*) FROM push_subscriptions`); got != "0" {
		t.Fatalf("还剩 %s 条", got)
	}

	for _, bad := range []Subscription{sub("https://127.0.0.1/x"), func() Subscription { s := sub(fcm); s.Keys.P256dh = "x"; return s }(),
		func() Subscription { s := sub(fcm); s.Keys.Auth = "not base64!!" + strings.Repeat("a", 10); return s }()} {
		if err := e.svc.Subscribe(ctx, "u1", "d1", bad); !errors.Is(err, ErrBadSubscription) {
			t.Errorf("%+v 应被拒，实际 %v", bad, err)
		}
	}
}

// ---- 发送 ----

type call struct {
	sub     Subscription
	payload Payload
	opts    SendOptions
}

// fakeSender 按 endpoint 返回事先设好的状态码，并记录每次调用。
type fakeSender struct {
	mu       sync.Mutex
	status   map[string]int
	err      map[string]error
	calls    []call
	inFlight atomic.Int32
	maxSeen  atomic.Int32
	delay    time.Duration
	during   func() // 每次发送时执行（用来确认发送不在写事务里）
}

func (f *fakeSender) Send(_ context.Context, s Subscription, payload []byte, opts SendOptions) (int, error) {
	n := f.inFlight.Add(1)
	defer f.inFlight.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	if f.during != nil {
		f.during()
	}
	time.Sleep(f.delay)
	var p Payload
	_ = json.Unmarshal(payload, &p)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{s, p, opts})
	if err := f.err[s.Endpoint]; err != nil {
		return 0, err
	}
	if st, ok := f.status[s.Endpoint]; ok {
		return st, nil
	}
	return 201, nil
}

func TestSendSuccess(t *testing.T) {
	e := newEnv(t)
	_ = e.svc.Subscribe(context.Background(), "u2", "d2", sub(fcm))
	id := e.reminder("u2", "t_minus_1h")
	f := &fakeSender{}
	res, err := NewWorker(e.svc, f, e.clk).RunOnce(context.Background())
	if err != nil || res != (Result{Sent: 1}) {
		t.Fatalf("结果 %+v %v", res, err)
	}
	if got := e.scalar(`SELECT sent_at FROM outbox WHERE id = ?`, id); got != fmt.Sprint(t0.Unix()) {
		t.Fatalf("sent_at = %s", got)
	}
	c := f.calls[0]
	if c.payload.Title != "还有 1 小时开局" || c.payload.Body != "10月14日 19:00 · Pacific Place" ||
		c.payload.Tag != "g1|u2|t_minus_1h|1" || c.payload.URL != "/g/SEED01" || c.payload.Kind != "t_minus_1h" {
		t.Fatalf("推送内容 %+v", c.payload)
	}
	// Topic：RFC 8030 最多 32 个 base64url 字符；同一条提醒的 Topic 固定。
	if len(c.opts.Topic) != 32 || c.opts.Topic != topic("g1|u2|t_minus_1h|1") || strings.ContainsAny(c.opts.Topic, "+/=") {
		t.Fatalf("Topic = %q", c.opts.Topic)
	}
	if c.opts.TTL != 2*time.Hour {
		t.Fatalf("TTL 应到开局为止，实际 %v", c.opts.TTL)
	}
	// 已发出的不会再发。
	if res, _ := NewWorker(e.svc, f, e.clk).RunOnce(context.Background()); res != (Result{}) {
		t.Fatalf("第二轮不该再发：%+v", res)
	}
}

// §5.3：404/410 删订阅，这条提醒视为成功；多台设备里有一台送达就算发出。
func TestGoneSubscriptionIsDeleted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.svc.Subscribe(ctx, "u2", "d2", sub(fcm))
	_ = e.svc.Subscribe(ctx, "u2", "d2", sub(mozilla))
	e.reminder("u2", "full")
	f := &fakeSender{status: map[string]int{fcm: 410, mozilla: 500}}
	res, err := NewWorker(e.svc, f, e.clk).RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 一台 410（删掉）、一台 500：没有设备真正收到，有可重试的错误 → 重试。
	if res != (Result{Retried: 1, GoneSubscriptions: 1}) {
		t.Fatalf("结果 %+v", res)
	}
	if got := e.scalar(`SELECT group_concat(endpoint) FROM push_subscriptions`); got != mozilla {
		t.Fatalf("410 的订阅应被删掉：%s", got)
	}
	if got := e.scalar(`SELECT fail_count FROM push_subscriptions WHERE endpoint = ?`, mozilla); got != "1" {
		t.Fatalf("失败的订阅 fail_count = %s", got)
	}

	// 只剩 410 的设备时，这条提醒视为成功。
	e2 := newEnv(t)
	_ = e2.svc.Subscribe(ctx, "u2", "d2", sub(fcm))
	e2.reminder("u2", "full")
	res, _ = NewWorker(e2.svc, &fakeSender{status: map[string]int{fcm: 404}}, e2.clk).RunOnce(ctx)
	if res != (Result{Sent: 1, GoneSubscriptions: 1}) {
		t.Fatalf("全部 404 应视为成功：%+v", res)
	}
}

// §5.3：429、5xx、网络错误按 30 秒、2 分、10 分、30 分……退避；第 8 次失败放弃。
func TestRetryBackoffAndGiveUp(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	// 开局放到很久以后，免得因为过了开局时间先被放弃。
	e.exec(`UPDATE games SET starts_at = ? WHERE id = 'g1'`, t0.Add(30*24*time.Hour).Unix())
	_ = e.svc.Subscribe(ctx, "u2", "d2", sub(fcm))
	id := e.reminder("u2", "promoted")
	f := &fakeSender{err: map[string]error{fcm: errors.New("connection reset")}}
	w := NewWorker(e.svc, f, e.clk)

	wantGaps := []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, gap := range wantGaps {
		res, err := w.RunOnce(ctx)
		if err != nil || res.Retried != 1 {
			t.Fatalf("第 %d 次：%+v %v", i+1, res, err)
		}
		next := e.scalar(`SELECT next_try - ? FROM outbox WHERE id = ?`, e.clk.Now().Unix(), id)
		if next != fmt.Sprint(int64(gap/time.Second)) {
			t.Fatalf("第 %d 次失败后应等 %v，实际 %s 秒", i+1, gap, next)
		}
		if got := e.scalar(`SELECT last_error FROM outbox WHERE id = ?`, id); got != "connection reset" {
			t.Fatalf("last_error = %s", got)
		}
		// 没到时间不重试。
		if res, _ := w.RunOnce(ctx); res != (Result{}) {
			t.Fatalf("没到 next_try 不该重试：%+v", res)
		}
		e.clk.Advance(gap)
	}
	res, _ := w.RunOnce(ctx) // 第 8 次
	if res != (Result{Dropped: 1}) {
		t.Fatalf("第 8 次失败应放弃：%+v", res)
	}
	if got := e.scalar(`SELECT attempts || '|' || (dropped_at IS NOT NULL) FROM outbox WHERE id = ?`, id); got != "8|1" {
		t.Fatalf("attempts|dropped = %s", got)
	}
}

// 其他 4xx（比如 400、403 VAPID 不匹配）重试也没用，直接放弃。
func TestFatalStatusDrops(t *testing.T) {
	e := newEnv(t)
	_ = e.svc.Subscribe(context.Background(), "u2", "d2", sub(fcm))
	id := e.reminder("u2", "kicked")
	res, _ := NewWorker(e.svc, &fakeSender{status: map[string]int{fcm: 403}}, e.clk).RunOnce(context.Background())
	if res != (Result{Dropped: 1}) {
		t.Fatalf("%+v", res)
	}
	if got := e.scalar(`SELECT last_error FROM outbox WHERE id = ?`, id); got != "HTTP 403" {
		t.Fatalf("last_error = %s", got)
	}
}

// §5.3：发送前先检查，没有意义的提醒写 dropped_at，不发送。
func TestPointlessRemindersAreDropped(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	_ = e.svc.Subscribe(ctx, "u1", "d1", sub(fcm))
	f := &fakeSender{}
	w := NewWorker(e.svc, f, e.clk)

	noSub := e.reminder("u2", "full") // u2 没有订阅
	res, _ := w.RunOnce(ctx)
	if res != (Result{Dropped: 1}) || e.scalar(`SELECT last_error FROM outbox WHERE id = ?`, noSub) != "没有推送订阅" {
		t.Fatalf("没有订阅应放弃：%+v", res)
	}

	// 牌局取消：其他提醒放弃，「这局取消了」照发。
	e.exec(`UPDATE games SET status = 'cancelled', ended_at = ? WHERE id = 'g1'`, t0.Unix())
	stale := e.reminder("u1", "t_minus_1h")
	cancelled := e.reminder("u1", "cancelled")
	res, _ = w.RunOnce(ctx)
	if res != (Result{Sent: 1, Dropped: 1}) {
		t.Fatalf("%+v", res)
	}
	if e.scalar(`SELECT dropped_at IS NOT NULL FROM outbox WHERE id = ?`, stale) != "1" ||
		e.scalar(`SELECT sent_at IS NOT NULL FROM outbox WHERE id = ?`, cancelled) != "1" {
		t.Fatal("取消的局：只发「取消了」")
	}
	if len(f.calls) != 1 || f.calls[0].payload.Title != "这局取消了" {
		t.Fatalf("发出去的 %+v", f.calls)
	}

	// 开局前 1 小时的提醒过了开局时间就没意义了；补位成功之类的提醒在补人宽限期（#44）里照发，过了才放弃。
	e.exec(`UPDATE games SET status = 'open', ended_at = NULL WHERE id = 'g1'`)
	e.clk.Advance(3 * time.Hour) // 开局后 1 小时
	tooLate := e.reminder("u1", "t_minus_1h_late")
	e.exec(`UPDATE outbox SET kind = 't_minus_1h' WHERE id = ?`, tooLate)
	inGrace := e.reminder("u1", "promoted")
	res, _ = w.RunOnce(ctx)
	if res != (Result{Sent: 1, Dropped: 1}) {
		t.Fatalf("开局后 1 小时：%+v", res)
	}
	if e.scalar(`SELECT last_error FROM outbox WHERE id = ?`, tooLate) != "已过开局时间" ||
		e.scalar(`SELECT sent_at IS NOT NULL FROM outbox WHERE id = ?`, inGrace) != "1" {
		t.Fatal("t_minus_1h 应放弃，promoted 应照发")
	}
	e.clk.Advance(2 * time.Hour) // 开局后 3 小时，过了补人宽限期
	late := e.reminder("u1", "seat_left")
	res, _ = w.RunOnce(ctx)
	if res != (Result{Dropped: 1}) || e.scalar(`SELECT last_error FROM outbox WHERE id = ?`, late) != "已过补人时间" {
		t.Fatalf("过了补人宽限期应放弃：%+v", res)
	}
}

// 推送服务不该让我们跟着重定向走：endpoint 是按白名单校验过的，重定向会绕开它（SSRF）。
func TestSenderDoesNotFollowRedirects(t *testing.T) {
	e := newEnv(t)
	var hitTarget atomic.Bool
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hitTarget.Store(true) }))
	defer target.Close()
	redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/internal", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	uaPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	var s Subscription
	s.Endpoint = redirector.URL + "/push"
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(uaPriv.PublicKey().Bytes())
	s.Keys.Auth = auth16
	sender := e.svc.NewSender()
	tr := redirector.Client().Transport.(*http.Transport).Clone()
	tr.TLSClientConfig.InsecureSkipVerify = true // 两个测试服务器各有自签证书
	sender.Client.Transport = tr
	status, err := sender.Send(context.Background(), s, []byte(`{}`), SendOptions{TTL: time.Minute})
	if err != nil || status != http.StatusTemporaryRedirect {
		t.Fatalf("应原样拿到 307，实际 %d %v", status, err)
	}
	if hitTarget.Load() {
		t.Fatal("跟着重定向去了别的地址")
	}
}

// 一直失败（但不回 404/410）的订阅累计 50 次后删掉；成功一次就清零。
func TestFailingSubscriptionIsEventuallyRemoved(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.exec(`UPDATE games SET starts_at = ? WHERE id = 'g1'`, t0.Add(30*24*time.Hour).Unix())
	_ = e.svc.Subscribe(ctx, "u2", "d2", sub(fcm))
	_ = e.svc.Subscribe(ctx, "u2", "d2", sub(mozilla))
	e.exec(`UPDATE push_subscriptions SET fail_count = ? WHERE endpoint = ?`, MaxSubscriptionFailures-1, fcm)
	e.exec(`UPDATE push_subscriptions SET fail_count = 7 WHERE endpoint = ?`, mozilla)
	e.reminder("u2", "full")
	f := &fakeSender{status: map[string]int{fcm: 500}} // mozilla 成功
	if _, err := NewWorker(e.svc, f, e.clk).RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.scalar(`SELECT group_concat(endpoint || ':' || fail_count) FROM push_subscriptions`); got != mozilla+":0" {
		t.Fatalf("剩下的订阅：%s", got)
	}
}

// §5.3、§11.1：发送在事务外进行（发的时候写锁是空的），并且最多 8 个同时发。
func TestSendsOutsideTransactionWithBoundedConcurrency(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		var s Subscription = sub(fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i))
		if err := e.svc.Subscribe(ctx, "u2", "d2", s); err != nil {
			t.Fatal(err)
		}
	}
	e.reminder("u2", "full")
	var writeErr atomic.Value
	f := &fakeSender{delay: 20 * time.Millisecond, during: func() {
		wctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		if err := e.st.Write(wctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO kv (k, v) VALUES ('probe', x'01')`)
			return err
		}); err != nil {
			writeErr.Store(err)
		}
	}}
	res, err := NewWorker(e.svc, f, e.clk).RunOnce(ctx)
	if err != nil || res.Sent != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if v := writeErr.Load(); v != nil {
		t.Fatalf("发送期间写库失败（发送不该占着写事务）：%v", v)
	}
	if len(f.calls) != 20 {
		t.Fatalf("应发 20 次，实际 %d", len(f.calls))
	}
	if m := f.maxSeen.Load(); m > Concurrency || m < 2 {
		t.Fatalf("同时发送的数量 %d，应在 2 到 %d 之间", m, Concurrency)
	}
}

func TestMessageForEveryKind(t *testing.T) {
	g := gameInfo{Code: "X", Venue: "PIK Avenue", StartsAt: t0, Missing: 2}
	for _, kind := range []string{"t_minus_1h", "full", "seat_left", "promoted", "host_seat_taken", "host_waitlist",
		"host_transferred", "host_changed", "rescheduled", "cancelled", "kicked", "invited"} {
		title, body := message(kind, g)
		if title == "" || title == "掼蛋局有更新" || !strings.Contains(body, "PIK Avenue") || !strings.Contains(body, "17:00") {
			t.Errorf("%s：%q %q", kind, title, body)
		}
	}
	if title, _ := message("seat_left", g); title != "有人退出了，还差 2 人" {
		t.Errorf("seat_left：%q", title)
	}
}

// ---- 真的走一遍 webpush-go：VAPID 签名和 RFC 8291 加密 ----

// 用一个假的推送服务接请求：检查 VAPID 头、Topic、TTL，再用订阅方的私钥按 RFC 8291 解密，
// 确认解出来的正是我们发的 JSON。
func TestWebPushSenderEncryptsForTheSubscriber(t *testing.T) {
	e := newEnv(t)
	uaPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	authSecret := make([]byte, 16)
	_, _ = rand.Read(authSecret)

	var got struct {
		header http.Header
		body   []byte
	}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.header = r.Header.Clone()
		got.body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	var s Subscription
	s.Endpoint = srv.URL + "/push/abc"
	s.Keys.P256dh = base64.RawURLEncoding.EncodeToString(uaPriv.PublicKey().Bytes())
	s.Keys.Auth = base64.RawURLEncoding.EncodeToString(authSecret)

	sender := e.svc.NewSender()
	sender.Client = srv.Client()
	payload := []byte(`{"title":"还有 1 小时开局","tag":"g1|u2|t_minus_1h|1"}`)
	status, err := sender.Send(context.Background(), s, payload, SendOptions{Topic: topic("k"), TTL: 2 * time.Hour})
	if err != nil || status != http.StatusCreated {
		t.Fatalf("发送：%d %v", status, err)
	}

	if ce := got.header.Get("Content-Encoding"); ce != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q", ce)
	}
	if ttl := got.header.Get("TTL"); ttl != "7200" {
		t.Fatalf("TTL = %q", ttl)
	}
	if tp := got.header.Get("Topic"); tp != topic("k") {
		t.Fatalf("Topic = %q", tp)
	}
	if a := got.header.Get("Authorization"); !strings.HasPrefix(a, "vapid t=") || !strings.Contains(a, "k="+e.svc.PublicKey()) {
		t.Fatalf("Authorization = %q", a)
	}
	if bytesContains(got.body, payload) {
		t.Fatal("请求体里不该出现明文")
	}
	plain := decryptAES128GCM(t, got.body, uaPriv, authSecret)
	if string(plain) != string(payload) {
		t.Fatalf("解密结果 %q", plain)
	}
}

func bytesContains(b, sub []byte) bool { return strings.Contains(string(b), string(sub)) }

// decryptAES128GCM 按 RFC 8291 §3.4 / RFC 8188 解密（只处理单记录）。
func decryptAES128GCM(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idlen := int(body[20])
	asPubBytes := body[21 : 21+idlen]
	ciphertext := body[21+idlen:]
	if uint32(len(ciphertext)) > rs {
		t.Fatalf("超出一个记录：%d > %d", len(ciphertext), rs)
	}
	asPub, err := ecdh.P256().NewPublicKey(asPubBytes)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := uaPriv.ECDH(asPub)
	if err != nil {
		t.Fatal(err)
	}
	keyInfo := "WebPush: info\x00" + string(uaPriv.PublicKey().Bytes()) + string(asPubBytes)
	prkKey, _ := hkdf.Extract(sha256.New, shared, authSecret)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, keyInfo, 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	padded, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("解密失败：%v", err)
	}
	// 最后一个记录：明文后跟分隔符 0x02，再跟若干个 0。
	i := len(padded) - 1
	for i >= 0 && padded[i] == 0 {
		i--
	}
	if i < 0 || padded[i] != 0x02 {
		t.Fatalf("记录分隔符不对：%x", padded)
	}
	return padded[:i]
}
