package identity_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/realtime"
	"github.com/dayuer/jkt-guandan/internal/route"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// 写这个包的测试时踩过的两个坑，记在这儿省得再踩：
//
//  1. **必须用 `httptest.NewTLSServer`，不能用 `NewServer`。** 生产环境的 cookie 叫
//     `__Host-gd_dev`、带 `Secure`，而 cookie jar 不会把 Secure cookie 发回普通 http
//     地址，用 `NewServer` 的话服务端永远认不出身份，症状是「创建成功但下一个请求 401」。
//  2. **`srv.Client()` 每次返回的是同一个 `*http.Client` 指针。** 想在它上面设 `Jar`
//     来模拟「另一个浏览器」，会把所有浏览器串成同一个。要自己 new 一个 client，
//     只复用它的 `Transport`（见 newBrowser）。
//
// 另外：cookie 的属性（Secure / HttpOnly / SameSite）只能从原始的 Set-Cookie 响应头上看，
// jar 取回来的 cookie 只剩名字和值。
const testOrigin = "https://guandan.id"

type fixture struct {
	t   *testing.T
	srv *httptest.Server
	svc *identity.Service
	st  *store.Store
	clk *clock.Fake
	// http 带 cookie jar，模拟同一个浏览器。
	http *http.Client
}

func newFixture(t *testing.T, dev bool) *fixture {
	t.Helper()
	return newFixtureWithHub(t, dev, nil)
}

// newFixtureWithHub 和 newFixture 一样，另外在 hub 非空时挂上 GET /ws，并把身份事件接到 hub 上。
func newFixtureWithHub(t *testing.T, dev bool, hub *realtime.Hub) *fixture {
	t.Helper()

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// open 之后要显式跑迁移（§12.1 的顺序：quick_check 在迁移之前）。
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	svc := identity.NewService(st, clk, dev)
	cfg := config.Config{DataDir: dir, Dev: dev, PublicURL: testOrigin}
	if hub != nil {
		svc.SetPublisher(hubPublisher{hub})
	}

	// 必须用 TLS：生产环境的 cookie 名带 __Host- 前缀、带 Secure，
	// 而 cookie jar 不会把 Secure cookie 发回普通 http 的地址，
	// 用 NewServer 的话测试里根本认不出身份。
	routes := identity.Routes(svc)
	if hub != nil {
		// 和 cmd/guandan 的组装一样：/ws 走同一个身份中间件，封禁交给握手处理（DeferBan）。
		routes = append(routes, route.Route{
			Method: http.MethodGet, Pattern: "/ws", Access: route.Public, DeferBan: true,
			Handler: realtime.Handler(realtime.Options{
				Hub:         hub,
				AllowOrigin: cfg.AllowsOrigin,
				Identify: func(r *http.Request) (realtime.Principal, error) {
					userID, deviceID, err := identity.ForRealtime(r)
					return realtime.Principal{UserID: userID, DeviceID: deviceID}, err
				},
				Revalidate: func(ctx context.Context, p realtime.Principal) bool {
					return svc.StillValid(ctx, p.UserID, p.DeviceID)
				},
			}),
		})
	}
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config:   cfg,
		Store:    st,
		Clock:    clk,
		Identity: svc,
		Routes:   routes,
	}))
	t.Cleanup(srv.Close)

	f := &fixture{t: t, srv: srv, svc: svc, st: st, clk: clk}
	f.http = f.newBrowser() // 模拟「这台浏览器」
	return f
}

// newBrowser 建一个独立 cookie 的客户端，用来模拟「另一台设备 / 另一个浏览器」。
//
// 只复用测试服务器的传输层（它信任自签证书），不能直接用 srv.Client()：
// 它每次返回的是同一个 *http.Client 指针，在上面设 Jar 会把所有「浏览器」串成同一个。
func (f *fixture) newBrowser() *http.Client {
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

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (f *fixture) do(method, path string, body any) response {
	f.t.Helper()
	return f.doWith(f.http, method, path, body)
}

func (f *fixture) doWith(client *http.Client, method, path string, body any) response {
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
	// CSRF 中间件要求带本站 Origin 和 JSON 的 Content-Type（§10.5）。
	// 注意：会改数据的请求即使没有请求体（比如 POST /api/claim、DELETE 退出设备），
	// 也必须带上 Content-Type: application/json，否则会被 CSRF 拦下。
	req.Header.Set("Origin", testOrigin)
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", "test-browser")

	resp, err := client.Do(req)
	if err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		f.t.Fatalf("读响应: %v", err)
	}

	out := response{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out.body)
	}
	return out
}

func (r response) str(key string) string {
	s, _ := r.body[key].(string)
	return s
}

func (r response) userField(key string) string {
	u, _ := r.body["user"].(map[string]any)
	s, _ := u[key].(string)
	return s
}

