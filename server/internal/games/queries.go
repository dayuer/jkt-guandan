package games

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// queryer 让同一段查询既能跑在写事务里，也能跑在读池上。
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func (s *Service) loadDetail(ctx context.Context, q queryer, code, viewerID string) (*Detail, error) {
	return loadDetailFrom(ctx, q, code, viewerID)
}

func loadDetailTx(ctx context.Context, tx *store.Tx, code, viewerID string) (*Detail, error) {
	return loadDetailFrom(ctx, tx, code, viewerID)
}

// loadDetailFrom 把 03 屏要的数据一次性读出来。
func loadDetailFrom(ctx context.Context, q queryer, code, viewerID string) (*Detail, error) {
	var (
		d                   Detail
		startsAt, createdAt int64
		rescheduledAt       int64
		hostSince           int64
		hostName            string
	)
	err := q.QueryRowContext(ctx, `
		SELECT g.id, g.short_code, g.host_id, g.starts_at, g.area, g.venue, g.kind,
		       COALESCE(g.note, ''), g.status, g.version, g.created_at,
		       COALESCE(g.rescheduled_at, 0), COALESCE(g.host_since, 0), COALESCE(u.nickname, '')
		  FROM games g
		  LEFT JOIN users u ON u.id = g.host_id
		 WHERE g.short_code = ?`, code).
		Scan(&d.Game.ID, &d.Game.ShortCode, &d.Game.HostID, &startsAt, &d.Game.Area,
			&d.Game.Venue, &d.Game.Kind, &d.Game.Note, &d.Game.Status, &d.Game.Version,
			&createdAt, &rescheduledAt, &hostSince, &hostName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读牌局 %s: %w", code, err)
	}
	d.Game.StartsAt = time.Unix(startsAt, 0)
	d.Game.CreatedAt = time.Unix(createdAt, 0)
	d.Game.RescheduledAt = unixOrZero(rescheduledAt)
	d.Game.HostSince = unixOrZero(hostSince)
	d.HostName = hostName

	seats, err := q.QueryContext(ctx, `
		SELECT s.seat_no, s.user_id, COALESCE(u.nickname, ''), s.created_at, COALESCE(s.waitlisted_at, 0)
		  FROM seats s LEFT JOIN users u ON u.id = s.user_id
		 WHERE s.game_id = ? ORDER BY s.seat_no`, d.Game.ID)
	if err != nil {
		return nil, err
	}
	for seats.Next() {
		var (
			seat                    Seat
			createdAt, waitlistedAt int64
		)
		if err := seats.Scan(&seat.SeatNo, &seat.UserID, &seat.Nickname, &createdAt, &waitlistedAt); err != nil {
			seats.Close()
			return nil, err
		}
		seat.CreatedAt = time.Unix(createdAt, 0)
		seat.WaitlistedAt = unixOrZero(waitlistedAt)
		d.Seats = append(d.Seats, seat)
	}
	seats.Close()
	if err := seats.Err(); err != nil {
		return nil, err
	}

	waiting, err := q.QueryContext(ctx, `
		SELECT w.user_id, COALESCE(u.nickname, ''), w.created_at
		  FROM waitlist w LEFT JOIN users u ON u.id = w.user_id
		 WHERE w.game_id = ? ORDER BY w.created_at ASC, w.rowid ASC`, d.Game.ID)
	if err != nil {
		return nil, err
	}
	for waiting.Next() {
		var (
			entry     WaitEntry
			createdAt int64
		)
		if err := waiting.Scan(&entry.UserID, &entry.Nickname, &createdAt); err != nil {
			waiting.Close()
			return nil, err
		}
		entry.CreatedAt = time.Unix(createdAt, 0)
		entry.Position = len(d.Waitlist) + 1
		d.Waitlist = append(d.Waitlist, entry)
	}
	waiting.Close()
	if err := waiting.Err(); err != nil {
		return nil, err
	}

	d.Missing = Capacity - len(d.Seats)
	if d.Missing < 0 {
		d.Missing = 0
	}

	d.Me = "none"
	if viewerID != "" {
		for _, seat := range d.Seats {
			if seat.UserID == viewerID {
				d.Me = "seat"
				d.MySeatNo = seat.SeatNo
				d.MyJoinedAt = seat.JoinedAt()
				d.MyPromotedAt = seat.PromotedAt()
			}
		}
		for _, entry := range d.Waitlist {
			if entry.UserID == viewerID {
				d.Me = "waitlist"
				d.MyPosition = entry.Position
				d.MyJoinedAt = entry.CreatedAt
			}
		}
		// 我接手当上局头的时刻（#189）：HostSince 是空的就说明我还是开局那个人本人
		// —— 「你成了局头」对他不是新闻。
		if d.Game.HostID == viewerID && !d.Game.HostSince.IsZero() {
			d.MyBecameHostAt = d.Game.HostSince
		}
		var inviter Seat
		err := q.QueryRowContext(ctx, `
			SELECT i.invited_by, COALESCE(u.nickname, '')
			  FROM invites i LEFT JOIN users u ON u.id = i.invited_by
			 WHERE i.game_id = ? AND i.user_id = ?`, d.Game.ID, viewerID).Scan(&inviter.UserID, &inviter.Nickname)
		switch {
		case err == nil:
			d.MyInviter = &inviter
		case !errors.Is(err, sql.ErrNoRows):
			return nil, fmt.Errorf("读邀请: %w", err)
		}
	}
	return &d, nil
}

