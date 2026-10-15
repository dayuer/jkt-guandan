package games

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/clock"
	"github.com/dayuer/jkt-guandan/server/internal/ratelimit"
	"github.com/dayuer/jkt-guandan/server/internal/secheck"
	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// 提醒类型（§5.3）。
const (
	ReminderFull          = "full"
	ReminderTMinus1h      = "t_minus_1h"
	ReminderSeatLeft      = "seat_left"
	ReminderPromoted      = "promoted"
	ReminderHostSeatTaken = "host_seat_taken"
	ReminderHostWaitlist  = "host_waitlist"
	ReminderHostTransfer  = "host_transferred"
	ReminderHostChanged   = "host_changed"
	ReminderRescheduled   = "rescheduled"
	ReminderCancelled     = "cancelled"
	ReminderKicked        = "kicked"
)

// shortCodeLen 是分享链接里短码的长度（§10.9：6 位 Crockford base32，约 10 亿种组合）。
const shortCodeLen = 6

var crockford = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// Service 是牌局业务的入口。
type Service struct {
	st  *store.Store
	clk clock.Clock
	pub Publisher

	// 报名/退出：每用户每分钟 30 次（§12.3）。
	joinLimiter *ratelimit.Limiter
	// 计分（开始、记一局、改一局）：每用户每分钟 30 次。一局牌几分钟才打完一局，正常用不到。
	scoreLimiter *ratelimit.Limiter

	// 内容安全检测（#146）：小游戏用户写的地点、备注先送微信查。nil 时不查。
	sec *secheck.Guard
}

// SetContentGuard 接上微信内容安全检测（#146）。
func (s *Service) SetContentGuard(g *secheck.Guard) { s.sec = g }

// NewService 建牌局服务。pub 为 nil 时用 NopPublisher（#18 的 Hub 还没合进来）。
func NewService(st *store.Store, clk clock.Clock, pub Publisher) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	if pub == nil {
		pub = NopPublisher{}
	}
	return &Service{
		st:           st,
		clk:          clk,
		pub:          pub,
		joinLimiter:  ratelimit.New(30, 30, clk),
		scoreLimiter: ratelimit.New(30, 30, clk),
	}
}

// ---------------------------------------------------------------------------
// 读
// ---------------------------------------------------------------------------

// Get 按短码取一局的详情。viewerID 为空表示访客。
func (s *Service) Get(ctx context.Context, code, viewerID string) (*Detail, error) {
	return s.loadDetail(ctx, s.st.Read(), code, viewerID)
}

