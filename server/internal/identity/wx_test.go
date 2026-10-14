package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
)

// 小游戏身份（#84）。

const testAppID = "wxtestapp"

// fakeWx 是假的 code2Session：code → 会话。没登记的 code 当作无效。
type fakeWx struct {
	mu       sync.Mutex
	sessions map[string]identity.WxSession
}

func (w *fakeWx) Exchange(_ context.Context, code string) (identity.WxSession, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s, ok := w.sessions[code]
	if !ok {
		return identity.WxSession{}, identity.ErrWxCodeInvalid
	}
	delete(w.sessions, code) // code 只能用一次，和微信一样
	return s, nil
}

func (w *fakeWx) add(code, openid, unionid string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessions[code] = identity.WxSession{OpenID: openid, UnionID: unionid}
}

func newWxFixture(t *testing.T) (*fixture, *fakeWx) {
	t.Helper()
	f := newFixture(t, false)
	wx := &fakeWx{sessions: map[string]identity.WxSession{}}
	f.svc.SetWx(testAppID, wx)
	return f, wx
}

// mini 模拟小游戏的 wx.request：没有 cookie，不发 Origin；headers 里可以带 Authorization。
func (f *fixture) mini(method, path string, body any, headers map[string]string) response {
	f.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.srv.URL+path, reader)
	if err != nil {
		f.t.Fatal(err)
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := (&http.Client{Transport: f.srv.Client().Transport}).Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, header: resp.Header, raw: string(raw)}
	_ = json.Unmarshal(raw, &out.body)
	return out
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// wxLogin 登录并要求 200 拿到 token。
func (f *fixture) wxLogin(code, nickname string) response {
	f.t.Helper()
	body := map[string]string{"code": code}
	if nickname != "" {
		body["nickname"] = nickname
	}
	r := f.mini(http.MethodPost, "/api/wx/login", body, nil)
	if r.status != http.StatusOK || r.str("device_token") == "" || r.str("device_id") == "" || r.userField("id") == "" {
		f.t.Fatalf("小游戏登录应成功：%d %s", r.status, r.raw)
	}
	if sc := r.header.Values("Set-Cookie"); len(sc) != 0 {
		f.t.Fatalf("小游戏登录不能写 cookie：%v", sc)
	}
	return r
}

func (f *fixture) links(userID string) map[string]string {
	f.t.Helper()
	rows, err := f.st.Read().Query(`SELECT provider, subject FROM user_links WHERE user_id = ?`, userID)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var p, s string
		if err := rows.Scan(&p, &s); err != nil {
			f.t.Fatal(err)
		}
		out[p] = s
	}
	return out
}

