package games_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
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
		Config:   config.Config{DataDir: t.TempDir(), Dev: true, PublicURL: testOrigin},
		Store:    st,
		Clock:    clk,
		Identity: identSvc,
		Routes:   slices.Concat(identity.Routes(identSvc), games.Routes(gamesSvc)),
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
	for _, rt := range games.Routes(nil) { // 只列路由，不处理请求
		rt := rt
		for _, role := range []string{"guest", "user"} {
			role := role
			t.Run(rt.Method+" "+rt.Pattern+" as "+role, func(t *testing.T) {
				f := newServer(t)

				client := f.http
				code := "ZZZZZZ" // 路由存在但这一局不存在：只关心授权结果
				if role == "user" {
					f.signUp(client, "局头")
					code = f.createGame(client, nil)
				}

				path := strings.ReplaceAll(rt.Pattern, "{code}", code)
				status := f.probe(client, rt.Method, path).status

				permitted := role == "user" || rt.Access.AllowsGuest()
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

// 移出一个不在这一局里的人：404 not_in_game（不能给任意用户推送「你被移出了」）。
func TestKickOutsiderOverHTTP(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	stranger := f.newBrowser()
	strangerID := f.signUp(stranger, "路人")

	r := f.do(f.http, http.MethodPost, "/api/games/"+code+"/kick",
		map[string]any{"user_id": strangerID}, nil)
	if r.status != http.StatusNotFound || r.errCode() != "not_in_game" {
		t.Fatalf("移出不在局里的人应当 404 not_in_game，实际 %d %s", r.status, r.raw)
	}
}

// 终态的局：报名/退出/移出/修改都是 409 game_closed；重复取消幂等地成功。
func TestClosedGameOverHTTP(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	other := f.newBrowser()
	f.signUp(other, "甲")
	if r := f.do(other, http.MethodPost, "/api/games/"+code+"/seat", nil, nil); r.status != http.StatusOK {
		t.Fatalf("报名: %d %s", r.status, r.raw)
	}

	if r := f.do(f.http, http.MethodPost, "/api/games/"+code+"/cancel", nil, nil); r.status != http.StatusOK {
		t.Fatalf("取消: %d %s", r.status, r.raw)
	}
	// 重复取消：幂等地成功。
	if r := f.do(f.http, http.MethodPost, "/api/games/"+code+"/cancel", nil, nil); r.status != http.StatusOK {
		t.Fatalf("重复取消应当成功: %d %s", r.status, r.raw)
	}

	for name, r := range map[string]resp{
		"报名": f.do(other, http.MethodPost, "/api/games/"+code+"/seat", nil, nil),
		"退出": f.do(other, http.MethodDelete, "/api/games/"+code+"/seat", nil, nil),
	} {
		if r.status != http.StatusConflict || r.errCode() != "game_closed" {
			t.Fatalf("已取消的局上「%s」应当 409 game_closed，实际 %d %s", name, r.status, r.raw)
		}
	}
}

// If-Match 按 HTTP 规范可以是带引号的 ETag，两种写法都收。
func TestPatchAcceptsQuotedIfMatch(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, nil)

	detail := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil)
	version := int64(detail.gameField("version").(float64))

	r := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]any{"venue": "Grand Indonesia"},
		map[string]string{"If-Match": `"` + strconv.FormatInt(version, 10) + `"`})
	if r.status != http.StatusOK {
		t.Fatalf("带引号的 If-Match 应当被接受，实际 %d %s", r.status, r.raw)
	}
}

// ---------------------------------------------------------------------------
// 内容过滤（§14）
// ---------------------------------------------------------------------------

// 备注或地点命中敏感词时，开局要被 422 拒绝，并且告诉前端是哪个字段——
// 前端据此把提示放到对应的输入框下面、不清空用户写好的内容（原型 X1）。
func TestCreateRejectsSensitiveWords(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")

	cases := []struct {
		name    string
		venue   string
		note    string
		wantFld string
	}{
		{"备注里写钱（设计稿的例子）", "Pacific Place", "一把 50 块钱，输了请宵夜", "note"},
		{"备注里说赌", "Pacific Place", "小赌怡情", "note"},
		{"地点里带敏感词", "钱柜包厢", "", "venue"},
		{"印尼语", "Pacific Place", "judi santai", "note"},
		// #130：拉丁词紧贴着汉字也要拦住（生产环境走查时「测试judi」「带点taruhan」漏过去了）。
		{"备注：汉字紧贴 judi", "Pacific Place", "测试judi", "note"},
		{"备注：汉字紧贴 taruhan", "Pacific Place", "带点taruhan", "note"},
		{"地点：汉字紧贴 judi", "judi馆二楼", "", "venue"},
		{"地点：汉字夹 bet", "Kemang 来bet局", "", "venue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(),
				"area":      "SCBD",
				"venue":     tc.venue,
				"kind":      "classic",
			}
			if tc.note != "" {
				body["note"] = tc.note
			}
			r := f.do(f.http, http.MethodPost, "/api/games", body, nil)
			if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" {
				t.Fatalf("应当 422 sensitive_word，实际 %d %s", r.status, r.raw)
			}
			e, _ := r.body["error"].(map[string]any)
			if got, _ := e["field"].(string); got != tc.wantFld {
				t.Fatalf("field = %q，期望 %q（响应 %s）", got, tc.wantFld, r.raw)
			}
			// 提示文案要说清楚怎么办，而不是只报错。
			if msg, _ := e["message"].(string); !strings.Contains(msg, "请勿涉及金钱") {
				t.Fatalf("提示文案不对：%q", msg)
			}
		})
	}

	// 被拒之后不该留下局。
	r := f.do(f.http, http.MethodGet, "/api/games", nil, nil)
	if games, _ := r.body["games"].([]any); len(games) != 0 {
		t.Fatalf("被拒的局不该建出来，列表里有 %d 个", len(games))
	}
}

