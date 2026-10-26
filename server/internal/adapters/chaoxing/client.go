// Package chaoxing 实现学习通原生认证、只读核验、受控HTTP任务执行与章节测验提交。
//
// 平台连接与执行分别默认关闭。执行器不提供伪造时长、重复参数或跳过验证的开关；
// 上游请求经过域名、TLS、目标IP、重定向、响应体积和速率限制，
// 并复用服务端已核对的平台会话。请求执行记录与真实媒体记录分开。
package chaoxing

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const adapterVersion = "chaoxing-read-v1"

const loginKey = "u2oh6Vu^HWe4_AES"

var defaultEndpoints = Endpoints{
	CourseList: "https://mooc2-ans.chaoxing.com/mooc2-ans/visit/courselistdata",
	Catalog:    "https://mooc2-ans.chaoxing.com/mooc2-ans/mycourse/studentcourse",
	Cards:      "https://mooc1.chaoxing.com/mooc-ans/knowledge/cards",
	Study:      "https://mooc1.chaoxing.com/mooc-ans/mycourse/studentstudyAjax",
	Login:      "https://passport2.chaoxing.com/fanyalogin",
	Work:       "https://mooc1.chaoxing.com/mooc-ans/api/work",
	WorkSubmit: "https://mooc1.chaoxing.com/mooc-ans/work/addStudentWorkNew",
}

// Endpoints 仅用于固定上游入口和隔离契约测试，不接受用户输入。
type Endpoints struct {
	CourseList string
	Catalog    string
	Cards      string
	Study      string
	Login      string
	Work       string
	WorkSubmit string
}

type httpOptions struct {
	AllowedHosts       []string
	MinRequestInterval time.Duration
	Timeout            time.Duration
	MaxResponseBytes   int64
	// allowPrivateNetworks 与 newClient 只供隔离测试使用，不导出给生产接线。
	allowPrivateNetworks bool
	newClient            func(jar http.CookieJar) *http.Client
}

type Options struct {
	httpOptions
	ReadOnlyEnabled bool
	MaxChapters     int
	Endpoints       Endpoints
}

type VerifierOptions struct {
	httpOptions
	Enabled bool
	// LoginEnabled 与 ReadOnlyAuthorized 是显式授权门槛；默认 false。
	LoginEnabled       bool
	ReadOnlyAuthorized bool
	// KnownNamespace 是配置接线兼容字段；Namespace 优先。
	KnownNamespace string
	Namespace      string
	EvidenceRef    string
	SubjectProbe   SubjectProbe
	UserAgent      string
	Endpoints      Endpoints
}

type clientFactory struct {
	options httpOptions
	limiter *rateLimiter
}

func normalizeOptions(options *httpOptions) error {
	if len(options.AllowedHosts) == 0 {
		options.AllowedHosts = []string{"chaoxing.com"}
	}
	for index, host := range options.AllowedHosts {
		host = strings.TrimSpace(strings.ToLower(host))
		if host == "" || strings.ContainsAny(host, "/:@ \t\r\n") {
			return errors.New("上游允许域名无效")
		}
		options.AllowedHosts[index] = host
	}
	if options.MinRequestInterval <= 0 {
		options.MinRequestInterval = 300 * time.Millisecond
	}
	if options.MinRequestInterval < 50*time.Millisecond {
		return errors.New("上游请求间隔不得低于 50 毫秒")
	}
	if options.Timeout <= 0 {
		options.Timeout = 20 * time.Second
	}
	if options.Timeout > 2*time.Minute {
		return errors.New("上游超时过长")
	}
	if options.MaxResponseBytes <= 0 {
		options.MaxResponseBytes = 2 << 20
	}
	if options.MaxResponseBytes > 8<<20 {
		return errors.New("上游响应上限过大")
	}
	return nil
}

type rateLimiter struct {
	once sync.Once
	gate chan struct{}
	next time.Time
}

// wait 按实际放行时间推进下一时刻，避免预留时间槽在调度延迟后形成突发。
// 等待名额和等待发送间隔均可取消，不会因其他账号排队而阻塞停止请求。
func (r *rateLimiter) wait(ctx context.Context, interval time.Duration) error {
	r.once.Do(func() { r.gate = make(chan struct{}, 1) })
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if delay := time.Until(r.next); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.next = time.Now().Add(interval)
	return nil
}