// loadGameByCode 在读事务里按短码取一局。
func loadGameByCode(ctx context.Context, tx *store.Tx, code string) (Game, error) {
	return scanGame(tx.QueryRowContext(ctx, gameColumns+` WHERE g.short_code = ?`, code))
}

// loadGameByID 在读事务里按 id 取一局。
func loadGameByID(ctx context.Context, tx *store.Tx, id string) (Game, error) {
	return scanGame(tx.QueryRowContext(ctx, gameColumns+` WHERE g.id = ?`, id))
}

const gameColumns = `
	SELECT g.id, g.short_code, g.host_id, g.starts_at, g.area, g.venue, g.kind,
	       COALESCE(g.note, ''), g.status, g.version, g.created_at,
	       COALESCE(g.rescheduled_at, 0), COALESCE(g.host_since, 0)
	  FROM games g`

// scanner 是 *sql.Row 和 *sql.Rows 共有的部分，让同一个扫描逻辑两处都能用。
type scanner interface{ Scan(dest ...any) error }

func scanGame(row *sql.Row) (Game, error) {
	g, err := scanGameFrom(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	return g, err
}

func scanGameFrom(row scanner) (Game, error) {
	var (
		g                   Game
		startsAt, createdAt int64
		rescheduledAt       int64
		hostSince           int64
	)
	err := row.Scan(&g.ID, &g.ShortCode, &g.HostID, &startsAt, &g.Area, &g.Venue,
		&g.Kind, &g.Note, &g.Status, &g.Version, &createdAt, &rescheduledAt, &hostSince)
	if err != nil {
		return Game{}, err
	}
	g.StartsAt = time.Unix(startsAt, 0)
	g.CreatedAt = time.Unix(createdAt, 0)
	g.RescheduledAt = unixOrZero(rescheduledAt)
	g.HostSince = unixOrZero(hostSince)
	return g, nil
}

// unixOrZero 把库里的 0（空）读成零值时间，其余按 Unix 秒。
func unixOrZero(sec int64) time.Time {
	if sec == 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

func isSeated(ctx context.Context, q queryer, gameID, userID string) (bool, error) {
	return exists(ctx, q,
		`SELECT count(*) FROM seats WHERE game_id = ? AND user_id = ?`, gameID, userID)
}

func isWaitlisted(ctx context.Context, q queryer, gameID, userID string) (bool, error) {
	return exists(ctx, q,
		`SELECT count(*) FROM waitlist WHERE game_id = ? AND user_id = ?`, gameID, userID)
}

func isBanned(ctx context.Context, q queryer, gameID, userID string) (bool, error) {
	return exists(ctx, q,
		`SELECT count(*) FROM game_bans WHERE game_id = ? AND user_id = ?`, gameID, userID)
}

func exists(ctx context.Context, q queryer, query string, args ...any) (bool, error) {
	var n int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func countSeats(ctx context.Context, q queryer, gameID string) (int, error) {
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM seats WHERE game_id = ?`, gameID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// formerSeatNo 返回这个人记分开始以后离开过、现在还空着的座号；他也是这个座号最后一个离开的人
// （#168）。有好几个时取最近离开的那个。没有返回 0。
func formerSeatNo(ctx context.Context, q queryer, gameID, userID string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT seat_no FROM score_seat_leavers
		 WHERE game_id = ?1 AND user_id = ?2
		   AND seat_no NOT IN (SELECT seat_no FROM seats WHERE game_id = ?1)
		 ORDER BY left_at DESC, seat_no
		 LIMIT 1`, gameID, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// freeSeatNo 返回最小的空座号；坐满了返回 0。
func freeSeatNo(ctx context.Context, q queryer, gameID string) (int, error) {
	var n sql.NullInt64
	err := q.QueryRowContext(ctx, `
		SELECT MIN(n) FROM (
			SELECT 1 AS n UNION ALL SELECT 2 UNION ALL SELECT 3 UNION ALL SELECT 4
		) WHERE n NOT IN (SELECT seat_no FROM seats WHERE game_id = ?)`, gameID).Scan(&n)
	if err != nil {
		return 0, err
	}
	if !n.Valid {
		return 0, nil
	}
	return int(n.Int64), nil
}

// heldSlots 数这个人还占着几个「别人局里」的座位或候补位（§10.8）。
//
// 口径：**自己开的局不算**。这条配额是为了防止有人在别人的局里占一大堆座位；
// 自己能开几局已经由「每天最多 10 局」管着了，把局头自己那一座也算进来的话，
// 两条规则会互相压住——开到第 6 局时就会被这条拦住，「每天 10 局」永远到不了。
func heldSlots(ctx context.Context, q queryer, userID string, now time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM seats s JOIN games g ON g.id = s.game_id
		    WHERE s.user_id = ? AND g.host_id <> ? AND g.starts_at > ? AND g.status IN ('open','full'))
		+ (SELECT count(*) FROM waitlist w JOIN games g ON g.id = w.game_id
		    WHERE w.user_id = ? AND g.host_id <> ? AND g.starts_at > ? AND g.status IN ('open','full'))`,
		userID, userID, now.Unix(), userID, userID, now.Unix()).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

// gamesCreatedToday 数这个人今天（Asia/Jakarta）开了几局（§10.8）。
func gamesCreatedToday(ctx context.Context, q queryer, hostID string, now time.Time) (int, error) {
	start := startOfDay(now)
	var n int
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FROM games WHERE host_id = ? AND created_at >= ?`,
		hostID, start.Unix()).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// startOfDay 返回 now 所在那一天的起点，按 Asia/Jakarta 算（§13.2）。
func startOfDay(now time.Time) time.Time {
	local := now.In(jakarta)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, jakarta)
}

// dayRange 把 02 屏的筛选条件翻译成时间区间。day 为空表示「未来 7 天」。
func dayRange(now time.Time, day string) (from, to time.Time) {
	today := startOfDay(now)
	switch day {
	case "today":
		return today, today.AddDate(0, 0, 1)
	case "tomorrow":
		return today.AddDate(0, 0, 1), today.AddDate(0, 0, 2)
	case "weekend":
		// 本周末：周六 00:00 到下周一 00:00。
		// 周六和周日当天都要返回「当前这个」周末——周日再往后找会跳到下一个周六，
		// 今天的局反而看不见了。
		switch today.Weekday() {
		case time.Saturday:
			return today, today.AddDate(0, 0, 2)
		case time.Sunday:
			return today.AddDate(0, 0, -1), today.AddDate(0, 0, 1)
		default:
			offset := (int(time.Saturday) - int(today.Weekday()) + 7) % 7
			sat := today.AddDate(0, 0, offset)
			return sat, sat.AddDate(0, 0, 2)
		}
	default:
		return now, now.AddDate(0, 0, 7)
	}
}
