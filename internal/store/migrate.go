package store

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/migrations"
)

// migrate 按文件名顺序执行 migrations/*.sql，执行记录存在 schema_migrations 表（§2）。
// 只做向前兼容的变更：升级时先停容器、再启动新镜像，迁移在启动阶段跑完。
//
// 库里记着本程序不认识的迁移，说明库已经被更新的版本迁移过、现在跑的是旧二进制（降级）。
// 旧代码不知道新表新列的约束，接着写可能把数据写坏，所以直接拒绝启动。
func migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}
	applied, err := appliedNames(ctx, db)
	if err != nil {
		return err
	}

	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	var unknown []string
	for name := range applied {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("数据库里有本程序不认识的迁移 %s，可能是用旧版本打开了新版本的数据库（降级）；"+
			"请换回新版本，或从备份恢复（docs/server-design.md §13.3）", strings.Join(unknown, "、"))
	}

	for _, name := range names {
		if applied[name] {
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

// checkUniqueNumbers 要求每个迁移文件都以数字编号开头（如 0002_bans.sql），且编号互不相同。
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
			return fmt.Errorf("迁移文件 %s 的名字应为「编号_说明.sql」，如 0002_bans.sql", name)
		}
		num := strings.TrimLeft(name[:i], "0")
		if prev, ok := seen[num]; ok {
			return fmt.Errorf("迁移 %s 和 %s 的编号重复；编号由 coord 分配，请改名", prev, name)
		}
		seen[num] = name
	}
	return nil
}

func appliedNames(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("查询迁移记录: %w", err)
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("读取迁移记录: %w", err)
		}
		applied[name] = true
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
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		name, time.Now().Unix()); err != nil {
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移 %s: %w", name, err)
	}
	return nil
}
