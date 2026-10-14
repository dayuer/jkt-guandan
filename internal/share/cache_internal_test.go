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
	"unicode/utf8"

	"golang.org/x/image/font/opentype"
	"golang.org/x/text/encoding/traditionalchinese"

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

// §12.5：渲染失败（出错或 panic）时，handler 返回同样尺寸的内置通用图，而不是 500。
// 海报的兜底是竖版（前端按 3:4 占位），OG 的兜底是横版。
func TestImageFallsBackToGenericImage(t *testing.T) {
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
	for _, kind := range []struct {
		name     string
		fallback func() []byte
		w, h     int
	}{{"poster", genericPoster, PosterWidth, PosterHeight}, {"og", genericOG, OGWidth, OGHeight}} {
		for _, render := range []func(Card) (image.Image, error){
			func(Card) (image.Image, error) { return nil, errors.New("字体坏了") },
			func(Card) (image.Image, error) { panic("渲染器炸了") },
		} {
			req := httptest.NewRequest(http.MethodGet, "/g/SEED01/"+kind.name+".png", nil)
			req.SetPathValue("code", "SEED01")
			rec := httptest.NewRecorder()
			h.image(rec, req, kind.name, render, kind.fallback)
			if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
				t.Fatalf("%d %v", rec.Code, rec.Header())
			}
			img, err := png.Decode(bytes.NewReader(rec.Body.Bytes()))
			if err != nil || img.Bounds().Dx() != kind.w || img.Bounds().Dy() != kind.h {
				t.Fatalf("%s 应返回 %d×%d 的通用图：%v %v", kind.name, kind.w, kind.h, err, img.Bounds())
			}
		}
	}
}

// singleflight 合并的请求里，第一个请求者断开了，其他请求照样拿到图（#54 复审）。
func TestCacheFirstCallerCancelDoesNotFailOthers(t *testing.T) {
	c := newImageCache(16, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	render := func() ([]byte, error) {
		close(started)
		<-release
		return []byte("png"), nil
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, err := c.get(firstCtx, "k", render)
		firstErr <- err
	}()
	<-started
	second := make(chan []byte, 1)
	go func() {
		b, _ := c.get(context.Background(), "k", func() ([]byte, error) { t.Error("同键不该再渲染"); return nil, nil })
		second <- b
	}()
	time.Sleep(20 * time.Millisecond) // 让第二个请求进到 singleflight 里
	cancelFirst()
	if err := <-firstErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("断开的请求者应拿到 ctx 的错误：%v", err)
	}
	close(release)
	if b := <-second; string(b) != "png" {
		t.Fatalf("第二个请求应拿到图，实际 %q", b)
	}
	if b, err := c.get(context.Background(), "k", render); err != nil || string(b) != "png" {
		t.Fatal("渲染结果应进缓存")
	}
}

// 称呼里的 emoji 没有字形：不画成方框，首字跳过 emoji、标点和符号；一个能画的都没有就用占位。
func TestRenderableSkipsEmoji(t *testing.T) {
	if err := loadFonts(); err != nil {
		t.Fatal(err)
	}
	fonts := []*opentype.Font{serifFont, sansFont}
	var missed []rune
	if got := renderable("老王😀🀄️ok", fonts, func(r rune) { missed = append(missed, r) }); got != "老王ok" {
		t.Fatalf("renderable = %q", got)
	}
	if len(missed) != 2 || missed[0] != '😀' || missed[1] != '🀄' {
		t.Fatalf("跳过的字应记下来：%q", missed)
	}
	for in, want := range map[string]string{
		"😀Lily": "L", "老王": "老", "  阿杰": "阿", "😀🎉": "?", "掼蛋": "掼",
		`"><b>x`: "B", "【阿杰】": "阿", "★小美": "小", "...": "?", "陳姐": "陳", "賴": "賴",
	} {
		if got := initial(in, fonts, nil); got != want {
			t.Errorf("initial(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// #54 复审：繁体的地点和称呼不能被悄悄丢字。字体子集里有 Big5 一级常用字，
// 「鼎泰豐包間 · 陳姐的茶館」「陳」「賴」都画得出来，渲染时一个字都没有跳过。
func TestTraditionalCharsAreNotSkipped(t *testing.T) {
	card := Card{
		Code: "TRAD01", StartsAt: time.Now().Add(time.Hour), Now: time.Now(),
		Venue: "鼎泰豐包間 · 陳姐的茶館", Seats: []string{"陳", "Lily", "賴阿嬤"}, Missing: 1, Status: "open",
		URL: "https://guandan.synon.ai/g/TRAD01",
	}
	for name, render := range map[string]func(Card) (image.Image, []rune, error){"poster": renderPoster, "og": renderOG} {
		if _, missing, err := render(card); err != nil || len(missing) != 0 {
			t.Errorf("%s：%v，跳过了 %q", name, err, missing)
		}
	}
	// 繁体常用字表里的字全都要在两个字体里有字形。
	if err := loadFonts(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []*opentype.Font{sansFont, serifFont} {
		var lost []rune
		for _, r := range traditionalSample {
			if pick(r, []*opentype.Font{f}) == nil {
				lost = append(lost, r)
			}
		}
		if len(lost) > 0 {
			t.Errorf("字体缺繁体常用字：%q", lost)
		}
	}
}

// 子集里有 Big5 一级常用字（0xA440–0xC67E）的全部 5401 字，两个字体都要有（scripts/fonts/chars.py）。
func TestFontsCoverBig5Level1(t *testing.T) {
	if err := loadFonts(); err != nil {
		t.Fatal(err)
	}
	dec := traditionalchinese.Big5.NewDecoder()
	n := 0
	for code := 0xa440; code <= 0xc67e; code++ {
		b2 := code & 0xff
		if !(0x40 <= b2 && b2 <= 0x7e || 0xa1 <= b2 && b2 <= 0xfe) {
			continue
		}
		out, err := dec.Bytes([]byte{byte(code >> 8), byte(b2)})
		if err != nil || len([]rune(string(out))) != 1 || []rune(string(out))[0] == utf8.RuneError {
			continue
		}
		r := []rune(string(out))[0]
		n++
		for _, f := range []*opentype.Font{sansFont, serifFont} {
			if pick(r, []*opentype.Font{f}) == nil {
				t.Fatalf("字体缺 Big5 一级字 %q（U+%04X）", r, r)
			}
		}
	}
	if n != 5401 {
		t.Fatalf("Big5 一级应有 5401 字，数到 %d", n)
	}
}

// traditionalSample 是一些常见的繁体字（店名、姓氏、地名里常见）。
const traditionalSample = "鼎泰豐包間陳館賴黃劉張趙錢孫吳鄭馮衛蔣沈韓楊朱秦許何呂施華謝鄒喬龍葉蕭蘇鍾譚廣東門開關樓灣號廳麵飯魚雞點茶餐愛國體學會"

// 字体里没有的字要报出来，不能悄悄丢掉。
func TestMissingGlyphsAreReported(t *testing.T) {
	card := Card{
		StartsAt: time.Now().Add(time.Hour), Now: time.Now(),
		Venue: "老地方😀", Seats: []string{"🎉阿杰"}, Missing: 3, Status: "open", URL: "https://x/g/A",
	}
	_, missing, err := renderPoster(card)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != '😀' {
		t.Fatalf("应报出缺的 😀（🎉 是符号，首字直接跳过），实际 %q", missing)
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
