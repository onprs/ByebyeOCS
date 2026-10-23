//go:build integration

package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
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

// TestQuizUnknownSubmissionNotReplayedByTaskControl 验证提交结果未知后，用户暂停、
// 恢复、重新调度以及提交落库中断都只核验平台事实，不会再次查询答案或发送最终提交。
func TestQuizUnknownSubmissionNotReplayedByTaskControl(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	platform.unknown = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	actor := Actor{ID: job.UserID, Kind: "user"}
	if _, err := service.ControlTask(context.Background(), actor, job.TaskID, "pause-quiz-0001", "pause", "暂停"); err != nil {
		t.Fatal(err)
	}
	state, desired, _, _, _, _ := taskRow(t, service.pool, job.TaskID)
	if state != StatePaused || desired != DesiredPause {
		t.Fatalf("提交未知任务暂停状态错误: %s/%s", state, desired)
	}
	if _, err := service.ControlTask(context.Background(), actor, job.TaskID, "resume-quiz-0001", "resume", "继续"); err != nil {
		t.Fatal(err)
	}
	// 控制入口直接回到核验，不再新领一次执行后由执行器拒绝重放。
	state, desired, _, _, _, _ = taskRow(t, service.pool, job.TaskID)
	if state != StateWaitingVerification || desired != DesiredRun {
		t.Fatalf("提交未知任务恢复后没有直接核验: %s/%s", state, desired)
	}
	if resumed, err := service.claimNext(context.Background()); err != nil || resumed != nil {
		t.Fatalf("暂停恢复产生新的执行尝试: %#v/%v", resumed, err)
	}
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("暂停恢复导致重复查询/提交: posts=%d calls=%d", platform.posts, answers.modelCalls)
	}
	// 模拟进程在最终提交状态落库前崩溃：持久提交记录仍为 submitting。
	if _, err := service.pool.Exec(context.Background(), `UPDATE public.learning_work_runs
		SET state='submitting', submitted_at=NULL WHERE task_id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlTask(context.Background(), actor, job.TaskID, "redispatch-quiz-0001", "redispatch", "重新调度"); err != nil {
		t.Fatal(err)
	}
	if claimed, err := service.claimNext(context.Background()); err != nil || claimed != nil {
		t.Fatalf("提交崩溃恢复产生新执行尝试: %#v/%v", claimed, err)
	}
	state, _, _, _, _, _ = taskRow(t, service.pool, job.TaskID)
	if state != StateWaitingVerification {
		t.Fatalf("提交崩溃恢复未直接核验: %s", state)
	}
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("崩溃恢复重放了题库查询或最终提交: posts=%d calls=%d", platform.posts, answers.modelCalls)
	}
	var runs int
	if err := service.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_work_runs
		WHERE task_id=$1`, job.TaskID).Scan(&runs); err != nil || runs != 1 {
		t.Fatalf("重复提交事实不唯一: %d %v", runs, err)
	}
}

