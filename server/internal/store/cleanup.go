package store

import (
	"context"
	"fmt"
	"time"
)

// 清理任务的各项期限（§13.1、§14）。
const (
	GameEndAfterStart    = 6 * time.Hour
	claimTokenRetention  = 24 * time.Hour
	idempotencyRetention = 24 * time.Hour
	outboxRetention      = 30 * 24 * time.Hour
	deviceInactiveLimit  = 180 * 24 * time.Hour
	eventRetention       = 365 * 24 * time.Hour

	// 每个写事务最多结束这么多局，保证写事务足够短（§11.1）。
	endGamesBatch = 100
)

// CleanupResult 是一轮清理做了什么，用来记日志。
type CleanupResult struct {
	GamesDone         int
	GamesCancelled    int
	ClaimTokens       int64
	IdempotencyKeys   int64
	Outbox            int64
	Devices           int64
	PushSubscriptions int64
	Events            int64
}

// Cleanup 执行一轮 §13.1 的清理：
//   - 开局 6 小时后，open / full 的局改为 done；open 且只剩局头一人的改为 cancelled；
//   - 删除过期的认领令牌、幂等键、早已发完或放弃的 outbox；
//   - 删除 180 天没出现过的设备和它们的推送订阅；删除一年前的埋点（§14）。
//
// 改牌局状态的那部分先直接写 SQL，并 Touch 触发不变量检查；#4 的 games 包落地后统一到那里。
func (s *Store) Cleanup(ctx context.Context, now time.Time) (CleanupResult, error) {
	var res CleanupResult
	for {
		done, cancelled, err := s.endStartedGames(ctx, now)
		if err != nil {
			return res, err
		}
		res.GamesDone += done
		res.GamesCancelled += cancelled
		if done+cancelled < endGamesBatch {
			break
		}
	}

	err := s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		del := func(n *int64, query string, args ...any) error {
			r, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				return err
			}
			*n, err = r.RowsAffected()
			return err
		}
		unix := now.Unix()
		if err := del(&res.ClaimTokens, `DELETE FROM claim_tokens WHERE expires_at < ?`,
			unix-int64(claimTokenRetention/time.Second)); err != nil {
			return fmt.Errorf("清理 claim_tokens: %w", err)
		}
		if err := del(&res.IdempotencyKeys, `DELETE FROM idempotency_keys WHERE created_at < ?`,
			unix-int64(idempotencyRetention/time.Second)); err != nil {
			return fmt.Errorf("清理 idempotency_keys: %w", err)
		}
		if err := del(&res.Outbox, `DELETE FROM outbox WHERE COALESCE(sent_at, dropped_at) < ?`,
			unix-int64(outboxRetention/time.Second)); err != nil {
			return fmt.Errorf("清理 outbox: %w", err)
		}
		staleDevice := unix - int64(deviceInactiveLimit/time.Second)
		if err := del(&res.PushSubscriptions, `DELETE FROM push_subscriptions
			WHERE device_id IN (SELECT id FROM devices WHERE last_seen < ?)`, staleDevice); err != nil {
			return fmt.Errorf("清理推送订阅: %w", err)
		}
		if err := del(&res.Devices, `DELETE FROM devices WHERE last_seen < ?`, staleDevice); err != nil {
			return fmt.Errorf("清理设备: %w", err)
		}
		if err := del(&res.Events, `DELETE FROM events WHERE at < ?`,
			unix-int64(eventRetention/time.Second)); err != nil {
			return fmt.Errorf("清理埋点: %w", err)
		}
		return nil
	})
	return res, err
}

// endStartedGames 结束一批开局已超过 6 小时的局，返回改成 done 和 cancelled 的数量。
func (s *Store) endStartedGames(ctx context.Context, now time.Time) (done, cancelled int, err error) {
	err = s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		done, cancelled = 0, 0
		rows, err := tx.QueryContext(ctx, `
			SELECT g.id, g.status, (SELECT count(*) FROM seats WHERE game_id = g.id)
			  FROM games g
			 WHERE g.status IN ('open', 'full') AND g.starts_at <= ?
			 ORDER BY g.starts_at
			 LIMIT ?`, now.Add(-GameEndAfterStart).Unix(), endGamesBatch)
		if err != nil {
			return fmt.Errorf("查找要结束的局: %w", err)
		}
		type game struct {
			id, status string
			seats      int
		}
		var games []game
		for rows.Next() {
			var g game
			if err := rows.Scan(&g.id, &g.status, &g.seats); err != nil {
				rows.Close()
				return err
			}
			games = append(games, g)
		}
		if err := rows.Close(); err != nil {
			return err
		}

		for _, g := range games {
			next := "done"
			if g.status == "open" && g.seats <= 1 {
				next = "cancelled" // 只剩局头一个人，局没打成
			}
			if err := endGame(ctx, tx, g.id, next, now); err != nil {
				return err
			}
			if next == "done" {
				done++
			} else {
				cancelled++
			}
		}
		return nil
	})
	return done, cancelled, err
}

// endGame 把一局改成 done 或 cancelled：写 ended_at、version+1，
// 并放弃还没发的提醒（否则违反 §4 不变量 5），最后登记不变量检查。
func endGame(ctx context.Context, tx *Tx, gameID, status string, now time.Time) error {
	unix := now.Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE games SET status = ?, ended_at = ?, version = version + 1 WHERE id = ?`,
		status, unix, gameID); err != nil {
		return fmt.Errorf("结束牌局 %s: %w", gameID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE outbox SET dropped_at = ?
		  WHERE game_id = ? AND sent_at IS NULL AND dropped_at IS NULL AND send_at > ?`,
		unix, gameID, unix); err != nil {
		return fmt.Errorf("放弃牌局 %s 的提醒: %w", gameID, err)
	}
	tx.Touch(gameID)
	return nil
}

// Optimize 执行 PRAGMA optimize（§13.1：每天一次）。
func (s *Store) Optimize(ctx context.Context) error {
	return s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		_, err := tx.ExecContext(ctx, `PRAGMA optimize`)
		return err
	})
}

// OutboxStats 是 /metrics 里的 outbox 积压情况（§13.7）。
type OutboxStats struct {
	Pending  int64 // 还没发也没放弃
	Retrying int64 // 其中至少失败过一次的
	Dropped  int64 // 放弃发送的（30 天内，更早的已被清理）
}

func (s *Store) OutboxStats(ctx context.Context) (OutboxStats, error) {
	var st OutboxStats
	err := s.read.QueryRowContext(ctx, `
		SELECT
		  count(*) FILTER (WHERE sent_at IS NULL AND dropped_at IS NULL),
		  count(*) FILTER (WHERE sent_at IS NULL AND dropped_at IS NULL AND attempts > 0),
		  count(*) FILTER (WHERE dropped_at IS NOT NULL)
		FROM outbox`).Scan(&st.Pending, &st.Retrying, &st.Dropped)
	return st, err
}
