package browser

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
)

type chromeProcess struct {
	cmd        *exec.Cmd
	cdp        *cdpConn
	readerFile *os.File
	writerFile *os.File
	waitCh     chan error
	profile    string
	closeMu    sync.Mutex
	closed     bool
}

func launchChrome(ctx context.Context, options Options, proxyAddr string) (*chromeProcess, error) {
	if options.ChromePath == "" {
		return nil, errors.New("未配置 Chrome 路径")
	}
	info, err := os.Stat(options.ChromePath)
	if err != nil || info.IsDir() {
		return nil, errors.New("Chrome 路径不可用")
	}
	root := options.ProfileRoot
	if root == "" {
		root = os.TempDir()
	}
	profile, err := os.MkdirTemp(root, "byebyeocs-chrome-")
	if err != nil {
		return nil, errors.New("无法创建独立浏览器配置目录")
	}
	if err := os.Chmod(profile, 0o700); err != nil {
		os.RemoveAll(profile)
		return nil, errors.New("独立浏览器配置目录权限设置失败")
	}
	arguments := []string{
		"--remote-debugging-pipe",
		"--user-data-dir=" + profile,
		"--no-first-run", "--no-default-browser-check",
		"--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--disable-extensions", "--disable-default-apps", "--disable-popup-blocking",
		"--disable-dev-shm-usage", "--disable-quic", "--disable-gpu", "--dns-prefetch-disable",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		"--disable-features=Translate,MediaRouter,OptimizationHints,AutofillServerCommunication,InterestFeedContentSuggestions,CalculateNativeWinOcclusion",
		"--disable-background-timer-throttling", "--disable-backgrounding-occluded-windows", "--disable-renderer-backgrounding",
		"--mute-audio", "--autoplay-policy=no-user-gesture-required", "--deny-permission-prompts",
		"--renderer-process-limit=1", "--window-size=1280,720",
		"--lang=zh-CN",
	}
	if options.Headful {
		// 维护桌面的有头调试模式；服务端默认使用无头新实现。
	} else {
		arguments = append(arguments, "--headless=new")
	}
	if options.NoSandbox {
		arguments = append(arguments, "--no-sandbox")
	}
	if options.MemoryLimitMB > 0 {
		arguments = append(arguments, "--js-flags=--max-old-space-size="+strconv.Itoa(options.MemoryLimitMB))
	}
	if !options.disableProxy && proxyAddr != "" {
		arguments = append(arguments,
			"--proxy-server=http://"+proxyAddr,
			"--proxy-bypass-list=<-loopback>",
		)
	} else {
		arguments = append(arguments, "--no-proxy-server")
	}
	arguments = append(arguments, "about:blank")

	childRead, parentWrite, err := os.Pipe()
	if err != nil {
		os.RemoveAll(profile)
		return nil, errors.New("无法建立浏览器通信管道")
	}
	parentRead, childWrite, err := os.Pipe()
	if err != nil {
		childRead.Close()
		parentWrite.Close()
		os.RemoveAll(profile)
		return nil, errors.New("无法建立浏览器通信管道")
	}
	command := exec.Command(options.ChromePath, arguments...)
	command.Env = filteredEnvironment()
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.ExtraFiles = []*os.File{childRead, childWrite}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		childRead.Close()
		parentWrite.Close()
		parentRead.Close()
		childWrite.Close()
		os.RemoveAll(profile)
		return nil, errors.New("浏览器启动失败")
	}
	childRead.Close()
	childWrite.Close()
	process := &chromeProcess{cmd: command, profile: profile, waitCh: make(chan error, 1),
		readerFile: parentRead, writerFile: parentWrite}
	go func() { process.waitCh <- command.Wait() }()
	process.cdp = newCDPConn(parentRead, parentWrite, options.MaxMessageBytes)
	select {
	case <-ctx.Done():
		process.close()
		return nil, ctx.Err()
	case <-time.After(time.Millisecond):
		return process, nil
	}
}

