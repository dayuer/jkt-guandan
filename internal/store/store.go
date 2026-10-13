// Package store 是唯一写 SQL 的地方（docs/server-design.md §3）。
//
// 并发要点（§2、§4）：
//   - 写连接池只开 1 个连接，写事务一律用 BEGIN IMMEDIATE，拿不到写锁就直接失败而不是死等；
//   - 读另开一个连接池，WAL 模式下读写互不阻塞；
//   - 连接级 PRAGMA：journal_mode=WAL、busy_timeout=5000、foreign_keys=ON、synchronous=NORMAL。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // 纯 Go 驱动，所以 CGO_ENABLED=0，运行时镜像可以用 distroless/static
)

// DSN 的 PRAGMA 参数由 modernc.org/sqlite 驱动在每次建连接时执行。
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)"

// Store 持有两个连接池：write 只开 1 个连接，read 用于普通查询。
type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
}

// Open 打开（必要时创建）dataDir 下的数据库，并执行尚未应用的迁移。
func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, "guandan.db")
	dsn := "file:" + path + pragmas

	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	write.SetMaxOpenConns(1)

	read, err := sql.Open("sqlite", dsn)
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("打开只读连接池: %w", err)
	}

	if err := migrate(write); err != nil {
		write.Close()
		read.Close()
		return nil, err
	}
	return &Store{write: write, read: read, path: path}, nil
}

// Path 返回数据库文件的绝对路径，备份和日志会用到。
func (s *Store) Path() string { return s.path }

func (s *Store) Close() error {
	var first error
	if err := s.read.Close(); err != nil {
		first = err
	}
	if err := s.write.Close(); err != nil && first == nil {
		first = err
	}
	return first
}

// Ping 供 /healthz 使用。它走读连接池，不占用唯一那条写连接。
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.read.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Read 返回读连接池。只读查询用它，避免和写事务抢那条唯一的写连接。
func (s *Store) Read() *sql.DB { return s.read }

// Tx 是一次写事务。SQL 都通过它执行，事务级的事务后回调也登记在它上面。
type Tx struct {
	conn  *sql.Conn
	after []func(context.Context)
}

func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(ctx, query, args...)
}

func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(ctx, query, args...)
}

func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(ctx, query, args...)
}

// AfterCommit 登记一个「提交后回调」，按登记顺序执行，只在事务提交成功后执行。
// 事务回滚时回调全部丢弃——这是 §5.5 要求的语义：不能发布「幽灵座位」。
//
// 用法：games / identity 在写事务里改完数据后登记回调，#18 在这些回调里调用
// realtime.Publish。回调和事务共享 ctx，但会拿到一个不受请求取消影响的副本，
// 因为提交已经成功，事件就该发出去，哪怕客户端这时已经断开。
func (t *Tx) AfterCommit(fn func(context.Context)) {
	t.after = append(t.after, fn)
}

// Write 在同一个写事务里执行 fn。事务用 BEGIN IMMEDIATE 开启，开头就拿到写锁，
// 所以「读—判断—写」这一段天然串行，抢座之类的逻辑不需要额外加锁（§5.1）。
func (s *Store) Write(ctx context.Context, fn func(ctx context.Context, tx *Tx) error) error {
	conn, err := s.write.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("开启写事务: %w", err)
	}

	tx := &Tx{conn: conn}
	if err := fn(ctx, tx); err != nil {
		if _, rbErr := conn.ExecContext(ctx, "ROLLBACK"); rbErr != nil {
			return fmt.Errorf("%w（回滚也失败: %v）", err, rbErr)
		}
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("提交写事务: %w", err)
	}

	runAfterCommit(context.WithoutCancel(ctx), tx.after)
	return nil
}

// runAfterCommit 依次执行登记过的回调。提交已经成功，回调里的问题不能再改变这次请求的结果，
// 所以单个回调 panic 会被拦下并记录，后面的回调照常执行。
func runAfterCommit(ctx context.Context, fns []func(context.Context)) {
	for _, fn := range fns {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("提交后回调 panic", "panic", r)
				}
			}()
			fn(ctx)
		}()
	}
}
