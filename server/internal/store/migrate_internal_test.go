package store

import (
	"context"
	"strings"
	"testing"

	"github.com/dayuer/jkt-guandan/server/migrations"
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
var confirmedBreaking = map[string]bool{
	// 删掉计分的 a_fail_limit（#205）：2022 竞赛规则里没有「打 A 几次不过回到 2」，A 必打
	// （第二条 13）。#165 还没部署（线上是 9254990），没有要迁移的数据；回滚到 #205 之前的
	// 镜像要先恢复发布前的快照（runbook §2.3）。
	"0012_score_drop_a_fail_limit.sql": true,
}

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

// 0009（#172）：0008 以来已经记下的局按现在的座位补上四个座位上的人；那个座号空着的留空。
func TestMigration0009BackfillsRoundSeats(t *testing.T) {
	ctx := context.Background()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := ensureMigrationsTable(ctx, st.write); err != nil {
		t.Fatal(err)
	}
	names, err := migrationNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name >= "0009" {
			break
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := apply(ctx, st.write, name, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO users (id, nickname, created_at) VALUES ('u1', '一号', 0), ('u2', '二号', 0), ('u4', '四号', 0)`,
		`INSERT INTO games (id, short_code, host_id, starts_at, area, venue, kind, status, created_at)
		 VALUES ('g1', 'c1', 'u1', 0, 'SCBD', 'Pacific Place', 'classic', 'open', 0)`,
		`INSERT INTO seats (game_id, seat_no, user_id, created_at) VALUES ('g1', 1, 'u1', 0), ('g1', 2, 'u2', 0), ('g1', 4, 'u4', 0)`,
		`INSERT INTO score_matches (game_id, kind, a_fail_limit, started_by, started_at) VALUES ('g1', 'classic', 3, 'u1', 0)`,
		`INSERT INTO score_rounds (game_id, round_no, head_seat, partner_place, recorded_by, created_at) VALUES ('g1', 1, 1, 2, 'u1', 0)`,
	} {
		if _, err := st.write.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var s1, s2, s4 string
	var s3 *string
	if err := st.read.QueryRowContext(ctx,
		`SELECT seat1_user, seat2_user, seat3_user, seat4_user FROM score_rounds WHERE game_id = 'g1'`).
		Scan(&s1, &s2, &s3, &s4); err != nil {
		t.Fatal(err)
	}
	if s1 != "u1" || s2 != "u2" || s3 != nil || s4 != "u4" {
		t.Fatalf("补的座位不对：%s %s %v %s", s1, s2, s3, s4)
	}
}
