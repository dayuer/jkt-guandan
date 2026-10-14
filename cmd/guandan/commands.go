package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dayuer/jkt-guandan/internal/backup"
	"github.com/dayuer/jkt-guandan/internal/clock"
	"github.com/dayuer/jkt-guandan/internal/config"
	"github.com/dayuer/jkt-guandan/internal/store"
)

// 子命令都在容器里用 docker exec 或 docker run --rm 执行（§13.6）：
//
//	guandan healthcheck [--ready]                 请求本机 /healthz（或 /readyz），200 退出 0
//	guandan metrics                               打印本机 /metrics
//	guandan backup [now] [--local] [--if-exists]  立刻做一次备份（快照 + 加密上传），不迁移数据库
//	guandan backup list                           列出本地快照和异地备份
//	guandan backup keygen                         生成 age 密钥对
//	guandan restore [-i 私钥文件] <快照|对象名|latest>   从备份恢复，必须先停服务
//	guandan seed [--force]                        写入设计稿里的示例数据
//	guandan admin ban <用户或设备 id>
//	guandan admin game cancel <短码>
//	guandan admin user delete --yes <用户 id>
//	guandan admin stats [--since 7d]
//
// 除 restore 外，这些命令都和正在运行的服务共用同一个数据库：写操作同样经过 store.Write，
// 由 SQLite 的锁和 busy_timeout 排队，不会和服务冲突。restore 要换掉数据库文件，所以要拿数据目录锁。

const usage = `用法：guandan [子命令]
  不带子命令          启动服务
  healthcheck [--ready]
  metrics
  backup [now [--local] [--if-exists]|list|keygen]
  restore [-i 私钥文件] <本地快照路径 | 异地对象名 | latest>
  seed [--force]
  admin ban <用户或设备 id>
  admin game cancel <短码>
  admin user delete --yes <用户 id>
  admin stats [--since 7d]
`

// cmdIO 让测试能拿到输出。
type cmdIO struct {
	out, errOut io.Writer
	clk         clock.Clock
}

func runCommand(name string, args []string) int {
	return cmdIO{out: os.Stdout, errOut: os.Stderr, clk: clock.System{}}.run(name, args)
}

