package config_test

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dayuer/jkt-guandan/server/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GUANDAN_DATA", "GUANDAN_ADDR", "GUANDAN_DOMAIN", "GUANDAN_PUBLIC_URL",
		"GUANDAN_ADMIN_EMAIL", "GUANDAN_DEV", "GUANDAN_BACKUP_URL",
		"GUANDAN_BACKUP_KEY", "ANTHROPIC_API_KEY",
		"GUANDAN_GROUP_WECHAT_ID", "GUANDAN_GROUP_WHATSAPP_URL",
		"GUANDAN_WX_APPID", "GUANDAN_WX_SECRET",
		"GUANDAN_WX_TMPL_FULL", "GUANDAN_WX_TMPL_FULL_KEYS", "GUANDAN_WX_TMPL_REMIND", "GUANDAN_WX_TMPL_REMIND_KEYS",
		"AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("GUANDAN_TRUSTED_PROXIES", "")
	// GUANDAN_METRICS_ADDR 设成空字符串表示关闭 /metrics，所以这里要真的删掉它。
	t.Setenv("GUANDAN_METRICS_ADDR", "")
	os.Unsetenv("GUANDAN_METRICS_ADDR")
}

// §12.1 第 1 步：值不合法就直接退出。
func TestLoadRejectsBackupURLWithoutKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_PUBLIC_URL", "https://guandan.id")
	t.Setenv("GUANDAN_BACKUP_URL", "https://s3.example.com/bucket/backup")

	if _, err := config.Load(); err == nil {
		t.Fatal("设置了备份地址却没有加密公钥，应当报错")
	}
}

func TestLoadRejectsAutocertWithoutAdminEmail(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DOMAIN", "guandan.id")

	if _, err := config.Load(); err == nil {
		t.Fatal("启用 autocert 却没有管理员邮箱，应当报错")
	}
}

// 非开发模式下不知道本站域名，CSRF 会把所有写请求拒掉，所以启动时就要报错。
func TestLoadRequiresPublicURLOrDomainOutsideDev(t *testing.T) {
	clearEnv(t)
	if _, err := config.Load(); err == nil {
		t.Fatal("非开发模式下 GUANDAN_PUBLIC_URL 和 GUANDAN_DOMAIN 都没设，应当报错")
	}

	t.Setenv("GUANDAN_DEV", "1")
	if _, err := config.Load(); err != nil {
		t.Fatalf("开发模式可以不设: %v", err)
	}
}

func TestLoadRejectsMalformedPublicURL(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_PUBLIC_URL", "guandan.id")
	if _, err := config.Load(); err == nil {
		t.Fatal("没有 scheme 的 GUANDAN_PUBLIC_URL 应当报错")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_PUBLIC_URL", "http://127.0.0.1:8080")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != "/data" {
		t.Fatalf("DataDir = %q", cfg.DataDir)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q", cfg.Addr)
	}
	if cfg.TLSAddr != config.DefaultTLSAddr {
		t.Fatalf("TLSAddr = %q", cfg.TLSAddr)
	}
	if cfg.TLSEnabled() {
		t.Fatal("没有域名时不该启用 TLS")
	}
}

func TestLoadDerivesPublicURLFromDomain(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DOMAIN", "guandan.id,backup.example.com")
	t.Setenv("GUANDAN_ADMIN_EMAIL", "a@b.c")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PublicURL != "https://guandan.id" {
		t.Fatalf("PublicURL = %q", cfg.PublicURL)
	}
	if len(cfg.Domains()) != 2 {
		t.Fatalf("Domains = %v", cfg.Domains())
	}
	if !cfg.TLSEnabled() {
		t.Fatal("有域名时应当启用 TLS")
	}
}

func TestDevModeDisablesTLS(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	t.Setenv("GUANDAN_DOMAIN", "guandan.id")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Dev {
		t.Fatal("GUANDAN_DEV=1 应当进入开发模式")
	}
	if cfg.TLSEnabled() {
		t.Fatal("开发模式不该启用 TLS")
	}
}

