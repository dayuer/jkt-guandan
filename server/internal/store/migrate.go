package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/server/migrations"
)

// migrate 按文件名顺序执行 server/migrations/*.sql，执行记录存在 schema_migrations 表（§2）。
//
// 迁移默认是向前兼容的（§12.1：只加表、加列），旧版本的程序面对多出来的兼容迁移照常能跑，
// 所以库里有本程序不认识的迁移时（降级，或者发布失败后回滚到旧版本），只记一条 warn 照常启动。
// 真正破坏兼容的迁移（删列、改语义、改约束）要在文件开头的注释里写「-- breaking: <原因>」，
// 执行时在 schema_migrations 里记 breaking=1；旧版本只在发现「不认识、并且是 breaking」的迁移时
// 拒绝启动（coord 对 #34 的裁定）。
func migrate(ctx context.Context, db *sql.DB) error {
	if err := ensureMigrationsTable(ctx, db); err != nil {
		return err
	}
	names, err := migrationNames()
	if err != nil {
		return err
	}
	applied, err := appliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if err := checkUnknown(ctx, applied, names); err != nil {
		return err
	}

	for _, name := range names {
		if _, ok := applied[name]; ok {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		if err := apply(ctx, db, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// ensureMigrationsTable 建 schema_migrations；老库上缺 breaking 列就补上（这张元数据表由 migrate 自己维护）。
func ensureMigrationsTable(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL,
		breaking   INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return fmt.Errorf("建 schema_migrations: %w", err)
	}
	has, err := hasBreakingColumn(ctx, db)
	if err != nil {
		return err
	}
	if !has {
		if _, err := db.ExecContext(ctx,
			`ALTER TABLE schema_migrations ADD COLUMN breaking INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("给 schema_migrations 加 breaking 列: %w", err)
		}
	}
	return nil
}

func hasBreakingColumn(ctx context.Context, db *sql.DB) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info('schema_migrations') WHERE name = 'breaking'`).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("读取 schema_migrations 结构: %w", err)
	}
	return n > 0, nil
}

// checkUnknown 处理库里有、本程序不认识的迁移：兼容的记 warn 放行，breaking 的拒绝。
func checkUnknown(ctx context.Context, applied map[string]bool, known []string) error {
	knownSet := make(map[string]bool, len(known))
	for _, n := range known {
		knownSet[n] = true
	}
	var compatible, breaking []string
	for name, isBreaking := range applied {
		switch {
		case knownSet[name]:
		case isBreaking:
			breaking = append(breaking, name)
		default:
			compatible = append(compatible, name)
		}
	}
	sort.Strings(compatible)
	sort.Strings(breaking)
	if len(breaking) > 0 {
		return fmt.Errorf("数据库里有本程序不认识的不兼容迁移 %s（标记了 breaking），旧版本不能在它上面运行；"+
			"请换回新版本，或者用发布前的快照恢复，见 docs/runbook.md §2.2", strings.Join(breaking, "、"))
	}
	if len(compatible) > 0 {
		slog.WarnContext(ctx, "数据库里有本程序不认识的兼容迁移，照常启动（通常是回滚到了旧版本）",
			"migrations", strings.Join(compatible, "、"))
	}
	return nil
}

// isBreaking 判断迁移文件开头的注释里有没有「-- breaking: <原因>」。
// 只看开头连续的注释行（空行跳过），遇到第一条 SQL 就停。
func isBreaking(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "--") {
			return false
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "--"))
		if len(rest) >= len("breaking:") && strings.EqualFold(rest[:len("breaking:")], "breaking:") {
			return true
		}
	}
	return false
}

// migrationNames 返回按文件名排序的迁移文件名，并检查编号唯一。
func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("列出迁移: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if err := checkUniqueNumbers(names); err != nil {
		return nil, err
	}
	return names, nil
}

// checkUniqueNumbers 要求每个迁移文件都以数字编号开头（如 0003_bans.sql），且编号互不相同。
// 编号由 coord 统一分配；两个分支各自加了同号迁移、合并后一起进来时，这里直接拒绝启动，
// 不让「同一个版本号对应两份内容」的状态悄悄进入生产。
func checkUniqueNumbers(names []string) error {
	seen := make(map[string]string, len(names))
	for _, name := range names {
		i := 0
		for i < len(name) && name[i] >= '0' && name[i] <= '9' {
			i++
		}
		if i == 0 || i >= len(name) || name[i] != '_' {
			return fmt.Errorf("迁移文件 %s 的名字应为「编号_说明.sql」，如 0003_bans.sql", name)
		}
		num := strings.TrimLeft(name[:i], "0")
		if prev, ok := seen[num]; ok {
			return fmt.Errorf("迁移 %s 和 %s 的编号重复；编号由 coord 分配，请改名", prev, name)
		}
		seen[num] = name
	}
	return nil
}

// appliedMigrations 返回已执行的迁移，值表示它是不是 breaking。
func appliedMigrations(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, breaking FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("查询迁移记录: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		var breaking bool
		if err := rows.Scan(&name, &breaking); err != nil {
			return nil, fmt.Errorf("读取迁移记录: %w", err)
		}
		applied[name] = breaking
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历迁移记录: %w", err)
	}
	return applied, nil
}

// apply 在一个事务里执行迁移正文并写下记录，避免迁移执行到一半就断掉。
func apply(ctx context.Context, db *sql.DB, name, body string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务 %s: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return fmt.Errorf("执行迁移 %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (name, applied_at, breaking) VALUES (?, ?, ?)`,
		name, time.Now().Unix(), isBreaking(body)); err != nil {
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移 %s: %w", name, err)
	}
	return nil
}

// MigrationStatus 只读地比较库里的迁移记录和本程序带的迁移，不做任何修改：
// pending 是本程序有、库里还没执行的；unknownBreaking 是库里有、本程序不认识、并且标记了 breaking 的。
// 给不该改 schema 的子命令（backup、admin）用：它们只打开数据库，不迁移。
func (s *Store) MigrationStatus(ctx context.Context) (pending, unknownBreaking []string, err error) {
	names, err := migrationNames()
	if err != nil {
		return nil, nil, err
	}
	var exists int
	if err := s.read.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`).Scan(&exists); err != nil {
		return nil, nil, fmt.Errorf("读取迁移记录: %w", err)
	}
	applied := map[string]bool{}
	if exists > 0 {
		has, err := hasBreakingColumn(ctx, s.read)
		if err != nil {
			return nil, nil, err
		}
		if has {
			applied, err = appliedMigrations(ctx, s.read)
		} else {
			applied, err = legacyAppliedMigrations(ctx, s.read)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
		if _, ok := applied[n]; !ok {
			pending = append(pending, n)
		}
	}
	for n, breaking := range applied {
		if !known[n] && breaking {
			unknownBreaking = append(unknownBreaking, n)
		}
	}
	sort.Strings(unknownBreaking)
	return pending, unknownBreaking, nil
}
