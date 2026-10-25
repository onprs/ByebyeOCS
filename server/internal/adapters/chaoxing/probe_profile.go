package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// ProfileProbeOptions 配置账户资料页探针。学习通网页没有同时提供稳定主体与
// 会话到期的 JSON 资料端点；账户管理页由服务端渲染稳定 uid，学校信息由
// cacheUserOrg 返回 JSON。到期时间使用保守 TTL，不读取 Cookie 自述 uid。
type ProfileProbeOptions struct {
	Enabled     bool
	AccountURL  string
	SchoolURL   string
	Namespace   string
	EvidenceRef string
	TTL         time.Duration

	AllowedHosts       []string
	MinRequestInterval time.Duration
	Timeout            time.Duration
	MaxResponseBytes   int64

	// 仅隔离测试使用，不导出给生产接线。
	allowPrivateNetworks bool
	newClient            func(jar http.CookieJar) *http.Client
}

type ProfileSubjectProbe struct {
	options ProfileProbeOptions
	account *url.URL
	school  *url.URL
	factory clientFactory
}

var (
	profileUIDPattern  = regexp.MustCompile(`(?s)id=["']uid["'][^>]*>\s*([0-9]{3,64})\s*<`)
	profileNamePattern = regexp.MustCompile(`(?s)id=["']messageName["'][^>]*value=["']([^"']{0,128})["']`)
)

func NewProfileSubjectProbe(options ProfileProbeOptions) (*ProfileSubjectProbe, error) {
	if !options.Enabled {
		return &ProfileSubjectProbe{options: options}, nil
	}
	if strings.TrimSpace(options.Namespace) == "" || strings.TrimSpace(options.EvidenceRef) == "" {
		return nil, errors.New("账户资料探针必须配置命名空间与证据引用")
	}
	if options.TTL == 0 {
		options.TTL = 24 * time.Hour
	}
	if options.TTL < time.Hour || options.TTL > 7*24*time.Hour {
		return nil, errors.New("账户资料探针会话时长必须在 1 小时至 7 天之间")
	}
	account, err := profileEndpoint(options.AccountURL)
	if err != nil {
		return nil, errors.New("账户资料探针地址必须是固定 HTTPS 地址")
	}
	var school *url.URL
	if strings.TrimSpace(options.SchoolURL) != "" {
		school, err = profileEndpoint(options.SchoolURL)
		if err != nil {
			return nil, errors.New("学校查询地址必须是固定 HTTPS 地址")
		}
	}
	httpOptions := httpOptions{
		AllowedHosts: options.AllowedHosts, MinRequestInterval: options.MinRequestInterval,
		Timeout: options.Timeout, MaxResponseBytes: options.MaxResponseBytes,
		allowPrivateNetworks: options.allowPrivateNetworks, newClient: options.newClient,
	}
	if err := normalizeOptions(&httpOptions); err != nil {
		return nil, err
	}
	factory := clientFactory{options: httpOptions, limiter: &rateLimiter{}}
	if !factory.allowedURL(account) || (school != nil && !factory.allowedURL(school)) {
		return nil, errors.New("账户资料探针地址不在允许域名或端口范围")
	}
	options.AllowedHosts = httpOptions.AllowedHosts
	options.MinRequestInterval = httpOptions.MinRequestInterval
	options.Timeout = httpOptions.Timeout
	options.MaxResponseBytes = httpOptions.MaxResponseBytes
	return &ProfileSubjectProbe{options: options, account: account, school: school, factory: factory}, nil
}

func profileEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("invalid endpoint")
	}
	return endpoint, nil
}

func (p *ProfileSubjectProbe) Probe(ctx context.Context, session Session) (SubjectEvidence, error) {
	if !p.options.Enabled {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "账户资料探针未启用")
	}
	if p.account == nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "账户资料探针未配置地址")
	}
	jar, err := sessionJar(session)
	if err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法建立 Cookie 容器")
	}
	client := p.factory.client(jar)
	defer client.CloseIdleConnections()
	page, err := p.fetch(ctx, client, p.account, session.UserAgent, "text/html")
	if err != nil {
		return SubjectEvidence{}, err
	}
	uid := profileUIDPattern.FindSubmatch(page)
	if len(uid) != 2 {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "账户资料页缺少稳定主体字段")
	}
	subject := strings.TrimSpace(string(uid[1]))
	displayName := ""
	if match := profileNamePattern.FindSubmatch(page); len(match) == 2 {
		displayName = truncateRunes(strings.TrimSpace(html.UnescapeString(string(match[1]))), 128)
	}
	school := ""
	if p.school != nil {
		if schoolName, err := p.schoolName(ctx, client, jar, session.UserAgent); err == nil {
			school = schoolName
		}
	}
	return SubjectEvidence{
		Namespace: p.options.Namespace, Subject: subject, DisplayName: displayName,
		School: school, ExpiresAt: time.Now().Add(p.options.TTL).UTC(),
	}, nil
}

func (p *ProfileSubjectProbe) schoolName(ctx context.Context, client *http.Client, jar http.CookieJar, userAgent string) (string, error) {
	body, err := p.fetch(ctx, client, p.school, userAgent, "application/json")
	if err != nil {
		return "", err
	}
	var document struct {
		Site []struct {
			FID        any    `json:"fid"`
			SchoolName string `json:"schoolname"`
		} `json:"site"`
	}
	if err := json.Unmarshal(body, &document); err != nil || len(document.Site) == 0 {
		return "", errors.New("学校信息结构无效")
	}
	current := ""
	for _, cookie := range jar.Cookies(p.school) {
		if cookie.Name == "fid" {
			current = cookie.Value
			break
		}
	}
	for _, site := range document.Site {
		if current != "" && strings.TrimSpace(fmt.Sprint(site.FID)) == current {
			return truncateRunes(strings.TrimSpace(html.UnescapeString(site.SchoolName)), 190), nil
		}
	}
	return truncateRunes(strings.TrimSpace(html.UnescapeString(document.Site[0].SchoolName)), 190), nil
}

func (p *ProfileSubjectProbe) fetch(ctx context.Context, client *http.Client, endpoint *url.URL, userAgent, accept string) ([]byte, error) {
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return nil, learning.Fail(learning.FailureTransient, "账户资料查询等待被取消")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, learning.Fail(learning.FailurePermanent, "无法建立账户资料查询")
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", accept)
	base := *endpoint
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 2 {
			return errors.New("账户资料查询重定向次数过多")
		}
		if !sameOrigin(req.URL, &base) {
			return errors.New("账户资料查询禁止跨域重定向")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, learning.Fail(learning.FailureTransient, "账户资料查询未完成")
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		return nil, learning.Fail(learning.FailureIdentityInvalid, "账户资料查询被平台拒绝")
	case response.StatusCode == http.StatusTooManyRequests:
		return nil, learning.Fail(learning.FailureRateLimited, "账户资料查询被限流")
	case response.StatusCode != http.StatusOK:
		return nil, learning.Fail(learning.FailureTransient, "账户资料查询未成功")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, p.options.MaxResponseBytes+1))
	if err != nil || int64(len(payload)) > p.options.MaxResponseBytes {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "账户资料响应超出允许体积")
	}
	return payload, nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

var _ SubjectProbe = (*ProfileSubjectProbe)(nil)
