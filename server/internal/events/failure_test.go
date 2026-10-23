package events

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestFailureLogThrottlesAndSanitizes(t *testing.T) {
	var buffer bytes.Buffer
	service := New(nil)
	service.SetLogger(slog.New(slog.NewJSONHandler(&buffer, nil)))
	for range 5 {
		service.failures.report("publish", fault.New("service_unavailable", "连接串与凭据细节"))
	}
	if lines := strings.Count(strings.TrimSpace(buffer.String()), "\n") + 1; lines != 1 {
		t.Fatalf("重复故障未节流：记录了 %d 条", lines)
	}
	if strings.Contains(buffer.String(), "连接串与凭据细节") {
		t.Fatal("后台日志泄露了原始错误内容")
	}
	if !strings.Contains(buffer.String(), `"code":"service_unavailable"`) || !strings.Contains(buffer.String(), `"component":"publish"`) {
		t.Fatal("后台日志缺少安全分类码")
	}
	// 超过节流窗口后再次失败，必须带上被抑制的次数。
	service.failures.mu.Lock()
	service.failures.last["publish/service_unavailable"] = time.Now().Add(-2 * failureLogInterval)
	service.failures.mu.Unlock()
	service.failures.report("publish", fault.New("service_unavailable", "连接串与凭据细节"))
	lines := strings.Split(strings.TrimSpace(buffer.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("节流窗口后未补记：%d 条", len(lines))
	}
	if !strings.Contains(lines[1], `"suppressed":4`) {
		t.Fatalf("未记录被抑制次数：%s", lines[1])
	}
	// 不同分类立即记录，未装配日志与 nil 接收者都不得 panic。
	service.failures.report("relay_learning", fault.New("validation_failed", "细节"))
	if got := len(strings.Split(strings.TrimSpace(buffer.String()), "\n")); got != 3 {
		t.Fatalf("不同分类未立即记录：%d 条", got)
	}
	var missing *failureLog
	missing.report("publish", fault.New("service_unavailable", "细节"))
}
