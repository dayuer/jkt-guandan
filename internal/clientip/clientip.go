// Package clientip 从请求里取真实的客户端 IP（docs/server-design.md §12.3 的按 IP 限流要用）。
//
// 生产环境是 nginx（宿主机）→ Docker 容器：容器看到的对端地址永远是 docker 网桥或 127.0.0.1，
// 真实地址在 nginx 加的 X-Real-IP / X-Forwarded-For 里。这两个头客户端也能自己伪造，
// 所以只有对端落在可信网段（GUANDAN_TRUSTED_PROXIES）里时才看它们，否则一律用对端地址。
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies 是默认信任的反向代理网段：本机回环，加 Docker 默认的网桥网段
// （docker0 是 172.17.0.0/16，自建网络从 172.18 往后分，都在 172.16.0.0/12 里）。
const DefaultTrustedProxies = "127.0.0.0/8,::1/128,172.16.0.0/12"

// Resolver 按可信网段解析客户端 IP。零值不信任任何代理，直接用对端地址。
type Resolver struct {
	trusted []netip.Prefix
}

// Parse 解析逗号分隔的网段列表，单个地址按 /32 或 /128 处理。空字符串表示不信任任何代理。
func Parse(spec string) (Resolver, error) {
	var r Resolver
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !strings.Contains(part, "/") {
			addr, err := netip.ParseAddr(part)
			if err != nil {
				return Resolver{}, fmt.Errorf("可信代理 %q 不是合法的地址或网段", part)
			}
			r.trusted = append(r.trusted, netip.PrefixFrom(addr.Unmap(), addr.Unmap().BitLen()))
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return Resolver{}, fmt.Errorf("可信代理 %q 不是合法的地址或网段", part)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// Trusted 说明 addr 是否属于可信代理。
func (r Resolver) Trusted(addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, p := range r.trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// ClientIP 返回请求的客户端 IP：
//  1. 对端（RemoteAddr）不是可信代理：就是对端地址，转发头一概不看；
//  2. 对端是可信代理，且有合法的 X-Real-IP：用它（nginx 用 $remote_addr 覆盖设置，客户端改不了）；
//  3. 否则看 X-Forwarded-For，从右往左跳过可信代理，取第一个不可信的地址——
//     左边的部分是客户端自己能写的，不能信；遇到不合法的条目就停下，退回对端地址；
//  4. 都没有，用对端地址。
func (r Resolver) ClientIP(req *http.Request) netip.Addr {
	peer, ok := parseHostPort(req.RemoteAddr)
	if !ok || !r.Trusted(peer) {
		return peer
	}
	if v := strings.TrimSpace(req.Header.Get("X-Real-IP")); v != "" {
		if addr, err := netip.ParseAddr(v); err == nil {
			return addr.Unmap()
		}
	}
	var hops []string
	for _, h := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			return peer
		}
		addr = addr.Unmap()
		if !r.Trusted(addr) {
			return addr
		}
	}
	return peer
}

// Middleware 把 r.RemoteAddr 改写成「客户端 IP:0」。下游（按 IP 限流、访问日志）只看 RemoteAddr，
// 不用各自处理代理。对端地址解析不了（比如 unix socket）时保持原样。
func (r Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if ip := r.ClientIP(req); ip.IsValid() {
			req.RemoteAddr = net.JoinHostPort(ip.String(), "0")
		}
		next.ServeHTTP(w, req)
	})
}

func parseHostPort(remote string) (netip.Addr, bool) {
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}
