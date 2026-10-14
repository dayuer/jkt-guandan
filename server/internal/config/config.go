// Package config 读环境变量并校验，对应 docs/server-design.md §7、§12.1。
package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/dayuer/jkt-guandan/server/internal/clientip"
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
	// GroupWeChatID、GroupWhatsAppURL 是 09 牌友顶部「雅加达掼蛋群」横幅用的（#108），
	// 读 GUANDAN_GROUP_WECHAT_ID、GUANDAN_GROUP_WHATSAPP_URL；都为空时前端不显示横幅。
	GroupWeChatID    string
	GroupWhatsAppURL string
	// WxAppID、WxSecret 是小游戏登录（POST /api/wx/login，#84）调 code2Session 用的，读 GUANDAN_WX_APPID、
	// GUANDAN_WX_SECRET。要么都设、要么都不设；都不设时小游戏登录关闭（503 wx_disabled），
	// 开发模式下改为接受 dev:<openid> 形式的假 code。Secret 只放环境变量，不写日志。
	WxAppID  string
	WxSecret string
	// WxTmplFull、WxTmplRemind 是小游戏订阅消息的两个模板（#137，§5.3「订阅消息通道」），读
	// GUANDAN_WX_TMPL_FULL / GUANDAN_WX_TMPL_REMIND（模板 ID）和 …_KEYS（模板里「事项、时间、地点」
	// 三个字段的名字，逗号分隔，默认 thing1,time2,thing3）。没配 AppID / Secret 时整个通道关闭；
	// 某个模板没配时，它对应的提醒不走订阅消息。
	WxTmplFull   WxTemplate
	WxTmplRemind WxTemplate
}

// WxTemplate 是一个订阅消息模板：ID 和三个字段名（事项、时间、地点，顺序固定）。
type WxTemplate struct {
	ID   string
	Keys [3]string
}

// 订阅消息模板的用途，也是 GET /api/config 里 wx_subscribe 的键。
const (
	WxTmplFull   = "full"   // 牌局状态：成局、有人退出（full、seat_left）
	WxTmplRemind = "remind" // 开局提醒：开局前 1 小时、改时间（t_minus_1h、rescheduled）
)

// DefaultWxTmplKeys 是模板字段名的默认值：微信公共模板库里常见的「事项 / 时间 / 地点」排法。
const DefaultWxTmplKeys = "thing1,time2,thing3"

// WxSubscribeTemplates 是配置了的订阅消息模板，按用途（WxTmplFull、WxTmplRemind）索引。
// 没配 AppID / Secret 时为空：没有 access_token 发不出去，整个通道关闭。开发模式例外：
// 和小游戏登录接受 dev:<openid> 一样，配了模板就开，发送只记日志（小游戏开发者工具连本机用）。
func (c Config) WxSubscribeTemplates() map[string]WxTemplate {
	out := map[string]WxTemplate{}
	if !c.WxEnabled() && !c.Dev {
		return out
	}
	if c.WxTmplFull.ID != "" {
		out[WxTmplFull] = c.WxTmplFull
	}
	if c.WxTmplRemind.ID != "" {
		out[WxTmplRemind] = c.WxTmplRemind
	}
	return out
}

// WxEnabled 说明是否配置了真实的小游戏 AppID 和 AppSecret。
func (c Config) WxEnabled() bool { return c.WxAppID != "" && c.WxSecret != "" }

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

		GroupWeChatID:    strings.TrimSpace(os.Getenv("GUANDAN_GROUP_WECHAT_ID")),
		GroupWhatsAppURL: strings.TrimSpace(os.Getenv("GUANDAN_GROUP_WHATSAPP_URL")),

		WxAppID:  strings.TrimSpace(os.Getenv("GUANDAN_WX_APPID")),
		WxSecret: strings.TrimSpace(os.Getenv("GUANDAN_WX_SECRET")),

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
	// 群链接会原样放进页面的链接里：只接受 https，免得配错成 javascript: 之类。
	if c.GroupWhatsAppURL != "" {
		if u, err := url.Parse(c.GroupWhatsAppURL); err != nil || u.Scheme != "https" || u.Host == "" {
			return Config{}, fmt.Errorf("GUANDAN_GROUP_WHATSAPP_URL=%q 必须是 https 地址", c.GroupWhatsAppURL)
		}
	}

	if (c.WxAppID == "") != (c.WxSecret == "") {
		return Config{}, fmt.Errorf("GUANDAN_WX_APPID 和 GUANDAN_WX_SECRET 要么都设置，要么都不设置")
	}
	if c.WxTmplFull, err = wxTemplate("GUANDAN_WX_TMPL_FULL"); err != nil {
		return Config{}, err
	}
	if c.WxTmplRemind, err = wxTemplate("GUANDAN_WX_TMPL_REMIND"); err != nil {
		return Config{}, err
	}

	if !dev && len(c.Domains()) > 0 && c.AdminEmail == "" {
		return Config{}, fmt.Errorf("启用自动签发证书（GUANDAN_DOMAIN）时必须设置 GUANDAN_ADMIN_EMAIL")
	}

	return c, nil
}

// wxTemplate 读一个订阅消息模板：<name> 是模板 ID，<name>_KEYS 是三个字段名。
// 两者都会原样放进发给微信的 JSON，只接受字母、数字、下划线和短横线。
func wxTemplate(name string) (WxTemplate, error) {
	t := WxTemplate{ID: strings.TrimSpace(os.Getenv(name))}
	if t.ID == "" {
		return t, nil
	}
	if !wxToken(t.ID, 128) {
		return t, fmt.Errorf("%s=%q 不像订阅消息模板 ID", name, t.ID)
	}
	keys := strings.Split(env(name+"_KEYS", DefaultWxTmplKeys), ",")
	if len(keys) != 3 {
		return t, fmt.Errorf("%s_KEYS 要写三个字段名（事项、时间、地点），逗号分隔，比如 %s", name, DefaultWxTmplKeys)
	}
	for i, k := range keys {
		k = strings.TrimSpace(k)
		if !wxToken(k, 32) {
			return t, fmt.Errorf("%s_KEYS 第 %d 个字段名 %q 不合法", name, i+1, k)
		}
		t.Keys[i] = k
	}
	return t, nil
}

func wxToken(s string, max int) bool {
	if s == "" || len(s) > max {
		return false
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
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
