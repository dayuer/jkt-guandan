package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/route"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// Routes 是身份包的全部路由（§6）。httpapi 按这张表注册、统一识别身份和授权（#41）；
// 授权矩阵测试也遍历它。svc 为 nil 时只能用来列路由，不能处理请求。
func Routes(svc *Service) []route.Route {
	h := &handler{svc: svc}
	return []route.Route{
		// 访客填称呼就创建身份；已有身份的人再次提交是幂等的。
		{Method: http.MethodPost, Pattern: "/api/me", Access: route.Public, Handler: http.HandlerFunc(h.createMe)},
		{Method: http.MethodGet, Pattern: "/api/me", Access: route.User, Handler: http.HandlerFunc(h.getMe)},
		{Method: http.MethodDelete, Pattern: "/api/me/devices/{id}", Access: route.User, Handler: http.HandlerFunc(h.signOutDevice)},
		// 注销：删掉这个人自己的数据（§14）。访问方式与 GET/POST /api/me 同一个路径。
		{Method: http.MethodDelete, Pattern: "/api/me", Access: route.User, Handler: http.HandlerFunc(h.deleteMe)},
		// 绑定 WhatsApp（#116）：号码只给自己看。
		{Method: http.MethodPut, Pattern: "/api/me/wa", Access: route.User, Handler: http.HandlerFunc(h.putWA)},
		{Method: http.MethodDelete, Pattern: "/api/me/wa", Access: route.User, Handler: http.HandlerFunc(h.deleteWA)},
		{Method: http.MethodPost, Pattern: "/api/claim", Access: route.User, Handler: http.HandlerFunc(h.issueClaim)},
		// 两种身份都能进来：有身份的人凭 token 兑换时会先拿到 409，确认后才切换。
		// 「凭码兑换只给没有身份的设备用」是业务规则，在 handler 里判。
		{Method: http.MethodPost, Pattern: "/api/claim/redeem", Access: route.Public, Handler: http.HandlerFunc(h.redeemClaim)},
	}
}

type handler struct{ svc *Service }

// requireIdentity 取出中间件放进 ctx 的身份。route.User 的路由这里一定非 nil；
// 多判一次是为了万一以后有人改了路由表。
func requireIdentity(w http.ResponseWriter, r *http.Request) (*Identity, bool) {
	ident := FromContext(r.Context())
	if ident == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "还没有身份")
		return nil, false
	}
	return ident, true
}

// createMe 处理「首次填称呼」（§5.2）。
func (h *handler) createMe(w http.ResponseWriter, r *http.Request) {
	current := FromContext(r.Context())

	var body struct {
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}

	// 只有真的要新建身份时才计入配额（§12.3：每 IP 每小时 20 次）。
	if current == nil && !h.svc.AllowCreate(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "创建身份太频繁，请稍后再试")
		return
	}

	ident, deviceToken, err := h.svc.CreateOrGet(r.Context(), current, body.Nickname, r.UserAgent())
	switch {
	case errors.Is(err, ErrNicknameInvalid):
		writeError(w, http.StatusBadRequest, "nickname_invalid", "称呼需要 1–12 个字符")
		return
	case err != nil:
		writeServiceError(w, "创建身份失败", err)
		return
	}

	if deviceToken != "" {
		h.svc.Cookies().Set(w, deviceToken, CookieMaxAge)
	}

	// 明文 token 只走 Set-Cookie，不放进响应体：cookie 是 HttpOnly 的，
	// 一旦在响应体里回一次，JS 就能读到，HttpOnly 也就没意义了（§10.3）。
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(ident.User)})
}

// getMe 返回当前身份和本用户的设备列表（§10.3 的 10「我的」）。
func (h *handler) getMe(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok {
		return
	}

	devices, err := h.svc.Devices(r.Context(), current.User.ID)
	if err != nil {
		writeServiceError(w, "读取设备列表失败", err)
		return
	}
	phone, err := h.svc.WAPhone(r.Context(), current.User.ID)
	if err != nil {
		writeServiceError(w, "读取 WhatsApp 号码失败", err)
		return
	}

	body := map[string]any{
		"user": toUserJSON(current.User),
		// 当前这台设备的 id：前端「不是我」要拿它去调用退出接口（#36）。
		"device_id": current.Device.ID,
		"devices":   toDevicesJSON(devices),
	}
	// 绑定的 WhatsApp 号码（#116，E.164）。没绑定就没有这个字段。只在这里给自己看。
	if phone != "" {
		body["wa_phone"] = phone
	}
	writeJSON(w, http.StatusOK, body)
}

