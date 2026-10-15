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
	"database/sql/driver"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 驱动，所以 CGO_ENABLED=0，运行时镜像可以用 distroless/static
)

// DSN 的 PRAGMA 参数由 modernc.org/sqlite 驱动在每次建连接时执行。
const pragmas = "?_pragma=journal_mode(WAL)" +
	"&_pragma=busy_timeout(5000)" +
	"&_pragma=foreign_keys(ON)" +
	"&_pragma=synchronous(NORMAL)"

// readOnly 只加在读连接池上：SQLite 拒绝这些连接上的一切写操作，
// 保证 Write 是唯一的写入口（§11.1）。放在最后，前面的 journal_mode 先生效。
const readOnly = "&_pragma=query_only(1)"

// WriteTimeout 是单个写事务的总时限。超时返回 ErrBusy，HTTP 层映射成 503 + Retry-After（§11.1、§12.3）。
// 生产一律用它；只有测试在机器重负载下才用 Options 调大（#171）。
const WriteTimeout = 2 * time.Second

// ErrBusy 表示等写锁超时。调用方用 errors.Is 判断。
var ErrBusy = errors.New("写事务等待超时")

// Options 是 Open 的可选参数。零值就是生产语义（§11.1）。
type Options struct {
	// WriteTimeout 覆盖单个写事务的总时限。0 表示用默认的 WriteTimeout。
	//
	// **只有测试该动它**：一个用例里几十个 goroutine 抢同一条写连接时（比如
	// TestFiftyRaceForFourSeats、TestCleanupEndsGamesAcrossBatches），机器 load 高的时候
	// 排队就会超过 2 秒，用例随机变红，而它想验的是并发的**结果**、不是等锁的耗时（#171）。
	// 生产不传，保持 2 秒不变 —— 等锁超时给 503 是有意设计的行为，
	// TestWriteReturnsBusyWhenLockHeldTooLong 钉的就是默认值。
	WriteTimeout time.Duration
}

// Store 持有两个连接池：write 只有 1 条连接，read 有 4 条。
type Store struct {
	write *sql.DB
	read  *sql.DB
	path  string
	dsn   string
	opts  Options

	// onLockWait 收到每次写事务的拿锁等待时间，供 /metrics 使用（§13.7）。
	onLockWait atomic.Pointer[func(time.Duration)]
}

// writeTimeout 是这个 Store 的写事务总时限。
func (s *Store) writeTimeout() time.Duration {
	if s.opts.WriteTimeout > 0 {
		return s.opts.WriteTimeout
	}
	return WriteTimeout
}

// Open 打开（必要时创建）dataDir 下的数据库，但不执行迁移。
// 启动顺序见 §12.1：调用方先锁数据目录、检查磁盘，Open 之后先 QuickCheck，再 Migrate。
// 数据目录由调用方创建。
func Open(dataDir string) (*Store, error) {
	return OpenWith(dataDir, Options{})
}

// OpenWith 和 Open 一样，只是可以覆盖写事务总时限等选项。生产用 Open，语义不变。
func OpenWith(dataDir string, opts Options) (*Store, error) {
	path := filepath.Join(dataDir, "guandan.db")
	dsn := "file:" + path + pragmas

	write, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库: %w", err)
	}
	write.SetMaxOpenConns(1)
	// 先用写连接建出数据库文件并切到 WAL，读连接池（query_only）随后打开的就是现成的库。
	if err := write.Ping(); err != nil {
		write.Close()
		return nil, fmt.Errorf("打开数据库 %s: %w", path, err)
	}

	read, err := sql.Open("sqlite", dsn+readOnly)
	if err != nil {
		write.Close()
		return nil, fmt.Errorf("打开读连接池: %w", err)
	}
	read.SetMaxOpenConns(4)

	return &Store{write: write, read: read, path: path, dsn: dsn, opts: opts}, nil
}

// Migrate 执行尚未应用的迁移（§12.1 第 5 步，在 QuickCheck 之后）。
func (s *Store) Migrate(ctx context.Context) error {
	return migrate(ctx, s.write)
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
//  1. 给 ctx 加写事务上限（默认 WriteTimeout，测试可用 Options 调大）；等写锁超时返回 ErrBusy；
//  2. 开 BEGIN IMMEDIATE，调用业务函数；
//  3. 提交前对登记过的牌局检查数据不变量，不通过就回滚；
//  4. 提交成功后依次执行登记的回调。
//
// 只要没有提交成功（业务函数出错或 panic、不变量不通过、COMMIT 失败），都会回滚；
// 回滚也失败时丢弃这条连接，不让开着事务的连接回到池子里卡死后面所有的写。
//
// 写事务里不要做任何 I/O（推送、LLM、出图、WebSocket），那些放到 AfterCommit 里。
func (s *Store) Write(ctx context.Context, fn func(ctx context.Context, tx *Tx) error) (err error) {
	ctx, cancel := context.WithTimeout(ctx, s.writeTimeout())
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
	s.recordLockWait(ctx, time.Since(start))

	// 这个 defer 先于 conn.Close 执行，保证连接回池之前事务已经结束。
	committed := false
	defer func() {
		if committed {
			return
		}
		p := recover()
		if rbErr := rollback(ctx, conn); rbErr != nil {
			slog.Error("写事务回滚失败，丢弃这条连接", "err", rbErr)
			discard(conn)
			if p == nil {
				err = fmt.Errorf("%w（回滚也失败: %v）", err, rbErr)
			}
		}
		if p != nil {
			panic(p)
		}
	}()

	tx := &Tx{conn: conn}
	if err := fn(ctx, tx); err != nil {
		return err
	}

	for _, gameID := range tx.touched {
		if err := CheckInvariants(ctx, conn, gameID); err != nil {
			slog.Error("写事务不变量检查失败，回滚", "game", gameID, "err", err)
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("提交写事务: %w", err)
	}
	committed = true

	// 提交已经成功，事件就该发出去，哪怕客户端这时已经断开，
	// 所以回调拿到的是去掉取消和超时的 ctx。
	runAfterCommit(context.WithoutCancel(ctx), tx.after)
	return nil
}

// discard 让连接池丢掉这条连接：Raw 的回调返回 driver.ErrBadConn 时，
// database/sql 会关闭底层连接而不是放回池子，下一次 Write 拿到的是新连接。
func discard(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
}

// lockWaitWarnThreshold 是记 warn 的门槛（§13.7）。
const lockWaitWarnThreshold = 100 * time.Millisecond

// ObserveLockWait 登记一个观察者，每次写事务拿到锁后收到等待时间。启动时设置一次。
func (s *Store) ObserveLockWait(fn func(time.Duration)) { s.onLockWait.Store(&fn) }

func (s *Store) recordLockWait(ctx context.Context, wait time.Duration) {
	if stats := writeStatsFrom(ctx); stats != nil {
		stats.Record(wait)
	}
	if fn := s.onLockWait.Load(); fn != nil {
		(*fn)(wait)
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
