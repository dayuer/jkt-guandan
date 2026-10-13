package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayuer/jkt-guandan/internal/backup"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/store"
)

type cmdRun struct {
	t   *testing.T
	clk *clock.Fake
}

func (r cmdRun) run(args ...string) (code int, out, errOut string) {
	r.t.Helper()
	var o, e strings.Builder
	code = cmdIO{out: &o, errOut: &e, clk: r.clk}.run(args[0], args[1:])
	return code, o.String(), e.String()
}

func (r cmdRun) ok(args ...string) string {
	r.t.Helper()
	code, out, errOut := r.run(args...)
	if code != 0 {
		r.t.Fatalf("guandan %s 退出码 %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func setEnv(t *testing.T, dataDir, remoteDir, recipient string) {
	t.Helper()
	for k, v := range map[string]string{
		"GUANDAN_DEV": "1", "GUANDAN_DATA": dataDir,
		"GUANDAN_BACKUP_URL": "file://" + remoteDir, "GUANDAN_BACKUP_KEY": recipient,
		"GUANDAN_DOMAIN": "", "GUANDAN_PUBLIC_URL": "",
	} {
		t.Setenv(k, v)
	}
}

// 演练 §13.3 的完整链路：seed → backup → 「换一台新机器」→ restore latest → 数据都在。
func TestCommandsBackupAndRestoreDrill(t *testing.T) {
	r := cmdRun{t: t, clk: clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}
	keyOut := r.ok("backup", "keygen")
	var identity, recipient string
	for _, line := range strings.Split(keyOut, "\n") {
		switch {
		case strings.HasPrefix(line, "AGE-SECRET-KEY-"):
			identity = line
		case strings.HasPrefix(line, "# age1"):
			recipient = strings.TrimPrefix(line, "# ")
		}
	}
	if identity == "" || recipient == "" {
		t.Fatalf("keygen 输出不对：\n%s", keyOut)
	}
	keyFile := filepath.Join(t.TempDir(), "key.txt")
	os.WriteFile(keyFile, []byte(identity+"\n"), 0o600)

	oldMachine, remote := t.TempDir(), t.TempDir()
	setEnv(t, oldMachine, remote, recipient)
	if out := r.ok("seed"); !strings.Contains(out, "新增 12 个用户、5 个牌局") {
		t.Fatalf("seed 输出：%s", out)
	}
	if out := r.ok("admin", "stats", "--since", "1d"); !strings.Contains(out, "新牌局  5") {
		t.Fatalf("stats 输出：%s", out)
	}
	if out := r.ok("backup"); !strings.Contains(out, "guandan-20261013T120000Z.db.age 已上传") {
		t.Fatalf("backup 输出：%s", out)
	}
	if out := r.ok("backup", "list"); !strings.Contains(out, "guandan-20261013T120000Z.db.age") {
		t.Fatalf("backup list 输出：%s", out)
	}

	// 服务还在跑（拿着锁）时拒绝恢复。
	lock, err := store.LockDataDir(oldMachine)
	if err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := r.run("restore", "-i", keyFile, "latest"); code != 1 || !strings.Contains(errOut, "先停服务") {
		t.Fatalf("服务运行时应拒绝恢复：%d %s", code, errOut)
	}
	lock.Release()

	newMachine := t.TempDir()
	setEnv(t, newMachine, remote, recipient)
	out := r.ok("restore", "-i", keyFile, "latest")
	if !strings.Contains(out, "已从 file://") || !strings.Contains(out, "0003_bans.sql") {
		t.Fatalf("restore 输出：%s", out)
	}
	if out := r.ok("admin", "stats", "--since", "1d"); !strings.Contains(out, "新牌局  5") {
		t.Fatalf("恢复后的 stats：%s", out)
	}

	// 恢复后的库上，管理命令照常可用。
	if out := r.ok("admin", "game", "cancel", "seed01"); !strings.Contains(out, "已取消牌局") {
		t.Fatalf("game cancel：%s", out)
	}
	if out := r.ok("admin", "ban", "seed-lily"); !strings.Contains(out, "已封禁用户 seed-lily") {
		t.Fatalf("ban：%s", out)
	}
	if code, _, errOut := r.run("admin", "user", "delete", "seed-ajie"); code != 1 || !strings.Contains(errOut, "--yes") {
		t.Fatalf("没加 --yes 应拒绝：%d %s", code, errOut)
	}
	if out := r.ok("admin", "user", "delete", "--yes", "seed-ajie"); !strings.Contains(out, "已注销牌友") {
		t.Fatalf("user delete：%s", out)
	}
	if _, err := os.Stat(filepath.Join(newMachine, backup.DBFile)); err != nil {
		t.Fatal(err)
	}
}

func TestCommandsUsageAndGuards(t *testing.T) {
	r := cmdRun{t: t, clk: clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}
	if code, _, _ := r.run("bogus"); code != 2 {
		t.Fatalf("未知子命令应退出 2，实际 %d", code)
	}
	if code, _, _ := r.run("admin", "frobnicate"); code != 2 {
		t.Fatal("不认识的 admin 命令应退出 2")
	}
	if code, _, _ := r.run("restore"); code != 2 {
		t.Fatal("restore 不带参数应退出 2")
	}

	t.Setenv("GUANDAN_DEV", "")
	t.Setenv("GUANDAN_PUBLIC_URL", "https://guandan.example")
	t.Setenv("GUANDAN_DATA", t.TempDir())
	if code, _, errOut := r.run("seed"); code != 1 || !strings.Contains(errOut, "--force") {
		t.Fatalf("生产环境 seed 要 --force：%d %s", code, errOut)
	}
	if code, _, errOut := r.run("admin", "stats"); code != 1 || !strings.Contains(errOut, "不存在") {
		t.Fatalf("数据库不存在时 admin 应报错而不是建空库：%d %s", code, errOut)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "24h": 24 * time.Hour, "30m": 30 * time.Minute} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "0d", "-1h", "x"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) 应报错", bad)
		}
	}
}

