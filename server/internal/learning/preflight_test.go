package learning

import (
	"context"
	"testing"
	"time"
)

type unknownVerifier struct{}

func (unknownVerifier) Verify(context.Context, VerificationRequest) (PlatformCheck, error) {
	return PlatformCheck{Status: CheckUnknown}, nil
}

// 完成事实未知允许原生执行器校验协议；未支持类型仍阻断，绝不把未知当完成。
func TestPreflightUnknownAllowsSupportedHTTPTasks(t *testing.T) {
	service := &Service{opts: Options{PreflightTimeout: time.Second, Verifier: unknownVerifier{}}}
	quiz := executionJob{TaskID: "task", TaskKind: "quiz"}
	completed, err := service.preflight(context.Background(), quiz, nil)
	if err != nil || completed {
		t.Fatalf("测验核验未知被阻断: %v/%v", completed, err)
	}
	for _, kind := range []string{"video", "audio", "document"} {
		completed, err := service.preflight(context.Background(), executionJob{TaskID: "task", TaskKind: kind}, nil)
		if err != nil || completed {
			t.Fatalf("支持的任务不能检查协议：%s/%v", kind, err)
		}
	}
	if _, err := service.preflight(context.Background(), executionJob{TaskID: "task", TaskKind: "other"}, nil); err == nil {
		t.Fatal("未支持类型被放行")
	}
}
