package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

type executionClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *executionClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *executionClock) wait(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
	return nil
}

type httpExecutionFixture struct {
	server      *httptest.Server
	executor    *HTTPExecutor
	request     learning.ExecutionRequest
	clock       *executionClock
	mu          sync.Mutex
	reports     []url.Values
	requests    int
	mutate      func(map[string]any, map[string]any)
	status      int
	failurePath string
	failureBody string
	timeline    string
	neverPass   bool
}

func newHTTPExecutionFixture(t *testing.T) *httpExecutionFixture {
	t.Helper()
	f := &httpExecutionFixture{clock: &executionClock{at: time.Unix(1700000000, 0)}}
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests++
		if cookie, err := request.Cookie("fixture_session"); err != nil || cookie.Value != "fixture-authorized-session" {
			w.WriteHeader(401)
			return
		}
		if f.status != 0 && (f.failurePath == "" || f.failurePath == request.URL.Path) {
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(f.status)
			fmt.Fprint(w, f.failureBody)
			return
		}
		switch request.URL.Path {
		case "/cards":
			defaults := map[string]any{"courseid": "100", "clazzId": "200", "cpi": "300", "userid": "500", "fid": "114", "reportTimeInterval": 30, "reportUrl": f.server.URL + "/multimedia/log/a/300", "initdataUrl": f.server.URL + "/timeline"}
			task := map[string]any{"type": "video", "objectId": "video-object", "jobid": "video-job", "playTime": 30000, "isPassed": false, "attDuration": 90, "attDurationEnc": "fixture-duration-claim", "otherInfo": "node-rt_d-ds0&courseId=100"}
			if f.mutate != nil {
				f.mutate(defaults, task)
			}
			body, _ := json.Marshal(map[string]any{"defaults": defaults, "attachments": []any{task}})
			fmt.Fprintf(w, `<iframe src="/ananas/modules/video/index.html"></iframe><script>mArg = %s;</script>`, body)
		case "/ananas/modules/video/index.html":
			fmt.Fprint(w, "<html><body>协议测试文档</body></html>")
		case "/ananas/status/video-object":
			fmt.Fprint(w, `{"status":"success","duration":90,"dtoken":"fixture-request-token"}`)
		case "/timeline":
			fmt.Fprint(w, f.timeline)
		case "/multimedia/log/a/300/fixture-request-token":
			if request.Header.Get("Referer") != f.server.URL+"/ananas/modules/video/index.html" {
				t.Error("原生请求没有正常来源上下文")
			}
			query := request.URL.Query()
			for key, values := range query {
				if len(values) != 1 {
					t.Errorf("请求键 %s 重复", key)
				}
			}
			f.reports = append(f.reports, query)
			position, _ := strconv.Atoi(query.Get("playingTime"))
			_ = json.NewEncoder(w).Encode(map[string]any{"isPassed": !f.neverPass && position >= 90, "captcha": false})
		default:
			t.Errorf("出现未声明的请求路径 %s", request.URL.Path)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.server.Close)
	target, _ := url.Parse(f.server.URL)
	raw, _ := json.Marshal(Session{Authorized: true, UserAgent: "ByebyeOCS-Protocol-Fixture", Cookies: []Cookie{{Name: "fixture_session", Value: "fixture-authorized-session", Domain: target.Hostname(), Path: "/", Secure: true}}})
	f.request = learning.ExecutionRequest{Session: raw, Task: learning.PlatformTaskRef{Course: learning.PlatformCourseRef{ExternalCourseID: "100", ExternalClassID: "200", CPI: "300"}, ChapterExternalID: "400", ObjectID: "video-object", TaskPointID: "video-job"}, MediaDurationSeconds: 90}
	var err error
	f.executor, err = NewHTTPExecutor(HTTPExecutorOptions{Enabled: true, Authorized: true, Concurrency: 2, ConfirmGrace: time.Minute, now: f.clock.now, wait: f.clock.wait, Endpoints: Endpoints{Cards: f.server.URL + "/cards", Catalog: f.server.URL + "/catalog", CourseList: f.server.URL + "/courses"}, httpOptions: httpOptions{AllowedHosts: []string{target.Hostname()}, allowPrivateNetworks: true, MinRequestInterval: 50 * time.Millisecond, newClient: func(jar http.CookieJar) *http.Client {
		client := f.server.Client()
		copy := *client
		copy.Jar = jar
		return &copy
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestHTTPExecutorNormalProtocolAndEvidence(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	result, err := f.executor.Execute(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed || result.Media.Available || result.Observation.Source != "chaoxing-http" || *result.Observation.PositionSeconds != 90 {
		t.Fatalf("执行结果或证据不符: %+v", result)
	}
	var positions, events []string
	for _, query := range f.reports {
		positions = append(positions, query.Get("playingTime"))
		events = append(events, query.Get("isdrag"))
		if query.Get("duration") != "90" || query.Get("clipTime") != "0_90" || query.Get("otherInfo") != "node-rt_d-ds0" || query.Get("rt") != "0.9" || query.Get("attDurationEnc") != "fixture-duration-claim" || query.Get("courseId") != "100" {
			t.Fatalf("平台原始字段未保持: %v", query)
		}
		if query.Has("courseEngineInfo") {
			t.Fatal("未知课程引擎状态被伪造为默认值")
		}
		if len(query.Get("enc")) != 32 {
			t.Fatal("签名缺失")
		}
	}
	if !reflect.DeepEqual(positions, []string{"30", "60", "90", "90"}) || !reflect.DeepEqual(events, []string{"3", "0", "0", "4"}) {
		t.Fatalf("没有按保存位置和普通节奏执行: %v/%v", positions, events)
	}
	if f.reports[0].Get("enc") != "bdf2142f22d715b2a3abf787b6e3fdbf" {
		t.Fatal("普通协议签名与固定向量不符")
	}
	if f.reports[1].Get("_t") != "1700000030000" {
		t.Fatal("请求时间没有使用真实时钟语义")
	}
	if f.executor.Capabilities().ProducesMediaEvidence || !f.executor.Capabilities().SafeLeaseRecovery {
		t.Fatal("执行器能力声明不符")
	}
}

func TestHTTPExecutorUnconfirmedDoesNotSucceed(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	f.neverPass = true
	result, err := f.executor.Execute(context.Background(), f.request)
	var failure *learning.Failure
	if !errors.As(err, &failure) || failure.Class != learning.FailureCapabilityMissing || result.Completed || result.Media.Available {
		t.Fatalf("未确认请求被报为完成: %+v/%v", result, err)
	}
	if len(f.reports) > 6 {
		t.Fatal("平台未确认时出现无界请求")
	}
	for _, query := range f.reports {
		if query.Get("isdrag") == "4" {
			t.Fatal("未确认时提前发送结束请求")
		}
	}
}

func TestHTTPExecutorStopsOnPlatformFailures(t *testing.T) {
	for _, item := range []struct {
		name   string
		status int
		body   string
		want   learning.FailureClass
	}{
		{"身份失效", 401, "private-response", learning.FailureIdentityInvalid},
		{"拒绝访问", 403, "private-response", learning.FailureForbidden},
		{"限流", 429, "private-response", learning.FailureRateLimited},
		{"验证码", 200, "captcha: private-response", learning.FailureVerificationNeeded},
		{"明确要求验证", 200, `{"captcha":true}`, learning.FailureVerificationNeeded},
		{"拒绝请求与通过字段冲突", 200, `{"status":false,"isPassed":true}`, learning.FailureCapabilityMissing},
		{"未知响应", 200, "private-response", learning.FailureCapabilityMissing},
	} {
		t.Run(item.name, func(t *testing.T) {
			f := newHTTPExecutionFixture(t)
			f.status = item.status
			f.failureBody = item.body
			f.failurePath = "/multimedia/log/a/300/fixture-request-token"
			_, err := f.executor.Execute(context.Background(), f.request)
			var failure *learning.Failure
			if !errors.As(err, &failure) || failure.Class != item.want {
				t.Fatalf("错误分类不符: %v", err)
			}
			if strings.Contains(err.Error(), "private-response") || strings.Contains(err.Error(), "fixture-request-token") {
				t.Fatal("错误暴露上游内容或令牌")
			}
			if item.status == 429 && failure.RetryAfter != 2*time.Minute {
				t.Fatal("限流等待信息丢失")
			}
			if len(f.reports) != 0 || f.requests != 4 {
				t.Fatalf("出错后出现重复请求: %d", f.requests)
			}
		})
	}
}

func TestHTTPExecutorRejectsUnsafeTaskContexts(t *testing.T) {
	for _, item := range []struct {
		name   string
		mutate func(map[string]any, map[string]any)
	}{
		{"课程不符", func(d, a map[string]any) { d["courseid"] = "999" }},
		{"任务不符", func(d, a map[string]any) { a["jobid"] = "wrong-job" }},
		{"无任务点附件", func(d, a map[string]any) { delete(a, "jobid") }},
		{"保存位置缺失", func(d, a map[string]any) { delete(a, "playTime") }},
		{"保存位置类型错误", func(d, a map[string]any) { a["playTime"] = false }},
		{"位置超长", func(d, a map[string]any) { a["playTime"] = 91000 }},
		{"请求间隔过短", func(d, a map[string]any) { d["reportTimeInterval"] = 1 }},
		{"未知比例", func(d, a map[string]any) { a["otherInfo"] = "node-rt_4" }},
		{"课程参数注入", func(d, a map[string]any) { a["otherInfo"] = "node-rt_d&courseId=100&playingTime=90" }},
		{"跨域请求入口", func(d, a map[string]any) { d["reportUrl"] = "https://metadata.example.invalid/multimedia/log/a/300" }},
		{"错误协议路径", func(d, a map[string]any) { d["reportUrl"] = "https://mooc1.chaoxing.com/other/300" }},
		{"抓拍限制", func(d, a map[string]any) { d["chapterCapture"] = 1 }},
		{"未知限制类型", func(d, a map[string]any) { d["chapterCapture"] = true }},
		{"未知引擎上下文", func(d, a map[string]any) { d["courseEngineInfo"] = "unknown" }},
		{"未知交互结构", func(d, a map[string]any) { a["jumpTimePointList"] = "unknown" }},
		{"交互时间点", func(d, a map[string]any) { a["jumpTimePointList"] = []any{1} }},
	} {
		t.Run(item.name, func(t *testing.T) {
			f := newHTTPExecutionFixture(t)
			f.mutate = item.mutate
			result, err := f.executor.Execute(context.Background(), f.request)
			if err == nil || result.Completed || len(f.reports) != 0 {
				t.Fatalf("不安全上下文触发执行: %+v/%v", result, err)
			}
		})
	}
}

func TestHTTPExecutorRequiresManualInteractions(t *testing.T) {
	for _, body := range []string{`[{"type":"question"}]`, `{"unexpected":true}`} {
		f := newHTTPExecutionFixture(t)
		f.timeline = body
		f.mutate = func(d, a map[string]any) { a["mid"] = "fixture-mid" }
		_, err := f.executor.Execute(context.Background(), f.request)
		if err == nil || len(f.reports) != 0 {
			t.Fatal("交互任务或未知结构被跳过")
		}
	}
	f := newHTTPExecutionFixture(t)
	f.timeline = `{"data":[]}`
	f.mutate = func(d, a map[string]any) { a["mid"] = "fixture-mid"; d["courseEngineInfo"] = true }
	if _, err := f.executor.Execute(context.Background(), f.request); err != nil {
		t.Fatal(err)
	}
	if f.reports[0].Get("courseEngineInfo") != "true" {
		t.Fatal("显式平台引擎上下文丢失")
	}
}

func TestHTTPExecutorConcurrentSlotsAndCancellation(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	ready := make(chan struct{}, 2)
	f.executor.options.wait = func(ctx context.Context, _ time.Duration) error {
		ready <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := f.executor.Execute(ctx, f.request)
			if err == nil || !result.StopAcknowledged {
				t.Error("并发请求未安全取消")
			}
		}()
	}
	for range 2 {
		select {
		case <-ready:
		case <-time.After(3 * time.Second):
			t.Fatal("不同任务无法并发进入原生执行器")
		}
	}
	_, err := f.executor.Execute(context.Background(), f.request)
	var failure *learning.Failure
	if !errors.As(err, &failure) || failure.Class != learning.FailureRateLimited {
		t.Fatal("原生执行并发上限未生效")
	}
	cancel()
	group.Wait()
}

func TestHTTPExecutorAuthorizationCancellationAndLimits(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	f.executor.options.Authorized = false
	if _, err := f.executor.Execute(context.Background(), f.request); err == nil || f.requests != 0 {
		t.Fatal("未授权仍发送请求")
	}
	f.executor.options.Authorized = true
	f.request.StopRequested = true
	result, err := f.executor.Execute(context.Background(), f.request)
	if err == nil || !result.StopAcknowledged || f.requests != 0 {
		t.Fatal("停止请求未在网络操作前生效")
	}
	f.request.StopRequested = false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.executor.options.wait = func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }
	result, err = f.executor.Execute(ctx, f.request)
	if err == nil || !result.StopAcknowledged || result.Completed || len(f.reports) != 1 {
		t.Fatal("执行取消后仍请求平台或误报完成")
	}
	for _, options := range []HTTPExecutorOptions{{Concurrency: 65}, {MaxDuration: 25 * time.Hour}, {ConfirmGrace: time.Hour}} {
		if _, err := NewHTTPExecutor(options); err == nil {
			t.Fatal("无界执行预算被接受")
		}
	}
}
