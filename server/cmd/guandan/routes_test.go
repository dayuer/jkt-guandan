package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/agent"
	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/friends"
	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/httpapi"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/notify"
	"github.com/dayuer/jkt-guandan/server/internal/realtime"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/share"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/telemetry"
)

// 路由表和授权矩阵（#41、§10.4、§10.12）。用的是 server/cmd/guandan 真实组装出来的整张表：
// 新加一条路由不在这里列出来，路由表测试就失败；授权矩阵按表逐条、逐个身份打一遍。

const routesTestOrigin = "https://guandan.id"

type app struct {
	t       *testing.T
	st      *store.Store
	clk     *clock.Fake
	srv     *httptest.Server
	options httpapi.Options
}

func newApp(t *testing.T) *app {
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
	clk := clock.NewFake(time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC))
	cfg := config.Config{DataDir: dir, PublicURL: routesTestOrigin}

	hub := realtime.NewHub()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hub.Shutdown(ctx)
	})
	identitySvc := identity.NewService(st, clk, false)
	gamesSvc := games.NewService(st, clk, gamesPublisher{hub})
	notifySvc, err := notify.New(context.Background(), st, clk, "mailto:ops@example.com")
	if err != nil {
		t.Fatal(err)
	}
	opts := httpapi.Options{
		Config: cfg, Store: st, Clock: clk, Identity: identitySvc,
		Routes: appRoutes(appDeps{
			identity: identitySvc, games: gamesSvc, notify: notifySvc, friends: friends.NewService(st, clk),
			ws: realtime.Handler(realtime.Options{
				Hub: hub, AllowOrigin: cfg.AllowsOrigin, CookieFree: identitySvc.CookieFree, Identify: realtimeIdentify,
				Revalidate: func(ctx context.Context, p realtime.Principal) bool {
					return identitySvc.StillValid(ctx, p.UserID, p.DeviceID)
				},
			}),
			share: share.Options{Games: gamesSvc, PublicURL: cfg.PublicURL, Clock: clk,
				Dist: fstest.MapFS{"index.html": {Data: []byte("<!doctype html><html><head><title>x</title></head><body></body></html>")}}},
			agent:     agent.Options{Clock: clk}, // 没有 key：agent_disabled，矩阵只看授权
			telemetry: telemetry.Options{Store: st, Clock: clk},
		}),
	}
	// 必须 TLS：生产 cookie 带 __Host- 前缀和 Secure（见 identity 测试里的说明）。
	srv := httptest.NewTLSServer(httpapi.New(opts))
	t.Cleanup(srv.Close)
	return &app{t: t, st: st, clk: clk, srv: srv, options: opts}
}

