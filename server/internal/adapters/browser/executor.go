package browser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// Options 是可选浏览器执行器的维护配置。默认关闭且未授权。
type Options struct {
	Enabled    bool
	Authorized bool
	ChromePath string
	// Headful 为 true 时使用维护者桌面；服务端默认无头新实现。
	Headful   bool
	NoSandbox bool

	StartupTimeout      time.Duration
	ExecutionTimeout    time.Duration
	PauseGrace          time.Duration
	NoProgressTimeout   time.Duration
	DevToolsTimeout     time.Duration
	MaxMessageBytes     int
	MemoryLimitMB       int
	MaxProxyConnections int
	ProfileRoot         string

	// 仅隔离测试使用，不导出到生产配置。
	testTargetURL      string
	allowLocalTestURLs bool
	disableProxy       bool
	onPlaybackStarted  func()
}

type Executor struct {
	options Options
	slot    chan struct{}
}

func New(options Options) (*Executor, error) {
	if options.StartupTimeout <= 0 {
		options.StartupTimeout = 30 * time.Second
	}
	if options.ExecutionTimeout <= 0 {
		options.ExecutionTimeout = 45 * time.Minute
	}
	if options.PauseGrace <= 0 {
		options.PauseGrace = 8 * time.Second
	}
	if options.NoProgressTimeout <= 0 {
		options.NoProgressTimeout = 20 * time.Second
	}
	if options.DevToolsTimeout <= 0 {
		options.DevToolsTimeout = 10 * time.Second
	}
	if options.MaxMessageBytes <= 0 {
		options.MaxMessageBytes = 8 << 20
	}
	if options.MemoryLimitMB <= 0 {
		options.MemoryLimitMB = 512
	}
	if options.MaxProxyConnections <= 0 {
		options.MaxProxyConnections = 16
	}
	if options.StartupTimeout > 2*time.Minute || options.ExecutionTimeout > 4*time.Hour ||
		options.PauseGrace > 2*time.Minute || options.NoProgressTimeout > 10*time.Minute {
		return nil, errors.New("浏览器执行器超时预算超出范围")
	}
	if options.MemoryLimitMB < 128 || options.MemoryLimitMB > 4096 {
		return nil, errors.New("浏览器内存预算超出范围")
	}
	if options.MaxMessageBytes < 1<<20 || options.MaxMessageBytes > 64<<20 {
		return nil, errors.New("浏览器消息预算超出范围")
	}
	if options.MaxProxyConnections < 1 || options.MaxProxyConnections > 64 {
		return nil, errors.New("浏览器出站连接预算超出范围")
	}
	if options.Enabled && options.Authorized && options.ChromePath == "" {
		return nil, errors.New("启用浏览器执行器必须显式配置 Chrome 路径")
	}
	return &Executor{options: options, slot: make(chan struct{}, 1)}, nil
}

func (e *Executor) Capabilities() learning.ExecutorCapabilities {
	capabilities := learning.ExecutorCapabilities{
		Name: "chrome-html5-video", Enabled: false, TaskKinds: []string{"video"},
		ProducesMediaEvidence: true, SafeLeaseRecovery: false,
	}
	switch {
	case !e.options.Enabled:
		capabilities.DisabledReason = "browser_executor_disabled"
	case !e.options.Authorized:
		capabilities.DisabledReason = "browser_executor_not_authorized"
	case e.options.ChromePath == "":
		capabilities.DisabledReason = "browser_path_not_configured"
	default:
		capabilities.Enabled = true
	}
	return capabilities
}