// List 返回某个片区未来几天的局（§10.9：只返回未来 7 天内，并且分页）。
func (s *Service) List(ctx context.Context, viewerID, area, day string, limit, offset int) ([]Summary, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	now := s.clk.Now()
	from, to := dayRange(now, day)
	// 下界：已经开始了但还在 JoinGrace 之内的局仍然可以补人，所以列表里还要看得见；
	// 超过宽限期的就不显示了。
	//
	// - 没筛日期：窗口就从宽限期起点开始（不能用 dayRange 返回的 now，
	//   否则「一小时前刚开始、正缺人」的局会被漏掉）；
	// - 筛了日期：取日期起点和宽限期起点里更晚的那个。
	earliest := now.Add(-JoinGrace)
	if day == "" {
		from = earliest
	} else if from.Before(earliest) {
		from = earliest
	}
	if weekEnd := now.AddDate(0, 0, 7); to.After(weekEnd) {
		to = weekEnd
	}

	query := `
		SELECT g.id, g.short_code, g.host_id, g.starts_at, g.area, g.venue, g.kind,
		       COALESCE(g.note, ''), g.status, g.version, g.created_at,
		       COALESCE(g.rescheduled_at, 0), COALESCE(u.nickname, ''),
		       (SELECT count(*) FROM seats s WHERE s.game_id = g.id)
		  FROM games g
		  LEFT JOIN users u ON u.id = g.host_id
		 WHERE g.status IN ('open','full') AND g.starts_at >= ? AND g.starts_at < ?`
	args := []any{from.Unix(), to.Unix()}
	if area != "" {
		query += ` AND g.area = ?`
		args = append(args, area)
	}
	query += ` ORDER BY g.starts_at ASC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := s.st.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("列出牌局: %w", err)
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var (
			sum                 Summary
			startsAt, createdAt int64
			rescheduledAt       int64
			seats               int
		)
		if err := rows.Scan(&sum.Game.ID, &sum.Game.ShortCode, &sum.Game.HostID, &startsAt,
			&sum.Game.Area, &sum.Game.Venue, &sum.Game.Kind, &sum.Game.Note,
			&sum.Game.Status, &sum.Game.Version, &createdAt, &rescheduledAt,
			&sum.HostName, &seats); err != nil {
			return nil, err
		}
		sum.Game.StartsAt = time.Unix(startsAt, 0)
		sum.Game.CreatedAt = time.Unix(createdAt, 0)
		sum.Game.RescheduledAt = unixOrZero(rescheduledAt)
		sum.Missing = Capacity - seats
		sum.MyRole = "none"
		out = append(out, sum)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachSeats(ctx, out); err != nil {
		return nil, err
	}
	if err := s.attachMyRole(ctx, viewerID, out); err != nil {
		return nil, err
	}
	return out, nil
}

// attachMyRole 算出当前身份在这些局里的位置（座位上 / 候补第几位 / 都不是）。
// 座位已经读出来了，从里面找；候补只查这个人在这些局里排的位置，不读别人的。
// 没有身份时全是 none。
func (s *Service) attachMyRole(ctx context.Context, viewerID string, items []Summary) error {
	if viewerID == "" || len(items) == 0 {
		return nil
	}
	byID := make(map[string]int, len(items))
	args := []any{viewerID}
	placeholders := ""
	for i := range items {
		byID[items[i].Game.ID] = i
		for _, seat := range items[i].Seats {
			if seat.UserID == viewerID {
				items[i].MyRole = "seated"
			}
		}
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, items[i].Game.ID)
	}

	// 位置和 loadDetailFrom 一致：先按 created_at，相同再按 rowid。
	rows, err := s.st.Read().QueryContext(ctx, `
		SELECT w.game_id,
		       (SELECT count(*) FROM waitlist o
		         WHERE o.game_id = w.game_id
		           AND (o.created_at < w.created_at
		                OR (o.created_at = w.created_at AND o.rowid <= w.rowid)))
		  FROM waitlist w
		 WHERE w.user_id = ? AND w.game_id IN (`+placeholders+`)`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			gameID string
			pos    int
		)
		if err := rows.Scan(&gameID, &pos); err != nil {
			return err
		}
		if idx, ok := byID[gameID]; ok && items[idx].MyRole != "seated" {
			items[idx].MyRole = "waitlist"
			items[idx].WaitlistPos = pos
		}
	}
	return rows.Err()
}

// attachSeats 一次性把这些局的座位都读出来，避免每条一次查询。
func (s *Service) attachSeats(ctx context.Context, items []Summary) error {
	if len(items) == 0 {
		return nil
	}

	byID := make(map[string]int, len(items))
	args := make([]any, 0, len(items))
	placeholders := ""
	for i := range items {
		byID[items[i].Game.ID] = i
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, items[i].Game.ID)
	}

	rows, err := s.st.Read().QueryContext(ctx, `
		SELECT s.game_id, s.seat_no, s.user_id, COALESCE(u.nickname, ''), s.created_at
		  FROM seats s LEFT JOIN users u ON u.id = s.user_id
		 WHERE s.game_id IN (`+placeholders+`)
		 ORDER BY s.game_id, s.seat_no`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			gameID    string
			seat      Seat
			createdAt int64
		)
		if err := rows.Scan(&gameID, &seat.SeatNo, &seat.UserID, &seat.Nickname, &createdAt); err != nil {
			return err
		}
		seat.CreatedAt = time.Unix(createdAt, 0)
		if idx, ok := byID[gameID]; ok {
			items[idx].Seats = append(items[idx].Seats, seat)
		}
	}
	return rows.Err()
}

// ---------------------------------------------------------------------------
// 开局
// ---------------------------------------------------------------------------

// Create 开一局：建牌局、把局头放进 1 号座、状态 open（§5.1）。
//
// 局头必须在座位上——§4 的不变量 3 对 open/full 的局就是这么要求的。
//
// idemKey 非空时按 §11.4 做幂等：微信里「点两下」或超时后重试很常见，
// 而开局本身不幂等，重复执行会开出两个一样的局。查键和写键都在同一个写事务里，
// 所以并发提交同一个键也只会执行一次。
func (s *Service) Create(ctx context.Context, hostID string, in CreateInput, idemKey string) (*Detail, error) {
	now := s.clk.Now()
	if err := in.Normalize(now); err != nil {
		return nil, err
	}
	// 本地的长度和词表都过了才送微信（场景 3 论坛），在写事务之外。
	if err := s.sec.CheckUser(ctx, hostID, secheck.SceneForum,
		secheck.Field{Name: "venue", Text: in.Venue}, secheck.Field{Name: "note", Text: in.Note}); err != nil {
		return nil, err
	}

	const route = "POST /api/games"
	var detail *Detail
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 幂等键：查到了就返回第一次的结果，不再建局。
		if idemKey != "" {
			var saved []byte
			err := tx.QueryRowContext(ctx,
				`SELECT response FROM idempotency_keys WHERE user_id = ? AND key = ?`,
				hostID, idemKey).Scan(&saved)
			switch {
			case err == nil:
				var prev struct {
					ShortCode string `json:"short_code"`
				}
				if err := json.Unmarshal(saved, &prev); err != nil {
					return err
				}
				detail, err = loadDetailTx(ctx, tx, prev.ShortCode, hostID)
				return err
			case errors.Is(err, sql.ErrNoRows):
				// 第一次见到这个键，继续往下建局。
			default:
				return err
			}
		}

		// 配额：每人每天最多开 10 局（§10.8）。
		today, err := gamesCreatedToday(ctx, tx, hostID, now)
		if err != nil {
			return err
		}
		if today >= MaxGamesPerDay {
			return fmt.Errorf("%w：每天最多开 %d 局", ErrQuota, MaxGamesPerDay)
		}

		code, err := uniqueShortCode(ctx, tx)
		if err != nil {
			return err
		}
		gameID := newID(now)

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, note,
			                   status, version, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'open', 1, ?)`,
			gameID, code, hostID, in.StartsAt.Unix(), in.Area, in.Venue, in.Kind,
			nullIfEmpty(in.Note), now.Unix()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, 1, ?, ?)`,
			gameID, hostID, now.Unix()); err != nil {
			return err
		}

		tx.Touch(gameID)
		// 新开一局是「首页列表多了一条」，所以发到片区主题（§5.5）。
		s.publishFeedAfterCommit(tx, code, in.Area, EventFeedAdded, 1)

		// 记下这次的结果，下次同一个键直接用（§11.4）。
		if idemKey != "" {
			body, err := json.Marshal(map[string]string{"short_code": code})
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO idempotency_keys (key, user_id, route, status, response, created_at)
				VALUES (?, ?, ?, 200, ?, ?)`,
				idemKey, hostID, route, body, now.Unix()); err != nil {
				return err
			}
		}

		detail, err = loadDetailTx(ctx, tx, code, hostID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// ---------------------------------------------------------------------------
// 报名
// ---------------------------------------------------------------------------

// Join 报名：有空座就落座，没有就进候补（§5.1）。重复提交是幂等的。
func (s *Service) Join(ctx context.Context, code, userID string) (*Detail, error) {
	if !s.joinLimiter.Allow(userID) {
		return nil, ErrRateLimited
	}

	now := s.clk.Now()
	var detail *Detail

	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if !game.Open() {
			return ErrClosed
		}
		if banned, err := isBanned(ctx, tx, game.ID, userID); err != nil {
			return err
		} else if banned {
			return ErrBanned
		}

		// 已经在座位上或候补里：直接返回当前状态，重复提交也安全。
		seated, err := isSeated(ctx, tx, game.ID, userID)
		if err != nil {
			return err
		}
		waiting, err := isWaitlisted(ctx, tx, game.ID, userID)
		if err != nil {
			return err
		}
		if seated || waiting {
			detail, err = loadDetailTx(ctx, tx, code, userID)
			return err
		}

		// 开局后还能补人到 JoinGrace：缺一个人的局到点了，正是要临时补人的时候。
		// 再晚就没意义了。（放在「已经在局里」之后，是为了让重复提交仍然幂等。）
		if now.After(game.StartsAt.Add(JoinGrace)) {
			return ErrStarted
		}

		// 配额：同时最多占 5 个别人局里的座位或候补位（§10.8）。
		held, err := heldSlots(ctx, tx, userID, now)
		if err != nil {
			return err
		}
		if held >= MaxConcurrentHolds {
			return fmt.Errorf("%w：同时最多占 %d 个位置", ErrQuota, MaxConcurrentHolds)
		}

		seatNo, err := freeSeatNo(ctx, tx, game.ID)
		if err != nil {
			return err
		}

		if seatNo == 0 {
			err = s.joinWaitlist(ctx, tx, game, userID, now)
		} else {
			err = s.takeSeat(ctx, tx, game, userID, seatNo, now)
		}
		if err != nil {
			return err
		}

		detail, err = loadDetailTx(ctx, tx, code, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// takeSeat 落座，并处理满员、提醒和实时事件。
func (s *Service) takeSeat(ctx context.Context, tx *store.Tx, game Game, userID string, seatNo int, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, ?)`,
		game.ID, seatNo, userID, now.Unix()); err != nil {
		return err
	}

	version, err := bumpVersion(ctx, tx, game.ID)
	if err != nil {
		return err
	}

	// 开局前不到 1 小时才报名的，不写 t_minus_1h——那时候再提醒已经没意义了（§13.2）。
	if game.StartsAt.Sub(now) > time.Hour {
		if err := addReminder(ctx, tx, game.ID, userID, ReminderTMinus1h,
			game.StartsAt.Add(-time.Hour), version); err != nil {
			return err
		}
	}
	if err := addReminder(ctx, tx, game.ID, game.HostID, ReminderHostSeatTaken, now, version); err != nil {
		return err
	}

	seats, err := countSeats(ctx, tx, game.ID)
	if err != nil {
		return err
	}

	eventType := EventSeatTaken
	// 刚坐满：状态转 full，给 4 个人各写一条提醒。dedupe_key 带版本号，
	// 所以一次满员只会产生一组 full 提醒（§9）。
	if seats == Capacity {
		if _, err := tx.ExecContext(ctx,
			`UPDATE games SET status = 'full', version = version + 1 WHERE id = ?`, game.ID); err != nil {
			return err
		}
		version++
		if err := rememberFull(ctx, tx, game, version, now); err != nil {
			return err
		}
		eventType = EventGameFull
	}

	tx.Touch(game.ID)
	s.publishAfterCommit(tx, game.ShortCode, eventType, version, true)
	return nil
}

// joinWaitlist 进候补，并告诉局头有人排队。
func (s *Service) joinWaitlist(ctx context.Context, tx *store.Tx, game Game, userID string, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO waitlist (game_id, user_id, created_at) VALUES (?, ?, ?)`,
		game.ID, userID, now.Unix()); err != nil {
		return err
	}
	version, err := bumpVersion(ctx, tx, game.ID)
	if err != nil {
		return err
	}
	if err := addReminder(ctx, tx, game.ID, game.HostID, ReminderHostWaitlist, now, version); err != nil {
		return err
	}
	tx.Touch(game.ID)
	s.publishAfterCommit(tx, game.ShortCode, EventSeatTaken, version, true)
	return nil
}

// rememberFull 给座位上的每个人写一条 full 提醒。
func rememberFull(ctx context.Context, tx *store.Tx, game Game, version int64, now time.Time) error {
	users, err := userIDs(ctx, tx, `SELECT user_id FROM seats WHERE game_id = ? ORDER BY seat_no`, game.ID)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := addReminder(ctx, tx, game.ID, u, ReminderFull, now, version); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 退出 / 移出
// ---------------------------------------------------------------------------

// Leave 退出（§5.1）。
func (s *Service) Leave(ctx context.Context, code, userID string) (*Detail, error) {
	if !s.joinLimiter.Allow(userID) {
		return nil, ErrRateLimited
	}

	now := s.clk.Now()
	var detail *Detail
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		// 已经结束或取消的局是终态：不能再退出，否则座位会被删掉、
		// 而且一旦「没什么人剩下」还会把状态改回 open，把历史局复活（审计发现）。
		if !game.Open() {
			return ErrClosed
		}
		if _, err := s.leaveInTx(ctx, tx, game, userID, now); err != nil {
			return err
		}
		detail, err = loadDetailTx(ctx, tx, code, userID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// Kick 局头移出某个人：效果等同于他自己退出，另外写入 game_bans 并记一条 kicked（§5.1）。
func (s *Service) Kick(ctx context.Context, code, hostID, targetID string) (*Detail, error) {
	if hostID == targetID {
		return nil, ErrHostCantKickSelf
	}

	now := s.clk.Now()
	var detail *Detail
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if game.HostID != hostID {
			return ErrNotHost
		}
		if !game.Open() {
			return ErrClosed
		}
		inGame, err := s.leaveInTx(ctx, tx, game, targetID, now)
		if err != nil {
			return err
		}
		// 不在这一局里的人不能被「移出」：不写 ban，也不发通知。
		// 否则任何人都能拿白名单里的 user id 给任意用户推送「你被移出了」。
		if !inGame {
			return ErrNotInGame
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO game_bans (game_id, user_id) VALUES (?, ?)`,
			game.ID, targetID); err != nil {
			return err
		}
		version, err := currentVersion(ctx, tx, game.ID)
		if err != nil {
			return err
		}
		if err := addReminder(ctx, tx, game.ID, targetID, ReminderKicked, now, version); err != nil {
			return err
		}
		detail, err = loadDetailTx(ctx, tx, code, hostID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// 返回的 bool 说明这个人本来是否在这一局里——Kick 靠它判断该不该写 ban、发通知。
func (s *Service) leaveInTx(ctx context.Context, tx *store.Tx, game Game, userID string, now time.Time) (bool, error) {
	waiting, err := isWaitlisted(ctx, tx, game.ID, userID)
	if err != nil {
		return false, err
	}
	if waiting {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM waitlist WHERE game_id = ? AND user_id = ?`, game.ID, userID); err != nil {
			return false, err
		}
		version, err := bumpVersion(ctx, tx, game.ID)
		if err != nil {
			return false, err
		}
		tx.Touch(game.ID)
		s.publishAfterCommit(tx, game.ShortCode, EventSeatLeft, version, true)
		return true, nil
	}

	var seatNo int
	err = tx.QueryRowContext(ctx,
		`SELECT seat_no FROM seats WHERE game_id = ? AND user_id = ?`, game.ID, userID).Scan(&seatNo)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil // 本来就不在这一局里，什么也不用做
	}
	if err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM seats WHERE game_id = ? AND seat_no = ?`, game.ID, seatNo); err != nil {
		return false, err
	}
	// 他不用来了，作废他的开局提醒。
	if err := dropReminders(ctx, tx, game.ID, userID, ReminderTMinus1h, now); err != nil {
		return false, err
	}

	version, err := bumpVersion(ctx, tx, game.ID)
	if err != nil {
		return false, err
	}

	// 候补自动补位：队首坐进刚空出来的座号，状态保持 full（§5.1）。
	promoted, err := s.promoteHead(ctx, tx, game, seatNo, version, now)
	if err != nil {
		return false, err
	}

	remaining, err := countSeats(ctx, tx, game.ID)
	if err != nil {
		return false, err
	}

	if !promoted && remaining < Capacity {
		// 候补为空：从 full 回到 open，并告诉大家有人走了。
		// `AND status = 'full'` 是兜底：只有真的是满员的局才需要从 full 退回来，
		// 免得把别的状态（例如已经 done/cancelled 的）误改成 open。
		if _, err := tx.ExecContext(ctx, `
			UPDATE games SET status = 'open', version = version + 1
			 WHERE id = ? AND status = 'full'`, game.ID); err != nil {
			return false, err
		}
		version, err = currentVersion(ctx, tx, game.ID)
		if err != nil {
			return false, err
		}
		if err := notifySeated(ctx, tx, game, ReminderSeatLeft, version, now); err != nil {
			return false, err
		}
	}

	// 局头退出：先补位，再把局头转给座位上最早进来的人；没人了就把局取消（§5.1）。
	if game.HostID == userID {
		if err := s.transferHost(ctx, tx, game, version, now); err != nil {
			return false, err
		}
	}

	tx.Touch(game.ID)
	s.publishAfterCommit(tx, game.ShortCode, EventSeatLeft, version, true)
	return true, nil
}

// promoteHead 把候补队首补到 seatNo 上，返回是否补到了人。
func (s *Service) promoteHead(ctx context.Context, tx *store.Tx, game Game, seatNo int, version int64, now time.Time) (bool, error) {
	var userID string
	err := tx.QueryRowContext(ctx, `
		SELECT user_id FROM waitlist
		 WHERE game_id = ? ORDER BY created_at ASC, rowid ASC LIMIT 1`, game.ID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM waitlist WHERE game_id = ? AND user_id = ?`, game.ID, userID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, ?)`,
		game.ID, seatNo, userID, now.Unix()); err != nil {
		return false, err
	}

	if err := addReminder(ctx, tx, game.ID, userID, ReminderPromoted, now, version); err != nil {
		return false, err
	}
	if game.StartsAt.Sub(now) > time.Hour {
		if err := addReminder(ctx, tx, game.ID, userID, ReminderTMinus1h,
			game.StartsAt.Add(-time.Hour), version); err != nil {
			return false, err
		}
	}
	return true, nil
}

// transferHost 把局头转给座位上最早进来的人；座位上没人了就取消这一局（§5.1）。
func (s *Service) transferHost(ctx context.Context, tx *store.Tx, game Game, _ int64, now time.Time) error {
	var newHost string
	err := tx.QueryRowContext(ctx, `
		SELECT user_id FROM seats WHERE game_id = ?
		 ORDER BY created_at ASC, rowid ASC LIMIT 1`, game.ID).Scan(&newHost)
	if errors.Is(err, sql.ErrNoRows) {
		return s.cancelInTx(ctx, tx, game, now)
	}
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE games SET host_id = ?, version = version + 1 WHERE id = ?`, newHost, game.ID); err != nil {
		return err
	}
	version, err := currentVersion(ctx, tx, game.ID)
	if err != nil {
		return err
	}

	if err := addReminder(ctx, tx, game.ID, newHost, ReminderHostTransfer, now, version); err != nil {
		return err
	}
	others, err := userIDs(ctx, tx,
		`SELECT user_id FROM seats WHERE game_id = ? AND user_id <> ?`, game.ID, newHost)
	if err != nil {
		return err
	}
	for _, u := range others {
		if err := addReminder(ctx, tx, game.ID, u, ReminderHostChanged, now, version); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 取消 / 结束
// ---------------------------------------------------------------------------

// Cancel 由局头取消（§5.1）。
func (s *Service) Cancel(ctx context.Context, code, hostID string) error {
	now := s.clk.Now()
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if game.HostID != hostID {
			return ErrNotHost
		}
		// 已经取消过的局：幂等地成功，但**什么都不写**。
		// 否则每点一次取消都会把版本号 +1，而 dedupe_key 带着版本号，
		// 于是同一条「局取消了」会被重复发出去（微信里双击、超时重试都会踩到）。
		if game.Status == StatusCancelled {
			return nil
		}
		if !game.Open() {
			return ErrClosed
		}
		return s.cancelInTx(ctx, tx, game, now)
	})
}

