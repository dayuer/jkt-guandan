package practice

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// Routes 是练习局的接口表。
//
// 两个接口都要求登录（`route.User`）：练习局是**一个人自己的**东西，没有「公开看看」这回事 ——
// 这和 `/api/games/{code}`（公开）是有意的区别。
func Routes(svc *Service) []route.Route {
	h := &handler{svc: svc}
	return []route.Route{
		{Method: http.MethodPost, Pattern: "/api/practice", Access: route.User, Handler: http.HandlerFunc(h.create)},
		{Method: http.MethodGet, Pattern: "/api/practice/{id}", Access: route.User, Handler: http.HandlerFunc(h.get)},
	}
}

type handler struct{ svc *Service }

// create 建一局（POST /api/practice）：发牌、落库、返回局面。没有请求参数 ——
// 打几、坐几号座都由服务端定（见 Service.Create）。
func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Create(r.Context(), userID)
	if err != nil {
		writeServiceError(w, "建练习局失败", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// get 取一局（GET /api/practice/{id}）：断线回来、刷新、复盘都走它。
func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	v, err := h.svc.Get(r.Context(), r.PathValue("id"), userID)
	if err != nil {
		writeServiceError(w, "取练习局失败", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// requireUser 取当前身份；没有就 401。
func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if id := identity.UserID(r.Context()); id != "" {
		return id, true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
	return "", false
}

// writeServiceError 把业务错误映射成状态码（照 games 的口径）。
//
// 特别地：**「不是你的局」和「不存在」返回同一个 404**（`ErrNotFound`）——
// id 是不透明的随机串，没必要让对方知道「这个 id 存在、但不是你的」（见 Service.Get）。
func writeServiceError(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "practice_not_found", "练习局不存在")
	case errors.Is(err, ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "操作太频繁，请稍后再试")
	case errors.Is(err, store.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "系统繁忙，请稍后再试")
	default:
		slog.Error(what, "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时不可用，请稍后再试")
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("写响应失败", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
