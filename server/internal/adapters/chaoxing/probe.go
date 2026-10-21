package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// JSONProbeOptions 配置可信会话查询探针。所有字段由维护配置提供，
// 不接受终端用户提交的地址、方法或 JSON 路径。
//
// 默认关闭。启用前必须核实个人资料 JSON 端点、稳定主体字段与命名空间证据；
// 探针只读取服务端会话响应的配置路径，不读取 Cookie 自述 UID，也不从用户名派生主体。
type JSONProbeOptions struct {
	Enabled       bool
	Endpoint      string
	Method        string
	Namespace     string
	EvidenceRef   string
	SubjectPath   string
	ExpiresAtPath string
	// ExpiresAtFormat 支持 rfc3339、unix、unix_ms。
	ExpiresAtFormat string
	DisplayNamePath string
	SchoolPath      string

	AllowedHosts       []string
	MinRequestInterval time.Duration
	Timeout            time.Duration
	MaxResponseBytes   int64

	// 仅隔离测试使用，不导出给生产接线。
	allowPrivateNetworks bool
	newClient            func(jar http.CookieJar) *http.Client
}

type JSONSubjectProbe struct {
	options  JSONProbeOptions
	endpoint *url.URL
	factory  clientFactory
}

func NewJSONSubjectProbe(options JSONProbeOptions) (*JSONSubjectProbe, error) {
	if !options.Enabled {
		return &JSONSubjectProbe{options: options}, nil
	}
	if strings.TrimSpace(options.Namespace) == "" || strings.TrimSpace(options.EvidenceRef) == "" {
		return nil, errors.New("可信会话探针必须配置命名空间与证据引用")
	}
	if strings.TrimSpace(options.SubjectPath) == "" || strings.TrimSpace(options.ExpiresAtPath) == "" {
		return nil, errors.New("可信会话探针必须配置主体与到期 JSON 路径")
	}
	if options.Method == "" {
		options.Method = http.MethodGet
	}
	if options.Method != http.MethodGet && options.Method != http.MethodPost {
		return nil, errors.New("可信会话探针仅支持 GET 或 POST")
	}
	switch options.ExpiresAtFormat {
	case "":
		options.ExpiresAtFormat = "rfc3339"
	case "rfc3339", "unix", "unix_ms":
	default:
		return nil, errors.New("可信会话探针到期时间格式无效")
	}
	endpoint, err := url.Parse(options.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("可信会话探针必须是固定 HTTPS 地址")
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
	if !factory.allowedURL(endpoint) {
		return nil, errors.New("可信会话探针地址不在允许域名或端口范围")
	}
	options.AllowedHosts = httpOptions.AllowedHosts
	options.MinRequestInterval = httpOptions.MinRequestInterval
	options.Timeout = httpOptions.Timeout
	options.MaxResponseBytes = httpOptions.MaxResponseBytes
	return &JSONSubjectProbe{options: options, endpoint: endpoint, factory: factory}, nil
}

func (p *JSONSubjectProbe) Probe(ctx context.Context, session Session) (SubjectEvidence, error) {
	if !p.options.Enabled {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话探针未启用")
	}
	if p.endpoint == nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话探针未配置地址")
	}
	jar, err := sessionJar(session)
	if err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法建立 Cookie 容器")
	}
	client := p.factory.client(jar)
	base := *p.endpoint
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 2 {
			return errors.New("可信会话探针重定向次数过多")
		}
		if !sameOrigin(req.URL, &base) {
			return errors.New("可信会话探针禁止跨域重定向")
		}
		return nil
	}
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureTransient, "可信会话查询等待被取消")
	}
	request, err := http.NewRequestWithContext(ctx, p.options.Method, p.endpoint.String(), nil)
	if err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailurePermanent, "无法建立可信会话查询")
	}
	request.Header.Set("User-Agent", session.UserAgent)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureTransient, "可信会话查询未完成")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return SubjectEvidence{}, learning.Fail(learning.FailureIdentityInvalid, "可信会话查询被平台拒绝")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return SubjectEvidence{}, learning.Fail(learning.FailureRateLimited, "可信会话查询被限流")
	}
	if response.StatusCode != http.StatusOK {
		return SubjectEvidence{}, learning.Fail(learning.FailureTransient, "可信会话查询未成功")
	}
	contentType := strings.ToLower(response.Header.Get("Content-Type"))
	if !strings.Contains(contentType, "application/json") {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话端点未返回 JSON")
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, p.options.MaxResponseBytes+1))
	if err != nil || int64(len(payload)) > p.options.MaxResponseBytes {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话响应超出允许体积")
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话响应不是有效 JSON")
	}
	subjectValue, ok := lookupJSONPath(document, p.options.SubjectPath)
	if !ok {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话响应缺少稳定主体字段")
	}
	subject := strings.TrimSpace(jsonText(subjectValue))
	if subject == "" || len(subject) > 256 {
		return SubjectEvidence{}, learning.Fail(learning.FailureIdentityInvalid, "可信会话返回的稳定主体无效")
	}
	expiresValue, ok := lookupJSONPath(document, p.options.ExpiresAtPath)
	if !ok {
		return SubjectEvidence{}, learning.Fail(learning.FailureCapabilityMissing, "可信会话响应缺少到期时间")
	}
	expiresAt, err := probeExpiry(expiresValue, p.options.ExpiresAtFormat)
	if err != nil || !expiresAt.After(time.Now()) {
		return SubjectEvidence{}, learning.Fail(learning.FailureIdentityInvalid, "可信会话到期时间无效")
	}
	displayName := probeOptionalString(document, p.options.DisplayNamePath, 128)
	school := probeOptionalString(document, p.options.SchoolPath, 190)
	return SubjectEvidence{
		Namespace: p.options.Namespace, Subject: subject, DisplayName: displayName,
		School: school, ExpiresAt: expiresAt.UTC(),
	}, nil
}