func (r response) errCode() string {
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

// createIdentity 走一遍「首次填称呼」。
func (f *fixture) createIdentity(client *http.Client, nickname string) string {
	f.t.Helper()
	resp := f.doWith(client, http.MethodPost, "/api/me", map[string]string{"nickname": nickname})
	if resp.status != http.StatusOK {
		f.t.Fatalf("创建身份失败：%d %s", resp.status, resp.raw)
	}
	return resp.userField("id")
}

func (f *fixture) deviceCookie(client *http.Client) *http.Cookie {
	f.t.Helper()
	u, _ := url.Parse(f.srv.URL)
	for _, c := range client.Jar.Cookies(u) {
		if strings.HasSuffix(c.Name, "gd_dev") {
			return c
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 身份创建
// ---------------------------------------------------------------------------

func TestCreateIdentityServesCookieAndToken(t *testing.T) {
	f := newFixture(t, false)

	resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"})
	if resp.status != http.StatusOK {
		t.Fatalf("状态码 %d：%s", resp.status, resp.raw)
	}
	if got := resp.userField("nickname"); got != "老王" {
		t.Fatalf("称呼 %q", got)
	}

	if _, ok := resp.body["device_token"]; ok {
		t.Fatal("响应体里不能出现 device_token：它只该走 Set-Cookie（HttpOnly）")
	}
	cookie := f.deviceCookie(f.http)
	if cookie == nil {
		t.Fatal("应当下发设备 cookie")
	}
	if cookie.Value == "" {
		t.Fatal("cookie 值不该为空")
	}
	if strings.Contains(resp.raw, cookie.Value) {
		t.Fatal("响应体里出现了 cookie 的值，等于把 HttpOnly 绕过去了")
	}

	// GET /api/me 能认出同一个人，并列出设备。
	me := f.do(http.MethodGet, "/api/me", nil)
	if me.status != http.StatusOK {
		t.Fatalf("GET /api/me 状态码 %d", me.status)
	}
	if me.userField("id") != resp.userField("id") {
		t.Fatalf("GET /api/me 认回了不同的用户：%s vs %s", me.userField("id"), resp.userField("id"))
	}
	devices, _ := me.body["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("设备数 %d，期望 1", len(devices))
	}
}

// parseSetCookie 把 Set-Cookie 拆成名字、值和属性。
//
// 属性必须从原始响应头上看：cookie jar 取回来的 cookie 只有名字和值，
// 属性（Secure / HttpOnly / SameSite）在往返过程中会丢掉。
func parseSetCookie(t *testing.T, raw string) (name, value string, attrs map[string]string) {
	t.Helper()
	parts := strings.Split(raw, ";")
	if len(parts) == 0 || !strings.Contains(parts[0], "=") {
		t.Fatalf("Set-Cookie 格式不对: %q", raw)
	}
	name, value, _ = strings.Cut(strings.TrimSpace(parts[0]), "=")

	attrs = map[string]string{}
	for _, p := range parts[1:] {
		p = strings.TrimSpace(p)
		k, v, _ := strings.Cut(p, "=")
		attrs[strings.ToLower(k)] = v
	}
	return name, value, attrs
}

func (f *fixture) createAndReadSetCookie() (name, value string, attrs map[string]string) {
	f.t.Helper()
	resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"})
	if resp.status != http.StatusOK {
		f.t.Fatalf("创建身份失败：%d %s", resp.status, resp.raw)
	}
	return parseSetCookie(f.t, resp.header.Get("Set-Cookie"))
}

// §10.3 生产环境的 cookie 属性。
func TestProductionCookieAttributes(t *testing.T) {
	f := newFixture(t, false)
	name, value, attrs := f.createAndReadSetCookie()

	if name != identity.ProdCookieName {
		t.Fatalf("cookie 名 %q，期望 %q", name, identity.ProdCookieName)
	}
	if value == "" {
		t.Fatal("cookie 值不该为空")
	}
	if _, ok := attrs["secure"]; !ok {
		t.Fatalf("生产环境的 cookie 必须带 Secure：%v", attrs)
	}
	if _, ok := attrs["httponly"]; !ok {
		t.Fatalf("必须带 HttpOnly：%v", attrs)
	}
	if attrs["path"] != "/" {
		t.Fatalf("Path 应当是 /，实际 %q", attrs["path"])
	}
	if attrs["samesite"] != "Lax" {
		t.Fatalf("SameSite 应当是 Lax，实际 %q", attrs["samesite"])
	}
	// `__Host-` 前缀要求不带 Domain。
	if _, ok := attrs["domain"]; ok {
		t.Fatalf("`__Host-` cookie 不能设 Domain：%v", attrs)
	}
	if attrs["max-age"] == "" {
		t.Fatalf("应当有 Max-Age：%v", attrs)
	}
}

// §10.3 开发环境：局域网 IP 不是安全上下文，带 Secure 的 cookie 会被浏览器丢掉，
// 所以换成不带前缀的名字，也不带 Secure。
func TestDevCookieAttributes(t *testing.T) {
	f := newFixture(t, true)
	name, _, attrs := f.createAndReadSetCookie()

	if name != identity.DevCookieName {
		t.Fatalf("开发环境 cookie 名 %q，期望 %q", name, identity.DevCookieName)
	}
	if _, ok := attrs["secure"]; ok {
		t.Fatalf("开发环境的 cookie 不能带 Secure，否则局域网下会被浏览器丢掉：%v", attrs)
	}
	if _, ok := attrs["httponly"]; !ok {
		t.Fatalf("其余属性应当和生产一致，缺 HttpOnly：%v", attrs)
	}
	if attrs["path"] != "/" || attrs["samesite"] != "Lax" {
		t.Fatalf("其余属性应当和生产一致：%v", attrs)
	}
}

// 任何响应体里都不能出现 device_token 明文——它只走 Set-Cookie（§10.3）。
// 一旦在响应体里回一次，JS 就能读到，HttpOnly 就白设了。
func TestNoResponseBodyLeaksDeviceToken(t *testing.T) {
	f := newFixture(t, false)

	created := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"})
	assertTokenOnlyInCookie(t, created)

	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")
	byToken := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	assertTokenOnlyInCookie(t, byToken)

	code := f.do(http.MethodPost, "/api/claim", nil).str("code")
	byCode := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", map[string]string{"code": code})
	assertTokenOnlyInCookie(t, byCode)
}

func assertTokenOnlyInCookie(t *testing.T, resp response) {
	t.Helper()
	if strings.Contains(resp.raw, "device_token") {
		t.Fatalf("响应体里出现了 device_token：%s", resp.raw)
	}
	if resp.header.Get("Set-Cookie") == "" {
		t.Fatal("响应应当通过 Set-Cookie 下发设备凭证")
	}
}

func TestCreateIdentityRejectsBadNickname(t *testing.T) {
	f := newFixture(t, false)

	for _, bad := range []string{"", "   ", "\n\t", strings.Repeat("长", identity.NicknameMax+1)} {
		resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": bad})
		if resp.status != http.StatusBadRequest || resp.errCode() != "nickname_invalid" {
			t.Fatalf("称呼 %q 应当被拒绝，实际 %d %s", bad, resp.status, resp.raw)
		}
	}
}

func TestCreateIdentityTrimsControlCharacters(t *testing.T) {
	f := newFixture(t, false)

	resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "  老\x00王\n "})
	if resp.status != http.StatusOK {
		t.Fatalf("状态码 %d：%s", resp.status, resp.raw)
	}
	if got := resp.userField("nickname"); got != "老王" {
		t.Fatalf("称呼应当去掉控制字符和首尾空白，实际 %q", got)
	}
}

func TestCreateIdentityIsIdempotent(t *testing.T) {
	f := newFixture(t, false)

	first := f.createIdentity(f.http, "老王")
	second := f.createIdentity(f.http, "老王")

	if first != second {
		t.Fatalf("已有身份的人再提交不该新建用户：%s vs %s", first, second)
	}
	if resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); resp.body["device_token"] != nil {
		t.Fatal("响应体里不该有 device_token")
	}
}

func TestGetMeRequiresIdentity(t *testing.T) {
	f := newFixture(t, false)

	resp := f.do(http.MethodGet, "/api/me", nil)
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("状态码 %d，期望 401", resp.status)
	}
}

