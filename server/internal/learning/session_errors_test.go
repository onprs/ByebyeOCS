package learning

import (
	"context"
	"errors"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestResolveSessionPreservesTemporaryFailureWithoutBlockingIdentity(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"数据库暂不可用", fault.New("service_unavailable", "夹具数据库不可用"), FailureTransient},
		{"已失效会话", fault.New("platform_session_unavailable", "夹具无有效会话"), FailureIdentityInvalid},
		{"查询超时", context.DeadlineExceeded, FailureTransient},
		{"查询取消", context.Canceled, FailureStopped},
		{"未知内部故障", errors.New("fixture-private-error"), FailureTransient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &Service{opts: Options{Sessions: SessionResolverFunc(func(context.Context, string) ([]byte, error) { return nil, tc.err })}}
			_, err := service.resolveSession(context.Background(), "fixture-user")
			if failureOf(err).Class != tc.want {
				t.Fatalf("会话错误分类错误: %v", err)
			}
			if err.Error() == tc.err.Error() {
				t.Fatal("未净化内部错误")
			}
		})
	}
}
