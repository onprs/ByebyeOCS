package operations

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestQuestionPermitsShareLimitAndCancelBackgroundWait(t *testing.T) {
	service := &Service{options: Options{DataDir: t.TempDir()}, questionSlots: make(chan struct{}, 1)}
	release, err := service.AcquireQuestion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AcquireQuestion(context.Background()); err == nil {
		t.Fatal("HTTP 查询绕过了共享并发限制")
	}
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		release, err := service.WaitForQuestion(ctx)
		if err == nil {
			release()
		}
		finished <- err
	}()
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("已取消的后台查询仍取得名额")
		}
	case <-time.After(time.Second):
		t.Fatal("取消没有中断后台等待")
	}
	release()
	if len(service.questionSlots) != 0 {
		t.Fatal("取消等待泄漏了题库并发名额")
	}
	release, err = service.WaitForQuestion(context.Background())
	if err != nil {
		t.Fatal("原查询释放后后台无法继续", err)
	}
	release()
}

func TestBackgroundQuestionRechecksRecoveryStateAfterWaiting(t *testing.T) {
	service := &Service{options: Options{DataDir: t.TempDir()}, questionSlots: make(chan struct{}, 1)}
	release, err := service.AcquireQuestion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		permit, err := service.WaitForQuestion(context.Background())
		if err == nil {
			permit()
		}
		finished <- err
	}()
	if err := os.WriteFile(filepath.Join(service.options.DataDir, ".recovery-required"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	release()
	select {
	case err := <-finished:
		code, _ := fault.Describe(err)
		if code != "recovery_required" {
			t.Fatal("等待结束后没有核对恢复状态", code)
		}
	case <-time.After(time.Second):
		t.Fatal("后台等待没有结束")
	}
	if len(service.questionSlots) != 0 {
		t.Fatal("运行门槛拒绝后仍占用名额")
	}
}