// ---------------------------------------------------------------------------
// claim token
// ---------------------------------------------------------------------------

func TestIssueClaimRequiresIdentity(t *testing.T) {
	f := newFixture(t, false)

	resp := f.do(http.MethodPost, "/api/claim", nil)
	if resp.status != http.StatusUnauthorized {
		t.Fatalf("状态码 %d，期望 401", resp.status)
	}
}

func TestRedeemByTokenMovesIdentityToNewBrowser(t *testing.T) {
	f := newFixture(t, false)

	original := f.createIdentity(f.http, "老王")

	claim := f.do(http.MethodPost, "/api/claim", nil)
	if claim.status != http.StatusOK {
		t.Fatalf("签发 claim 失败：%d %s", claim.status, claim.raw)
	}
	token := claim.str("claim_token")
	if token == "" || len(claim.str("code")) != identity.ClaimCodeDigits {
		t.Fatalf("应当同时返回 token 和 4 位认领码：%s", claim.raw)
	}

	// 另一个浏览器（比如从微信跳到 Chrome）。
	other := f.newBrowser()
	redeemed := f.doWith(other, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if redeemed.status != http.StatusOK {
		t.Fatalf("兑换失败：%d %s", redeemed.status, redeemed.raw)
	}
	if redeemed.userField("id") != original {
		t.Fatalf("应当认回同一个 user_id：%s vs %s", redeemed.userField("id"), original)
	}
	if _, ok := redeemed.body["device_token"]; ok {
		t.Fatal("响应体里不该有明文 device_token，凭证只走 Set-Cookie")
	}
	if f.deviceCookie(other) == nil {
		t.Fatal("新浏览器应当拿到设备 cookie")
	}

	// 同一个 token 不能用第二次（§5.2）。
	third := f.newBrowser()
	again := f.doWith(third, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if again.status != http.StatusBadRequest || again.errCode() != "claim_invalid" {
		t.Fatalf("token 应当只能用一次，实际 %d %s", again.status, again.raw)
	}
}

// 打开带 token 的页面（GET）不消耗它——链接预览抓取器只发 GET（§10.3）。
func TestGetWithClaimTokenDoesNotConsumeIt(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")

	page := f.do(http.MethodGet, "/?claim="+url.QueryEscape(token), nil)
	if got := page.header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("带 claim 参数的页面应当返回 no-referrer，实际 %q", got)
	}
	if page.status == http.StatusServiceUnavailable {
		// 前端还没构建（web/dist 只有 .gitkeep）。头部断言照样有效，
		// 页面本体由 `make test`（先构建前端）覆盖。
		t.Log("前端未构建，跳过页面本体断言")
	}

	// token 还在，仍然可以兑换。
	other := f.newBrowser()
	redeemed := f.doWith(other, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if redeemed.status != http.StatusOK {
		t.Fatalf("GET 之后 token 应当仍然有效，实际 %d %s", redeemed.status, redeemed.raw)
	}
}

func TestRedeemTokenExpiresAfterTenMinutes(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")

	f.clk.Advance(identity.ClaimTTL + time.Minute)

	other := f.newBrowser()
	resp := f.doWith(other, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if resp.status != http.StatusBadRequest || resp.errCode() != "claim_invalid" {
		t.Fatalf("过期 token 应当被拒绝，实际 %d %s", resp.status, resp.raw)
	}
}

// ---------------------------------------------------------------------------
// 认领码
// ---------------------------------------------------------------------------

func TestRedeemByCodeWorksForFreshDevice(t *testing.T) {
	f := newFixture(t, false)
	original := f.createIdentity(f.http, "老王")

	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	fresh := f.newBrowser()
	resp := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": code})
	if resp.status != http.StatusOK {
		t.Fatalf("认领码兑换失败：%d %s", resp.status, resp.raw)
	}
	if resp.userField("id") != original {
		t.Fatalf("应当认回同一个 user_id，实际 %s", resp.userField("id"))
	}
}

func TestRedeemByCodeRejectsUnknownCode(t *testing.T) {
	f := newFixture(t, false)

	resp := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"code": "0000"})
	if resp.status != http.StatusBadRequest || resp.errCode() != "claim_invalid" {
		t.Fatalf("未知认领码应当返回统一的错误，实际 %d %s", resp.status, resp.raw)
	}

	// 格式不对的也一样。
	for _, bad := range []string{"123", "12a4", "12345"} {
		r := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"code": bad})
		if r.status != http.StatusBadRequest || r.errCode() != "claim_invalid" {
			t.Fatalf("格式不对的认领码 %q 应当返回同一个错误，实际 %d", bad, r.status)
		}
	}
}

