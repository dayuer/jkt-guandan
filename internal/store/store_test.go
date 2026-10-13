package store_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/store"
	"github.com/dayuer/jkt-guandan/migrations"
)

func openTest(t *testing.T) *store.Store {
	t.Helper()
	return openDir(t, t.TempDir())
}

func openDir(t *testing.T, dir string) *store.Store {
	t.Helper()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("迁移: %v", err)
	}
	return st
}

func tables(t *testing.T, st *store.Store) []string {
	t.Helper()
	rows, err := st.Read().Query(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatalf("列出表: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("读取表名: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历表: %v", err)
	}
	return names
}

// 迁移要建出 §4 的全部表。
func TestMigrationsCreateDesignTables(t *testing.T) {
	got := tables(t, openTest(t))
	want := []string{
		"claim_tokens", "devices", "events", "follows", "game_bans", "games",
		"idempotency_keys", "invites", "kv", "outbox", "push_subscriptions",
		"schema_migrations", "seats", "users", "waitlist",
	}
	if len(got) != len(want) {
		t.Fatalf("表数量不对，实际 %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("表不对，实际 %v", got)
		}
	}
}

// §4 的两条触发器要在数据库层挡住「既占座又候补」。
func TestSeatAndWaitlistTriggersExist(t *testing.T) {
	st := openTest(t)
	rows, err := st.Read().Query(`SELECT name FROM sqlite_master WHERE type = 'trigger' ORDER BY name`)
	if err != nil {
		t.Fatalf("列出触发器: %v", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatalf("读触发器名: %v", err)
		}
		names = append(names, n)
	}
	want := []string{"seat_not_waitlisted", "waitlist_not_seated"}
	if len(names) != len(want) {
		t.Fatalf("触发器不对：%v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("触发器不对：%v", names)
		}
	}
}

// 重复打开同一个数据目录不应该重复执行迁移。
func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()
	_ = openDir(t, dir).Close()
	second := openDir(t, dir)

	// 期望条数按内嵌的迁移文件数算，别写死——加一个迁移文件就会过时。
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("列出迁移: %v", err)
	}
	want := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			want++
		}
	}

	var count int
	if err := second.Read().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("读迁移记录: %v", err)
	}
	if count != want {
		t.Fatalf("迁移记录应为 %d 条（迁移文件数），实际 %d 条", want, count)
	}
}

// §12.1：Open 只打开数据库，迁移要等 QuickCheck 之后由调用方显式执行。
func TestOpenDoesNotMigrate(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	defer st.Close()

	if err := st.QuickCheck(context.Background()); err != nil {
		t.Fatalf("新库的 quick_check 应该通过: %v", err)
	}
	if got := tables(t, st); len(got) != 0 {
		t.Fatalf("Open 不该建表，实际 %v", got)
	}
}

// 库里有本程序不认识的迁移（降级）时拒绝启动。
func TestMigrateRefusesUnknownMigration(t *testing.T) {
	dir := t.TempDir()
	st := openDir(t, dir)
	mustExec(t, st, `INSERT INTO schema_migrations (name, applied_at) VALUES ('9999_from_the_future.sql', 0)`)
	_ = st.Close()

	again, err := store.Open(dir)
	if err != nil {
		t.Fatalf("重新打开: %v", err)
	}
	defer again.Close()
	err = again.Migrate(context.Background())
	if err == nil || !contains(err.Error(), "9999_from_the_future.sql") {
		t.Fatalf("应当拒绝不认识的迁移，实际 %v", err)
	}
}

func TestQuickCheckOnFreshDatabase(t *testing.T) {
	st := openTest(t)
	if err := st.QuickCheck(context.Background()); err != nil {
		t.Fatalf("quick_check 失败: %v", err)
	}
}

// 写事务要真的落库，并且提交后按登记顺序执行回调（§5.5、§11.1）。
func TestWriteCommitsAndRunsAfterCommitInOrder(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	var order []string

	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES ('vapid', x'01')`); err != nil {
			return err
		}
		tx.AfterCommit(func(context.Context) { order = append(order, "first") })
		tx.AfterCommit(func(context.Context) { order = append(order, "second") })
		return nil
	})
	if err != nil {
		t.Fatalf("写事务: %v", err)
	}

	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM kv WHERE k = 'vapid'`).Scan(&n); err != nil {
		t.Fatalf("查询: %v", err)
	}
	if n != 1 {
		t.Fatalf("数据没落库，kv 里 %d 行", n)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("回调执行顺序不对: %v", order)
	}
}

