package questions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

const maxForkResponseBytes = 2 << 20

type ForkQuestion struct {
	Title   string   `json:"title"`
	Options string   `json:"options"`
	Type    string   `json:"type"`
	Images  []string `json:"images"`
}

type ForkBudget struct {
	MaxOutputTokens int `json:"max_output_tokens"`
}

type ForkAnswerRequest struct {
	RequestID       string       `json:"request_id"`
	ModelOfferingID string       `json:"model_offering_id"`
	Mode            string       `json:"mode"`
	CacheScope      string       `json:"cache_scope"`
	CacheScopeKey   string       `json:"cache_scope_key"`
	Question        ForkQuestion `json:"question"`
	Budget          ForkBudget   `json:"budget"`
}

type ForkModel struct {
	OfferingID   string   `json:"offering_id"`
	DisplayName  string   `json:"display_name"`
	Capabilities []string `json:"capabilities"`
}

type ForkCache struct {
	Hit       bool       `json:"hit"`
	EntryID   *string    `json:"entry_id"`
	CreatedAt *time.Time `json:"created_at"`
}

type Call struct {
	Slot         string `json:"slot"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
	Status       string `json:"status"`
}

type Usage struct {
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
	Complete     bool   `json:"complete"`
	Calls        []Call `json:"calls"`
}

type ForkAnswerResponse struct {
	RequestID string    `json:"request_id"`
	Status    string    `json:"status"`
	Question  string    `json:"question"`
	Answer    string    `json:"answer"`
	Model     ForkModel `json:"model"`
	Mode      string    `json:"mode"`
	Cache     ForkCache `json:"cache"`
	Usage     Usage     `json:"usage"`
	LatencyMS int64     `json:"latency_ms"`
}

type ForkTestResult struct {
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Reachable  bool   `json:"reachable"`
	Functional bool   `json:"functional"`
	LatencyMS  int64  `json:"latency_ms"`
	ErrorCode  string `json:"error_code"`
}

type ForkError struct {
	Code      string
	Message   string
	Status    int
	Retryable bool
}

func (e *ForkError) Error() string { return e.Message }

// safeForkCode 只允许有界的机器码，避免把上游自由文本或超长内容写入查询事实。
func safeForkCode(code string) string {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return "service_unavailable"
	}
	for _, c := range code {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' {
			continue
		}
		return "service_unavailable"
	}
	return code
}

type ForkClient interface {
	Answer(ctx context.Context, request ForkAnswerRequest, idempotencyKey string) (ForkAnswerResponse, error)
	TestModel(ctx context.Context, providerRef, kind string, confirmCost bool) (ForkTestResult, error)
	// InvalidateCache 返回 fork 是否真的标记了条目失效；已失效或已过期条目返回 false。
	InvalidateCache(ctx context.Context, entryID, reason string) (bool, error)
}

type httpFork struct {
	base  string
	token string
	http  *http.Client
}

// NewHTTPFork 构造受超时和重定向保护的 fork 客户端；不自动重试付费 POST。
func NewHTTPFork(baseURL, token string, timeout time.Duration) (ForkClient, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fault.New("not_configured", "题库服务地址无效")
	}
	if strings.TrimSpace(token) == "" {
		return nil, fault.New("not_configured", "题库服务令牌未配置")
	}
	if timeout <= 0 {
		timeout = 95 * time.Second
	}
	return &httpFork{
		base: strings.TrimRight(parsed.String(), "/"), token: token,
		http: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("题库服务重定向已拒绝")
		}},
	}, nil
}

func (c *httpFork) doJSON(ctx context.Context, method, path, idempotencyKey string, body, target any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fault.New("internal_error", "无法构造题库请求")
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return fault.New("service_unavailable", "无法构造题库请求")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &ForkError{Code: "upstream_timeout", Message: "题库服务超时，结果未知", Status: 504, Retryable: true}
		}
		// 调用方取消表示上游可能已受理请求，不能当作可重试的确定失败。
		if errors.Is(ctx.Err(), context.Canceled) {
			return &ForkError{Code: "request_cancelled", Message: "题库请求已取消，结果未知", Status: 504, Retryable: false}
		}
		return &ForkError{Code: "service_unavailable", Message: "题库服务不可用", Status: 503, Retryable: true}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxForkResponseBytes+1))
	if readErr != nil || len(data) > maxForkResponseBytes {
		return &ForkError{Code: "service_unavailable", Message: "题库服务响应无效", Status: 502, Retryable: true}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var envelope struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				Retryable bool   `json:"retryable"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &envelope)
		code := safeForkCode(envelope.Error.Code)
		// 不保存或转发 fork 的自由文本；错误分类只依赖安全的机器码。
		return &ForkError{Code: code, Message: "题库服务返回错误", Status: response.StatusCode, Retryable: envelope.Error.Retryable}
	}
	if target == nil {
		return nil
	}
	if err := json.Unmarshal(data, target); err != nil {
		return &ForkError{Code: "service_unavailable", Message: "题库服务响应格式无效", Status: 502, Retryable: true}
	}
	return nil
}