// 正常内容照样能开——误伤的代价比漏掉高。
func TestCreateAcceptsNormalContent(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")

	for _, body := range []map[string]any{
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "SCBD",
			"venue": "Pacific Place · 鼎泰丰包间", "kind": "classic", "note": "新手局，随便打打，教规则"},
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "PIK",
			"venue": "Bandar Djakarta 二楼", "kind": "classic"},
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "Kemang",
			"venue": "Coffee Bean", "kind": "teaching", "note": "人均 200 左右，各付各的"},
		// #130 之后汉字算词边界，但含敏感词子串的正常拉丁词紧贴汉字时仍然放行。
		{"starts_at": f.clk.Now().Add(2 * time.Hour).Unix(), "area": "Kemang",
			"venue": "Betawi菜馆", "kind": "classic", "note": "alphabet街口集合，带上chipset笔记本也行"},
	} {
		r := f.do(f.http, http.MethodPost, "/api/games", body, nil)
		if r.status != http.StatusOK {
			t.Fatalf("正常内容被拒了：%d %s（%v）", r.status, r.raw, body)
		}
	}
}

// 「改」不能绕过「开」那一关。
func TestPatchRejectsSensitiveWords(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, map[string]string{"Idempotency-Key": "sensitive-1"})

	r := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]string{"note": "赢钱请宵夜"}, map[string]string{"If-Match": "1"})
	if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" {
		t.Fatalf("改备注应当 422 sensitive_word，实际 %d %s", r.status, r.raw)
	}

	r = f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]string{"venue": "筹码酒吧"}, map[string]string{"If-Match": "1"})
	if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" {
		t.Fatalf("改地点应当 422 sensitive_word，实际 %d %s", r.status, r.raw)
	}

	// #130：改的时候拉丁词紧贴汉字也拦得住。
	for field, text := range map[string]string{"note": "带点taruhan", "venue": "测试judi馆"} {
		r = f.do(f.http, http.MethodPatch, "/api/games/"+code,
			map[string]string{field: text}, map[string]string{"If-Match": "1"})
		if r.status != http.StatusUnprocessableEntity || r.errCode() != "sensitive_word" {
			t.Fatalf("改 %s 为 %q 应当 422 sensitive_word，实际 %d %s", field, text, r.status, r.raw)
		}
	}

	// 被拒之后这一局没有被改动（版本号还是 1）。
	if v := f.do(f.http, http.MethodGet, "/api/games/"+code, nil, nil).gameField("version"); v != float64(1) {
		t.Fatalf("被拒的修改不该改到牌局，version = %v", v)
	}

	// 正常修改照旧。
	r = f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]string{"note": "各付各的，纯娱乐"}, map[string]string{"If-Match": "1"})
	if r.status != http.StatusOK {
		t.Fatalf("正常修改应当成功，实际 %d %s", r.status, r.raw)
	}
}

// 词表后来才加的词，不该卡住局头改时间（只检查这次要改的字段）。
func TestPatchWithSensitiveOldNoteCanStillChangeTime(t *testing.T) {
	f := newServer(t)
	f.signUp(f.http, "局头")
	code := f.createGame(f.http, map[string]string{"Idempotency-Key": "sensitive-2"})

	// 直接写库，绕开接口，模拟「词表是后来才加的词」。
	var changed int64
	if err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		res, err := tx.ExecContext(ctx,
			`UPDATE games SET note = '筹码局' WHERE short_code = ?`, code)
		if err != nil {
			return err
		}
		changed, err = res.RowsAffected()
		return err
	}); err != nil {
		t.Fatalf("写库: %v", err)
	}
	if changed != 1 {
		t.Fatalf("没写进去，影响了 %d 行", changed)
	}

	newStart := f.clk.Now().Add(5 * time.Hour).Unix()
	r := f.do(f.http, http.MethodPatch, "/api/games/"+code,
		map[string]any{"starts_at": newStart}, map[string]string{"If-Match": "1"})
	if r.status != http.StatusOK {
		t.Fatalf("只改时间应当成功，实际 %d %s", r.status, r.raw)
	}
}
