package games

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 「我的牌局」（#211）。用户原话：
//
//	「增加一个我的牌局，要把我开的、我参与的，这些还没有开始的牌局列出来，时间地点，让我能看清凑局的状态」
//	「历史局也要放进去，可以删」
//
// 分两段：
//   - 要打的（Mine）：我开的 / 已坐下 / 候补中，还没开始、也没取消的局，按开始时间从近到远；
//   - 历史（History）：我开的 / 坐过的，已经开始的局，加上已取消、已打完的局，从近到远分页。
//     候补没轮上的不算「我参与过」（用户说的「我参与的」是坐下打过的）。
//
// 删除（HideFromHistory）只写一行 hidden_games：只影响我自己的列表，
// 不动牌局本身、不动同局别人的列表、不动计分记录和牌友关系。

// HistoryLimit 是历史一页的条数，也是单次请求的上限。
const HistoryLimit = 20

// ErrNotHistory 表示这一局还没开始，不能从历史里删（那要去详情页退出或取消）。
var ErrNotHistory = errors.New("这一局还没开始，不能从历史里删")

// ScoreBrief 是历史里一条的计分摘要，只在这一局记过分时才有。
//
// 只放本仓库语言里本来就有的数字（§4：不涉及金钱，没有分数、倍率、金额）：
// 「双上几次」是牌桌上会说的话，级数是牌面本身。
type ScoreBrief struct {
	Kind string `json:"kind"`   // classic / no-level
	Rounds int  `json:"rounds"` // 记了几局
	// Winner 是过了 A 的那一队（red / black）；没打完是空串。
	Winner         string `json:"winner,omitempty"`
	RedLevel       int    `json:"red_level"`
	BlackLevel     int    `json:"black_level"`
	RedDoubleUps   int    `json:"red_double_ups"`
	BlackDoubleUps int    `json:"black_double_ups"`
}

// HistoryItem 是历史列表里的一条：列表摘要 + 可选比分。
type HistoryItem struct {
	Summary
	Score *ScoreBrief
}

// mineWhere 是「这一局和我有关」的条件。三个占位符都传同一个 userID。
const mineWhere = `( g.host_id = ?
      OR EXISTS (SELECT 1 FROM seats s WHERE s.game_id = g.id AND s.user_id = ?)
      OR EXISTS (SELECT 1 FROM waitlist m WHERE m.game_id = g.id AND m.user_id = ?) )`

// historyWhere 是「我参与过」的条件（坐下过或是我开的，候补没轮上的不算）。
const historyWhere = `( g.host_id = ?
      OR EXISTS (SELECT 1 FROM seats s WHERE s.game_id = g.id AND s.user_id = ?) )`

// hiddenWhere 是「我没把它从历史里删掉」。
const hiddenWhere = ` NOT EXISTS (SELECT 1 FROM hidden_games h WHERE h.game_id = g.id AND h.user_id = ?)`

