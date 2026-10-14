package identity

import (
	"context"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// DeletedNickname 是注销之后座位上显示的名字（§14）。
//
// 直接把称呼改掉，而不是留个空值让每个查询自己兜底：座位、候补、邀请列表都从
// users 里取称呼，改一处就够了，也不会出现「有的地方显示空白」。
const DeletedNickname = "已注销牌友"

// GameCleaner 是注销时要用到的牌局侧能力（由 games.Service 实现）。
//
// 接口定义在这里，是为了不让 identity 依赖 games——依赖方向是反的（games 用
// identity 做鉴权）。main 里把两者接起来。
type GameCleaner interface {
	// PurgeUser 让这个人在所有还没结束的局里按正常退出离开（候补补位、局头转让、
	// 通知座位上的人），返回处理了几局。已结束/已取消的局不动：那是历史记录，
	// 座位要留着，人数统计才不出错（§14）。
	PurgeUser(ctx context.Context, tx *store.Tx, userID string, now time.Time) (int, error)
}

// SetGameCleaner 接上牌局侧的清理（§14）。启动时调用一次；不接则注销只清身份自己的数据。
func (s *Service) SetGameCleaner(g GameCleaner) { s.games = g }

// deleteStatements 是要按「关于这个人的个人数据」清掉的表（§14：删除称呼、WhatsApp
// 号码、设备和推送订阅；顺带把和身份绑在一起的一次性数据也清掉）。
//
// 用 (?, ?) 的语句都要把 userID 传两次。
var deleteStatements = []struct {
	what string
	sql  string
}{
	// 推送订阅挂在设备上，所以要排在删设备前面。
	{"推送订阅", `DELETE FROM push_subscriptions
		WHERE device_id IN (SELECT id FROM devices WHERE user_id = ?)`},
	{"设备", `DELETE FROM devices WHERE user_id = ?`},
	{"认领令牌", `DELETE FROM claim_tokens WHERE user_id = ?`},
	{"幂等键", `DELETE FROM idempotency_keys WHERE user_id = ?`},
	{"邀请", `DELETE FROM invites WHERE user_id = ? OR invited_by = ?`},
	// 星标是双向的：他标了谁、谁标了他，都是关于这个人的数据。
	{"星标", `DELETE FROM follows WHERE user_id = ? OR target_id = ?`},
	// 注销之后没有保留的意义。
	{"被移出记录", `DELETE FROM game_bans WHERE user_id = ?`},
	// 他都不来了，别再推给他。
	{"未发出的提醒", `DELETE FROM outbox WHERE user_id = ?`},
	// 埋点留着做汇总，但不能再指回这个人（和 store.DeleteUserData 一致，§14）。
	{"埋点", `UPDATE events SET user_id = NULL WHERE user_id = ?`},
}

// DeleteMe 注销当前身份（§14、`DELETE /api/me`）。
//
// 一次事务里做完所有事：
//   - 还没结束的局：按正常退出离开（这个人在那些局里等于「不来了」）；
//   - 已结束的局：座位行保留，称呼变成「已注销牌友」；
//   - 个人数据：设备、推送订阅、认领令牌、幂等键、邀请、星标、被移出记录、未发出的提醒，埋点去掉 user_id；
//   - 用户行保留（座位还引用着它），但称呼抹掉、WhatsApp 清空、写上 deleted_at。
//
// 重复调用是安全的：已经注销过就直接返回（前端可能重试）。
func (s *Service) DeleteMe(ctx context.Context, ident *Identity) error {
	if ident == nil {
		return ErrNotFound
	}
	now := s.clk.Now()
	userID := ident.User.ID

	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		var deletedAt *int64
		if err := tx.QueryRowContext(ctx,
			`SELECT deleted_at FROM users WHERE id = ?`, userID).Scan(&deletedAt); err != nil {
			return err
		}
		if deletedAt != nil {
			return nil // 已经注销过了
		}

		// 先把这个人从还没结束的局里挪出去：候补要补位、局头要转让。
		if s.games != nil {
			if _, err := s.games.PurgeUser(ctx, tx, userID, now); err != nil {
				return err
			}
		}

		// 要断开的实时连接：设备行删掉之前先把 id 记下来。
		deviceIDs, err := deviceIDsOf(ctx, tx, userID)
		if err != nil {
			return err
		}

		for _, stmt := range deleteStatements {
			args := []any{userID}
			if hasTwoPlaceholders(stmt.sql) {
				args = append(args, userID)
			}
			if _, err := tx.ExecContext(ctx, stmt.sql, args...); err != nil {
				return err
			}
		}

		// 用户行留着（座位引用着它），但个人数据抹掉。
		if _, err := tx.ExecContext(ctx, `
			UPDATE users SET nickname = ?, wa_phone = NULL, deleted_at = ?
			 WHERE id = ?`, DeletedNickname, now.Unix(), userID); err != nil {
			return err
		}

		// 这些设备上已经建立的实时连接立刻断开；不这样做要等下一次心跳（25 秒）才断。
		for _, id := range deviceIDs {
			s.kickDeviceAfterCommit(tx, id)
		}
		return nil
	})
}

func deviceIDsOf(ctx context.Context, tx *store.Tx, userID string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM devices WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func hasTwoPlaceholders(sql string) bool {
	n := 0
	for _, r := range sql {
		if r == '?' {
			n++
			if n > 1 {
				return true
			}
		}
	}
	return false
}
