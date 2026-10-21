package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientKey 仅在连接对端属于显式信任代理时接受其覆盖的单一客户端 IP。
// 不接受多跳拼接，不保存或记录原始地址；认证服务会将返回值再次 HMAC 后存储。
func ClientKey(r *http.Request, trusted []netip.Prefix) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	address, err := netip.ParseAddr(remote)
	if err != nil {
		return "unknown"
	}
	address = address.Unmap()
	allowed := false
	for _, prefix := range trusted {
		if prefix.Contains(address) {
			allowed = true
			break
		}
	}
	if allowed {
		values := r.Header.Values("X-Forwarded-For")
		if len(values) == 1 && !strings.Contains(values[0], ",") {
			if value, err := netip.ParseAddr(strings.TrimSpace(values[0])); err == nil && value.IsValid() && !value.IsUnspecified() && !value.IsMulticast() {
				return value.Unmap().String()
			}
		}
	}
	return address.String()
}
