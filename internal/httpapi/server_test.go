package httpapi_test

import (
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/httpapi"
	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/web"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := httptest.NewServer(httpapi.New(config.Config{DataDir: t.TempDir()}, st))
	t.Cleanup(srv.Close)
	return srv
}

func TestHealthzOK(t *testing.T) {
	srv := newServer(t)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("响应 %v，期望 status=ok", body)
	}
}

// 数据库挂掉时 /healthz 要如实报 503，而不是假装健康。
func TestHealthzReportsUnavailableDatabase(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	srv := httptest.NewServer(httpapi.New(config.Config{}, st))
	defer srv.Close()
	_ = st.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 %d，期望 503", resp.StatusCode)
	}
}

// `/` 返回内嵌的前端页面。需要先构建前端（make web-build）；仓库里 dist 只有 .gitkeep。
func TestRootServesEmbeddedFrontend(t *testing.T) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		t.Fatalf("读取内嵌资源: %v", err)
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Skip("web/dist 里还没有 index.html，先执行 make web-build")
	}

	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type 是 %q，期望 text/html", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if !strings.Contains(string(body), `id="app"`) {
		t.Fatalf("返回的不是前端页面：%s", truncate(string(body)))
	}
}

// 单页应用：未知路径回落到 index.html，交给前端路由。
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		t.Fatalf("读取内嵌资源: %v", err)
	}
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Skip("web/dist 里还没有 index.html，先执行 make web-build")
	}

	srv := newServer(t)
	resp, err := http.Get(srv.URL + "/g/8K2")
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d，期望 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	if !strings.Contains(string(body), `id="app"`) {
		t.Fatalf("没有回落到 index.html：%s", truncate(string(body)))
	}
}

func truncate(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