// 整张路由表逐条列出来：方法、路径、身份要求、handler。新增或改动路由时必须同时改这里，
// 路由不可能悄悄丢掉授权要求、也不可能多出一条没人审过的路由。
func TestRouteTable(t *testing.T) {
	a := newApp(t)
	type row struct{ key, access, handler string }
	want := []row{
		{"GET /healthz", "anonymous", "httpapi.(*Server).healthz"},
		{"GET /readyz", "anonymous", "httpapi.(*Server).readyz"},
		{"GET /api/config", "anonymous", "httpapi.(*Server).publicConfig"},
		{"POST /api/me", "public", "identity.(*handler).createMe"},
		{"GET /api/me", "user", "identity.(*handler).getMe"},
		{"DELETE /api/me/devices/{id}", "user", "identity.(*handler).signOutDevice"},
		{"DELETE /api/me", "user", "identity.(*handler).deleteMe"},
		{"PUT /api/me/wa", "user", "identity.(*handler).putWA"},
		{"PUT /api/me/nickname", "user", "identity.(*handler).putNickname"},
		{"DELETE /api/me/wa", "user", "identity.(*handler).deleteWA"},
		{"POST /api/claim", "user", "identity.(*handler).issueClaim"},
		{"POST /api/claim/redeem", "public", "identity.(*handler).redeemClaim"},
		{"POST /api/wx/login", "anonymous", "identity.(*handler).wxLogin"},
		{"POST /api/wx/link", "anonymous", "identity.(*handler).wxLink"},
		{"GET /api/games", "public", "games.(*handler).list"},
		{"GET /api/games/{code}", "public", "games.(*handler).detail"},
		{"POST /api/games", "user", "games.(*handler).create"},
		{"PATCH /api/games/{code}", "user", "games.(*handler).update"},
		{"POST /api/games/{code}/seat", "user", "games.(*handler).join"},
		{"DELETE /api/games/{code}/seat", "user", "games.(*handler).leave"},
		{"POST /api/games/{code}/cancel", "user", "games.(*handler).cancel"},
		{"POST /api/games/{code}/kick", "user", "games.(*handler).kick"},
		{"POST /api/games/{code}/invites", "user", "games.(*handler).invite"},
		{"GET /api/games/{code}/score", "public", "games.(*handler).score"},
		{"POST /api/games/{code}/score", "user", "games.(*handler).startScore"},
		{"POST /api/games/{code}/score/rounds", "user", "games.(*handler).recordRound"},
		{"PUT /api/games/{code}/score/rounds/{no}", "user", "games.(*handler).editRound"},
		{"GET /ws", "public+deferidentity", "*realtime.handler"},
		{"GET /g/{code}", "anonymous", "share.(*handler).ogPage"},
		{"GET /g/{code}/og.png", "anonymous", "share.(*handler).ogImage"},
		{"GET /g/{code}/poster.png", "anonymous", "share.(*handler).posterImage"},
		{"GET /g/{code}/game.ics", "anonymous", "share.(*handler).ics"},
		{"GET /g/{code}/google", "anonymous", "share.(*handler).google"},
		{"GET /api/push/key", "anonymous", "notify.(*handler).key"},
		{"POST /api/push/subscribe", "user", "notify.(*handler).subscribe"},
		{"DELETE /api/push/subscribe", "user", "notify.(*handler).unsubscribe"},
		{"POST /api/wx/subscribe", "user", "notify.(*handler).wxSubscribe"},
		{"GET /api/friends", "user", "friends.(*handler).list"},
		{"PUT /api/follows/{user_id}", "user", "friends.(*handler).star"},
		{"DELETE /api/follows/{user_id}", "user", "friends.(*handler).unstar"},
		{"POST /api/agent", "public", "agent.(*handler).draft"},
		{"POST /api/events", "public", "telemetry.(*handler).events"},
		{"POST /api/client-errors", "public", "telemetry.(*handler).clientError"},
		{" /", "anonymous", "httpapi.referrerPolicyForClaim.func1"},
	}
	var got []row
	for _, rt := range httpapi.Table(a.options) {
		access := rt.Access.String()
		if rt.DeferIdentity {
			access += "+deferidentity"
		}
		got = append(got, row{rt.Key(), access, handlerName(rt.Handler)})
	}
	if !reflect.DeepEqual(got, want) {
		var b strings.Builder
		for _, r := range got {
			fmt.Fprintf(&b, "\t\t{%q, %q, %q},\n", r.key, r.access, r.handler)
		}
		t.Fatalf("路由表和预期不一致，实际：\n%s", b.String())
	}
}

func handlerName(h http.Handler) string {
	if f, ok := h.(http.HandlerFunc); ok {
		name := runtime.FuncForPC(reflect.ValueOf(f).Pointer()).Name()
		name = strings.TrimPrefix(name, "github.com/dayuer/jkt-guandan/server/internal/")
		return strings.TrimSuffix(name, "-fm")
	}
	return fmt.Sprintf("%T", h)
}

