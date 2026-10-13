// Package backup 负责数据库的本地快照、加密异地备份和恢复（docs/server-design.md §13.3、§12.5）。
//
//   - 每小时 VACUUM INTO 一份本地快照，quick_check 通过才算成功；
//   - 配置了 GUANDAN_BACKUP_URL 时，用 age 公钥加密后上传，远端保留 30 天；
//   - 本地保留最近 48 个小时快照，再加 7 个每日快照；
//   - /readyz 通过 Runner.Check 发现「最近一次备份超过 2 小时」或「异地上传连续失败 3 次」。
package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"filippo.io/age"

	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/store"
)

const (
	// Interval 是快照周期。
	Interval = time.Hour
	// MaxAge 超过它 /readyz 就报备份过期（§12.5）。
	MaxAge = 2 * time.Hour
	// MaxUploadFailures 是异地上传连续失败多少次开始告警（§12.5 降级表）。
	MaxUploadFailures = 3
	// RemoteRetention 是异地备份保留时间（§13.3）。
	RemoteRetention = 30 * 24 * time.Hour

	keepHourly = 48
	keepDaily  = 7

	filePrefix = "guandan-"
	fileSuffix = ".db"
	encSuffix  = ".age"
	timeLayout = "20060102T150405Z"
)

// SnapshotName 是某个时刻的快照文件名，如 guandan-20261013T160000Z.db。
func SnapshotName(t time.Time) string {
	return filePrefix + t.UTC().Format(timeLayout) + fileSuffix
}

// ParseSnapshotTime 从快照文件名（可以带 .age）里取出时间。
func ParseSnapshotTime(name string) (time.Time, bool) {
	name = strings.TrimSuffix(name, encSuffix)
	if !strings.HasPrefix(name, filePrefix) || !strings.HasSuffix(name, fileSuffix) {
		return time.Time{}, false
	}
	t, err := time.Parse(timeLayout, strings.TrimSuffix(strings.TrimPrefix(name, filePrefix), fileSuffix))
	return t, err == nil
}

// Runner 是备份后台任务。用 New 创建。
type Runner struct {
	st        *store.Store
	dir       string // 本地快照目录，/data/backup
	target    Target // 异地备份；为 nil 时只做本地快照
	recipient age.Recipient
	clk       clock.Clock

	mu             sync.Mutex
	lastUpload     time.Time
	uploadFailures int
	lastErr        error
}

// Options 组装 Runner。Target 和 Recipient 要么都给、要么都不给。
type Options struct {
	Store     *store.Store
	Dir       string
	Target    Target
	Recipient string // age 公钥（age1…）
	Clock     clock.Clock
}

func New(opts Options) (*Runner, error) {
	r := &Runner{st: opts.Store, dir: opts.Dir, target: opts.Target, clk: opts.Clock}
	if r.clk == nil {
		r.clk = clock.System{}
	}
	if opts.Target != nil {
		rec, err := age.ParseX25519Recipient(opts.Recipient)
		if err != nil {
			return nil, fmt.Errorf("GUANDAN_BACKUP_KEY 不是合法的 age 公钥: %w", err)
		}
		r.recipient = rec
	}
	return r, nil
}

// Task 是交给 supervise.Group 的循环：最近一份本地快照不到一小时就等到满一小时，
// 否则立刻做一次；之后每小时一次。每转一圈 beat 一次，周期是 Interval。
func (r *Runner) Task(ctx context.Context, beat func()) error {
	wait := time.Duration(0)
	if latest, ok, err := r.LatestLocal(); err == nil && ok {
		if elapsed := r.clk.Since(latest); elapsed < Interval {
			wait = Interval - elapsed
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.clk.After(wait):
		}
		if err := r.RunOnce(ctx); err != nil {
			slog.Error("备份失败", "err", err)
		}
		beat()
		wait = Interval
	}
}

// RunOnce 做一次完整备份：本地快照 → 校验 → 加密上传 → 清理本地和远端的旧备份。
// 本地快照失败直接返回错误；上传失败只记录（本地快照照常保留，下个小时重试）。
func (r *Runner) RunOnce(ctx context.Context) error {
	now := r.clk.Now()
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		return r.fail(fmt.Errorf("创建备份目录: %w", err))
	}
	// 文件名精确到秒。同一秒里已经有一份（比如服务刚启动时自动做了一份，紧接着又手动执行
	// guandan backup），就往后挪一秒，保证新快照不会因为重名而做不出来。
	name := SnapshotName(now)
	for i := 0; i < 60; i++ {
		if _, err := os.Stat(filepath.Join(r.dir, name)); errors.Is(err, os.ErrNotExist) {
			break
		}
		now = now.Add(time.Second)
		name = SnapshotName(now)
	}
	local := filepath.Join(r.dir, name)
	start := time.Now()
	if err := r.st.Snapshot(ctx, local); err != nil {
		return r.fail(err)
	}
	if _, err := store.VerifySnapshot(ctx, local); err != nil {
		_ = os.Remove(local)
		return r.fail(fmt.Errorf("快照校验失败，已删除: %w", err))
	}
	info, _ := os.Stat(local)
	slog.Info("本地快照完成", "file", name, "bytes", info.Size(), "ms", time.Since(start).Milliseconds())

	if r.target != nil {
		if err := r.upload(ctx, local, name+encSuffix); err != nil {
			r.mu.Lock()
			r.uploadFailures++
			n := r.uploadFailures
			r.lastErr = err
			r.mu.Unlock()
			slog.Error("异地备份上传失败", "target", r.target.String(), "consecutive", n, "err", err)
		} else {
			r.mu.Lock()
			r.uploadFailures = 0
			r.lastUpload = now
			r.mu.Unlock()
			slog.Info("异地备份上传完成", "target", r.target.String(), "object", name+encSuffix)
			if err := r.pruneRemote(ctx, now); err != nil {
				slog.Warn("清理异地旧备份失败", "err", err)
			}
		}
	}
	if err := r.pruneLocal(now); err != nil {
		slog.Warn("清理本地旧快照失败", "err", err)
	}
	r.mu.Lock()
	r.lastErr = nil
	r.mu.Unlock()
	return nil
}