func filteredEnvironment() []string {
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		lower := strings.ToLower(key)
		if strings.Contains(lower, "proxy") {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func (c *chromeProcess) close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.cdp != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = c.cdp.call(closeCtx, "Browser.close", nil, "")
		cancel()
	}
	exited := c.waitProcess(5 * time.Second)
	if !exited {
		c.killGroup()
		exited = c.waitProcess(3 * time.Second)
	}
	if c.cdp != nil {
		c.cdp.Close()
	}
	if c.readerFile != nil {
		c.readerFile.Close()
	}
	if c.writerFile != nil {
		c.writerFile.Close()
	}
	removeErr := os.RemoveAll(c.profile)
	if !exited {
		return errors.New("浏览器进程未能关闭")
	}
	if removeErr != nil {
		return errors.New("浏览器配置目录未能清理")
	}
	return nil
}

func (c *chromeProcess) waitProcess(timeout time.Duration) bool {
	select {
	case <-c.waitCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (c *chromeProcess) killGroup() {
	if c.cmd == nil || c.cmd.Process == nil {
		return
	}
	pid := c.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = c.cmd.Process.Kill()
}

type chromePage struct {
	process     *chromeProcess
	targetID    string
	sessionID   string
	mainFrameID string
	worlds      map[string]int64
	callTimeout time.Duration
}

func (c *chromeProcess) openPage(ctx context.Context) (*chromePage, error) {
	result, err := c.cdp.call(ctx, "Target.createTarget", map[string]any{"url": "about:blank"}, "")
	if err != nil {
		return nil, errors.New("无法创建浏览器页面")
	}
	var created struct {
		TargetID string `json:"targetId"`
	}
	if json.Unmarshal(result, &created) != nil || created.TargetID == "" {
		return nil, errors.New("浏览器页面标识无效")
	}
	attached, err := c.cdp.call(ctx, "Target.attachToTarget",
		map[string]any{"targetId": created.TargetID, "flatten": true}, "")
	if err != nil {
		return nil, errors.New("无法连接浏览器页面")
	}
	var attach struct {
		SessionID string `json:"sessionId"`
	}
	if json.Unmarshal(attached, &attach) != nil || attach.SessionID == "" {
		return nil, errors.New("浏览器页面会话无效")
	}
	page := &chromePage{process: c, targetID: created.TargetID, sessionID: attach.SessionID,
		worlds: map[string]int64{}, callTimeout: 10 * time.Second}
	for _, method := range []string{"Page.enable", "Runtime.enable", "Network.enable"} {
		if _, err := page.call(ctx, method, map[string]any{}); err != nil {
			return nil, errors.New("浏览器页面能力初始化失败")
		}
	}
	tree, err := page.frameTree(ctx)
	if err != nil {
		return nil, err
	}
	page.mainFrameID = tree.Frame.ID
	return page, nil
}

func (p *chromePage) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	callCtx := ctx
	cancel := func() {}
	if p.callTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, p.callTimeout)
	}
	defer cancel()
	return p.process.cdp.call(callCtx, method, params, p.sessionID)
}

type frameNode struct {
	Frame struct {
		ID  string `json:"id"`
		URL string `json:"url"`
	} `json:"frame"`
	ChildFrames []frameNode `json:"childFrames"`
}

func (p *chromePage) frameTree(ctx context.Context) (frameNode, error) {
	raw, err := p.call(ctx, "Page.getFrameTree", map[string]any{})
	if err != nil {
		return frameNode{}, errors.New("无法读取浏览器页面结构")
	}
	var result struct {
		FrameTree frameNode `json:"frameTree"`
	}
	if json.Unmarshal(raw, &result) != nil || result.FrameTree.Frame.ID == "" {
		return frameNode{}, errors.New("浏览器页面结构无效")
	}
	return result.FrameTree, nil
}

func flattenFrames(root frameNode) []frameNode {
	result := []frameNode{root}
	for _, child := range root.ChildFrames {
		result = append(result, flattenFrames(child)...)
	}
	return result
}

func (p *chromePage) frameIDs(ctx context.Context) ([]string, error) {
	tree, err := p.frameTree(ctx)
	if err != nil {
		return nil, err
	}
	frames := flattenFrames(tree)
	result := make([]string, 0, len(frames))
	for _, frame := range frames {
		if frame.Frame.ID != "" {
			result = append(result, frame.Frame.ID)
		}
	}
	return result, nil
}

