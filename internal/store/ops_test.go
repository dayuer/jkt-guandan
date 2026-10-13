package store_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/store"
)

var opsNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func scalar(t *testing.T, st *store.Store, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := st.Read().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("查询 %q: %v", query, err)
	}
	return n
}

func str(t *testing.T, st *store.Store, query string, args ...any) string {
	t.Helper()
	var s string
	if err := st.Read().QueryRow(query, args...).Scan(&s); err != nil {
		t.Fatalf("查询 %q: %v", query, err)
	}
	return s
}

// 快照是一个自包含、能通过校验的独立文件，写入快照之后的改动不会出现在里面（§13.3）。
func TestSnapshotIsConsistentAndVerifiable(t *testing.T) {
	st := openTest(t)
	seedUsers(t, st)
	dst := filepath.Join(t.TempDir(), "snap.db")

	if err := st.Snapshot(context.Background(), dst); err != nil {
		t.Fatalf("快照: %v", err)
	}
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u-late', '晚到', 0)`)

	info, err := store.VerifySnapshot(context.Background(), dst)
	if err != nil {
		t.Fatalf("校验快照: %v", err)
	}
	if n := len(info.Migrations); n < 2 || info.Migrations[n-1] != "0003_bans.sql" {
		t.Fatalf("快照里的迁移记录 %v", info.Migrations)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dst + suffix); err == nil {
			t.Errorf("快照应是单个文件，却留下了 %s", suffix)
		}
	}

	if err := st.Snapshot(context.Background(), dst); err == nil {
		t.Fatal("快照文件已存在时应报错")
	}
}

