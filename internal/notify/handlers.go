package notify

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/internal/identity"
)

// Register 挂上推送相关的路由（§6）：
//
//	GET    /api/push/key        VAPID 公钥（公开）
//	POST   /api/push/subscribe  保存订阅（要有身份）
//	DELETE /api/push/subscribe  取消订阅（要有身份）
//
// 前面的中间件已经做了 CSRF（JSON + 本站 Origin）、请求体上限和按 IP 的客户端识别。
func Register(mux *http.ServeMux, svc *Service, ident *identity.Service) {
	h := &handler{svc: svc, ident: ident}
	mux.HandleFunc("GET /api/push/key", h.key)
	mux.HandleFunc("POST /api/push/subscribe", h.subscribe)
	mux.HandleFunc("DELETE /api/push/subscribe", h.unsubscribe)
}

type handler struct {
	svc   *Service
	ident *identity.Service
}

func (h *handler) key(w http.ResponseWriter, _ *http.Request) {
	// 公钥不会变（变了所有订阅都会失效），可以缓存一会儿。
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]string{"key": h.svc.PublicKey()})
}

// who 认出当前用户；没有身份 401，被封禁 403。
func (h *handler) who(w http.ResponseWriter, r *http.Request) (*identity.Identity, bool) {
	ident := h.ident.FromRequest(r)
	if ident == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
		return nil, false
	}
	banned, err := h.ident.Banned(r.Context(), ident)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法处理，请稍后再试")
		return nil, false
	}
	if banned {
		writeError(w, http.StatusForbidden, "banned", "这个账号已被停用")
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
