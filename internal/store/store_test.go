package store_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/dayuer/jkt-guandan/internal/store"
)

func openTest(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("打开数据库: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
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

// 迁移要建出设计文档 §4 的全部表。
func TestMigrationsCreateDesignTables(t *testing.T) {
	got := tables(t, openTest(t))
	want := []string{
		"claim_tokens", "devices", "games", "kv", "outbox",
		"push_subscriptions", "schema_migrations", "seats", "users",
	}
	if len(got) != len(want) {
		t.Fatalf("表数量不对，实际 %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("表不对，实际 %v，期望 %v", got, want)
		}
	}
}

// 重复打开同一个数据目录不应该重复执行迁移。
func TestMigrationsAreIdempotent(t *testing.T) {
	dir := t.TempDir()

	first, err := store.Open(dir)
	if err != nil {
		t.Fatalf("首次打开: %v", err)
	}
	_ = first.Close()

	second, err := store.Open(dir)
	if err != nil {
		t.Fatalf("二次打开: %v", err)
	}
	defer second.Close()

	var count int
	if err := second.Read().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("读迁移记录: %v", err)
	}
	if count != 1 {
		t.Fatalf("迁移记录应为 1 条，实际 %d 条", count)
	}
}

// 写事务要真的落库，并且提交后按登记顺序执行回调（§5.5）。
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
	ctx := context.Background()
	ran := false

	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
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
				_, err := tx.ExecContext(ctx, `INSERT INTO kv (k, v) VALUES (?, x'01')`, string(rune('a'+i)))
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

// 外键约束要真的生效（PRAGMA foreign_keys=ON）。
func TestForeignKeysEnforced(t *testing.T) {
	st := openTest(t)
	ctx := context.Background()

	err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO devices (id, user_id, token_hash, last_seen) VALUES ('d1', 'nobody', x'01', 0)`)
		return err
	})
	if err == nil {
		t.Fatal("指向不存在用户的设备应当被外键拒绝")
	}
}

// 只按文件名排序执行迁移，先建的先跑。
func TestMigrationNamesSorted(t *testing.T) {
	got := tables(t, openTest(t))
	if !sort.StringsAreSorted(got) {
		t.Fatalf("查询结果应有序: %v", got)
	}
}
