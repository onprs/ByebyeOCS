//go:build integration

package learning

import (
	"context"
	"testing"
	"time"
)

func TestOlderIdentityBindingPreservesNewerIdentityBlock(t *testing.T) {
	pool := testPool(t)
	service := newVideoLearningService(t, pool, defaultFakePlatform())
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='identity_invalid',blocked_at=now() WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	if err := service.BindAccount(ctx, user, AccountIdentity{
		Provider: "chaoxing", Namespace: "chaoxing-test", Subject: "subject-" + user, VerifiedAt: time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	var blocked string
	if err := pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, user).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if blocked != "identity_invalid" {
		t.Fatal("旧验证结果解除新发生的会话失效限制")
	}
}

func TestResumeActiveTaskDoesNotCancelItsAttempt(t *testing.T) {
	pool := testPool(t)
	service, user, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	result, err := service.ControlTask(context.Background(), Actor{ID: user, Kind: "user"}, taskID, "resume-already-active", "resume", "")
	if err != nil {
		t.Fatal(err)
	}
	var cancelled bool
	if err := pool.QueryRow(context.Background(), `SELECT cancel_requested FROM public.learning_task_attempts WHERE id=$1`, job.AttemptID).Scan(&cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled || result.DesiredState != DesiredRun || result.State != StatePreparing {
		t.Fatalf("继续操作取消正常在途任务：%#v cancelled=%v", result, cancelled)
	}
}

func TestResumeBeforeStopAcknowledgementReturnsTaskToRun(t *testing.T) {
	pool := testPool(t)
	service, user, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	actor := Actor{ID: user, Kind: "user"}
	if _, err := service.ControlTask(context.Background(), actor, taskID, "pause-before-ack", "pause", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlTask(context.Background(), actor, taskID, "resume-before-ack", "resume", ""); err != nil {
		t.Fatal(err)
	}
	if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{StopAcknowledged: true}, nil, service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	state, desired, _, _, _, _ := taskRow(t, pool, taskID)
	if state != StatePending || desired != DesiredRun {
		t.Fatalf("继续操作被迟到的停止确认覆盖：%s/%s", state, desired)
	}
	if next, err := service.claimNext(context.Background()); err != nil || next == nil || next.TaskID != taskID {
		t.Fatalf("继续后无法领取任务：%#v/%v", next, err)
	}
}

func TestRedispatchSubmittedQuizOnlyReturnsToVerification(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	platform.unknown = true
	ctx := context.Background()
	result, err := executor.Execute(ctx, workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(ctx, job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_tasks SET state='needs_attention' WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	controlled, err := service.ControlTask(ctx, Actor{ID: job.UserID, Kind: "user"}, job.TaskID, "redispatch-submitted-quiz", "redispatch", "")
	if err != nil || controlled.State != StateWaitingVerification {
		t.Fatalf("已提交测验未直接回到核验：%#v/%v", controlled, err)
	}
	if next, err := service.claimNext(ctx); err != nil || next != nil {
		t.Fatalf("已提交测验被再次领取执行：%#v/%v", next, err)
	}
	detail, err := service.GetTask(ctx, Actor{ID: job.UserID, Kind: "user"}, job.TaskID)
	if err != nil || detail.Version != controlled.Version || platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("核验恢复版本或外部调用不符：version=%d/%d posts=%d calls=%d err=%v", detail.Version, controlled.Version, platform.posts, answers.modelCalls, err)
	}
}