// TestQuizUnconfirmedSubmissionStopsAfterVerificationLimit 验证提交未知的测验只做
// 有界平台核验，达到上限后转人工，不会重新执行、重新查询或再次提交。
func TestQuizUnconfirmedSubmissionStopsAfterVerificationLimit(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	platform.unknown = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	current := time.Now().UTC().Add(time.Second)
	service.opts.Now = func() time.Time { return current }
	verifierCalls := 0
	verifier := verifierFunc(func(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
		verifierCalls++
		return PlatformCheck{Status: CheckUnconfirmed, CheckedAt: current, Source: "fake-check"}, nil
	})
	for attempt := 1; attempt <= service.opts.MaxVerifications; attempt++ {
		current = current.Add(5 * time.Minute)
		taskID, userID, err := service.claimVerification(context.Background())
		if err != nil || taskID != job.TaskID || userID != job.UserID {
			t.Fatalf("第 %d 次核验领取失败: %s %s %v", attempt, taskID, userID, err)
		}
		var attempts int
		if err := service.pool.QueryRow(context.Background(), `SELECT verification_attempts
			FROM public.learning_tasks WHERE id=$1`, job.TaskID).Scan(&attempts); err != nil || attempts != attempt {
			t.Fatalf("核验次数错误: %d %v，期望 %d", attempts, err, attempt)
		}
		service.runVerification(context.Background(), verifier, taskID, userID)
		state, _, platformStatus, _, _, _ := taskRow(t, service.pool, job.TaskID)
		if attempt < service.opts.MaxVerifications {
			if state != StateWaitingVerification || platformStatus != CheckUnconfirmed {
				t.Fatalf("第 %d 次未确认后的状态错误: %s/%s", attempt, state, platformStatus)
			}
			continue
		}
		if state != StateNeedsAttention || platformStatus != CheckUnconfirmed {
			t.Fatalf("核验上限后未转人工: %s/%s", state, platformStatus)
		}
	}
	if verifierCalls != service.opts.MaxVerifications {
		t.Fatalf("核验次数超出上限: %d", verifierCalls)
	}
	var errorCode string
	var nextVerify *time.Time
	var lastReason string
	if err := service.pool.QueryRow(context.Background(), `SELECT last_error_code, next_verify_at,
		verification_reason FROM public.learning_tasks WHERE id=$1`, job.TaskID).
		Scan(&errorCode, &nextVerify, &lastReason); err != nil {
		t.Fatal(err)
	}
	if errorCode != "submission_unconfirmed" || nextVerify != nil {
		t.Fatalf("核验上限异常事实错误: %s %v", errorCode, nextVerify)
	}
	var attempts int
	if err := service.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_task_attempts
		WHERE task_id=$1`, job.TaskID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("提交未知后重新执行了测验: %d %v", attempts, err)
	}
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("核验上限过程中重复查询或提交: posts=%d calls=%d", platform.posts, answers.modelCalls)
	}
}

// TestQuizUsesEnabledPolicyFromOverlappingPlan 验证同一测验任务被多个安排引用时，
// 高优先级但未启用题库的安排不会阻止另一有效安排自动完成该任务。
func TestQuizUsesEnabledPolicyFromOverlappingPlan(t *testing.T) {
	pool := testPool(t)
	platform := &workTaskPlatform{fakePlatform: defaultFakePlatform()}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = []PlatformAttachment{{ObjectID: "work-1", TaskPointID: "work-1", Kind: "quiz", PassedKnown: true}}
	platform.snapshots["101/202"] = snapshot
	hash := sha256.Sum256([]byte("overlapping-plan-v1"))
	platform.sheet = WorkSheet{Fingerprint: hex.EncodeToString(hash[:]), Questions: []WorkQuestion{
		{ID: "1", Type: "single", Title: "第一题", Options: []WorkOption{
			{Label: "A", Value: "platform-a", Text: "第一项"}, {Label: "B", Value: "platform-b", Text: "第二项"}}},
	}}
	answers := &retainedWorkAnswers{results: map[string]WorkQueryResult{}}
	executor, err := NewAutomaticExecutor(pool, AutomaticExecutorOptions{Enabled: true, Platform: platform, Answerer: answers})
	if err != nil {
		t.Fatal(err)
	}
	opts := fastOptions(platform, executor)
	opts.QuestionsPolicyValidator = &fakePolicyValidator{allowed: "qm_allowed"}
	service, err := New(pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}
	if _, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "高优先级未启用题库", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"quiz"}, Priority: 90,
	}); err != nil {
		t.Fatal(err)
	}
	enabled, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "低优先级启用题库", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"quiz"}, Priority: 10,
		QuestionPolicy: QuestionPolicyInput{Enabled: true, ModelOfferingID: ptrString("qm_allowed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	job := claimOnePointer(t, service)
	if job.PlanRevisionID != enabled.CurrentRevisionID || !job.QuestionPolicy.Enabled {
		t.Fatalf("测验任务未使用已启用题库的安排: %+v", job)
	}
}