// §10.5：CSRF 只接受本站来源。
func TestAllowsOrigin(t *testing.T) {
	cfg := config.Config{PublicURL: "https://guandan.id", Domain: "guandan.id,backup.example.com"}

	for _, origin := range []string{"https://guandan.id", "https://backup.example.com"} {
		if !cfg.AllowsOrigin(origin) {
			t.Errorf("%s 应当被接受", origin)
		}
	}
	for _, origin := range []string{"", "https://evil.example", "http://guandan.id", "null"} {
		if cfg.AllowsOrigin(origin) {
			t.Errorf("%q 不该被接受", origin)
		}
	}
}

// 开发模式下页面由 Vite 提供，所以要放行本机来源（§13.5）。
func TestDevAllowsLocalOrigins(t *testing.T) {
	cfg := config.Config{Dev: true}

	for _, origin := range []string{"http://localhost:5173", "http://127.0.0.1:8080"} {
		if !cfg.AllowsOrigin(origin) {
			t.Errorf("%s 在开发模式下应当被接受", origin)
		}
	}
	if cfg.AllowsOrigin("https://evil.example") {
		t.Error("开发模式也不该接受外站来源")
	}
}

func TestLoadValidatesBackupTarget(t *testing.T) {
	cases := []struct {
		url, access string
		ok          bool
	}{
		{"file:///backup-remote", "", true},
		{"https://s3.ap-southeast-1.amazonaws.com/guandan/prod", "AKID", true},
		{"https://s3.example.com/guandan", "", false},           // 缺凭据
		{"https://s3.example.com/", "AKID", false},              // 缺 bucket
		{"https://AKID:secret@s3.example.com/b", "AKID", false}, // 凭据不能放 URL 里
		{"s3://bucket/backup", "AKID", false},
	}
	for _, c := range cases {
		clearEnv(t)
		t.Setenv("GUANDAN_DEV", "1")
		t.Setenv("GUANDAN_BACKUP_URL", c.url)
		t.Setenv("GUANDAN_BACKUP_KEY", "age1example")
		if c.access != "" {
			t.Setenv("AWS_ACCESS_KEY_ID", c.access)
			t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
		}
		_, err := config.Load()
		if (err == nil) != c.ok {
			t.Errorf("%s：期望 ok=%v，实际 err=%v", c.url, c.ok, err)
		}
	}
}

func TestMetricsAddrDefaultsToLoopback(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsAddr != "127.0.0.1:9090" {
		t.Fatalf("MetricsAddr = %q", cfg.MetricsAddr)
	}
}

// 默认不信任任何代理；生产拓扑（nginx → docker 网桥 → 容器）显式设置网桥网关。
func TestTrustedProxies(t *testing.T) {
	clientIP := func(cfg config.Config) string {
		req := httptest.NewRequest("GET", "/", nil)
		req.RemoteAddr = "172.17.0.1:40000"
		req.Header.Set("X-Real-IP", "198.51.100.9")
		return cfg.TrustedProxies.ClientIP(req).String()
	}

	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := clientIP(cfg); got != "172.17.0.1" {
		t.Fatalf("默认不该信任任何代理，得到 %s", got)
	}

	t.Setenv("GUANDAN_TRUSTED_PROXIES", "172.17.0.1/32")
	cfg, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := clientIP(cfg); got != "198.51.100.9" {
		t.Fatalf("显式信任网桥网关后应采用 X-Real-IP，得到 %s", got)
	}

	t.Setenv("GUANDAN_TRUSTED_PROXIES", "not-a-cidr")
	if _, err := config.Load(); err == nil {
		t.Fatal("不合法的网段应报错")
	}
}

