//go:build integration

package learning

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestVerificationHonorsAccountAndUserStops(t *testing.T) {
	for _, item := range []struct{ name, update string }{
		{"账号限制", `UPDATE public.learning_accounts SET blocked_code='admin_blocked' WHERE user_id=$1`},
		{"平台冷却", `UPDATE public.learning_accounts SET blocked_until=now()+interval '5 minutes' WHERE user_id=$1`},
		{"用户限制", `UPDATE public.users SET status='restricted' WHERE id=$1`},
	} {
		t.Run(item.name, func(t *testing.T) {
			pool := testPool(t)
			service, user, taskID := prepareTask(t, pool, defaultFakePlatform())
			job := claimOne(t, service)
			ctx := context.Background()
			if err := service.applyExecutionResult(ctx, job, ExecutionResult{Completed: true}, nil, service.opts.Executor.Capabilities()); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, item.update, user); err != nil {
				t.Fatal(err)
			}
			claimed, _, err := service.claimVerification(ctx)
			if err != nil || claimed != "" {
				t.Fatalf("受限账号仍被领取核验：%q/%v", claimed, err)
			}
			var calls atomic.Int64
			// 覆盖领取之后才发生限制、调用即将发出的窗口。
			service.runVerification(ctx, verifierFunc(func(context.Context, VerificationRequest) (PlatformCheck, error) {
				calls.Add(1)
				return PlatformCheck{Status: CheckUnconfirmed}, nil
			}), taskID, user)
			if calls.Load() != 0 {
				t.Fatal("账号停止后仍发出平台核验请求")
			}
			if _, err := pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='',blocked_until=NULL WHERE user_id=$1`, user); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE public.users SET status='active' WHERE id=$1`, user); err != nil {
				t.Fatal(err)
			}
			claimed, _, err = service.claimVerification(ctx)
			if err != nil || claimed != taskID {
				t.Fatalf("解除限制后无法继续原核验：%q/%v", claimed, err)
			}
		})
	}
}
