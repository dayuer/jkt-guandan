package games

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dayuer/jkt-guandan/internal/store"
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
		hostName            string
	)
	err := q.QueryRowContext(ctx, `
		SELECT g.id, g.short_code, g.host_id, g.starts_at, g.area, g.venue, g.kind,
		       COALESCE(g.note, ''), g.status, g.version, g.created_at,
		       COALESCE(u.nickname, '')
		  FROM games g
		  LEFT JOIN users u ON u.id = g.host_id
		 WHERE g.short_code = ?`, code).
		Scan(&d.Game.ID, &d.Game.ShortCode, &d.Game.HostID, &startsAt, &d.Game.Area,
			&d.Game.Venue, &d.Game.Kind, &d.Game.Note, &d.Game.Status, &d.Game.Version,
			&createdAt, &hostName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("读牌局 %s: %w", code, err)
	}
	d.Game.StartsAt = time.Unix(startsAt, 0)
	d.Game.CreatedAt = time.Unix(createdAt, 0)
	d.HostName = hostName

	seats, err := q.QueryContext(ctx, `
		SELECT s.seat_no, s.user_id, COALESCE(u.nickname, ''), s.created_at
		  FROM seats s LEFT JOIN users u ON u.id = s.user_id
		 WHERE s.game_id = ? ORDER BY s.seat_no`, d.Game.ID)
	if err != nil {
		return nil, err
	}
	for seats.Next() {
		var (
			seat      Seat
			createdAt int64
		)
		if err := seats.Scan(&seat.SeatNo, &seat.UserID, &seat.Nickname, &createdAt); err != nil {
			seats.Close()
			return nil, err
		}
		seat.CreatedAt = time.Unix(createdAt, 0)
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
			}
		}
		for _, entry := range d.Waitlist {
			if entry.UserID == viewerID {
				d.Me = "waitlist"
				d.MyPosition = entry.Position
			}
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
	       COALESCE(g.note, ''), g.status, g.version, g.created_at
	  FROM games g`

func scanGame(row *sql.Row) (Game, error) {
	var (
		g                   Game
		startsAt, createdAt int64
	)
	err := row.Scan(&g.ID, &g.ShortCode, &g.HostID, &startsAt, &g.Area, &g.Venue,
		&g.Kind, &g.Note, &g.Status, &g.Version, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	if err != nil {
		return Game{}, err
	}
	g.StartsAt = time.Unix(startsAt, 0)
	g.CreatedAt = time.Unix(createdAt, 0)
	return g, nil
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

// heldSlots 数这个人还占着几个「未开始局」的座位或候补位（§10.8 的配额）。
func heldSlots(ctx context.Context, q queryer, userID string, now time.Time) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `
		SELECT
		  (SELECT count(*) FROM seats s JOIN games g ON g.id = s.game_id
		    WHERE s.user_id = ? AND g.starts_at > ? AND g.status IN ('open','full'))
		+ (SELECT count(*) FROM waitlist w JOIN games g ON g.id = w.game_id
		    WHERE w.user_id = ? AND g.starts_at > ? AND g.status IN ('open','full'))`,
		userID, now.Unix(), userID, now.Unix()).Scan(&n)
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
		// 下一个周六 00:00 到下周一 00:00。
		offset := (int(time.Saturday) - int(today.Weekday()) + 7) % 7
		sat := today.AddDate(0, 0, offset)
		return sat, sat.AddDate(0, 0, 2)
	default:
		return now, now.AddDate(0, 0, 7)
	}
}
