package games_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/games"
)

// 终态的指纹：状态 + 座位 + 候补。只用来比较有没有变。
func fingerprint(t *testing.T, f *fixture, gameID string) string {
	t.Helper()

	var status string
	if err := f.st.Read().QueryRow(
		`SELECT status FROM games WHERE id = ?`, gameID).Scan(&status); err != nil {
		t.Fatalf("取状态: %v", err)
	}

	rows, err := f.st.Read().Query(
		`SELECT seat_no || ':' || user_id FROM seats WHERE game_id = ? ORDER BY seat_no`, gameID)
	if err != nil {
		t.Fatalf("取座位: %v", err)
	}
	defer rows.Close()

	out := status + "|"
	for rows.Next() {
		var one string
		if err := rows.Scan(&one); err != nil {
			t.Fatalf("扫描: %v", err)
		}
		out += one + ","
	}
	return out
}

// 回归测试（原审计 PoC `TestAuditPoC_LeaveReopensDoneGame`）：
// 已经结束的局是终态——座位上的人再点「退出」不能把它复活成 open，座位也不能被删掉。
func TestLeaveOnFinishedGameIsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	host, other := f.newUser("局头"), f.newUser("甲")
	code := f.newGame(host)
	if _, err := f.games.Join(ctx, code, other); err != nil {
		t.Fatalf("报名: %v", err)
	}

	var gameID string
	if err := f.st.Read().QueryRow(
		`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatalf("查牌局: %v", err)
	}

	f.fake.Advance(9 * time.Hour)
	if err := f.games.End(ctx, gameID); err != nil {
		t.Fatalf("结束这一局: %v", err)
	}
	before := fingerprint(t, f, gameID)

	if _, err := f.games.Leave(ctx, code, other); !errors.Is(err, games.ErrClosed) {
		t.Fatalf("已结束的局上退出应当返回 game_closed，实际 %v", err)
	}
	if after := fingerprint(t, f, gameID); after != before {
		t.Fatalf("终态的局不该被改动：%s -> %s", before, after)
	}
	if before[:4] != "done" {
		t.Fatalf("状态应当还是 done，实际 %s", before)
	}
}

// 已经取消的局同理，而且取消本身可以重复调用——但不能写任何东西。
func TestLeaveOnCancelledGameIsRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	host, other := f.newUser("局头"), f.newUser("甲")
	code := f.newGame(host)
	if _, err := f.games.Join(ctx, code, other); err != nil {
		t.Fatalf("报名: %v", err)
	}
	if err := f.games.Cancel(ctx, code, host); err != nil {
		t.Fatalf("取消: %v", err)
	}

	var gameID string
	if err := f.st.Read().QueryRow(
		`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	before := fingerprint(t, f, gameID)

	if _, err := f.games.Leave(ctx, code, other); !errors.Is(err, games.ErrClosed) {
		t.Fatalf("已取消的局上退出应当返回 game_closed，实际 %v", err)
	}
	if after := fingerprint(t, f, gameID); after != before {
		t.Fatalf("终态的局不该被改动：%s -> %s", before, after)
	}
}

// 回归测试（原审计 PoC `TestAuditPoC_DoubleCancelNotifiesTwice`）：
// 局头连点两次取消，只该产生一组 cancelled 提醒。
func TestDoubleCancelNotifiesOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	host := f.newUser("局头")
	code := f.newGame(host)
	for i := 0; i < 2; i++ {
		if _, err := f.games.Join(ctx, code, f.newUser(fmt.Sprintf("牌友%d", i))); err != nil {
			t.Fatalf("报名: %v", err)
		}
	}

	if err := f.games.Cancel(ctx, code, host); err != nil {
		t.Fatalf("第一次取消: %v", err)
	}

	var gameID string
	var versionAfterFirst int64
	if err := f.st.Read().QueryRow(
		`SELECT id, version FROM games WHERE short_code = ?`, code).Scan(&gameID, &versionAfterFirst); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	noticesAfterFirst := f.count(
		`SELECT count(*) FROM outbox WHERE game_id = ? AND kind = ?`, gameID, games.ReminderCancelled)

	// 第二次取消：幂等地成功，但什么都不写。
	if err := f.games.Cancel(ctx, code, host); err != nil {
		t.Fatalf("第二次取消应当幂等成功，实际 %v", err)
	}

	var versionAfterSecond int64
	if err := f.st.Read().QueryRow(
		`SELECT version FROM games WHERE id = ?`, gameID).Scan(&versionAfterSecond); err != nil {
		t.Fatalf("查版本: %v", err)
	}
	if versionAfterSecond != versionAfterFirst {
		t.Fatalf("重复取消不该推进版本号：%d -> %d", versionAfterFirst, versionAfterSecond)
	}
	if got := f.count(
		`SELECT count(*) FROM outbox WHERE game_id = ? AND kind = ?`, gameID, games.ReminderCancelled); got != noticesAfterFirst {
		t.Fatalf("重复取消不该多发通知：%d -> %d", noticesAfterFirst, got)
	}
}

