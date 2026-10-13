// Package clientip 从请求里取真实的客户端 IP（docs/server-design.md §12.3 的按 IP 限流要用）。
//
// 生产环境是 nginx（宿主机）→ Docker 容器：容器看到的对端地址永远是 docker 网桥网关，
// 真实地址在 nginx 加的 X-Real-IP / X-Forwarded-For 里。这两个头客户端也能自己伪造，
// 所以只有对端落在可信网段（GUANDAN_TRUSTED_PROXIES）里时才看它们，否则一律用对端地址。
//
// 默认不信任任何代理：容器直接对外（§8）时，经 docker-proxy 转进来的连接（宿主机 IPv6、rootless
// Docker、回环）对端也是网桥网关，默认信任网桥就等于让任何人伪造 IP、绕过按 IP 的限流。
// 前面确实有反向代理、并且它会覆盖 X-Real-IP 时，才在部署时显式设置。
//
// 带 zone 的 IPv6 地址（fe80::1%eth0）在转发头里一律视为不合法：zone 是任意字符串，
// 采信它等于让请求方随意指定限流的键。
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver 按可信网段解析客户端 IP。零值不信任任何代理，直接用对端地址。
type Resolver struct {
	trusted []netip.Prefix
}

// Parse 解析逗号分隔的网段列表，单个地址按 /32 或 /128 处理。空字符串表示不信任任何代理。
// IPv4 映射形式的网段（::ffff:10.0.0.0/104）换算成对应的 IPv4 网段，否则它永远匹配不上
// （待查地址会先 Unmap 成 IPv4）；带 zone 的地址报错。
func Parse(spec string) (Resolver, error) {
	var r Resolver
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var p netip.Prefix
		if strings.Contains(part, "/") {
			var err error
			if p, err = netip.ParsePrefix(part); err != nil {
				return Resolver{}, fmt.Errorf("可信代理 %q 不是合法的地址或网段", part)
			}
		} else {
			addr, err := netip.ParseAddr(part)
			if err != nil || addr.Zone() != "" {
				return Resolver{}, fmt.Errorf("可信代理 %q 不是合法的地址或网段", part)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return Resolver{}, fmt.Errorf("可信代理 %q：IPv4 映射网段的前缀至少要 /96", part)
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		r.trusted = append(r.trusted, p.Masked())
	}
	return r, nil
}

// String 列出生效的可信网段，启动日志里打印出来方便核对；空表示不信任任何代理。
func (r Resolver) String() string {
	if len(r.trusted) == 0 {
		return "（不信任任何代理）"
	}
	parts := make([]string, len(r.trusted))
	for i, p := range r.trusted {
		parts[i] = p.String()
	}
	return strings.Join(parts, ",")
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
		if addr, ok := parseHeaderAddr(v); ok {
			return addr
		}
	}
	var hops []string
	for _, h := range req.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		addr, ok := parseHeaderAddr(strings.TrimSpace(hops[i]))
		if !ok {
			return peer
		}
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

// parseHeaderAddr 解析转发头里的一个地址。带 zone 的一律不合法。
func parseHeaderAddr(v string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(v)
	if err != nil || addr.Zone() != "" {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

// parseHostPort 解析对端地址。对端来自内核，链路本地地址可能带 zone（fe80::1%eth0），
// 去掉 zone 再用：限流的键不该随网卡名变化，改写后的 RemoteAddr 也必须能被 SplitHostPort 解析。
func parseHostPort(remote string) (netip.Addr, bool) {
	host := remote
	if h, _, err := net.SplitHostPort(remote); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone("").Unmap(), true
}
