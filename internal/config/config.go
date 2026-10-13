// Package config 读环境变量，对应 docs/server-design.md §7。
package config

import (
	"os"
	"strings"
)

type Config struct {
	// DataDir 存放数据库、备份和证书缓存，是唯一的持久状态目录。
	DataDir string
	// Addr 是不启用内置 TLS 时的监听地址。
	Addr string
	// Domain 非空时启用 autocert，监听 :80 和 :443；可以写多个域名，逗号分隔。
	Domain string
	// PublicURL 生成分享链接和 OG 图 URL 时用；留空时按 Domain 推导。
	PublicURL string
	// AdminEmail 填给 Let's Encrypt，也用作 VAPID 的 subject。
	AdminEmail string
	// AnthropicKey 为空时蛋蛋不可用（phase:3）。
	AnthropicKey string
}

func Load() Config {
	c := Config{
		DataDir:      env("GUANDAN_DATA", "/data"),
		Addr:         env("GUANDAN_ADDR", ":8080"),
		Domain:       strings.TrimSpace(os.Getenv("GUANDAN_DOMAIN")),
		PublicURL:    strings.TrimSpace(os.Getenv("GUANDAN_PUBLIC_URL")),
		AdminEmail:   strings.TrimSpace(os.Getenv("GUANDAN_ADMIN_EMAIL")),
		AnthropicKey: strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")),
	}
	if c.PublicURL == "" && c.Domain != "" {
		c.PublicURL = "https://" + strings.Split(c.Domain, ",")[0]
	}
	return c
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
