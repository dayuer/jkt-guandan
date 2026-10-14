package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/config"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
)

const (
	wxApp      = "wxtestapp"
	tmplFull   = "TMPL_FULL_x1"
	tmplRemind = "TMPL_REMIND_y2"
	openidOfU2 = "oSECRET-openid-u2"
)

// fakeWx 按模板返回事先设好的 errcode，并记录每次调用。
type fakeWx struct {
	mu    sync.Mutex
	code  map[string]int
	err   error
	calls []WxMessage
}

func (f *fakeWx) SendSubscribe(_ context.Context, m WxMessage) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, m)
	if f.err != nil {
		return 0, f.err
	}
	return f.code[m.TemplateID], nil
}

func (f *fakeWx) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// withWx 打开订阅消息通道，并让 u2 成为小游戏用户（有 openid 关联）。
func (e *env) withWx(f *fakeWx) {
	e.t.Helper()
	e.svc.SetWx(&WxChannel{
		AppID: wxApp,
		Templates: map[string]config.WxTemplate{
			config.WxTmplFull:   {ID: tmplFull, Keys: [3]string{"thing4", "date5", "thing6"}},
			config.WxTmplRemind: {ID: tmplRemind, Keys: [3]string{"thing1", "time2", "thing3"}},
		},
		Sender: f,
	})
	e.exec(`INSERT INTO user_links (provider, subject, user_id, created_at) VALUES (?, ?, 'u2', 0)`,
		"wx_openid:"+wxApp, openidOfU2)
}

func (e *env) quota(userID, tmpl string) string {
	e.t.Helper()
	return e.scalar(`SELECT coalesce((SELECT remaining FROM wx_subscribe_quota WHERE user_id = ? AND template_id = ?), -1)`, userID, tmpl)
}

// 次数：同意一次 + 1，同一次上报里重复的只算一次，不认识的模板忽略，最多攒 MaxWxQuota 次。
func TestWxQuotaAdd(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if _, err := e.svc.AddWxQuota(ctx, "u2", []string{tmplFull}); !errors.Is(err, ErrWxDisabled) {
		t.Fatalf("通道没开时应 ErrWxDisabled，实际 %v", err)
	}
	e.withWx(&fakeWx{})
	n, err := e.svc.AddWxQuota(ctx, "u2", []string{tmplRemind, tmplRemind, "someone-elses", tmplFull})
	if err != nil || n != 2 {
		t.Fatalf("记下 %d %v", n, err)
	}
	if _, err := e.svc.AddWxQuota(ctx, "u2", []string{tmplRemind}); err != nil {
		t.Fatal(err)
	}
	if e.quota("u2", tmplRemind) != "2" || e.quota("u2", tmplFull) != "1" || e.quota("u2", "someone-elses") != "-1" {
		t.Fatalf("次数 remind=%s full=%s", e.quota("u2", tmplRemind), e.quota("u2", tmplFull))
	}
	for i := 0; i < MaxWxQuota+5; i++ {
		_, _ = e.svc.AddWxQuota(ctx, "u2", []string{tmplFull})
	}
	if got := e.quota("u2", tmplFull); got != fmt.Sprint(MaxWxQuota) {
		t.Fatalf("次数应封顶 %d，实际 %s", MaxWxQuota, got)
	}
}

// 没有 Web Push 订阅的小游戏用户：走订阅消息，发一次减一次；内容按模板字段名填。
func TestWxSendConsumesQuota(t *testing.T) {
	e := newEnv(t)
	f := &fakeWx{}
	e.withWx(f)
	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplRemind})
	id := e.reminder("u2", "t_minus_1h")

	res, err := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background())
	if err != nil || res != (Result{Sent: 1, WxSent: 1}) {
		t.Fatalf("结果 %+v %v", res, err)
	}
	if e.quota("u2", tmplRemind) != "0" {
		t.Fatalf("发一次应减一次，剩 %s", e.quota("u2", tmplRemind))
	}
	if got := e.scalar(`SELECT sent_at || '/' || coalesce(last_error, '')  FROM outbox WHERE id = ?`, id); got != fmt.Sprint(t0.Unix())+"/" {
		t.Fatalf("outbox %s", got)
	}
	m := f.calls[0]
	if m.OpenID != openidOfU2 || m.TemplateID != tmplRemind || m.Page != "?g=SEED01" {
		t.Fatalf("消息 %+v", m)
	}
	if m.Data["thing1"] != "还有 1 小时开局" || m.Data["time2"] != "2026年10月14日 19:00" || m.Data["thing3"] != "Pacific Place" {
		t.Fatalf("字段 %v", m.Data)
	}

	// 「成局」走另一个模板，字段名按那个模板的配置。
	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplFull})
	e.reminder("u2", "full")
	if res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background()); res.WxSent != 1 {
		t.Fatalf("成局 %+v", res)
	}
	if m := f.calls[1]; m.TemplateID != tmplFull || m.Data["thing4"] != "成局了！4 人已齐" || m.Data["date5"] == "" {
		t.Fatalf("成局消息 %+v", m)
	}
}

