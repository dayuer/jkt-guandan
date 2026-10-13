package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/internal/supervise"
	"github.com/dayuer/jkt-guandan/web"
)

type fixture struct {
	srv   *httptest.Server
	store *store.Store
	group *supervise.Group
	clk   *clock.Fake
}

// newFixture 起一个真实的 HTTP 服务，数据库用 t.TempDir() 下的真文件。
func newFixture(t *testing.T, cfg config.Config, routes func(*http.ServeMux)) *fixture {
	t.Helper()
	dir := t.TempDir()

	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	clk := clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))
	group := supervise.New(clk)
	cfg.DataDir = dir

	srv := httptest.NewServer(httpapi.New(httpapi.Options{
		Config:         cfg,
		Store:          st,
		Group:          group,
		Clock:          clk,
		RegisterRoutes: routes,
	}))
	t.Cleanup(srv.Close)

	return &fixture{srv: srv, store: st, group: group, clk: clk}
}

func (f *fixture) get(t *testing.T, path string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	return resp, string(body)
}

func frontendBuilt(t *testing.T) bool {
	t.Helper()
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		t.Fatalf("读取内嵌资源: %v", err)
	}
	_, err = fs.Stat(dist, "index.html")
	return err == nil
}

func TestHealthzAlwaysOK(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)
	resp, body := f.get(t, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	if !strings.Contains(body, `"ok"`) {
		t.Fatalf("响应 %s", body)
	}
}

// /healthz 是存活检查，数据库挂了它仍然要 200（§12.5）。
func TestHealthzIgnoresDatabaseFailure(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)
	_ = f.store.Close()

	resp, _ := f.get(t, "/healthz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
}

func TestReadyzOK(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)
	resp, body := f.get(t, "/readyz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200（响应 %s）", resp.StatusCode, body)
	}
	if !strings.Contains(body, `"ok"`) {
		t.Fatalf("响应 %s", body)
	}
}

// 写事务不可用时 /readyz 必须失败，否则探活看不出问题（§12.5）。
func TestReadyzFailsWhenWritesBreak(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)
	_ = f.store.Close()

	resp, body := f.get(t, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 %d，期望 503", resp.StatusCode)
	}
	if !strings.Contains(body, "写事务不可用") {
		t.Fatalf("响应里应说明原因：%s", body)
	}
}

// 后台任务心跳过期时 /readyz 也要失败（§12.5）。
func TestReadyzFailsOnStaleBackgroundTask(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)

	beaten := make(chan struct{})
	f.group.Go(t.Context(), "heartbeat-test", 50*time.Millisecond, func(ctx context.Context, beat func()) error {
		beat()
		close(beaten)
		<-ctx.Done()
		return nil
	})
	<-beaten // 先等它打一次心跳，否则可能先推时钟、后记心跳

	f.clk.Advance(10 * time.Second) // 把假时钟推远，让心跳显得过期

	resp, body := f.get(t, "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 %d，期望 503（响应 %s）", resp.StatusCode, body)
	}
	if !strings.Contains(body, "后台任务心跳过期") {
		t.Fatalf("响应里应提到心跳：%s", body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	f := newFixture(t, config.Config{}, nil)
	resp, _ := f.get(t, "/healthz")

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q，期望 %q", header, got, want)
		}
	}
	csp := resp.Header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "object-src 'none'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP 里缺少 %q：%s", want, csp)
		}
	}
	// 没启用 TLS 时不该发 HSTS。
	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("未启用 TLS 时不该有 HSTS，实际 %q", hsts)
	}
}

func TestHSTSOnlyWithTLS(t *testing.T) {
	f := newFixture(t, config.Config{Domain: "guandan.id", AdminEmail: "a@b.c"}, nil)
	resp, _ := f.get(t, "/healthz")
	if got := resp.Header.Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("HSTS = %q", got)
	}
}

func TestCSRFRejectsNonJSON(t *testing.T) {
	f := newFixture(t, config.Config{PublicURL: "https://guandan.id"}, nil)

	resp, err := http.Post(f.srv.URL+"/api/games", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 %d，期望 403", resp.StatusCode)
	}
}

func TestCSRFRejectsForeignOrigin(t *testing.T) {
	f := newFixture(t, config.Config{PublicURL: "https://guandan.id"}, nil)

	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/games", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 %d，期望 403", resp.StatusCode)
	}
}

func TestCSRFAllowsSameOriginJSON(t *testing.T) {
	seen := false
	f := newFixture(t, config.Config{PublicURL: "https://guandan.id"}, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/ping", func(w http.ResponseWriter, r *http.Request) {
			seen = true
			w.WriteHeader(http.StatusNoContent)
		})
	})

	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/ping", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://guandan.id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("状态码 %d，期望 204", resp.StatusCode)
	}
	if !seen {
		t.Fatal("handler 没有被调用")
	}
}

// 单个请求 panic 要变成 500，不能把进程带走（§12.2）。
func TestPanicRecovery(t *testing.T) {
	f := newFixture(t, config.Config{}, func(mux *http.ServeMux) {
		mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
			panic("炸了")
		})
	})

	resp, body := f.get(t, "/boom")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("状态码 %d，期望 500", resp.StatusCode)
	}
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("响应不是 JSON：%s", body)
	}
	if parsed.Error.Code != "internal" {
		t.Fatalf("错误码 %q，期望 internal", parsed.Error.Code)
	}
}

// 请求体超过 64 KB 时 handler 读 body 会失败（§12.2）。
func TestRequestBodyLimit(t *testing.T) {
	var readErr error
	f := newFixture(t, config.Config{PublicURL: "https://guandan.id"}, func(mux *http.ServeMux) {
		mux.HandleFunc("POST /api/echo", func(w http.ResponseWriter, r *http.Request) {
			_, readErr = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		})
	})

	big := strings.Repeat("a", 70<<10)
	req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/api/echo", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://guandan.id")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if readErr == nil {
		t.Fatal("超过 64 KB 的请求体应该读失败")
	}
}

// `/` 返回内嵌的前端页面。需要先构建前端（make web-build）；仓库里 dist 只有 .gitkeep。
func TestRootServesEmbeddedFrontend(t *testing.T) {
	if !frontendBuilt(t) {
		t.Skip("web/dist 里还没有 index.html，先执行 make web-build")
	}
	f := newFixture(t, config.Config{}, nil)

	resp, body := f.get(t, "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type 是 %q", ct)
	}
	if !strings.Contains(body, `id="app"`) {
		t.Fatalf("返回的不是前端页面：%.200s", body)
	}
}

// 单页应用：未知路径回落到 index.html。
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	if !frontendBuilt(t) {
		t.Skip("web/dist 里还没有 index.html，先执行 make web-build")
	}
	f := newFixture(t, config.Config{}, nil)

	resp, body := f.get(t, "/g/8K2QXM")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	if !strings.Contains(body, `id="app"`) {
		t.Fatalf("没有回落到 index.html：%.200s", body)
	}
}
