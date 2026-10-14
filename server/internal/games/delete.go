package games

import (
	"context"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// PurgeUser 让这个人在所有**还没结束**的局里按正常退出离开（§14 的注销）。
//
// 「正常退出」就是 §5.1 那一套：候补队首补位、局头转给座位上最早进来的人、
// 通知座位上的人、这一局没人了就取消。注销的人跟点了「退出」的人在牌局侧没有区别——
// 区别只在于他不会回来了，所以他的提醒、设备、身份数据一并删掉（在 identity 那边做）。
//
// 已结束 / 已取消的局**不动**：那是历史记录，座位行留着，称呼由 identity 改成
// 「已注销牌友」，人数统计因此不会出错（§14）。
//
// 返回处理了几局。调用方负责把它放在自己的写事务里（identity.DeleteMe）。
func (s *Service) PurgeUser(ctx context.Context, tx *store.Tx, userID string, now time.Time) (int, error) {
	rows, err := tx.QueryContext(ctx, gameColumns+`
		 WHERE g.status IN ('open', 'full')
		   AND (EXISTS (SELECT 1 FROM seats s WHERE s.game_id = g.id AND s.user_id = ?)
		     OR EXISTS (SELECT 1 FROM waitlist w WHERE w.game_id = g.id AND w.user_id = ?))
		 ORDER BY g.starts_at`, userID, userID)
	if err != nil {
		return 0, err
	}
	var targets []Game
	for rows.Next() {
		g, err := scanGameFrom(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		targets = append(targets, g)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, game := range targets {
		if _, err := s.leaveInTx(ctx, tx, game, userID, now); err != nil {
			return 0, err
		}
	}
	return len(targets), nil
}
