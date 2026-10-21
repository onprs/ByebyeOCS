// Package modelclient 调用 OpenAI-compatible 非流式接口。
//
// 只使用服务端返回的 usage；缺字段或总量矛盾时标记为不完整，绝不补 0。
package modelclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/services/tiku/internal/normalize"
)

const maxResponseBytes = 2 << 20

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

type TokenUsage struct {
	InputTokens  *int64
	OutputTokens *int64
	TotalTokens  *int64
	Complete     bool
}

type Result struct {
	Content   string
	Reasoning string
	Usage     TokenUsage
	Latency   time.Duration
}

type Call struct {
	BaseURL         string
	APIKey          string
	Model           string
	System          string
	Prompt          string
	Images          []normalize.Image
	MaxOutputTokens int
	Temperature     float64
}

type Client struct {
	http          *http.Client
	allowLoopback bool
}

func New(allowLoopback bool) *Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// 不使用环境代理，避免代理绕过目标 IP 校验。
		DialContext:           guardedDial(dialer, allowLoopback),
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 85 * time.Second,
		MaxIdleConns:          16,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{
		http: &http.Client{
			Transport: transport,
			// 重定向可能绕过目标校验；OpenAI-compatible 端点不需要重定向。
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("上游重定向已拒绝") },
		},
		allowLoopback: allowLoopback,
	}
}

func guardedDial(dialer *net.Dialer, allowLoopback bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("上游地址无效")
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, errors.New("上游地址无法解析")
		}
		for _, item := range ips {
			ip := item.IP
			if ip.IsLoopback() {
				if allowLoopback {
					return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				}
				continue
			}
			if ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		return nil, errors.New("上游地址不可访问")
	}
}

func validateEndpoint(raw string, allowLoopback bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
		return nil, errors.New("上游地址无效")
	}
	if u.Scheme != "https" {
		if !(allowLoopback && u.Scheme == "http" && isLoopback(u.Hostname())) {
			return nil, errors.New("上游地址必须使用 https")
		}
	}
	return u, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL string `json:"url"`
}

type message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Stream      bool      `json:"stream"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature float64   `json:"temperature,omitempty"`
}

type usageWire struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
	TotalTokens      *int64 `json:"total_tokens"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content          json.RawMessage `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *usageWire `json:"usage"`
}

// Chat 执行一次非流式模型调用。调用方负责超时；本函数不自动重试。
func (c *Client) Chat(ctx context.Context, call Call) (Result, error) {
	start := time.Now()
	endpoint, err := validateEndpoint(call.BaseURL, c.allowLoopback)
	if err != nil {
		return Result{}, &Error{Code: "model_unavailable", Message: "模型服务地址不可用"}
	}
	content := any(call.Prompt)
	if len(call.Images) > 0 {
		parts := []contentPart{{Type: "text", Text: call.Prompt}}
		for _, img := range call.Images {
			mime := img.MIME
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, contentPart{
				Type:     "image_url",
				ImageURL: &imageURL{URL: "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)},
			})
		}
		content = parts
	}
	messages := make([]message, 0, 2)
	if call.System != "" {
		messages = append(messages, message{Role: "system", Content: call.System})
	}
	messages = append(messages, message{Role: "user", Content: content})
	payload := chatRequest{
		Model: call.Model, Messages: messages, Stream: false,
		MaxTokens: call.MaxOutputTokens, Temperature: call.Temperature,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Result{}, &Error{Code: "invalid_request", Message: "无法构造模型请求"}
	}
	requestURL := strings.TrimRight(endpoint.String(), "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return Result{}, &Error{Code: "model_unavailable", Message: "无法构造模型请求"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if call.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+call.APIKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Result{}, &Error{Code: "upstream_timeout", Message: "模型调用超时，结果未知"}
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return Result{}, &Error{Code: "request_cancelled", Message: "请求已取消，模型结果未知"}
		}
		return Result{}, &Error{Code: "model_unavailable", Message: "模型服务不可用"}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return Result{}, &Error{Code: "upstream_rate_limited", Message: "模型服务限流，请稍后人工重试"}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return Result{}, &Error{Code: "model_auth_failed", Message: "模型服务认证失败"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Result{}, &Error{Code: "model_unavailable", Message: fmt.Sprintf("模型服务返回状态 %d", resp.StatusCode)}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Result{}, &Error{Code: "model_unavailable", Message: "读取模型响应失败"}
	}
	if len(data) > maxResponseBytes {
		return Result{}, &Error{Code: "model_unavailable", Message: "模型响应过大"}
	}
	var parsed chatResponse
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Result{}, &Error{Code: "model_unavailable", Message: "模型响应格式无效"}
	}
	if len(parsed.Choices) == 0 {
		return Result{}, &Error{Code: "model_unavailable", Message: "模型未返回结果"}
	}
	text, err := decodeContent(parsed.Choices[0].Message.Content)
	if err != nil {
		return Result{}, &Error{Code: "model_unavailable", Message: "模型响应内容无效"}
	}
	return Result{
		Content: text, Reasoning: parsed.Choices[0].Message.ReasoningContent,
		Usage: normalizeUsage(parsed.Usage), Latency: time.Since(start),
	}, nil
}

func decodeContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String(), nil
}

func normalizeUsage(u *usageWire) TokenUsage {
	if u == nil || u.PromptTokens == nil || u.CompletionTokens == nil || u.TotalTokens == nil {
		return TokenUsage{Complete: false}
	}
	input, output, total := *u.PromptTokens, *u.CompletionTokens, *u.TotalTokens
	if input < 0 || output < 0 || total < 0 || output > math.MaxInt64-input || total != input+output {
		return TokenUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, Complete: false}
	}
	return TokenUsage{InputTokens: u.PromptTokens, OutputTokens: u.CompletionTokens, TotalTokens: u.TotalTokens, Complete: true}
}

// Reachability 只检查网络/认证可达性，不产生回答 token。
func (c *Client) Reachability(ctx context.Context, baseURL, apiKey string) error {
	endpoint, err := validateEndpoint(baseURL, c.allowLoopback)
	if err != nil {
		return &Error{Code: "model_unavailable", Message: "模型服务地址不可用"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(endpoint.String(), "/")+"/models", nil)
	if err != nil {
		return &Error{Code: "model_unavailable", Message: "无法构造可达性请求"}
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &Error{Code: "upstream_timeout", Message: "可达性检查超时"}
		}
		return &Error{Code: "model_unavailable", Message: "模型服务不可达"}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &Error{Code: "model_auth_failed", Message: "模型服务认证失败"}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &Error{Code: "model_unavailable", Message: "模型服务未通过可达性检查"}
	}
	return nil
}
