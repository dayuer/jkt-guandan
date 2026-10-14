package share

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/image/font/opentype"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/games"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// §5.4：同一个键的并发请求只渲染一次。
func TestCacheSingleflight(t *testing.T) {
	c := newImageCache(16, 2)
	var calls atomic.Int32
	render := func() ([]byte, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return []byte("png"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b, err := c.get(context.Background(), "og|A|1|2", render); err != nil || string(b) != "png" {
				t.Errorf("%q %v", b, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("50 个并发请求应只渲染 1 次，实际 %d", calls.Load())
	}
	// 之后直接命中缓存。
	_, _ = c.get(context.Background(), "og|A|1|2", render)
	if calls.Load() != 1 {
		t.Fatal("缓存没命中")
	}
}

// 不同的键最多 2 个并发渲染；缓存有上限。
func TestCacheConcurrencyAndBound(t *testing.T) {
	c := newImageCache(3, 2)
	var inFlight, maxSeen atomic.Int32
	render := func() ([]byte, error) {
		n := inFlight.Add(1)
		for m := maxSeen.Load(); n > m && !maxSeen.CompareAndSwap(m, n); m = maxSeen.Load() {
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return []byte("x"), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = c.get(context.Background(), string(rune('a'+i)), render)
		}(i)
	}
	wg.Wait()
	if m := maxSeen.Load(); m != 2 {
		t.Fatalf("最多 2 个并发渲染，实际 %d", m)
	}
	if n := len(c.entries); n != 3 || len(c.order) != 3 {
		t.Fatalf("缓存应只保留 3 项，实际 %d", n)
	}
}

// 渲染出错（或 panic）不缓存，返回错误，由 handler 换成通用图。
func TestCacheRenderErrors(t *testing.T) {
	c := newImageCache(4, 2)
	if _, err := c.get(context.Background(), "k", func() ([]byte, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("应返回错误")
	}
	if _, err := c.get(context.Background(), "k", func() ([]byte, error) { panic("渲染器炸了") }); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("panic 应变成错误：%v", err)
	}
	if b, err := c.get(context.Background(), "k", func() ([]byte, error) { return []byte("ok"), nil }); err != nil || string(b) != "ok" {
		t.Fatal("出错的结果不该被缓存")
	}
}

// §12.5：渲染失败（出错或 panic）时，handler 返回内置的通用 OG 图，而不是 500。
func TestImageFallsBackToGenericOG(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC))
	if _, err := st.Seed(ctx, clk.Now()); err != nil {
		t.Fatal(err)
	}
	h := &handler{svc: games.NewService(st, clk, nil), publicURL: "https://x", clk: clk, images: newImageCache(4, 2)}
	for _, render := range []func(Card) (image.Image, error){
		func(Card) (image.Image, error) { return nil, errors.New("字体坏了") },
		func(Card) (image.Image, error) { panic("渲染器炸了") },
	} {
		req := httptest.NewRequest(http.MethodGet, "/g/SEED01/poster.png", nil)
		req.SetPathValue("code", "SEED01")
		rec := httptest.NewRecorder()
		h.image(rec, req, "poster", render)
		if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
			t.Fatalf("%d %v", rec.Code, rec.Header())
		}
		img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
		if err != nil || img.Bounds().Dx() != OGWidth || img.Bounds().Dy() != OGHeight {
			t.Fatalf("应返回通用 OG 图：%v", err)
		}
	}
}

// 称呼里的 emoji 没有字形：不画成方框，首字跳过 emoji。
func TestRenderableSkipsEmoji(t *testing.T) {
	if err := loadFonts(); err != nil {
		t.Fatal(err)
	}
	fonts := []*opentype.Font{serifFont, sansFont}
	if got := renderable("老王😀🀄️ok", fonts); got != "老王ok" {
		t.Fatalf("renderable = %q", got)
	}
	for in, want := range map[string]string{"😀Lily": "L", "老王": "老", "  阿杰": "阿", "😀🎉": "牌", "掼蛋": "掼"} {
		if got := initial(in, fonts); got != want {
			t.Errorf("initial(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestFitTruncates(t *testing.T) {
	if err := loadFonts(); err != nil {
		t.Fatal(err)
	}
	c := newCanvas(10, 10, colRed)
	defer c.close()
	st := textStyle{[]*opentype.Font{sansFont}, 40, colCream}
	long := strings.Repeat("鼎泰丰包间", 20)
	got := c.fit(long, 600, st)
	if c.measure(got, st) > 600 || !strings.HasSuffix(got, "…") {
		t.Fatalf("截断后 %q 宽 %.0f", got, c.measure(got, st))
	}
	if c.fit("短", 600, st) != "短" {
		t.Fatal("够短的不该截断")
	}
}

// 海报和 OG 图在各种状态下都能出图，尺寸正确。
func TestRenderSizes(t *testing.T) {
	base := Card{StartsAt: time.Now().Add(time.Hour), Now: time.Now(), Venue: "PIK Avenue", URL: "https://guandan.synon.ai/g/SEED02"}
	for _, c := range []Card{
		{Seats: nil, Missing: 4, Status: "open"},
		{Seats: []string{"a", "b", "c", "d"}, Missing: 0, Status: "full"},
		{Seats: []string{"老王"}, Missing: 3, Status: "cancelled"},
		{Seats: []string{"😀"}, Missing: 3, Status: "done", Venue: strings.Repeat("很长的地点", 30)},
	} {
		card := base
		card.Seats, card.Missing, card.Status = c.Seats, c.Missing, c.Status
		if c.Venue != "" {
			card.Venue = c.Venue
		}
		p, err := RenderPoster(card)
		if err != nil || p.Bounds().Dx() != PosterWidth || p.Bounds().Dy() != PosterHeight {
			t.Fatalf("海报 %+v：%v", c, err)
		}
		o, err := RenderOG(card)
		if err != nil || o.Bounds().Dx() != OGWidth || o.Bounds().Dy() != OGHeight {
			t.Fatalf("OG %+v：%v", c, err)
		}
	}
}
