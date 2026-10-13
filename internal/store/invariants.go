package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Queryer 让不变量检查既能跑在写事务里（*sql.Conn），也能跑在读池上（*sql.DB）。
type Queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CheckInvariants 检查 docs/server-design.md §4 的数据不变量。
// Write 在提交前对每个 Touch 过的牌局调用它，测试里也能在每一步操作后调用。
//
// §4 第 1 条写的是「status='full' 当且仅当座位数为 4」。字面执行会让 done 的牌局
// （局结束后座位记录仍然保留，见 §13.1）违规，所以这里把这条限定在 open / full 两个状态上。
func CheckInvariants(ctx context.Context, q Queryer, gameID string) error {
	var (
		status  string
		endedAt sql.NullInt64
	)
	err := q.QueryRowContext(ctx, `SELECT status, ended_at FROM games WHERE id = ?`, gameID).
		Scan(&status, &endedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // 局被删掉了，没有不变量可查
	}
	if err != nil {
		return fmt.Errorf("读取牌局 %s: %w", gameID, err)
	}

	seats, err := count(ctx, q, `SELECT count(*) FROM seats WHERE game_id = ?`, gameID)
	if err != nil {
		return err
	}
	waitlisted, err := count(ctx, q, `SELECT count(*) FROM waitlist WHERE game_id = ?`, gameID)
	if err != nil {
		return err
	}

	// 1. 座位数 0–4；open / full 时 status='full' 与满座互为充要条件。
	if seats > 4 {
		return fmt.Errorf("不变量 1：牌局 %s 有 %d 个座位", gameID, seats)
	}
	if status == "full" && seats != 4 {
		return fmt.Errorf("不变量 1：牌局 %s 是 full，却只坐了 %d 人", gameID, seats)
	}
	if status == "open" && seats == 4 {
		return fmt.Errorf("不变量 1：牌局 %s 已满座，状态却是 open", gameID)
	}

	// 2. 有人在候补，座位一定是满的。
	if waitlisted > 0 && seats != 4 {
		return fmt.Errorf("不变量 2：牌局 %s 只有 %d 人入座，却有 %d 人在候补", gameID, seats, waitlisted)
	}

	// 3. 局还在进行时，局头一定坐在某个座位上。
	if status == "open" || status == "full" {
		hostSeated, err := count(ctx, q,
			`SELECT count(*) FROM seats s JOIN games g ON g.id = s.game_id
			  WHERE s.game_id = ? AND s.user_id = g.host_id`, gameID)
		if err != nil {
			return err
		}
		if hostSeated == 0 {
			return fmt.Errorf("不变量 3：牌局 %s 的局头不在座位上", gameID)
		}
	}

	// 4. 同一个人不会同时出现在座位和候补里（触发器也拦，这里再确认一次）。
	both, err := count(ctx, q,
		`SELECT count(*) FROM seats s
		   JOIN waitlist w ON w.game_id = s.game_id AND w.user_id = s.user_id
		  WHERE s.game_id = ?`, gameID)
	if err != nil {
		return err
	}
	if both > 0 {
		return fmt.Errorf("不变量 4：牌局 %s 有 %d 人同时在座位和候补里", gameID, both)
	}

	// 5. 已结束的牌局不再留未发送的开局提醒。
	if status == "cancelled" || status == "done" {
		pending, err := count(ctx, q,
			`SELECT count(*) FROM outbox
			  WHERE game_id = ? AND kind = 't_minus_1h'
			    AND sent_at IS NULL AND dropped_at IS NULL
			    AND send_at > COALESCE(?, 0)`, gameID, endedAt)
		if err != nil {
			return err
		}
		if pending > 0 {
			return fmt.Errorf("不变量 5：牌局 %s 已结束，还有 %d 条开局提醒没发", gameID, pending)
		}
	}

	return nil
}

func count(ctx context.Context, q Queryer, query string, args ...any) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