// 群信息（#108）：没配就是空；WhatsApp 群链接会原样放进页面的链接里，只接受 https。
func TestLoadGroupInfo(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	if c, err := config.Load(); err != nil || c.GroupWeChatID != "" || c.GroupWhatsAppURL != "" {
		t.Fatalf("没配置时应为空：%+v %v", c, err)
	}
	t.Setenv("GUANDAN_GROUP_WECHAT_ID", " jktguandan ")
	t.Setenv("GUANDAN_GROUP_WHATSAPP_URL", "https://chat.whatsapp.com/AbCdEf")
	c, err := config.Load()
	if err != nil || c.GroupWeChatID != "jktguandan" || c.GroupWhatsAppURL != "https://chat.whatsapp.com/AbCdEf" {
		t.Fatalf("%+v %v", c, err)
	}
	for _, bad := range []string{"javascript:alert(1)", "http://chat.whatsapp.com/x", "chat.whatsapp.com/x", "https://"} {
		t.Setenv("GUANDAN_GROUP_WHATSAPP_URL", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("GUANDAN_GROUP_WHATSAPP_URL=%q 应当报错", bad)
		}
	}
}

// 小游戏登录（#84）：AppID 和 AppSecret 都可选，但只设一个是配错了。
func TestLoadWxCredentials(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	if c, err := config.Load(); err != nil || c.WxEnabled() {
		t.Fatalf("没配置时应关闭：%+v %v", c, err)
	}
	t.Setenv("GUANDAN_WX_APPID", " wx123 ")
	if _, err := config.Load(); err == nil {
		t.Fatal("只设 AppID 应报错")
	}
	t.Setenv("GUANDAN_WX_SECRET", "s3cret")
	c, err := config.Load()
	if err != nil || !c.WxEnabled() || c.WxAppID != "wx123" || c.WxSecret != "s3cret" {
		t.Fatalf("%+v %v", c, err)
	}
}

// 订阅消息模板（#137）：字段名默认 thing1,time2,thing3；写错了启动就报错；没配 AppID / Secret 时通道关闭。
func TestLoadWxTemplates(t *testing.T) {
	clearEnv(t)
	t.Setenv("GUANDAN_DEV", "1")
	t.Setenv("GUANDAN_WX_TMPL_REMIND", "abc_DEF-123")
	t.Setenv("GUANDAN_WX_TMPL_FULL", "xyz")
	t.Setenv("GUANDAN_WX_TMPL_FULL_KEYS", " thing4, date5 ,phrase6")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.WxTmplRemind.ID != "abc_DEF-123" || c.WxTmplRemind.Keys != [3]string{"thing1", "time2", "thing3"} {
		t.Fatalf("remind %+v", c.WxTmplRemind)
	}
	if c.WxTmplFull.Keys != [3]string{"thing4", "date5", "phrase6"} {
		t.Fatalf("full %+v", c.WxTmplFull)
	}
	if len(c.WxSubscribeTemplates()) != 2 {
		t.Fatal("开发模式没配 AppID / Secret 时也开（发送只记日志）")
	}
	c.Dev = false
	if len(c.WxSubscribeTemplates()) != 0 {
		t.Fatal("非开发模式没配 AppID / Secret 时通道应关闭")
	}
	t.Setenv("GUANDAN_WX_APPID", "wxapp")
	t.Setenv("GUANDAN_WX_SECRET", "sec")
	c, _ = config.Load()
	if got := c.WxSubscribeTemplates(); len(got) != 2 || got[config.WxTmplFull].ID != "xyz" {
		t.Fatalf("%+v", got)
	}

	for k, v := range map[string]string{
		"GUANDAN_WX_TMPL_FULL_KEYS": "thing1,time2",
		"GUANDAN_WX_TMPL_REMIND":    `a"b`,
	} {
		t.Run(k, func(t *testing.T) {
			t.Setenv(k, v)
			if _, err := config.Load(); err == nil {
				t.Fatalf("%s=%q 应报错", k, v)
			}
		})
	}
}
