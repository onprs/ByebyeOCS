//go:build integration

package learning

import (
	"context"
	"fmt"
	"testing"
)

func TestRunningTaskDisappearanceRequestsStopAndPreservesItsHold(t *testing.T) {
	for _, reappeared := range []bool{false, true} {
		t.Run(fmt.Sprintf("重现_%t", reappeared), func(t *testing.T) {
			pool := testPool(t)
			platform := defaultFakePlatform()
			service := newVideoLearningService(t, pool, platform)
			user := seedUser(t, pool)
			bindTestAccount(t, service, user)
			syncAll(t, service, user)
			createVideoPlan(t, service, user, courseIDForUser(t, pool, user))
			job := claimOne(t, service)
			if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks
				SET state='running', billing_state='reserved', billing_hold_id='fixture-running-hold',
				billing_quote='{"configured":true,"amount_minor":125,"state":"quoted"}' WHERE id=$1`, job.TaskID); err != nil {
				t.Fatal(err)
			}
			platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001"))
			syncAll(t, service, user)
			var cancelled bool
			var code string
			if err := pool.QueryRow(context.Background(), `SELECT a.cancel_requested, t.last_error_code
				FROM public.learning_task_attempts a JOIN public.learning_tasks t ON t.id=a.task_id
				WHERE a.id=$1`, job.AttemptID).Scan(&cancelled, &code); err != nil {
				t.Fatal(err)
			}
			if !cancelled || code != snapshotMissingCode {
				t.Fatalf("快照消失未触发在途停止握手: %v/%s", cancelled, code)
			}
			if reappeared {
				platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001", videoAttachment("video-1", "9001")))
				syncAll(t, service, user)
			}
			if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{StopAcknowledged: true},
				Fail(FailureStopped, "夹具确认停止"), service.opts.Executor.Capabilities()); err != nil {
				t.Fatal(err)
			}
			state, desired, _, _, _, _ := taskRow(t, pool, job.TaskID)
			expected := StateNeedsAttention
			if reappeared {
				expected = StatePending
			}
			var billing, hold string
			if err := pool.QueryRow(context.Background(), `SELECT billing_state,billing_hold_id FROM public.learning_tasks
				WHERE id=$1`, job.TaskID).Scan(&billing, &hold); err != nil {
				t.Fatal(err)
			}
			if state != expected || desired != DesiredRun || billing != "reserved" || hold != "fixture-running-hold" {
				t.Fatalf("停止后状态或资金事实错误: %s/%s/%s/%s", state, desired, billing, hold)
			}
		})
	}
}

func TestRunningQuizDisappearanceAfterFinalPostOnlyVerifies(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	result, err := executor.Execute(context.Background(), workRequest(job))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Completed {
		t.Fatal("夹具未完成最终提交")
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = nil
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, job.UserID)
	if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{StopAcknowledged: true},
		Fail(FailureStopped, "夹具提交后确认停止"), executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	state, _, _, _, _, _ := taskRow(t, service.pool, job.TaskID)
	var reason string
	if err := service.pool.QueryRow(context.Background(), `SELECT verification_reason FROM public.learning_tasks WHERE id=$1`,
		job.TaskID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if state != StateWaitingVerification || reason != "question_submission" || platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("最终提交后的消失任务未保持只核验: %s/%s/posts=%d/calls=%d", state, reason, platform.posts, answers.modelCalls)
	}
}