func (f *fixture) countRows(q string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.st.Read().QueryRow(q, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// 没有 unionid：第一次要称呼，填了之后建用户、设备、openid 关联；再登录认回同一个人、新挂一台设备。
func TestWxLoginWithoutUnionID(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c1"}, nil)
	if r.status != http.StatusOK || r.body["need_nickname"] != true {
		t.Fatalf("没关联过应要称呼：%d %s", r.status, r.raw)
	}
	if n := f.countRows(`SELECT count(*) FROM users`); n != 0 {
		t.Fatalf("要称呼时不能建用户，实际 %d", n)
	}

	wx.add("c2", "open-1", "")
	first := f.wxLogin("c2", "  阿强 ")
	uid := first.userField("id")
	if first.userField("nickname") != "阿强" {
		t.Fatalf("称呼应按 POST /api/me 的规则整理：%s", first.raw)
	}
	if got := f.links(uid); len(got) != 1 || got["wx_openid:"+testAppID] != "open-1" {
		t.Fatalf("应只有 openid 关联：%v", got)
	}
	if strings.Contains(first.raw, "open-1") {
		t.Fatalf("响应里不能出现 openid：%s", first.raw)
	}

	wx.add("c3", "open-1", "")
	again := f.wxLogin("c3", "")
	if again.userField("id") != uid || again.str("device_token") == first.str("device_token") {
		t.Fatalf("应认回同一个人、换一台新设备：%s", again.raw)
	}
	if n := f.countRows(`SELECT count(*) FROM devices WHERE user_id = ? AND ua = ?`, uid, identity.WxMinigameUA); n != 2 {
		t.Fatalf("应有两台 wx-minigame 设备，实际 %d", n)
	}
}

// 有 unionid：两条关联都建；换一个 openid（以后的第二个微信应用）、同一个 unionid 也认回同一个人。
func TestWxLoginWithUnionID(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "union-1")
	uid := f.wxLogin("c1", "阿强").userField("id")
	got := f.links(uid)
	if got["wx_unionid"] != "union-1" || got["wx_openid:"+testAppID] != "open-1" {
		t.Fatalf("应有 unionid 和 openid 两条关联：%v", got)
	}

	wx.add("c2", "open-other", "union-1")
	if again := f.wxLogin("c2", ""); again.userField("id") != uid {
		t.Fatalf("同一个 unionid 应认回同一个人：%s", again.raw)
	}
}

// 先只有 openid，后来才有 unionid：补写 unionid 关联。
func TestWxLoginBackfillsUnionID(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	uid := f.wxLogin("c1", "阿强").userField("id")

	wx.add("c2", "open-1", "union-1")
	if again := f.wxLogin("c2", ""); again.userField("id") != uid {
		t.Fatalf("应按 openid 认回同一个人：%s", again.raw)
	}
	if got := f.links(uid); got["wx_unionid"] != "union-1" {
		t.Fatalf("应补写 unionid 关联：%v", got)
	}
}

func TestWxLoginErrors(t *testing.T) {
	f, wx := newWxFixture(t)
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "nope"}, nil); r.status != http.StatusBadRequest || r.errCode() != "wx_code_invalid" {
		t.Fatalf("无效 code：%d %s", r.status, r.raw)
	}
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{}, nil); r.status != http.StatusBadRequest || r.errCode() != "bad_request" {
		t.Fatalf("缺 code：%d %s", r.status, r.raw)
	}
	wx.add("c1", "open-1", "")
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c1", "nickname": "   "}, nil); r.status != http.StatusBadRequest || r.errCode() != "nickname_invalid" {
		t.Fatalf("坏称呼：%d %s", r.status, r.raw)
	}
}

// 生产没配 AppID / Secret：两个接口都 503 wx_disabled。
func TestWxDisabledWithoutCredentials(t *testing.T) {
	f := newFixture(t, false)
	for _, path := range []string{"/api/wx/login", "/api/wx/link"} {
		r := f.mini(http.MethodPost, path, map[string]string{"code": "dev:alice", "wx_code": "dev:alice"}, nil)
		if r.status != http.StatusServiceUnavailable || r.errCode() != "wx_disabled" {
			t.Fatalf("%s 应 503 wx_disabled：%d %s", path, r.status, r.raw)
		}
	}
}

// 开发模式：dev:<openid> 直接当 openid，appid 记成 dev。别的格式无效。
func TestWxDevCodes(t *testing.T) {
	f := newFixture(t, true)
	f.svc.SetWx(identity.DevWxAppID, identity.DevWxExchanger{})

	r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "dev:alice", "nickname": "Alice"}, nil)
	if r.status != http.StatusOK || r.str("device_token") == "" {
		t.Fatalf("dev: code 应能登录：%d %s", r.status, r.raw)
	}
	uid := r.userField("id")
	if got := f.links(uid); got["wx_openid:dev"] != "alice" {
		t.Fatalf("应关联 wx_openid:dev/alice：%v", got)
	}
	if again := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "dev:alice"}, nil); again.userField("id") != uid {
		t.Fatalf("同一个 dev: code 应认回同一个人：%s", again.raw)
	}
	if bad := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "alice"}, nil); bad.status != http.StatusBadRequest || bad.errCode() != "wx_code_invalid" {
		t.Fatalf("没有 dev: 前缀应无效：%d %s", bad.status, bad.raw)
	}
}