// #34 复审第 3 条：backup、admin stats 只打开数据库不迁移；schema 落后时写库的 admin 命令拒绝执行。
// 用「库停在上一个版本」来模拟：迁移完再把 0003 撤掉。
func TestCommandsDoNotMigrate(t *testing.T) {
	r := cmdRun{t: t, clk: clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}
	dataDir, remote := t.TempDir(), t.TempDir()
	_, recipient, _ := backup.Keygen()
	setEnv(t, dataDir, remote, recipient)
	r.ok("seed")

	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.Write(ctx, func(ctx context.Context, tx *store.Tx) error {
		for _, q := range []string{
			`ALTER TABLE users DROP COLUMN banned_at`,
			`ALTER TABLE devices DROP COLUMN banned_at`,
			`DELETE FROM schema_migrations WHERE name = '0003_bans.sql'`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	pendingOf := func() []string {
		st, err := store.Open(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		p, _, err := st.MigrationStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := pendingOf(); len(p) != 1 || p[0] != "0003_bans.sql" {
		t.Fatalf("准备失败：pending=%v", p)
	}

	if out := r.ok("backup", "now", "--local", "--no-migrate"); !strings.Contains(out, "未配置 GUANDAN_BACKUP_URL") {
		t.Fatalf("--local 不该上传：%s", out)
	}
	if entries, _ := os.ReadDir(remote); len(entries) != 0 {
		t.Fatalf("--local 不该往异地写东西：%d 个", len(entries))
	}
	r.ok("admin", "stats")
	if p := pendingOf(); len(p) != 1 {
		t.Fatalf("backup 和 admin stats 不该迁移，pending=%v", p)
	}

	code, _, errOut := r.run("admin", "ban", "seed-lily")
	if code != 1 || !strings.Contains(errOut, "先发布这个版本") {
		t.Fatalf("schema 落后时写库的命令应拒绝：%d %s", code, errOut)
	}
	if p := pendingOf(); len(p) != 1 {
		t.Fatalf("被拒绝的命令也不该迁移，pending=%v", p)
	}
}

func TestBackupNowIfExists(t *testing.T) {
	r := cmdRun{t: t, clk: clock.NewFake(time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC))}
	dataDir := t.TempDir()
	setEnv(t, dataDir, t.TempDir(), "")
	t.Setenv("GUANDAN_BACKUP_URL", "")
	if out := r.ok("backup", "now", "--if-exists"); !strings.Contains(out, "还没有数据库") {
		t.Fatalf("输出 %s", out)
	}
	if code, _, _ := r.run("backup", "now"); code != 1 {
		t.Fatal("不带 --if-exists 时没有数据库应报错")
	}
	if _, err := os.Stat(filepath.Join(dataDir, backup.DBFile)); err == nil {
		t.Fatal("不该建出空库")
	}
}
