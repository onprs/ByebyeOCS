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

// 测验卡片没有完成字段，核验未知必须放行给工作执行器；其他任务仍然阻断。
func TestPreflightUnknownAllowsQuizOnly(t *testing.T) {
	service := &Service{opts: Options{PreflightTimeout: time.Second, Verifier: unknownVerifier{}}}
	quiz := executionJob{TaskID: "task", TaskKind: "quiz"}
	completed, err := service.preflight(context.Background(), quiz, nil)
	if err != nil || completed {
		t.Fatalf("测验核验未知被阻断: %v/%v", completed, err)
	}
	video := executionJob{TaskID: "task", TaskKind: "video"}
	if _, err := service.preflight(context.Background(), video, nil); err == nil {
		t.Fatal("视频核验未知未阻断")
	}
}