func (e *Executor) Execute(ctx context.Context, request learning.ExecutionRequest) (learning.ExecutionResult, error) {
	capabilities := e.Capabilities()
	if !capabilities.Enabled {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing,
			"浏览器视频执行器未启用或未授权")
	}
	if request.Task.TaskPointID == "" || request.Task.ObjectID == "" {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "任务缺少可核对的视频标识")
	}
	if request.MediaDurationSeconds <= 0 {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing,
			"缺少平台任务时长，无法与页面视频唯一核对")
	}
	select {
	case e.slot <- struct{}{}:
		defer func() { <-e.slot }()
	default:
		return learning.ExecutionResult{}, (&learning.Failure{
			Class: learning.FailureRateLimited, RetryAfter: time.Minute,
			Detail: "浏览器执行器正在处理其他任务",
		})
	}
	target, err := e.targetURL(request)
	if err != nil {
		return learning.ExecutionResult{}, err
	}
	session, err := chaoxing.DecodeSession(request.Session)
	if err != nil {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureIdentityInvalid,
			"平台会话材料无效，需要重新验证")
	}
	var proxy *connectProxy
	if !e.options.disableProxy {
		proxy, err = startConnectProxy(nil, e.options.MaxProxyConnections)
		if err != nil {
			return learning.ExecutionResult{}, learning.Fail(learning.FailureTransient, "浏览器出站代理启动失败")
		}
		defer proxy.Close()
	}
	proxyAddr := ""
	if proxy != nil {
		proxyAddr = proxy.Addr()
	}
	process, err := launchChrome(ctx, e.options, proxyAddr)
	if err != nil {
		if ctx.Err() != nil {
			return learning.ExecutionResult{StopAcknowledged: true}, nil
		}
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing,
			"Chrome 无法启动，请检查浏览器路径与运行预算")
	}
	page, err := process.openPage(ctx)
	if err == nil {
		page.callTimeout = e.options.DevToolsTimeout
	}
	if err != nil {
		_ = process.close()
		if ctx.Err() != nil {
			return learning.ExecutionResult{StopAcknowledged: true}, nil
		}
		return learning.ExecutionResult{}, learning.Fail(learning.FailureTransient, "浏览器页面无法建立")
	}
	deadline := time.Now().Add(e.options.ExecutionTimeout)
	result, runErr := e.runPage(ctx, page, request, target, session, deadline)
	closeErr := process.close()
	if closeErr != nil {
		return result, learning.Fail(learning.FailurePermanent, "浏览器进程未能安全关闭")
	}
	if ctx.Err() != nil && !result.StopAcknowledged {
		return result, learning.Fail(learning.FailurePermanent, "停止时无法确认浏览器暂停")
	}
	return result, runErr
}

func (e *Executor) runPage(ctx context.Context, page *chromePage, request learning.ExecutionRequest,
	target string, session chaoxing.Session, deadline time.Time) (learning.ExecutionResult, error) {
	var result learning.ExecutionResult
	var candidate *videoState
	stop := func() (learning.ExecutionResult, error) {
		// 取消/租约失效时先真正暂停，再由调用方关闭浏览器。
		if candidate != nil {
			pauseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			paused, err := page.pause(pauseCtx, candidate.ContextID)
			cancel()
			if err != nil || !paused {
				return result, learning.Fail(learning.FailurePermanent, "无法确认页面播放器已暂停")
			}
		}
		result.StopAcknowledged = true
		return result, nil
	}
	startupDeadline := time.Now().Add(e.options.StartupTimeout)
	if deadline.Before(startupDeadline) {
		startupDeadline = deadline
	}
	if err := page.setSession(ctx, session); err != nil {
		if ctx.Err() != nil {
			return stop()
		}
		return result, learning.Fail(learning.FailureIdentityInvalid, "无法建立平台会话")
	}
	navigateTimeout := time.Until(startupDeadline)
	if navigateTimeout <= 0 {
		return result, learning.Fail(learning.FailureCapabilityMissing, "浏览器启动预算已耗尽")
	}
	if err := page.navigate(ctx, target, navigateTimeout); err != nil {
		if ctx.Err() != nil {
			return stop()
		}
		return result, learning.Fail(learning.FailureCapabilityMissing, "课程页面加载失败或页面结构未确认")
	}
	var waitErr error
	candidate, waitErr = e.waitVideo(ctx, page, request, startupDeadline)
	if waitErr != nil {
		if ctx.Err() != nil {
			return stop()
		}
		return result, waitErr
	}
	playStatus, err := page.play(ctx, candidate.ContextID)
	if err != nil {
		if ctx.Err() != nil {
			return stop()
		}
		return result, learning.Fail(learning.FailureCapabilityMissing, "页面播放器未能启动")
	}
	if playStatus != "ok" {
		return result, learning.Fail(learning.FailureCapabilityMissing, "浏览器拒绝正常播放或页面结构变化")
	}
	if e.options.onPlaybackStarted != nil {
		e.options.onPlaybackStarted()
	}
	return e.monitorPlayback(ctx, page, candidate, request, deadline, &result)
}

