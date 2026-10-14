package share_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/share"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

func setup(t *testing.T) (*httptest.Server, *store.Store, *clock.Fake) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC))
	if _, err := st.Seed(ctx, clk.Now()); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mount(mux, share.Options{Games: games.NewService(st, clk, nil), PublicURL: "https://guandan.synon.ai", Clock: clk})
	client := httptest.NewServer(mux)
	t.Cleanup(client.Close)
	return client, st, clk
}

func get(t *testing.T, srv *httptest.Server, path string) (*http.Response, string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestGameICS(t *testing.T) {
	srv, st, clk := setup(t)
	resp, body := get(t, srv, "/g/SEED01/game.ics")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 %d：%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/calendar; charset=utf-8" {
		t.Errorf("Content-Type %s", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename="guandan-SEED01.ics"` {
		t.Errorf("Content-Disposition %s", cd)
	}
	for _, want := range []string{
		"UID:seed-g1@guandan.synon.ai", "SEQUENCE:1", "SUMMARY:掼蛋 · Pacific Place · 鼎泰丰包间",
		"DTSTART;TZID=Asia/Jakarta:", "URL:https://guandan.synon.ai/g/SEED01", "STATUS:CONFIRMED",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 %q", want)
		}
	}

	// 局头取消后重新导入：同一个 UID、更大的 SEQUENCE、STATUS:CANCELLED，日历会更新原有事件。
	if _, err := st.CancelGame(context.Background(), "SEED01", clk.Now()); err != nil {
		t.Fatal(err)
	}
	_, body = get(t, srv, "/g/SEED01/game.ics")
	if !strings.Contains(body, "UID:seed-g1@guandan.synon.ai") || !strings.Contains(body, "SEQUENCE:2") ||
		!strings.Contains(body, "STATUS:CANCELLED") {
		t.Fatalf("取消后的日历文件不对：\n%s", body)
	}
}

func TestGoogleRedirect(t *testing.T) {
	srv, _, _ := setup(t)
	resp, _ := get(t, srv, "/g/SEED02/google")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码 %d", resp.StatusCode)
	}
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || u.Host != "calendar.google.com" || !strings.Contains(u.Query().Get("text"), "PIK Avenue") {
		t.Fatalf("跳转到 %s", resp.Header.Get("Location"))
	}
}

func TestUnknownGame404(t *testing.T) {
	srv, _, _ := setup(t)
	for _, p := range []string{"/g/NOPE99/game.ics", "/g/NOPE99/google"} {
		if resp, _ := get(t, srv, p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s 状态码 %d", p, resp.StatusCode)
		}
	}
}

// mount 把分享路由直接挂到 mux 上（都是 route.Anonymous，不需要身份中间件）。
func mount(mux *http.ServeMux, opts share.Options) {
	for _, rt := range share.Routes(opts) {
		mux.Handle(rt.Key(), rt.Handler)
	}
}
