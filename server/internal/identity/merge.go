package identity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// mergeUsers 把同一个人的两个身份合成一个，返回最终保留的 user id（§5.2）。
//
// **不要在面向用户的接口里调用它。** §5.2 修订之后，claim 兑换遇到「这台设备已经有别人的
// 身份」时不再合并，而是返回 409 让用户确认，确认后只把这台设备切过去。合并是不可逆的，
// 谁都不该因为点了一个链接就把自己的身份并进别人的账号（审计 PoC：攻击者把自己的
// claim 链接发到群里，受害者一点，两个身份就被合了）。
//
// 以后要合并身份，只作为 `guandan admin` 子命令提供。
//
// 规则：以**先注册**的那个为主。座位冲突时保留更早的那个座位。
// 旧用户只写 merged_into，不删除——删除会让历史牌局的人数统计出错（§14）。
//
// §5.2 只提到「座位和推送订阅」，这里把其余引用 user_id 的表也一并迁移，
// 否则会留下指向已合并身份的记录（尤其是 games.host_id，不迁的话 §4 的不变量 3 会不成立）。
func mergeUsers(ctx context.Context, tx *store.Tx, a, b string) (string, error) {
	if a == b {
		return a, nil
	}

	primary, other, err := olderUser(ctx, tx, a, b)
	if err != nil {
		return "", err
	}

	// 牌局：局头也要跟着走，否则「局头一定坐在座位上」这条不变量会破。
	if _, err := tx.ExecContext(ctx,
		`UPDATE games SET host_id = ? WHERE host_id = ?`, primary, other); err != nil {
		return "", err
	}

	// 座位：两边都报了同一局时保留更早的那个。
	// 先删掉 primary 在「other 更早」的那些局里的座位，好把 other 的搬过去；
	// 剩下的冲突就是 primary 更早，UPDATE OR IGNORE 会忽略，最后被清掉。
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM seats
		 WHERE user_id = ?
		   AND EXISTS (SELECT 1 FROM seats o
		                WHERE o.game_id = seats.game_id
		                  AND o.user_id = ?
		                  AND o.created_at < seats.created_at)`, primary, other); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE OR IGNORE seats SET user_id = ? WHERE user_id = ?`, primary, other); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM seats WHERE user_id = ?`, other); err != nil {
		return "", err
	}

	// 其余以 (game_id, user_id) 或 (user_id, key) 为主键的表，同样是「能搬就搬，搬不动就丢」。
	for _, stmt := range []struct{ update, cleanup string }{
		{`UPDATE OR IGNORE waitlist SET user_id = ? WHERE user_id = ?`, `DELETE FROM waitlist WHERE user_id = ?`},
		{`UPDATE OR IGNORE game_bans SET user_id = ? WHERE user_id = ?`, `DELETE FROM game_bans WHERE user_id = ?`},
		{`UPDATE OR IGNORE invites SET user_id = ? WHERE user_id = ?`, `DELETE FROM invites WHERE user_id = ?`},
		{`UPDATE OR IGNORE idempotency_keys SET user_id = ? WHERE user_id = ?`, `DELETE FROM idempotency_keys WHERE user_id = ?`},
	} {
		if _, err := tx.ExecContext(ctx, stmt.update, primary, other); err != nil {
			return "", err
		}
		if _, err := tx.ExecContext(ctx, stmt.cleanup, other); err != nil {
			return "", err
		}
	}

	// 星标牌友：两个方向都要搬。
	if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE follows SET user_id = ? WHERE user_id = ?`, primary, other); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE user_id = ?`, other); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE OR IGNORE follows SET target_id = ? WHERE target_id = ?`, primary, other); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE target_id = ?`, other); err != nil {
		return "", err
	}
	// 两个身份原来互相星标的话，搬完会变成「自己星标自己」，去掉。
	if _, err := tx.ExecContext(ctx, `DELETE FROM follows WHERE user_id = ? AND target_id = ?`, primary, primary); err != nil {
		return "", err
	}

	// 这几张表的键不含 user_id，直接改就行。
	for _, stmt := range []string{
		`UPDATE devices SET user_id = ? WHERE user_id = ?`,
		`UPDATE push_subscriptions SET user_id = ? WHERE user_id = ?`,
		`UPDATE outbox SET user_id = ? WHERE user_id = ?`,
		// 计分记录里的「谁开始的、谁记的、谁改的」（#165）。
		`UPDATE score_matches SET started_by = ? WHERE started_by = ?`,
		`UPDATE score_rounds SET recorded_by = ? WHERE recorded_by = ?`,
		`UPDATE score_rounds SET edited_by = ? WHERE edited_by = ?`,
	} {
		if _, err := tx.ExecContext(ctx, stmt, primary, other); err != nil {
			return "", err
		}
	}

	// 旧身份只标记，不删除（§5.2）。
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET merged_into = ? WHERE id = ?`, primary, other); err != nil {
		return "", err
	}

	return primary, nil
}

// olderUser 返回 (更早注册的 id, 另一个 id)。
func olderUser(ctx context.Context, tx *store.Tx, a, b string) (primary, other string, err error) {
	createdAt := func(id string) (int64, error) {
		var t int64
		err := tx.QueryRowContext(ctx, `SELECT created_at FROM users WHERE id = ?`, id).Scan(&t)
		return t, err
	}

	ta, err := createdAt(a)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, a)
	}
	if err != nil {
		return "", "", err
	}
	tb, err := createdAt(b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("%w: %s", ErrNotFound, b)
	}
	if err != nil {
		return "", "", err
	}

	// created_at 相同时按 id 比较，保证结果确定。
	if tb < ta || (tb == ta && b < a) {
		return b, a, nil
	}
	return a, b, nil
}
