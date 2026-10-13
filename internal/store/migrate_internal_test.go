package store

import (
	"context"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/migrations"
)

// 两个迁移编号相同（比如两个分支各自加了 0002）时拒绝启动（coord 对 #34 的要求）。
func TestCheckUniqueNumbers(t *testing.T) {
	cases := []struct {
		names []string
		bad   string // 错误信息里应出现的内容，空表示应通过
	}{
		{[]string{"0001_init.sql", "0002_bans.sql", "0003_x.sql"}, ""},
		{[]string{"0001_init.sql", "0002_bans.sql", "0002_identity.sql"}, "编号重复"},
		{[]string{"0002_a.sql", "2_b.sql"}, "编号重复"}, // 前导零不同也算同号
		{[]string{"init.sql"}, "编号_说明"},
		{[]string{"0003.sql"}, "编号_说明"},
	}
	for _, c := range cases {
		err := checkUniqueNumbers(c.names)
		if c.bad == "" && err != nil {
			t.Errorf("%v 应通过: %v", c.names, err)
		}
		if c.bad != "" && (err == nil || !strings.Contains(err.Error(), c.bad)) {
			t.Errorf("%v 应报「%s」，实际 %v", c.names, c.bad, err)
		}
	}
}

// 仓库里实际的迁移文件必须满足这条规则。
func TestEmbeddedMigrationsHaveUniqueNumbers(t *testing.T) {
	if _, err := migrationNames(); err != nil {
		t.Fatal(err)
	}
}

func TestIsBreaking(t *testing.T) {
	cases := map[string]bool{
		"-- breaking: 删掉 users.wa_phone\nALTER TABLE users DROP COLUMN wa_phone;": true,
		"-- 说明\n\n--   Breaking: 改约束\nCREATE TABLE x (a);":                        true,
		"-- 加一列\nALTER TABLE users ADD COLUMN banned_at INTEGER;":                 false,
		"CREATE TABLE x (a);\n-- breaking: 写在 SQL 后面的不算":                          false,
		"": false,
	}
	for body, want := range cases {
		if got := isBreaking(body); got != want {
			t.Errorf("isBreaking(%q) = %v，期望 %v", body, got, want)
		}
	}
}

// 加 breaking 列之前的老库：migrate 会把列补上，已有记录按兼容处理。
func TestMigrateAddsBreakingColumnToOldTable(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.write.ExecContext(ctx, `ALTER TABLE schema_migrations DROP COLUMN breaking`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("老库上再次迁移: %v", err)
	}
	has, err := hasBreakingColumn(ctx, st.write)
	if err != nil || !has {
		t.Fatalf("breaking 列应被补上：%v %v", has, err)
	}
}

// confirmedBreaking 列出已经按 runbook §2.3 人工确认过的 breaking 迁移。
// 新加 breaking 迁移时这条测试会失败，逼着作者和复审方在 PR 里明确确认，而不是悄悄混进去。
var confirmedBreaking = map[string]bool{}

func TestEmbeddedBreakingMigrationsAreConfirmed(t *testing.T) {
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		body, _ := migrations.FS.ReadFile(n)
		if isBreaking(string(body)) && !confirmedBreaking[n] {
			t.Errorf("%s 标记了 breaking：按 runbook §2.3 确认后加进 confirmedBreaking", n)
		}
	}
}