func (e *Executor) waitVideo(ctx context.Context, page *chromePage, request learning.ExecutionRequest, deadline time.Time) (*videoState, error) {
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, learning.Fail(learning.FailureCapabilityMissing, "页面在预算内未出现唯一可核对视频")
		}
		states, err := page.videos(ctx)
		if err == nil {
			if len(states) > 1 {
				return nil, learning.Fail(learning.FailureCapabilityMissing, "页面存在多个视频，无法唯一对应任务点")
			}
			if len(states) == 1 && states[0].ReadyState >= 1 {
				state := states[0]
				if err := validateVideoState(state, request); err != nil {
					return nil, err
				}
				return &state, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func validateVideoState(state videoState, request learning.ExecutionRequest) error {
	if state.Error != 0 {
		return learning.Fail(learning.FailurePermanent, "页面视频播放器报告媒体错误")
	}
	if state.Src == "" || !strings.Contains(state.Src, request.Task.ObjectID) {
		return learning.Fail(learning.FailureCapabilityMissing, "无法将页面视频与任务点对象唯一核对")
	}
	if !withinDurationTolerance(state.Duration, request.MediaDurationSeconds) {
		return learning.Fail(learning.FailureCapabilityMissing, "页面视频时长与任务快照不一致")
	}
	return nil
}

func (e *Executor) monitorPlayback(ctx context.Context, page *chromePage, initial *videoState,
	request learning.ExecutionRequest, deadline time.Time, result *learning.ExecutionResult) (learning.ExecutionResult, error) {
	lastPosition := initial.CurrentTime
	lastWall := time.Now()
	lastProgress := time.Now()
	var pausedSince time.Time
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return e.stopPlayback(page, initial.ContextID, result)
		case <-ticker.C:
		}
		if time.Now().After(deadline) {
			_ = e.pauseAndRecord(page, initial.ContextID, result)
			return *result, learning.Fail(learning.FailureTransient, "浏览器执行预算耗尽")
		}
		states, err := page.videos(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return e.stopPlayback(page, initial.ContextID, result)
			}
			return *result, learning.Fail(learning.FailureTransient, "浏览器页面连接中断")
		}
		if len(states) != 1 {
			return *result, learning.Fail(learning.FailureCapabilityMissing, "页面视频数量在播放过程中发生变化")
		}
		state := states[0]
		now := time.Now()
		if err := validateVideoState(state, request); err != nil {
			return *result, err
		}
		if state.CurrentTime < lastPosition-0.75 {
			return *result, learning.Fail(learning.FailurePermanent, "检测到播放位置回退或外部跳转")
		}
		elapsed := now.Sub(lastWall).Seconds()
		if state.CurrentTime-lastPosition > elapsed*2.5+2 {
			return *result, learning.Fail(learning.FailurePermanent, "检测到播放位置跳跃，停止执行")
		}
		if state.CurrentTime > lastPosition+0.02 {
			lastProgress = now
			pausedSince = time.Time{}
		}
		if state.Paused && !state.Ended {
			if pausedSince.IsZero() {
				pausedSince = now
			}
			if now.Sub(pausedSince) > e.options.PauseGrace {
				return *result, learning.Fail(learning.FailureCapabilityMissing,
					"页面播放被暂停或需要平台交互，停止自动执行")
			}
		} else {
			pausedSince = time.Time{}
		}
		if now.Sub(lastProgress) > e.options.NoProgressTimeout {
			return *result, learning.Fail(learning.FailureCapabilityMissing, "页面播放长期无进展")
		}
		if state.Ended || (state.Duration > 0 && state.CurrentTime >= state.Duration-0.2) {
			return e.completePlayback(state, result)
		}
		lastPosition = state.CurrentTime
		lastWall = now
	}
}

func (e *Executor) stopPlayback(page *chromePage, contextID int64, result *learning.ExecutionResult) (learning.ExecutionResult, error) {
	pauseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	paused, err := page.pause(pauseCtx, contextID)
	if err != nil || !paused {
		return *result, learning.Fail(learning.FailurePermanent, "无法确认页面播放器已暂停")
	}
	result.StopAcknowledged = true
	return *result, nil
}

func (e *Executor) pauseAndRecord(page *chromePage, contextID int64, result *learning.ExecutionResult) bool {
	pauseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	paused, err := page.pause(pauseCtx, contextID)
	if err == nil && paused {
		result.StopAcknowledged = true
		return true
	}
	return false
}

