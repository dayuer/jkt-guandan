package identity

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dayuer/jkt-guandan/server/internal/secheck"
)

// 小游戏的两个接口（#84）。都是 route.Anonymous：请求里还没有凭证（小游戏没有 cookie，登录前也没有 Bearer），
// 身份只凭微信 code 认，所以不读 cookie，也不会给浏览器写 cookie。被封禁的用户在 service 里拦。

// wxLogin 是 POST /api/wx/login {code, nickname?}。
//
//	200 {"user":{…},"device_token":"…","device_id":"…"}  认出或新建了身份
//	200 {"need_nickname":true}                         没关联过，也没给称呼
func (h *handler) wxLogin(w http.ResponseWriter, r *http.Request) {
	if !h.svc.WxEnabled() {
		writeError(w, http.StatusServiceUnavailable, "wx_disabled", "小游戏登录未开启")
		return
	}
	var body struct {
		Code     string `json:"code"`
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 code")
		return
	}
	ip := clientIP(r)
	if !h.svc.AllowWxLogin(ip) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "登录太频繁，请稍后再试")
		return
	}

	res, err := h.svc.WxLogin(r.Context(), body.Code, body.Nickname, func() bool { return h.svc.AllowCreate(ip) })
	if err != nil {
		writeWxError(w, err)
		return
	}
	if res.NeedNickname {
		writeJSON(w, http.StatusOK, map[string]any{"need_nickname": true})
		return
	}
	writeWxLogin(w, res.Identity, res.DeviceToken)
}

// wxLink 是 POST /api/wx/link {code, wx_code, confirm?}：用 H5 签发的 6 位认领码把微信身份关联到那个用户（#84 §B2）。
func (h *handler) wxLink(w http.ResponseWriter, r *http.Request) {
	if !h.svc.WxEnabled() {
		writeError(w, http.StatusServiceUnavailable, "wx_disabled", "小游戏登录未开启")
		return
	}
	var body struct {
		Code    string `json:"code"`
		WxCode  string `json:"wx_code"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" || body.WxCode == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "需要 code 和 wx_code")
		return
	}
	// 和 POST /api/claim/redeem 凭码兑换共用每 IP 的限流（§12.3：每分钟 10 次）。
	if !h.svc.AllowCodeRedeem(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "rate_limited", "尝试太频繁，请稍后再试")
		return
	}

	ident, token, err := h.svc.WxLink(r.Context(), body.Code, body.WxCode, netPrefix(clientIP(r)), body.Confirm)
	var conflict *ClaimConflictError
	switch {
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": ErrorBody{
				Code:    "claim_conflict",
				Message: "这个微信已经关联了另一个身份，需要确认",
			},
			"other_nickname": conflict.OtherNickname,
		})
		return
	case errors.Is(err, ErrClaimInvalid):
		writeError(w, http.StatusBadRequest, "claim_invalid", "兑换失败")
		return
	case err != nil:
		writeWxError(w, err)
		return
	}
	writeWxLogin(w, ident, token)
}

// writeWxLogin 写登录成功的响应。device_token 在响应体里：小游戏没有 cookie，这是 §10.3 的唯一例外。
// 这里不 Set-Cookie。
func writeWxLogin(w http.ResponseWriter, ident Identity, token string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"user":         toUserJSON(ident.User),
		"device_token": token,
		"device_id":    ident.Device.ID,
	})
}

// writeUnsafeError 处理内容安全检测没过：422 + field，和地点、备注的敏感词同一个错误码（#146），
// 小游戏的表单和称呼弹层已经按它显示提示。不是这个错就返回 false，交给调用处继续判。
//
// 「新建身份」（writeWxError）和「改称呼」（putNickname）共用，免得两边的错误体走样。
func writeUnsafeError(w http.ResponseWriter, err error) bool {
	if !errors.Is(err, secheck.ErrUnsafe) {
		return false
	}
	var unsafe *secheck.UnsafeError
	errors.As(err, &unsafe)
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": map[string]any{"code": "sensitive_word", "message": secheck.Message, "field": unsafe.Field},
	})
	return true
}

func writeWxError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrWxDisabled):
		writeError(w, http.StatusServiceUnavailable, "wx_disabled", "小游戏登录未开启")
	case errors.Is(err, ErrNicknameInvalid):
		writeError(w, http.StatusBadRequest, "nickname_invalid", "称呼需要 1–12 个字符")
	case errors.Is(err, secheck.ErrUnsafe):
		writeUnsafeError(w, err)
	case errors.Is(err, ErrWxCodeInvalid):
		writeError(w, http.StatusBadRequest, "wx_code_invalid", "微信登录凭证无效，请重新登录")
	case errors.Is(err, ErrWxUnavailable):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "wx_unavailable", "微信暂时连不上，请稍后再试")
	case errors.Is(err, ErrWxRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "创建身份太频繁，请稍后再试")
	case errors.Is(err, ErrBanned):
		writeError(w, http.StatusForbidden, "banned", "这个账号已被停用")
	default:
		writeServiceError(w, "小游戏登录失败", err)
	}
}