// Bearer 能在 /api/ 上认身份（包括设备列表里的小游戏设备），会改数据的请求只带 Bearer 时不查 Origin。
func TestBearerAcceptedOnAPI(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	login := f.wxLogin("c1", "阿强")
	token := login.str("device_token")

	me := f.mini(http.MethodGet, "/api/me", nil, bearer(token))
	if me.status != http.StatusOK || me.userField("id") != login.userField("id") || me.str("device_id") != login.str("device_id") {
		t.Fatalf("Bearer 应认出身份：%d %s", me.status, me.raw)
	}
	if !strings.Contains(me.raw, identity.WxMinigameUA) {
		t.Fatalf("设备列表里应有小游戏设备：%s", me.raw)
	}
	// 没有 Origin、只带 Bearer 的写请求不是 CSRF。
	if r := f.mini(http.MethodPost, "/api/claim", nil, bearer(token)); r.status != http.StatusOK || r.str("code") == "" {
		t.Fatalf("Bearer 写请求应放行：%d %s", r.status, r.raw)
	}
	// Content-Type 仍然必须是 JSON。
	h := bearer(token)
	h["Content-Type"] = "text/plain"
	if r := f.mini(http.MethodPost, "/api/claim", nil, h); r.status != http.StatusForbidden || r.errCode() != "csrf" {
		t.Fatalf("Bearer 也必须是 JSON：%d %s", r.status, r.raw)
	}
	// 无效的 Bearer：认不出身份。
	if r := f.mini(http.MethodGet, "/api/me", nil, bearer("not-a-token")); r.status != http.StatusUnauthorized {
		t.Fatalf("无效 Bearer 应 401：%d %s", r.status, r.raw)
	}
}

// Bearer 只在 /api/ 和 /ws 上认；cookie 和 Bearer 都有时按 cookie 认。
func TestBearerOnlyOnAPIAndWS(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	token := f.wxLogin("c1", "阿强").str("device_token")

	for path, want := range map[string]bool{
		"/api/me": true, "/api/games": true, "/ws": true,
		"/g/ABC123": false, "/": false, "/apix": false, "/wsx": false, "/index.html": false,
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		if got := f.svc.FromRequest(req) != nil; got != want {
			t.Errorf("%s 上 Bearer 认身份 = %v，期望 %v", path, got, want)
		}
	}
	// 查询串里的 token 不认。
	if f.svc.FromRequest(httptest.NewRequest(http.MethodGet, "/api/me?access_token="+token, nil)) != nil {
		t.Error("查询串里的 token 不能认")
	}

	// cookie 优先：H5 的 cookie 和小游戏的 Bearer 都带时，按 cookie 认。
	h5 := f.createIdentity(f.http, "老王")
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(f.deviceCookie(f.http))
	req.Header.Set("Authorization", "Bearer "+token)
	if ident := f.svc.FromRequest(req); ident == nil || ident.User.ID != h5 {
		t.Fatalf("cookie 应优先：%+v", ident)
	}
	if f.svc.BearerOnly(req) {
		t.Fatal("带 cookie 的请求不算只带 Bearer")
	}
}

// CSRF：只有「Bearer、没有 cookie」才免 Origin；带 cookie 的、什么都不带的照常要 Origin。
// /api/wx/login 没有 Origin 放行，有外站 Origin 拒绝。
func TestCSRFBearerAndWxExceptions(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	token := f.wxLogin("c1", "阿强").str("device_token")

	// 带 cookie 又带 Bearer、没有 Origin：照常拦。
	f.createIdentity(f.http, "老王")
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/claim", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := f.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("带 cookie 的请求没有 Origin 应被拦，实际 %d", resp.StatusCode)
	}

	// 什么凭证都没有、没有 Origin 的 POST /api/me：照常拦。
	if r := f.mini(http.MethodPost, "/api/me", map[string]string{"nickname": "x"}, nil); r.status != http.StatusForbidden || r.errCode() != "csrf" {
		t.Fatalf("没凭证没 Origin 的 POST /api/me 应被拦：%d %s", r.status, r.raw)
	}

	// /api/wx/login：外站 Origin 拒绝，本站 Origin、没有 Origin 都放行。
	wx.add("c2", "open-1", "")
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c2"}, map[string]string{"Origin": "https://evil.example"}); r.status != http.StatusForbidden || r.errCode() != "csrf" {
		t.Fatalf("外站 Origin 应被拦：%d %s", r.status, r.raw)
	}
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c2"}, map[string]string{"Origin": testOrigin}); r.status != http.StatusOK {
		t.Fatalf("本站 Origin 应放行：%d %s", r.status, r.raw)
	}
}