func (r *Runner) fail(err error) error {
	r.mu.Lock()
	r.lastErr = err
	r.mu.Unlock()
	return err
}

func (r *Runner) upload(ctx context.Context, local, object string) error {
	tmp, err := os.CreateTemp(r.dir, ".encrypt-*"+encSuffix)
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := encryptFile(local, tmp, r.recipient); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return r.target.Put(ctx, object, tmp.Name())
}

// Check 是注册给 /readyz 的就绪检查（§12.5）。
func (r *Runner) Check(context.Context) error {
	latest, ok, err := r.LatestLocal()
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("还没有任何本地快照")
	}
	if elapsed := r.clk.Since(latest); elapsed > MaxAge {
		return fmt.Errorf("最近一次备份在 %s 前，超过 %s", elapsed.Round(time.Minute), MaxAge)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.uploadFailures >= MaxUploadFailures {
		return fmt.Errorf("异地备份连续失败 %d 次：%v", r.uploadFailures, r.lastErr)
	}
	return nil
}

// Status 是给 /metrics 的状态。
type Status struct {
	LastLocal      time.Time
	LastUpload     time.Time
	UploadFailures int
	RemoteEnabled  bool
}

func (r *Runner) Status() Status {
	latest, _, _ := r.LatestLocal()
	r.mu.Lock()
	defer r.mu.Unlock()
	return Status{LastLocal: latest, LastUpload: r.lastUpload, UploadFailures: r.uploadFailures, RemoteEnabled: r.target != nil}
}

// LatestLocal 返回本地最新快照的时间。直接看目录而不是记在内存里，
// 这样重启之后、或者手动执行 guandan backup 之后都能看到。
func (r *Runner) LatestLocal() (time.Time, bool, error) {
	snaps, err := localSnapshots(r.dir)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(snaps) == 0 {
		return time.Time{}, false, nil
	}
	return snaps[len(snaps)-1], true, nil
}

func localSnapshots(dir string) ([]time.Time, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []time.Time
	for _, e := range entries {
		if t, ok := ParseSnapshotTime(e.Name()); ok && !e.IsDir() && !strings.HasSuffix(e.Name(), encSuffix) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}

// Keep 按「最近 48 个，再加 7 个每日快照」挑出要保留的快照（§13.3）。
// 每日快照取那一天（UTC）最新的一份，从小时快照之外最近的 7 天里选。
func Keep(snaps []time.Time) map[time.Time]bool {
	sorted := append([]time.Time(nil), snaps...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].After(sorted[j]) })
	keep := make(map[time.Time]bool)
	days := make(map[string]bool)
	for i, t := range sorted {
		if i < keepHourly {
			keep[t] = true
			continue
		}
		day := t.UTC().Format("2006-01-02")
		if days[day] || len(days) >= keepDaily {
			continue
		}
		days[day] = true
		keep[t] = true
	}
	return keep
}

func (r *Runner) pruneLocal(time.Time) error {
	snaps, err := localSnapshots(r.dir)
	if err != nil {
		return err
	}
	keep := Keep(snaps)
	for _, t := range snaps {
		if !keep[t] {
			if err := os.Remove(filepath.Join(r.dir, SnapshotName(t))); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) pruneRemote(ctx context.Context, now time.Time) error {
	objs, err := r.target.List(ctx)
	if err != nil {
		return err
	}
	for _, o := range objs {
		t, ok := ParseSnapshotTime(o.Name)
		if !ok || now.Sub(t) <= RemoteRetention {
			continue
		}
		if err := r.target.Delete(ctx, o.Name); err != nil {
			return err
		}
		slog.Info("删除过期的异地备份", "object", o.Name)
	}
	return nil
}

// ---- 加密 ----

func encryptFile(src string, dst io.Writer, rec age.Recipient) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	w, err := age.Encrypt(dst, rec)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, in); err != nil {
		return err
	}
	return w.Close()
}

// Decrypt 用身份文件（age-keygen 或 guandan backup keygen 生成的 AGE-SECRET-KEY-…）解密。
func Decrypt(dst io.Writer, src io.Reader, identityFile string) error {
	f, err := os.Open(identityFile)
	if err != nil {
		return fmt.Errorf("打开身份文件: %w", err)
	}
	defer f.Close()
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return fmt.Errorf("解析身份文件: %w", err)
	}
	r, err := age.Decrypt(src, ids...)
	if err != nil {
		return fmt.Errorf("解密: %w", err)
	}
	_, err = io.Copy(dst, r)
	return err
}

// Keygen 生成一对 age 密钥：公钥填进 GUANDAN_BACKUP_KEY，私钥离线保存，只有恢复时用。
func Keygen() (identity, recipient string, err error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return "", "", err
	}
	return id.String(), id.Recipient().String(), nil
}