// 事务回滚时既不能留下数据，也不能执行回调——否则会发出「幽灵座位」（§5.5）。
func TestWriteRollbackDiscardsDataAndAfterCommit(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()
	boom := errors.New("业务校验失败")
	called := false

	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES ('ghost', x'01')`); err != nil {
			return err
		}
		tx.AfterCommit(func(context.Context) { called = true })
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("应返回原始错误，实际 %v", err)
	}

	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM kv WHERE k = 'ghost'`).Scan(&n); err != nil {
		t.Fatalf("查询: %v", err)
	}
	if n != 0 {
		t.Fatalf("回滚后不该有数据，kv 里 %d 行", n)
	}
	if called {
		t.Fatal("回滚后不该执行提交后回调")
	}
}

// 一个回调 panic 不能连累后面的回调，也不能把请求打挂。
func TestAfterCommitPanicDoesNotStopLaterCallbacks(t *testing.T) {
	st := openTest(t)
	ran := false

	err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		tx.AfterCommit(func(context.Context) { panic("订阅者炸了") })
		tx.AfterCommit(func(context.Context) { ran = true })
		return nil
	})
	if err != nil {
		t.Fatalf("写事务: %v", err)
	}
	if !ran {
		t.Fatal("panic 之后的回调也应该执行")
	}
}

// 写连接池只有 1 条连接，并发写应当被排队而不是报 SQLITE_BUSY。
func TestConcurrentWritesAreSerialized(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	const writers = 20
	var wg sync.WaitGroup
	errs := make([]error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, x'01')`, fmt.Sprintf("k%d", i))
				return err
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("第 %d 个写入失败: %v", i, err)
		}
	}
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM kv`).Scan(&n); err != nil {
		t.Fatalf("查询: %v", err)
	}
	if n != writers {
		t.Fatalf("应有 %d 行，实际 %d 行", writers, n)
	}
}

// 等写锁超过 2 秒要返回 ErrBusy，而不是无限排队（§11.1、§12.3）。
func TestWriteReturnsBusyWhenLockHeldTooLong(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	release := make(chan struct{})
	held := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held // 第一个事务已经拿到写锁

	start := time.Now()
	err := st.Write(ctx, func(context.Context, *store.Tx) error { return nil })
	elapsed := time.Since(start)

	close(release)
	<-firstDone

	if !errors.Is(err, store.ErrBusy) {
		t.Fatalf("应返回 ErrBusy，实际 %v", err)
	}
	if elapsed < time.Second {
		t.Fatalf("应该等满 2 秒才放弃，实际只等了 %v", elapsed)
	}
}

func TestHeartbeatWritesAndCommits(t *testing.T) {
	st := openTest(t)
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	if err := st.Heartbeat(context.Background(), now); err != nil {
		t.Fatalf("心跳写事务失败: %v", err)
	}

	var v []byte
	if err := st.Read().QueryRow(`SELECT v FROM kv WHERE k = 'readyz'`).Scan(&v); err != nil {
		t.Fatalf("读心跳: %v", err)
	}
	if string(v) != fmt.Sprint(now.Unix()) {
		t.Fatalf("心跳值 %q", v)
	}
}

// 外键约束要真的生效（PRAGMA foreign_keys=ON）。
func TestForeignKeysEnforced(t *testing.T) {
	st := openTest(t)
	err := st.Write(context.Background(), func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', 'nobody', x'01', 0)`)
		return err
	})
	if err == nil {
		t.Fatal("指向不存在用户的设备应当被外键拒绝")
	}
}

