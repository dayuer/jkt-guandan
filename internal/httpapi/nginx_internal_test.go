package httpapi

import (
	"os"
	"strings"
	"testing"
)

// nginx 直接返回静态文件时用 deploy/nginx/guandan-security.conf 里的安全头，
// 必须和应用自己发的一致，否则改了一边忘了另一边（§10.6）。
func TestNginxSnippetMatchesAppSecurityHeaders(t *testing.T) {
	b, err := os.ReadFile("../../deploy/nginx/guandan-security.conf")
	if err != nil {
		t.Fatal(err)
	}
	snippet := string(b)
	for _, want := range []string{
		`add_header Content-Security-Policy "` + contentSecurityPolicy + `" always;`,
		`add_header X-Content-Type-Options "nosniff" always;`,
		`add_header X-Frame-Options "DENY" always;`,
		`add_header Referrer-Policy "strict-origin-when-cross-origin" always;`,
		`add_header Permissions-Policy "camera=(), microphone=(), geolocation=()" always;`,
		`add_header Strict-Transport-Security "max-age=31536000" always;`,
	} {
		if !strings.Contains(snippet, want) {
			t.Errorf("nginx 片段里缺少或不一致：%s", want)
		}
	}
}

// 站点模板必须覆盖设置两个客户端 IP 头（GUANDAN_TRUSTED_PROXIES 的前提），并且只代理到 127.0.0.1:18200。
func TestNginxSiteTemplate(t *testing.T) {
	b, err := os.ReadFile("../../deploy/nginx/guandan.synon.ai.conf")
	if err != nil {
		t.Fatal(err)
	}
	site := string(b)
	for _, want := range []string{
		"proxy_set_header X-Real-IP         $remote_addr;",
		"proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;",
		"server 127.0.0.1:18200;",
		"root /var/www/guandan/current;",
		"open_file_cache off;",
	} {
		if !strings.Contains(site, want) {
			t.Errorf("站点模板里缺少：%s", want)
		}
	}
	if strings.Contains(site, "location /metrics") || strings.Contains(site, ":9090;") {
		t.Error("/metrics 不能经 nginx 对外")
	}
}