func (c cmdIO) run(name string, args []string) int {
	ctx := context.Background()
	var err error
	switch name {
	case "healthcheck":
		fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
		fs.SetOutput(c.errOut)
		ready := fs.Bool("ready", false, "检查 /readyz 而不是 /healthz")
		if fs.Parse(args) != nil {
			return 2
		}
		path := "/healthz"
		if *ready {
			path = "/readyz"
		}
		return healthcheck(os.Getenv("GUANDAN_ADDR"), path)
	case "metrics":
		// /metrics 只听容器内的 127.0.0.1，从宿主机看用 docker exec guandan /guandan metrics。
		addr := os.Getenv("GUANDAN_METRICS_ADDR")
		if addr == "" {
			addr = config.DefaultMetricsAddr
		}
		url, perr := probeURL(addr, "/metrics")
		if perr != nil {
			err = perr
			break
		}
		err = fetchTo(c.out, url)
	case "backup":
		err = c.backup(ctx, args)
	case "restore":
		err = c.restore(ctx, args)
	case "seed":
		err = c.seed(ctx, args)
	case "admin":
		err = c.admin(ctx, args)
	case "help", "-h", "--help":
		fmt.Fprint(c.out, usage)
		return 0
	default:
		fmt.Fprintf(c.errOut, "未知子命令 %q\n%s", name, usage)
		return 2
	}
	var uerr usageError
	if errors.As(err, &uerr) {
		fmt.Fprintf(c.errOut, "%s\n%s", uerr.msg, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(c.errOut, "错误:", err)
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// openExisting 打开已有的数据库，**不迁移**。数据库不存在时报错，免得在错误的目录里建出一个空库。
//
// 子命令可能用和线上服务不同版本的镜像执行（比如 deploy-remote.sh 用新镜像做冷备份，或者回滚时用旧镜像
// 查看快照）。这时如果顺手迁移，一个本意只读的命令就会改掉生产库的 schema，做出来的快照也不再是迁移前的
// 状态。所以只有服务启动和 seed 会迁移（#34 复审第 3 条）。
func openExisting(ctx context.Context, cfg config.Config) (*store.Store, error) {
	path := filepath.Join(cfg.DataDir, backup.DBFile)
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("数据库 %s 不存在（GUANDAN_DATA 设对了吗？）: %w", path, err)
	}
	return store.Open(cfg.DataDir)
}

// requireCurrentSchema 给会写库的 admin 命令用：schema 必须和本程序一致。
// 库比本程序旧（还有没执行的迁移），新代码写进去可能缺列；库里有本程序不认识的 breaking 迁移，旧代码写进去可能写坏。
func requireCurrentSchema(ctx context.Context, st *store.Store) error {
	pending, unknown, err := st.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return fmt.Errorf("数据库的 schema 比本程序旧（还没执行 %s）；先发布这个版本，由服务启动时迁移，再执行这条命令",
			strings.Join(pending, "、"))
	}
	if len(unknown) > 0 {
		return fmt.Errorf("数据库里有本程序不认识的不兼容迁移 %s；请用和线上服务相同版本的镜像执行", strings.Join(unknown, "、"))
	}
	return nil
}

func openAndMigrate(ctx context.Context, cfg config.Config) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	if err := st.Migrate(ctx); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// ---- backup ----

func (c cmdIO) backup(ctx context.Context, args []string) error {
	sub := "now"
	if len(args) > 0 {
		sub = args[0]
	}
	if sub == "keygen" {
		id, rec, err := backup.Keygen()
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "# 公钥：填进 GUANDAN_BACKUP_KEY\n# %s\n# 私钥：离线保存（密码管理器），只有恢复时才用，不要放到服务器上\n%s\n", rec, id)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch sub {
	case "now":
		fs := flag.NewFlagSet("backup now", flag.ContinueOnError)
		fs.SetOutput(c.errOut)
		local := fs.Bool("local", false, "只做本地快照，不上传异地（发布时的冷备份用）")
		ifExists := fs.Bool("if-exists", false, "数据库还不存在时跳过，退出码 0（首次发布）")
		fs.Bool("no-migrate", true, "不迁移数据库（总是如此，写出来只为让调用方意图明确）")
		if len(args) > 0 {
			args = args[1:]
		}
		if err := fs.Parse(args); err != nil {
			return usageError{err.Error()}
		}
		if *ifExists {
			if _, err := os.Stat(filepath.Join(cfg.DataDir, backup.DBFile)); errors.Is(err, os.ErrNotExist) {
				fmt.Fprintln(c.out, "还没有数据库，跳过备份")
				return nil
			}
		}
		st, err := openExisting(ctx, cfg)
		if err != nil {
			return err
		}
		defer st.Close()
		if *local {
			cfg.BackupURL = ""
		}
		r, err := newBackupRunner(cfg, st, c.clk)
		if err != nil {
			return err
		}
		if err := r.RunOnce(ctx); err != nil {
			return err
		}
		s := r.Status()
		fmt.Fprintf(c.out, "本地快照：%s\n", filepath.Join(backupDir(cfg), backup.SnapshotName(s.LastLocal)))
		switch {
		case !s.RemoteEnabled:
			fmt.Fprintln(c.out, "异地备份：未配置 GUANDAN_BACKUP_URL")
		case s.UploadFailures > 0:
			return errors.New("本地快照已完成，但异地上传失败，详见上面的日志")
		default:
			fmt.Fprintf(c.out, "异地备份：%s.age 已上传\n", backup.SnapshotName(s.LastLocal))
		}
		return nil
	case "list":
		entries, err := os.ReadDir(backupDir(cfg))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Fprintf(c.out, "本地 %s：\n", backupDir(cfg))
		for _, e := range entries {
			if _, ok := backup.ParseSnapshotTime(e.Name()); ok {
				info, _ := e.Info()
				fmt.Fprintf(c.out, "  %s  %d 字节\n", e.Name(), info.Size())
			}
		}
		target, err := backupTarget(cfg)
		if err != nil {
			return err
		}
		if target == nil {
			fmt.Fprintln(c.out, "异地：未配置 GUANDAN_BACKUP_URL")
			return nil
		}
		objs, err := target.List(ctx)
		if err != nil {
			return fmt.Errorf("列出异地备份: %w", err)
		}
		fmt.Fprintf(c.out, "异地 %s：\n", target)
		for _, o := range objs {
			fmt.Fprintf(c.out, "  %s  %d 字节\n", o.Name, o.Size)
		}
		return nil
	default:
		return usageError{"backup 的子命令是 now、list 或 keygen"}
	}
}

// ---- restore ----

func (c cmdIO) restore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	identity := fs.String("i", os.Getenv("GUANDAN_BACKUP_IDENTITY"), "age 私钥文件（解密 .age 快照用）")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != 1 {
		return usageError{"restore 需要且只需要一个参数：本地快照路径、异地对象名或 latest"}
	}
	arg := fs.Arg(0)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return err
	}
	// 拿得到数据目录锁，才说明服务已经停了（§13.3：先停掉服务）。
	lock, err := store.LockDataDir(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("%w；恢复前先停服务：docker stop guandan", err)
	}
	defer func() { _ = lock.Release() }()

	src := backup.Source{IdentityFile: *identity}
	if _, statErr := os.Stat(arg); statErr == nil || strings.Contains(arg, "/") {
		src.Path = arg
	} else {
		src.Object = arg
		if src.Target, err = backupTarget(cfg); err != nil {
			return err
		}
	}

	res, err := backup.Restore(ctx, cfg.DataDir, src, c.clk.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已从 %s 恢复到 %s\n", res.From, filepath.Join(cfg.DataDir, backup.DBFile))
	fmt.Fprintf(c.out, "快照里的迁移：%s\n", strings.Join(res.Migrations, "、"))
	for _, p := range res.MovedAside {
		fmt.Fprintf(c.out, "原文件已挪到：%s\n", p)
	}
	fmt.Fprintln(c.out, "现在可以启动服务：docker start guandan（或 scripts/deploy.sh）")
	return nil
}