// 授权矩阵：每条路由用访客、普通用户（device）、这一局的局头（host）、被封禁的用户（banned）各打一次。
//
//   - anonymous：谁来都不会被身份挡住（被封禁也不挡：不识别身份）；
//   - public：访客和用户都能用，被封禁 403 banned；
//   - user：访客 401 unauthorized，被封禁 403 banned；
//   - 局头才能做的事（改局、取消、移出）：普通用户 403 not_host，局头放行；
//   - 座位上的人才能做的事（记分）：不在座位上的普通用户 403 not_seated，局头（坐在 1 号）放行。
//
// 「放行」只看授权：业务上的 400/404/409/503 都算放行。
func TestAuthorizationMatrix(t *testing.T) {
	hostOnly := map[string]bool{
		"PATCH /api/games/{code}": true, "POST /api/games/{code}/cancel": true, "POST /api/games/{code}/kick": true,
		"POST /api/games/{code}/invites": true,
	}
	seatedOnly := map[string]bool{
		"POST /api/games/{code}/score": true, "POST /api/games/{code}/score/rounds": true,
		"PUT /api/games/{code}/score/rounds/{no}": true,
	}
	for _, rt := range httpapi.Table(newApp(t).options) {
		for _, role := range []string{"guest", "device", "host", "banned"} {
			rt, role := rt, role
			t.Run(rt.Key()+" as "+role, func(t *testing.T) {
				a := newApp(t)
				hostClient := a.browser()
				a.signUp(hostClient, "局头")
				code := a.createGame(hostClient)

				client := a.browser()
				switch role {
				case "device":
					a.signUp(client, "牌友")
				case "host":
					client = hostClient
				case "banned":
					id := a.signUp(client, "捣乱的")
					if _, err := a.st.Ban(context.Background(), id, a.clk.Now()); err != nil {
						t.Fatal(err)
					}
				}

				status, errCode, raw := a.probe(client, rt, code)
				// 5xx 不算放行：授权之外的地方坏了，矩阵也该发现（没配 key 的蛋蛋、没配 AppID 的小游戏登录和订阅消息是预期的 503）。
				if status >= 500 && errCode != "agent_disabled" && errCode != "wx_disabled" {
					t.Fatalf("意外的 %d：%s", status, raw)
				}
				want := "pass"
				switch {
				case rt.Access == route.Anonymous:
				case role == "banned":
					want = "banned"
				case role == "guest" && !rt.Access.AllowsGuest():
					want = "unauthorized"
				case role == "device" && hostOnly[rt.Key()]:
					want = "not_host"
				case role == "device" && seatedOnly[rt.Key()]:
					want = "not_seated"
				}
				got := "pass"
				switch {
				case status == http.StatusUnauthorized:
					got = "unauthorized"
				case status == http.StatusForbidden && errCode == "banned":
					got = "banned"
				case status == http.StatusForbidden && rt.Pattern == "/ws" && strings.TrimSpace(raw) == "forbidden":
					got = "banned" // /ws 握手拒绝被封禁的身份是纯文本 403 forbidden
				case status == http.StatusForbidden:
					got = errCode
				}
				if got != want {
					t.Fatalf("期望 %s，实际 %d %s", want, status, errCode)
				}
			})
		}
	}
}

func (a *app) browser() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Transport:     a.srv.Client().Transport,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (a *app) do(c *http.Client, method, path string, body any, header map[string]string) (int, map[string]any) {
	a.t.Helper()
	status, out, _ := a.doRaw(c, method, path, body, header)
	return status, out
}

func (a *app) doRaw(c *http.Client, method, path string, body any, header map[string]string) (int, map[string]any, string) {
	a.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.srv.URL+path, r)
	if err != nil {
		a.t.Fatal(err)
	}
	req.Header.Set("Origin", routesTestOrigin)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, string(raw)
}

func (a *app) signUp(c *http.Client, nickname string) string {
	a.t.Helper()
	status, body := a.do(c, http.MethodPost, "/api/me", map[string]string{"nickname": nickname}, nil)
	if status != http.StatusOK {
		a.t.Fatalf("建身份: %d %v", status, body)
	}
	return body["user"].(map[string]any)["id"].(string)
}

func (a *app) createGame(c *http.Client) string {
	a.t.Helper()
	status, body := a.do(c, http.MethodPost, "/api/games", a.gameBody(), nil)
	if status != http.StatusOK {
		a.t.Fatalf("开局: %d %v", status, body)
	}
	return body["game"].(map[string]any)["short_code"].(string)
}

func (a *app) gameBody() map[string]any {
	return map[string]any{
		"starts_at": a.clk.Now().Add(3 * time.Hour).Unix(), "area": "SCBD", "venue": "Pacific Place", "kind": "classic",
	}
}

