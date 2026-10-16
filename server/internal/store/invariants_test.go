package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

func mustExec(t *testing.T, st *store.Store, query string, args ...any) {
	t.Helper()
	if err := tryExec(t, st, query, args...); err != nil {
		t.Fatalf("执行失败: %v\n%s", err, query)
	}
}

func tryExec(t *testing.T, st *store.Store, query string, args ...any) error {
	t.Helper()
	return st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	})
}

// seedUsers 建两个用户，返回局头和另一个人的 id。
func seedUsers(t *testing.T, st *store.Store) (host, guest string) {
	t.Helper()
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u-host', '老王', 0)`)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u-guest', 'Lily', 0)`)
	return "u-host", "u-guest"
}

func newGame(t *testing.T, st *store.Store, id, host, status string) {
	t.Helper()
	mustExec(t, st, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
		VALUES (?, ?, ?, 0, 'SCBD', 'Pacific Place', 'classic', ?, 0)`, id, "c"+id, host, status)
}

func seat(t *testing.T, st *store.Store, gameID string, no int, userID string) {
	t.Helper()
	mustExec(t, st, `INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, 0)`,
		gameID, no, userID)
}

func check(t *testing.T, st *store.Store, gameID string) error {
	t.Helper()
	return store.CheckInvariants(context.Background(), st.Read(), gameID)
}

func TestInvariantsAcceptValidOpenGame(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, host)

	if err := check(t, st, "g1"); err != nil {
		t.Fatalf("合法的牌局不该违规：%v", err)
	}
}

func TestInvariantsAcceptValidFullGame(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u3', '阿杰', 0)`)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u4', '小美', 0)`)
	newGame(t, st, "g1", host, "full")
	for i, u := range []string{host, guest, "u3", "u4"} {
		seat(t, st, "g1", i+1, u)
	}

	if err := check(t, st, "g1"); err != nil {
		t.Fatalf("满座的 full 牌局不该违规：%v", err)
	}
}

// 不变量 1：还空着座位却标成 full。
func TestInvariantsRejectFullWithEmptySeats(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "full")
	seat(t, st, "g1", 1, host)

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 1") {
		t.Fatalf("应报不变量 1，实际 %v", err)
	}
}

// 不变量 1：坐满了却还是 open。
func TestInvariantsRejectOpenWhenFull(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u3', '阿杰', 0)`)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u4', '小美', 0)`)
	newGame(t, st, "g1", host, "open")
	for i, u := range []string{host, guest, "u3", "u4"} {
		seat(t, st, "g1", i+1, u)
	}

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 1") {
		t.Fatalf("应报不变量 1，实际 %v", err)
	}
}

// 不变量 2：还有空座，却有人在候补。
func TestInvariantsRejectWaitlistWithoutFullHouse(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, host)
	mustExec(t, st, `INSERT INTO waitlist (game_id, user_id, created_at) VALUES ('g1', 'u-guest', 0)`)

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 2") {
		t.Fatalf("应报不变量 2，实际 %v（%s）", err, guest)
	}
}

// 不变量 3：局头不在座位上。
func TestInvariantsRejectHostNotSeated(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, guest)

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 3") {
		t.Fatalf("应报不变量 3，实际 %v", err)
	}
}

// 不变量 5：牌局已结束，还留着没发出去的开局提醒。
func TestInvariantsRejectReminderAfterGameEnded(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "cancelled")
	mustExec(t, st, `UPDATE games SET ended_at = 1000 WHERE id = 'g1'`)
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
		VALUES ('g1', ?, 't_minus_1h', 2000, 2000, 'k1')`, host)

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 5") {
		t.Fatalf("应报不变量 5，实际 %v", err)
	}
}

// ended_at 为空（老数据或写漏了）时 COALESCE 落到 0，没发的开局提醒一律算违规。
func TestInvariantsRejectReminderWhenEndedAtIsNull(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "done")
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
		VALUES ('g1', ?, 't_minus_1h', 1000, 1000, 'k1')`, host)

	err := check(t, st, "g1")
	if err == nil || !strings.Contains(err.Error(), "不变量 5") {
		t.Fatalf("应报不变量 5，实际 %v", err)
	}
}

