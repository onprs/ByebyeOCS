//go:build integration

package learning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecutionObservationUsesCurrentLeaseAndNeverConfirms(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	service, _, id := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	half := 0.5
	if err := service.recordExecutionObservation(ctx, job, Observation{Fraction: &half, Source: "chaoxing-http", ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var state, platformStatus, eventState string
	var version, eventVersion int64
	if err := pool.QueryRow(ctx, `SELECT state,platform_status,version FROM public.learning_tasks WHERE id=$1`, id).Scan(&state, &platformStatus, &version); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT payload->>'state',resource_version FROM public.learning_outbox WHERE resource_id=$1 AND topic='task.observation_updated' ORDER BY resource_version DESC LIMIT 1`, id).Scan(&eventState, &eventVersion); err != nil {
		t.Fatal(err)
	}
	if state != StateRunning || platformStatus == CheckConfirmed || eventState != state || eventVersion != version {
		t.Fatalf("观察替代确认或事件状态错误：%s/%s/%s/%d/%d", state, platformStatus, eventState, version, eventVersion)
	}
	stale := job
	stale.FenceToken++
	if err := service.recordExecutionObservation(ctx, stale, Observation{Fraction: &half}); err == nil {
		t.Fatal("旧租约覆盖了观察")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.learning_tasks SET desired_state='pause' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := service.recordExecutionObservation(ctx, job, Observation{}); err == nil {
		t.Fatal("暂停后仍更新运行状态")
	}
}

type emptyRecoveringAnswers struct{ calls int }

func (a *emptyRecoveringAnswers) AnswerWorkQuestion(_ context.Context, _ string, key string, _ QuestionPolicy, _ WorkQuestion) (WorkQueryResult, error) {
	if !strings.Contains(key, "/recovery/1") {
		return WorkQueryResult{}, &Failure{Class: FailureAnswerUnavailable, Code: "answer_empty", Detail: "原结果为空"}
	}
	a.calls++
	return WorkQueryResult{QueryID: newID(), Answer: "A"}, nil
}
func TestEmptyWorkAnswerRequiresExplicitRecoveryAndDoesNotReplaySubmission(t *testing.T) {
	_, executor, platform, _, job, _ := setupWorkTask(t)
	answers := &emptyRecoveringAnswers{}
	executor.opts.Answerer = answers
	req := workRequest(job)
	_, err := executor.Execute(context.Background(), req)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Code != "answer_empty" || answers.calls != 0 || platform.posts != 0 {
		t.Fatalf("自动重发了历史空答案：%v", err)
	}
	req.AnswerRetryGeneration = 1
	result, err := executor.Execute(context.Background(), req)
	if err != nil || !result.Completed || answers.calls != 2 || platform.posts != 1 {
		t.Fatalf("显式重查没有恢复答案：%+v/%v/%d/%d", result, err, answers.calls, platform.posts)
	}
	if _, err = executor.Execute(context.Background(), req); err != nil || platform.posts != 1 || answers.calls != 2 {
		t.Fatalf("重放了最终提交：%v/%d/%d", err, answers.calls, platform.posts)
	}
}
