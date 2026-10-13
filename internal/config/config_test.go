package config_test

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/dayuer/jkt-guandan/internal/config"
)

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"GUANDAN_DATA", "GUANDAN_ADDR", "GUANDAN_DOMAIN", "GUANDAN_PUBLIC_URL",
		"GUANDAN_ADMIN_EMAIL", "GUANDAN_DEV", "GUANDAN_BACKUP_URL",
		"GUANDAN_BACKUP_KEY", "ANTHROPIC_API_KEY",
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