// CancelAsSystem 取消一局，不需要局头身份。
// 给清理任务用（§13.1：状态 open 而且座位上只剩局头一人的局）。
func (s *Service) CancelAsSystem(ctx context.Context, gameID string) error {
	now := s.clk.Now()
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByID(ctx, tx, gameID)
		if err != nil {
			return err
		}
		if !game.Open() {
			return nil
		}
		return s.cancelInTx(ctx, tx, game, now)
	})
}

// End 把已经打完的局标成 done。
// 给清理任务用（§13.1：开局 6 小时后仍是 open/full 的局）。
func (s *Service) End(ctx context.Context, gameID string) error {
	now := s.clk.Now()
	return s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByID(ctx, tx, gameID)
		if err != nil {
			return err
		}
		if !game.Open() {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE games SET status = 'done', ended_at = ?, version = version + 1 WHERE id = ?`,
			now.Unix(), game.ID); err != nil {
			return err
		}
		// 结束后再发的开局提醒没有意义（§4 的不变量 5 也是这么要求的）。
		if err := dropReminders(ctx, tx, game.ID, "", ReminderTMinus1h, now); err != nil {
			return err
		}
		tx.Touch(game.ID)
		return nil
	})
}

// cancelInTx 是「取消」这件事的全部逻辑，局头取消和系统取消共用。
func (s *Service) cancelInTx(ctx context.Context, tx *store.Tx, game Game, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE games SET status = 'cancelled', ended_at = ?, version = version + 1 WHERE id = ?`,
		now.Unix(), game.ID); err != nil {
		return err
	}
	version, err := currentVersion(ctx, tx, game.ID)
	if err != nil {
		return err
	}

	// 所有还没发出去的提醒都作废。
	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox SET dropped_at = ?
		 WHERE game_id = ? AND sent_at IS NULL AND dropped_at IS NULL`,
		now.Unix(), game.ID); err != nil {
		return err
	}

	// 座位上的人和候补都收到取消通知。
	for _, query := range []string{
		`SELECT user_id FROM seats WHERE game_id = ?`,
		`SELECT user_id FROM waitlist WHERE game_id = ?`,
	} {
		users, err := userIDs(ctx, tx, query, game.ID)
		if err != nil {
			return err
		}
		for _, u := range users {
			if err := addReminder(ctx, tx, game.ID, u, ReminderCancelled, now, version); err != nil {
				return err
			}
		}
	}

	tx.Touch(game.ID)
	s.publishAfterCommit(tx, game.ShortCode, EventGameUpdated, version, true)
	return nil
}

// ---------------------------------------------------------------------------
// 修改
// ---------------------------------------------------------------------------

// Update 局头修改牌局。必须带对 If-Match 的版本号，不一致返回 VersionConflict（§5.1、§11.3）。
func (s *Service) Update(ctx context.Context, code, hostID string, patch Patch, ifVersion int64) (*Detail, error) {
	now := s.clk.Now()
	if err := patch.Normalize(now); err != nil {
		return nil, err
	}
	if err := s.checkPatchContent(ctx, code, hostID, patch); err != nil {
		return nil, err
	}

	var detail *Detail
	err := s.st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		game, err := loadGameByCode(ctx, tx, code)
		if err != nil {
			return err
		}
		if game.HostID != hostID {
			return ErrNotHost
		}
		// 已结束或已取消的局不能再改（不然能改掉历史局的地点、备注）。
		if !game.Open() {
			return ErrClosed
		}
		if game.Version != ifVersion {
			latest, err := loadDetailTx(ctx, tx, code, hostID)
			if err != nil {
				return err
			}
			return &VersionConflict{Latest: *latest}
		}

		sets := []string{}
		args := []any{}
		if patch.StartsAt != nil {
			sets = append(sets, "starts_at = ?")
			args = append(args, patch.StartsAt.Unix())
		}
		if patch.Venue != nil {
			sets = append(sets, "venue = ?")
			args = append(args, *patch.Venue)
		}
		if patch.Area != nil {
			sets = append(sets, "area = ?")
			args = append(args, *patch.Area)
		}
		if patch.Kind != nil {
			sets = append(sets, "kind = ?")
			args = append(args, *patch.Kind)
		}
		if patch.Note != nil {
			sets = append(sets, "note = ?")
			args = append(args, nullIfEmpty(*patch.Note))
		}
		// 时间或地点真的变了才记 rescheduled_at（#114）；原样重发不算。
		if patch.TimeChanged(game.StartsAt) || (patch.Venue != nil && *patch.Venue != game.Venue) {
			sets = append(sets, "rescheduled_at = ?")
			args = append(args, now.Unix())
		}
		sets = append(sets, "version = version + 1")

		query := "UPDATE games SET " + strings.Join(sets, ", ") + " WHERE id = ?"
		args = append(args, game.ID)
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
		version, err := currentVersion(ctx, tx, game.ID)
		if err != nil {
			return err
		}

		// 改了时间：作废旧的开局提醒，按新时间重排，并通知座位上的人（§5.1）。
		if patch.TimeChanged(game.StartsAt) {
			if err := dropReminders(ctx, tx, game.ID, "", ReminderTMinus1h, now); err != nil {
				return err
			}
			newStart := *patch.StartsAt
			users, err := userIDs(ctx, tx, `SELECT user_id FROM seats WHERE game_id = ?`, game.ID)
			if err != nil {
				return err
			}
			for _, u := range users {
				if newStart.Sub(now) > time.Hour {
					if err := addReminder(ctx, tx, game.ID, u, ReminderTMinus1h,
						newStart.Add(-time.Hour), version); err != nil {
						return err
					}
				}
				if err := addReminder(ctx, tx, game.ID, u, ReminderRescheduled, now, version); err != nil {
					return err
				}
			}
		}

		tx.Touch(game.ID)
		// game.updated 发到 game:{code}，同时通知牌局现在所在片区的首页（includeFeed）。
		s.publishAfterCommit(tx, code, EventGameUpdated, version, true)
		// 改了片区：原来那个片区的首页也要知道这局走了（条目里的 area 已经变了，客户端据此移除卡片）。
		// 以前这里给新片区重复发了一次，原片区反而收不到（#46）。
		if patch.Area != nil && *patch.Area != game.Area {
			s.publishFeedAfterCommit(tx, code, game.Area, EventFeedChanged, version)
		}

		detail, err = loadDetailTx(ctx, tx, code, hostID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return detail, nil
}

// ---------------------------------------------------------------------------
// 实时事件（§5.5）
// ---------------------------------------------------------------------------

// publishAfterCommit 登记一个提交后回调，把这一局的变化推出去。
//
// 回调在 COMMIT 成功之后才跑，所以读到的就是提交后的状态；事务回滚时它根本不会被调用，
// 客户端不会看到「幽灵座位」。includeFeed 为真时顺带通知这个片区的首页列表。
func (s *Service) publishAfterCommit(tx *store.Tx, code, eventType string, version int64, includeFeed bool) {
	tx.AfterCommit(func(ctx context.Context) {
		detail, err := s.loadDetail(ctx, s.st.Read(), code, "")
		if err != nil {
			slog.Error("推实时事件前读牌局失败", "code", code, "err", err)
			return
		}
		s.pub.Publish(Event{
			Topic: GameTopic(code),
			Type:  eventType,
			V:     version,
			Data:  seatData(detail),
		})
		if includeFeed {
			s.pub.Publish(Event{
				Topic: FeedTopic(detail.Game.Area),
				Type:  EventFeedChanged,
				V:     version,
				Data:  feedItem(detail),
			})
		}
	})
}

// publishFeedAfterCommit 只通知某个片区的首页列表（feed:{area}）。eventType 是 feed.game_added
// 或者 feed.game_changed。数据是提交后的列表条目（feedItem），首页据此直接插入或替换那一张卡片（#46）。
// area 可以和牌局现在的片区不同：局头把局改到别的片区时，用它通知原来的片区，客户端看到条目里的
// area 已经不是本片区，就把卡片移除。
func (s *Service) publishFeedAfterCommit(tx *store.Tx, code, area, eventType string, version int64) {
	tx.AfterCommit(func(ctx context.Context) {
		detail, err := s.loadDetail(ctx, s.st.Read(), code, "")
		if err != nil {
			slog.Error("推首页事件前读牌局失败", "code", code, "err", err)
			return
		}
		s.pub.Publish(Event{Topic: FeedTopic(area), Type: eventType, V: version, Data: feedItem(detail)})
	})
}

// feedItem 是 feed:* 事件的数据：和 GET /api/games 列表里的一项完全相同，另外带上 version。
// 片区里每一局的版本号各自独立，客户端按 short_code 比较 version 去重（§5.5）。
func feedItem(d *Detail) map[string]any {
	item := toSummariesJSON([]Summary{{Game: d.Game, HostName: d.HostName, Seats: d.Seats, Missing: d.Missing}})[0] // MyRole 留空：广播不带按身份算的字段
	item["version"] = d.Game.Version
	return item
}

// seatData 是事件里带的座位快照（§5.5：客户端据此直接更新，不用再拉一次 REST）。
func seatData(d *Detail) map[string]any {
	seats := make([]map[string]any, 0, len(d.Seats))
	for _, seat := range d.Seats {
		seats = append(seats, map[string]any{
			"seat_no":  seat.SeatNo,
			"user_id":  seat.UserID,
			"nickname": seat.Nickname,
		})
	}
	waitlist := make([]map[string]any, 0, len(d.Waitlist))
	for _, entry := range d.Waitlist {
		waitlist = append(waitlist, map[string]any{
			"user_id":  entry.UserID,
			"nickname": entry.Nickname,
			"position": entry.Position,
		})
	}
	return map[string]any{
		"status":   d.Game.Status,
		"seats":    seats,
		"waitlist": waitlist,
		"missing":  d.Missing,
	}
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func newID(now time.Time) string {
	var b [16]byte
	ms := uint64(now.UnixMilli())
	b[0] = byte(ms >> 40)
	b[1] = byte(ms >> 32)
	b[2] = byte(ms >> 24)
	b[3] = byte(ms >> 16)
	b[4] = byte(ms >> 8)
	b[5] = byte(ms)
	if _, err := rand.Read(b[6:]); err != nil {
		panic(fmt.Sprintf("生成标识失败: %v", err))
	}
	return crockford.EncodeToString(b[:])
}

// uniqueShortCode 生成一个没被占用的 6 位短码（§10.9：不能顺着编号遍历）。
func uniqueShortCode(ctx context.Context, tx *store.Tx) (string, error) {
	var raw [4]byte
	for attempt := 0; attempt < 8; attempt++ {
		if _, err := rand.Read(raw[:]); err != nil {
			return "", err
		}
		code := crockford.EncodeToString(raw[:])[:shortCodeLen]
		var taken int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM games WHERE short_code = ?`, code).Scan(&taken); err != nil {
			return "", err
		}
		if taken == 0 {
			return code, nil
		}
	}
	return "", errors.New("连续 8 次都生成了已被占用的短码")
}

