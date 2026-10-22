package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// HTTPExecutorOptions 控制原生请求执行；真实平台连接与执行授权分别确认。
// 不提供倍速、跳过交互、覆盖时长或任意上报地址的生产配置。
type HTTPExecutorOptions struct {
	httpOptions
	Enabled      bool
	Authorized   bool
	Concurrency  int
	MaxDuration  time.Duration
	ConfirmGrace time.Duration
	Endpoints    Endpoints
	// 时钟和等待函数只供包内协议测试使用，不能从配置或请求注入。
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

// HTTPExecutor 通过平台下发的任务上下文和普通请求节奏执行视频任务。
// 本执行器不下载或解码媒体，所有观察均为请求执行记录，Media 始终不可用。
type HTTPExecutor struct {
	options  HTTPExecutorOptions
	platform *Platform
	slots    chan struct{}
}

func NewHTTPExecutor(options HTTPExecutorOptions) (*HTTPExecutor, error) {
	if options.Concurrency == 0 {
		options.Concurrency = 4
	}
	if options.MaxDuration == 0 {
		options.MaxDuration = 6 * time.Hour
	}
	if options.ConfirmGrace == 0 {
		options.ConfirmGrace = 10 * time.Minute
	}
	if options.Concurrency < 1 || options.Concurrency > 64 || options.MaxDuration < time.Minute || options.MaxDuration > 24*time.Hour || options.ConfirmGrace < time.Second || options.ConfirmGrace > 30*time.Minute {
		return nil, errors.New("原生请求执行预算无效")
	}
	if options.now == nil {
		options.now = time.Now
	}
	if options.wait == nil {
		options.wait = waitHTTPExecution
	}
	platform, err := NewPlatform(Options{httpOptions: options.httpOptions, ReadOnlyEnabled: true, Endpoints: options.Endpoints})
	if err != nil {
		return nil, err
	}
	return &HTTPExecutor{options: options, platform: platform, slots: make(chan struct{}, options.Concurrency)}, nil
}

func (e *HTTPExecutor) Capabilities() learning.ExecutorCapabilities {
	result := learning.ExecutorCapabilities{Name: "chaoxing-http-video", TaskKinds: []string{"video"}, SafeLeaseRecovery: true}
	switch {
	case !e.options.Enabled:
		result.DisabledReason = "http_executor_disabled"
	case !e.options.Authorized:
		result.DisabledReason = "http_executor_not_authorized"
	default:
		result.Enabled = true
	}
	return result
}

func (e *HTTPExecutor) Execute(ctx context.Context, request learning.ExecutionRequest) (learning.ExecutionResult, error) {
	result := learning.ExecutionResult{}
	if !e.Capabilities().Enabled {
		return result, learning.Fail(learning.FailureCapabilityMissing, "原生请求执行未启用或未授权")
	}
	if request.StopRequested || ctx.Err() != nil {
		result.StopAcknowledged = true
		return result, learning.Fail(learning.FailureStopped, "原生请求执行已停止")
	}
	for _, value := range []string{request.Task.Course.ExternalCourseID, request.Task.Course.ExternalClassID, request.Task.Course.CPI, request.Task.ChapterExternalID, request.Task.ObjectID, request.Task.TaskPointID} {
		if !protocolID(value) {
			return result, learning.Fail(learning.FailureInvalidTask, "任务缺少有效的课程、章节或任务点标识")
		}
	}
	session, err := decodeSession(request.Session)
	if err != nil {
		return result, learning.Fail(learning.FailureIdentityInvalid, "平台会话无效，需要重新验证")
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	default:
		return result, &learning.Failure{Class: learning.FailureRateLimited, RetryAfter: 30 * time.Second, Detail: "原生请求执行已达到并发上限"}
	}
	jar, err := sessionJar(session)
	if err != nil {
		return result, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法使用")
	}
	client := e.platform.factory.client(jar)
	defer client.CloseIdleConnections()
	run := &httpExecution{executor: e, session: session, client: client, request: request}
	result, err = run.execute(ctx)
	if ctx.Err() != nil {
		result.Completed = false
		result.StopAcknowledged = true
		return result, learning.Fail(learning.FailureStopped, "原生请求执行已按停止请求中断")
	}
	return result, err
}

func waitHTTPExecution(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func protocolID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

type httpExecution struct {
	executor  *HTTPExecutor
	session   Session
	client    *http.Client
	request   learning.ExecutionRequest
	cardsURL  string
	player    string
	defaults  map[string]any
	task      map[string]any
	metadata  map[string]any
	reportURL string
	other     string
	rt        string
	engine    *bool
	duration  int
	position  int
	interval  time.Duration
}

var executionVerificationPage = regexp.MustCompile(`(?is)(<input\b[^>]*(?:name|id)\s*=\s*["'](?:captcha|verifycode)|<title>[^<]*(?:验证码|验证|captcha)|请输入验证码|验证码错误|captcha:)`)

func (r *httpExecution) get(ctx context.Context, address, role string) ([]byte, error) {
	factory := &r.executor.platform.factory
	target, err := url.Parse(address)
	if err != nil || !factory.allowedURL(target) || target.Fragment != "" {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "原生请求地址未通过平台范围校验")
	}
	if err := factory.limiter.wait(ctx, factory.options.MinRequestInterval); err != nil {
		return nil, learning.Fail(learning.FailureStopped, "原生请求等待已停止")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, learning.Fail(learning.FailureInvalidTask, "无法构造原生请求")
	}
	request.Header.Set("User-Agent", r.session.UserAgent)
	request.Header.Set("Accept", "application/json,text/html;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	referer := r.player
	if referer == "" {
		referer = r.cardsURL
	}
	request.Header.Set("Referer", referer)
	response, err := r.client.Do(request)
	if err != nil {
		return nil, learning.Fail(learning.FailureTransient, "原生请求未完成")
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return nil, learning.Fail(learning.FailureIdentityInvalid, "平台身份已失效")
	case http.StatusForbidden:
		return nil, learning.Fail(learning.FailureForbidden, "平台拒绝原生请求，已停止执行")
	case http.StatusTooManyRequests:
		delay := time.Minute
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds > 0 && seconds <= 86400 {
			delay = time.Duration(seconds) * time.Second
		}
		return nil, &learning.Failure{Class: learning.FailureRateLimited, RetryAfter: delay, Detail: "平台限流，已停止执行"}
	case http.StatusOK:
	default:
		return nil, learning.Fail(learning.FailureTransient, "平台原生请求未成功")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, factory.options.MaxResponseBytes+1))
	if err != nil || int64(len(data)) > factory.options.MaxResponseBytes {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "平台原生响应不可用或超出体积限制")
	}
	lower := strings.ToLower(string(data))
	if executionVerificationPage.Match(data) {
		return nil, learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止执行")
	}
	var challenge map[string]any
	if json.Unmarshal(data, &challenge) == nil {
		for _, key := range []string{"captcha", "needCaptcha", "needVerify", "verification_required"} {
			if required, ok := challenge[key].(bool); ok && required {
				return nil, learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止执行")
			}
		}
		for _, key := range []string{"error", "message", "msg"} {
			message := strings.ToLower(text(challenge[key]))
			if strings.Contains(message, "captcha") || strings.Contains(message, "验证码") {
				return nil, learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止执行")
			}
		}
	}
	if strings.Contains(lower, "fanyalogin") || strings.Contains(lower, "passport2.chaoxing.com/login") {
		return nil, learning.Fail(learning.FailureIdentityInvalid, "平台要求重新登录")
	}
	return data, nil
}