// Mine 返回「要打的」那一段：还没开始、也没取消，并且我开的 / 已坐下 / 候补中。
func (s *Service) Mine(ctx context.Context, userID string) ([]Summary, error) {
	now := s.clk.Now().Unix()
	args := []any{StatusOpen, StatusFull, now, userID, userID, userID}

	rows, err := s.st.Read().QueryContext(ctx, summarySelect+`
		 WHERE g.status IN (?, ?) AND g.starts_at > ? AND `+mineWhere+`
		 ORDER BY g.starts_at ASC, g.id ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("列出我的牌局: %w", err)
	}
	defer rows.Close()

	out, err := s.scanSummaries(rows)
	if err != nil {
		return nil, err
	}
	if err := s.attachSeats(ctx, out); err != nil {
		return nil, err
	}
	if err := s.attachMyRole(ctx, userID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// History 返回「历史」那一段。before/beforeID 是上一页最后一条的位置（同一个开始时间可能有
// 好几条，所以还要 id 才算得准）；首次调用传 0 和 ""。hasMore 表示还有更早的。
//
// 历史 = 我开过或坐过的局，并且已经开始了、或者已取消 / 已打完；我自己删掉的（hidden_games）不出现。
func (s *Service) History(ctx context.Context, userID string, before int64, beforeID string, limit int) ([]HistoryItem, bool, error) {
	if limit <= 0 || limit > HistoryLimit {
		limit = HistoryLimit
	}
	now := s.clk.Now().Unix()

	args := []any{
		now, StatusCancelled, StatusDone, // 已经开始了 / 已取消 / 已打完
		userID, userID, // 我开的或坐过的
		userID, // 没被我删掉
	}
	page := ""
	if before != 0 {
		page = ` AND (g.starts_at < ? OR (g.starts_at = ? AND g.id < ?))`
		args = append(args, before, before, beforeID)
	}
	// 多取一条：拿它判断还有没有下一批。返回时截掉。
	args = append(args, limit+1)

	rows, err := s.st.Read().QueryContext(ctx, summarySelect+`
		 WHERE ( g.starts_at <= ? OR g.status IN (?, ?) ) AND `+historyWhere+`
		   AND `+hiddenWhere+page+`
		 ORDER BY g.starts_at DESC, g.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, false, fmt.Errorf("列出历史牌局: %w", err)
	}
	defer rows.Close()

	all, err := s.scanSummaries(rows)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}
	if err := s.attachSeats(ctx, all); err != nil {
		return nil, false, err
	}

	items := make([]HistoryItem, 0, len(all))
	for _, sum := range all {
		brief, err := s.scoreBrief(ctx, sum.Game)
		if err != nil {
			return nil, false, err
		}
		items = append(items, HistoryItem{Summary: sum, Score: brief})
	}
	return items, hasMore, nil
}

// scoreBrief 读一局的历史比分。没记过分返回 nil。
//
// 用的就是计分页那一个 loadScore（同一份重算逻辑，不另写一条 SQL）：一页最多 20 条，
// 多 20 次小查询，换的是「口径和计分页永远一致」。
func (s *Service) scoreBrief(ctx context.Context, g Game) (*ScoreBrief, error) {
	sc, err := loadScore(ctx, s.st.Read(), g, "", s.clk.Now())
	if err != nil {
		return nil, err
	}
	if !sc.Started {
		return nil, nil
	}
	return &ScoreBrief{
		Kind:           string(sc.Settings.Kind),
		Rounds:         len(sc.Rounds),
		Winner:         string(sc.State.Winner),
		RedLevel:       sc.State.Red.Level,
		BlackLevel:     sc.State.Black.Level,
		RedDoubleUps:   sc.State.Red.DoubleUps,
		BlackDoubleUps: sc.State.Black.DoubleUps,
	}, nil
}

// HideFromHistory 把一局从**我自己的**历史里拿掉。重复删是幂等的（都是 204 的效果）。
func (s *Service) HideFromHistory(ctx context.Context, userID, code string) error {
	now := s.clk.Now()
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		var (
			gameID   string
			startsAt int64
			status   string
		)
		err := tx.QueryRowContext(ctx,
			`SELECT id, starts_at, status FROM games WHERE short_code = ?`, code).
			Scan(&gameID, &startsAt, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		// 只能删自己历史里的。不是我的就当没这一局，不告诉对方存在（和别的接口一个口径）。
		var mine int
		if err := tx.QueryRowContext(ctx, `SELECT CASE WHEN `+historyWhere+` THEN 1 ELSE 0 END
			 FROM games g WHERE g.id = ?`, userID, userID, gameID).Scan(&mine); err != nil {
			return err
		}
		if mine == 0 {
			return ErrNotFound
		}

		// 还没开始的局不走这里：那是退出座位或取消牌局，在详情页做。
		if status != StatusCancelled && status != StatusDone && startsAt > now.Unix() {
			return ErrNotHistory
		}

		_, err = tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO hidden_games (user_id, game_id, hidden_at) VALUES (?, ?, ?)`,
			userID, gameID, now.Unix())
		return err
	})
}
