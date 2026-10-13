package store

import (
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
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name       TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("建 schema_migrations: %w", err)
	}

	names, err := migrationNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		applied, err := isApplied(db, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		if err := apply(db, name, string(body)); err != nil {
			return err
		}
	}
	return nil
}

// migrationNames 返回按文件名排序的迁移文件名。
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
	return names, nil
}

func isApplied(db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT count(*) FROM schema_migrations WHERE name = ?`, name).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("查询迁移记录 %s: %w", name, err)
	}
	return count > 0, nil
}

// apply 在一个事务里执行迁移正文并写下记录，避免迁移执行到一半就断掉。
func apply(db *sql.DB, name, body string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启迁移事务 %s: %w", name, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(body); err != nil {
		return fmt.Errorf("执行迁移 %s: %w", name, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (name, applied_at) VALUES (?, ?)`,
		name, time.Now().Unix()); err != nil {
		return fmt.Errorf("记录迁移 %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移 %s: %w", name, err)
	}
	return nil
}
