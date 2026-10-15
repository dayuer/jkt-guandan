package secheck_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/metrics"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
	"github.com/dayuer/jkt-guandan/server/internal/wxapi"
)

type fixedToken struct{}

func (fixedToken) Token(context.Context) (string, error) { return "tok", nil }
func (fixedToken) Invalidate(string, string, int)        {}

// wxServer 是假的 msg_sec_check：记下收到的请求体，按 reply 回应。
func wxServer(t *testing.T, reply func(body map[string]any) string) (*secheck.WxChecker, *[]map[string]any) {
	t.Helper()
	var (
		mu   sync.Mutex
		reqs []map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/wxa/game/content_spam/msg_sec_check" || r.URL.Query().Get("access_token") != "tok" {
			t.Errorf("请求地址不对：%s", r.URL)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		reqs = append(reqs, body)
		mu.Unlock()
		io.WriteString(w, reply(body))
	}))
	t.Cleanup(srv.Close)
	return &secheck.WxChecker{API: &wxapi.Client{Tokens: fixedToken{}, BaseURL: srv.URL}}, &reqs
}

func TestWxCheckerPassAndRisky(t *testing.T) {
	c, reqs := wxServer(t, func(body map[string]any) string {
		if strings.Contains(body["content"].(string), "坏") {
			return `{"errcode":0,"result":{"suggest":"risky","label":20003}}`
		}
		return `{"errcode":0,"result":{"suggest":"pass","label":100}}`
	})

	v, err := c.Check(context.Background(), "open-1", secheck.SceneForum, "好地方")
	if err != nil || v.Risky || v.Label != 100 {
		t.Fatalf("应通过：%+v %v", v, err)
	}
	v, err = c.Check(context.Background(), "open-1", secheck.SceneProfile, "坏话")
	if err != nil || !v.Risky || v.Label != 20003 {
		t.Fatalf("应判 risky：%+v %v", v, err)
	}

	got := (*reqs)[1]
	if got["openid"] != "open-1" || got["version"] != float64(2) || got["scene"] != float64(1) || got["content"] != "坏话" {
		t.Fatalf("请求体不对：%v", got)
	}
	if _, ok := got["nickname"]; ok {
		t.Fatalf("不该带 nickname：%v", got)
	}
}

func TestWxCheckerErrors(t *testing.T) {
	c, _ := wxServer(t, func(map[string]any) string { return `{"errcode":-1,"errmsg":"system error"}` })
	if _, err := c.Check(context.Background(), "o", 1, "x"); err == nil {
		t.Fatal("errcode 非 0 应报错")
	}
	c, _ = wxServer(t, func(map[string]any) string { return `{"errcode":0,"result":{"suggest":"???"}}` })
	if _, err := c.Check(context.Background(), "o", 1, "x"); !errors.Is(err, wxapi.ErrUnavailable) {
		t.Fatalf("不认识的 suggest 应当是 ErrUnavailable：%v", err)
	}
}

// fakeChecker 按内容给结论；block 为真时一直等到 ctx 结束（模拟超时）。
type fakeChecker struct {
	mu      sync.Mutex
	calls   []call
	block   bool
	err     error
	riskyOn string
}

type call struct {
	openid, content string
	scene           int
}

func (f *fakeChecker) Check(ctx context.Context, openid string, scene int, content string) (secheck.Verdict, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call{openid, content, scene})
	f.mu.Unlock()
	if f.block {
		<-ctx.Done()
		return secheck.Verdict{}, ctx.Err()
	}
	if f.err != nil {
		return secheck.Verdict{}, f.err
	}
	if f.riskyOn != "" && strings.Contains(content, f.riskyOn) {
		return secheck.Verdict{Risky: true, Suggest: "risky", Label: 20002}, nil
	}
	return secheck.Verdict{Suggest: "pass", Label: 100}, nil
}

func newStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}

func link(t *testing.T, st *store.Store, userID, provider, subject string) {
	t.Helper()
	err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, nickname, created_at) VALUES (?, 'x', 0)`, userID); err != nil {
			return err
		}
		if provider == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO user_links (provider, subject, user_id, created_at) VALUES (?, ?, ?, 0)`, provider, subject, userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// captureLogs 把默认 logger 换成写到缓冲区的（Debug 级别也记），测试结束换回去。
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func metricsText(reg *metrics.Registry) string {
	var b strings.Builder
	reg.WriteTo(context.Background(), &b)
	return b.String()
}