func (r *httpExecution) observation(position int) learning.ExecutionResult {
	duration := float64(r.duration)
	value := float64(position)
	fraction := math.Min(1, value/duration)
	return learning.ExecutionResult{Observation: learning.Observation{Fraction: &fraction, PositionSeconds: &value, DurationSeconds: &duration, Source: "chaoxing-http", ObservedAt: r.executor.options.now().UTC(), Note: "原生请求执行记录"}}
}

func (r *httpExecution) execute(ctx context.Context) (learning.ExecutionResult, error) {
	if err := r.prepare(ctx); err != nil {
		return learning.ExecutionResult{}, err
	}
	result := r.observation(r.position)
	passed, err := r.report(ctx, r.position, 3)
	if err != nil {
		return result, err
	}
	if passed {
		result.Completed = true
		return result, nil
	}
	started := r.executor.options.now()
	deadline := started.Add(time.Duration(r.duration-r.position)*time.Second + r.executor.options.ConfirmGrace)
	for {
		remaining := deadline.Sub(r.executor.options.now())
		if remaining <= 0 {
			return result, learning.Fail(learning.FailureCapabilityMissing, "原生请求已达到执行预算，平台未确认完成")
		}
		if remaining < r.interval {
			if err := r.executor.options.wait(ctx, remaining); err != nil {
				return result, err
			}
			return result, learning.Fail(learning.FailureCapabilityMissing, "原生请求已达到执行预算，平台未确认完成")
		}
		if err := r.executor.options.wait(ctx, r.interval); err != nil {
			return result, err
		}
		// 使用服务端保存位置和实际经过时间，保持正常节奏；不提供位置或时钟覆盖开关。
		elapsed := r.executor.options.now().Sub(started)
		position := min(r.duration, r.position+int(max(elapsed, 0)/time.Second))
		passed, err := r.report(ctx, position, 0)
		if err != nil {
			return result, err
		}
		result = r.observation(position)
		if passed {
			if position == r.duration {
				if _, err := r.report(ctx, position, 4); err != nil {
					return result, err
				}
			}
			// Completed 只是执行阶段结束，调度器仍须独立核验平台状态后才能计量。
			result.Completed = true
			return result, nil
		}
	}
}
