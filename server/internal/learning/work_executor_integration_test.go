//go:build integration

package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

type workTaskPlatform struct {
	*fakePlatform
	sheet        WorkSheet
	reads, posts int
	unknown      bool
	beforePost   func()
}

func (p *workTaskPlatform) ReadWork(context.Context, []byte, PlatformTaskRef) (WorkSheet, error) {
	p.reads++
	return p.sheet, nil
}
func (p *workTaskPlatform) SubmitWork(ctx context.Context, _ []byte, _ PlatformTaskRef, _ string, _ []WorkAnswer, beforePost func(context.Context) error) (WorkSubmission, error) {
	if p.beforePost != nil {
		p.beforePost()
	}
	if err := beforePost(ctx); err != nil {
		return WorkSubmission{}, err
	}
	p.posts++
	if p.unknown {
		return WorkSubmission{}, Fail(FailureSubmissionUnknown, "夹具模拟提交结果未知")
	}
	return WorkSubmission{Accepted: true}, nil
}

type retainedWorkAnswers struct {
	results    map[string]WorkQueryResult
	modelCalls int
	failSecond bool
}

func (a *retainedWorkAnswers) AnswerWorkQuestion(_ context.Context, _, key string, _ QuestionPolicy, question WorkQuestion) (WorkQueryResult, error) {
	if result, ok := a.results[key]; ok {
		return result, nil
	}
	if a.failSecond && question.ID == "2" {
		a.failSecond = false
		return WorkQueryResult{}, Fail(FailureTransient, "夹具在第二题前中断")
	}
	a.modelCalls++
	hash := sha256.Sum256([]byte(key))
	result := WorkQueryResult{QueryID: "qq_" + hex.EncodeToString(hash[:16]), Answer: "A"}
	a.results[key] = result
	return result, nil
}

func setupWorkTask(t *testing.T) (*Service, *AutomaticExecutor, *workTaskPlatform, *retainedWorkAnswers, executionJob, PlanView) {
	t.Helper()
	pool := testPool(t)
	platform := &workTaskPlatform{fakePlatform: defaultFakePlatform()}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = []PlatformAttachment{{ObjectID: "work-1", TaskPointID: "work-1", Kind: "quiz", PassedKnown: true}}
	platform.snapshots["101/202"] = snapshot
	hash := sha256.Sum256([]byte("isolated-work-task-v1"))
	options := []WorkOption{{Label: "A", Value: "platform-a", Text: "第一项"}, {Label: "B", Value: "platform-b", Text: "第二项"}}
	platform.sheet = WorkSheet{Fingerprint: hex.EncodeToString(hash[:]), Questions: []WorkQuestion{
		{ID: "1", Type: "single", Title: "第一题", Options: options},
		{ID: "2", Type: "single", Title: "第二题", Options: options},
	}}
	answers := &retainedWorkAnswers{results: map[string]WorkQueryResult{}}
	executor, err := NewAutomaticExecutor(pool, AutomaticExecutorOptions{Enabled: true, Platform: platform, Answerer: answers})
	if err != nil {
		t.Fatal(err)
	}
	opts := fastOptions(platform, executor)
	opts.RecoveryRetry = true
	opts.QuestionsPolicyValidator = &fakePolicyValidator{allowed: "qm_allowed"}
	service, err := New(pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	plan, err := service.CreatePlan(context.Background(), Actor{ID: userID, Kind: "user"}, PlanInput{
		Name: "自动测验", CourseIDs: []string{courseIDForUser(t, pool, userID)}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"quiz"}, QuestionPolicy: QuestionPolicyInput{Enabled: true, ModelOfferingID: ptrString("qm_allowed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, executor, platform, answers, *claimOnePointer(t, service), plan
}

func claimOnePointer(t *testing.T, service *Service) *executionJob {
	t.Helper()
	job, err := service.claimNext(context.Background())
	if err != nil || job == nil {
		t.Fatalf("无法领取测验任务: %v", err)
	}
	return job
}

func workRequest(job executionJob) ExecutionRequest {
	return ExecutionRequest{AttemptID: job.AttemptID, TaskID: job.TaskID, UserID: job.UserID, TaskKind: job.TaskKind,
		FenceToken: job.FenceToken, PlanRevision: job.PlanRevisionID, Session: []byte("test-session"), QuestionPolicy: job.QuestionPolicy,
		Task: PlatformTaskRef{Course: PlatformCourseRef{ExternalCourseID: job.CourseExternal, ExternalClassID: job.ClassExternal,
			CPI: job.CPI, CycleKey: job.CycleKey}, ChapterExternalID: job.ChapterExternal, ObjectID: job.ObjectID, TaskPointID: job.TaskPointID}}
}

func requireWorkFailure(t *testing.T, err error, class FailureClass) {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Class != class {
		t.Fatalf("执行错误分类不符: %v，期望 %s", err, class)
	}
}

func TestAutomaticWorkReusesQueriesAfterInterruptedAttempt(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	answers.failSecond = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureTransient)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(context.Background(), `UPDATE public.learning_tasks SET next_attempt_at=now() WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	resumed := claimOnePointer(t, service)
	restarted, err := NewAutomaticExecutor(service.pool, executor.opts)
	if err != nil {
		t.Fatal(err)
	}
	result, err = restarted.Execute(context.Background(), workRequest(*resumed))
	if err != nil || !result.Completed || answers.modelCalls != 2 || platform.posts != 1 {
		t.Fatalf("恢复重复查询或未完成: completed=%t calls=%d posts=%d err=%v", result.Completed, answers.modelCalls, platform.posts, err)
	}
	var runs, queries int
	if err := service.pool.QueryRow(context.Background(), `SELECT count(*),max(cardinality(query_ids)) FROM public.learning_work_runs WHERE task_id=$1`, job.TaskID).Scan(&runs, &queries); err != nil || runs != 1 || queries != 2 {
		t.Fatal("恢复没有复用持久运行记录", runs, queries, err)
	}
}

func TestAutomaticWorkChecksLeaseAndClosureBeforeReading(t *testing.T) {
	for _, scenario := range []string{"lease", "closed", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			service, executor, platform, answers, job, _ := setupWorkTask(t)
			var err error
			switch scenario {
			case "lease":
				_, err = service.pool.Exec(context.Background(), `UPDATE public.learning_task_attempts SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, job.AttemptID)
			case "closed":
				_, err = service.pool.Exec(context.Background(), `UPDATE public.users SET status='closed' WHERE id=$1`, job.UserID)
			case "cancel":
				_, err = service.pool.Exec(context.Background(), `UPDATE public.learning_task_attempts SET cancel_requested=true WHERE id=$1`, job.AttemptID)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = executor.Execute(context.Background(), workRequest(job))
			requireWorkFailure(t, err, FailureStopped)
			if platform.reads != 0 || platform.posts != 0 || answers.modelCalls != 0 {
				t.Fatal("授权结束后仍发起外部请求")
			}
		})
	}
}

