package chaoxing

import (
	"context"
	"os"
	"testing"
	"time"
)

// 资源检查显式运行普通60秒请求序列，仅连接本测试创建的TLS协议夹具。
func TestHTTPExecutorResourceScenario(t *testing.T) {
	if os.Getenv("BYEBYEOCS_RUN_RESOURCE_TEST") != "1" {
		t.Skip("资源检查需要显式启用，并在受限控制组中运行")
	}
	fixture := newHTTPExecutionFixture(t)
	fixture.executor.options.now = time.Now
	fixture.executor.options.wait = waitHTTPExecution
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	started := time.Now()
	result, err := fixture.executor.Execute(ctx, fixture.request)
	if err != nil || !result.Completed || result.Media.Available {
		t.Fatalf("原生请求资源场景未完成: %v", err)
	}
	if time.Since(started) < time.Minute || len(fixture.reports) != 4 {
		t.Fatal("资源检查未使用真实等待时间和完整普通请求序列")
	}
}