func (e *Executor) completePlayback(state videoState, result *learning.ExecutionResult) (learning.ExecutionResult, error) {
	if state.Width <= 0 || state.Height <= 0 {
		return *result, learning.Fail(learning.FailureCapabilityMissing, "未观察到真实视频解码尺寸")
	}
	presentedFrames := state.TotalFrames
	if state.DecodedFrames > presentedFrames {
		presentedFrames = state.DecodedFrames
	}
	if presentedFrames <= 0 {
		return *result, learning.Fail(learning.FailureCapabilityMissing,
			fmt.Sprintf("未观察到真实播放帧 decoded=%.0f total=%.0f size=%dx%d pos=%.2f dur=%.2f",
				state.DecodedFrames, state.TotalFrames, state.Width, state.Height, state.CurrentTime, state.Duration))
	}
	if len(state.Played) == 0 {
		return *result, learning.Fail(learning.FailureCapabilityMissing, "未观察到 HTMLMediaElement 播放区间")
	}
	playedEnd := 0.0
	for _, item := range state.Played {
		if item[1] > playedEnd {
			playedEnd = item[1]
		}
	}
	if playedEnd < state.CurrentTime-1 {
		return *result, learning.Fail(learning.FailureCapabilityMissing, "播放区间与当前位置不一致")
	}
	position := math.Max(0, state.CurrentTime)
	duration := state.Duration
	if duration <= 0 {
		return *result, learning.Fail(learning.FailureCapabilityMissing, "页面视频时长无效")
	}
	fraction := math.Min(1, position/duration)
	now := time.Now().UTC()
	observation := learning.Observation{
		Fraction: &fraction, PositionSeconds: &position, DurationSeconds: &duration,
		Source: "browser-html5-video", ObservedAt: now,
		Note: fmt.Sprintf("decoded_frames=%.0f total_frames=%.0f presented_frames=%.0f played_end=%.2f",
			state.DecodedFrames, state.TotalFrames, presentedFrames, playedEnd),
	}
	ranges := make([]string, 0, len(state.Played))
	for _, item := range state.Played {
		ranges = append(ranges, fmt.Sprintf("%.2f-%.2f", item[0], item[1]))
	}
	media := learning.MediaEvidence{
		Available: true, Source: "chrome-html5-video",
		Detail: fmt.Sprintf("decoded_frames=%.0f total_frames=%.0f presented_frames=%.0f played_ranges=%s",
			state.DecodedFrames, state.TotalFrames, presentedFrames, strings.Join(ranges, ",")),
		ObservedAt: now,
	}
	result.Completed = true
	result.Observation = observation
	result.Media = media
	return *result, nil
}

func (e *Executor) targetURL(request learning.ExecutionRequest) (string, error) {
	if e.options.testTargetURL != "" {
		if !e.options.allowLocalTestURLs {
			return "", learning.Fail(learning.FailureCapabilityMissing, "测试目标地址未获准使用")
		}
		if _, err := validateTargetURL(e.options.testTargetURL, true); err != nil {
			return "", learning.Fail(learning.FailureInvalidTask, "测试目标地址无效")
		}
		return e.options.testTargetURL, nil
	}
	for _, value := range []string{request.Task.Course.ExternalCourseID, request.Task.Course.ExternalClassID,
		request.Task.Course.CPI, request.Task.ChapterExternalID} {
		if !safePathID(value) {
			return "", learning.Fail(learning.FailureInvalidTask, "课程或章节标识无效，无法构造可信课程地址")
		}
	}
	target := url.URL{Scheme: "https", Host: "mooc1.chaoxing.com", Path: "/mooc-ans/knowledge/cards"}
	query := target.Query()
	query.Set("clazzid", request.Task.Course.ExternalClassID)
	query.Set("courseid", request.Task.Course.ExternalCourseID)
	query.Set("knowledgeid", request.Task.ChapterExternalID)
	query.Set("num", "0")
	query.Set("ut", "s")
	query.Set("cpi", request.Task.Course.CPI)
	query.Set("v", "20190915")
	query.Set("mooc2", "1")
	target.RawQuery = query.Encode()
	if _, err := validateTargetURL(target.String(), false); err != nil {
		return "", learning.Fail(learning.FailureInvalidTask, "构造的课程地址未通过信任校验")
	}
	return target.String(), nil
}

func safePathID(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
