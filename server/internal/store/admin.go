package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound 表示管理命令指定的对象不存在。
var ErrNotFound = errors.New("不存在")

// DeletedNickname 是注销后座位记录上显示的称呼（§14）。
const DeletedNickname = "已注销牌友"

// Ban 封禁一个用户或设备（§13.6 guandan admin ban <user|device>）。
// 先按用户 id 找，找不到再按设备 id 找，返回实际封禁的是 "user" 还是 "device"。
// 拦截在 httpapi 唯一的身份中间件里（#41）：识别身份的路由解析出身份后调用 IsBanned，命中返回 403。
func (s *Store) Ban(ctx context.Context, id string, now time.Time) (kind string, err error) {
	err = s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		for _, k := range []string{"user", "device"} {
			table := map[string]string{"user": "users", "device": "devices"}[k]
			r, err := tx.ExecContext(ctx,
				`UPDATE `+table+` SET banned_at = COALESCE(banned_at, ?) WHERE id = ?`, now.Unix(), id)
			if err != nil {
				return err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				kind = k
				if k == "user" {
					// 被合并过的旧 id：顺着 merged_into 把实际在用的那个身份也封掉（auditor 在 #34 的建议）。
					return banMergedRoot(ctx, tx, id, now)
				}
				return nil
			}
		}
		return fmt.Errorf("用户或设备 %s %w", id, ErrNotFound)
	})
	return kind, err
}

// IsBanned 判断用户或设备是否被封禁。任一为空字符串就不查那一项。
func (s *Store) IsBanned(ctx context.Context, userID, deviceID string) (bool, error) {
	var banned bool
	err := s.read.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM users   WHERE id = ? AND banned_at IS NOT NULL)
		    OR EXISTS (SELECT 1 FROM devices WHERE id = ? AND banned_at IS NOT NULL)`,
		userID, deviceID).Scan(&banned)
	return banned, err
}

// CancelGame 强制取消一局（§13.6 guandan admin game cancel <code>）。
// 已经结束或取消的局报错，不重复处理。通知牌友由 #4、#5 接上。
func (s *Store) CancelGame(ctx context.Context, shortCode string, now time.Time) (gameID string, err error) {
	err = s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		var status string
		err := tx.QueryRowContext(ctx, `SELECT id, status FROM games WHERE short_code = ?`, shortCode).
			Scan(&gameID, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("牌局 %s %w", shortCode, ErrNotFound)
		}
		if err != nil {
			return err
		}
		if status != "open" && status != "full" {
			return fmt.Errorf("牌局 %s 的状态是 %s，不能取消", shortCode, status)
		}
		return endGame(ctx, tx, gameID, "cancelled", now)
	})
	return gameID, err
}

// DeleteUserData 删除一个用户的个人数据（§14，guandan admin user delete，以后 DELETE /api/me 也用它）：
//   - 称呼改成「已注销牌友」，清空 WhatsApp 号码；用户行和座位记录保留，历史局的人数统计不受影响；
//   - 删除设备、推送订阅、认领令牌、微信关联、订阅消息次数、星标关系、邀请记录、候补；
//   - 埋点去掉 user_id。
func (s *Store) DeleteUserData(ctx context.Context, userID string) error {
	return s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		r, err := tx.ExecContext(ctx,
			`UPDATE users SET nickname = ?, wa_phone = NULL WHERE id = ?`, DeletedNickname, userID)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			return fmt.Errorf("用户 %s %w", userID, ErrNotFound)
		}
		for _, q := range []string{
			`DELETE FROM push_subscriptions WHERE user_id = ?`,
			`DELETE FROM devices WHERE user_id = ?`,
			`DELETE FROM claim_tokens WHERE user_id = ?`,
			`DELETE FROM user_links WHERE user_id = ?`,
			`DELETE FROM wx_subscribe_quota WHERE user_id = ?`,
			`DELETE FROM follows WHERE user_id = ?1 OR target_id = ?1`,
			`DELETE FROM invites WHERE user_id = ?1 OR invited_by = ?1`,
			`DELETE FROM waitlist WHERE user_id = ?`,
			`UPDATE events SET user_id = NULL WHERE user_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, userID); err != nil {
				return fmt.Errorf("删除用户 %s 的数据: %w", userID, err)
			}
		}
		return nil
	})
}

