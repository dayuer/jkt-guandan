package notify

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/internal/identity"
	"github.com/dayuer/jkt-guandan/internal/route"
)

// Routes 是推送相关的路由（§6）。httpapi 按这张表注册、统一识别身份和授权（#41）：
//
//	GET    /api/push/key        VAPID 公钥（公开，不识别身份）
//	POST   /api/push/subscribe  保存订阅（要有身份）
//	DELETE /api/push/subscribe  取消订阅（要有身份）
//
// svc 为 nil 时只能用来列路由，不能处理请求。
func Routes(svc *Service) []route.Route {
	h := &handler{svc: svc}
	return []route.Route{
		{Method: http.MethodGet, Pattern: "/api/push/key", Access: route.Anonymous, Handler: http.HandlerFunc(h.key)},
		{Method: http.MethodPost, Pattern: "/api/push/subscribe", Access: route.User, Handler: http.HandlerFunc(h.subscribe)},
		{Method: http.MethodDelete, Pattern: "/api/push/subscribe", Access: route.User, Handler: http.HandlerFunc(h.unsubscribe)},
	}
}

type handler struct {
	svc *Service
}

func (h *handler) key(w http.ResponseWriter, _ *http.Request) {
	// 公钥不会变（变了所有订阅都会失效），可以缓存一会儿。
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]string{"key": h.svc.PublicKey()})
}

// who 取出中间件放进 ctx 的身份（没有身份 401、被封禁 403 已经在中间件里处理了）。
func (h *handler) who(w http.ResponseWriter, r *http.Request) (*identity.Identity, bool) {
	ident := identity.FromContext(r.Context())
	if ident == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
		return nil, false
	}
	return ident, true
}

func (h *handler) subscribe(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.who(w, r)
	if !ok {
		return
	}
	var sub Subscription
	if err := json.NewDecoder(r.Body).Decode(&sub); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	err := h.svc.Subscribe(r.Context(), ident.User.ID, ident.Device.ID, sub)
	switch {
	case errors.Is(err, ErrBadSubscription):
		writeError(w, http.StatusBadRequest, "bad_subscription", "推送订阅不合法")
	case err != nil:
		slog.Error("保存推送订阅失败", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法保存，请稍后再试")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *handler) unsubscribe(w http.ResponseWriter, r *http.Request) {
	ident, ok := h.who(w, r)
	if !ok {
		return
	}
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Endpoint == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 endpoint")
		return
	}
	err := h.svc.Unsubscribe(r.Context(), ident.User.ID, body.Endpoint)
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "没有这个订阅")
	case err != nil:
		slog.Error("取消推送订阅失败", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法处理，请稍后再试")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
