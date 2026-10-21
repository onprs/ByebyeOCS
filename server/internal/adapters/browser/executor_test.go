package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func chromeBinary(t *testing.T) string {
	t.Helper()
	if value := os.Getenv("BYEBYEOCS_CHROME_PATH"); value != "" {
		return value
	}
	for _, candidate := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	t.Skip("本机没有可用于隔离测试的 Chrome")
	return ""
}

func createFixture(t *testing.T, seconds int) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机没有 ffmpeg，无法生成无版权测试视频")
	}
	path := filepath.Join(t.TempDir(), "fixture.webm")
	command := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=15",
		"-t", fmt.Sprintf("%d", seconds), "-c:v", "libvpx-vp9", "-b:v", "200k", "-an", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("无法生成测试视频: %v %s", err, string(output))
	}
	return path
}

func testSessionBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"cookies": []map[string]any{{"name": "test-session", "value": "opaque",
			"domain": "127.0.0.1", "path": "/", "secure": false, "httpOnly": true}},
		"user_agent": "byebyeocs-browser-test", "authorized_by_user": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func testBrowserOptions(t *testing.T) Options {
	return Options{
		Enabled: true, Authorized: true, ChromePath: chromeBinary(t), Headful: false,
		StartupTimeout: 25 * time.Second, ExecutionTimeout: 30 * time.Second,
		PauseGrace: 6 * time.Second, NoProgressTimeout: 12 * time.Second,
		DevToolsTimeout: 10 * time.Second, MemoryLimitMB: 512,
		ProfileRoot: t.TempDir(), allowLocalTestURLs: true, disableProxy: true,
	}
}

func testRequest(serverURL, objectID string, duration float64) learning.ExecutionRequest {
	return learning.ExecutionRequest{
		AttemptID: "attempt-1", TaskID: "task-1", UserID: "user-1", FenceToken: 1,
		Session: testSessionBytesForTest(),
		Task: learning.PlatformTaskRef{
			Course: learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202",
				CPI: "303", CycleKey: "current"},
			ChapterExternalID: "5001", ObjectID: objectID, TaskPointID: "9001",
		},
		MediaDurationSeconds: duration,
	}
}

func testSessionBytesForTest() []byte {
	raw, _ := json.Marshal(map[string]any{
		"cookies": []map[string]any{{"name": "test-session", "value": "opaque",
			"domain": "127.0.0.1", "path": "/", "secure": false, "httpOnly": true}},
		"user_agent": "byebyeocs-browser-test", "authorized_by_user": true,
	})
	return raw
}

func videoServer(t *testing.T, videoPath string, videos int, seek *atomic.Int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		elements := strings.Repeat(`<video src="/fixture.webm" controls playsinline></video>`, videos)
		io.WriteString(w, `<!doctype html><html><body>`+elements+`<script>
			Array.from(document.querySelectorAll('video')).forEach(function(v){
				v.addEventListener('seeking', function(){ fetch('/seek?at='+v.currentTime); });
			});
		</script></body></html>`)
	})
	mux.HandleFunc("/seek", func(w http.ResponseWriter, r *http.Request) {
		if seek != nil {
			seek.Add(1)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/fixture.webm", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/webm")
		http.ServeFile(w, r, videoPath)
	})
	return httptest.NewServer(mux)
}