// 次数为 0 不发；同一批里同一个模板有两条、只剩 1 次时只发一条。
func TestWxZeroQuotaDoesNotSend(t *testing.T) {
	e := newEnv(t)
	f := &fakeWx{}
	e.withWx(f)
	id := e.reminder("u2", "t_minus_1h")
	res, err := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background())
	if err != nil || res != (Result{Dropped: 1}) || f.n() != 0 {
		t.Fatalf("没有次数不该发：%+v %v calls=%d", res, err, f.n())
	}
	if got := e.scalar(`SELECT sent_at IS NULL AND dropped_at IS NOT NULL FROM outbox WHERE id = ?`, id); got != "1" {
		t.Fatal("没次数的提醒应写 dropped_at，且不能留着 sent_at")
	}

	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplRemind})
	e.reminder("u2", "rescheduled")
	e.reminder("u2", "seat_left") // full 模板，没次数
	e.exec(`INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key) VALUES ('g1', 'u2', 't_minus_1h', ?, ?, 'g1|u2|t_minus_1h|2')`,
		e.clk.Now().Unix(), e.clk.Now().Unix())
	res, _ = NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background())
	if res != (Result{Sent: 1, WxSent: 1, Dropped: 2}) || f.n() != 1 {
		t.Fatalf("只剩 1 次时只该发 1 条：%+v calls=%d", res, f.n())
	}
	if e.quota("u2", tmplRemind) != "0" {
		t.Fatalf("剩 %s", e.quota("u2", tmplRemind))
	}
}

// 去重：同一个用户同时有 Web Push 订阅和订阅消息次数时，一条提醒只走 Web Push，不动订阅消息的次数。
// 至多一次：订阅消息发出后不会因为下一轮再发。
func TestWxDedupeWithWebPush(t *testing.T) {
	e := newEnv(t)
	f := &fakeWx{}
	e.withWx(f)
	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplRemind})
	_ = e.svc.Subscribe(context.Background(), "u2", "d2", sub(fcm))
	e.reminder("u2", "t_minus_1h")
	push := &fakeSender{}
	res, _ := NewWorker(e.svc, push, e.clk).RunOnce(context.Background())
	if res != (Result{Sent: 1}) || len(push.calls) != 1 || f.n() != 0 {
		t.Fatalf("应只走 Web Push：%+v push=%d wx=%d", res, len(push.calls), f.n())
	}
	if e.quota("u2", tmplRemind) != "1" {
		t.Fatal("走了 Web Push 就不该用掉订阅消息次数")
	}

	// 退订 Web Push 之后，下一条走订阅消息；发过的不会再发。
	_ = e.svc.Unsubscribe(context.Background(), "u2", fcm)
	e.reminder("u2", "rescheduled")
	for i := 0; i < 3; i++ {
		_, _ = NewWorker(e.svc, push, e.clk).RunOnce(context.Background())
	}
	if f.n() != 1 || len(push.calls) != 1 {
		t.Fatalf("每条提醒只该送达一次：push=%d wx=%d", len(push.calls), f.n())
	}
}

// 网络错误、系统繁忙：退回次数、清掉预占时写的 sent_at，按退避重试；
// 43101（用户没订阅）：次数清零，放弃；其他错误：退回次数，放弃。
func TestWxFailureRefundsQuota(t *testing.T) {
	e := newEnv(t)
	f := &fakeWx{err: errors.New("connection reset")}
	e.withWx(f)
	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplRemind})
	id := e.reminder("u2", "t_minus_1h")
	res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background())
	if res != (Result{Retried: 1}) || e.quota("u2", tmplRemind) != "1" {
		t.Fatalf("网络错误：%+v 次数 %s", res, e.quota("u2", tmplRemind))
	}
	if got := e.scalar(`SELECT (sent_at IS NULL) || '/' || attempts || '/' || (next_try - ?) FROM outbox WHERE id = ?`, t0.Unix(), id); got != "1/1/30" {
		t.Fatalf("重试状态 %s", got)
	}

	f.err, f.code = nil, map[string]int{tmplRemind: wxErrBusy}
	e.clk.Advance(31 * time.Second)
	if res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background()); res != (Result{Retried: 1}) {
		t.Fatalf("系统繁忙应重试：%+v", res)
	}

	f.code = map[string]int{tmplRemind: wxErrNotSubscribed}
	e.clk.Advance(3 * time.Minute)
	if res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background()); res != (Result{Dropped: 1}) {
		t.Fatalf("43101 应放弃：%+v", res)
	}
	if e.quota("u2", tmplRemind) != "0" {
		t.Fatalf("43101 后次数应清零，实际 %s", e.quota("u2", tmplRemind))
	}

	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplFull})
	f.code = map[string]int{tmplFull: 47003} // 模板参数不对
	e.reminder("u2", "full")
	if res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background()); res != (Result{Dropped: 1}) {
		t.Fatalf("其他错误应放弃：%+v", res)
	}
	if e.quota("u2", tmplFull) != "1" {
		t.Fatal("没发出去的应退回次数")
	}
}