func TestVerifySnapshotRejectsGarbageAndForeignDB(t *testing.T) {
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.db")
	if err := os.WriteFile(garbage, []byte("not a database at all, just bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifySnapshot(context.Background(), garbage); err == nil {
		t.Fatal("乱码文件应校验失败")
	}

	// 规则和启动时一样：不认识的兼容迁移放行，不认识的 breaking 迁移拒绝。
	st := openTest(t)
	mustExec(t, st, `INSERT INTO schema_migrations (name, applied_at) VALUES ('9998_future_compat.sql', 0)`)
	compat := filepath.Join(dir, "compat.db")
	if err := st.Snapshot(context.Background(), compat); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifySnapshot(context.Background(), compat); err != nil {
		t.Fatalf("含不认识的兼容迁移的快照应放行，实际 %v", err)
	}

	mustExec(t, st, `INSERT INTO schema_migrations (name, applied_at, breaking) VALUES ('9999_future_breaking.sql', 0, 1)`)
	newer := filepath.Join(dir, "newer.db")
	if err := st.Snapshot(context.Background(), newer); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifySnapshot(context.Background(), newer); err == nil ||
		!strings.Contains(err.Error(), "9999_future_breaking.sql") {
		t.Fatalf("含不认识的 breaking 迁移的快照应拒绝，实际 %v", err)
	}
}

func newGameAt(t *testing.T, st *store.Store, id, host, status string, startsAt time.Time) {
	t.Helper()
	mustExec(t, st, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
		VALUES (?, ?, ?, ?, 'SCBD', 'Pacific Place', 'classic', ?, 0)`, id, "C"+strings.ToUpper(id), host, startsAt.Unix(), status)
}

// §13.1：开局 6 小时后 open/full → done；open 且只剩局头 → cancelled；未到 6 小时的不动。
func TestCleanupEndsStartedGames(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u3', 'c', 0), ('u4', 'd', 0)`)
	old := opsNow.Add(-7 * time.Hour)

	newGameAt(t, st, "g-two", host, "open", old) // 两个人 → done
	seat(t, st, "g-two", 1, host)
	seat(t, st, "g-two", 2, guest)

	newGameAt(t, st, "g-alone", host, "open", old) // 只剩局头 → cancelled
	seat(t, st, "g-alone", 1, host)

	newGameAt(t, st, "g-full", host, "open", old) // 满座 → done
	for i, u := range []string{host, guest, "u3", "u4"} {
		seat(t, st, "g-full", i+1, u)
	}
	mustExec(t, st, `UPDATE games SET status = 'full' WHERE id = 'g-full'`)

	newGameAt(t, st, "g-recent", host, "open", opsNow.Add(-5*time.Hour)) // 不到 6 小时
	seat(t, st, "g-recent", 1, host)

	// 一条发送时间在将来的开局提醒：结束时必须放弃，否则违反不变量 5。
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
		VALUES ('g-two', ?, 't_minus_1h', ?, ?, 'k-future')`, guest, opsNow.Add(time.Hour).Unix(), opsNow.Add(time.Hour).Unix())

	res, err := st.Cleanup(context.Background(), opsNow)
	if err != nil {
		t.Fatalf("清理: %v", err)
	}
	if res.GamesDone != 2 || res.GamesCancelled != 1 {
		t.Fatalf("结果 %+v", res)
	}
	for id, want := range map[string]string{"g-two": "done", "g-alone": "cancelled", "g-full": "done", "g-recent": "open"} {
		if got := str(t, st, `SELECT status FROM games WHERE id = ?`, id); got != want {
			t.Errorf("%s 状态 %s，期望 %s", id, got, want)
		}
	}
	if n := scalar(t, st, `SELECT count(*) FROM games WHERE status IN ('done','cancelled') AND ended_at = ? AND version = 2`, opsNow.Unix()); n != 3 {
		t.Errorf("结束的局应写上 ended_at 并 version+1，符合的只有 %d 个", n)
	}
	if n := scalar(t, st, `SELECT count(*) FROM outbox WHERE dropped_at IS NOT NULL`); n != 1 {
		t.Errorf("结束的局里将来的提醒应被放弃，实际 %d 条", n)
	}
	for _, id := range []string{"g-two", "g-alone", "g-full"} {
		if err := check(t, st, id); err != nil {
			t.Errorf("%s 清理后违反不变量: %v", id, err)
		}
	}

	// 再跑一次什么都不做。
	again, err := st.Cleanup(context.Background(), opsNow)
	if err != nil || again.GamesDone+again.GamesCancelled != 0 {
		t.Fatalf("第二次清理 %+v, %v", again, err)
	}
}

// 超过一批（100 局）也要全部处理完。
func TestCleanupEndsGamesAcrossBatches(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		for i := 0; i < 230; i++ {
			id := fmt.Sprintf("g%03d", i)
			if _, err := tx.ExecContext(ctx, `INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
				VALUES (?, ?, ?, ?, 'SCBD', 'v', 'classic', 'open', 0)`, id, "S"+id, host, opsNow.Add(-24*time.Hour).Unix()); err != nil {
				return err
			}
			for no, u := range []string{host, guest} {
				if _, err := tx.ExecContext(ctx, `INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES (?, ?, ?, 0)`, id, no+1, u); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := st.Cleanup(context.Background(), opsNow)
	if err != nil {
		t.Fatal(err)
	}
	if res.GamesDone != 230 {
		t.Fatalf("应结束 230 局，实际 %+v", res)
	}
}

func TestCleanupDeletesExpiredRows(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGameAt(t, st, "g1", host, "open", opsNow.Add(24*time.Hour))
	seat(t, st, "g1", 1, host)
	day := int64(24 * 3600)
	now := opsNow.Unix()

	mustExec(t, st, `INSERT INTO claim_tokens (token_hash, code, user_id, expires_at) VALUES
		(x'01', '1111', ?, ?), (x'02', '2222', ?, ?)`, host, now-2*day, host, now-3600)
	mustExec(t, st, `INSERT INTO idempotency_keys (key, user_id, route, status, response, created_at) VALUES
		('old', ?, 'r', 200, x'00', ?), ('new', ?, 'r', 200, x'00', ?)`, host, now-2*day, host, now-3600)
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, sent_at, dropped_at, dedupe_key) VALUES
		('g1', ?, 'full', 0, 0, ?, NULL, 'sent-old'),
		('g1', ?, 'full', 0, 0, NULL, ?, 'dropped-old'),
		('g1', ?, 'full', 0, 0, ?, NULL, 'sent-new'),
		('g1', ?, 'full', ?, ?, NULL, NULL, 'pending')`,
		host, now-31*day, host, now-31*day, host, now-day, guest, now+day, now+day)
	mustExec(t, st, `INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES
		('d-stale', ?, x'aa', ?), ('d-live', ?, x'bb', ?)`, guest, now-181*day, guest, now-day)
	mustExec(t, st, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id, device_id, created_at) VALUES
		('https://push/stale', 'p', 'a', ?, 'd-stale', 0), ('https://push/live', 'p', 'a', ?, 'd-live', 0)`, guest, guest)
	mustExec(t, st, `INSERT INTO events (at, name) VALUES (?, 'old'), (?, 'new')`, now-366*day, now-day)

	res, err := st.Cleanup(context.Background(), opsNow)
	if err != nil {
		t.Fatalf("清理: %v", err)
	}
	want := store.CleanupResult{ClaimTokens: 1, IdempotencyKeys: 1, Outbox: 2, Devices: 1, PushSubscriptions: 1, Events: 1}
	if res != want {
		t.Fatalf("结果 %+v，期望 %+v", res, want)
	}
	if got := str(t, st, `SELECT group_concat(dedupe_key) FROM (SELECT dedupe_key FROM outbox ORDER BY dedupe_key)`); got != "pending,sent-new" {
		t.Errorf("剩下的 outbox: %s", got)
	}
	if got := str(t, st, `SELECT id FROM devices`); got != "d-live" {
		t.Errorf("剩下的设备: %s", got)
	}
}

func TestOptimize(t *testing.T) {
	if err := openTest(t).Optimize(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestOutboxStats(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st)
	newGameAt(t, st, "g1", host, "open", opsNow.Add(24*time.Hour))
	seat(t, st, "g1", 1, host)
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, attempts, sent_at, dropped_at, dedupe_key) VALUES
		('g1', ?, 'full', 0, 0, 0, NULL, NULL, 'a'),
		('g1', ?, 'full', 0, 0, 2, NULL, NULL, 'b'),
		('g1', ?, 'full', 0, 0, 5, NULL, 1, 'c'),
		('g1', ?, 'full', 0, 0, 1, 1, NULL, 'd')`, host, host, host, host)
	got, err := st.OutboxStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != (store.OutboxStats{Pending: 2, Retrying: 1, Dropped: 1}) {
		t.Fatalf("%+v", got)
	}
}

