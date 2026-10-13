package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/dayuer/jkt-guandan/internal/backup"
	"github.com/dayuer/jkt-guandan/internal/buildinfo"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/metrics"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// appMetrics 是 /metrics 的全部内容（§13.7）。WebSocket 连接数由 #18 登记进来。
type appMetrics struct {
	*metrics.Registry
	http *metrics.HTTP
}

func newMetrics(st *store.Store) appMetrics {
	reg := &metrics.Registry{}
	reg.Info("guandan_build_info", "构建号。", "build", buildinfo.Get())
	m := appMetrics{Registry: reg, http: metrics.NewHTTP(reg)}

	lockWait := metrics.NewHistogram(.001, .005, .01, .05, .1, .5, 1, 2)
	reg.Histogram("guandan_write_lock_wait_seconds", "写事务等写锁的时间。", lockWait)
	st.ObserveLockWait(lockWait.Observe)

	outbox := func(pick func(store.OutboxStats) int64) metrics.Gauge {
		return func(ctx context.Context) (float64, error) {
			s, err := st.OutboxStats(ctx)
			return float64(pick(s)), err
		}
	}
	reg.Gauge("guandan_outbox_pending", "还没发出也没放弃的提醒。",
		outbox(func(s store.OutboxStats) int64 { return s.Pending }))
	reg.Gauge("guandan_outbox_retrying", "其中至少发送失败过一次的。",
		outbox(func(s store.OutboxStats) int64 { return s.Retrying }))
	reg.Gauge("guandan_outbox_dropped", "放弃发送的提醒（30 天内）。",
		outbox(func(s store.OutboxStats) int64 { return s.Dropped }))
	return m
}

func registerBackupMetrics(m appMetrics, r *backup.Runner) {
	unix := func(t time.Time) float64 {
		if t.IsZero() {
			return 0
		}
		return float64(t.Unix())
	}
	m.Gauge("guandan_backup_last_local_timestamp_seconds", "最近一份本地快照的时间（Unix 秒）。",
		func(context.Context) (float64, error) { return unix(r.Status().LastLocal), nil })
	m.Gauge("guandan_backup_last_upload_timestamp_seconds", "本进程最近一次异地上传成功的时间（Unix 秒，没有为 0）。",
		func(context.Context) (float64, error) { return unix(r.Status().LastUpload), nil })
	m.Gauge("guandan_backup_upload_consecutive_failures", "异地上传连续失败次数。",
		func(context.Context) (float64, error) { return float64(r.Status().UploadFailures), nil })
}

// backupDir 是本地快照目录（§13.3）。
func backupDir(cfg config.Config) string { return filepath.Join(cfg.DataDir, "backup") }

func backupTarget(cfg config.Config) (backup.Target, error) {
	if cfg.BackupURL == "" {
		return nil, nil
	}
	return backup.NewTarget(backup.TargetConfig{
		URL: cfg.BackupURL, Region: cfg.BackupRegion,
		AccessKey: cfg.BackupAccessKey, SecretKey: cfg.BackupSecretKey,
	})
}

func newBackupRunner(cfg config.Config, st *store.Store, clk clock.Clock) (*backup.Runner, error) {
	target, err := backupTarget(cfg)
	if err != nil {
		return nil, err
	}
	return backup.New(backup.Options{
		Store: st, Dir: backupDir(cfg), Target: target, Recipient: cfg.BackupKey, Clock: clk,
	})
}
