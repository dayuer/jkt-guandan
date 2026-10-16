package identity

import (
	"context"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/store"
)

// mergeUsers 已经不由任何面向用户的接口触发了（§5.2 修订之后，claim 兑换遇到冲突
// 只会返回 409 让用户确认，确认后也只切换设备、不迁移数据）。它以后只给
// `guandan admin` 子命令用，所以这里直接测函数本身。
func TestMergeUsersMovesSeatsAndKeepsEarlierSeat(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("执行迁移: %v", err)
	}

	ctx := context.Background()
	mustExec := func(query string, args ...any) {
		t.Helper()
		err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, query, args...)
			return err
		})
		if err != nil {
			t.Fatalf("执行 %s: %v", query, err)
		}
	}

	// primary 先注册，other 后注册。
	mustExec(`INSERT INTO users (id, nickname, created_at) VALUES ('u-primary', '老王', 100)`)
	mustExec(`INSERT INTO users (id, nickname, created_at) VALUES ('u-other', '老王', 200)`)
	mustExec(`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
	          VALUES ('g1', 'c1', 'u-primary', 0, 'SCBD', 'Pacific Place', 'classic', 'open', 0)`)
	mustExec(`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
	          VALUES ('g2', 'c2', 'u-other', 0, 'SCBD', 'Pacific Place', 'classic', 'open', 0)`)
	// g1：两个身份都占了座，primary 的更早。
	mustExec(`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g1', 1, 'u-primary', 100)`)
	mustExec(`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g1', 2, 'u-other', 200)`)
	// g2：只有 other 占了座，应当搬过去，同时局头也要跟着搬。
	mustExec(`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g2', 1, 'u-other', 100)`)

	var kept string
	err = st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		kept, err = mergeUsers(ctx, tx, "u-other", "u-primary")
		return err
	})
	if err != nil {
		t.Fatalf("合并: %v", err)
	}
	if kept != "u-primary" {
		t.Fatalf("应当保留先注册的 u-primary，实际 %s", kept)
	}

	var count, seatNo int
	if err := st.Read().QueryRow(
		`SELECT count(*), COALESCE(min(seat_no), 0) FROM seats WHERE game_id = 'g1'`).
		Scan(&count, &seatNo); err != nil {
		t.Fatalf("查 g1: %v", err)
	}
	if count != 1 || seatNo != 1 {
		t.Fatalf("g1 应当只剩更早的那个座位，实际 %d 个、座位号 %d", count, seatNo)
	}

	var g2Owner, g2Host string
	if err := st.Read().QueryRow(`SELECT user_id FROM seats WHERE game_id = 'g2'`).Scan(&g2Owner); err != nil {
		t.Fatalf("查 g2 座位: %v", err)
	}
	if g2Owner != "u-primary" {
		t.Fatalf("g2 的座位应当搬到 u-primary，实际 %s", g2Owner)
	}
	if err := st.Read().QueryRow(`SELECT host_id FROM games WHERE id = 'g2'`).Scan(&g2Host); err != nil {
		t.Fatalf("查 g2 局头: %v", err)
	}
	if g2Host != "u-primary" {
		t.Fatalf("局头也要跟着迁，实际 %s", g2Host)
	}

	// 旧身份只标记，不删除。
	var mergedInto string
	if err := st.Read().QueryRow(
		`SELECT COALESCE(merged_into, '') FROM users WHERE id = 'u-other'`).Scan(&mergedInto); err != nil {
		t.Fatalf("查旧身份: %v", err)
	}
	if mergedInto != "u-primary" {
		t.Fatalf("旧身份应当指向 u-primary，实际 %q", mergedInto)
	}
}

// 合并时星标牌友两个方向都搬到保留的身份上（#108）：重复的只留一条，互相星标不会变成自己星标自己。
func TestMergeUsersMovesFollows(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, q, args...)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for i, id := range []string{"u-primary", "u-other", "u-x", "u-y"} {
		exec(`INSERT INTO users (id, nickname, created_at) VALUES (?, ?, ?)`, id, id, 100+i)
	}
	for _, f := range [][2]string{
		{"u-other", "u-x"},   // other 的星标 → 搬给 primary
		{"u-x", "u-other"},   // 别人星标 other → 改成星标 primary
		{"u-primary", "u-y"}, // 两边都星标了 y → 只留一条
		{"u-other", "u-y"},
		{"u-primary", "u-other"}, // 互相星标 → 合并后不能自己星标自己
		{"u-other", "u-primary"},
	} {
		exec(`INSERT INTO follows (user_id, target_id, created_at) VALUES (?, ?, 1)`, f[0], f[1])
	}
	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Read().Query(`SELECT user_id || '>' || target_id FROM follows ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		got = append(got, s)
	}
	want := []string{"u-primary>u-x", "u-primary>u-y", "u-x>u-primary"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("合并后的星标 %v，期望 %v", got, want)
	}
}

// 合并时计分记录里的「谁开始的、谁记的、谁改的」也搬到保留的身份上（#165）。
func TestMergeUsersMovesScoreRecords(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			_, err := tx.ExecContext(ctx, q, args...)
			return err
		}); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users (id, nickname, created_at) VALUES ('u-primary', '老王', 100), ('u-other', '老王', 200)`)
	exec(`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
	      VALUES ('g1', 'c1', 'u-primary', 0, 'SCBD', 'Pacific Place', 'classic', 'done', 0)`)
	exec(`INSERT INTO score_matches (game_id, kind, started_by, started_at) VALUES ('g1', 'classic', 'u-other', 0)`)
	exec(`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at, edited_by, edited_at,
	                                seat1_user, seat2_user, seat3_user, seat4_user)
	      VALUES ('g1', 1, 1, 2, 'u-other', 0, 'u-other', 1, 'u-other', 'u-other', 'u-other', 'u-other')`)
	exec(`INSERT INTO score_seat_leavers (game_id, seat_no, user_id, left_at) VALUES ('g1', 3, 'u-other', 2)`)
	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var startedBy, recordedBy, editedBy string
	if err := st.Read().QueryRow(`SELECT m.started_by, r.recorded_by, r.edited_by
		FROM score_matches m JOIN score_rounds r ON r.game_id = m.game_id`).Scan(&startedBy, &recordedBy, &editedBy); err != nil {
		t.Fatal(err)
	}
	if startedBy != "u-primary" || recordedBy != "u-primary" || editedBy != "u-primary" {
		t.Fatalf("计分记录没搬：%s %s %s", startedBy, recordedBy, editedBy)
	}
	// 每局当时座位上的人（#172）。
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM score_rounds
		WHERE seat1_user = 'u-primary' AND seat2_user = 'u-primary' AND seat3_user = 'u-primary' AND seat4_user = 'u-primary'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("每局的座位没搬：%d %v", n, err)
	}
	// 离座记录也要搬，合并后由保留的身份回原座号（#168）。
	var leaver string
	if err := st.Read().QueryRow(`SELECT user_id FROM score_seat_leavers WHERE game_id = 'g1' AND seat_no = 3`).Scan(&leaver); err != nil || leaver != "u-primary" {
		t.Fatalf("离座记录没搬：%q %v", leaver, err)
	}
}
