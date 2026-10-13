package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/dayuer/jkt-guandan/internal/store"
)

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

	// 必须用 TLS：生产环境的 cookie 名带 __Host- 前缀、带 Secure，
	// 而 cookie jar 不会把 Secure cookie 发回普通 http 的地址，
	// 用 NewServer 的话测试里根本认不出身份。
	srv := httptest.NewTLSServer(httpapi.New(httpapi.Options{
		Config: config.Config{DataDir: dir, Dev: dev, PublicURL: testOrigin},
		Store:  st,
		Clock:  clk,
		RegisterRoutes: func(mux *http.ServeMux) {
			identity.Register(mux, svc)
		},
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

	token := resp.str("device_token")
	if token == "" {
		t.Fatal("新建身份时应当回传一次明文 device_token，供前端写 localStorage")
	}

	cookie := f.deviceCookie(f.http)
	if cookie == nil {
		t.Fatal("应当下发设备 cookie")
	}
	if cookie.Value != token {
		t.Fatalf("cookie 值和回传的 token 不一致：%q vs %q", cookie.Value, token)
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
	if resp := f.do(http.MethodPost, "/api/me", map[string]string{"nickname": "老王"}); resp.str("device_token") != "" {
		t.Fatal("没有新建设备时不该再回传明文 token")
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
	if token == "" || len(claim.str("code")) != 4 {
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
	if redeemed.str("device_token") == "" {
		t.Fatal("兑换成功应当给新设备下发 token")
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

// 每个码最多试 5 次，超过就作废（§5.2）。
func TestRedeemByCodeBurnsOutAfterMaxAttempts(t *testing.T) {
	f := newFixture(t, false)
	f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	burnTo := func(n int) {
		t.Helper()
		err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE claim_tokens SET attempts = ? WHERE code = ?`, n, code)
			return err
		})
		if err != nil {
			t.Fatalf("改 attempts: %v", err)
		}
	}

	burnTo(identity.ClaimCodeMaxAttempts)

	fresh := f.newBrowser()
	resp := f.doWith(fresh, http.MethodPost, "/api/claim/redeem", map[string]string{"code": code})
	if resp.status != http.StatusBadRequest || resp.errCode() != "claim_invalid" {
		t.Fatalf("超过尝试上限的码应当作废，实际 %d %s", resp.status, resp.raw)
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

// ---------------------------------------------------------------------------
// 身份合并
// ---------------------------------------------------------------------------

func TestRedeemMergesAccidentallyCreatedIdentity(t *testing.T) {
	f := newFixture(t, false)

	// 老王先在微信里有了身份。
	primary := f.createIdentity(f.http, "老王")

	// 他在 Safari 里清过存储，于是又填了一次称呼，错建了第二个身份。
	// 时间要往前走，否则两个身份的 created_at 一样，「先注册的」就没法确定了。
	f.clk.Advance(time.Minute)
	safari := f.newBrowser()
	secondary := f.createIdentity(safari, "老王")
	if secondary == primary {
		t.Fatal("两个浏览器应当是不同身份")
	}

	// 他回到微信拿 claim token，然后跳到 Safari 兑换。
	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")
	redeemed := f.doWith(safari, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token})
	if redeemed.status != http.StatusOK {
		t.Fatalf("兑换失败：%d %s", redeemed.status, redeemed.raw)
	}

	// 认回的是先注册的那个。
	if got := redeemed.userField("id"); got != primary {
		t.Fatalf("应当保留先注册的身份 %s，实际 %s", primary, got)
	}

	// 错建的身份被标记成已合并，没有删除（§5.2、§14）。
	var mergedInto string
	err := f.st.Read().QueryRow(
		`SELECT COALESCE(merged_into, '') FROM users WHERE id = ?`, secondary).Scan(&mergedInto)
	if err != nil {
		t.Fatalf("查旧身份: %v", err)
	}
	if mergedInto != primary {
		t.Fatalf("旧身份应当指向 %s，实际 %q", primary, mergedInto)
	}

	// Safari 现在也是老王的身份了。
	me := f.doWith(safari, http.MethodGet, "/api/me", nil)
	if me.userField("id") != primary {
		t.Fatalf("合并后应当认回主身份，实际 %s", me.userField("id"))
	}
}

func TestMergeMovesSeatsAndKeepsEarlierSeat(t *testing.T) {
	f := newFixture(t, false)

	primary := f.createIdentity(f.http, "老王")
	f.clk.Advance(time.Minute) // 让「先注册的」有确定答案
	safari := f.newBrowser()
	secondary := f.createIdentity(safari, "老王")

	// 两个身份各自在一个局里占了座位，其中 secondary 的那个更早。
	seedGame := func(id, hostID string) {
		t.Helper()
		err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
				 VALUES (?, ?, ?, 0, 'SCBD', 'Pacific Place', 'classic', 'open', 0)`, id, "c"+id, hostID)
			return err
		})
		if err != nil {
			t.Fatalf("建牌局 %s: %v", id, err)
		}
	}
	seedGame("g1", primary) // 两个人在同一局里都占了座，primary 的座位更早
	seedGame("g2", primary) // 只有 secondary 占座

	seat := func(gameID, userID string, no int, at int64) {
		t.Helper()
		err := f.st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx,
				`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, ?)`,
				gameID, no, userID, at)
			return err
		})
		if err != nil {
			t.Fatalf("占座 %s/%s: %v", gameID, userID, err)
		}
	}
	seat("g1", primary, 1, 100)   // 更早
	seat("g1", secondary, 2, 200) // 更晚：合并后应当被丢掉
	seat("g2", secondary, 1, 100) // 只有它，应当搬过去

	token := f.do(http.MethodPost, "/api/claim", nil).str("claim_token")
	if resp := f.doWith(safari, http.MethodPost, "/api/claim/redeem", map[string]string{"token": token}); resp.status != http.StatusOK {
		t.Fatalf("兑换失败：%d %s", resp.status, resp.raw)
	}

	// g1：只留 primary 那个更早的座位。
	var g1Count, g1Seat int
	if err := f.st.Read().QueryRow(
		`SELECT count(*), COALESCE(min(seat_no), 0) FROM seats WHERE game_id = 'g1'`).Scan(&g1Count, &g1Seat); err != nil {
		t.Fatalf("查 g1: %v", err)
	}
	if g1Count != 1 || g1Seat != 1 {
		t.Fatalf("g1 应当只剩更早的那个座位，实际 %d 个、座位号 %d", g1Count, g1Seat)
	}

	// g2：座位搬到了主身份名下。
	var g2Owner string
	if err := f.st.Read().QueryRow(`SELECT user_id FROM seats WHERE game_id = 'g2'`).Scan(&g2Owner); err != nil {
		t.Fatalf("查 g2: %v", err)
	}
	if g2Owner != primary {
		t.Fatalf("g2 的座位应当搬到 %s，实际 %s", primary, g2Owner)
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
	for _, rt := range identity.Routes {
		rt := rt
		for _, role := range []identity.Role{identity.RoleGuest, identity.RoleUser} {
			role := role
			t.Run(rt.Method+" "+rt.Pattern+" as "+string(role), func(t *testing.T) {
				f := newFixture(t, false)

				client := f.http
				if role == identity.RoleUser {
					f.createIdentity(client, "老王")
				}

				status := f.probe(rt, role)
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

// probe 用一个能跑通的请求去打这条路由，只看授权结果，不看业务结果。
func (f *fixture) probe(rt identity.Route, _ identity.Role) int {
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
