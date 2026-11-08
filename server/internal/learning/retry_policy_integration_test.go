//go:build integration

package learning

import (
	"context"
	"testing"
	"time"
)

func TestTransientRecoveryHasCooldownBudgetAndKeepsFacts(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	service, user, id := prepareTask(t, pool, defaultFakePlatform())
	for cycle := 0; cycle < 3; cycle++ {
		if _, err := pool.Exec(ctx, `UPDATE public.learning_tasks SET state='pending',failure_count=$2,next_attempt_at=now() WHERE id=$1`, id, service.opts.MaxAttempts-1); err != nil {
			t.Fatal(err)
		}
		job := claimOne(t, service)
		service.finish(ctx, job, ExecutionResult{}, &Failure{Class: FailureTransient, Code: "network_timeout", Detail: "连接超时"}, service.opts.Executor.Capabilities())
		var state, code string
		var count, recovered int
		var next *time.Time
		if err := pool.QueryRow(ctx, `SELECT state,last_error_code,failure_count,recovery_cycles,next_attempt_at FROM public.learning_tasks WHERE id=$1`, id).Scan(&state, &code, &count, &recovered, &next); err != nil {
			t.Fatal(err)
		}
		if code != "network_timeout" {
			t.Fatalf("失败原因丢失：%s", code)
		}
		if cycle < 2 {
			if state != StatePending || count != 0 || recovered != cycle+1 || next == nil || next.Before(time.Now().Add(4*time.Minute)) {
				t.Fatalf("冷却预算错误：%s/%d/%d/%v", state, count, recovered, next)
			}
		} else if state != StateFailed || recovered != 2 {
			t.Fatalf("无限恢复或超出预算：%s/%d", state, recovered)
		}
	}
	var confirmed bool
	if err := pool.QueryRow(ctx, `SELECT platform_status='confirmed' FROM public.learning_tasks WHERE id=$1`, id).Scan(&confirmed); err != nil || confirmed {
		t.Fatalf("网络恢复伪造完成：%v/%v", confirmed, err)
	}
	result, err := service.ControlTask(ctx, Actor{ID: user, Kind: "user"}, id, newID(), "redispatch", "网络恢复后重试")
	if err != nil || result.State != StatePending {
		t.Fatalf("明确重试未恢复：%+v/%v", result, err)
	}
}

func TestAnswerRecoveryGenerationIsExplicitBoundedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	service, user, id := prepareTask(t, pool, defaultFakePlatform())
	actor := Actor{ID: user, Kind: "user"}
	for i := 1; i <= 4; i++ {
		if _, err := pool.Exec(ctx, `UPDATE public.learning_tasks SET state='needs_attention',last_error_code='answer_result_unknown' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		key := newID()
		for range 2 {
			if _, err := service.ControlTask(ctx, actor, id, key, "redispatch", "重新查询不可恢复答案"); err != nil {
				t.Fatal(err)
			}
		}
		var generation int
		if err := pool.QueryRow(ctx, `SELECT answer_retry_generation FROM public.learning_tasks WHERE id=$1`, id).Scan(&generation); err != nil {
			t.Fatal(err)
		}
		expected := i
		if expected > 3 {
			expected = 3
		}
		if generation != expected {
			t.Fatalf("重查未去重或没有上限：%d/%d", generation, expected)
		}
	}
}