// 猜错的码匹配不到任何行，数不出「每个码试了几次」（#95）；真正拦住猜码的是每 IP 限速：
// 同一个 IP 连续猜错，用完额度后直接 429。
func TestRedeemByCodeWrongGuessesHitPerIPLimit(t *testing.T) {
	f := newFixture(t, false)
	fresh := f.newBrowser()

	for i := 0; i < 10; i++ {
		r := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": "000000"})
		if r.status != http.StatusBadRequest || r.errCode() != "claim_invalid" {
			t.Fatalf("第 %d 次猜错应当是 claim_invalid，实际 %d %s", i+1, r.status, r.raw)
		}
	}
	r := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": "000000"})
	if r.status != http.StatusTooManyRequests || r.errCode() != "rate_limited" {
		t.Fatalf("第 11 次应当被每 IP 限速拦下，实际 %d %s", r.status, r.raw)
	}
}

// 已经有身份的设备不能用认领码，而且返回的错误和「码不对」完全一样（§10.3）。
func TestRedeemByCodeRejectedWhenAlreadyIdentified(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	resp := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"code": code})
	if resp.status != http.StatusBadRequest {
		t.Fatalf("状态码 %d，期望 400", resp.status)
	}

	// 和「码不对」的错误一模一样，不透露码是否存在。
	unknown := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"code": "9999"})
	if unknown.raw != resp.raw {
		t.Fatalf("两种失败应当返回同样的错误：%s vs %s", resp.raw, unknown.raw)
	}
}