func bumpVersion(ctx context.Context, tx *store.Tx, gameID string) (int64, error) {
	if _, err := tx.ExecContext(ctx,
		`UPDATE games SET version = version + 1 WHERE id = ?`, gameID); err != nil {
		return 0, err
	}
	return currentVersion(ctx, tx, gameID)
}

func currentVersion(ctx context.Context, q queryer, gameID string) (int64, error) {
	var v int64
	err := q.QueryRowContext(ctx, `SELECT version FROM games WHERE id = ?`, gameID).Scan(&v)
	return v, err
}

func userIDs(ctx context.Context, q queryer, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func notifySeated(ctx context.Context, tx *store.Tx, game Game, kind string, version int64, now time.Time) error {
	users, err := userIDs(ctx, tx, `SELECT user_id FROM seats WHERE game_id = ?`, game.ID)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := addReminder(ctx, tx, game.ID, u, kind, now, version); err != nil {
			return err
		}
	}
	return nil
}

// addReminder 往 outbox 写一条提醒（§5.3）。dedupe_key = 局 + 人 + 类型 + 局版本号，
// 所以同一个版本里同一件事只会写一条。发送是 notify 的后台任务干的，不在这里（§11.1）。
func addReminder(ctx context.Context, tx *store.Tx, gameID, userID, kind string, sendAt time.Time, version int64) error {
	key := fmt.Sprintf("%s|%s|%s|%d", gameID, userID, kind, version)
	_, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO outbox (game_id, user_id, kind, send_at, next_try, attempts, dedupe_key)
		VALUES (?, ?, ?, ?, ?, 0, ?)`,
		gameID, userID, kind, sendAt.Unix(), sendAt.Unix(), key)
	return err
}

// dropReminders 作废还没发出去的某类提醒；userID 为空表示这一局里所有人的。
func dropReminders(ctx context.Context, tx *store.Tx, gameID, userID, kind string, now time.Time) error {
	query := `UPDATE outbox SET dropped_at = ?
	           WHERE game_id = ? AND kind = ? AND sent_at IS NULL AND dropped_at IS NULL`
	args := []any{now.Unix(), gameID, kind}
	if userID != "" {
		query += ` AND user_id = ?`
		args = append(args, userID)
	}
	_, err := tx.ExecContext(ctx, query, args...)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// checkPatchContent 把改局里的地点、备注送微信内容安全检测（#146）。先用读连接确认是局头：
// 别人对任意一局发 PATCH 不该触发微信调用。是不是局头、版本号对不对，写事务里还会再判一次。
func (s *Service) checkPatchContent(ctx context.Context, code, hostID string, p Patch) error {
	if s.sec == nil || (p.Venue == nil && p.Note == nil) {
		return nil
	}
	var host string
	err := s.st.Read().QueryRowContext(ctx, `SELECT host_id FROM games WHERE short_code = ?`, code).Scan(&host)
	if err != nil || host != hostID {
		return nil // 交给写事务报 ErrNotFound / ErrNotHost
	}
	var fields []secheck.Field
	if p.Venue != nil {
		fields = append(fields, secheck.Field{Name: "venue", Text: *p.Venue})
	}
	if p.Note != nil {
		fields = append(fields, secheck.Field{Name: "note", Text: *p.Note})
	}
	return s.sec.CheckUser(ctx, hostID, secheck.SceneForum, fields...)
}