// 通道关闭（没配 AppID / Secret 或模板）、提醒类型没有模板、用户没有 openid 关联：都不走订阅消息。
func TestWxChannelOff(t *testing.T) {
	if NewWxChannel(config.Config{WxTmplRemind: config.WxTemplate{ID: tmplRemind}}, nil) != nil {
		t.Fatal("没配 AppID / Secret 时通道应关闭")
	}
	if NewWxChannel(config.Config{WxAppID: wxApp, WxSecret: "s"}, nil) != nil {
		t.Fatal("一个模板都没配时通道应关闭")
	}
	if ch := NewWxChannel(config.Config{Dev: true, WxTmplRemind: config.WxTemplate{ID: tmplRemind}}, nil); ch == nil ||
		ch.AppID != identity.DevWxAppID || ch.Sender != (DevWxSender{}) {
		t.Fatalf("开发模式没配 AppID：应开启、openid 按 wx_openid:dev 找、只记日志：%+v", ch)
	}
	ch := NewWxChannel(config.Config{WxAppID: wxApp, WxSecret: "s", WxTmplRemind: config.WxTemplate{ID: tmplRemind}}, nil)
	if ch == nil || len(ch.TemplateIDs()) != 1 {
		t.Fatalf("配了 remind 模板应开启，只有一个模板：%v", ch.TemplateIDs())
	}
	if _, ok := ch.template("full"); ok {
		t.Fatal("没配 full 模板时成局提醒不走订阅消息")
	}
	if _, ok := ch.template("host_seat_taken"); ok {
		t.Fatal("不在对应表里的提醒不走订阅消息")
	}

	e := newEnv(t)
	f := &fakeWx{}
	e.withWx(f)
	_, _ = e.svc.AddWxQuota(context.Background(), "u1", []string{tmplRemind}) // u1 没有 openid 关联
	e.reminder("u1", "t_minus_1h")
	e.reminder("u2", "host_seat_taken")
	res, _ := NewWorker(e.svc, &fakeSender{}, e.clk).RunOnce(context.Background())
	if res != (Result{Dropped: 2}) || f.n() != 0 {
		t.Fatalf("%+v wx=%d", res, f.n())
	}
	if e.quota("u1", tmplRemind) != "1" {
		t.Fatal("没 openid 时不该用掉次数")
	}
}