func TestObserveLockWait(t *testing.T) {
	st := openTest(t)
	calls := 0
	st.ObserveLockWait(func(time.Duration) { calls++ })
	mustExec(t, st, `INSERT INTO kv (k, v) VALUES ('a', x'01')`)
	if calls != 1 {
		t.Fatalf("观察者应被调用 1 次，实际 %d", calls)
	}
}

func TestBanUserAndDevice(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	mustExec(t, st, `INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', ?, x'01', 0)`, guest)
	ctx := context.Background()

	if banned, _ := st.IsBanned(ctx, host, "d1"); banned {
		t.Fatal("还没封禁")
	}
	if kind, err := st.Ban(ctx, host, opsNow); err != nil || kind != "user" {
		t.Fatalf("封禁用户: %s %v", kind, err)
	}
	if kind, err := st.Ban(ctx, "d1", opsNow); err != nil || kind != "device" {
		t.Fatalf("封禁设备: %s %v", kind, err)
	}
	if _, err := st.Ban(ctx, "nobody", opsNow); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的 id 应返回 ErrNotFound，实际 %v", err)
	}
	for _, c := range []struct {
		user, device string
		want         bool
	}{{host, "", true}, {"", "d1", true}, {guest, "", false}, {guest, "d1", true}} {
		if got, err := st.IsBanned(ctx, c.user, c.device); err != nil || got != c.want {
			t.Errorf("IsBanned(%q, %q) = %v, %v", c.user, c.device, got, err)
		}
	}
}

func TestCancelGame(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGameAt(t, st, "g1", host, "open", opsNow.Add(24*time.Hour))
	seat(t, st, "g1", 1, host)
	seat(t, st, "g1", 2, guest)
	mustExec(t, st, `INSERT INTO outbox (game_id, user_id, kind, send_at, next_try, dedupe_key)
		VALUES ('g1', ?, 't_minus_1h', ?, ?, 'k')`, guest, opsNow.Add(23*time.Hour).Unix(), opsNow.Add(23*time.Hour).Unix())
	ctx := context.Background()

	if _, err := st.CancelGame(ctx, "CG1", opsNow); err != nil {
		t.Fatalf("取消: %v", err)
	}
	if got := str(t, st, `SELECT status FROM games WHERE id = 'g1'`); got != "cancelled" {
		t.Fatalf("状态 %s", got)
	}
	if n := scalar(t, st, `SELECT count(*) FROM outbox WHERE dropped_at IS NULL`); n != 0 {
		t.Fatalf("取消后还有 %d 条提醒没放弃", n)
	}
	if _, err := st.CancelGame(ctx, "CG1", opsNow); err == nil {
		t.Fatal("已取消的局不能再取消")
	}
	if _, err := st.CancelGame(ctx, "NOPE", opsNow); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的局应返回 ErrNotFound，实际 %v", err)
	}
}