// 结束或取消之后，报名、移出、修改都要被拒（终态守卫）。
func TestTerminalGamesRejectEveryMutation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for _, terminal := range []string{"done", "cancelled"} {
		terminal := terminal
		t.Run(terminal, func(t *testing.T) {
			host, other := f.newUser("局头"+terminal), f.newUser("甲"+terminal)
			code := f.newGame(host)
			if _, err := f.games.Join(ctx, code, other); err != nil {
				t.Fatalf("报名: %v", err)
			}

			var gameID string
			var version int64
			if err := f.st.Read().QueryRow(
				`SELECT id, version FROM games WHERE short_code = ?`, code).Scan(&gameID, &version); err != nil {
				t.Fatalf("查牌局: %v", err)
			}

			if terminal == "done" {
				f.fake.Advance(9 * time.Hour)
				if err := f.games.End(ctx, gameID); err != nil {
					t.Fatalf("结束: %v", err)
				}
			} else {
				if err := f.games.Cancel(ctx, code, host); err != nil {
					t.Fatalf("取消: %v", err)
				}
			}

			before := fingerprint(t, f, gameID)
			venue := "改个地点"
			latecomer := f.newUser("后来的" + terminal)

			checks := []struct {
				name string
				fn   func() error
			}{
				{"报名", func() error { _, err := f.games.Join(ctx, code, latecomer); return err }},
				{"退出", func() error { _, err := f.games.Leave(ctx, code, other); return err }},
				{"移出", func() error { _, err := f.games.Kick(ctx, code, host, other); return err }},
				{"修改", func() error {
					_, err := f.games.Update(ctx, code, host, games.Patch{Venue: &venue}, version)
					return err
				}},
			}
			for _, c := range checks {
				if err := c.fn(); !errors.Is(err, games.ErrClosed) {
					t.Fatalf("终态的局上「%s」应当返回 game_closed，实际 %v", c.name, err)
				}
			}
			if after := fingerprint(t, f, gameID); after != before {
				t.Fatalf("终态的局被改动了：%s -> %s", before, after)
			}
		})
	}
}

// 移出一个不在这一局里的人：404，不写 ban，也不发通知。
// 否则任何人都能拿白名单里的 user id 给任意用户推送「你被移出了」。
func TestKickOutsiderWritesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	host := f.newUser("局头")
	code := f.newGame(host)
	outsider := f.newUser("路人")

	var gameID string
	if err := f.st.Read().QueryRow(
		`SELECT id FROM games WHERE short_code = ?`, code).Scan(&gameID); err != nil {
		t.Fatalf("查牌局: %v", err)
	}

	if _, err := f.games.Kick(ctx, code, host, outsider); !errors.Is(err, games.ErrNotInGame) {
		t.Fatalf("移出不在局里的人应当返回 not_in_game，实际 %v", err)
	}
	if n := f.count(`SELECT count(*) FROM game_bans WHERE game_id = ? AND user_id = ?`, gameID, outsider); n != 0 {
		t.Fatalf("不该给不在局里的人写 ban，实际 %d 条", n)
	}
	if n := f.count(`SELECT count(*) FROM outbox WHERE game_id = ? AND user_id = ? AND kind = ?`,
		gameID, outsider, games.ReminderKicked); n != 0 {
		t.Fatalf("不该给不在局里的人发 kicked 通知，实际 %d 条", n)
	}
	// 他也没有因此被这一局挡住。
	if _, err := f.games.Join(ctx, code, outsider); err != nil {
		t.Fatalf("路人应当还能正常报名: %v", err)
	}
}