// 注销（DELETE /api/me）删掉订阅消息次数。
func TestWxQuotaDeletedWithAccount(t *testing.T) {
	e := newEnv(t)
	e.withWx(&fakeWx{})
	_, _ = e.svc.AddWxQuota(context.Background(), "u2", []string{tmplRemind, tmplFull})
	idsvc := identity.NewService(e.st, e.clk, false)
	ident := &identity.Identity{}
	ident.User.ID = "u2"
	if err := idsvc.DeleteMe(context.Background(), ident); err != nil {
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM wx_subscribe_quota WHERE user_id = 'u2'`); n != "0" {
		t.Fatalf("注销后还剩 %s 条订阅记录", n)
	}
	// guandan admin user delete 走的是 store.DeleteUserData。
	e.exec(`INSERT INTO wx_subscribe_quota (user_id, template_id, remaining, updated_at) VALUES ('u1', ?, 3, 0)`, tmplFull)
	if err := e.st.DeleteUserData(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if n := e.scalar(`SELECT count(*) FROM wx_subscribe_quota WHERE user_id = 'u1'`); n != "0" {
		t.Fatalf("admin 删除后还剩 %s 条", n)
	}
}

// access_token：缓存到过期前 5 分钟；快过期时换新的；微信说无效（40001）时作废，下次带 force_refresh 重新拿。
// 日志里不出现 openid 和 access_token。
func TestStableTokenAndSubscribeClient(t *testing.T) {
	var tokenCalls atomic.Int32
	var forced atomic.Int32
	var mu sync.Mutex
	var sent []map[string]any
	var sendCode atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["appid"] != wxApp || body["secret"] != "sec" || body["grant_type"] != "client_credential" {
				t.Errorf("stable_token 请求 %v", body)
			}
			if body["force_refresh"] == true {
				forced.Add(1)
			}
			n := tokenCalls.Add(1)
			fmt.Fprintf(w, `{"access_token":"tok%d","expires_in":7200}`, n)
		case "/send":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			body["_token"] = r.URL.Query().Get("access_token")
			mu.Lock()
			sent = append(sent, body)
			mu.Unlock()
			fmt.Fprintf(w, `{"errcode":%d,"errmsg":"x"}`, sendCode.Load())
		}
	}))
	defer srv.Close()

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prev)

	e := newEnv(t)
	tokens := &StableToken{AppID: wxApp, Secret: "sec", Endpoint: srv.URL + "/token", Clock: e.clk}
	c := &SubscribeClient{Tokens: tokens, Endpoint: srv.URL + "/send"}
	msg := WxMessage{OpenID: openidOfU2, TemplateID: tmplRemind, Page: "?g=SEED01", Data: map[string]string{"thing1": "还有 1 小时开局"}}
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if code, err := c.SendSubscribe(ctx, msg); code != 0 || err != nil {
			t.Fatalf("发送 %d %v", code, err)
		}
	}
	if tokenCalls.Load() != 1 {
		t.Fatalf("token 应缓存，实际取了 %d 次", tokenCalls.Load())
	}
	data, _ := sent[0]["data"].(map[string]any)
	if sent[0]["touser"] != openidOfU2 || sent[0]["template_id"] != tmplRemind || sent[0]["page"] != "?g=SEED01" ||
		sent[0]["_token"] != "tok1" || data["thing1"].(map[string]any)["value"] != "还有 1 小时开局" {
		t.Fatalf("请求 %v", sent[0])
	}

	// 过期前 5 分钟以内：换新的。
	e.clk.Advance(2*time.Hour - 4*time.Minute)
	_, _ = c.SendSubscribe(ctx, msg)
	if tokenCalls.Load() != 2 || sent[len(sent)-1]["_token"] != "tok2" {
		t.Fatalf("快过期时应刷新：取了 %d 次，用的 %v", tokenCalls.Load(), sent[len(sent)-1]["_token"])
	}

	// 40001：作废缓存，下次 force_refresh。
	sendCode.Store(wxErrTokenInvalid)
	if code, _ := c.SendSubscribe(ctx, msg); code != wxErrTokenInvalid || !wxRetryable(code) {
		t.Fatalf("40001 应原样返回且可重试，实际 %d", code)
	}
	sendCode.Store(0)
	_, _ = c.SendSubscribe(ctx, msg)
	if tokenCalls.Load() != 3 || forced.Load() != 1 || sent[len(sent)-1]["_token"] != "tok3" {
		t.Fatalf("40001 后应强制刷新：取了 %d 次，强制 %d 次", tokenCalls.Load(), forced.Load())
	}

	// 走一遍 worker 的失败日志：只有 errcode，没有 openid / token。
	logWxError("t_minus_1h", 47003, nil)
	logWxError("t_minus_1h", 0, errors.New("调 subscribeMessage.send: *url.Error"))
	if s := logs.String(); strings.Contains(s, openidOfU2) || strings.Contains(s, "tok") {
		t.Fatalf("日志里出现了 openid 或 access_token：%s", s)
	}
}

// 上报接口：POST /api/wx/subscribe。
func TestWxSubscribeHandler(t *testing.T) {
	e := newEnv(t)
	h := &handler{svc: e.svc}
	call := func(body string) (int, string) {
		req := httptest.NewRequest(http.MethodPost, "/api/wx/subscribe", strings.NewReader(body))
		ident := &identity.Identity{}
		ident.User.ID = "u2"
		req = req.WithContext(identity.NewContext(req.Context(), ident, false))
		w := httptest.NewRecorder()
		h.wxSubscribe(w, req)
		return w.Code, strings.TrimSpace(w.Body.String())
	}
	if code, body := call(`{"accepted":["` + tmplRemind + `"]}`); code != 503 || !strings.Contains(body, "wx_disabled") {
		t.Fatalf("通道关闭：%d %s", code, body)
	}
	e.withWx(&fakeWx{})
	if code, body := call(`{"accepted":["` + tmplRemind + `","x"]}`); code != 200 || body != `{"added":1}` {
		t.Fatalf("上报：%d %s", code, body)
	}
	if code, _ := call(`not json`); code != 400 {
		t.Fatalf("坏请求应 400，实际 %d", code)
	}
	if code, _ := call(`{"accepted":["a","b","c","d","e","f","g","h","i","j","k"]}`); code != 400 {
		t.Fatalf("太多模板应 400，实际 %d", code)
	}
	if e.quota("u2", tmplRemind) != "1" {
		t.Fatalf("次数 %s", e.quota("u2", tmplRemind))
	}
}

func TestClipRunes(t *testing.T) {
	if got := clip("Pacific Place 太平洋广场五楼大厅旁边的包间", 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "…") {
		t.Fatalf("clip = %q", got)
	}
	if clip("短", 20) != "短" {
		t.Fatal("短的不截")
	}
}