// ---- seed ----

func (c cmdIO) seed(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(c.errOut)
	force := fs.Bool("force", false, "非开发模式下也写入")
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.Dev && !*force {
		return errors.New("示例数据只用于本地和 PR 验证；非开发模式（GUANDAN_DEV 未设）下要加 --force")
	}
	st, err := openAndMigrate(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	res, err := st.Seed(ctx, c.clk.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "示例数据：新增 %d 个用户、%d 个牌局（已存在的跳过）。牌局短码 SEED01–SEED05\n", res.Users, res.Games)
	return nil
}

// ---- admin ----

func (c cmdIO) admin(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageError{"admin 需要子命令"}
	}
	switch args[0] {
	case "ban", "game", "user", "stats":
	default:
		return usageError{"不认识的 admin 命令：" + strings.Join(args, " ")}
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	open := func() (*store.Store, error) { return openExisting(ctx, cfg) }
	// 会写库的命令要求 schema 和本程序一致。
	openForWrite := func() (*store.Store, error) {
		st, err := openExisting(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err := requireCurrentSchema(ctx, st); err != nil {
			st.Close()
			return nil, err
		}
		return st, nil
	}
	now := c.clk.Now()

	switch {
	case args[0] == "ban" && len(args) == 2:
		st, err := openForWrite()
		if err != nil {
			return err
		}
		defer st.Close()
		kind, err := st.Ban(ctx, args[1], now)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已封禁%s %s\n", map[string]string{"user": "用户", "device": "设备"}[kind], args[1])
		return nil

	case len(args) == 3 && args[0] == "game" && args[1] == "cancel":
		st, err := openForWrite()
		if err != nil {
			return err
		}
		defer st.Close()
		id, err := st.CancelGame(ctx, strings.ToUpper(args[2]), now)
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已取消牌局 %s（%s），未发的提醒已放弃\n", args[2], id)
		return nil

	case len(args) >= 3 && args[0] == "user" && args[1] == "delete":
		fs := flag.NewFlagSet("admin user delete", flag.ContinueOnError)
		fs.SetOutput(c.errOut)
		yes := fs.Bool("yes", false, "确认删除")
		if err := fs.Parse(args[2:]); err != nil {
			return usageError{err.Error()}
		}
		if fs.NArg() != 1 {
			return usageError{"admin user delete 需要一个用户 id"}
		}
		if !*yes {
			return errors.New("删除用户数据不可撤销，确认无误后加 --yes 再执行")
		}
		st, err := openForWrite()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.DeleteUserData(ctx, fs.Arg(0)); err != nil {
			return err
		}
		fmt.Fprintf(c.out, "已删除用户 %s 的个人数据，座位记录显示为「%s」\n", fs.Arg(0), store.DeletedNickname)
		return nil

	case args[0] == "stats":
		fs := flag.NewFlagSet("admin stats", flag.ContinueOnError)
		fs.SetOutput(c.errOut)
		sinceFlag := fs.String("since", "7d", "统计多久以来，如 7d、24h")
		if err := fs.Parse(args[1:]); err != nil {
			return usageError{err.Error()}
		}
		since, err := parseSince(*sinceFlag)
		if err != nil {
			return usageError{err.Error()}
		}
		st, err := open()
		if err != nil {
			return err
		}
		defer st.Close()
		s, err := st.Stats(ctx, now.Add(-since))
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "自 %s 起（%s）：\n", s.Since.UTC().Format(time.RFC3339), *sinceFlag)
		fmt.Fprintf(c.out, "  新用户  %d\n  新牌局  %d", s.NewUsers, s.NewGames)
		for _, state := range []string{"open", "full", "done", "cancelled"} {
			if n := s.GamesByState[state]; n > 0 {
				fmt.Fprintf(c.out, "  %s=%d", state, n)
			}
		}
		fmt.Fprintln(c.out)
		if len(s.Events) > 0 {
			fmt.Fprintln(c.out, "  埋点：")
			for _, e := range s.Events {
				fmt.Fprintf(c.out, "    %-20s %d\n", e.Name, e.Count)
			}
		}
		funnels, err := st.Funnels(ctx, now.Add(-since))
		if err != nil {
			return err
		}
		printFunnels(c.out, funnels)
		return nil
	}
	return usageError{"不认识的 admin 命令：" + strings.Join(args, " ")}
}

// printFunnels 按环境打印转化漏斗：各埋点的次数，加上看详情 → 点报名 → 报名成功、推送弹窗 → 开启的转化率。
// 是事件次数，不是去重后的人数（埋点不记设备）。
func printFunnels(w io.Writer, funnels []store.Funnel) {
	if len(funnels) == 0 {
		return
	}
	pct := func(num, den int64) string {
		if den == 0 {
			return "-"
		}
		return fmt.Sprintf("%.0f%%", 100*float64(num)/float64(den))
	}
	idx := func(name string) int { return slices.Index(store.FunnelEvents, name) }
	fmt.Fprintln(w, "  漏斗（按环境，事件次数）：")
	fmt.Fprintf(w, "    %-10s", "环境")
	for _, n := range store.FunnelEvents {
		fmt.Fprintf(w, " %13s", n)
	}
	fmt.Fprintf(w, " %11s %11s %11s\n", "view→click", "click→ok", "prompt→ok")
	for _, f := range funnels {
		fmt.Fprintf(w, "    %-10s", f.Env)
		for _, n := range f.Counts {
			fmt.Fprintf(w, " %13d", n)
		}
		c := f.Counts
		fmt.Fprintf(w, " %11s %11s %11s\n",
			pct(c[idx("join_click")], c[idx("detail_view")]),
			pct(c[idx("join_ok")], c[idx("join_click")]),
			pct(c[idx("push_ok")], c[idx("push_prompt")]))
	}
}

func fetchTo(w io.Writer, url string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 %d", url, resp.StatusCode)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// parseSince 解析 7d、24h、30m 这样的时长。
func parseSince(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("--since=%q 不合法", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--since=%q 不合法", s)
	}
	return d, nil
}