// probe 用一个格式合法的请求打这条路由，返回状态码和错误码。
func (a *app) probe(c *http.Client, rt route.Route, code string) (int, string, string) {
	a.t.Helper()
	path := strings.NewReplacer("{code}", code, "{id}", "nonexistent", "{no}", "1").Replace(rt.Pattern)
	method := rt.Method
	if method == "" {
		method, path = http.MethodGet, "/some/spa/page"
	}
	var body any
	header := map[string]string{}
	switch rt.Key() {
	case "POST /api/me":
		body = map[string]string{"nickname": "新名字"}
	case "POST /api/claim/redeem":
		body = map[string]string{"token": "not-a-real-token"}
	case "POST /api/wx/login":
		body = map[string]string{"code": "dev:someone"}
	case "POST /api/wx/link":
		body = map[string]string{"code": "123456", "wx_code": "dev:someone"}
	case "POST /api/games":
		body = a.gameBody()
	case "PATCH /api/games/{code}":
		body, header["If-Match"] = map[string]any{"venue": "Grand Indonesia"}, "1"
	case "POST /api/games/{code}/kick":
		body = map[string]string{"user_id": "someone"}
	case "POST /api/games/{code}/invites":
		body = map[string]any{"user_ids": []string{"someone"}}
	case "POST /api/games/{code}/score":
		body = map[string]any{"a_fail_limit": 3}
	case "POST /api/games/{code}/score/rounds":
		body = map[string]any{"expected_round_no": 1, "head_seat": 1, "partner_place": 2}
	case "PUT /api/games/{code}/score/rounds/{no}":
		body = map[string]any{"head_seat": 1, "partner_place": 2}
	case "POST /api/wx/subscribe":
		body = map[string]any{"accepted": []string{"tmpl"}}
	case "POST /api/push/subscribe":
		body = map[string]any{"endpoint": "https://fcm.googleapis.com/fcm/send/x", "keys": map[string]string{"p256dh": "x", "auth": "y"}}
	case "DELETE /api/push/subscribe":
		body = map[string]string{"endpoint": "https://fcm.googleapis.com/fcm/send/x"}
	case "POST /api/agent":
		body = map[string]string{"text": "周四晚上打一局"}
	case "POST /api/events":
		body = map[string]any{"env": "browser", "events": []map[string]any{{"name": "detail_view"}}}
	case "POST /api/client-errors":
		body = map[string]string{"env": "browser", "message": "boom"}
	}
	status, out, raw := a.doRaw(c, method, path, body, header)
	var errCode string
	if e, ok := out["error"].(map[string]any); ok {
		errCode, _ = e["code"].(string)
	}
	return status, errCode, raw
}

// GET /ws 是 DeferIdentity：先查 Origin，过了才识别身份。被封禁的身份带着错误的 Origin 来，
// 应该被 Origin 拦下（origin not allowed），而不是被封禁拦下（forbidden）——以后有人去掉
// DeferIdentity、把识别身份提到前面，这个测试就会失败。
func TestRealtimeChecksOriginBeforeIdentity(t *testing.T) {
	a := newApp(t)
	c := a.browser()
	id := a.signUp(c, "捣乱的")
	if _, err := a.st.Ban(context.Background(), id, a.clk.Now()); err != nil {
		t.Fatal(err)
	}
	get := func(origin string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, a.srv.URL+"/ws", nil)
		req.Header.Set("Origin", origin)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, strings.TrimSpace(string(b))
	}
	if status, body := get("https://evil.example"); status != http.StatusForbidden || body != "origin not allowed" {
		t.Fatalf("错误的 Origin 应先被拦下：%d %q", status, body)
	}
	if status, body := get(routesTestOrigin); status != http.StatusForbidden || body != "forbidden" {
		t.Fatalf("Origin 正确时才轮到封禁：%d %q", status, body)
	}
}

// #123 复审：两个埋点接口走真实路由表要能用，不是 500。
// （main 里漏填 telemetry.Options 时，Store 为空，两个接口会一直 500。）
func TestTelemetryRoutesWorkThroughRealRouter(t *testing.T) {
	a := newApp(t)
	c := a.browser()
	status, body, raw := a.doRaw(c, http.MethodPost, "/api/events", map[string]any{
		"env": "wechat", "events": []map[string]any{{"name": "detail_view"}, {"name": "jumpout"}},
	}, nil)
	if status != http.StatusOK || body["accepted"] != 2.0 {
		t.Fatalf("POST /api/events: %d %s", status, raw)
	}
	status, _, raw = a.doRaw(c, http.MethodPost, "/api/client-errors",
		map[string]any{"env": "pwa", "message": "boom"}, nil)
	if status != http.StatusNoContent {
		t.Fatalf("POST /api/client-errors: %d %s", status, raw)
	}
	var n int
	if err := a.st.Read().QueryRow(`SELECT count(*) FROM events WHERE name IN ('detail_view','jumpout','client_error')`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("应存下 3 条，实际 %d, %v", n, err)
	}
}

// appRoutes 里漏填 telemetry 要启动即失败，不是上线后 500。
func TestAppRoutesRequiresTelemetryStore(t *testing.T) {
	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), "telemetry") {
			t.Fatalf("telemetry 没填 Store 应当因它而 panic，实际 %v", r)
		}
	}()
	appRoutes(appDeps{})
}
