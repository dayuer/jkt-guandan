package identity

import (
	"context"
	"testing"
	"time"

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
		kept, err = mergeUsers(ctx, tx, "u-other", "u-primary", time.Unix(1000, 0))
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
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary", time.Unix(1000, 0))
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
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary", time.Unix(1000, 0))
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

// 合并时「我的牌局」里删掉过的记录也要搬（#211）：两边删过同一局就只留一条，
// 各自删过的也要并过来 —— 不然合并之后那几局又会冒回历史列表里。
func TestMergeUsersMovesHiddenGames(t *testing.T) {
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
	for i, id := range []string{"u-primary", "u-other"} {
		exec(`INSERT INTO users (id, nickname, created_at) VALUES (?, ?, ?)`, id, id, 100+i)
	}
	for i, code := range []string{"g1", "g2", "g3"} {
		exec(`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
		      VALUES (?, ?, 'u-primary', 0, 'SCBD', 'Pacific Place', 'classic', 'done', ?)`, code, code, i)
	}
	exec(`INSERT INTO hidden_games (user_id, game_id, hidden_at) VALUES ('u-primary', 'g1', 1)`) // 保留方删过
	exec(`INSERT INTO hidden_games (user_id, game_id, hidden_at) VALUES ('u-other', 'g2', 2)`)   // 被合并方删过 → 要搬
	exec(`INSERT INTO hidden_games (user_id, game_id, hidden_at) VALUES ('u-primary', 'g3', 3)`)
	exec(`INSERT INTO hidden_games (user_id, game_id, hidden_at) VALUES ('u-other', 'g3', 3)`) // 两边都删过 → 只留一条

	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary", time.Unix(1000, 0))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := st.Read().Query(`SELECT user_id || '>' || game_id FROM hidden_games ORDER BY 1`)
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
	want := []string{"u-primary>g1", "u-primary>g2", "u-primary>g3"}
	if len(got) != len(want) {
		t.Fatalf("合并后的隐藏记录 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("合并后的隐藏记录 %v，期望 %v", got, want)
		}
	}
}

// 合并身份时练习局（#88）跟着走，而且**并入方那一局开着的要先结束**：
// `practice_games_one_open_per_user`（`user_id WHERE ended_at IS NULL`）会把「我正开着一局 +
// 他也没结束一局」这种搬法挡下来，所以搬之前先给并入方那局写上结束时间。
// 主身份自己开着的局**不动**（他先来的，那局还在打）。
func TestMergeUsersMovesPracticeGames(t *testing.T) {
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
	for i, id := range []string{"u-primary", "u-other"} {
		exec(`INSERT INTO users (id, nickname, created_at) VALUES (?, ?, ?)`, id, id, 100+i)
	}
	newPractice := func(id, user string, ended any) {
		exec(`INSERT INTO practice_games (id, user_id, level, seat, state, revision, created_at, ended_at)
		      VALUES (?, ?, 2, 1, '{}', 1, 1, ?)`, id, user, ended)
	}
	newPractice("p-primary-open", "u-primary", nil) // 主身份开着的 → 不动
	newPractice("p-other-open", "u-other", nil)     // 并入方开着的 → 要结束
	newPractice("p-other-done", "u-other", 500)     // 并入方已结束的 → 原样搬

	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := mergeUsers(ctx, tx, "u-other", "u-primary", time.Unix(1000, 0))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	rows, err := st.Read().Query(
		`SELECT id || '|' || user_id || '|' || COALESCE(ended_at, -1) FROM practice_games ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	want := []string{
		"p-other-done|u-primary|500",  // 搬过来，结束时间不动
		"p-other-open|u-primary|1000", // 搬过来，而且被结束（写的就是合并时的时间）
		"p-primary-open|u-primary|-1", // 主身份自己那局不受影响
	}
	if len(got) != len(want) {
		t.Fatalf("合并后的练习局 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("合并后的练习局 %v，期望 %v", got, want)
		}
	}
	// 「一人同时只有一局」仍然成立（两条路径各一条：主身份那局 + 并入方被结束的那局）。
	var open int
	if err := st.Read().QueryRow(
		`SELECT count(*) FROM practice_games WHERE user_id = 'u-primary' AND ended_at IS NULL`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if open != 1 {
		t.Fatalf("合并后主身份开着的练习局该只有 1 局，实际 %d 局", open)
	}
}
