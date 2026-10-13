// Package store 是唯一写 SQL 的地方（docs/server-design.md §3、§11.1）。
//
// 并发要点：
//   - 整个进程只有一个写连接（SetMaxOpenConns(1)），所有写操作都经过 Write 这一个入口，
//     写事务一律 BEGIN IMMEDIATE，开头就拿到写锁，所以「读—判断—写」天然串行，
//     业务层不需要任何互斥锁（§11.2）；
//   - 读另开一个 4 连接的池，WAL 模式下读写互不阻塞，读到的是一致快照；
//   - 每个连接执行 PRAGMA journal_mode=WAL、busy_timeout=5000、foreign_keys=ON、synchronous=NORMAL。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 驱动，所以 CGO_ENABLED=0，运行时镜像可以用 distroless/static
)

// DSN 的 PRAGMA 参数由 modernc.org/sqlite 驱动在每次建连接时执行。
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)"

// WriteTimeout 是单个写事务的总时限。超时返回 ErrBusy，HTTP 层映射成 503 + Retry-After（§11.1、§12.3）。
const WriteTimeout = 2 * time.Second

// ErrBusy 表示等写锁超时。调用方用 errors.Is 判断。
var ErrBusy = errors.New("写事务等待超时")

// Store 持有两个连接池：write 只有 1 条连接，read 有 4 条。
type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
}

// Open 打开（必要时创建）dataDir 下的数据库，并执行尚未应用的迁移。
// 只负责数据库本身；数据目录的文件锁和磁盘检查在 Open 之前做（见 LockDataDir、FreeBytes）。
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
		return nil, fmt.Errorf("打开读连接池: %w", err)
	}
	read.SetMaxOpenConns(4)

	if err := migrate(write); err != nil {
		write.Close()
		read.Close()
		return nil, err
	}
	return &Store{write: write, read: read, path: path}, nil
}

// Path 返回数据库文件的绝对路径。
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

// Ping 走读连接池，不占用唯一那条写连接。
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.read.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// QuickCheck 在启动时检查数据库文件本身有没有损坏（§12.1 第 4 步）。
func (s *Store) QuickCheck(ctx context.Context) error {
	var result string
	if err := s.read.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("quick_check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("quick_check 检出问题: %s", result)
	}
	return nil
}

// Checkpoint 在关闭前把 WAL 落盘并截断（§12.4 第 5 步）。
func (s *Store) Checkpoint(ctx context.Context) error {
	if _, err := s.write.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("wal_checkpoint: %w", err)
	}
	return nil
}

// Heartbeat 供 /readyz 使用：真的跑一次写事务，确认写路径是通的（§12.5）。
func (s *Store) Heartbeat(ctx context.Context, now time.Time) error {
	return s.Write(ctx, func(ctx context.Context, tx *Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO kv (k, v) VALUES ('readyz', ?)
			 ON CONFLICT(k) DO UPDATE SET v = excluded.v`,
			[]byte(strconv.FormatInt(now.Unix(), 10)))
		return err
	})
}

// Read 返回读连接池，供只读查询使用。
func (s *Store) Read() *sql.DB { return s.read }

// Tx 是一次写事务。SQL 通过它执行，事务后回调也登记在它上面。
type Tx struct {
	conn    *sql.Conn
	after   []func(context.Context)
	touched []string
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

// AfterCommit 登记一个提交后回调，按登记顺序执行，只在事务提交成功后执行。
// 事务回滚时全部丢弃——这是 §5.5 要求的语义：不能发布「幽灵座位」。
// #18 会在这些回调里调用 realtime.Publish。
func (t *Tx) AfterCommit(fn func(context.Context)) {
	t.after = append(t.after, fn)
}

// Touch 登记这次事务改过的牌局。提交前会对每个登记的牌局跑一次 CheckInvariants（§4）。
func (t *Tx) Touch(gameID string) {
	for _, id := range t.touched {
		if id == gameID {
			return
		}
	}
	t.touched = append(t.touched, gameID)
}

// Write 是唯一的写入口（§11.1）。它依次做四件事：
//  1. 给 ctx 加 2 秒上限；等写锁超时返回 ErrBusy；
//  2. 开 BEGIN IMMEDIATE，调用业务函数；
//  3. 提交前对登记过的牌局检查数据不变量，不通过就回滚；
//  4. 提交成功后依次执行登记的回调。
//
// 写事务里不要做任何 I/O（推送、LLM、出图、WebSocket），那些放到 AfterCommit 里。
func (s *Store) Write(ctx context.Context, fn func(ctx context.Context, tx *Tx) error) error {
	ctx, cancel := context.WithTimeout(ctx, WriteTimeout)
	defer cancel()

	start := time.Now()
	conn, err := s.write.Conn(ctx)
	if err != nil {
		return asBusyResult(err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		if isTimeout(err) {
			return fmt.Errorf("%w: %v", ErrBusy, err)
		}
		return fmt.Errorf("开启写事务: %w", err)
	}
	recordLockWait(ctx, time.Since(start))

	tx := &Tx{conn: conn}
	if err := fn(ctx, tx); err != nil {
		if rbErr := rollback(ctx, conn); rbErr != nil {
			return fmt.Errorf("%w（回滚也失败: %v）", err, rbErr)
		}
		return err
	}

	for _, gameID := range tx.touched {
		if err := CheckInvariants(ctx, conn, gameID); err != nil {
			if rbErr := rollback(ctx, conn); rbErr != nil {
				return fmt.Errorf("%w（回滚也失败: %v）", err, rbErr)
			}
			slog.Error("写事务不变量检查失败，已回滚", "game", gameID, "err", err)
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("提交写事务: %w", err)
	}

	// 提交已经成功，事件就该发出去，哪怕客户端这时已经断开，
	// 所以回调拿到的是去掉取消和超时的 ctx。
	runAfterCommit(context.WithoutCancel(ctx), tx.after)
	return nil
}

// lockWaitWarnThreshold 是记 warn 的门槛（§13.7）。
const lockWaitWarnThreshold = 100 * time.Millisecond

func recordLockWait(ctx context.Context, wait time.Duration) {
	if stats := writeStatsFrom(ctx); stats != nil {
		stats.Record(wait)
	}
	if wait > lockWaitWarnThreshold {
		slog.Warn("写锁等待超过 100ms", "wait_ms", wait.Milliseconds())
	}
}

func rollback(ctx context.Context, conn *sql.Conn) error {
	// 事务已经因为超时被取消时，用原 ctx 回滚会直接失败，这里给回滚单独留时间。
	rbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := conn.ExecContext(rbCtx, "ROLLBACK")
	return err
}

func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.Is(err, sql.ErrConnDone)
}

func asBusyResult(err error) error {
	if isTimeout(err) {
		return fmt.Errorf("%w: %v", ErrBusy, err)
	}
	return err
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
