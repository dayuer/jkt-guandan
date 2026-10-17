package practice

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/rules"
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
		{Method: http.MethodPost, Pattern: "/api/practice/{id}/play", Access: route.User, Handler: http.HandlerFunc(h.play)},
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

// play 出牌（`POST /api/practice/{id}/play`）。体：
//
//	{"cards":["♠K/0"],"kind":"single","rank":"K","expected_revision":3}
//	{"pass":true,                              "expected_revision":3}
//
// `expected_revision` 必须带（少了是 400）—— 它是乐观锁，和 games 那边 `If-Match` 的规矩一致：
// 宁可不做，也不要拿一个「不知道自己是哪一版」的请求去改局面。
func (h *handler) play(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Cards            []string `json:"cards"`
		Kind             string   `json:"kind"`
		Rank             string   `json:"rank"`
		Pass             bool     `json:"pass"`
		ExpectedRevision *int64   `json:"expected_revision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	if body.ExpectedRevision == nil {
		writeError(w, http.StatusBadRequest, "bad_request", "缺少 expected_revision")
		return
	}
	mv, err := parseMove(body.Cards, body.Kind, body.Rank, body.Pass)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	v, err := h.svc.Play(r.Context(), r.PathValue("id"), userID, mv, *body.ExpectedRevision,
		r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(w, "练习局出牌失败", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// parseMove 把请求体里的牌面 key / 牌型 / 点数解析成引擎的类型。
// 解析放在这一层：引擎要的是它自己的类型，而线上传的是客户端那套写法（两端共用牌例的写法）。
func parseMove(cards []string, kind, rank string, pass bool) (Move, error) {
	if pass {
		return Move{Pass: true}, nil
	}
	if len(cards) == 0 {
		return Move{}, errors.New("出牌要给 cards（不出请 pass:true）")
	}
	out := make([]rules.Card, 0, len(cards))
	// 同一张牌写两次是**调用方的错**，在这里就挡成 400：引擎的「手里有没有」按 ID 查 map，
	// 不数次数，重复的牌会漏到 Recognize 才报「出现了两次」，而那是个没有原因码的普通 error
	// （#88 第 3 件复审 B2：原来落进 default，成了 503 + ERROR 日志）。
	seen := make(map[int]bool, len(cards))
	for _, k := range cards {
		c, err := rules.ParseKey(k)
		if err != nil {
			return Move{}, fmt.Errorf("牌 %q：%w", k, err)
		}
		if seen[c.ID] {
			return Move{}, fmt.Errorf("牌 %q 写了两次：同一张牌不能出两次", k)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	t, err := rules.ParseComboType(kind)
	if err != nil {
		return Move{}, fmt.Errorf("牌型 %q：%w", kind, err)
	}
	rk, err := rules.ParseRank(rank)
	if err != nil {
		return Move{}, fmt.Errorf("点数 %q：%w", rank, err)
	}
	return Move{Cards: out, Type: t, Rank: rk}, nil
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
	var (
		conflict *VersionConflict
		rejected *MoveRejected
	)
	switch {
	case errors.As(err, &conflict):
		// 409 带上**最新局面**（`practice` 键里，和成功响应的形状一样），客户端直接刷新重来。
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":    map[string]string{"code": "version_conflict", "message": err.Error()},
			"practice": conflict.Latest,
		})
	case errors.As(err, &rejected):
		// 一手不成立：**原因码就是引擎那一套**（not-your-turn / does-not-beat / bad-combo / ……），
		// 客户端拿它和自己本地算出来的对照，提示文案可以共用。局面一点没动。
		writeError(w, http.StatusConflict, rejected.Reason, "这一手不成立（"+rejected.Reason+"）")
	case errors.Is(err, ErrDealOver):
		writeError(w, http.StatusConflict, "deal_over", err.Error())
	case errors.Is(err, ErrPracticeEnded):
		// 已经收掉的练习局（建了新局）只能复盘，不能再出牌。
		writeError(w, http.StatusConflict, "practice_ended", "这局练习已经结束了，开一局新的吧")
	case IsIdemKeyReused(err):
		writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", "这个幂等键已经用在别的请求上了")
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