// signOutDevice 退出另一台设备（§10.3）。只能退自己的。
func (h *handler) signOutDevice(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok {
		return
	}

	deviceID := r.PathValue("id")
	err := h.svc.SignOutDevice(r.Context(), current.User.ID, deviceID)
	switch {
	case errors.Is(err, ErrDeviceNotFound):
		writeError(w, http.StatusNotFound, "device_not_found", "没有这台设备")
		return
	case err != nil:
		writeServiceError(w, "退出设备失败", err)
		return
	}

	// 退掉的如果是当前这台，顺手把 cookie 清掉。
	if deviceID == current.Device.ID {
		h.svc.Cookies().Clear(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMe 注销：删掉称呼、WhatsApp、设备和推送订阅，历史局里的座位显示成
// 「已注销牌友」（§14）。注销之后这台设备就没有身份了，所以要把 cookie 清掉。
func (h *handler) deleteMe(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok {
		return
	}

	if err := h.svc.DeleteMe(r.Context(), current); err != nil {
		writeServiceError(w, "注销失败", err)
		return
	}

	h.svc.Cookies().Clear(w)
	w.WriteHeader(http.StatusNoContent)
}

// issueClaim 签发交接令牌和认领码（§5.2 修订）。
func (h *handler) issueClaim(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok {
		return
	}

	// 每个用户每小时最多 5 次，免得有效码的数量被推高、撞库变容易（§5.2 修订）。
	if !h.svc.AllowClaim(current.User.ID) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "签发交接令牌太频繁，请稍后再试")
		return
	}

	token, code, expiresAt, err := h.svc.IssueClaim(r.Context(), current.User.ID, netPrefix(clientIP(r)))
	if err != nil {
		writeServiceError(w, "签发交接令牌失败", err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"claim_token": token,
		"code":        code,
		"expires_at":  expiresAt.Unix(),
	})
}

// redeemClaim 兑换身份：凭 token 或凭认领码（§5.2 修订、§10.3）。
//
// 只有 POST 会消耗 token。抓链接预览的机器人只发 GET，所以不会把一次性 token 用掉。
func (h *handler) redeemClaim(w http.ResponseWriter, r *http.Request) {
	current := FromContext(r.Context())

	var body struct {
		Token string `json:"token"`
		Code  string `json:"code"`
		// Confirm 表示用户在确认屏上点了「切换成 X」。没有它，服务端不会改动任何东西。
		Confirm bool `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}

	switch {
	case body.Token != "":
		var currentDeviceID, currentUserID string
		if current != nil {
			currentDeviceID, currentUserID = current.Device.ID, current.User.ID
		}

		ident, deviceToken, err := h.svc.RedeemByToken(
			r.Context(), body.Token, r.UserAgent(), currentDeviceID, currentUserID, body.Confirm)

		var conflict *ClaimConflictError
		if errors.As(err, &conflict) {
			// 409：告诉前端这个链接属于谁，让用户自己决定。token 没有被消耗。
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": ErrorBody{
					Code:    "claim_conflict",
					Message: "这个链接属于另一个身份，需要确认",
				},
				"other_nickname": conflict.OtherNickname,
			})
			return
		}
		if err != nil {
			writeRedeemError(w, err)
			return
		}

		// 只有新挂了设备才换 cookie；「切换已有设备」那条路沿用原来的 cookie。
		if deviceToken != "" {
			h.svc.Cookies().Set(w, deviceToken, CookieMaxAge)
		}
		writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(ident.User)})

	case body.Code != "":
		// 认领码只给还没有身份的设备用（§5.2）。已经有身份时也返回同一个错误，
		// 不告诉对方这个码到底存不存在（§10.3）。
		if current != nil {
			writeError(w, http.StatusBadRequest, "claim_invalid", "兑换失败")
			return
		}
		if !h.svc.AllowCodeRedeem(clientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "尝试太频繁，请稍后再试")
			return
		}

		ident, deviceToken, err := h.svc.RedeemByCode(
			r.Context(), body.Code, netPrefix(clientIP(r)), r.UserAgent())
		if err != nil {
			writeRedeemError(w, err)
			return
		}

		h.svc.Cookies().Set(w, deviceToken, CookieMaxAge)
		writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(ident.User)})

	default:
		writeError(w, http.StatusBadRequest, "bad_request", "需要 token 或 code")
	}
}

// writeRedeemError 把兑换失败统一成一个错误，不透露失败原因（§10.3）。
func writeRedeemError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrClaimInvalid) {
		writeError(w, http.StatusBadRequest, "claim_invalid", "兑换失败")
		return
	}
	writeServiceError(w, "兑换失败", err)
}

// writeServiceError 把底层错误映射成状态码：等写锁超时是 503 busy（§11.1）。
func writeServiceError(w http.ResponseWriter, what string, err error) {
	if errors.Is(err, store.ErrBusy) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "busy", "系统繁忙，请稍后再试")
		return
	}
	slog.Error(what, "err", err)
	writeError(w, http.StatusServiceUnavailable, "unavailable", "暂时不可用，请稍后再试")
}

// ErrorBody 是统一的错误体：{"error":{"code":"…","message":"…"}}（§6）。
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type userJSON struct {
	ID        string `json:"id"`
	Nickname  string `json:"nickname"`
	CreatedAt int64  `json:"created_at"`
}

type deviceJSON struct {
	ID        string `json:"id"`
	UserAgent string `json:"user_agent,omitempty"`
	LastSeen  int64  `json:"last_seen"`
}

// toUserJSON 只返回白名单字段（§10.4）：不带 wa_phone、设备信息、merged_into。
func toUserJSON(u User) userJSON {
	return userJSON{ID: u.ID, Nickname: u.Nickname, CreatedAt: u.CreatedAt.Unix()}
}

func toDevicesJSON(devices []Device) []deviceJSON {
	out := make([]deviceJSON, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceJSON{
			ID:        d.ID,
			UserAgent: d.UserAgent,
			LastSeen:  d.LastSeen.Unix(),
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("写响应失败", "err", err)
	}
}

// writeError 用统一的错误格式：{"error":{"code":"…","message":"…"}}（§6）。
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": ErrorBody{Code: code, Message: message},
	})
}

// clientIP 取对端 IP。按 IP 的限流只在印尼这种大量用运营商级 NAT 的网络里当粗筛子用（§12.3）。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