// 首页的日期筛选：「明天」不能把今天剩下的局也带进来。
func TestListDayFilter(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	host := f.newUser("局头")

	// 今天稍晚一点的一局。时钟不动，否则「今天」的参照点也跟着走了。
	today, err := f.games.Create(ctx, host, games.CreateInput{
		StartsAt: f.clk.Now().Add(30 * time.Minute),
		Area:     "SCBD", Venue: "今天", Kind: "classic",
	}, "")
	if err != nil {
		t.Fatalf("开今天的局: %v", err)
	}
	// 明天晚上的一局。
	tomorrow, err := f.games.Create(ctx, host, games.CreateInput{
		StartsAt: startOfDayWIB(f.clk.Now()).AddDate(0, 0, 1).Add(19 * time.Hour),
		Area:     "SCBD", Venue: "明天", Kind: "classic",
	}, "")
	if err != nil {
		t.Fatalf("开明天的局: %v", err)
	}

	// 「今天」不含明天那局。
	todayList, err := f.games.List(ctx, "SCBD", "today", 20, 0)
	if err != nil {
		t.Fatalf("今天的列表: %v", err)
	}
	if !hasCode(todayList, today.Game.ShortCode) {
		t.Fatalf("今天的列表里应当有今天的局，实际 %v", codes(todayList))
	}
	if hasCode(todayList, tomorrow.Game.ShortCode) {
		t.Fatalf("今天的列表里不该有明天的局，实际 %v", codes(todayList))
	}

	// 「明天」不含今天那局——它已经过去了。
	tomorrowList, err := f.games.List(ctx, "SCBD", "tomorrow", 20, 0)
	if err != nil {
		t.Fatalf("明天的列表: %v", err)
	}
	if !hasCode(tomorrowList, tomorrow.Game.ShortCode) {
		t.Fatalf("明天的列表里应当有明天的局，实际 %v", codes(tomorrowList))
	}
	if hasCode(tomorrowList, today.Game.ShortCode) {
		t.Fatalf("明天的列表里不该有已经过去的局，实际 %v", codes(tomorrowList))
	}
}

func hasCode(items []games.Summary, code string) bool {
	for _, s := range items {
		if s.Game.ShortCode == code {
			return true
		}
	}
	return false
}

func codes(items []games.Summary) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		out = append(out, s.Game.ShortCode)
	}
	return out
}

// assertTerminal 断言 done / cancelled 是终态：任何操作都不该成功，
// 状态和座位也一丝不变。属性测试每遇到一个终态的局就调它一次。
func assertTerminal(t *testing.T, f *fixture, code string, users []string) {
	t.Helper()
	ctx := context.Background()

	var gameID string
	var version int64
	if err := f.st.Read().QueryRow(
		`SELECT id, version FROM games WHERE short_code = ?`, code).Scan(&gameID, &version); err != nil {
		t.Fatalf("查牌局: %v", err)
	}
	before := fingerprint(t, f, gameID)
	host := hostOf(t, f, code)
	venue := "终态不该被改"

	// 移出的目标不能是局头本人——「不能移出自己」在事务之前就返回了，
	// 测不到终态守卫。
	target := ""
	for _, u := range users {
		if u != host {
			target = u
			break
		}
	}
	if target == "" {
		t.Fatal("测试需要至少一个不是局头的用户")
	}

	checks := []struct {
		name string
		fn   func() error
	}{
		{"报名", func() error { _, err := f.games.Join(ctx, code, users[0]); return err }},
		{"退出", func() error { _, err := f.games.Leave(ctx, code, users[0]); return err }},
		{"移出", func() error { _, err := f.games.Kick(ctx, code, host, target); return err }},
		{"修改", func() error {
			_, err := f.games.Update(ctx, code, host, games.Patch{Venue: &venue}, version)
			return err
		}},
	}
	for _, c := range checks {
		if err := c.fn(); !errors.Is(err, games.ErrClosed) {
			t.Fatalf("终态的局（%s）上「%s」应当返回 game_closed，实际 %v", before, c.name, err)
		}
	}
	if after := fingerprint(t, f, gameID); after != before {
		t.Fatalf("终态的局被改动了：%s -> %s", before, after)
	}
}

// startOfDayWIB 是测试里算「今天」用的：按 Asia/Jakarta 取当天 00:00。
func startOfDayWIB(now time.Time) time.Time {
	loc, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		panic(err)
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}
