package games_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// 和 identity 的测试一样：生产 cookie 带 __Host- 前缀和 Secure，
// 所以测试服务器必须是 TLS，否则 cookie jar 根本不会把它发回来。
const testOrigin = "https://guandan.id"

type srvFixture struct {
	t   *testing.T
	srv *httptest.Server
	st  *store.Store
	clk *clock.Fake
	// http 是「这台浏览器」，带自己的 cookie jar。
	http *http.Client
}

func newServer(t *testing.T) *srvFixture {
	t.Helper()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	clk := clock.NewFake(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	identSvc := identity.NewService(st, clk, true)
	gamesSvc := games.NewService(st, clk, nil)

	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config: config.Config{DataDir: t.TempDir(), Dev: true, PublicURL: testOrigin},
		Store:  st,
		Clock:  clk,
		RegisterRoutes: func(mux *http.ServeMux) {
			identity.Register(mux, identSvc)
			games.Register(mux, gamesSvc, identSvc)
		},
	}))
	t.Cleanup(srv.Close)

	f := &srvFixture{t: t, srv: srv, st: st, clk: clk}
	f.http = f.newBrowser()
	return f
}

// newBrowser 建一个独立 cookie 的客户端，只复用测试服务器的传输层
// （srv.Client() 每次返回同一个对象，直接在上面设 Jar 会把所有浏览器串成一个）。
func (f *srvFixture) newBrowser() *http.Client {
	f.t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		f.t.Fatalf("建 cookie jar: %v", err)
	}
	return &http.Client{
		Transport: f.srv.Client().Transport,
		Jar:       jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type resp struct {
	status int
	header http.Header
	raw    string
	body   map[string]any
}

func (r resp) str(key string) string {
	s, _ := r.body[key].(string)
	return s
}

// gameField 取响应里 game 对象的字段。
func (r resp) gameField(key string) any {
	g, _ := r.body["game"].(map[string]any)
	return g[key]
}

func (r resp) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func (f *srvFixture) do(client *http.Client, method, path string, body any, headers map[string]string) resp {
	f.t.Helper()

	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("编码请求体: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	if err != nil {
		f.t.Fatalf("构造请求: %v", err)
	}
	// CSRF 要求会改数据的请求带本站 Origin 和 JSON 的类型（§10.5）。
	req.Header.Set("Origin", testOrigin)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	res, err := client.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)

	out := resp{status: res.StatusCode, header: res.Header, raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

// signUp 走一遍「填称呼」，让这个浏览器有身份。
func (f *srvFixture) signUp(client *http.Client, nickname string) string {
	f.t.Helper()
	r := f.do(client, http.MethodPost, "/api/me", map[string]string{"nickname": nickname}, nil)
	if r.status != http.StatusOK {
		f.t.Fatalf("填称呼失败：%d %s", r.status, r.raw)
	}
	u, _ := r.body["user"].(map[string]any)
	id, _ := u["id"].(string)
	return id
}

func (f *srvFixture) createGame(client *http.Client, extra map[string]string) string {
	f.t.Helper()
	r := f.do(client, http.MethodPost, "/api/games", map[string]any{
		"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(),
		"area":      "SCBD",
		"venue":     "Pacific Place",
		"kind":      "classic",
	}, extra)
	if r.status != http.StatusOK {
		f.t.Fatalf("开局失败：%d %s", r.status, r.raw)
	}
	return r.gameField("short_code").(string)
}

// ---------------------------------------------------------------------------
// 基本流程
// ---------------------------------------------------------------------------

func TestCreateJoinLeaveOverHTTP(t *testing.T) {
	f := newServer(t)

	hostID := f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	// 局头自己已经在座位上了。
	detail := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
	if detail.status != http.StatusOK {
		t.Fatalf("读详情：%d %s", detail.status, detail.raw)
	}
	me, _ := detail.body["me"].(map[string]any)
	if me["role"] != "seat" || me["seat_no"].(float64) != 1 {
		t.Fatalf("局头应当在 1 号座上，实际 %v", me)
	}
	if detail.body["missing"].(float64) != 3 {
		t.Fatalf("还差 3 人，实际 %v", detail.body["missing"])
	}

	// 另一个浏览器报名。
	other := f.newBrowser()
	otherID := f.signUp(other, "牌友")
	joined := f.do(other, http.MethodPost, "/api/games/"+code+"/seat", nil, nil)
	if joined.status != http.StatusOK {
		t.Fatalf("报名失败：%d %s", joined.status, joined.raw)
	}
	if joined.body["missing"].(float64) != 2 {
		t.Fatalf("报名后还差 2 人，实际 %v", joined.body["missing"])
	}

	// 再报一次是幂等的，不会占两个座。
	again := f.do(other, http.MethodPost, "/api/games/"+code+"/seat", nil, nil)
	if again.status != http.StatusOK {
		t.Fatalf("重复报名应当成功：%d %s", again.status, again.raw)
	}
	var gameID string
	if err := f.st.Read().QueryRow(`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	var seats int
	if err := f.st.Read().QueryRow(`SELECT count(*) FROM seats WHERE game_id = ?`, gameID).Scan(&seats); err != nil {
		t.Fatalf("数座位: %v", err)
	}
	if seats != 2 {
		t.Fatalf("重复报名不该多占座，实际 %d 个座位", seats)
	}

	// 退出。
	left := f.do(other, http.MethodDelete, "/api/games/"+code+"/seat", nil, nil)
	if left.status != http.StatusOK {
		t.Fatalf("退出失败：%d %s", left.status, left.raw)
	}
	if left.body["missing"].(float64) != 3 {
		t.Fatalf("退出后应当还差 3 人，实际 %v", left.body["missing"])
	}
	_ = hostID
	_ = otherID
}

func TestGuestCanViewButNotJoin(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	guest := f.newBrowser()
	if r := f.do(guest, http.MethodGet, "/api/games/"+code, nil, nil); r.status != http.StatusOK {
		t.Fatalf("访客应当能看牌局详情：%d", r.status)
	}
	r := f.do(guest, http.MethodPost, "/api/games/"+code+"/seat", nil, nil)
	if r.status != http.StatusUnauthorized {
		t.Fatalf("访客报名应当 401，实际 %d %s", r.status, r.raw)
	}
	if r := f.do(guest, http.MethodGet, "/api/games", nil, nil); r.status != http.StatusOK {
		t.Fatalf("访客应当能看首页列表：%d", r.status)
	}
}

func TestUnknownGameReturns404(t *testing.T) {
	f := newServer(t)
	r := f.do(f.http, http.MethodGet, "/api/games/ZZZZZZ", nil, nil)
	if r.status != http.StatusNotFound || r.errCode() != "game_not_found" {
		t.Fatalf("不存在的局应当 404 game_not_found，实际 %d %s", r.status, r.raw)
	}
}

// PATCH 必须带 If-Match，版本不对返回 409 和最新数据（§11.3）。
func TestPatchRequiresIfMatch(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	without := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]any{"venue": "Grand Indonesia"}, nil)
	if without.status != http.StatusBadRequest {
		t.Fatalf("没有 If-Match 应当 400，实际 %d %s", without.status, without.raw)
	}

	detail := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
	version := int64(detail.gameField("version").(float64))

	ok := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]any{"venue": "Grand Indonesia"}, map[string]string{"If-Match": strconv.FormatInt(version, 10)})
	if ok.status != http.StatusOK {
		t.Fatalf("带对版本号应当成功：%d %s", ok.status, ok.raw)
	}

	// 同一个版本号再改一次：409，并且带回最新的牌局。
	stale := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]any{"venue": "PIK Avenue"}, map[string]string{"If-Match": strconv.FormatInt(version, 10)})
	if stale.status != http.StatusConflict || stale.errCode() != "version_conflict" {
		t.Fatalf("旧版本号应当 409 version_conflict，实际 %d %s", stale.status, stale.raw)
	}
	if stale.gameField("version").(float64) <= float64(version) {
		t.Fatalf("409 里应当带上最新的版本号，实际 %v", stale.gameField("version"))
	}
}

// 同一个 Idempotency-Key 提交两次只开一局（§11.4）。
func TestIdempotencyKeyOverHTTP(t *testing.T) {
	f := newServer(t)
	hostID := f.signUp(f.http, "局头")

	headers := map[string]string{"Idempotency-Key": "abc-123"}
	first := f.createGame(f.http, headers)
	second := f.createGame(f.http, headers)

	if first != second {
		t.Fatalf("同一个键应当返回同一局，实际 %s vs %s", first, second)
	}
	var n int
	if err := f.st.Read().QueryRow(`SELECT count(*) FROM games WHERE host_id = ?`, hostID).Scan(&n); err != nil {
		t.Fatalf("数牌局: %v", err)
	}
	if n != 1 {
		t.Fatalf("只应当开出一局，实际 %d 局", n)
	}
}

func TestCreateValidatesInput(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")

	cases := []map[string]any{
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "SCBD", "venue": "", "kind": "classic"},
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "不存在的片区", "venue": "X", "kind": "classic"},
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "SCBD",
			"venue": strings.Repeat("长", 41), "kind": "classic"},
		{"starts_at": f.clk.Now().Add(time.Minute).Unix(), "area": "SCBD", "venue": "X", "kind": "classic"},
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "SCBD", "venue": "X", "kind": "不存在的玩法"},
	}
	for i, body := range cases {
		r := f.do(f.http, http.MethodPost, "/api/games", body, nil)
		if r.status != http.StatusBadRequest || r.errCode() != "invalid_input" {
			t.Fatalf("第 %d 个用例应当被 400 invalid_input 拒绝，实际 %d %s", i+1, r.status, r.raw)
		}
	}
}

// ---------------------------------------------------------------------------
// 授权矩阵（§10.12）
// ---------------------------------------------------------------------------

// 每条路由都用访客和有身份的用户各打一次。没在路由表里声明权限的路由不会出现在这里，
// 因为 Register 也是按这张表注册的。
func TestAuthorizationMatrix(t *testing.T) {
	for _, rt := range games.Routes {
		rt := rt
		for _, role := range []games.Role{games.RoleGuest, games.RoleUser} {
			role := role
			t.Run(rt.Method+" "+rt.Pattern+" as "+string(role), func(t *testing.T) {
				f := newServer(t)

				client := f.http
				code := "ZZZZZZ" // 路由存在但这一局不存在：只关心授权结果
				if role == games.RoleUser {
					f.signUp(client, "局头")
					code = f.createGame(client, nil)
				}

				path := strings.ReplaceAll(rt.Pattern, "{code}", code)
				status := f.probe(client, rt.Method, path).status

				permitted := rt.Permitted[role]
				switch {
				case permitted && (status == http.StatusUnauthorized || status == http.StatusForbidden):
					t.Fatalf("路由已声明允许 %s，却返回 %d", role, status)
				case !permitted && status != http.StatusUnauthorized:
					t.Fatalf("路由没有声明允许 %s，应当返回 401，实际 %d", role, status)
				}
			})
		}
	}
}

// probe 用一个格式合法的请求打这条路由，只看授权结果。
func (f *srvFixture) probe(client *http.Client, method, path string) resp {
	f.t.Helper()
	var body any
	switch {
	case method == http.MethodPost && strings.HasSuffix(path, "/kick"):
		body = map[string]string{"user_id": "someone"}
	case method == http.MethodPost && path == "/api/games":
		body = map[string]any{
			"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(),
			"area":      "SCBD", "venue": "Pacific Place", "kind": "classic",
		}
	case method == http.MethodPatch:
		body = map[string]any{"venue": "Grand Indonesia"}
	}
	headers := map[string]string{}
	if method == http.MethodPatch {
		headers["If-Match"] = "1"
	}
	return f.do(client, method, path, body, headers)
}