// /ws：没有 Origin、没有 cookie 的握手放行；带有效 Bearer 认出身份，否则是访客。带 cookie 却没有 Origin 的拒绝。
func TestWSBearerWithoutOrigin(t *testing.T) {
	f := newHubFixture(t)
	wx := &fakeWx{sessions: map[string]identity.WxSession{}}
	f.svc.SetWx(testAppID, wx)
	wx.add("c1", "open-1", "")
	login := f.wxLogin("c1", "阿强")

	dial := func(h http.Header) (*websocket.Conn, *http.Response, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return websocket.Dial(ctx, "wss"+strings.TrimPrefix(f.srv.URL, "https")+"/ws", &websocket.DialOptions{
			HTTPClient: &http.Client{Transport: f.srv.Client().Transport},
			HTTPHeader: h,
		})
	}
	c, _, err := dial(http.Header{"Authorization": {"Bearer " + login.str("device_token")}})
	if err != nil {
		t.Fatalf("Bearer 没有 Origin 应能连上：%v", err)
	}
	defer c.CloseNow()
	if m := wsRead(t, c, time.Second); m["type"] != "hello" || m["user_id"] != login.userField("id") {
		t.Fatalf("hello 应带上 Bearer 认出的用户：%v", m)
	}

	for name, h := range map[string]http.Header{
		"无效 Bearer": {"Authorization": {"Bearer not-a-token"}},
		"什么都没有":     {},
	} {
		c, _, err := dial(h)
		if err != nil {
			t.Errorf("%s、没有 Origin 应能以访客连上：%v", name, err)
			continue
		}
		if m := wsRead(t, c, time.Second); m["type"] != "hello" || m["user_id"] != nil && m["user_id"] != "" {
			t.Errorf("%s：应是访客的 hello：%v", name, m)
		}
		c.CloseNow()
	}

	cookie := http.Header{"Cookie": {identity.DevCookieName + "=whatever; " + identity.ProdCookieName + "=whatever"}}
	if _, resp, err := dial(cookie); err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Errorf("带 cookie、没有 Origin 应 403：%v", err)
	}
}

// 注销（用 Bearer）删掉关联，同一个微信再来要重新填称呼、建新用户。
func TestDeleteMeRemovesWxLinks(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "union-1")
	login := f.wxLogin("c1", "阿强")
	uid := login.userField("id")

	if r := f.mini(http.MethodDelete, "/api/me", nil, bearer(login.str("device_token"))); r.status != http.StatusNoContent {
		t.Fatalf("Bearer 注销：%d %s", r.status, r.raw)
	}
	if n := f.countRows(`SELECT count(*) FROM user_links WHERE user_id = ?`, uid); n != 0 {
		t.Fatalf("注销后关联应全部删除，还剩 %d", n)
	}
	wx.add("c2", "open-1", "union-1")
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c2"}, nil); r.body["need_nickname"] != true {
		t.Fatalf("注销后同一个微信应重新要称呼：%s", r.raw)
	}
	wx.add("c3", "open-1", "union-1")
	if again := f.wxLogin("c3", "新人"); again.userField("id") == uid {
		t.Fatal("注销后应建新用户")
	}
}

// 被封禁的用户：小游戏登录 403，Bearer 请求也 403。
func TestWxBannedUser(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	login := f.wxLogin("c1", "阿强")
	if _, err := f.st.Ban(context.Background(), login.userField("id"), f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	wx.add("c2", "open-1", "")
	if r := f.mini(http.MethodPost, "/api/wx/login", map[string]string{"code": "c2"}, nil); r.status != http.StatusForbidden || r.errCode() != "banned" {
		t.Fatalf("被封禁的用户登录应 403：%d %s", r.status, r.raw)
	}
	if r := f.mini(http.MethodGet, "/api/me", nil, bearer(login.str("device_token"))); r.status != http.StatusForbidden || r.errCode() != "banned" {
		t.Fatalf("被封禁的 Bearer 应 403：%d %s", r.status, r.raw)
	}
}

// B2：H5 签发的认领码在小游戏里兑换，微信身份关联到 H5 的那个人。
func TestWxLinkByClaimCode(t *testing.T) {
	f, wx := newWxFixture(t)
	h5 := f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	wx.add("w1", "open-1", "union-1")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if r := f.mini(http.MethodPost, "/api/wx/link", map[string]string{"code": wrong, "wx_code": "w1"}, nil); r.status != http.StatusBadRequest || r.errCode() != "claim_invalid" {
		t.Fatalf("错的认领码应 claim_invalid：%d %s", r.status, r.raw)
	}

	wx.add("w2", "open-1", "union-1")
	r := f.mini(http.MethodPost, "/api/wx/link", map[string]string{"code": code, "wx_code": "w2"}, nil)
	if r.status != http.StatusOK || r.userField("id") != h5 || r.str("device_token") == "" {
		t.Fatalf("认领码关联应成功：%d %s", r.status, r.raw)
	}
	if len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatal("不能写 cookie")
	}
	if got := f.links(h5); got["wx_unionid"] != "union-1" || got["wx_openid:"+testAppID] != "open-1" {
		t.Fatalf("应关联到 H5 用户：%v", got)
	}
	if me := f.mini(http.MethodGet, "/api/me", nil, bearer(r.str("device_token"))); me.userField("id") != h5 {
		t.Fatalf("新 token 应认出 H5 用户：%s", me.raw)
	}
	// 码只能用一次。
	wx.add("w3", "open-1", "union-1")
	if again := f.mini(http.MethodPost, "/api/wx/link", map[string]string{"code": code, "wx_code": "w3"}, nil); again.errCode() != "claim_invalid" {
		t.Fatalf("用过的码应失效：%s", again.raw)
	}
	// 之后小游戏直接登录就是 H5 那个人。
	wx.add("w4", "open-1", "union-1")
	if login := f.wxLogin("w4", ""); login.userField("id") != h5 {
		t.Fatalf("关联之后登录应认回 H5 用户：%s", login.raw)
	}
}