func TestExecutorActualPlaybackAndNoSeek(t *testing.T) {
	video := createFixture(t, 3)
	var seek atomic.Int32
	server := videoServer(t, video, 1, &seek)
	defer server.Close()
	options := testBrowserOptions(t)
	options.testTargetURL = server.URL + "/page"
	executor, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(server.URL, "fixture", 3)
	request.Session = testSessionBytes(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	result, err := executor.Execute(ctx, request)
	if err != nil {
		t.Fatalf("浏览器执行失败: %v", err)
	}
	if !result.Completed || !result.Media.Available {
		t.Fatalf("没有完成真实播放或缺少媒体证据: %+v", result)
	}
	if result.Observation.PositionSeconds == nil || *result.Observation.PositionSeconds < 1 {
		t.Fatalf("播放位置没有实际推进: %+v", result.Observation)
	}
	if !strings.Contains(result.Media.Detail, "presented_frames=") ||
		strings.Contains(result.Media.Detail, "presented_frames=0 ") {
		t.Fatalf("没有观察到真实播放帧: %s", result.Media.Detail)
	}
	if seek.Load() != 0 {
		t.Fatal("执行器对页面视频执行了 seek")
	}
	entries, err := os.ReadDir(options.ProfileRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("独立浏览器配置目录未清理", err, len(entries))
	}
}

func TestExecutorCancelPausesAndCloses(t *testing.T) {
	video := createFixture(t, 8)
	server := videoServer(t, video, 1, nil)
	defer server.Close()
	options := testBrowserOptions(t)
	options.testTargetURL = server.URL + "/page"
	options.ExecutionTimeout = 20 * time.Second
	started := make(chan struct{}, 1)
	options.onPlaybackStarted = func() {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	executor, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(server.URL, "fixture", 8)
	request.Session = testSessionBytes(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var result learning.ExecutionResult
	var executeErr error
	go func() {
		defer close(done)
		result, executeErr = executor.Execute(ctx, request)
	}()
	select {
	case <-started:
	case <-time.After(20 * time.Second):
		cancel()
		t.Fatal("浏览器播放没有开始")
	}
	time.Sleep(700 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消后浏览器执行器没有及时退出")
	}
	if executeErr != nil || !result.StopAcknowledged {
		t.Fatalf("取消没有确认暂停/关闭: %+v %v", result, executeErr)
	}
	entries, err := os.ReadDir(options.ProfileRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("取消后浏览器配置目录未清理", err, len(entries))
	}
}

func TestExecutorRejectsMultipleVideos(t *testing.T) {
	video := createFixture(t, 2)
	server := videoServer(t, video, 2, nil)
	defer server.Close()
	options := testBrowserOptions(t)
	options.testTargetURL = server.URL + "/page"
	executor, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest(server.URL, "fixture", 2)
	request.Session = testSessionBytes(t)
	result, err := executor.Execute(context.Background(), request)
	var failure *learning.Failure
	if !errors.As(err, &failure) || failure.Class != learning.FailureCapabilityMissing {
		t.Fatalf("多个视频未被拒绝: %+v %v", result, err)
	}
	if result.Media.Available {
		t.Fatal("被拒绝的执行仍产生媒体证据")
	}
}

func TestExecutorDisabledAndMissingChrome(t *testing.T) {
	options := Options{Enabled: true, Authorized: true, ChromePath: filepath.Join(t.TempDir(), "missing")}
	executor, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := testRequest("https://127.0.0.1/", "fixture", 2)
	request.Session = testSessionBytes(t)
	if _, err := executor.Execute(context.Background(), request); err == nil {
		t.Fatal("缺少 Chrome 仍执行")
	}
	unauthorized, err := New(Options{Enabled: true, Authorized: false, ChromePath: "/bin/false"})
	if err != nil {
		t.Fatal(err)
	}
	if unauthorized.Capabilities().Enabled {
		t.Fatal("未授权执行器被标记为可用")
	}
	if _, err := unauthorized.Execute(context.Background(), request); err == nil {
		t.Fatal("未授权执行器仍执行")
	}
	options.testTargetURL = "http://127.0.0.1:1/page"
	options.allowLocalTestURLs = false
	denied, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Execute(context.Background(), request); err == nil {
		t.Fatal("未获准的测试目标仍被使用")
	}
}

func TestTargetURLIsConstructedFromRefs(t *testing.T) {
	executor, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	request := learning.ExecutionRequest{Task: learning.PlatformTaskRef{
		Course:            learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"},
		ChapterExternalID: "5001",
	}}
	target, err := executor.targetURL(request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(target, "https://mooc1.chaoxing.com/mooc-ans/knowledge/cards?") ||
		!strings.Contains(target, "courseid=101") || !strings.Contains(target, "knowledgeid=5001") {
		t.Fatalf("可信课程地址构造错误: %s", target)
	}
	request.Task.ChapterExternalID = "https://evil.example"
	if _, err := executor.targetURL(request); err == nil {
		t.Fatal("不可信章节标识未被拒绝")
	}
}

func TestProxyRejectsUntrustedTargets(t *testing.T) {
	proxy, err := startConnectProxy(nil, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.Close()
	connection, err := net.Dial("tcp", proxy.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	io.WriteString(connection, "CONNECT 127.0.0.1:443 HTTP/1.1\r\nHost: 127.0.0.1:443\r\n\r\n")
	buffer := make([]byte, 128)
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _ := connection.Read(buffer)
	if !strings.Contains(string(buffer[:n]), "403") {
		t.Fatalf("私网目标未被代理拒绝: %s", string(buffer[:n]))
	}
	for _, target := range []string{"evil.example:443", "mooc1.chaoxing.com:80"} {
		conn, err := net.Dial("tcp", proxy.Addr())
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := conn.Read(buffer)
		conn.Close()
		if !strings.Contains(string(buffer[:n]), "403") {
			t.Fatalf("非允许目标未被拒绝: %s %s", target, string(buffer[:n]))
		}
	}
}

func TestPublicIPValidation(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1", "100.64.0.1"} {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatal(err)
		}
		if isPublicIP(address) {
			t.Fatalf("内网地址未被拒绝: %s", raw)
		}
	}
}
