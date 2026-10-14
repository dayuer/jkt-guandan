package share_test

import (
	"bytes"
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/share"
	"github.com/dayuer/jkt-guandan/internal/store"
)

const indexHTML = `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="utf-8" />
    <title>随时掼掼蛋</title>
    <script type="module" src="/assets/index.js"></script>
  </head>
  <body><div id="app"></div></body>
</html>`

func setupPages(t *testing.T) (*httptest.Server, *store.Store, *clock.Fake) {
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
	mount(mux, share.Options{
		Games: games.NewService(st, clk, nil), PublicURL: "https://guandan.synon.ai", Clock: clk,
		Dist: fstest.MapFS{"index.html": {Data: []byte(indexHTML)}},
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st, clk
}

func exec(t *testing.T, st *store.Store, q string, args ...any) {
	t.Helper()
	if err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// §10.6：地点、称呼、备注都是用户输入，注入 OG 标签时必须按 HTML 上下文转义。
func TestOGPageEscapesUserInput(t *testing.T) {
	srv, st, _ := setupPages(t)
	exec(t, st, `UPDATE games SET venue = ?, note = ? WHERE short_code = 'SEED01'`,
		`"><script>alert(1)</script>`, `a & b </title><img src=x onerror=alert(2)>`)
	exec(t, st, `UPDATE users SET nickname = ? WHERE id = 'seed-laowang'`, `老王"><svg onload=alert(3)>`)

	resp, body := get(t, srv, "/g/SEED01")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	for _, bad := range []string{"<script>alert", "<img src=x", "<svg onload", `"><script`, `"><svg`} {
		if strings.Contains(body, bad) {
			t.Fatalf("输出里出现了没转义的 %q：\n%s", bad, body)
		}
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") || !strings.Contains(body, "a &amp; b") {
		t.Fatalf("用户输入应被转义后保留：\n%s", body)
	}
	if n := strings.Count(body, "<title>"); n != 1 {
		t.Fatalf("<title> 应只有 1 个，实际 %d", n)
	}
	// 页面其他部分原样保留（脚本、挂载点）。
	if !strings.Contains(body, `<script type="module" src="/assets/index.js"></script>`) || !strings.Contains(body, `<div id="app">`) {
		t.Fatalf("原页面内容丢了：\n%s", body)
	}
}

func TestOGPageContent(t *testing.T) {
	srv, st, clk := setupPages(t)
	_, body := get(t, srv, "/g/SEED01")
	for _, want := range []string{
		`<meta property="og:title" content="掼蛋局 · 今天 10/13 周二 19:30 · Pacific Place · 鼎泰丰包间" />`,
		`<meta property="og:description" content="还差 1 人，点开占座 · 局头 老王 · 打到 A 为止，新手友好。停车 P2。" />`,
		`<meta property="og:url" content="https://guandan.synon.ai/g/SEED01" />`,
		`<meta property="og:image" content="https://guandan.synon.ai/g/SEED01/og.png?v=1-3-20261013" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("缺少 %s", want)
		}
	}
	// 已取消：标题带上【已取消】。
	if _, err := st.CancelGame(context.Background(), "SEED01", clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, body := get(t, srv, "/g/SEED01"); !strings.Contains(body, `content="【已取消】掼蛋局`) || !strings.Contains(body, "这局已取消") {
		t.Fatalf("取消的局：\n%s", body)
	}
}

// 牌局不存在：返回普通的 index.html，由前端显示「这局不存在」。
func TestOGPageUnknownGameServesPlainIndex(t *testing.T) {
	srv, _, _ := setupPages(t)
	resp, body := get(t, srv, "/g/NOPE99")
	if resp.StatusCode != 200 || body != indexHTML {
		t.Fatalf("%d\n%s", resp.StatusCode, body)
	}
}

func decodeSize(t *testing.T, body string) (int, int) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader([]byte(body)))
	if err != nil {
		t.Fatalf("不是 PNG：%v", err)
	}
	return img.Bounds().Dx(), img.Bounds().Dy()
}

func TestImages(t *testing.T) {
	srv, st, clk := setupPages(t)
	for path, size := range map[string][2]int{
		"/g/SEED01/og.png":     {share.OGWidth, share.OGHeight},
		"/g/SEED04/poster.png": {share.PosterWidth, share.PosterHeight}, // 满座的局
	} {
		resp, body := get(t, srv, path)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" ||
			resp.Header.Get("Cache-Control") != "public, max-age=60" {
			t.Fatalf("%s：%d %v", path, resp.StatusCode, resp.Header)
		}
		if w, h := decodeSize(t, body); w != size[0] || h != size[1] {
			t.Fatalf("%s 尺寸 %dx%d", path, w, h)
		}
	}
	// 已取消的局照样能出图。
	if _, err := st.CancelGame(context.Background(), "SEED02", clk.Now()); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, srv, "/g/SEED02/poster.png"); resp.StatusCode != 200 {
		t.Fatalf("取消的局出图：%d", resp.StatusCode)
	}
	if resp, _ := get(t, srv, "/g/NOPE99/og.png"); resp.StatusCode != 404 {
		t.Fatalf("不存在的局：%d", resp.StatusCode)
	}
}

