package store

import (
	"context"
	"fmt"
	"time"
)

// 示例数据取自设计稿 design/guandan-offline 里的牌局和牌友（§13.5），
// 让 auditor 和 design 不用手动建数据就能在页面上看效果。

type seedGame struct {
	id, code  string
	dayOffset int    // 相对「今天」（雅加达时间）的天数
	clock     string // 雅加达时间 HH:MM
	area      string
	venue     string
	kind      string
	note      string
	host      string
	players   []string // 含局头，按入座顺序
}

var seedUsers = []struct{ id, nickname string }{
	{"seed-laowang", "老王"}, {"seed-lily", "Lily"}, {"seed-ajie", "阿杰"},
	{"seed-chenjie", "陈姐"}, {"seed-xiaozhou", "小周"}, {"seed-kevin", "Kevin"},
	{"seed-amay", "阿May"}, {"seed-daliu", "大刘"}, {"seed-tony", "Tony"},
	{"seed-xiaolin", "小林"}, {"seed-laoli", "老李"}, {"seed-afang", "阿芳"},
}

var seedGames = []seedGame{
	{"seed-g1", "SEED01", 0, "19:30", "SCBD", "Pacific Place · 鼎泰丰包间", "classic",
		"打到 A 为止，新手友好。停车 P2。", "seed-laowang", []string{"seed-laowang", "seed-lily", "seed-ajie"}},
	{"seed-g2", "SEED02", 0, "20:00", "PIK", "PIK Avenue · 茶室二楼", "no-level",
		"有茶有点心，打完一起宵夜。", "seed-chenjie", []string{"seed-chenjie", "seed-xiaozhou"}},
	{"seed-g3", "SEED03", 1, "14:00", "Central Park", "Central Park Mall · 咖啡厅", "teaching",
		"完全不会也可以来，我教。", "seed-kevin", []string{"seed-kevin"}},
	{"seed-g4", "SEED04", 2, "19:30", "Kemang", "Kemang Village 公寓", "classic",
		"", "seed-amay", []string{"seed-amay", "seed-daliu", "seed-tony", "seed-xiaolin"}},
	{"seed-g5", "SEED05", 4, "15:00", "Kelapa Gading", "MKG · 粤菜馆包间", "classic",
		"周末下午局，打到晚饭。", "seed-laoli", []string{"seed-laoli", "seed-afang"}},
}

// SeedResult 说明这次写入了多少。重复执行时已有的数据不动，计数为 0。
type SeedResult struct {
	Users, Games int
}

// Seed 写入示例数据（guandan seed）。可以重复执行：id 固定，已存在的跳过。
// 开局时间按雅加达时间的「今天 + 偏移」计算；已经过去的往后顺延一周，保证都在将来。
func (s *Store) Seed(ctx context.Context, now time.Time) (SeedResult, error) {
	wib, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		return SeedResult{}, fmt.Errorf("加载时区: %w", err)
	}
	var res SeedResult
	err = s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		res = SeedResult{}
		for _, u := range seedUsers {
			r, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO users (id, nickname, created_at) VALUES (?, ?, ?)`,
				u.id, u.nickname, now.Unix())
			if err != nil {
				return fmt.Errorf("写入示例用户 %s: %w", u.nickname, err)
			}
			n, _ := r.RowsAffected()
			res.Users += int(n)
		}

		local := now.In(wib)
		for _, g := range seedGames {
			var exists int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM games WHERE id = ?`, g.id).Scan(&exists); err != nil {
				return err
			}
			if exists > 0 {
				continue
			}
			hm, err := time.ParseInLocation("15:04", g.clock, wib)
			if err != nil {
				return err
			}
			starts := time.Date(local.Year(), local.Month(), local.Day()+g.dayOffset,
				hm.Hour(), hm.Minute(), 0, 0, wib)
			for !starts.After(now.Add(time.Hour)) {
				starts = starts.AddDate(0, 0, 7)
			}
			status := "open"
			if len(g.players) == 4 {
				status = "full"
			}
			var note any
			if g.note != "" {
				note = g.note
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, note, status, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				g.id, g.code, g.host, starts.Unix(), g.area, g.venue, g.kind, note, status, now.Unix()); err != nil {
				return fmt.Errorf("写入示例牌局 %s: %w", g.code, err)
			}
			for i, p := range g.players {
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, ?)`,
					g.id, i+1, p, now.Unix()); err != nil {
					return fmt.Errorf("写入示例座位 %s: %w", g.code, err)
				}
			}
			tx.Touch(g.id)
			res.Games++
		}
		return nil
	})
	return res, err
}
