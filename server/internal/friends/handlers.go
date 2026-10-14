package friends

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// Routes 是牌友的路由（§6），都要有身份。svc 为 nil 时只能用来列路由。
//
//	GET    /api/friends              牌友列表
//	PUT    /api/follows/{user_id}    星标（幂等，204）
//	DELETE /api/follows/{user_id}    取消星标（幂等，204）
func Routes(svc *Service) []route.Route {
	h := &handler{svc: svc}
	return []route.Route{
		{Method: http.MethodGet, Pattern: "/api/friends", Access: route.User, Handler: http.HandlerFunc(h.list)},
		{Method: http.MethodPut, Pattern: "/api/follows/{user_id}", Access: route.User, Handler: http.HandlerFunc(h.star)},
		{Method: http.MethodDelete, Pattern: "/api/follows/{user_id}", Access: route.User, Handler: http.HandlerFunc(h.unstar)},
	}
}

type handler struct{ svc *Service }

func (h *handler) me(w http.ResponseWriter, r *http.Request) (string, bool) {
	if id := identity.UserID(r.Context()); id != "" {
		return id, true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
	return "", false
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	me, ok := h.me(w, r)
	if !ok {
		return
	}
	list, err := h.svc.List(r.Context(), me)
	if err != nil {
		slog.Error("读牌友列表失败", "err", err)
		writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时无法读取，请稍后再试")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, f := range list {
		out = append(out, map[string]any{
			"user":             map[string]string{"id": f.UserID, "nickname": f.Nickname},
			"together_count":   f.TogetherCount,
			"last_together_at": f.LastTogether.Unix(),
			"starred":          f.Starred,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"friends": out})
}

func (h *handler) star(w http.ResponseWriter, r *http.Request)   { h.follow(w, r, h.svc.Star) }
func (h *handler) unstar(w http.ResponseWriter, r *http.Request) { h.follow(w, r, h.svc.Unstar) }

func (h *handler) follow(w http.ResponseWriter, r *http.Request, do func(ctx context.Context, me, target string) error) {
	me, ok := h.me(w, r)
	if !ok {
		return
	}
	err := do(r.Context(), me, r.PathValue("user_id"))
	switch {
	case errors.Is(err, ErrNotFriend):
		writeError(w, http.StatusNotFound, "not_friend", "只能星标一起打过局的牌友")
	case errors.Is(err, store.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "系统繁忙，请稍后再试")
	case err != nil:
		slog.Error("改星标失败", "err", err)
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
