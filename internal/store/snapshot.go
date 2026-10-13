package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
)

// Snapshot 用 VACUUM INTO 把当前数据库写成一个独立的快照文件（§13.3）。
//
// 读连接池是 query_only，不能执行 VACUUM INTO；也不能占用唯一那条写连接，
// 否则快照期间所有写请求都要排队。所以这里临时开一条单独的连接：
// WAL 模式下它读的是一个一致的快照，不阻塞写。dst 已存在时报错。
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return fmt.Errorf("快照文件 %s 已存在", dst)
	}
	db, err := sql.Open("sqlite", s.dsn)
	if err != nil {
		return fmt.Errorf("打开快照连接: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	if _, err := db.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		_ = os.Remove(dst)
		return fmt.Errorf("VACUUM INTO %s: %w", dst, err)
	}
	// 快照要是单个自包含的文件（上传、恢复、只读校验都不该依赖 -wal/-shm），
	// 所以把它切回 rollback journal 模式。
	snap, err := sql.Open("sqlite", "file:"+dst)
	if err != nil {
		return fmt.Errorf("打开快照 %s: %w", dst, err)
	}
	defer snap.Close()
	var mode string
	if err := snap.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil {
		return fmt.Errorf("快照切换 journal_mode: %w", err)
	}
	return snap.Close()
}

// SnapshotInfo 是校验快照时读到的信息。
type SnapshotInfo struct {
	Migrations []string // 快照里记录的已执行迁移，按名字排序
}

// VerifySnapshot 只读打开一个快照文件，执行 quick_check，并确认它是本程序能用的库：
// 有 schema_migrations，且没有本程序不认识的 breaking 迁移（§13.3：校验通过才算备份成功、才能恢复）。
// 不认识的兼容迁移放行，规则和启动时的 migrate 一致。
func VerifySnapshot(ctx context.Context, path string) (SnapshotInfo, error) {
	if _, err := os.Stat(path); err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照文件: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("打开快照: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照 quick_check: %w", err)
	}
	if result != "ok" {
		return SnapshotInfo{}, fmt.Errorf("快照 quick_check 检出问题: %s", result)
	}

	var info SnapshotInfo

	var applied map[string]bool
	has, err := hasBreakingColumn(ctx, db)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照里读不到 schema_migrations，不是本程序的数据库: %w", err)
	}
	if has {
		applied, err = appliedMigrations(ctx, db)
	} else {
		applied, err = legacyAppliedMigrations(ctx, db) // 加 breaking 列之前的快照
	}
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照里读不到 schema_migrations，不是本程序的数据库: %w", err)
	}
	if len(applied) == 0 {
		return SnapshotInfo{}, errors.New("快照里没有任何迁移记录，不是本程序的数据库")
	}
	for name := range applied {
		info.Migrations = append(info.Migrations, name)
	}
	sort.Strings(info.Migrations)

	// 规则和启动时一样：不认识的兼容迁移放行，不认识的 breaking 迁移拒绝。
	known, err := migrationNames()
	if err != nil {
		return SnapshotInfo{}, err
	}
	if err := checkUnknown(ctx, applied, known); err != nil {
		return SnapshotInfo{}, fmt.Errorf("快照来自更新的版本：%w", err)
	}
	return info, nil
}

func legacyAppliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		applied[name] = false
	}
	return applied, rows.Err()
}