// §14：注销后称呼换成「已注销牌友」，个人数据删掉，座位记录保留。
func TestDeleteUserData(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	newGameAt(t, st, "g1", host, "open", opsNow.Add(-24*time.Hour))
	seat(t, st, "g1", 1, host)
	seat(t, st, "g1", 2, guest)
	mustExec(t, st, `UPDATE users SET wa_phone = '+62811' WHERE id = ?`, guest)
	mustExec(t, st, `INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', ?, x'01', 0)`, guest)
	mustExec(t, st, `INSERT INTO push_subscriptions (endpoint, p256dh, auth, user_id, device_id, created_at)
		VALUES ('https://push/x', 'p', 'a', ?, 'd1', 0)`, guest)
	mustExec(t, st, `INSERT INTO claim_tokens (token_hash, code, user_id, expires_at) VALUES (x'01', '1234', ?, 0)`, guest)
	mustExec(t, st, `INSERT INTO follows (user_id, target_id, created_at) VALUES (?, ?, 0), (?, ?, 0)`, host, guest, guest, host)
	mustExec(t, st, `INSERT INTO events (at, user_id, name) VALUES (0, ?, 'seat_taken')`, guest)

	if err := st.DeleteUserData(context.Background(), guest); err != nil {
		t.Fatalf("删除: %v", err)
	}
	if got := str(t, st, `SELECT nickname || '|' || COALESCE(wa_phone, '') FROM users WHERE id = ?`, guest); got != store.DeletedNickname+"|" {
		t.Errorf("用户行 %q", got)
	}
	for q, want := range map[string]int64{
		`SELECT count(*) FROM devices`:                          0,
		`SELECT count(*) FROM push_subscriptions`:               0,
		`SELECT count(*) FROM claim_tokens`:                     0,
		`SELECT count(*) FROM follows`:                          0,
		`SELECT count(*) FROM events WHERE user_id IS NOT NULL`: 0,
		`SELECT count(*) FROM seats WHERE game_id = 'g1'`:       2,
	} {
		if got := scalar(t, st, q); got != want {
			t.Errorf("%s = %d，期望 %d", q, got, want)
		}
	}
	if err := st.DeleteUserData(context.Background(), "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的用户应返回 ErrNotFound，实际 %v", err)
	}
}

func TestStats(t *testing.T) {
	st := openTest(t)
	host, _ := seedUsers(t, st) // created_at = 0
	mustExec(t, st, `INSERT INTO users (id, nickname, created_at) VALUES ('u-new', 'n', ?)`, opsNow.Unix())
	newGameAt(t, st, "g1", host, "open", opsNow)
	seat(t, st, "g1", 1, host)
	mustExec(t, st, `UPDATE games SET created_at = ? WHERE id = 'g1'`, opsNow.Unix())
	mustExec(t, st, `INSERT INTO events (at, name) VALUES (?, 'seat_taken'), (?, 'seat_taken'), (?, 'push_enabled'), (0, 'old')`,
		opsNow.Unix(), opsNow.Unix(), opsNow.Unix())

	s, err := st.Stats(context.Background(), opsNow.Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if s.NewUsers != 1 || s.NewGames != 1 || s.GamesByState["open"] != 1 {
		t.Fatalf("%+v", s)
	}
	if len(s.Events) != 2 || s.Events[0] != (store.EventCount{Name: "seat_taken", Count: 2}) {
		t.Fatalf("事件 %+v", s.Events)
	}
}

// guandan seed 可以重复执行，写进去的局都满足不变量、都在将来。
func TestSeedIsIdempotentAndValid(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	first, err := st.Seed(ctx, opsNow)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if first.Users != 12 || first.Games != 5 {
		t.Fatalf("首次写入 %+v", first)
	}
	second, err := st.Seed(ctx, opsNow)
	if err != nil || second != (store.SeedResult{}) {
		t.Fatalf("重复执行应什么都不写：%+v %v", second, err)
	}
	if n := scalar(t, st, `SELECT count(*) FROM games WHERE starts_at <= ?`, opsNow.Unix()); n != 0 {
		t.Fatalf("有 %d 个示例局不在将来", n)
	}
	if got := str(t, st, `SELECT status FROM games WHERE short_code = 'SEED04'`); got != "full" {
		t.Fatalf("4 人局应是 full，实际 %s", got)
	}
	if got := str(t, st, `SELECT u.nickname FROM games g JOIN users u ON u.id = g.host_id WHERE g.short_code = 'SEED01'`); got != "老王" {
		t.Fatalf("SEED01 局头 %s", got)
	}
}

// 封禁一个已合并的旧 id，实际在用的身份也要被封（auditor 在 #34 的建议）。
func TestBanFollowsMergedInto(t *testing.T) {
	st := openTest(t)
	host, guest := seedUsers(t, st)
	mustExec(t, st, `UPDATE users SET merged_into = ? WHERE id = ?`, host, guest)
	if kind, err := st.Ban(context.Background(), guest, opsNow); err != nil || kind != "user" {
		t.Fatalf("封禁: %s %v", kind, err)
	}
	if banned, _ := st.IsBanned(context.Background(), host, ""); !banned {
		t.Fatal("合并后的最终身份也应被封禁")
	}
}