func (p *chromePage) world(ctx context.Context, frameID string) (int64, error) {
	if id, ok := p.worlds[frameID]; ok {
		return id, nil
	}
	raw, err := p.call(ctx, "Page.createIsolatedWorld", map[string]any{
		"frameId": frameID, "worldName": "byebyeocs-video-observer",
	})
	if err != nil {
		return 0, err
	}
	var result struct {
		ExecutionContextID int64 `json:"executionContextId"`
	}
	if json.Unmarshal(raw, &result) != nil || result.ExecutionContextID <= 0 {
		return 0, errors.New("无法建立页面观察上下文")
	}
	p.worlds[frameID] = result.ExecutionContextID
	return result.ExecutionContextID, nil
}

func (p *chromePage) forgetWorld(frameID string) { delete(p.worlds, frameID) }

type evalResult struct {
	Result struct {
		Type  string          `json:"type"`
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text string `json:"text"`
	} `json:"exceptionDetails"`
}

func (p *chromePage) evaluate(ctx context.Context, contextID int64, expression string) (json.RawMessage, error) {
	raw, err := p.call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expression, "contextId": contextID,
		"returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return nil, err
	}
	var decoded evalResult
	if json.Unmarshal(raw, &decoded) != nil {
		return nil, errors.New("页面观察结果无效")
	}
	if decoded.ExceptionDetails != nil {
		return nil, errors.New("页面脚本执行异常")
	}
	if len(decoded.Result.Value) == 0 {
		return nil, errors.New("页面观察没有返回值")
	}
	return decoded.Result.Value, nil
}

