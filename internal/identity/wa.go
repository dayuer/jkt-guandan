package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/dayuer/jkt-guandan/internal/store"
)

// 绑定 WhatsApp（#116，「我的」页 G2）。号码只给自己看：GET /api/me 里带，牌友列表、牌局详情里都没有（#108 的决定）。
// 注销时清空（delete.go 已有）。

// ErrPhoneInvalid 表示号码不是 E.164。
var ErrPhoneInvalid = errors.New("号码要带国家区号，比如 +62 812 3456 7890")

var e164 = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

// NormalizeWAPhone 把用户输入的号码整理成 E.164（+ 加 8–15 位数字）。允许空格、横线、点、括号这些分隔符，
// 开头的 00 当作 +。不猜国家区号：「0812…」这种本地写法不合法，让用户自己写成 +62。
func NormalizeWAPhone(raw string) (string, error) {
	s := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '.', '(', ')', ' ':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	if strings.HasPrefix(s, "00") {
		s = "+" + s[2:]
	}
	if !e164.MatchString(s) {
		return "", ErrPhoneInvalid
	}
	return s, nil
}

// WAPhone 返回这个用户绑定的号码；没绑定是空串。
func (s *Service) WAPhone(ctx context.Context, userID string) (string, error) {
	var phone sql.NullString
	err := s.st.Read().QueryRowContext(ctx, `SELECT wa_phone FROM users WHERE id = ?`, userID).Scan(&phone)
	if err != nil {
		return "", err
	}
	return phone.String, nil
}

// SetWAPhone 绑定或换号码；phone 为空表示解绑。
func (s *Service) SetWAPhone(ctx context.Context, userID, phone string) error {
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET wa_phone = ? WHERE id = ?`, nullIfEmpty(phone), userID)
		return err
	})
}

// putWA 绑定或换号码：{"phone":"+62 812-3456-7890"} → 200 {"wa_phone":"+6281234567890"}。
func (h *handler) putWA(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok || !h.allowWA(w, current.User.ID) {
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}
	phone, err := NormalizeWAPhone(body.Phone)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", err.Error())
		return
	}
	if err := h.svc.SetWAPhone(r.Context(), current.User.ID, phone); err != nil {
		writeServiceError(w, "绑定 WhatsApp 失败", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"wa_phone": phone})
}

// deleteWA 解绑，幂等：没绑定也是 204。
func (h *handler) deleteWA(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok || !h.allowWA(w, current.User.ID) {
		return
	}
	if err := h.svc.SetWAPhone(r.Context(), current.User.ID, ""); err != nil {
		writeServiceError(w, "解绑 WhatsApp 失败", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// allowWA 限制改号码的频率：每个用户每小时 10 次（绑定和解绑都算），够正常改错几次。
func (h *handler) allowWA(w http.ResponseWriter, userID string) bool {
	if h.svc.waLimiter.Allow(userID) {
		return true
	}
	writeError(w, http.StatusTooManyRequests, "rate_limited", "改得太频繁了，请稍后再试")
	return false
}