// Stats 是 guandan admin stats 的输出（§13.6、h5-pwa-plan §8 的指标来自 events 表）。
type Stats struct {
	Since        time.Time
	NewUsers     int64
	NewGames     int64
	GamesByState map[string]int64 // 这段时间内开出的局，按当前状态
	Events       []EventCount     // 按次数从多到少
}

type EventCount struct {
	Name  string
	Count int64
}

func (s *Store) Stats(ctx context.Context, since time.Time) (Stats, error) {
	st := Stats{Since: since, GamesByState: map[string]int64{}}
	unix := since.Unix()
	if err := s.read.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE created_at >= ?`, unix).
		Scan(&st.NewUsers); err != nil {
		return st, err
	}

	rows, err := s.read.QueryContext(ctx,
		`SELECT status, count(*) FROM games WHERE created_at >= ? GROUP BY status`, unix)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var status string
		var n int64
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			return st, err
		}
		st.GamesByState[status] = n
		st.NewGames += n
	}
	if err := rows.Close(); err != nil {
		return st, err
	}

	rows, err = s.read.QueryContext(ctx,
		`SELECT name, count(*) AS n FROM events WHERE at >= ? GROUP BY name ORDER BY n DESC, name`, unix)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var e EventCount
		if err := rows.Scan(&e.Name, &e.Count); err != nil {
			return st, err
		}
		st.Events = append(st.Events, e)
	}
	return st, rows.Err()
}

// banMergedRoot 顺着 merged_into 找到最终的用户并封禁。最多跟 10 层，防止数据里有环。
func banMergedRoot(ctx context.Context, tx *Tx, id string, now time.Time) error {
	for i := 0; i < 10; i++ {
		var next sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT merged_into FROM users WHERE id = ?`, id).Scan(&next); err != nil {
			return err
		}
		if !next.Valid || next.String == "" || next.String == id {
			return nil
		}
		id = next.String
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET banned_at = COALESCE(banned_at, ?) WHERE id = ?`, now.Unix(), id); err != nil {
			return err
		}
	}
	return nil
}

// FunnelEvents 是转化漏斗按顺序统计的埋点名（#117）。POST /api/events 的白名单也是这张表。
var FunnelEvents = []string{
	"detail_view", "join_click", "join_ok", "share_open", "share_channel",
	"push_prompt", "push_ok", "jumpout",
}

// Funnel 是一个环境（wechat / whatsapp / browser / pwa / 其他）里各埋点的次数，下标和 FunnelEvents 对应。
type Funnel struct {
	Env    string
	Counts []int64
}

// Funnels 按环境统计 FunnelEvents 里各埋点自 since 起的次数。没有环境的记成 other；按环境名排序。
// 是事件次数，不是去重后的人数：埋点不记设备，同一个人看两次详情页算两次。
func (s *Store) Funnels(ctx context.Context, since time.Time) ([]Funnel, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(FunnelEvents)), ",")
	args := []any{since.Unix()}
	for _, n := range FunnelEvents {
		args = append(args, n)
	}
	rows, err := s.read.QueryContext(ctx, `
		SELECT COALESCE(env, 'other'), name, count(*)
		  FROM events WHERE at >= ? AND name IN (`+placeholders+`)
		 GROUP BY 1, 2 ORDER BY 1`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	index := make(map[string]int, len(FunnelEvents))
	for i, n := range FunnelEvents {
		index[n] = i
	}
	var out []Funnel
	for rows.Next() {
		var (
			env, name string
			n         int64
		)
		if err := rows.Scan(&env, &name, &n); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].Env != env {
			out = append(out, Funnel{Env: env, Counts: make([]int64, len(FunnelEvents))})
		}
		out[len(out)-1].Counts[index[name]] = n
	}
	return out, rows.Err()
}
