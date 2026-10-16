package identity

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 改称呼（#210）。用户原话：「名称，我现在不能改，应该允许我修改」。
//
// 校验、内容安全检测都和小游戏新建身份**走同一套**：长度、保留名、去空白和零宽字符都在
// NormalizeNickname 里（#221 起新建身份也走它），检测用 secheck 的场景 1（资料）——
// 只是从「按 openid 查」换成「按 user_id 查」（CheckUser），因为这时候人已经在库里了。
//
// 称呼不存在快照：座位、候补、牌友列表、每局记录的「谁记的」都是按 user id 关联、
// 读的时候 JOIN users（见 #210 的说明），所以改完一处，各处都跟着变。

// ErrUserDeleted 表示这个人已经注销了。
var ErrUserDeleted = errors.New("这个身份已经注销")

// ChangeNickname 改称呼，返回改完之后的用户信息（接口层直接回给前端，省一次 GET）。
//
// 顺序和 WxLogin 一致：先校验，再送微信检测，最后才开写事务 —— 网络请求不占唯一的写连接。
func (s *Service) ChangeNickname(ctx context.Context, userID, raw string) (User, error) {
	clean, err := NormalizeNickname(raw)
	if err != nil {
		return User{}, err
	}
	// 长度和保留名（「已注销牌友」）都在 NormalizeNickname 里挡，和新建身份同一支。
	// 小游戏用户才查（H5 测试用户没有 openid，Guard 里会跳过）；微信出错、超时都放行。
	if err := s.sec.CheckUser(ctx, userID, secheck.SceneProfile, secheck.Field{Name: "nickname", Text: clean}); err != nil {
		return User{}, err
	}

	var u User
	err = s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		var createdAt int64
		var deleted sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`SELECT created_at, deleted_at FROM users WHERE id = ?`, userID).Scan(&createdAt, &deleted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		case deleted.Valid:
			// 注销会把设备和微信关联都删掉，正常到不了这里；万一哪台设备还留着 cookie，也不能把名字改回去。
			return ErrUserDeleted
		}

		// `AND deleted_at IS NULL` 是对上面那次 SELECT 的复核：这两条语句之间这个人可能刚注销。
		res, err := tx.ExecContext(ctx,
			`UPDATE users SET nickname = ? WHERE id = ? AND deleted_at IS NULL`, clean, userID)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrUserDeleted
		}

		u = User{ID: userID, Nickname: clean, CreatedAt: time.Unix(createdAt, 0)}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// putNickname 是 PUT /api/me/nickname：{"nickname":"新名字"} → 200 {"user":{…}}。
func (h *handler) putNickname(w http.ResponseWriter, r *http.Request) {
	current, ok := requireIdentity(w, r)
	if !ok || !h.allowNickname(w, current.User.ID) {
		return
	}

	var body struct {
		Nickname string `json:"nickname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", "请求体不是合法的 JSON")
		return
	}

	u, err := h.svc.ChangeNickname(r.Context(), current.User.ID, body.Nickname)
	switch {
	case writeUnsafeError(w, err):
		return
	case errors.Is(err, ErrNicknameInvalid):
		writeError(w, http.StatusBadRequest, "nickname_invalid", "称呼需要 1–12 个字符")
		return
	case errors.Is(err, ErrNicknameReserved):
		writeError(w, http.StatusBadRequest, "nickname_reserved", "这个称呼不能用")
		return
	case errors.Is(err, ErrUserDeleted):
		writeError(w, http.StatusForbidden, "user_deleted", "这个身份已经注销")
		return
	case err != nil:
		writeServiceError(w, "改称呼失败", err)
		return
	}

	// 称呼不是敏感信息，但这是「我的」页的数据，别让中间层缓存住。
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"user": toUserJSON(u)})
}

// allowNickname 限制改称呼的频率：每个用户每小时 10 次。
//
// 和改 WhatsApp 号码（allowWA）同一个量级：正常改错几次够用，又能挡住有人拿它刷内容安全检测。
func (h *handler) allowNickname(w http.ResponseWriter, userID string) bool {
	if h.svc.nickLimiter.Allow(userID) {
		return true
	}
	writeError(w, http.StatusTooManyRequests, "rate_limited", "改得太频繁了，请稍后再试")
	return false
}
