package app

import (
	"net"
	"net/http"
	"strings"
)

// localAdminRequest 只接受直接来自回环地址且 Host 为回环地址的请求；
// 不信任 X-Forwarded-For，避免本地开发自动登录被代理或域名重绑定借用。
func localAdminRequest(r *http.Request) bool {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	ip := net.ParseIP(strings.Trim(remote, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	host := r.Host
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip = net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