func (p *chromePage) navigate(ctx context.Context, target string, timeout time.Duration) error {
	raw, err := p.call(ctx, "Page.navigate", map[string]any{"url": target})
	if err != nil {
		return errors.New("浏览器无法打开课程页面")
	}
	var result struct {
		ErrorText string `json:"errorText"`
	}
	if json.Unmarshal(raw, &result) == nil && result.ErrorText != "" {
		return errors.New("浏览器打开课程页面失败")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		contextID, err := p.world(ctx, p.mainFrameID)
		if err == nil {
			value, err := p.evaluate(ctx, contextID, `document.readyState`)
			if err == nil && (string(value) == `"complete"` || string(value) == `"interactive"`) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
	return errors.New("课程页面加载超时")
}

func (p *chromePage) setSession(ctx context.Context, session chaoxing.Session) error {
	if _, err := p.call(ctx, "Network.setUserAgentOverride",
		map[string]any{"userAgent": session.UserAgent}); err != nil {
		return errors.New("无法设置浏览器会话上下文")
	}
	cookies := make([]map[string]any, 0, len(session.Cookies))
	for _, cookie := range session.Cookies {
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		item := map[string]any{
			"name": cookie.Name, "value": cookie.Value, "domain": cookie.Domain,
			"path": path, "secure": cookie.Secure, "httpOnly": cookie.HTTPOnly,
		}
		if cookie.Expires > 0 {
			item["expires"] = cookie.Expires
		}
		cookies = append(cookies, item)
	}
	if len(cookies) > 0 {
		if _, err := p.call(ctx, "Network.setCookies", map[string]any{"cookies": cookies}); err != nil {
			return errors.New("无法设置浏览器会话 Cookie")
		}
	}
	return nil
}

const videoDiscoveryScript = `(function(){
  return Array.from(document.querySelectorAll('video')).map(function(v){
    var ranges = [];
    for (var i = 0; i < v.played.length; i++) ranges.push([v.played.start(i), v.played.end(i)]);
    var quality = v.getVideoPlaybackQuality ? v.getVideoPlaybackQuality() : null;
    return {
      src: v.currentSrc || v.src || '',
      duration: Number.isFinite(v.duration) ? v.duration : 0,
      currentTime: Number.isFinite(v.currentTime) ? v.currentTime : 0,
      paused: !!v.paused,
      ended: !!v.ended,
      readyState: v.readyState,
      networkState: v.networkState,
      width: v.videoWidth || 0,
      height: v.videoHeight || 0,
      error: v.error ? v.error.code : 0,
      played: ranges,
      decodedFrames: quality ? quality.decodedVideoFrames : (v.webkitDecodedFrameCount || 0),
      totalFrames: quality ? quality.totalVideoFrames : (v.webkitTotalFrameCount || 0),
      droppedFrames: quality ? quality.droppedVideoFrames : 0
    };
  });
})()`

const playScript = `(function(){
  var list = document.querySelectorAll('video');
  if (list.length !== 1) return 'count';
  var v = list[0];
  v.muted = true;
  try {
    return v.play().then(function(){ return 'ok'; }, function(e){ return 'error:' + String(e && e.name || e); });
  } catch (e) {
    return 'error:' + String(e && e.name || e);
  }
})()`

const pauseScript = `(function(){
  var list = document.querySelectorAll('video');
  if (list.length !== 1) return 'missing';
  var v = list[0];
  v.pause();
  return v.paused ? 'paused' : 'playing';
})()`

type videoState struct {
	FrameID       string       `json:"-"`
	ContextID     int64        `json:"-"`
	Src           string       `json:"src"`
	Duration      float64      `json:"duration"`
	CurrentTime   float64      `json:"currentTime"`
	Paused        bool         `json:"paused"`
	Ended         bool         `json:"ended"`
	ReadyState    int          `json:"readyState"`
	NetworkState  int          `json:"networkState"`
	Width         int          `json:"width"`
	Height        int          `json:"height"`
	Error         int          `json:"error"`
	Played        [][2]float64 `json:"played"`
	DecodedFrames float64      `json:"decodedFrames"`
	TotalFrames   float64      `json:"totalFrames"`
	DroppedFrames float64      `json:"droppedFrames"`
}

func (p *chromePage) videos(ctx context.Context) ([]videoState, error) {
	frameIDs, err := p.frameIDs(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]videoState, 0, 1)
	found := 0
	for _, frameID := range frameIDs {
		contextID, err := p.world(ctx, frameID)
		if err != nil {
			p.forgetWorld(frameID)
			continue
		}
		value, err := p.evaluate(ctx, contextID, videoDiscoveryScript)
		if err != nil {
			p.forgetWorld(frameID)
			continue
		}
		var items []videoState
		if json.Unmarshal(value, &items) != nil {
			continue
		}
		for _, item := range items {
			item.FrameID = frameID
			item.ContextID = contextID
			result = append(result, item)
		}
		found++
	}
	if found == 0 {
		return nil, errors.New("页面中没有可观察的执行上下文")
	}
	return result, nil
}

func (p *chromePage) play(ctx context.Context, contextID int64) (string, error) {
	value, err := p.evaluate(ctx, contextID, playScript)
	if err != nil {
		return "", errors.New("页面播放器无法启动")
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return "", errors.New("页面播放器返回未知状态")
	}
	return result, nil
}

func (p *chromePage) pause(ctx context.Context, contextID int64) (bool, error) {
	value, err := p.evaluate(ctx, contextID, pauseScript)
	if err != nil {
		return false, errors.New("页面播放器无法暂停")
	}
	var result string
	if json.Unmarshal(value, &result) != nil {
		return false, errors.New("页面播放器暂停状态未知")
	}
	return result == "paused", nil
}

func durationTolerance(expected float64) float64 {
	tolerance := expected * 0.05
	if tolerance < 2 {
		tolerance = 2
	}
	return tolerance
}

func withinDurationTolerance(actual, expected float64) bool {
	return actual > 0 && math.Abs(actual-expected) <= durationTolerance(expected)
}

func validateTargetURL(target string, allowLocal bool) (*url.URL, error) {
	parsed, err := url.Parse(target)
	if err != nil || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("浏览器目标地址无效")
	}
	if allowLocal {
		// 只允许隔离测试注入回环地址；生产不可设置此开关。
		if (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1" && parsed.Hostname() != "localhost") {
			return nil, errors.New("测试目标必须是回环地址")
		}
		return parsed, nil
	}
	if parsed.Scheme != "https" {
		return nil, errors.New("浏览器目标必须是 HTTPS")
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "chaoxing.com" && !strings.HasSuffix(host, ".chaoxing.com") {
		return nil, errors.New("浏览器目标不在学习通域名")
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return nil, errors.New("浏览器目标端口无效")
	}
	return parsed, nil
}