func (c *httpFork) Answer(ctx context.Context, request ForkAnswerRequest, idempotencyKey string) (ForkAnswerResponse, error) {
	var response ForkAnswerResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/v2/answers", idempotencyKey, request, &response)
	return response, err
}

func (c *httpFork) TestModel(ctx context.Context, providerRef, kind string, confirmCost bool) (ForkTestResult, error) {
	var response ForkTestResult
	body := map[string]any{"kind": kind, "confirm_cost": confirmCost}
	err := c.doJSON(ctx, http.MethodPost, "/internal/v1/models/"+url.PathEscape(providerRef)+"/tests", "", body, &response)
	return response, err
}

type forkInvalidationResult struct {
	Invalidated bool   `json:"invalidated"`
	EntryID     string `json:"entry_id"`
}

func (c *httpFork) InvalidateCache(ctx context.Context, entryID, reason string) (bool, error) {
	var response forkInvalidationResult
	body := map[string]any{"entry_id": entryID, "reason": reason, "actor": "main-platform"}
	if err := c.doJSON(ctx, http.MethodPost, "/internal/v1/cache/invalidations", "", body, &response); err != nil {
		return false, err
	}
	if response.Invalidated && response.EntryID != entryID {
		// fork 返回的条目与请求不一致时不能记为已失效。
		return false, &ForkError{Code: "service_unavailable", Message: "题库缓存失效结果不匹配", Status: 502, Retryable: false}
	}
	return response.Invalidated, nil
}

// normalizeForkResponse 校验 fork 回归契约；总量矛盾时降级为待复核，不编造 token。
// maxCalls 是本次请求可能产生的调用上界：图片逐张转述加一次回答。
func normalizeForkResponse(response *ForkAnswerResponse, requestID, offeringID, mode string, maxCalls int) error {
	if response.RequestID != requestID {
		return fault.New("service_unavailable", "题库响应与请求不匹配")
	}
	if response.Mode != mode {
		return fault.New("service_unavailable", "题库响应用途不匹配")
	}
	if response.Model.OfferingID != offeringID {
		return fault.New("model_disabled", "题库服务返回的模型与请求不一致")
	}
	switch response.Status {
	case "answered":
		if strings.TrimSpace(response.Answer) == "" {
			return fault.New("service_unavailable", "题库服务未返回答案")
		}
		if response.Cache.Hit {
			return fault.New("service_unavailable", "题库响应缓存标记矛盾")
		}
		if len(response.Usage.Calls) == 0 {
			response.Status = "usage_incomplete"
			response.Usage.Complete = false
		}
	case "cache_hit":
		if !response.Cache.Hit || response.Cache.EntryID == nil || response.Cache.CreatedAt == nil {
			return fault.New("service_unavailable", "题库缓存来源事实不完整")
		}
		if response.Usage.TotalTokens == nil || *response.Usage.TotalTokens != 0 || len(response.Usage.Calls) != 0 {
			response.Status = "usage_incomplete"
			response.Usage.Complete = false
		}
	case "usage_incomplete":
		response.Usage.Complete = false
	default:
		return fault.New("service_unavailable", "题库响应状态未知")
	}
	if !validUsage(response.Usage, response.Status == "cache_hit", maxCalls) {
		if response.Status == "cache_hit" {
			return fault.New("service_unavailable", "题库缓存用量事实无效")
		}
		response.Status = "usage_incomplete"
		response.Usage.Complete = false
	}
	return nil
}

func validUsage(usage Usage, cacheHit bool, maxCalls int) bool {
	if maxCalls < 1 {
		maxCalls = 1
	}
	if len(usage.Calls) > maxCalls {
		return false
	}
	if cacheHit {
		return usage.Complete && usage.InputTokens != nil && usage.OutputTokens != nil && usage.TotalTokens != nil &&
			*usage.InputTokens == 0 && *usage.OutputTokens == 0 && *usage.TotalTokens == 0 && len(usage.Calls) == 0
	}
	if !usage.Complete {
		return true
	}
	if usage.InputTokens == nil || usage.OutputTokens == nil || usage.TotalTokens == nil {
		return false
	}
	input, output, total := *usage.InputTokens, *usage.OutputTokens, *usage.TotalTokens
	if input < 0 || output < 0 || total < 0 || output > math.MaxInt64-input || total != input+output {
		return false
	}
	var callInput, callOutput, callTotal int64
	for _, call := range usage.Calls {
		if call.InputTokens == nil || call.OutputTokens == nil || call.TotalTokens == nil || call.Status != "ok" {
			return false
		}
		if *call.InputTokens < 0 || *call.OutputTokens < 0 || *call.OutputTokens > math.MaxInt64-*call.InputTokens ||
			*call.TotalTokens != *call.InputTokens+*call.OutputTokens {
			return false
		}
		if *call.InputTokens > math.MaxInt64-callInput || *call.OutputTokens > math.MaxInt64-callOutput ||
			*call.TotalTokens > math.MaxInt64-callTotal {
			return false
		}
		callInput += *call.InputTokens
		callOutput += *call.OutputTokens
		callTotal += *call.TotalTokens
	}
	return callInput == input && callOutput == output && callTotal == total
}