// 数据目录的独占锁保证只有一个实例（§12.1 第 2 步）。
func TestLockDataDirIsExclusive(t *testing.T) {
	dir := t.TempDir()

	first, err := store.LockDataDir(dir)
	if err != nil {
		t.Fatalf("第一次加锁: %v", err)
	}

	if _, err := store.LockDataDir(dir); err == nil {
		t.Fatal("同一个目录第二次加锁应该失败")
	} else if !contains(err.Error(), "已被另一个实例占用") {
		t.Fatalf("错误信息应说明目录被占用：%v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("释放锁: %v", err)
	}

	second, err := store.LockDataDir(dir)
	if err != nil {
		t.Fatalf("释放后应该能再次加锁: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatalf("再次释放: %v", err)
	}
}

func TestFreeBytesReadsRealFilesystem(t *testing.T) {
	free, err := store.FreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("读磁盘空间: %v", err)
	}
	if free == 0 {
		t.Fatal("可用空间不该是 0")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// fn panic 之后，唯一的写连接不能卡在事务里：panic 要原样抛出，事务要回滚，
// 下一次 Write 必须能正常提交（PR #25 复审第 1 条）。
func TestWriteRecoversAfterPanic(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	func() {
		defer func() {
			if r := recover(); r != "业务函数炸了" {
				t.Fatalf("panic 应原样抛出，实际 %v", r)
			}
		}()
		_ = st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES ('ghost', x'01')`); err != nil {
				return err
			}
			panic("业务函数炸了")
		})
	}()

	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES ('after', x'01')`)
		return err
	}); err != nil {
		t.Fatalf("panic 之后的 Write 应该成功: %v", err)
	}
	if n := countKV(t, st, "ghost"); n != 0 {
		t.Fatalf("panic 的事务应当回滚，ghost 有 %d 行", n)
	}
	if n := countKV(t, st, "after"); n != 1 {
		t.Fatalf("after 应有 1 行，实际 %d", n)
	}
}

// COMMIT 失败时也要回滚。这里用延迟外键检查制造一次真实的 COMMIT 失败：
// SQLite 在 COMMIT 时才发现外键违规，报错后事务仍然开着（PR #25 复审第 1 条）。
func TestWriteRecoversAfterCommitFailure(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', 'nobody', x'01', 0)`)
		return err
	})
	if err == nil || !contains(err.Error(), "提交写事务") {
		t.Fatalf("COMMIT 应当失败，实际 %v", err)
	}

	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES ('after', x'01')`)
		return err
	}); err != nil {
		t.Fatalf("COMMIT 失败之后的 Write 应该成功: %v", err)
	}
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM devices`).Scan(&n); err != nil {
		t.Fatalf("查询: %v", err)
	}
	if n != 0 {
		t.Fatalf("提交失败的事务应当回滚，devices 有 %d 行", n)
	}
}

// 回滚本身也失败时，这条连接的状态不可信，不能还回池子。
// 用连接级的临时表做标记：连接被丢弃后，新连接上看不到它。
func TestWriteDiscardsConnWhenRollbackFails(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE conn_marker (x)`)
		return err
	}); err != nil {
		t.Fatalf("建标记表: %v", err)
	}

	boom := errors.New("业务失败")
	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		// 自己先把事务结束掉，Write 随后的 ROLLBACK 就会失败。
		if _, err := tx.ExecContext(ctx, `ROLLBACK`); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || !contains(err.Error(), "回滚也失败") {
		t.Fatalf("应返回原错误并说明回滚失败，实际 %v", err)
	}

	var markers int
	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT count(*) FROM temp.sqlite_master WHERE name = 'conn_marker'`).Scan(&markers)
	}); err != nil {
		t.Fatalf("回滚失败之后的 Write 应该成功: %v", err)
	}
	if markers != 0 {
		t.Fatal("回滚失败的连接应当被连接池丢弃，实际还在复用")
	}
}

// 读连接池是只读的：§11.1 只有 Write 一个写入口（PR #25 复审第 2 条）。
func TestReadPoolIsQueryOnly(t *testing.T) {
	st := openTest(t)
	if _, err := st.Read().Exec(`INSERT INTO kv (k, v) VALUES ('sneaky', x'01')`); err == nil {
		t.Fatal("通过 Read() 写入应当失败")
	}
	if n := countKV(t, st, "sneaky"); n != 0 {
		t.Fatalf("不该写进去，实际 %d 行", n)
	}
}

func countKV(t *testing.T, st *store.Store, k string) int {
	t.Helper()
	var n int
	if err := st.Read().QueryRow(`SELECT count(*) FROM kv WHERE k = ?`, k).Scan(&n); err != nil {
		t.Fatalf("查询 kv: %v", err)
	}
	return n
}
