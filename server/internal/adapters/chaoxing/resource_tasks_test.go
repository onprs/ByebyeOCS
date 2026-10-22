package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func TestAudioUsesAudioProtocolAtNormalElapsedTime(t *testing.T) {
	for _, platformType := range []string{"audio", "video"} {
		t.Run(platformType, func(t *testing.T) {
			f := newHTTPExecutionFixture(t)
			f.request.TaskKind = "audio"
			f.request.Task.CardIndex = 2
			f.mutate = func(defaults, task map[string]any) {
				task["type"] = platformType
				task["property"] = map[string]any{"type": ".mp3"}
			}
			result, err := f.executor.Execute(context.Background(), f.request)
			if err != nil || !result.Completed || result.Media.Available {
				t.Fatalf("音频执行或证据错误: %+v / %v", result, err)
			}
			if len(f.reports) != 4 || f.reports[0].Get("playingTime") != "30" || f.reports[1].Get("playingTime") != "60" {
				t.Fatal("音频没有按保存位置与实际经过时间执行")
			}
			for _, query := range f.reports {
				if query.Get("dtype") != "Audio" {
					t.Fatal("音频仍使用Video协议")
				}
			}
		})
	}
}

func TestDocumentAndReadingRequireIndependentTaskConfirmation(t *testing.T) {
	for _, kind := range []string{"document", "read"} {
		for _, confirm := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/confirm=%v", kind, confirm), func(t *testing.T) {
				var mu sync.Mutex
				passed, calls := false, 0
				var server *httptest.Server
				server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					mu.Lock()
					defer mu.Unlock()
					switch request.URL.Path {
					case "/cards":
						if request.URL.Query().Get("num") != "2" {
							t.Error("未使用任务实际所在卡片")
						}
						data, _ := json.Marshal(map[string]any{"defaults": map[string]any{"courseid": "101", "clazzId": "202", "cpi": "303", "knowledgeid": "5001"}, "attachments": []any{map[string]any{"type": kind, "jobid": "document-job", "jtoken": "fixture-document-token", "otherInfo": "nodeId_5001-cpi_303", "isPassed": passed}}})
						fmt.Fprintf(w, "mArg=%s;", data)
					case "/ananas/job/document", "/ananas/job/readv2":
						want := "/ananas/job/document"
						if kind == "read" {
							want = "/ananas/job/readv2"
						}
						query := request.URL.Query()
						if request.URL.Path != want || query.Get("jobid") != "document-job" || query.Get("knowledgeid") != "5001" || query.Get("jtoken") != "fixture-document-token" || query.Get("courseid") != "101" || query.Get("clazzid") != "202" {
							t.Error("文档/阅读端点或任务上下文错误")
						}
						calls++
						passed = confirm
						fmt.Fprint(w, `{"status":true,"msg":"请求已受理"}`)
					default:
						http.NotFound(w, request)
					}
				}))
				defer server.Close()
				target, _ := url.Parse(server.URL)
				executor, err := NewHTTPExecutor(HTTPExecutorOptions{Enabled: true, Authorized: true, Endpoints: Endpoints{Cards: server.URL + "/cards", Catalog: server.URL + "/catalog", CourseList: server.URL + "/courses"}, httpOptions: httpOptions{AllowedHosts: []string{target.Hostname()}, allowPrivateNetworks: true, MinRequestInterval: 50 * time.Millisecond, newClient: func(jar http.CookieJar) *http.Client {
					client := *server.Client()
					client.Jar = jar
					return &client
				}}})
				if err != nil {
					t.Fatal(err)
				}
				request := learning.ExecutionRequest{TaskKind: "document", Session: encodeTestSession(t), Task: learning.PlatformTaskRef{CardIndex: 2, Course: learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}, ChapterExternalID: "5001", TaskPointID: "document-job", ObjectID: "document-job"}}
				before, err := executor.platform.CheckTask(context.Background(), request.Session, request.Task)
				if err != nil || before.Passed {
					t.Fatal("执行前核验错误")
				}
				result, err := executor.Execute(context.Background(), request)
				if err != nil || !result.Completed || result.Media.Available {
					t.Fatalf("请求没有正常结束: %+v / %v", result, err)
				}
				after, err := executor.platform.CheckTask(context.Background(), request.Session, request.Task)
				if err != nil || after.Passed != confirm || calls != 1 {
					t.Fatalf("请求受理替代了平台确认: %+v / %v / %d", after, err, calls)
				}
			})
		}
	}
}

func TestMediaTitleContainingVerificationWordsDoesNotBlockExecution(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	f.mutate = func(defaults, task map[string]any) {
		task["name"] = "请输入验证码与登录权限的案例分析"
	}
	result, err := f.executor.Execute(context.Background(), f.request)
	if err != nil || !result.Completed {
		t.Fatalf("课程标题被误判成平台验证要求: %+v / %v", result, err)
	}
}

func TestUnsupportedTaskDoesNotIssueRequests(t *testing.T) {
	f := newHTTPExecutionFixture(t)
	f.request.TaskKind = "other"
	_, err := f.executor.Execute(context.Background(), f.request)
	var failure *learning.Failure
	if !errors.As(err, &failure) || failure.Class != learning.FailureCapabilityMissing || f.requests != 0 {
		t.Fatalf("未支持类型发出了执行请求: %v / %d", err, f.requests)
	}
}