func sameOrigin(left, right *url.URL) bool {
	return left != nil && right != nil && strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Host, right.Host)
}

func lookupJSONPath(document any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, false
	}
	current := document
	index := 0
	for index < len(path) {
		switch path[index] {
		case '.':
			if index == 0 || index+1 >= len(path) || path[index+1] == '.' || path[index+1] == '[' {
				return nil, false
			}
			index++
			continue
		case '[':
			end := strings.IndexByte(path[index:], ']')
			if end < 0 {
				return nil, false
			}
			number, err := strconv.Atoi(path[index+1 : index+end])
			if err != nil || number < 0 {
				return nil, false
			}
			array, ok := current.([]any)
			if !ok || number >= len(array) {
				return nil, false
			}
			current = array[number]
			index += end + 1
		default:
			end := index
			for end < len(path) && path[end] != '.' && path[end] != '[' {
				end++
			}
			key := path[index:end]
			if key == "" {
				return nil, false
			}
			object, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			current, ok = object[key]
			if !ok {
				return nil, false
			}
			index = end
		}
	}
	return current, true
}

func jsonText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	}
	return ""
}

func probeOptionalString(document any, path string, limit int) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	value, ok := lookupJSONPath(document, path)
	if !ok {
		return ""
	}
	text := strings.TrimSpace(jsonText(value))
	if len(text) > limit {
		text = text[:limit]
	}
	return text
}

func probeExpiry(value any, format string) (time.Time, error) {
	switch format {
	case "unix", "unix_ms":
		number, ok := value.(json.Number)
		if !ok {
			return time.Time{}, errors.New("到期时间不是数字")
		}
		seconds, err := number.Int64()
		if err != nil {
			return time.Time{}, err
		}
		if format == "unix_ms" {
			return time.UnixMilli(seconds), nil
		}
		return time.Unix(seconds, 0), nil
	default:
		text, ok := value.(string)
		if !ok {
			return time.Time{}, errors.New("到期时间不是字符串")
		}
		return time.Parse(time.RFC3339, strings.TrimSpace(text))
	}
}

var _ SubjectProbe = (*JSONSubjectProbe)(nil)
