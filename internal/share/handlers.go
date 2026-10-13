package share

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/games"
)

// Register 挂上分享相关的路由。它们是公开的（和 GET /api/games/{code} 能看到的数据一致），不需要身份。
//
//	GET /g/{code}/game.ics   日历文件（§5.4）：Safari、Chrome 里「加到日历」
//	GET /g/{code}/google     302 到 Google Calendar 的新建事件页（方案 §6）
//
// 微信内置浏览器下载不了 .ics，前端在那里不显示这两个按钮，改为引导跳出（方案 §6）。
func Register(mux *http.ServeMux, svc *games.Service, publicURL string, clk clock.Clock) {
	if clk == nil {
		clk = clock.System{}
	}
	h := &handler{svc: svc, publicURL: publicURL, clk: clk}
	mux.HandleFunc("GET /g/{code}/game.ics", h.ics)
	mux.HandleFunc("GET /g/{code}/google", h.google)
}

type handler struct {
	svc       *games.Service
	publicURL string
	clk       clock.Clock
}

func (h *handler) event(w http.ResponseWriter, r *http.Request) (Event, bool) {
	d, err := h.svc.Get(r.Context(), r.PathValue("code"), "")
	if errors.Is(err, games.ErrNotFound) {
		http.Error(w, "牌局不存在", http.StatusNotFound)
		return Event{}, false
	}
	if err != nil {
		slog.Error("读取牌局失败", "code", r.PathValue("code"), "err", err)
		http.Error(w, "暂时无法读取牌局", http.StatusServiceUnavailable)
		return Event{}, false
	}
	return EventFor(d, h.publicURL), true
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
