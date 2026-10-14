package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nginx 直接返回静态文件时用 deploy/nginx/guandan-security.conf 里的安全头，
// 必须和应用自己发的一致，否则改了一边忘了另一边（§10.6）。
func TestNginxSnippetMatchesAppSecurityHeaders(t *testing.T) {
	b, err := os.ReadFile("../../../deploy/nginx/guandan-security.conf")
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

// 站点模板过 scripts/check-nginx.sh；把 /ws 退回成只写 Upgrade、Connection 的写法（coord 在线上
// 比对时发现的漏洞：location 里写了 proxy_set_header 就不再继承 server 级的 X-Real-IP），检查必须报错。
func TestNginxSiteTemplatePassesCheck(t *testing.T) {
	if _, err := exec.LookPath("awk"); err != nil {
		t.Skip("没有 awk")
	}
	const tmpl = "../../../deploy/nginx/guandan.synon.ai.conf"
	run := func(path string) (string, error) {
		out, err := exec.Command("sh", "../../../scripts/check-nginx.sh", path).CombinedOutput()
		return string(out), err
	}
	if out, err := run(tmpl); err != nil {
		t.Fatalf("模板没通过检查：%v\n%s", err, out)
	}

	b, err := os.ReadFile(tmpl)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	inWS := false
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, "location = /ws") {
			inWS = true
		}
		if inWS && strings.Contains(line, "}") {
			inWS = false
		}
		if inWS && strings.Contains(line, "proxy_set_header") &&
			!strings.Contains(line, "Upgrade") && !strings.Contains(line, "Connection") {
			continue
		}
		kept = append(kept, line)
	}
	bad := filepath.Join(t.TempDir(), "bad.conf")
	if err := os.WriteFile(bad, []byte(strings.Join(kept, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := run(bad)
	if err == nil || !strings.Contains(out, "location = /ws") || !strings.Contains(out, "X-Real-IP") {
		t.Fatalf("/ws 漏了 X-Real-IP 时检查应报错，实际 %v\n%s", err, out)
	}
	if strings.Contains(string(b), ":9090") || strings.Contains(string(b), "location /metrics") {
		t.Error("/metrics 不能经 nginx 对外")
	}
}
