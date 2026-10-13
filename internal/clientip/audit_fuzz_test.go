package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"testing"
)

// FuzzAuditClientIP 来自 auditor 对 #35 的复审：
//   - 对端不可信时，改写后的 RemoteAddr 必须就是对端地址（转发头一概不看）；
//   - 改写后的 RemoteAddr 必须能被 SplitHostPort 解析（zone 里带 [ 时曾经不能）。
func FuzzAuditClientIP(f *testing.F) {
	f.Add("172.17.0.1:5000", "", "1.2.3.4, 10.0.0.1")
	f.Add("172.17.0.1:5000", "fe80::1%x", "")
	f.Add("172.17.0.1:5000", "::%[", "")
	f.Add("[fe80::1%eth0]:1", "", "")
	f.Add("[::1]:1", "", "[2001:db8::1]:80,,")
	f.Add("8.8.8.8:1", "9.9.9.9", "7.7.7.7")
	r, err := Parse("127.0.0.0/8,::1/128,172.16.0.0/12")
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, remote, xri, xff string) {
		req, _ := http.NewRequest("GET", "/", nil)
		req.RemoteAddr = remote
		if xri != "" {
			req.Header.Set("X-Real-IP", xri)
		}
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		var seen string
		r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, q *http.Request) { seen = q.RemoteAddr })).ServeHTTP(nil, req)
		peer, ok := parseHostPort(remote)
		if ok && !r.Trusted(peer) {
			if h, _, _ := net.SplitHostPort(seen); h != peer.String() {
				t.Fatalf("对端 %q 不可信，却得到 %q", remote, seen)
			}
		}
		if seen != remote {
			host, _, err := net.SplitHostPort(seen)
			if err != nil {
				t.Fatalf("改写后的 RemoteAddr 解析不了：%q", seen)
			}
			if a, err := netip.ParseAddr(host); err != nil || a.Zone() != "" {
				t.Fatalf("改写后的 RemoteAddr 不是不带 zone 的 IP：%q", seen)
			}
		}
	})
}
