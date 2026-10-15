package games

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/friends"
	"github.com/dayuer/jkt-guandan/server/internal/identity"
	"github.com/dayuer/jkt-guandan/server/internal/moderate"
	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// Routes 是牌局包的全部路由（§6）。httpapi 按这张表注册、统一识别身份和授权（#41）。
// 「是不是这一局的局头」要看具体是哪一局，属于业务逻辑，在 service 里判（§10.4）。
// svc 为 nil 时只能用来列路由，不能处理请求。
func Routes(svc *Service) []route.Route {
	h := &handler{svc: svc}
	return []route.Route{
		{Method: http.MethodGet, Pattern: "/api/games", Access: route.Public, Handler: http.HandlerFunc(h.list)},
		{Method: http.MethodGet, Pattern: "/api/games/{code}", Access: route.Public, Handler: http.HandlerFunc(h.detail)},
		{Method: http.MethodPost, Pattern: "/api/games", Access: route.User, Handler: http.HandlerFunc(h.create)},
		{Method: http.MethodPatch, Pattern: "/api/games/{code}", Access: route.User, Handler: http.HandlerFunc(h.update)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/seat", Access: route.User, Handler: http.HandlerFunc(h.join)},
		{Method: http.MethodDelete, Pattern: "/api/games/{code}/seat", Access: route.User, Handler: http.HandlerFunc(h.leave)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/cancel", Access: route.User, Handler: http.HandlerFunc(h.cancel)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/kick", Access: route.User, Handler: http.HandlerFunc(h.kick)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/invites", Access: route.User, Handler: http.HandlerFunc(h.invite)},
		// 牌桌计分（#165，§5.6）：能看详情的人都能看；只有座位上的人能写，在 service 里判。
		{Method: http.MethodGet, Pattern: "/api/games/{code}/score", Access: route.Public, Handler: http.HandlerFunc(h.score)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/score", Access: route.User, Handler: http.HandlerFunc(h.startScore)},
		{Method: http.MethodPost, Pattern: "/api/games/{code}/score/rounds", Access: route.User, Handler: http.HandlerFunc(h.recordRound)},
		{Method: http.MethodPut, Pattern: "/api/games/{code}/score/rounds/{no}", Access: route.User, Handler: http.HandlerFunc(h.editRound)},
	}
}

type handler struct {
	svc *Service
}

// viewer 返回当前用户 id；访客返回空串。身份由 httpapi 的中间件放进 ctx。
func viewer(r *http.Request) string { return identity.UserID(r.Context()) }

func requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	if id := viewer(r); id != "" {
		return id, true
	}
	writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
	return "", false
}

// ---------------------------------------------------------------------------
// 处理函数
// ---------------------------------------------------------------------------

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	items, err := h.svc.List(r.Context(), viewer(r), q.Get("area"), q.Get("day"), limit, offset)
	if err != nil {
		writeServiceError(w, "读牌局列表失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"games": toSummariesJSON(items)})
}

func (h *handler) detail(w http.ResponseWriter, r *http.Request) {
	detail, err := h.svc.Get(r.Context(), r.PathValue("code"), viewer(r))
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	var body struct {
		StartsAt int64  `json:"starts_at"`
		Area     string `json:"area"`
		Venue    string `json:"venue"`
		Kind     string `json:"kind"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}

	in := CreateInput{
		StartsAt: time.Unix(body.StartsAt, 0),
		Area:     body.Area,
		Venue:    body.Venue,
		Kind:     body.Kind,
		Note:     body.Note,
	}
	// 前端每次「提交」都带一个新键；同一个键重复提交只会执行一次（§11.4）。
	detail, err := h.svc.Create(r.Context(), userID, in, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}

	// 局头在两台手机上同时改时，版本号不一致就返回 409 和最新数据（§11.3）。
	// ETag 按 HTTP 规范是带引号的（"7"），两种都收。
	ifVersion, err := strconv.ParseInt(strings.Trim(r.Header.Get("If-Match"), `"`), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "缺少 If-Match 版本号")
		return
	}

	var body struct {
		StartsAt *int64  `json:"starts_at"`
		Area     *string `json:"area"`
		Venue    *string `json:"venue"`
		Kind     *string `json:"kind"`
		Note     *string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}

	patch := Patch{Area: body.Area, Venue: body.Venue, Kind: body.Kind, Note: body.Note}
	if body.StartsAt != nil {
		t := time.Unix(*body.StartsAt, 0)
		patch.StartsAt = &t
	}

	detail, err := h.svc.Update(r.Context(), r.PathValue("code"), userID, patch, ifVersion)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *handler) join(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.Join(r.Context(), r.PathValue("code"), userID)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *handler) leave(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	detail, err := h.svc.Leave(r.Context(), r.PathValue("code"), userID)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	if err := h.svc.Cancel(r.Context(), r.PathValue("code"), userID); err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": StatusCancelled})
}

func (h *handler) kick(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.UserID == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 user_id")
		return
	}

	detail, err := h.svc.Kick(r.Context(), r.PathValue("code"), userID, body.UserID)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toDetailJSON(detail))
}

// invite 局头邀请牌友（#115）。返回这次新邀请的人和跳过的人（已在局里、已邀请过、被移出过）。
func (h *handler) invite(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUser(w, r)
	if !ok {
		return
	}
	var body struct {
		UserIDs []string `json:"user_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 user_ids")
		return
	}
	res, err := h.svc.Invite(r.Context(), r.PathValue("code"), userID, body.UserIDs)
	if err != nil {
		writeGameError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invited": nonNil(res.Invited), "skipped": nonNil(res.Skipped)})
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------------------
// JSON 与错误
// ---------------------------------------------------------------------------

type userRefJSON struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
}

type gameJSON struct {
	ID        string `json:"id"`
	ShortCode string `json:"short_code"`
	StartsAt  int64  `json:"starts_at"`
	Area      string `json:"area"`
	Venue     string `json:"venue"`
	Kind      string `json:"kind"`
	Note      string `json:"note,omitempty"`
	Status    string `json:"status"`
	Version   int64  `json:"version"`
	Capacity  int    `json:"capacity"`
	// RescheduledAt 是局头最近一次改时间或地点的时刻（Unix 秒）；没改过就没有这一项。
	RescheduledAt int64 `json:"rescheduled_at,omitempty"`
}

type seatJSON struct {
	SeatNo int         `json:"seat_no"`
	User   userRefJSON `json:"user"`
}

type waitJSON struct {
	Position int         `json:"position"`
	User     userRefJSON `json:"user"`
}

type meJSON struct {
	Role      string       `json:"role"`
	SeatNo    int          `json:"seat_no,omitempty"`
	Position  int          `json:"position,omitempty"`
	InvitedBy *userRefJSON `json:"invited_by,omitempty"` // #115：谁邀请了我，只有被邀请的人看得到
}

func toDetailJSON(d *Detail) map[string]any {
	seats := make([]seatJSON, 0, len(d.Seats))
	// 只返回白名单字段：id 和称呼（§10.4）。
	for _, s := range d.Seats {
		seats = append(seats, seatJSON{
			SeatNo: s.SeatNo,
			User:   userRefJSON{ID: s.UserID, Nickname: s.Nickname},
		})
	}
	me := meJSON{Role: d.Me, SeatNo: d.MySeatNo, Position: d.MyPosition}
	if d.MyInviter != nil {
		me.InvitedBy = &userRefJSON{ID: d.MyInviter.UserID, Nickname: d.MyInviter.Nickname}
	}
	waiting := make([]waitJSON, 0, len(d.Waitlist))
	for _, w := range d.Waitlist {
		waiting = append(waiting, waitJSON{
			Position: w.Position,
			User:     userRefJSON{ID: w.UserID, Nickname: w.Nickname},
		})
	}

	return map[string]any{
		"game": gameJSON{
			ID: d.Game.ID, ShortCode: d.Game.ShortCode, StartsAt: d.Game.StartsAt.Unix(),
			Area: d.Game.Area, Venue: d.Game.Venue, Kind: d.Game.Kind, Note: d.Game.Note,
			Status: d.Game.Status, Version: d.Game.Version, Capacity: Capacity,
			RescheduledAt: unixSec(d.Game.RescheduledAt),
		},
		"host":     userRefJSON{ID: d.Game.HostID, Nickname: d.HostName},
		"seats":    seats,
		"waitlist": waiting,
		"missing":  d.Missing,
		"me":       me,
	}
}

func toSummariesJSON(items []Summary) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	for _, s := range items {
		seats := make([]seatJSON, 0, len(s.Seats))
		for _, seat := range s.Seats {
			seats = append(seats, seatJSON{
				SeatNo: seat.SeatNo,
				User:   userRefJSON{ID: seat.UserID, Nickname: seat.Nickname},
			})
		}
		item := map[string]any{
			"short_code": s.Game.ShortCode,
			"starts_at":  s.Game.StartsAt.Unix(),
			"area":       s.Game.Area,
			"venue":      s.Game.Venue,
			"kind":       s.Game.Kind,
			"status":     s.Game.Status,
			"seat_list":  seats,
			"seats":      len(seats),
			"missing":    s.Missing,
			"host":       userRefJSON{ID: s.Game.HostID, Nickname: s.HostName},
		}
		if at := unixSec(s.Game.RescheduledAt); at != 0 {
			item["rescheduled_at"] = at
		}
		// 按身份算的字段只在 GET /api/games 里有；首页事件是广播，MyRole 为空，不带。
		if s.MyRole != "" {
			item["my_role"] = s.MyRole
			item["waitlist_pos"] = s.WaitlistPos
		}
		out = append(out, item)
	}
	return out
}

// unixSec 零值时间返回 0，其余返回 Unix 秒。
func unixSec(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// writeGameError 把业务错误映射成状态码（§6）。
func writeGameError(w http.ResponseWriter, err error) {
	var (
		conflict      *VersionConflict
		roundConflict *RoundConflict
		roundChanged  *RoundChanged
	)
	switch {
	case errors.As(err, &conflict):
		// 409 里带上最新的牌局，前端据此提示「局已被修改，请确认」。
		body := toDetailJSON(&conflict.Latest)
		body["error"] = map[string]string{"code": "version_conflict", "message": err.Error()}
		writeJSON(w, http.StatusConflict, body)
	case errors.As(err, &roundConflict):
		// S12：两人同时记同一局。带上已经记下的那一局和最新的计分。
		body := toScoreJSON(&roundConflict.Latest)
		body["round"] = scoreRoundJSON(roundConflict.Existing)
		body["error"] = map[string]string{"code": "round_taken", "message": err.Error()}
		writeJSON(w, http.StatusConflict, body)
	case errors.As(err, &roundChanged):
		body := toScoreJSON(&roundChanged.Latest)
		body["error"] = map[string]string{"code": "round_changed", "message": err.Error()}
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, ErrNotSeated):
		writeError(w, http.StatusForbidden, "not_seated", err.Error())
	case errors.Is(err, ErrNotFull):
		writeError(w, http.StatusConflict, "not_full", err.Error())
	case errors.Is(err, ErrScoreTooEarly):
		writeError(w, http.StatusConflict, "too_early", err.Error())
	case errors.Is(err, ErrScoreNotStarted):
		writeError(w, http.StatusConflict, "score_not_started", err.Error())
	case errors.Is(err, ErrRoundNotFound):
		writeError(w, http.StatusNotFound, "round_not_found", err.Error())
	case errors.Is(err, ErrIdemKeyReused):
		writeError(w, http.StatusUnprocessableEntity, "idempotency_key_reused", err.Error())
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "game_not_found", "牌局不存在")
	case errors.Is(err, ErrClosed):
		writeError(w, http.StatusConflict, "game_closed", "这一局已经结束或取消了")
	case errors.Is(err, ErrBanned):
		writeError(w, http.StatusForbidden, "banned", "你被移出过这一局")
	case errors.Is(err, ErrNotHost):
		writeError(w, http.StatusForbidden, "not_host", "只有局头能做这件事")
	case errors.Is(err, ErrStarted):
		writeError(w, http.StatusConflict, "game_started", "这一局已经开始了")
	case errors.Is(err, friends.ErrNotFriend):
		writeError(w, http.StatusNotFound, "not_friend", "只能邀请一起打过局的牌友")
	case errors.Is(err, ErrNotInGame):
		writeError(w, http.StatusNotFound, "not_in_game", "这个人不在这一局里")
	case errors.Is(err, ErrHostCantKickSelf):
		writeError(w, http.StatusBadRequest, "kick_self", "局头不能移出自己，退出会触发转让")
	case errors.Is(err, moderate.ErrSensitive):
		// 422：请求本身没毛病，是内容不能收（§14）。带上字段名，前端把提示放到
		// 对应的输入框下面，并且不清空用户已经写好的内容（原型 X1）。
		var hit *moderate.Hit
		errors.As(err, &hit)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{
				"code":    "sensitive_word",
				"message": "请勿涉及金钱：这里只约纯娱乐局",
				"field":   hit.Field,
			},
		})
	case errors.Is(err, secheck.ErrUnsafe):
		// 微信内容安全检测判为 risky（#146）：同样是 422 sensitive_word + field，前端不用区分来源。
		var unsafe *secheck.UnsafeError
		errors.As(err, &unsafe)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{"code": "sensitive_word", "message": secheck.Message, "field": unsafe.Field},
		})
	case errors.Is(err, ErrQuota):
		writeError(w, http.StatusConflict, "quota_exceeded", err.Error())
	case errors.Is(err, ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "操作太频繁，请稍后再试")
	case errors.Is(err, ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
	default:
		writeServiceError(w, "牌局操作失败", err)
	}
}

func writeServiceError(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, store.ErrBusy) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "系统繁忙，请稍后再试")
		return
	}
	slog.Error(what, "err", err)
	writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时不可用，请稍后再试")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("写响应失败", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{"code": code, "message": message},
	})
}
