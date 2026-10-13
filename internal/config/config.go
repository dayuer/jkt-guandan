// Package config 读环境变量并校验，对应 docs/server-design.md §7、§12.1。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// DefaultTLSAddr 是启用 autocert 时的 TLS 监听地址。容器里监听 8443，
// 由宿主机把 443 映射过来——nonroot 用户不能监听 1024 以下的端口（§8、§10.12）。
const DefaultTLSAddr = ":8443"

type Config struct {
	// DataDir 是唯一的持久状态目录：数据库、备份、证书缓存都在这里。
	DataDir string
	// Addr 是不启用内置 TLS 时的监听地址。
	Addr string
	// TLSAddr 是启用 autocert 时的 TLS 监听地址。
	TLSAddr string
	// Domain 非空时启用 autocert；可以写多个域名，逗号分隔（主域名加备用域名）。
	Domain string
	// PublicURL 生成分享链接和 OG 图 URL 时用；留空时按 Domain 推导。
	PublicURL string
	// AdminEmail 填给 Let's Encrypt，也用作 VAPID 的 subject。
	AdminEmail string
	// Dev 为真时进入开发模式：不启用 TLS，cookie 不加 Secure，日志用文本格式（§13.5）。
	Dev bool
	// BackupURL 是异地备份目标（S3 兼容），BackupKey 是备份加密用的 age 公钥。
	BackupURL string
	BackupKey string
	// AnthropicKey 为空时蛋蛋不可用（phase:3）。
	AnthropicKey string
}

// Domains 把逗号分隔的域名拆开，去掉空项。
func (c Config) Domains() []string {
	var out []string
	for _, d := range strings.Split(c.Domain, ",") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// TLSEnabled 说明是否由本进程直接对外提供 HTTPS。
func (c Config) TLSEnabled() bool { return !c.Dev && c.Domain != "" }

// AllowedOrigins 是 CSRF 校验和 WebSocket 握手接受的站点（§10.5）。
func (c Config) AllowedOrigins() []string {
	var out []string
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err == nil && u.Scheme != "" && u.Host != "" {
			out = append(out, u.Scheme+"://"+u.Host)
		}
	}
	for _, d := range c.Domains() {
		out = append(out, "https://"+d)
	}
	return out
}

// AllowsOrigin 判断一个 Origin 头是否属于本站。开发模式下额外放行本机来源，
// 因为那时页面由 Vite 的开发服务器提供（§13.5）。
func (c Config) AllowsOrigin(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range c.AllowedOrigins() {
		if strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return c.Dev && isLocalOrigin(origin)
}

func isLocalOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// Load 读环境变量并校验（§12.1 第 1 步：缺少必填项或值不合法就直接退出）。
func Load() (Config, error) {
	dev := isTruthy(os.Getenv("GUANDAN_DEV"))

	c := Config{
		DataDir:      env("GUANDAN_DATA", "/data"),
		Addr:         env("GUANDAN_ADDR", ":8080"),
		TLSAddr:      DefaultTLSAddr,
		Domain:       strings.TrimSpace(os.Getenv("GUANDAN_DOMAIN")),
		PublicURL:    strings.TrimSpace(os.Getenv("GUANDAN_PUBLIC_URL")),
		AdminEmail:   strings.TrimSpace(os.Getenv("GUANDAN_ADMIN_EMAIL")),
		Dev:          dev,
		BackupURL:    strings.TrimSpace(os.Getenv("GUANDAN_BACKUP_URL")),
		BackupKey:    strings.TrimSpace(os.Getenv("GUANDAN_BACKUP_KEY")),
		AnthropicKey: strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
	}

	if c.PublicURL == "" && c.Domain != "" {
		c.PublicURL = "https://" + c.Domains()[0]
	}

	// CSRF 校验只接受本站来源（§10.5）。生产环境不知道本站是谁，所有写请求都会被静默拒成 403，
	// 所以启动时就报错。开发模式下放行本机来源，可以不设。
	if !dev && c.PublicURL == "" {
		return Config{}, fmt.Errorf("非开发模式必须设置 GUANDAN_PUBLIC_URL 或 GUANDAN_DOMAIN，否则无法校验请求来源；本机调试请设 GUANDAN_DEV=1")
	}
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("GUANDAN_PUBLIC_URL=%q 不是合法的 http(s) 地址", c.PublicURL)
		}
	}
	if c.BackupURL != "" && c.BackupKey == "" {
		return Config{}, fmt.Errorf("设置了 GUANDAN_BACKUP_URL 就必须同时设置 GUANDAN_BACKUP_KEY（备份必须加密）")
	}
	if !dev && len(c.Domains()) > 0 && c.AdminEmail == "" {
		return Config{}, fmt.Errorf("启用自动签发证书（GUANDAN_DOMAIN）时必须设置 GUANDAN_ADMIN_EMAIL")
	}

	return c, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