func (c *clientFactory) client(jar http.CookieJar) *http.Client {
	if c.options.newClient != nil {
		return c.options.newClient(jar)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// 不读取环境代理，避免会话被转发到任意代理。
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          8,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		TLSClientConfig:       tlsConfig(),
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("上游地址无效")
			}
			if !c.allowedHost(host) {
				return nil, errors.New("上游主机不在允许范围")
			}
			addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil || len(addresses) == 0 {
				return nil, errors.New("无法解析上游主机")
			}
			for _, address := range addresses {
				if !c.options.allowPrivateNetworks && !isPublicAddress(address) {
					continue
				}
				connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.String(), port))
				if err == nil {
					return connection, nil
				}
			}
			return nil, errors.New("上游目标地址不可用")
		},
	}
	return &http.Client{
		Jar:       jar,
		Transport: transport,
		Timeout:   c.options.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("上游重定向次数过多")
			}
			if !c.allowedURL(req.URL) {
				return errors.New("上游重定向目标不在允许范围")
			}
			return nil
		},
	}
}

func (c *clientFactory) allowedHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	for _, allowed := range c.options.AllowedHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func (c *clientFactory) allowedURL(target *url.URL) bool {
	if target == nil || target.Scheme != "https" || target.User != nil {
		return false
	}
	if !c.allowedHost(target.Hostname()) {
		return false
	}
	if !c.options.allowPrivateNetworks && target.Port() != "" && target.Port() != "443" {
		return false
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

func isPublicAddress(address netip.Addr) bool {
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

func tlsConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12}
}

// Platform 实现 learning.Platform 与 learning.Verifier（只读核验）接口。
type Platform struct {
	options       platformOptions
	factory       clientFactory
	resourceCache resourceCache
	discoverSlots chan struct{}
}

type platformOptions struct {
	Options
	endpoints Endpoints
}

func NewPlatform(options Options) (*Platform, error) {
	if err := normalizeOptions(&options.httpOptions); err != nil {
		return nil, err
	}
	if options.MaxChapters <= 0 {
		options.MaxChapters = 300
	}
	if options.MaxChapters > 1000 {
		return nil, errors.New("章节上限过大")
	}
	endpoints := options.Endpoints
	if endpoints.CourseList == "" {
		endpoints.CourseList = defaultEndpoints.CourseList
	}
	if endpoints.Catalog == "" {
		endpoints.Catalog = defaultEndpoints.Catalog
	}
	if endpoints.Cards == "" {
		endpoints.Cards = defaultEndpoints.Cards
		if endpoints.Study == "" {
			endpoints.Study = defaultEndpoints.Study
		}
	}
	if endpoints.Work == "" {
		endpoints.Work = defaultEndpoints.Work
	}
	if endpoints.WorkSubmit == "" {
		endpoints.WorkSubmit = defaultEndpoints.WorkSubmit
	}
	for _, raw := range []string{endpoints.CourseList, endpoints.Catalog, endpoints.Cards, endpoints.Work, endpoints.WorkSubmit, endpoints.Study} {
		if raw == "" {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil {
			return nil, errors.New("上游入口必须是 HTTPS 地址")
		}
	}
	return &Platform{options: platformOptions{Options: options, endpoints: endpoints},
		factory:       clientFactory{options: options.httpOptions, limiter: &rateLimiter{}},
		discoverSlots: make(chan struct{}, 4)}, nil
}

func (p *Platform) Capabilities() learning.PlatformCapabilities {
	capabilities := learning.PlatformCapabilities{
		Adapter: adapterVersion, CourseRead: p.options.ReadOnlyEnabled,
		TaskCheck: p.options.ReadOnlyEnabled, CredentialVerify: false,
		ReadRequiresSession: true,
	}
	if !p.options.ReadOnlyEnabled {
		capabilities.DisabledReason = "chaoxing_read_disabled"
	}
	return capabilities
}

func (p *Platform) Verify(ctx context.Context, request learning.VerificationRequest) (learning.PlatformCheck, error) {
	return p.CheckTask(ctx, request.Session, request.Task)
}

func (p *Platform) CheckTask(ctx context.Context, sessionBytes []byte, ref learning.PlatformTaskRef) (learning.PlatformCheck, error) {
	if !p.options.ReadOnlyEnabled {
		return learning.PlatformCheck{Status: learning.CheckUnknown}, learning.Fail(learning.FailureCapabilityMissing, "学习通只读适配未启用")
	}
	session, err := decodeSession(sessionBytes)
	if err != nil {
		return learning.PlatformCheck{Status: learning.CheckUnknown}, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	ctx, cleanup, err := p.withSessionClient(ctx, session)
	if err != nil {
		return learning.PlatformCheck{Status: learning.CheckUnknown}, err
	}
	defer cleanup()
	attachments, pending, err := p.readTaskAttachments(ctx, session, ref)
	if err != nil {
		return learning.PlatformCheck{Status: learning.CheckUnknown}, err
	}
	for _, attachment := range attachments {
		if attachment.ObjectID != ref.ObjectID {
			continue
		}
		if ref.TaskPointID != "" && attachment.TaskPointID != ref.TaskPointID {
			continue
		}
		check := learning.PlatformCheck{CheckedAt: time.Now().UTC(), Source: "chaoxing.card",
			PlayTimeSeconds: pointer(attachment.PlayedSeconds)}
		if attachment.PassedKnown {
			if attachment.Passed {
				check.Status = learning.CheckConfirmed
			} else {
				check.Status = learning.CheckUnconfirmed
			}
			check.Passed = attachment.Passed
			return check, nil
		}
		if value, pendingKnown := pending[ref.ChapterExternalID]; pendingKnown {
			check.Status = learning.CheckUnconfirmed
			if value == 0 {
				check.Status = learning.CheckConfirmed
				check.Passed = true
			}
			return check, nil
		}
		check.Status = learning.CheckUnknown
		check.Note = "任务卡片没有明确的完成字段"
		return check, nil
	}
	return learning.PlatformCheck{Status: learning.CheckUnknown, CheckedAt: time.Now().UTC(),
		Source: "chaoxing.card", Note: "任务卡片中未找到对应任务点"}, nil
}

func pointer(value float64) *float64 { return &value }

// Verify 实现认证服务所需的外部身份验证契约。适配器返回的会话材料是
// 本包定义的 JSON，认证服务可原样加密保存，后续通过 learning.SessionResolver 交回。
func (v *Verifier) Verify(ctx context.Context, credentials auth.Credentials) (auth.VerifiedSubject, error) {
	if !v.options.Enabled {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureCapabilityMissing, "学习通身份验证未启用")
	}
	if !v.options.LoginEnabled {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureCapabilityMissing, "学习通登录未启用")
	}
	if !v.options.ReadOnlyAuthorized {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureCapabilityMissing, "学习通只读授权未开启")
	}
	namespace := strings.TrimSpace(v.options.Namespace)
	if namespace == "" {
		namespace = strings.TrimSpace(v.options.KnownNamespace)
	}
	if namespace == "" || strings.TrimSpace(v.options.EvidenceRef) == "" ||
		v.options.SubjectProbe == nil {
		// 稳定命名空间没有已核实证据时明确拒绝，不把 Cookie 自述 UID 当主体。
		return VerifiedSubject{}, learning.Fail(learning.FailureCapabilityMissing,
			"学习通稳定身份命名空间尚未核实，验证能力保持关闭")
	}
	if strings.TrimSpace(credentials.Account) == "" || strings.TrimSpace(credentials.Password) == "" {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureVerificationNeeded, "请填写学习通账号与密码")
	}
	if strings.TrimSpace(credentials.Verification) != "" {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureVerificationNeeded, "需要账号持有人完成平台验证")
	}
	session, err := v.login(ctx, credentials)
	if err != nil {
		return auth.VerifiedSubject{}, err
	}
	evidence, err := v.options.SubjectProbe.Probe(ctx, session)
	if err != nil {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureVerificationNeeded, "无法通过可信会话核对稳定主体")
	}
	if strings.TrimSpace(evidence.Namespace) != namespace {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureVerificationNeeded, "稳定身份命名空间与已核实证据不一致")
	}
	subject := strings.TrimSpace(evidence.Subject)
	if subject == "" || len(subject) > 256 {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureVerificationNeeded, "可信会话未返回稳定主体")
	}
	expires := evidence.ExpiresAt
	if expires.IsZero() || !expires.After(time.Now()) {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话到期时间无效")
	}
	session.Authorized = true
	encoded, err := encodeSession(session)
	if err != nil {
		return auth.VerifiedSubject{}, learning.Fail(learning.FailurePermanent, "无法编码平台会话")
	}
	school := strings.TrimSpace(evidence.School)
	if school == "" {
		school = strings.TrimSpace(credentials.School)
	}
	display := strings.TrimSpace(evidence.DisplayName)
	if display == "" {
		display = "学习通用户"
	}
	return auth.VerifiedSubject{
		Provider: "chaoxing", Namespace: namespace, Subject: subject,
		DisplayName: display, School: school, Session: encoded, ExpiresAt: expires,
	}, nil
}