// 微信身份已经关联了别人：没有 confirm 就 409（码不消耗），确认后把关联切过去，原用户的数据不动。
func TestWxLinkConflictNeedsConfirm(t *testing.T) {
	f, wx := newWxFixture(t)
	wx.add("c1", "open-1", "")
	other := f.wxLogin("c1", "小游戏号").userField("id")

	h5 := f.createIdentity(f.http, "老王")
	code := f.do(http.MethodPost, "/api/claim", nil).str("code")

	wx.add("w1", "open-1", "")
	r := f.mini(http.MethodPost, "/api/wx/link", map[string]string{"code": code, "wx_code": "w1"}, nil)
	if r.status != http.StatusConflict || r.errCode() != "claim_conflict" || r.str("other_nickname") != "老王" {
		t.Fatalf("应 409 并告诉认领码属于谁：%d %s", r.status, r.raw)
	}
	if got := f.links(other); got["wx_openid:"+testAppID] != "open-1" {
		t.Fatalf("没确认时关联不能动：%v", got)
	}

	wx.add("w2", "open-1", "")
	r = f.mini(http.MethodPost, "/api/wx/link", map[string]any{"code": code, "wx_code": "w2", "confirm": true}, nil)
	if r.status != http.StatusOK || r.userField("id") != h5 {
		t.Fatalf("确认后应切过去：%d %s", r.status, r.raw)
	}
	if got := f.links(h5); got["wx_openid:"+testAppID] != "open-1" {
		t.Fatalf("关联应在 H5 用户上：%v", got)
	}
	if got := f.links(other); len(got) != 0 {
		t.Fatalf("原用户的关联应摘掉：%v", got)
	}
	if n := f.countRows(`SELECT count(*) FROM users WHERE id = ? AND deleted_at IS NULL`, other); n != 1 {
		t.Fatal("原用户应原样保留")
	}
}

// 真实的 code2Session 客户端：参数、成功、无效 code、其他错误。
func TestCode2SessionClient(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		switch r.URL.Query().Get("js_code") {
		case "ok":
			_, _ = io.WriteString(w, `{"openid":"o1","session_key":"sk","unionid":"u1"}`)
		case "used":
			_, _ = io.WriteString(w, `{"errcode":40163,"errmsg":"code been used"}`)
		default:
			_, _ = io.WriteString(w, `{"errcode":-1,"errmsg":"system busy"}`)
		}
	}))
	defer srv.Close()
	c := identity.Code2Session{AppID: "wxa", Secret: "sec", Endpoint: srv.URL}

	s, err := c.Exchange(context.Background(), "ok")
	if err != nil || s.OpenID != "o1" || s.UnionID != "u1" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, want := range []string{"appid=wxa", "secret=sec", "js_code=ok", "grant_type=authorization_code"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("查询串缺 %s：%s", want, gotQuery)
		}
	}
	if _, err := c.Exchange(context.Background(), "used"); !errors.Is(err, identity.ErrWxCodeInvalid) {
		t.Fatalf("40163 应是 code 无效：%v", err)
	}
	if _, err := c.Exchange(context.Background(), "busy"); !errors.Is(err, identity.ErrWxUnavailable) {
		t.Fatalf("-1 应是暂时不可用：%v", err)
	}
}
