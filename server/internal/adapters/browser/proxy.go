package browser

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"
)

// connectProxy 是浏览器出站的唯一通道。只接受 CONNECT 到允许域名的 443，
// 解析后拒绝私网/回环/保留地址，并直接拨解析出的公网 IP。
// 不终止 TLS，因此证书仍由 Chrome 正常校验；不支持任意代理认证。
type connectProxy struct {
	listener  net.Listener
	allowed   []string
	maxConns  int
	slots     chan struct{}
	closeOnce sync.Once
	closed    chan struct{}
	wait      sync.WaitGroup
}

func startConnectProxy(allowed []string, maxConns int) (*connectProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("无法启动浏览器出站代理")
	}
	if maxConns <= 0 {
		maxConns = 16
	}
	proxy := &connectProxy{listener: listener, allowed: normalizeAllowedHosts(allowed),
		maxConns: maxConns, slots: make(chan struct{}, maxConns), closed: make(chan struct{})}
	proxy.wait.Add(1)
	go proxy.acceptLoop()
	return proxy, nil
}

func normalizeAllowedHosts(values []string) []string {
	if len(values) == 0 {
		values = []string{"chaoxing.com"}
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Trim(strings.ToLower(strings.TrimSpace(value)), ".")
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func (p *connectProxy) Addr() string { return p.listener.Addr().String() }

func (p *connectProxy) acceptLoop() {
	defer p.wait.Done()
	for {
		connection, err := p.listener.Accept()
		if err != nil {
			select {
			case <-p.closed:
				return
			default:
			}
			// 监听器暂时不可用时不要 busy loop。
			time.Sleep(20 * time.Millisecond)
			continue
		}
		select {
		case p.slots <- struct{}{}:
		default:
			connection.Close()
			continue
		}
		p.wait.Add(1)
		go func() {
			defer p.wait.Done()
			defer func() { <-p.slots }()
			p.handle(connection)
		}()
	}
}

func (p *connectProxy) handle(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(connection)
	request, err := http.ReadRequest(reader)
	if err != nil || request.Method != http.MethodConnect {
		writeProxyStatus(connection, http.StatusMethodNotAllowed)
		return
	}
	host, port, err := net.SplitHostPort(request.Host)
	if err != nil || port != "443" || !p.allowedHost(host) {
		writeProxyStatus(connection, http.StatusForbidden)
		return
	}
	address, err := resolvePublicAddress(host)
	if err != nil {
		writeProxyStatus(connection, http.StatusForbidden)
		return
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", net.JoinHostPort(address.String(), "443"))
	if err != nil {
		writeProxyStatus(connection, http.StatusBadGateway)
		return
	}
	defer target.Close()
	if _, err := io.WriteString(connection, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	_ = connection.SetDeadline(time.Time{})
	_ = target.SetDeadline(time.Time{})
	relayBoth(connection, target)
}

func (p *connectProxy) allowedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if net.ParseIP(host) != nil {
		return false
	}
	for _, allowed := range p.allowed {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func resolvePublicAddress(host string) (netip.Addr, error) {
	addresses, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil || len(addresses) == 0 {
		return netip.Addr{}, errors.New("无法解析目标")
	}
	for _, address := range addresses {
		if !isPublicIP(address) {
			return netip.Addr{}, errors.New("目标包含非公网地址")
		}
	}
	return addresses[0], nil
}

func isPublicIP(address netip.Addr) bool {
	address = address.Unmap()
	if !address.IsValid() || !address.IsGlobalUnicast() || address.IsPrivate() ||
		address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() || address.IsMulticast() {
		return false
	}
	for _, prefix := range deniedPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var deniedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001:db8::/32"),
}

func relayBoth(left, right net.Conn) {
	var wait sync.WaitGroup
	wait.Add(2)
	copyOne := func(dst, src net.Conn) {
		defer wait.Done()
		_, _ = io.Copy(dst, src)
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
	}
	go copyOne(left, right)
	go copyOne(right, left)
	wait.Wait()
}

func writeProxyStatus(connection net.Conn, status int) {
	_, _ = io.WriteString(connection, fmt.Sprintf("HTTP/1.1 %s\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		statusText(status)))
}

func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return strconv.Itoa(status) + " " + text
	}
	return strconv.Itoa(status)
}

func (p *connectProxy) Close() {
	p.closeOnce.Do(func() {
		close(p.closed)
		p.listener.Close()
		p.wait.Wait()
	})
}
