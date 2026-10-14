package share

import (
	"errors"
	"fmt"
	"image"
	"io/fs"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/games"
	"github.com/dayuer/jkt-guandan/server/internal/route"
)

// Options 组装分享相关的路由。
type Options struct {
	Games     *games.Service
	PublicURL string
	Clock     clock.Clock
	// Dist 是前端构建产物（app/h5/dist）。有 index.html 时才挂 GET /g/{code}，否则交给前端兜底。
	Dist fs.FS
}

// Routes 是分享相关的路由。它们是公开的（和 GET /api/games/{code} 能看到的数据一致），不识别身份：
// 链接预览的抓取器没有 cookie，图片也不该因为谁来看而不同。
//
//	GET /g/{code}              注入了这一局 OG 标签的页面（§5.4）：微信、WhatsApp 的链接预览
//	GET /g/{code}/og.png       1200×630 的预览图
//	GET /g/{code}/poster.png   1080×1440 的海报（设计稿 C2），带指向牌局页面的二维码
//	GET /g/{code}/game.ics     日历文件（§5.4）：Safari、Chrome 里「加到日历」
//	GET /g/{code}/google       302 到 Google Calendar 的新建事件页（方案 §6）
//
// 前端构建产物（Dist）里有 index.html 时才有 GET /g/{code}，否则交给前端兜底。
// 微信内置浏览器下载不了 .ics，前端在那里不显示日历按钮，改为引导跳出（方案 §6）。
func Routes(opts Options) []route.Route {
	if opts.Clock == nil {
		opts.Clock = clock.System{}
	}
	h := &handler{svc: opts.Games, publicURL: opts.PublicURL, clk: opts.Clock, images: newImageCache(256, 2)}
	var routes []route.Route
	if opts.Dist != nil {
		if page, err := NewPage(opts.Dist); err == nil {
			h.page = page
			routes = append(routes, route.Route{Method: http.MethodGet, Pattern: "/g/{code}", Access: route.Anonymous, Handler: http.HandlerFunc(h.ogPage)})
		} else {
			slog.Warn("前端没有构建，/g/{code} 不注入 OG 标签", "err", err)
		}
	}
	return append(routes,
		route.Route{Method: http.MethodGet, Pattern: "/g/{code}/og.png", Access: route.Anonymous, Handler: http.HandlerFunc(h.ogImage)},
		route.Route{Method: http.MethodGet, Pattern: "/g/{code}/poster.png", Access: route.Anonymous, Handler: http.HandlerFunc(h.posterImage)},
		route.Route{Method: http.MethodGet, Pattern: "/g/{code}/game.ics", Access: route.Anonymous, Handler: http.HandlerFunc(h.ics)},
		route.Route{Method: http.MethodGet, Pattern: "/g/{code}/google", Access: route.Anonymous, Handler: http.HandlerFunc(h.google)},
	)
}

type handler struct {
	svc       *games.Service
	publicURL string
	clk       clock.Clock
	page      *Page
	images    *imageCache
}

