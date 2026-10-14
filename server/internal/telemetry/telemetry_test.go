package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/httpapi"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/telemetry"
)

const origin = "https://guandan.id"

type fixture struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	clk *clock.Fake
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	idSvc := identity.NewService(st, clk, true)
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config:   config.Config{DataDir: t.TempDir(), Dev: true, PublicURL: origin},
		Store:    st,
		Clock:    clk,
		Identity: idSvc,
		Routes: slices.Concat(identity.Routes(idSvc),
			telemetry.Routes(telemetry.Options{Store: st, Clock: clk})),
	}))
	t.Cleanup(srv.Close)
	return &fixture{t: t, srv: srv, st: st, clk: clk}
}

func (f *fixture) browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Transport: f.srv.Client().Transport, Jar: jar}
}

type result struct {
	status int
	raw    string
	body   map[string]any
}

func (f *fixture) post(c *http.Client, path string, body any) result {
	f.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+path, rd)
	req.Header.Set("Origin", origin)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := result{status: res.StatusCode, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func (f *fixture) signUp(c *http.Client, nick string) {
	f.t.Helper()
	if r := f.post(c, "/api/me", map[string]string{"nickname": nick}); r.status != http.StatusOK {
		f.t.Fatalf("填称呼：%d %s", r.status, r.raw)
	}
}

func (f *fixture) count(where string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.st.Read().QueryRow(`SELECT count(*) FROM events WHERE `+where, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

func ev(names ...string) []map[string]any {
	var out []map[string]any
	for _, n := range names {
		out = append(out, map[string]any{"name": n})
	}
	return out
}

func TestEventsAcceptsAnonymousBatch(t *testing.T) {
	f := newFixture(t)
	r := f.post(f.browser(), "/api/events", map[string]any{"env": "wechat", "events": []map[string]any{
		{"name": "detail_view", "props": map[string]string{"game": "8K2QXM"}},
		{"name": "share_channel", "props": map[string]string{"channel": "whatsapp"}},
		{"name": "jumpout"},
	}})
	if r.status != http.StatusOK || r.body["accepted"] != 3.0 || r.body["dropped"] != 0.0 {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	if n := f.count(`env = 'wechat' AND user_id IS NULL`); n != 3 {
		t.Fatalf("应有 3 条匿名埋点，实际 %d", n)
	}
	var props string
	if err := f.st.Read().QueryRow(`SELECT props FROM events WHERE name = 'detail_view'`).Scan(&props); err != nil || props != `{"game":"8K2QXM"}` {
		t.Fatalf("props = %q, %v", props, err)
	}
}

func TestEventsRecordsUserWhenIdentified(t *testing.T) {
	f := newFixture(t)
	c := f.browser()
	f.signUp(c, "老王")
	if r := f.post(c, "/api/events", map[string]any{"env": "pwa", "events": ev("join_ok")}); r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	if n := f.count(`name = 'join_ok' AND user_id IS NOT NULL`); n != 1 {
		t.Fatalf("有身份的埋点应记下 user_id，实际 %d 条", n)
	}
}

// 事件名、属性、环境都走白名单；坏的单条丢掉，不拖垮整批；整体不合法才拒绝。
func TestEventsValidation(t *testing.T) {
	f := newFixture(t)
	c := f.browser()

	// 单条不合法：丢掉并计数。
	r := f.post(c, "/api/events", map[string]any{"env": "browser", "events": []map[string]any{
		{"name": "detail_view"},
		{"name": "not_whitelisted"},
		{"name": "detail_view", "props": map[string]string{"nickname": "老王"}},              // 不认识的键
		{"name": "detail_view", "props": map[string]string{"game": "<script>"}},            // 形状不对
		{"name": "join_click", "props": map[string]string{"channel": "whatsapp"}},          // channel 只给 share_channel
		{"name": "share_channel", "props": map[string]string{"channel": "carrier-pigeon"}}, // 不在渠道白名单
	}})
	if r.status != http.StatusOK || r.body["accepted"] != 1.0 || r.body["dropped"] != 5.0 {
		t.Fatalf("%d %s", r.status, r.raw)
	}

	cases := []struct {
		name string
		body any
	}{
		{"环境不对", map[string]any{"env": "safari", "events": ev("detail_view")}},
		{"没有环境", map[string]any{"events": ev("detail_view")}},
		{"空批", map[string]any{"env": "browser", "events": []any{}}},
		{"超过 20 条", map[string]any{"env": "browser", "events": ev(slices.Repeat([]string{"detail_view"}, 21)...)}},
		{"不是 JSON", "{nope"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if r := f.post(f.browser(), "/api/events", tc.body); r.status != http.StatusBadRequest {
				t.Fatalf("期望 400，实际 %d %s", r.status, r.raw)
			}
		})
	}
}

func TestEventsBodySizeCap(t *testing.T) {
	f := newFixture(t)
	big := map[string]any{"env": "browser", "events": []map[string]any{
		{"name": "detail_view", "props": map[string]string{"game": strings.Repeat("A", telemetry.MaxBodyBytes)}},
	}}
	if r := f.post(f.browser(), "/api/events", big); r.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("期望 413，实际 %d %s", r.status, r.raw)
	}
}

func TestEventsRateLimitedPerIPAndPerDevice(t *testing.T) {
	f := newFixture(t)
	post := func(c *http.Client) int {
		return f.post(c, "/api/events", map[string]any{"env": "browser", "events": ev("detail_view")}).status
	}

	// 按设备：有身份的浏览器 30 次之后被拦（IP 额度 120 次，还没到）。
	dev := f.browser()
	f.signUp(dev, "老王")
	for i := 0; i < 30; i++ {
		if s := post(dev); s != http.StatusOK {
			t.Fatalf("第 %d 次应放行，实际 %d", i+1, s)
		}
	}
	if s := post(dev); s != http.StatusTooManyRequests {
		t.Fatalf("设备额度用完应 429，实际 %d", s)
	}

	// 按 IP：匿名的浏览器共用一个 IP（本机），一共 120 次。设备放行的 30 次加上被拦的那 1 次
	// 也扣过 IP 额度（先 IP 后设备），所以匿名的只剩 120 - 31 = 89 次。
	anon := f.browser()
	var ok int
	for i := 0; i < 150; i++ {
		if post(anon) == http.StatusOK {
			ok++
		}
	}
	if ok != 89 {
		t.Fatalf("同一 IP 的匿名请求应只放行 89 次，实际 %d", ok)
	}

	// 过了一分钟，额度回来一部分。
	f.clk.Advance(time.Minute)
	if s := post(anon); s != http.StatusOK {
		t.Fatalf("一分钟后应恢复，实际 %d", s)
	}
}

func TestClientErrorTruncatesAndStripsQuery(t *testing.T) {
	f := newFixture(t)
	r := f.post(f.browser(), "/api/client-errors", map[string]any{
		"env":     "whatsapp",
		"message": strings.Repeat("错", 400),
		"stack":   strings.Repeat("at x (app.js:1:1)\n", 150),
		"url":     "https://guandan.id/g/8K2QXM?claim=SECRETTOKEN#frag",
	})
	if r.status != http.StatusNoContent {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	var env, props string
	if err := f.st.Read().QueryRow(`SELECT env, props FROM events WHERE name = 'client_error'`).Scan(&env, &props); err != nil {
		t.Fatal(err)
	}
	var p map[string]string
	if err := json.Unmarshal([]byte(props), &p); err != nil {
		t.Fatal(err)
	}
	if env != "whatsapp" || len([]rune(p["message"])) != 300 || len([]rune(p["stack"])) != 2000 {
		t.Fatalf("env=%s message=%d stack=%d", env, len([]rune(p["message"])), len([]rune(p["stack"])))
	}
	if p["path"] != "/g/8K2QXM" || strings.Contains(props, "SECRETTOKEN") {
		t.Fatalf("地址只该留路径，实际 %q（props=%s）", p["path"], props)
	}
}

func TestClientErrorValidationAndLimits(t *testing.T) {
	f := newFixture(t)
	c := f.browser()
	if r := f.post(c, "/api/client-errors", map[string]any{"env": "browser"}); r.status != http.StatusBadRequest {
		t.Fatalf("缺 message 应 400，实际 %d", r.status)
	}
	if r := f.post(c, "/api/client-errors", map[string]any{"env": "ie6", "message": "x"}); r.status != http.StatusBadRequest {
		t.Fatalf("环境不对应 400，实际 %d", r.status)
	}
	count := func(client *http.Client, n int) (ok, limited int) {
		for i := 0; i < n; i++ {
			switch f.post(client, "/api/client-errors", map[string]any{"env": "browser", "message": "boom"}).status {
			case http.StatusNoContent:
				ok++
			case http.StatusTooManyRequests:
				limited++
			}
		}
		return
	}

	// 有身份：每台设备每分钟 6 条，先到的是设备额度。
	dev := f.browser()
	f.signUp(dev, "老王")
	if ok, limited := count(dev, 10); ok != 6 || limited != 4 {
		t.Fatalf("每台设备每分钟 6 条：放行 %d、拦下 %d", ok, limited)
	}

	// 匿名：只按 IP，每分钟 60 条。上面设备放行的 6 条和被拦的 4 条都扣过 IP 额度，所以还剩 50。
	if ok, limited := count(c, 70); ok != 50 || limited != 20 {
		t.Fatalf("每 IP 每分钟 60 条：放行 %d、拦下 %d", ok, limited)
	}
	if n := f.count(`name = 'client_error'`); n != 56 {
		t.Fatalf("应存下 56 条，实际 %d", n)
	}
}

// 报错文字里常带完整地址：认领令牌、认领码之类的参数值要抹掉，堆栈里也一样。
func TestClientErrorRedactsSecretsInMessageAndStack(t *testing.T) {
	f := newFixture(t)
	r := f.post(f.browser(), "/api/client-errors", map[string]any{
		"env":     "wechat",
		"message": "Failed to fetch https://guandan.id/?claim=AbC123SECRET&x=1",
		"stack":   "at load (https://guandan.id/g/8K2QXM?token=TOPSECRET#h:1:1)\nat code=123456 ok",
		"url":     "https://guandan.id/?claim=AbC123SECRET",
	})
	if r.status != http.StatusNoContent {
		t.Fatalf("%d %s", r.status, r.raw)
	}
	var props string
	if err := f.st.Read().QueryRow(`SELECT props FROM events WHERE name = 'client_error'`).Scan(&props); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"AbC123SECRET", "TOPSECRET", "123456"} {
		if strings.Contains(props, secret) {
			t.Fatalf("props 里还有 %s：%s", secret, props)
		}
	}
	if !strings.Contains(props, "claim=[redacted]") || !strings.Contains(props, "x=1") {
		t.Fatalf("只该抹令牌的值，别的参数保留：%s", props)
	}
}

// 漏填 Store 要在启动时就报错，而不是上线后两个接口一直 500。
func TestRoutesPanicsWithoutStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("没有 Store 应当 panic")
		}
	}()
	telemetry.Routes(telemetry.Options{})
}
