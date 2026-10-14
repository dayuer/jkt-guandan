// Package config 读环境变量并校验，对应 docs/server-design.md §7、§12.1。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/dayuer/jkt-guandan/internal/clientip"
)

// DefaultTLSAddr 是启用 autocert 时的 TLS 监听地址。容器里监听 8443，
// 由宿主机把 443 映射过来——nonroot 用户不能监听 1024 以下的端口（§8、§10.12）。
const DefaultTLSAddr = ":8443"

// DefaultMetricsAddr 是 /metrics 的默认监听地址：只有本机能访问（§13.7）。
// 生产上用 docker exec guandan /guandan metrics 查看，不映射端口（runbook §6）。
const DefaultMetricsAddr = "127.0.0.1:9090"

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
	// BackupURL 是异地备份目标：https://<endpoint>/<bucket>[/<前缀>]（S3 兼容，path-style），
	// 或者 file:///<目录>（挂载的网络盘，也用于本机演练）。BackupKey 是备份加密用的 age 公钥。
	BackupURL string
	BackupKey string
	// BackupRegion、BackupAccessKey、BackupSecretKey 是 S3 签名用的区域和凭据，
	// 读标准的 AWS_REGION、AWS_ACCESS_KEY_ID、AWS_SECRET_ACCESS_KEY。
	BackupRegion    string
	BackupAccessKey string
	BackupSecretKey string
	// MetricsAddr 是 /metrics 的监听地址，默认只听本机（§13.7）。设为空字符串关闭。
	MetricsAddr string
	// TrustedProxies 是可信的反向代理网段：只有对端在这里面时，才采用 X-Real-IP / X-Forwarded-For
	// 里的客户端地址（按 IP 限流要用）。读 GUANDAN_TRUSTED_PROXIES，默认为空：不信任任何代理。
	// 只有前面有反向代理、并且代理会覆盖 X-Real-IP 时才设置（生产：172.17.0.1/32，见 runbook）。
	TrustedProxies clientip.Resolver
	// AnthropicKey 为空时蛋蛋不可用（phase:3）。
	AnthropicKey string
	// AgentModel 是蛋蛋用的 Claude 模型，读 GUANDAN_AGENT_MODEL；为空用 agent.DefaultModel。
	AgentModel string
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
		AgentModel:   strings.TrimSpace(os.Getenv("GUANDAN_AGENT_MODEL")),

		BackupRegion:    env("AWS_REGION", "us-east-1"),
		BackupAccessKey: strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID")),
		BackupSecretKey: strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY")),
		MetricsAddr:     DefaultMetricsAddr,
	}
	if v, ok := os.LookupEnv("GUANDAN_METRICS_ADDR"); ok {
		c.MetricsAddr = strings.TrimSpace(v)
	}

	// 默认不信任任何代理（clientip 包注释说明了为什么）。
	resolver, err := clientip.Parse(os.Getenv("GUANDAN_TRUSTED_PROXIES"))
	if err != nil {
		return Config{}, fmt.Errorf("GUANDAN_TRUSTED_PROXIES: %w", err)
	}
	c.TrustedProxies = resolver

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
	if c.BackupURL != "" {
		u, err := url.Parse(c.BackupURL)
		if err != nil {
			return Config{}, fmt.Errorf("GUANDAN_BACKUP_URL 解析失败: %w", err)
		}
		switch u.Scheme {
		case "file":
			if u.Path == "" {
				return Config{}, fmt.Errorf("GUANDAN_BACKUP_URL=%q 缺少目录", c.BackupURL)
			}
		case "https", "http":
			if u.Host == "" || strings.Trim(u.Path, "/") == "" {
				return Config{}, fmt.Errorf("GUANDAN_BACKUP_URL=%q 应为 https://<endpoint>/<bucket>[/<前缀>]", c.BackupURL)
			}
			if u.User != nil {
				return Config{}, fmt.Errorf("GUANDAN_BACKUP_URL 里不要放凭据，改用 AWS_ACCESS_KEY_ID 和 AWS_SECRET_ACCESS_KEY")
			}
			if c.BackupAccessKey == "" || c.BackupSecretKey == "" {
				return Config{}, fmt.Errorf("S3 备份需要 AWS_ACCESS_KEY_ID 和 AWS_SECRET_ACCESS_KEY")
			}
		default:
			return Config{}, fmt.Errorf("GUANDAN_BACKUP_URL=%q 只支持 https://、http:// 或 file://", c.BackupURL)
		}
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