func TestGuardRiskyReturnsField(t *testing.T) {
	st := newStore(t)
	link(t, st, "u1", "wx_openid:app", "open-1")
	fc := &fakeChecker{riskyOn: "坏"}
	reg := &metrics.Registry{}
	g := &secheck.Guard{Checker: fc, AppID: "app", Store: st, Metrics: secheck.NewMetrics(reg)}

	err := g.CheckUser(context.Background(), "u1", secheck.SceneForum,
		secheck.Field{Name: "venue", Text: "好地方"}, secheck.Field{Name: "note", Text: "坏话"})
	var unsafe *secheck.UnsafeError
	if !errors.As(err, &unsafe) || unsafe.Field != "note" || unsafe.Label != 20002 || !errors.Is(err, secheck.ErrUnsafe) {
		t.Fatalf("备注应被拦：%v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("两个字段各查一次，实际 %d", len(fc.calls))
	}
	for _, c := range fc.calls {
		if c.openid != "open-1" || c.scene != secheck.SceneForum {
			t.Fatalf("openid / scene 不对：%+v", c)
		}
	}
	if err := g.CheckUser(context.Background(), "u1", secheck.SceneForum, secheck.Field{Name: "venue", Text: "好地方"}); err != nil {
		t.Fatalf("应通过：%v", err)
	}
	m := metricsText(reg)
	if !strings.Contains(m, `guandan_wx_sec_check_total{outcome="risky"} 1`) || !strings.Contains(m, `guandan_wx_sec_check_total{outcome="pass"} 2`) {
		t.Fatalf("指标不对：\n%s", m)
	}
}

// 空字段不送检。
func TestGuardSkipsEmptyFields(t *testing.T) {
	st := newStore(t)
	link(t, st, "u1", "wx_openid:app", "open-1")
	fc := &fakeChecker{}
	g := &secheck.Guard{Checker: fc, AppID: "app", Store: st}
	if err := g.CheckUser(context.Background(), "u1", 3, secheck.Field{Name: "venue", Text: "x"}, secheck.Field{Name: "note", Text: "  "}); err != nil {
		t.Fatal(err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("空备注不该查：%d 次", len(fc.calls))
	}
}

// 没有小游戏 openid（H5 测试用户，或者 openid 是别的 AppID 的）：跳过，不调微信。
func TestGuardSkipsUserWithoutOpenID(t *testing.T) {
	st := newStore(t)
	link(t, st, "h5", "", "")
	link(t, st, "other", "wx_openid:another-app", "open-x")
	fc := &fakeChecker{riskyOn: "坏"}
	reg := &metrics.Registry{}
	g := &secheck.Guard{Checker: fc, AppID: "app", Store: st, Metrics: secheck.NewMetrics(reg)}
	for _, uid := range []string{"h5", "other", "nobody"} {
		if err := g.CheckUser(context.Background(), uid, 3, secheck.Field{Name: "venue", Text: "坏"}); err != nil {
			t.Fatalf("%s 应跳过：%v", uid, err)
		}
	}
	if len(fc.calls) != 0 {
		t.Fatalf("不该调微信：%v", fc.calls)
	}
	if m := metricsText(reg); !strings.Contains(m, `outcome="no_openid"} 3`) {
		t.Fatalf("指标不对：\n%s", m)
	}
}

// 没配密钥时 Guard 是 nil：整体关闭。
func TestNilGuardIsOff(t *testing.T) {
	var g *secheck.Guard
	if err := g.CheckUser(context.Background(), "u1", 1, secheck.Field{Name: "nickname", Text: secheck.DevRiskyWord}); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckOpenID(context.Background(), "o", 1, secheck.Field{Name: "nickname", Text: secheck.DevRiskyWord}); err != nil {
		t.Fatal(err)
	}
}

// 超时、出错都放行；日志和指标里有结论，没有原文和 openid。
func TestGuardFailsOpenWithoutLeakingContent(t *testing.T) {
	const secret, openid = "原文不能进日志", "openid-should-not-be-logged"
	cases := []struct {
		name, outcome string
		fc            *fakeChecker
	}{
		{"timeout", "timeout", &fakeChecker{block: true}},
		{"errcode", "error", &fakeChecker{err: &wxapi.Error{Code: -1, Msg: "echo " + secret}}},
		{"unavailable", "error", &fakeChecker{err: wxapi.ErrUnavailable}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := captureLogs(t)
			reg := &metrics.Registry{}
			g := &secheck.Guard{Checker: tc.fc, Timeout: 30 * time.Millisecond, Metrics: secheck.NewMetrics(reg)}
			start := time.Now()
			if err := g.CheckOpenID(context.Background(), openid, 1, secheck.Field{Name: "nickname", Text: secret}); err != nil {
				t.Fatalf("应放行：%v", err)
			}
			if d := time.Since(start); d > time.Second {
				t.Fatalf("超时没生效：%v", d)
			}
			out := logs.String()
			if strings.Contains(out, secret) || strings.Contains(out, openid) {
				t.Fatalf("日志里有原文或 openid：%s", out)
			}
			if !strings.Contains(out, "outcome="+tc.outcome) || !strings.Contains(out, "field=nickname") {
				t.Fatalf("日志里应有结论和字段名：%s", out)
			}
			if m := metricsText(reg); !strings.Contains(m, `outcome="`+tc.outcome+`"} 1`) {
				t.Fatalf("指标不对：\n%s", m)
			}
		})
	}
}

// 拦截和通过的日志也不带原文和 openid。
func TestGuardLogsNoContentOnVerdict(t *testing.T) {
	logs := captureLogs(t)
	g := &secheck.Guard{Checker: &fakeChecker{riskyOn: "坏"}}
	_ = g.CheckOpenID(context.Background(), "openid-zzz", 1, secheck.Field{Name: "nickname", Text: "坏蛋称呼"})
	_ = g.CheckOpenID(context.Background(), "openid-zzz", 1, secheck.Field{Name: "nickname", Text: "好人称呼"})
	out := logs.String()
	if strings.Contains(out, "称呼") || strings.Contains(out, "openid-zzz") {
		t.Fatalf("日志里有原文或 openid：%s", out)
	}
	if !strings.Contains(out, "label=20002") {
		t.Fatalf("拦截日志应有 label：%s", out)
	}
}

func TestDevChecker(t *testing.T) {
	v, _ := secheck.DevChecker{}.Check(context.Background(), "o", 1, "我是"+secheck.DevRiskyWord)
	if !v.Risky {
		t.Fatal("含测试词应判 risky")
	}
	v, _ = secheck.DevChecker{}.Check(context.Background(), "o", 1, "老王")
	if v.Risky {
		t.Fatal("普通称呼应通过")
	}
}