// 同一张图被很多人同时请求（链接刚发进大群），每次响应都一样，并且都成功。
// 「只渲染一次」在 cache_internal_test.go 里直接数渲染次数。
func TestConcurrentImageRequests(t *testing.T) {
	srv, _, _ := setupPages(t)
	var wg sync.WaitGroup
	bodies := make([]string, 30)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/g/SEED03/og.png")
			if err != nil || resp.StatusCode != 200 {
				t.Errorf("请求 %d：%v", i, err)
				return
			}
			defer resp.Body.Close()
			var b bytes.Buffer
			_, _ = b.ReadFrom(resp.Body)
			bodies[i] = b.String()
		}(i)
	}
	wg.Wait()
	for i := range bodies {
		if bodies[i] != bodies[0] {
			t.Fatal("同一张图的响应不一致")
		}
	}
}

// 前端分享面板（#52）拼的是 /g/{code}/poster.png?v={version}：v 只用来让缓存失效，
// 渲染一律以当前牌局为准，带旧的 v 也拿到最新的图。
func TestPosterVersionQueryOnlyBustsCaches(t *testing.T) {
	srv, st, clk := setupPages(t)
	_, fresh := get(t, srv, "/g/SEED01/poster.png?v=1")
	_, stale := get(t, srv, "/g/SEED01/poster.png?v=999")
	if fresh != stale {
		t.Fatal("v 参数不该影响渲染结果")
	}
	if _, err := st.CancelGame(context.Background(), "SEED01", clk.Now()); err != nil {
		t.Fatal(err)
	}
	_, afterCancel := get(t, srv, "/g/SEED01/poster.png?v=1") // 旧的 v
	if afterCancel == fresh {
		t.Fatal("牌局变了，即使带着旧的 v 也应拿到新图")
	}
}

// #54 复审：图上写着「今天」「明天」。跨过雅加达的午夜，局没变也要换图，og:image 的地址也要变。
func TestImagesChangeAcrossJakartaMidnight(t *testing.T) {
	srv, _, clk := setupPages(t) // 10/13 15:00 WIB；SEED03 是明天 14:00
	_, before := get(t, srv, "/g/SEED03/poster.png")
	_, ogBefore := get(t, srv, "/g/SEED03/og.png")
	_, pageBefore := get(t, srv, "/g/SEED03")

	clk.Advance(8*time.Hour + 30*time.Minute) // 23:30 WIB：还是同一天，用缓存
	if _, again := get(t, srv, "/g/SEED03/poster.png"); again != before {
		t.Fatal("同一天里局没变，应该是同一张图")
	}

	clk.Advance(time.Hour) // 00:30 WIB，第二天：「明天」变成「今天」
	if _, after := get(t, srv, "/g/SEED03/poster.png"); after == before {
		t.Fatal("跨过雅加达午夜后海报没有重新渲染")
	}
	if _, after := get(t, srv, "/g/SEED03/og.png"); after == ogBefore {
		t.Fatal("跨过雅加达午夜后 OG 图没有重新渲染")
	}
	_, pageAfter := get(t, srv, "/g/SEED03")
	if !strings.Contains(pageBefore, "og.png?v=1-") || !strings.Contains(pageAfter, "-20261014\"") || pageAfter == pageBefore {
		t.Fatalf("og:image 的地址应带上雅加达日期：\n%s", pageAfter)
	}
	if !strings.Contains(pageAfter, "今天") {
		t.Fatalf("第二天的标题应是「今天」：\n%s", pageAfter)
	}
}

// §10.3：跳出微信的链接是「牌局页 + ?claim=」，页面必须带 Referrer-Policy: no-referrer，
// 不让 claim token 经 Referer 头泄露；不带 claim 的页面不加。
func TestOGPageClaimHasNoReferrer(t *testing.T) {
	srv, _, _ := setupPages(t)
	for path, want := range map[string]string{
		"/g/SEED01?claim=tok123": "no-referrer",
		"/g/NOPE99?claim=tok123": "no-referrer", // 牌局不存在、返回原样 index.html 时也要带
		"/g/SEED01":              "",
	} {
		resp, _ := get(t, srv, path)
		if got := resp.Header.Get("Referrer-Policy"); got != want {
			t.Errorf("%s：Referrer-Policy = %q，期望 %q", path, got, want)
		}
	}
}