// game 读出这一局；不存在 404，出错 503（都已经写好响应）。
func (h *handler) game(w http.ResponseWriter, r *http.Request) (*games.Detail, bool) {
	d, err := h.svc.Get(r.Context(), r.PathValue("code"), "")
	if errors.Is(err, games.ErrNotFound) {
		http.Error(w, "牌局不存在", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		slog.Error("读取牌局失败", "code", r.PathValue("code"), "err", err)
		http.Error(w, "暂时无法读取牌局", http.StatusServiceUnavailable)
		return nil, false
	}
	return d, true
}

func (h *handler) event(w http.ResponseWriter, r *http.Request) (Event, bool) {
	d, ok := h.game(w, r)
	if !ok {
		return Event{}, false
	}
	return EventFor(d, h.publicURL), true
}

// ogPage 返回注入了这一局 OG 标签的页面。牌局不存在（或读取出错）时返回普通的 index.html，
// 由前端显示「这局不存在」：链接预览拿到的是站点默认的标题。
func (h *handler) ogPage(w http.ResponseWriter, r *http.Request) {
	// 跳出微信的链接是「牌局页 + ?claim=<token>」：不能让 token 经 Referer 头带给别处（§10.3，
	// 和 httpapi 里静态资源的 referrerPolicyForClaim 一致）。
	if r.URL.Query().Has("claim") {
		w.Header().Set("Referrer-Policy", "no-referrer")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	d, err := h.svc.Get(r.Context(), r.PathValue("code"), "")
	if err != nil {
		if !errors.Is(err, games.ErrNotFound) {
			slog.Error("读取牌局失败", "code", r.PathValue("code"), "err", err)
		}
		_, _ = w.Write(h.page.Index())
		return
	}
	body, err := h.page.Render(OGFor(d, h.publicURL, h.clk.Now()))
	if err != nil {
		slog.Error("渲染 OG 页面失败", "code", d.Game.ShortCode, "err", err)
		body = h.page.Index()
	}
	_, _ = w.Write(body)
}

func (h *handler) ogImage(w http.ResponseWriter, r *http.Request) {
	h.image(w, r, "og", RenderOG, genericOG)
}
func (h *handler) posterImage(w http.ResponseWriter, r *http.Request) {
	h.image(w, r, "poster", RenderPoster, genericPoster)
}

// imageKey 是缓存键：「种类 + 短码 + 版本号 + 座位数 + 雅加达当天日期」。图上写着「今天」「明天」，
// 跨过雅加达的午夜，即使局没变也要重新渲染（#54 复审）。
func imageKey(kind string, d *games.Detail, now time.Time) string {
	return fmt.Sprintf("%s|%s|%d|%d|%s", kind, d.Game.ShortCode, d.Game.Version, len(d.Seats), jakartaDay(now))
}

// jakartaDay 是雅加达时区的当天日期，例如 20261014。
func jakartaDay(now time.Time) string { return now.In(Jakarta).Format("20060102") }

// image 返回渲染好的图（缓存键见 imageKey），同键合并、最多 2 个并发渲染。
// 渲染失败时返回同样尺寸的内置通用图（§12.5），并记 error 日志。
func (h *handler) image(w http.ResponseWriter, r *http.Request, kind string, render func(Card) (image.Image, error), fallback func() []byte) {
	d, ok := h.game(w, r)
	if !ok {
		return
	}
	now := h.clk.Now()
	key := imageKey(kind, d, now)
	card := CardFor(d, h.publicURL, now)
	png, err := h.images.get(r.Context(), key, func() ([]byte, error) {
		img, err := render(card)
		if err != nil {
			return nil, err
		}
		return EncodePNG(img)
	})
	if err != nil {
		slog.Error("渲染图片失败，返回通用图", "kind", kind, "code", d.Game.ShortCode, "err", err)
		png = fallback()
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(png)
}

// genericOG、genericPoster 是兜底的通用图，进程里各只渲染一次。
var (
	genericOG     = onceImage(RenderGenericOG)
	genericPoster = onceImage(RenderGenericPoster)
)

func onceImage(render func() image.Image) func() []byte {
	return sync.OnceValue(func() []byte {
		b, err := EncodePNG(render())
		if err != nil {
			panic(err) // 纯色加形状，编码不会失败
		}
		return b
	})
}

func (h *handler) ics(w http.ResponseWriter, r *http.Request) {
	ev, ok := h.event(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	// 文件名只用短码（Crockford base32，路由已经约束过），不会带出用户输入的内容。
	w.Header().Set("Content-Disposition", `attachment; filename="guandan-`+safeName(r.PathValue("code"))+`.ics"`)
	// 局头改时间后版本号会变，不能被缓存住。
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(ICS(ev, h.clk.Now()))
}

func (h *handler) google(w http.ResponseWriter, r *http.Request) {
	ev, ok := h.event(w, r)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.Redirect(w, r, GoogleCalendarURL(ev), http.StatusFound)
}

// safeName 只保留字母和数字，用在 Content-Disposition 的文件名里。
func safeName(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(out) < 16; i++ {
		c := s[i]
		if ('0' <= c && c <= '9') || ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') {
			out = append(out, c)
		}
	}
	return string(out)
}