func TestAutomaticWorkRechecksFenceImmediatelyBeforePost(t *testing.T) {
	service, executor, platform, _, job, _ := setupWorkTask(t)
	platform.beforePost = func() {
		if _, err := service.pool.Exec(context.Background(), `UPDATE public.learning_tasks SET fence_token=fence_token+1 WHERE id=$1`, job.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	_, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureStopped)
	var state string
	if err := service.pool.QueryRow(context.Background(), `SELECT state FROM public.learning_work_runs WHERE task_id=$1`, job.TaskID).Scan(&state); err != nil || state != "querying" || platform.posts != 0 {
		t.Fatal("最终提交前失去租约仍提交或过早写入提交标记", state, platform.posts, err)
	}
}

func TestAutomaticWorkUnknownSubmissionCannotReplayAfterPlanRevision(t *testing.T) {
	service, executor, platform, answers, job, plan := setupWorkTask(t)
	platform.unknown = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdatePlan(context.Background(), Actor{ID: job.UserID, Kind: "user"}, plan.ID, plan.Version, PlanInput{
		Name: "修订后的自动测验", CourseIDs: plan.Scope.CourseIDs, Scope: PlanScopeInput{Kind: "all"}, TaskTypes: []string{"quiz"},
		QuestionPolicy: QuestionPolicyInput{Enabled: true, ModelOfferingID: ptrString("qm_allowed"), AnswerMode: "always_llm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 模拟重启后重新调度；即使当前安排版本不同，任务级提交事实仍必须挡住重放。
	if _, err := service.pool.Exec(context.Background(), `UPDATE public.learning_tasks SET state='pending',next_attempt_at=NULL,next_verify_at=NULL WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	resumed := claimOnePointer(t, service)
	_, err = executor.Execute(context.Background(), workRequest(*resumed))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatal("提交结果未知时因新安排版本重复做题或提交", platform.posts, answers.modelCalls)
	}
}