// 回归测试（原审计 PoC `TestAuditPoC_CodeCollisionReturnsOtherUser` 的另一半）：
// 已用或已过期的同号记录，不能挡住仍然有效的那个码。
func TestUsedOrExpiredRowDoesNotShadowLiveCode(t *testing.T) {
	f := newFixture(t, false)
	owner := f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	// 塞一条同号的旧记录：已经用过、也过期了。
	err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO claim_tokens (token_hash, code, user_id, expires_at, used_at, issued_net)
			 VALUES (?, ?, ?, 0, 0, NULL)`,
			[]byte("stale-hash"), code, owner)
		return err
	})
	if err != nil {
		t.Fatalf("插入旧记录: %v", err)
	}

	fresh := f.newBrowser()
	r := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": code})
	if r.status != http.StatusOK || r.userField("id") != owner {
		t.Fatalf("有效码不该被已用/过期的同号记录挡住，实际 %d %s", r.status, r.raw)
	}
}

// 有效码之间不会重复：连签若干次，所有「未使用且未过期」的码必须互不相同（§5.2 修订第 2 条）。
func TestLiveClaimCodesNeverCollide(t *testing.T) {
	f := newFixture(t, false)

	seen := map[string]bool{}
	// 每个用户同时只有一个有效码，所以要从多个用户那里收集。
	for i := 0; i < 6; i++ {
		browser := f.newBrowser()
		f.createIdentity(browser, "牌友"+string(rune('A'+i)))
		code := f.doWith(browser, http.MethodPost, "/api/claim", nil).str("code")
		if len(code) != identity.ClaimCodeDigits {
			t.Fatalf("认领码应当是 %d 位，实际 %q", identity.ClaimCodeDigits, code)
		}
		if seen[code] {
			t.Fatalf("出现了重复的有效码 %q", code)
		}
		seen[code] = true
	}

	// 库里的有效码也确实两两不同。
	rows, err := f.st.Read().Query(
		`SELECT code FROM claim_tokens WHERE used_at IS NULL AND expires_at > ?`, f.clk.Now().Unix())
	if err != nil {
		t.Fatalf("查有效码: %v", err)
	}
	defer rows.Close()

	live := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("扫描: %v", err)
		}
		if live[c] {
			t.Fatalf("库里有重复的有效码 %q", c)
		}
		live[c] = true
	}
	if len(live) != 6 {
		t.Fatalf("应当有 6 个有效码，实际 %d", len(live))
	}
}

// 同一用户只保留一个有效码：签发新码会把旧码作废（§5.2 修订第 3 条）。
func TestIssuingClaimInvalidatesPreviousCode(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	first := f.do(http.MethodPost, "/api/claim", nil).str("code")
	second := f.do(http.MethodPost, "/api/claim", nil).str("code")
	if first == second {
		t.Fatal("两次签发不该拿到同一个码")
	}

	fresh := f.newBrowser()
	if r := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": first}); r.status != http.StatusBadRequest {
		t.Fatalf("旧码应当已经作废，实际 %d %s", r.status, r.raw)
	}
	if r := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", map[string]string{"code": second}); r.status != http.StatusOK {
		t.Fatalf("新码应当可用，实际 %d %s", r.status, r.raw)
	}
}

// 认领码只能在签发时的同一个网络里兑换（§5.2 修订第 4 条）。
func TestClaimCodeRejectedFromDifferentNetwork(t *testing.T) {
	f := newFixture(t, false)
	owner := f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	// 签发时记下的是测试客户端的网络（127.0.0.1 → 127.0.0.0/24）。
	var issuedNet string
	if err := f.st.Read().QueryRow(
		`SELECT COALESCE(issued_net, '') FROM claim_tokens WHERE code = ?`, code).Scan(&issuedNet); err != nil {
		t.Fatalf("查 issued_net: %v", err)
	}
	if issuedNet != "127.0.0.0/24" {
		t.Fatalf("签发时应当记下网络前缀，实际 %q", issuedNet)
	}

	// 换个网络兑换：失败，且和别的失败长得一模一样。
	if _, _, err := f.svc.RedeemByCode(context.Background(), code, "10.9.9.0/24", "other-ua"); !errors.Is(err, identity.ErrClaimInvalid) {
		t.Fatalf("不同网络应当返回统一的 claim_invalid，实际 %v", err)
	}

	// 同一个网络兑换：成功。
	if _, _, err := f.svc.RedeemByCode(context.Background(), code, "127.0.0.0/24", "same-ua"); err != nil {
		t.Fatalf("同一网络应当可以兑换: %v", err)
	}
	_ = owner
}

// 全站失败预算：失败太多时暂停凭码兑换，但凭 token 兑换不受影响（§5.2 修订第 5 条）。
func TestGlobalFailureBudgetSuspendsCodeRedeem(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	// 连续失败，把预算打爆。
	for i := 0; i < 21; i++ {
		if _, _, err := f.svc.RedeemByCode(context.Background(), "000000", "127.0.0.0/24", "ua"); !errors.Is(err, identity.ErrClaimInvalid) {
			t.Fatalf("第 %d 次失败应当是 claim_invalid，实际 %v", i, err)
		}
	}

	// 现在连正确的码也兑换不了。
	if _, _, err := f.svc.RedeemByCode(context.Background(), code, "127.0.0.0/24", "ua"); !errors.Is(err, identity.ErrClaimInvalid) {
		t.Fatalf("暂停期间应当一律返回 claim_invalid，实际 %v", err)
	}

	// 凭 token 兑换不受影响。用另一个人签发的 token：同一个用户再签一次
	// 会把上面那个码作废，测不到我们想测的东西。
	other := f.newBrowser()
	f.createIdentity(other, "小李")
	token := f.doWith(other, http.MethodPost, "/api/claim", nil).str("claim_token")
	if r := f.doWith(f.newBrowser(), http.MethodPost, "/api/claim/redeem", map[string]string{"token": token}); r.status != http.StatusOK {
		t.Fatalf("暂停只针对凭码兑换，token 应当照常可用，实际 %d %s", r.status, r.raw)
	}

	// 过了冷却期，凭码兑换恢复。
	f.clk.Advance(6 * time.Minute)
	if _, _, err := f.svc.RedeemByCode(context.Background(), code, "127.0.0.0/24", "ua"); err != nil {
		t.Fatalf("冷却期过后应当恢复: %v", err)
	}
}

// 签发交接令牌每用户每小时最多 5 次（§5.2 修订第 3 条）。
func TestIssueClaimIsRateLimitedPerUser(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	for i := 0; i < 5; i++ {
		if r := f.do(http.MethodPost, "/api/claim", nil); r.status != http.StatusOK {
			t.Fatalf("第 %d 次签发应当成功，实际 %d %s", i+1, r.status, r.raw)
		}
	}
	r := f.do(http.MethodPost, "/api/claim", nil)
	if r.status != http.StatusTooManyRequests {
		t.Fatalf("第 6 次签发应当被限流，实际 %d %s", r.status, r.raw)
	}
}

// ---------------------------------------------------------------------------
// 认领链接撞上已有身份（审计 PoC 的回归测试）
// ---------------------------------------------------------------------------

// 回归测试（原审计 PoC `TestAuditPoC_ClaimLinkMergesVictim`）：
// 攻击者把自己的 claim 链接发到群里，已有身份的受害者打开。
// 在用户明确确认之前，受害者的身份、攻击者的身份、这台设备、以及 token 本身都不能变。
func TestClaimLinkDoesNotSwitchIdentityWithoutConfirm(t *testing.T) {
	f := newFixture(t, false)

	victim := f.createIdentity(f.http, "受害者")
	f.clk.Advance(time.Minute)
	attacker := f.newBrowser()
	attackerID := f.createIdentity(attacker, "攻击者")
	token := f.doWith(attacker, http.MethodPost, "/api/claim", nil).str("claim_token")

	// 受害者打开 ?claim=<攻击者的 token>；前端会自动发一次不带 confirm 的请求。
	r := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if r.status != http.StatusConflict {
		t.Fatalf("应当返回 409 让用户确认，实际 %d %s", r.status, r.raw)
	}
	if r.str("other_nickname") != "攻击者" {
		t.Fatalf("409 应当带上对方的称呼，实际 %s", r.raw)
	}

	// 受害者的身份没变。
	if me := f.do(http.MethodGet, "/api/me", nil); me.userField("id") != victim {
		t.Fatalf("确认之前受害者的身份不该变，实际 %s", me.userField("id"))
	}
	// 攻击者也没拿到受害者的身份。
	if me := f.doWith(attacker, http.MethodGet, "/api/me", nil); me.userField("id") != attackerID {
		t.Fatalf("攻击者的身份不该变，实际 %s", me.userField("id"))
	}
	// 受害者的身份没有被标记成已合并——压根没有发生合并。
	var mergedInto sql.NullString
	if err := f.st.Read().QueryRow(
		`SELECT merged_into FROM users WHERE id = ?`, victim).Scan(&mergedInto); err != nil {
		t.Fatalf("查受害者: %v", err)
	}
	if mergedInto.Valid {
		t.Fatalf("不该发生任何合并，受害者的 merged_into = %q", mergedInto.String)
	}

	// token 没有被消耗：用户确认之后还能用。
	confirm := f.do(http.MethodPost, "/api/claim/redeem", map[string]any{"token": token, "confirm": true})
	if confirm.status != http.StatusOK {
		t.Fatalf("带 confirm 的兑换应当成功，实际 %d %s", confirm.status, confirm.raw)
	}
	if confirm.userField("id") != attackerID {
		t.Fatalf("确认后应当切换成 %s，实际 %s", attackerID, confirm.userField("id"))
	}

	// 这台设备现在属于新身份了。
	if me := f.do(http.MethodGet, "/api/me", nil); me.userField("id") != attackerID {
		t.Fatalf("确认后设备应当切过去，实际 %s", me.userField("id"))
	}
	// 原来的身份还在，没有被删也没有被合并（§5.2 修订：只切换，不迁移）。
	var nickname string
	if err := f.st.Read().QueryRow(
		`SELECT nickname FROM users WHERE id = ?`, victim).Scan(&nickname); err != nil {
		t.Fatalf("原身份应当仍然存在: %v", err)
	}
}

// 没有身份的设备凭 token 兑换，行为不变：直接成为那个身份（正常的跳出流程）。
func TestRedeemTokenWithoutCurrentIdentitySwitchesSilently(t *testing.T) {
	f := newFixture(t, false)
	owner := f.createIdentity(f.http, "老王")
	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")

	fresh := f.newBrowser()
	r := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if r.status != http.StatusOK {
		t.Fatalf("没有身份时应当直接兑换成功，实际 %d %s", r.status, r.raw)
	}
	if r.userField("id") != owner {
		t.Fatalf("应当认回 %s，实际 %s", owner, r.userField("id"))
	}
}

// 同一台设备拿自己身份的 token 兑换：不算冲突，照常换发一台新设备。
func TestRedeemOwnTokenIsNotAConflict(t *testing.T) {
	f := newFixture(t, false)
	me := f.createIdentity(f.http, "老王")
	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")

	r := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if r.status != http.StatusOK {
		t.Fatalf("自己的 token 不该算冲突，实际 %d %s", r.status, r.raw)
	}
	if r.userField("id") != me {
		t.Fatalf("身份不该变，实际 %s", r.userField("id"))
	}
}

// ---------------------------------------------------------------------------
// 设备管理
// ---------------------------------------------------------------------------

func TestSignOutOtherDevice(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	// 第二台设备用认领码认领过去（用同一个 fixture 的第二个浏览器不太方便，
	// 所以直接调服务签发一个码）。
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")
	other := f.newBrowser()
	if resp := f.doWith(other, http.MethodPost, "/api/claim/redeem", map[string]string{"code": code}); resp.status != http.StatusOK {
		t.Fatalf("第二台设备认领失败：%d %s", resp.status, resp.raw)
	}

	me := f.do(http.MethodGet, "/api/me", nil)
	devices, _ := me.body["devices"].([]any)
	if len(devices) != 2 {
		t.Fatalf("设备数 %d，期望 2", len(devices))
	}

	// 认清哪台是当前设备，要退出的是另一台。
	ident, err := f.svc.Current(context.Background(), f.deviceCookie(f.http).Value)
	if err != nil {
		t.Fatalf("解析当前身份: %v", err)
	}
	otherDeviceID := ""
	for _, d := range devices {
		if id := d.(map[string]any)["id"].(string); id != ident.Device.ID {
			otherDeviceID = id
		}
	}
	if otherDeviceID == "" {
		t.Fatal("没找到另一台设备")
	}

	resp := f.do(http.MethodDelete, "/api/me/devices/"+otherDeviceID, nil)
	if resp.status != http.StatusNoContent {
		t.Fatalf("退出设备状态码 %d：%s", resp.status, resp.raw)
	}

	// 另一台设备从此认不出来。
	if got := f.doWith(other, http.MethodGet, "/api/me", nil); got.status != http.StatusUnauthorized {
		t.Fatalf("被退出的设备应当认不出身份，实际 %d", got.status)
	}
	// 自己这台还在。
	if got := f.do(http.MethodGet, "/api/me", nil); got.status != http.StatusOK {
		t.Fatalf("当前设备不该被退出，实际 %d", got.status)
	}
}

func TestSignOutUnknownDevice(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	resp := f.do(http.MethodDelete, "/api/me/devices/nonexistent", nil)
	if resp.status != http.StatusNotFound {
		t.Fatalf("状态码 %d，期望 404", resp.status)
	}
}

// ---------------------------------------------------------------------------
// 授权矩阵（§10.12）
// ---------------------------------------------------------------------------

// 每个路由都用两种身份各打一次。没在路由表里声明授权的路由，这里根本不会出现，
// 所以新增接口时不可能漏掉声明。
func TestAuthorizationMatrix(t *testing.T) {
	for _, rt := range identity.Routes(nil) { // 只列路由，不处理请求
		rt := rt
		for _, role := range []string{"guest", "user"} {
			role := role
			t.Run(rt.Method+" "+rt.Pattern+" as "+role, func(t *testing.T) {
				f := newFixture(t, false)

				client := f.http
				if role == "user" {
					f.createIdentity(client, "老王")
				}

				status := f.probe(rt)
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

// probe 用一个能跑通的请求去打这条路由，只看授权结果，不看业务结果。
func (f *fixture) probe(rt route.Route) int {
	f.t.Helper()

	path := strings.ReplaceAll(rt.Pattern, "{id}", "nonexistent")

	// 每条路由给一个格式合法但一定不成立的请求体：矩阵只看授权，不看业务结果。
	var body any
	switch {
	case rt.Pattern == "/api/claim/redeem":
		body = map[string]string{"token": "not-a-real-token"}
	case rt.Method == http.MethodPost:
		body = map[string]string{"nickname": "老王"}
	}

	return f.do(rt.Method, path, body).status
}

// 同一个身份在自己的设备上兑换自己的 token：沿用原来的设备记录，
// 不新挂一条闲置设备（#36），原来的 cookie 也继续有效。
func TestRedeemOwnTokenReusesDevice(t *testing.T) {
	f := newFixture(t, false)
	me := f.createIdentity(f.http, "老王")

	var before int
	if err := f.st.Read().QueryRow(`SELECT count(*) FROM devices`).Scan(&before); err != nil {
		t.Fatalf("数设备: %v", err)
	}

	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")
	r := f.do(http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if r.status != http.StatusOK {
		t.Fatalf("兑换自己的 token 应当成功，实际 %d %s", r.status, r.raw)
	}
	if r.userField("id") != me {
		t.Fatalf("身份不该变，实际 %s", r.userField("id"))
	}

	var after int
	if err := f.st.Read().QueryRow(`SELECT count(*) FROM devices`).Scan(&after); err != nil {
		t.Fatalf("数设备: %v", err)
	}
	if after != before {
		t.Fatalf("自己的 token 不该新挂设备记录：%d -> %d", before, after)
	}

	// 原来那个 cookie 还能用。
	if got := f.do(http.MethodGet, "/api/me", nil); got.status != http.StatusOK {
		t.Fatalf("兑换后原来的 cookie 应当仍然有效，实际 %d", got.status)
	}
}

// GET /api/me 带上当前设备 id——「不是我」要用它退出（#36）。
func TestMeReturnsCurrentDeviceID(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")

	me := f.do(http.MethodGet, "/api/me", nil)
	deviceID := me.str("device_id")
	if deviceID == "" {
		t.Fatalf("应当返回当前设备 id，实际 %s", me.raw)
	}

	// 用它退出之后，这个浏览器就认不出身份了。
	if r := f.do(http.MethodDelete, "/api/me/devices/"+deviceID, nil); r.status != http.StatusNoContent {
		t.Fatalf("退出当前设备应当 204，实际 %d %s", r.status, r.raw)
	}
	if got := f.do(http.MethodGet, "/api/me", nil); got.status != http.StatusUnauthorized {
		t.Fatalf("退出之后应当认不出身份，实际 %d", got.status)
	}
}

// ---------------------------------------------------------------------------
// 封禁（#20：guandan admin ban 之后的请求一律 403）
// ---------------------------------------------------------------------------

func TestBannedUserGets403(t *testing.T) {
	f := newFixture(t, false)
	userID := f.createIdentity(f.http, "老王")
	other := f.newBrowser()
	f.createIdentity(other, "Lily")

	if _, err := f.st.Ban(context.Background(), userID, f.clk.Now()); err != nil {
		t.Fatalf("封禁: %v", err)
	}
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, "/api/me"},
		{http.MethodPost, "/api/claim"},
		{http.MethodPost, "/api/me"}, // 不能靠「再建一个身份」绕过：同一台设备还是被认出来
	} {
		var body any
		if req.method == http.MethodPost && req.path == "/api/me" {
			body = map[string]string{"nickname": "换个名字"}
		}
		resp := f.do(req.method, req.path, body)
		if resp.status != http.StatusForbidden || resp.errCode() != "banned" {
			t.Errorf("%s %s：%d %s，期望 403 banned", req.method, req.path, resp.status, resp.errCode())
		}
	}
	if resp := f.doWith(other, http.MethodGet, "/api/me", nil); resp.status != http.StatusOK {
		t.Fatalf("没被封禁的人不受影响，实际 %d", resp.status)
	}
}

func TestBannedDeviceGets403(t *testing.T) {
	f := newFixture(t, false)
	userID := f.createIdentity(f.http, "阿杰")
	var deviceID string
	if err := f.st.Read().QueryRow(`SELECT id FROM devices WHERE user_id = ?`, userID).Scan(&deviceID); err != nil {
		t.Fatal(err)
	}
	if kind, err := f.st.Ban(context.Background(), deviceID, f.clk.Now()); err != nil || kind != "device" {
		t.Fatalf("封禁设备: %s %v", kind, err)
	}
	if resp := f.do(http.MethodGet, "/api/me", nil); resp.status != http.StatusForbidden || resp.errCode() != "banned" {
		t.Fatalf("被封禁的设备应 403，实际 %d %s", resp.status, resp.errCode())
	}
}