// SubjectProbe 由已核实的可信会话查询实现。它必须独立于客户端 Cookie 自述字段
// 返回稳定主体，并给出命名空间证据引用。
type SubjectProbe interface {
	Probe(ctx context.Context, session Session) (SubjectEvidence, error)
}

type SubjectEvidence struct {
	Namespace   string
	Subject     string
	DisplayName string
	School      string
	ExpiresAt   time.Time
}

// Credentials/VerifiedSubject 直接使用认证服务类型。
type Credentials = auth.Credentials
type VerifiedSubject = auth.VerifiedSubject

type Verifier struct {
	options  VerifierOptions
	factory  clientFactory
	endpoint string
}

func NewVerifier(options VerifierOptions) (*Verifier, error) {
	if err := normalizeOptions(&options.httpOptions); err != nil {
		return nil, err
	}
	endpoint := options.Endpoints.Login
	if endpoint == "" {
		endpoint = defaultEndpoints.Login
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil {
		return nil, errors.New("登录入口必须是 HTTPS 地址")
	}
	if options.UserAgent == "" {
		options.UserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	}
	return &Verifier{options: options, endpoint: endpoint,
		factory: clientFactory{options: options.httpOptions, limiter: &rateLimiter{}}}, nil
}

func (v *Verifier) login(ctx context.Context, credentials auth.Credentials) (Session, error) {
	jar, err := newRecordingJar()
	if err != nil {
		return Session{}, learning.Fail(learning.FailurePermanent, "无法建立会话容器")
	}
	client := v.factory.client(jar)
	defer client.CloseIdleConnections()
	form := url.Values{
		"fid": {schoolFID(credentials.School)}, "uname": {encryptField(credentials.Account)},
		"password": {encryptField(credentials.Password)}, "refer": {"https%3A%2F%2Fi.chaoxing.com"},
		"t": {"true"}, "forbidotherlogin": {"0"}, "validate": {""}, "doubleFactorLogin": {"0"},
		"independentId": {"0"}, "independentNameId": {"0"},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, v.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Session{}, learning.Fail(learning.FailurePermanent, "无法建立登录请求")
	}
	request.Header.Set("User-Agent", v.options.UserAgent)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	request.Header.Set("Origin", "https://passport2.chaoxing.com")
	request.Header.Set("Referer", "https://passport2.chaoxing.com/login?fid=&refer=https%3A%2F%2Fi.chaoxing.com")
	response, err := client.Do(request)
	if err != nil {
		return Session{}, learning.Fail(learning.FailureTransient, "登录请求未完成")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, v.options.MaxResponseBytes+1))
	if err != nil || int64(len(body)) > v.options.MaxResponseBytes {
		return Session{}, learning.Fail(learning.FailureTransient, "登录响应不可用")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return Session{}, learning.Fail(learning.FailureRateLimited, "平台限流，请稍后再试")
	}
	if response.StatusCode != http.StatusOK {
		return Session{}, learning.Fail(learning.FailureTransient, "登录接口未成功")
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return Session{}, learning.Fail(learning.FailureCapabilityMissing, "登录响应结构未确认")
	}
	if value, _ := payload["containTwoFactorLogin"].(bool); value {
		return Session{}, learning.Fail(learning.FailureVerificationNeeded, "平台要求二次验证")
	}
	if status, _ := payload["status"].(bool); !status {
		message := text(payload["msg2"])
		if message == "" {
			message = text(payload["msg"])
		}
		if strings.Contains(message, "验证") {
			return Session{}, learning.Fail(learning.FailureVerificationNeeded, "平台要求验证码或身份验证")
		}
		return Session{}, auth.ErrInvalidCredentials
	}
	cookies := jar.export()
	if len(cookies) == 0 {
		return Session{}, learning.Fail(learning.FailureIdentityInvalid, "登录后没有取得平台会话")
	}
	return Session{Cookies: cookies, UserAgent: v.options.UserAgent, Authorized: true}, nil
}

func schoolFID(school string) string {
	value := strings.TrimSpace(school)
	if value == "" {
		return "-1"
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return "-1"
		}
	}
	return value
}

func encryptField(message string) string {
	data := []byte(message)
	padding := aes.BlockSize - len(data)%aes.BlockSize
	data = append(data, bytesRepeat(byte(padding), padding)...)
	block, err := aes.NewCipher([]byte(loginKey))
	if err != nil {
		return ""
	}
	output := make([]byte, len(data))
	cipher.NewCBCEncrypter(block, []byte(loginKey)).CryptBlocks(output, data)
	return base64.StdEncoding.EncodeToString(output)
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
