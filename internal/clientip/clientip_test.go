package clientip_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dayuer/jkt-guandan/internal/clientip"
)

func resolver(t *testing.T, spec string) clientip.Resolver {
	t.Helper()
	r, err := clientip.Parse(spec)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestClientIP(t *testing.T) {
	r := resolver(t, clientip.DefaultTrustedProxies)
	cases := []struct {
		name, remote, realIP string
		xff                  []string
		want                 string
	}{
		{"直连，没有代理", "203.0.113.7:51000", "", nil, "203.0.113.7"},
		{"直连的客户端伪造转发头，不理", "203.0.113.7:51000", "1.2.3.4", []string{"5.6.7.8"}, "203.0.113.7"},
		{"nginx 经 docker 网桥转发，用 X-Real-IP", "172.17.0.1:40000", "198.51.100.9", []string{"198.51.100.9"}, "198.51.100.9"},
		{"本机 nginx 转发", "127.0.0.1:40000", "198.51.100.9", nil, "198.51.100.9"},
		{"没有 X-Real-IP，取 XFF 最右边的不可信地址", "172.17.0.1:40000", "", []string{"6.6.6.6, 198.51.100.9"}, "198.51.100.9"},
		{"XFF 右边是可信代理，跳过", "172.17.0.1:40000", "", []string{"6.6.6.6, 198.51.100.9, 127.0.0.1"}, "198.51.100.9"},
		{"多个 XFF 头按顺序拼起来", "172.17.0.1:40000", "", []string{"6.6.6.6", "198.51.100.9"}, "198.51.100.9"},
		{"XFF 里有乱写的条目，退回对端", "172.17.0.1:40000", "", []string{"198.51.100.9, junk"}, "172.17.0.1"},
		{"X-Real-IP 不合法，退到 XFF", "172.17.0.1:40000", "not-an-ip", []string{"198.51.100.9"}, "198.51.100.9"},
		{"代理没带任何头，用对端", "172.17.0.1:40000", "", nil, "172.17.0.1"},
		{"IPv6 客户端", "[::1]:40000", "2001:db8::1", nil, "2001:db8::1"},
		{"IPv4 映射的 IPv6 对端也算可信", "[::ffff:127.0.0.1]:40000", "198.51.100.9", nil, "198.51.100.9"},
		{"公网的 172.32 不在 172.16.0.0/12 里", "172.32.0.1:40000", "198.51.100.9", nil, "172.32.0.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = c.remote
			if c.realIP != "" {
				req.Header.Set("X-Real-IP", c.realIP)
			}
			for _, v := range c.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			if got := r.ClientIP(req).String(); got != c.want {
				t.Fatalf("得到 %s，期望 %s", got, c.want)
			}
		})
	}
}

// 不信任任何代理时（GUANDAN_TRUSTED_PROXIES=""），转发头一概不看。
func TestEmptyTrustListIgnoresHeaders(t *testing.T) {
	r := resolver(t, "")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	if got := r.ClientIP(req).String(); got != "127.0.0.1" {
		t.Fatalf("得到 %s", got)
	}
}

func TestParse(t *testing.T) {
	r := resolver(t, " 10.0.0.5 , 192.168.0.0/16,::1")
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for remote, trusted := range map[string]bool{"10.0.0.5:1": true, "10.0.0.6:1": false, "192.168.9.9:1": true, "[::1]:1": true} {
		req.RemoteAddr = remote
		req.Header.Set("X-Real-IP", "198.51.100.9")
		if got := r.ClientIP(req).String() == "198.51.100.9"; got != trusted {
			t.Errorf("%s 可信=%v，期望 %v", remote, got, trusted)
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "example.com", "1.2.3"} {
		if _, err := clientip.Parse(bad); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestMiddlewareRewritesRemoteAddr(t *testing.T) {
	r := resolver(t, clientip.DefaultTrustedProxies)
	var seen string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { seen = req.RemoteAddr }))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.1:40000"
	req.Header.Set("X-Real-IP", "198.51.100.9")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "198.51.100.9:0" {
		t.Fatalf("RemoteAddr = %q", seen)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "@" // unix socket 之类解析不了的
	h.ServeHTTP(httptest.NewRecorder(), req)
	if seen != "@" {
		t.Fatalf("解析不了时应保持原样，实际 %q", seen)
	}
}