// 结束时间之前就发掉的提醒不算违规。
func TestInvariantsAllowReminderSentBeforeEnd(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "cancelled")
	mustExec(t, st, `UPDATE games SET ended_at = 2000 WHERE id = 'g1'`)
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
		VALUES ('g1', ?, 't_minus_1h', 1000, 1000, 'k1')`, host)

	if err := check(t, st, "g1"); err != nil {
		t.Fatalf("不该违规：%v", err)
	}
}

func TestInvariantsIgnoreMissingGame(t *testing.T) {
	st := openTest(t)
	if err := check(t, st, "nope"); err != nil {
		t.Fatalf("局不存在时不该报错：%v", err)
	}
}

// §4 的触发器：不能既坐在座位上又在候补里。
func TestTriggersBlockSeatedAndWaitlisted(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, host)

	err := tryExec(t, st, `INSERT INTO waitlist (game_id, user_id, created_at) VALUES ('g1', ?, 0)`, host)
	if err == nil || !strings.Contains(err.Error(), "already_seated") {
		t.Fatalf("已入座的人进候补应被触发器拒绝，实际 %v", err)
	}

	mustExec(t, st, `INSERT INTO waitlist (game_id, user_id, created_at) VALUES ('g1', ?, 0)`, guest)
	err = tryExec(t, st, `INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g1', 2, ?, 0)`, guest)
	if err == nil || !strings.Contains(err.Error(), "already_waitlisted") {
		t.Fatalf("已在候补的人占座应被触发器拒绝，实际 %v", err)
	}
}

// 提交前的不变量检查：不通过就回滚，状态不会被改坏（§11.1）。
func TestWriteRollsBackOnInvariantViolation(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, host)

	err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		tx.Touch("g1")
		_, err := tx.ExecContext(ctx, `UPDATE games SET status = 'full' WHERE id = 'g1'`)
		return err
	})
	if err == nil {
		t.Fatal("只有 1 个人却改成 full，应被不变量拦下")
	}

	var status string
	if err := st.Read().QueryRow(`SELECT status FROM games WHERE id = 'g1'`).Scan(&status); err != nil {
		t.Fatalf("查询: %v", err)
	}
	if status != "open" {
		t.Fatalf("状态应回滚成 open，实际 %s", status)
	}
}

// Touch 登记过的局才检查；没登记的不影响写入。
func TestWriteWithoutTouchSkipsInvariantCheck(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")

	// 故意造一个违反不变量 3 的状态（局头没入座），但因为没 Touch 所以写成功。
	if err := tryExec(t, st, `UPDATE games SET status = 'full' WHERE id = 'g1'`); err != nil {
		t.Fatalf("没 Touch 时不该触发不变量检查：%v", err)
	}

	if err := check(t, st, "g1"); err == nil {
		t.Fatal("显式检查时应该报出不变量问题")
	}
}

// 不变量 6：计分的局号要从 1 连续（#165）。
func TestInvariantsScoreRoundsContiguous(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "open")
	seat(t, st, "g1", 1, host)
	mustExec(t, st, `INSERT INTO score_matches (game_id, kind, started_by, started_at)
		VALUES ('g1', 'classic', ?, 0)`, host)
	round := func(no int) {
		mustExec(t, st, `INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at)
			VALUES ('g1', ?, 1, 2, ?, 0)`, no, host)
	}
	round(1)
	round(2)
	if err := check(t, st, "g1"); err != nil {
		t.Fatalf("连续的局号不该违规：%v", err)
	}
	round(4)
	if err := check(t, st, "g1"); err == nil || !strings.Contains(err.Error(), "不变量 6") {
		t.Fatalf("应报不变量 6，实际 %v", err)
	}
}

// 计分表的约束在数据库层挡住不合法的事实（#165）：玩法、座号、名次、同一局号两条。
func TestScoreTablesConstraints(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGame(t, st, "g1", host, "full")
	for _, bad := range []string{
		`INSERT INTO score_matches (game_id, kind, started_by, started_at) VALUES ('g1', 'teaching', 'u-host', 0)`,
		// 没开始记分就不能记局（外键指向 score_matches）。
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 1, 2, 'u-host', 0)`,
	} {
		if err := tryExec(t, st, bad); err == nil {
			t.Fatalf("应当被拒绝：%s", bad)
		}
	}
	mustExec(t, st, `INSERT INTO score_matches (game_id, kind, started_by, started_at) VALUES ('g1', 'no-level', 'u-host', 0)`)
	for _, bad := range []string{
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 0, 1, 2, 'u-host', 0)`,
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 5, 2, 'u-host', 0)`,
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 1, 1, 'u-host', 0)`,
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 1, 2, 'nobody', 0)`,
	} {
		if err := tryExec(t, st, bad); err == nil {
			t.Fatalf("应当被拒绝：%s", bad)
		}
	}
	mustExec(t, st, `INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 1, 2, 'u-host', 0)`)
	if err := tryExec(t, st, `INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 2, 3, 'u-guest', 0)`); err == nil {
		t.Fatal("同一局号不能记两条")
	}
}
