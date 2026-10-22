//go:build integration

package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newVideoLearningService 构造只执行视频任务的学习服务，用于调度回归测试。
func newVideoLearningService(t *testing.T, pool *pgxpool.Pool, platform Platform) *Service {
	t.Helper()
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	service, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func chapterIDByExternal(t *testing.T, service *Service, userID, courseID, externalID string) string {
	t.Helper()
	detail, err := service.GetCourse(context.Background(), Actor{ID: userID, Kind: "user"}, courseID)
	if err != nil {
		t.Fatal(err)
	}
	for _, chapter := range detail.Chapters {
		if chapter.ExternalID == externalID {
			return chapter.ID
		}
	}
	t.Fatalf("课程快照缺少章节 %s", externalID)
	return ""
}

func currentRevisionBindings(t *testing.T, pool *pgxpool.Pool, revisionID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_plan_task_bindings
		WHERE plan_revision_id = $1`, revisionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// addVideoTaskToSnapshot 在既有章节中追加一个视频任务点，模拟重新同步后的新任务。
func addVideoTaskToSnapshot(platform *fakePlatform, objectID, taskPointID string) {
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments,
		PlatformAttachment{ObjectID: objectID, TaskPointID: taskPointID, Kind: "video",
			Title: "新增视频任务", DurationSeconds: 90, Passed: false, PassedKnown: true})
	platform.snapshots["101/202"] = snapshot
}

// TestSelectedScopeSchedulesNewTaskAfterCourseResync 验证重新同步不会因为章节行重建
// 而让“选定章节”安排遗漏新发现的任务。
func TestSelectedScopeSchedulesNewTaskAfterCourseResync(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	chapterID := chapterIDByExternal(t, service, userID, courseID, "5001")
	actor := Actor{ID: userID, Kind: "user"}
	plan, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "选定章节安排", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterID}}},
		TaskTypes: []string{"video"}, Priority: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 1 {
		t.Fatalf("首次同步后应有 1 个任务绑定，实际 %d", got)
	}

	addVideoTaskToSnapshot(platform, "video-2", "9002")
	syncAll(t, service, userID)

	if got := len(taskIDsForUser(t, pool, userID)); got != 2 {
		t.Fatalf("重新同步后任务数错误: %d", got)
	}
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 2 {
		t.Fatalf("重新同步后新任务未绑定当前安排版本: %d", got)
	}
	seen := map[string]bool{}
	for range 2 {
		job, err := service.claimNext(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			t.Fatal("重新同步后仍有任务无法领取")
		}
		if seen[job.TaskID] {
			t.Fatalf("同一任务被重复领取: %s", job.TaskID)
		}
		seen[job.TaskID] = true
	}

	// 安排视图应返回当前快照中同一外部章节的标识，而不是旧快照行。
	currentChapterID := chapterIDByExternal(t, service, userID, courseID, "5001")
	got, err := service.GetPlan(context.Background(), actor, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Scope.Chapters) != 1 || got.Scope.Chapters[0].ChapterID != currentChapterID {
		t.Fatalf("安排范围未指向当前章节: %+v，当前 %s", got.Scope.Chapters, currentChapterID)
	}
}

// TestPlanResumeBindsTasksDiscoveredWhilePaused 验证暂停期间同步发现的新任务
// 会在恢复安排后进入调度，而不是必须再等一次课程同步。
func TestPlanResumeBindsTasksDiscoveredWhilePaused(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	plan := createVideoPlan(t, service, userID, courseID)
	actor := Actor{ID: userID, Kind: "user"}
	if _, err := service.ControlPlan(context.Background(), actor, plan.ID, "pause-plan-0001", "pause", "暂停安排"); err != nil {
		t.Fatal(err)
	}
	addVideoTaskToSnapshot(platform, "video-2", "9002")
	syncAll(t, service, userID)
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 1 {
		t.Fatalf("暂停期间不应为当前版本新增绑定: %d", got)
	}
	if job, err := service.claimNext(context.Background()); err != nil || job != nil {
		t.Fatalf("暂停安排仍领取了任务: %+v %v", job, err)
	}
	if _, err := service.ControlPlan(context.Background(), actor, plan.ID, "resume-plan-0001", "resume", "恢复安排"); err != nil {
		t.Fatal(err)
	}
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 2 {
		t.Fatalf("恢复安排后未绑定暂停期间发现的新任务: %d", got)
	}
	seen := map[string]bool{}
	for range 2 {
		job, err := service.claimNext(context.Background())
		if err != nil || job == nil {
			t.Fatalf("恢复安排后任务无法领取: %+v %v", job, err)
		}
		seen[job.TaskID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("恢复安排后领取任务数量错误: %d", len(seen))
	}
}

// TestClaimRevalidatesPlanTaskKindAfterResync 验证重新同步更新任务类型后，
// 旧安排版本残留的历史绑定不会让当前版本已排除的任务继续进入执行。
func TestClaimRevalidatesPlanTaskKindAfterResync(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video", "document"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	service, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	chapterID := chapterIDByExternal(t, service, userID, courseID, "5001")
	actor := Actor{ID: userID, Kind: "user"}
	plan, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "仅视频安排", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterID}}},
		TaskTypes: []string{"video"}, Priority: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 1 {
		t.Fatalf("首次同步后应有 1 个任务绑定，实际 %d", got)
	}
	// 同一任务点被重新识别为普通文档，当前安排版本不再包含该类型。
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments[0].Kind = "document"
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	var kind string
	if err := pool.QueryRow(context.Background(), `SELECT task_kind FROM public.learning_tasks
		WHERE user_id = $1`, userID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind != "document" {
		t.Fatalf("任务类型未随快照更新: %s", kind)
	}
	if job, err := service.claimNext(context.Background()); err != nil || job != nil {
		t.Fatalf("当前安排已排除的任务仍被领取: %+v %v", job, err)
	}
}

// TestCardIndexPropagatesToExecutionAndVerification 验证非零卡片页码从课程快照
// 持久化到附件，并在执行前核验、执行器和独立核验中使用同一张卡片。
func TestCardIndexPropagatesToExecutionAndVerification(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments[0].CardIndex = 2
	platform.snapshots["101/202"] = snapshot
	var executed []ExecutionRequest
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		executed = append(executed, request)
		return ExecutionResult{Completed: true}, nil
	}}
	var checked []PlatformTaskRef
	options := fastOptions(platform, executor)
	options.Verifier = verifierFunc(func(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
		checked = append(checked, request.Task)
		return PlatformCheck{Status: CheckUnconfirmed, CheckedAt: time.Now().UTC(), Source: "fake-check"}, nil
	})
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	createVideoPlan(t, service, userID, courseID)
	taskID := taskIDsForUser(t, pool, userID)[0]
	job := claimOne(t, service)
	if job.CardIndex != 2 {
		t.Fatalf("领取任务未读取附件卡片页码: %d", job.CardIndex)
	}
	service.execute(context.Background(), job)
	if len(executed) != 1 || executed[0].Task.CardIndex != 2 {
		t.Fatalf("执行器未收到卡片页码: %+v", executed)
	}
	if len(checked) != 1 || checked[0].CardIndex != 2 {
		t.Fatalf("执行前核验未收到卡片页码: %+v", checked)
	}
	service.runVerification(context.Background(), options.Verifier, taskID, userID)
	if len(checked) != 2 || checked[1].CardIndex != 2 {
		t.Fatalf("独立核验未收到卡片页码: %+v", checked)
	}
	var stored int
	if err := pool.QueryRow(context.Background(), `SELECT card_index FROM public.learning_attachments
		WHERE user_id = $1 AND external_object_id = 'video-1'`, userID).Scan(&stored); err != nil || stored != 2 {
		t.Fatalf("卡片页码未持久化: %d %v", stored, err)
	}
}

// billingTaskFacts 读取任务持久化的计费状态、预占和冻结报价。
func billingTaskFacts(t *testing.T, pool *pgxpool.Pool, taskID string) (string, string, string, BillingQuote) {
	t.Helper()
	var state, hold, price string
	var raw []byte
	if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_hold_id,
		COALESCE(price_version_id::text, ''), billing_quote FROM public.learning_tasks
		WHERE id=$1`, taskID).Scan(&state, &hold, &price, &raw); err != nil {
		t.Fatal(err)
	}
	var quote BillingQuote
	if err := json.Unmarshal(raw, &quote); err != nil {
		t.Fatal(err)
	}
	return state, hold, price, quote
}

// TestRetryKeepsFrozenBillingQuote 验证任务重试沿用首次领取时冻结的报价与预占，
// 不会因为改价或关闭收费而重新报出不同金额、丢失预占或按新价结算。
func TestRetryKeepsFrozenBillingQuote(t *testing.T) {
	cases := []struct {
		name  string
		quote BillingQuote
	}{
		{"关闭收费", BillingQuote{Configured: false, State: "disabled", PriceVersion: testPriceVersion}},
		{"提高价格", BillingQuote{Configured: true, State: "quoted", AmountMinor: 900,
			Currency: "CNY", PriceVersion: testPriceVersion}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pool := testPool(t)
			setupBillingMarker(t, pool)
			billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
				AmountMinor: 500, Currency: "CNY", PriceVersion: testPriceVersion}}
			recorder := &recordingConfirmation{}
			service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
			first := claimOne(t, service)
			if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "reserved" ||
				hold != "hold-state-1" || price != testPriceVersion || quote.AmountMinor != 500 {
				t.Fatalf("首次领取未冻结报价与预占: %s/%s/%s/%+v", state, hold, price, quote)
			}
			if err := service.applyExecutionResult(context.Background(), first, ExecutionResult{},
				Fail(FailureTransient, "夹具模拟临时失败"), service.opts.Executor.Capabilities()); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks
				SET next_attempt_at=now() WHERE id=$1`, taskID); err != nil {
				t.Fatal(err)
			}
			billing.quote = test.quote
			second := claimOne(t, service)
			if billing.quoteCalls != 1 {
				t.Fatalf("重试重新报价: %d", billing.quoteCalls)
			}
			if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "reserved" ||
				hold != "hold-state-1" || price != testPriceVersion || !quote.Configured || quote.AmountMinor != 500 {
				t.Fatalf("重试覆盖了冻结计费事实: %s/%s/%s/%+v", state, hold, price, quote)
			}
			waitingTask(t, service, taskID, second)
			check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
			if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
				t.Fatal(err)
			}
			if recorder.calls != 1 || billing.settleCalls != 1 ||
				len(billing.settleAmounts) != 1 || billing.settleAmounts[0] != 500 {
				t.Fatalf("确认未按冻结报价结算一次: recorder=%d settle=%d amounts=%v",
					recorder.calls, billing.settleCalls, billing.settleAmounts)
			}
		})
	}
}

// TestUnpricedTaskStaysUnpricedAfterRetry 验证首次领取时未定价的任务不会在
// 重试时因新价格发布而补收，保留原价格版本与待定价事实。
func TestUnpricedTaskStaysUnpricedAfterRetry(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: false, State: "unpriced",
		PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	first := claimOne(t, service)
	if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "unpriced" ||
		hold != "" || price != testPriceVersion || quote.Configured {
		t.Fatalf("未定价首次领取计费事实错误: %s/%s/%s/%+v", state, hold, price, quote)
	}
	if err := service.applyExecutionResult(context.Background(), first, ExecutionResult{},
		Fail(FailureTransient, "夹具模拟临时失败"), service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks
		SET next_attempt_at=now() WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	billing.quote = BillingQuote{Configured: true, State: "quoted", AmountMinor: 500,
		Currency: "CNY", PriceVersion: testPriceVersion}
	second := claimOne(t, service)
	if billing.quoteCalls != 1 || billing.reserveCalls != 0 {
		t.Fatalf("未定价任务重试重新报价或预占: quote=%d reserve=%d",
			billing.quoteCalls, billing.reserveCalls)
	}
	if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "unpriced" ||
		hold != "" || price != testPriceVersion || quote.Configured {
		t.Fatalf("未定价任务重试被改价: %s/%s/%s/%+v", state, hold, price, quote)
	}
	waitingTask(t, service, taskID, second)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 0 {
		t.Fatalf("未定价任务重试后被结算: recorder=%d settle=%d", recorder.calls, billing.settleCalls)
	}
}

// TestPausedTaskResumesWithSingleFrozenReservation 验证暂停再继续沿用同一预占，
// 不会在第二次领取时重新预占或建立第二笔收费事实。
func TestPausedTaskResumesWithSingleFrozenReservation(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, Currency: "CNY", PriceVersion: testPriceVersion}}
	recorder := &recordingConfirmation{}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	first := claimOne(t, service)
	actor := Actor{ID: first.UserID, Kind: "user"}
	if _, err := service.ControlTask(context.Background(), actor, taskID, "pause-billing-0001", "pause", "暂停"); err != nil {
		t.Fatal(err)
	}
	if err := service.applyExecutionResult(context.Background(), first,
		ExecutionResult{StopAcknowledged: true}, nil, service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	state, desired, _, _, _, _ := taskRow(t, pool, taskID)
	if state != StatePaused || desired != DesiredPause {
		t.Fatalf("暂停状态错误: %s/%s", state, desired)
	}
	billingState, hold, price, quote := billingTaskFacts(t, pool, taskID)
	if billingState != "review_required" || hold != "hold-state-1" ||
		price != testPriceVersion || quote.AmountMinor != 500 {
		t.Fatalf("暂停后预占事实错误: %s/%s/%s/%+v", billingState, hold, price, quote)
	}
	if _, err := service.ControlTask(context.Background(), actor, taskID, "resume-billing-0001", "resume", "继续"); err != nil {
		t.Fatal(err)
	}
	second := claimOne(t, service)
	if billing.quoteCalls != 1 || billing.reserveCalls != 1 {
		t.Fatalf("继续后重复报价或预占: quote=%d reserve=%d", billing.quoteCalls, billing.reserveCalls)
	}
	waitingTask(t, service, taskID, second)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 1 || billing.releaseCalls != 0 ||
		len(billing.settleAmounts) != 1 || billing.settleAmounts[0] != 500 {
		t.Fatalf("暂停继续导致重复收费: recorder=%d settle=%d release=%d amounts=%v",
			recorder.calls, billing.settleCalls, billing.releaseCalls, billing.settleAmounts)
	}
}

// TestAdminResumePlansBindsTasksDiscoveredWhilePaused 验证管理员恢复安排后
// 暂停期间同步发现的新任务会重新进入调度。
func TestAdminResumePlansBindsTasksDiscoveredWhilePaused(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	plan := createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	actor := adminActorForTest(t, pool)
	request := AccountOperationsRequest{UserIDs: []string{userID}, Action: "pause_plans", Reason: "运营暂停"}
	if _, err := service.AdminAccountOperations(context.Background(), actor, request, "admin-pause-0001"); err != nil {
		t.Fatal(err)
	}
	addVideoTaskToSnapshot(platform, "video-2", "9002")
	syncAll(t, service, userID)
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 1 {
		t.Fatalf("暂停期间不应新增当前版本绑定: %d", got)
	}
	resume := request
	resume.Action = "resume_plans"
	if _, err := service.AdminAccountOperations(context.Background(), actor, resume, "admin-resume-0001"); err != nil {
		t.Fatal(err)
	}
	if got := currentRevisionBindings(t, pool, plan.CurrentRevisionID); got != 2 {
		t.Fatalf("管理员恢复后未绑定新任务: %d", got)
	}
	seen := map[string]bool{}
	for range 2 {
		job, err := service.claimNext(context.Background())
		if err != nil || job == nil {
			t.Fatalf("管理员恢复后任务无法领取: %+v %v", job, err)
		}
		seen[job.TaskID] = true
	}
	if len(seen) != 2 {
		t.Fatalf("管理员恢复后领取任务数量错误: %d", len(seen))
	}
}

// TestClaimPolicyUsesCurrentScopeNotStaleBinding 验证实际领取所用的安排版本
// 与候选筛选使用同一当前范围；历史绑定不能把已排除任务的高优先级安排带回来。
func TestClaimPolicyUsesCurrentScopeNotStaleBinding(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video", "document"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	service, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}
	// 高优先级安排只包含视频，稍后任务被重新识别为文档后不再属于该范围。
	if _, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "高优先级仅视频", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"video"}, Priority: 90,
	}); err != nil {
		t.Fatal(err)
	}
	valid, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "低优先级视频文档", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"video", "document"}, Priority: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments[0].Kind = "document"
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	job := claimOne(t, service)
	if job.PlanRevisionID != valid.CurrentRevisionID {
		t.Fatalf("领取使用了陈旧绑定对应的安排版本: %s", job.PlanRevisionID)
	}
}

// TestTaskClaimedWithoutBillingStaysDisabledAfterWiring 验证未接线账本时启动的任务
// 会冻结为未启用扣费；之后接线账本重试也不会按新配置追溯报价或预占。
func TestTaskClaimedWithoutBillingStaysDisabledAfterWiring(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	platform := defaultFakePlatform()
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	before, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, before, userID)
	syncAll(t, before, userID)
	courseID := courseIDForUser(t, pool, userID)
	createVideoPlan(t, before, userID, courseID)
	taskID := taskIDsForUser(t, pool, userID)[0]
	first := claimOne(t, before)
	if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "disabled" ||
		hold != "" || price != "" || quote.Configured || quote.State != "disabled" {
		t.Fatalf("未接线账本未冻结未启用扣费: %s/%s/%s/%+v", state, hold, price, quote)
	}
	if err := before.applyExecutionResult(context.Background(), first, ExecutionResult{},
		Fail(FailureTransient, "夹具模拟临时失败"), before.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks
		SET next_attempt_at=now() WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, Currency: "CNY", PriceVersion: testPriceVersion}}
	recorder := &recordingConfirmation{}
	options := fastOptions(platform, executor)
	options.Billing = billing
	options.BillingConfirmationRecorder = recorder
	after, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	second := claimOne(t, after)
	if billing.quoteCalls != 0 || billing.reserveCalls != 0 {
		t.Fatalf("接线账本后追溯报价或预占: quote=%d reserve=%d", billing.quoteCalls, billing.reserveCalls)
	}
	if state, hold, price, quote := billingTaskFacts(t, pool, taskID); state != "disabled" ||
		hold != "" || price != "" || quote.Configured || quote.State != "disabled" {
		t.Fatalf("接线账本后覆盖冻结状态: %s/%s/%s/%+v", state, hold, price, quote)
	}
	waitingTask(t, after, taskID, second)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := after.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 0 {
		t.Fatalf("未启用扣费任务被追溯结算: recorder=%d settle=%d", recorder.calls, billing.settleCalls)
	}
}

// snapshotWithChapters 构造当前测试课程的完整快照。
func snapshotWithChapters(chapters ...PlatformChapter) PlatformCourseSnapshot {
	return PlatformCourseSnapshot{
		Course:   PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", CycleKey: "current"},
		Title:    "测试课程",
		Adapter:  "fake",
		Chapters: chapters,
	}
}

func videoChapter(externalID string, attachments ...PlatformAttachment) PlatformChapter {
	return PlatformChapter{ExternalID: externalID, Title: "章节 " + externalID, Attachments: attachments}
}

func videoAttachment(objectID, taskPointID string) PlatformAttachment {
	return PlatformAttachment{ObjectID: objectID, TaskPointID: taskPointID, Kind: "video",
		Title: "视频 " + objectID, DurationSeconds: 120, PassedKnown: true}
}

// TestClaimOrdersByCurrentPlanPriority 验证同账号多任务按当前有效、范围匹配的
// 安排最高 priority 排序，而非从未写入的任务优先级；实际安排 priority 进入执行请求。
func TestClaimOrdersByCurrentPlanPriority(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5001", videoAttachment("video-1", "9001")),
		videoChapter("5002", videoAttachment("video-2", "9002")),
	)
	executed := make([]ExecutionRequest, 0, 1)
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		executed = append(executed, request)
		return ExecutionResult{Completed: true}, nil
	}}
	service, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}
	lowChapter := chapterIDByExternal(t, service, userID, courseID, "5001")
	highChapter := chapterIDByExternal(t, service, userID, courseID, "5002")
	if _, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "低优先级", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: lowChapter}}},
		TaskTypes: []string{"video"}, Priority: 5,
	}); err != nil {
		t.Fatal(err)
	}
	high, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "高优先级", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: highChapter}}},
		TaskTypes: []string{"video"}, Priority: 90,
	})
	if err != nil {
		t.Fatal(err)
	}
	first := claimOne(t, service)
	if first.TaskPointID != "9002" || first.Priority != 90 || first.PlanRevisionID != high.CurrentRevisionID {
		t.Fatalf("首次领取未按安排优先级选择高优先级任务: %+v", first)
	}
	service.execute(context.Background(), first)
	if len(executed) != 1 || executed[0].Priority != 90 {
		t.Fatalf("执行请求未携带实际安排优先级: %+v", executed)
	}
	second := claimOne(t, service)
	if second.TaskPointID != "9001" || second.Priority != 5 {
		t.Fatalf("第二次领取安排优先级错误: %+v", second)
	}
}

// TestClaimUsesSelectedPolicyPlanPriority 验证测验任务按最高有效安排优先级排序，
// 但 ExecutionRequest 收到的是实际选中的题库策略安排 priority。
func TestClaimUsesSelectedPolicyPlanPriority(t *testing.T) {
	pool := testPool(t)
	platform := &workTaskPlatform{fakePlatform: defaultFakePlatform()}
	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5002", PlatformAttachment{ObjectID: "work-b", TaskPointID: "9002", Kind: "quiz", PassedKnown: true}),
		videoChapter("5001", PlatformAttachment{ObjectID: "work-a", TaskPointID: "9001", Kind: "quiz", PassedKnown: true}),
	)
	hash := sha256.Sum256([]byte("priority-selected-policy-v1"))
	options := []WorkOption{{Label: "A", Value: "platform-a", Text: "第一项"}, {Label: "B", Value: "platform-b", Text: "第二项"}}
	platform.sheet = WorkSheet{Fingerprint: hex.EncodeToString(hash[:]),
		Questions: []WorkQuestion{{ID: "1", Type: "single", Title: "第一题", Options: options}}}
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
	chapterA := chapterIDByExternal(t, service, userID, courseID, "5001")
	chapterB := chapterIDByExternal(t, service, userID, courseID, "5002")
	if _, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "A 高优先级未启用题库", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterA}}},
		TaskTypes: []string{"quiz"}, Priority: 90,
	}); err != nil {
		t.Fatal(err)
	}
	enabledA, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "A 低优先级已启用题库", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterA}}},
		TaskTypes: []string{"quiz"}, Priority: 15,
		QuestionPolicy: QuestionPolicyInput{Enabled: true, ModelOfferingID: ptrString("qm_allowed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	enabledB, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "B 中优先级已启用题库", CourseIDs: []string{courseID},
		Scope:     PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterB}}},
		TaskTypes: []string{"quiz"}, Priority: 50,
		QuestionPolicy: QuestionPolicyInput{Enabled: true, ModelOfferingID: ptrString("qm_allowed")},
	})
	if err != nil {
		t.Fatal(err)
	}
	first := claimOnePointer(t, service)
	if first.TaskPointID != "9001" || first.PlanRevisionID != enabledA.CurrentRevisionID ||
		first.Priority != 15 || !first.QuestionPolicy.Enabled {
		t.Fatalf("首次测验领取未按最高有效安排排序或未选实际策略安排: %+v", first)
	}
	second := claimOnePointer(t, service)
	if second.TaskPointID != "9002" || second.PlanRevisionID != enabledB.CurrentRevisionID || second.Priority != 50 {
		t.Fatalf("第二次测验领取安排优先级错误: %+v", second)
	}
}

// TestMissingSnapshotTaskStopsClaimAndRestoresOnReappear 验证最新完整快照已不存在的
// 未运行任务不能再领取，但也不会静默停留在 Pending；重新出现后恢复待执行。
func TestMissingSnapshotTaskStopsClaimAndRestoresOnReappear(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	createVideoPlan(t, service, userID, courseID)
	taskID := taskIDsForUser(t, pool, userID)[0]
	actor := Actor{ID: userID, Kind: "user"}

	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001"))
	syncAll(t, service, userID)
	state, desired, _, _, _, _ := taskRow(t, pool, taskID)
	var errorCode string
	if err := pool.QueryRow(context.Background(), `SELECT last_error_code FROM public.learning_tasks
		WHERE id=$1`, taskID).Scan(&errorCode); err != nil {
		t.Fatal(err)
	}
	if state != StateNeedsAttention || desired != DesiredRun || errorCode != snapshotMissingCode {
		t.Fatalf("消失任务未转人工: %s/%s/%s", state, desired, errorCode)
	}
	if job, err := service.claimNext(context.Background()); err != nil || job != nil {
		t.Fatalf("最新快照已不存在的任务仍被领取: %+v %v", job, err)
	}
	if _, err := service.ControlTask(context.Background(), actor, taskID,
		"redispatch-missing-0001", "redispatch", "尝试重新调度"); err == nil {
		t.Fatal("消失任务仍可重新调度为待执行")
	}
	// 模拟核验/重试路径重新写入 pending，后台修复应再次转人工而不是静默等待。
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks SET state='pending',
		last_error_code='', next_attempt_at=now() WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	service.recoverOnce(context.Background())
	state, _, _, _, _, _ = taskRow(t, pool, taskID)
	if state != StateNeedsAttention {
		t.Fatalf("遗留 pending 未被修复为人工处理: %s", state)
	}

	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5001", videoAttachment("video-1", "9001")))
	syncAll(t, service, userID)
	state, desired, _, _, _, _ = taskRow(t, pool, taskID)
	if state != StatePending || desired != DesiredRun {
		t.Fatalf("任务重新出现后未恢复待执行: %s/%s", state, desired)
	}
	if err := pool.QueryRow(context.Background(), `SELECT last_error_code FROM public.learning_tasks
		WHERE id=$1`, taskID).Scan(&errorCode); err != nil || errorCode != "" {
		t.Fatalf("恢复后错误码未清除: %s %v", errorCode, err)
	}
	job := claimOne(t, service)
	if job.TaskID != taskID {
		t.Fatalf("恢复后领取错误任务: %s", job.TaskID)
	}
}

// TestMissingSnapshotTaskPreservesUserPauseAndCancel 验证消失任务转人工后，
// 用户明确暂停/取消不会被重新出现自动覆盖，也不改动资金事实。
func TestMissingSnapshotTaskPreservesUserPauseAndCancel(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5001", videoAttachment("video-1", "9001"), videoAttachment("video-2", "9002")))
	service := newVideoLearningService(t, pool, platform)
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskIDs := taskIDsForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}

	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001"))
	syncAll(t, service, userID)
	for _, taskID := range taskIDs {
		state, _, _, _, _, _ := taskRow(t, pool, taskID)
		if state != StateNeedsAttention {
			t.Fatalf("消失任务未转人工: %s", state)
		}
	}
	if _, err := service.ControlTask(context.Background(), actor, taskIDs[0],
		"pause-missing-0001", "pause", "用户暂停"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ControlTask(context.Background(), actor, taskIDs[1],
		"cancel-missing-0001", "cancel", "用户取消"); err != nil {
		t.Fatal(err)
	}
	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5001", videoAttachment("video-1", "9001"), videoAttachment("video-2", "9002")))
	syncAll(t, service, userID)
	pausedState, pausedDesired, _, _, _, _ := taskRow(t, pool, taskIDs[0])
	cancelledState, cancelledDesired, _, _, _, _ := taskRow(t, pool, taskIDs[1])
	if pausedState != StatePaused || pausedDesired != DesiredPause {
		t.Fatalf("用户暂停被重新出现覆盖: %s/%s", pausedState, pausedDesired)
	}
	if cancelledState != StateCancelled || cancelledDesired != DesiredCancel {
		t.Fatalf("用户取消被重新出现覆盖: %s/%s", cancelledState, cancelledDesired)
	}
	for _, taskID := range taskIDs {
		var billingState, hold string
		if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_hold_id
			FROM public.learning_tasks WHERE id=$1`, taskID).Scan(&billingState, &hold); err != nil {
			t.Fatal(err)
		}
		if billingState != "disabled" || hold != "" {
			t.Fatalf("快照消失改动了资金事实: %s/%s", billingState, hold)
		}
	}
}

// TestMissingSnapshotWithSubmissionOnlyVerifies 验证附件消失但已有最终提交事实的
// 任务回到核验，而不是转人工或重新执行、重新提交。
func TestMissingSnapshotWithSubmissionOnlyVerifies(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	platform.unknown = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	// 模拟重新调度后、下一次领取前附件从最新快照消失。
	if _, err := service.pool.Exec(context.Background(), `UPDATE public.learning_tasks
		SET state='pending', verification_reason='', next_attempt_at=now(), next_verify_at=NULL
		WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = nil
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, job.UserID)
	state, _, _, _, _, _ := taskRow(t, service.pool, job.TaskID)
	var reason string
	var nextVerify *time.Time
	if err := service.pool.QueryRow(context.Background(), `SELECT verification_reason, next_verify_at
		FROM public.learning_tasks WHERE id=$1`, job.TaskID).Scan(&reason, &nextVerify); err != nil {
		t.Fatal(err)
	}
	if state != StateWaitingVerification || reason != "question_submission" || nextVerify == nil {
		t.Fatalf("最终提交事实未进入核验: %s/%s/%v", state, reason, nextVerify)
	}
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("快照消失后重新做题或提交: posts=%d calls=%d", platform.posts, answers.modelCalls)
	}
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), job.TaskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, _, _, _, _ = taskRow(t, service.pool, job.TaskID)
	if state != StateConfirmed {
		t.Fatalf("已有最终提交事实的任务未按平台确认完成: %s", state)
	}
	if platform.posts != 1 {
		t.Fatal("核验过程中重新提交了最终答案")
	}
}

// TestCourseSyncRejectsIncompleteBatch 验证 SyncBatch 限制不会再以部分课程记成功。
func TestCourseSyncRejectsIncompleteBatch(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.courses = append(platform.courses,
		PlatformCourse{ExternalCourseID: "102", ExternalClassID: "203", CPI: "303", Title: "第二门课程"})
	options := fastOptions(platform, nil)
	options.SyncBatch = 1
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	requested, _, err := service.RequestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal(err)
	}
	service.runCourseSync(context.Background(), *job)
	status, err := readCourseSync(context.Background(), pool, requested.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "needs_attention" || status.ErrorCode != "sync_batch_exceeded" {
		t.Fatalf("超出批量上限未拒绝不完整同步: %+v", status)
	}
	var courses int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_courses
		WHERE user_id=$1`, userID).Scan(&courses); err != nil || courses != 0 {
		t.Fatalf("不完整同步写入了部分课程: %d %v", courses, err)
	}
}

// TestMissingSnapshotTaskKeepsReservedBilling 验证任务从最新快照消失转入人工时
// 不释放或结算既有预占；重新出现并确认后仍只结算一次。
func TestMissingSnapshotTaskKeepsReservedBilling(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, Currency: "CNY", PriceVersion: testPriceVersion}}
	recorder := &recordingConfirmation{}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	first := claimOne(t, service)
	if err := service.applyExecutionResult(context.Background(), first, ExecutionResult{},
		Fail(FailureTransient, "夹具模拟临时失败"), service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	platform := service.opts.Platform.(*fakePlatform)
	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001"))
	syncAll(t, service, first.UserID)
	state, _, _, _, _, _ := taskRow(t, pool, taskID)
	billingState, hold, _, quote := billingTaskFacts(t, pool, taskID)
	if state != StateNeedsAttention || billingState != "reserved" || hold != "hold-state-1" ||
		!quote.Configured || quote.AmountMinor != 500 {
		t.Fatalf("消失任务改动了预占事实: %s/%s/%s/%+v", state, billingState, hold, quote)
	}
	platform.snapshots["101/202"] = snapshotWithChapters(
		videoChapter("5001", videoAttachment("video-1", "9001")))
	syncAll(t, service, first.UserID)
	second := claimOne(t, service)
	if billing.reserveCalls != 1 {
		t.Fatalf("重新出现后重复预占: %d", billing.reserveCalls)
	}
	waitingTask(t, service, taskID, second)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if billing.settleCalls != 1 || recorder.calls != 1 || len(billing.settleAmounts) != 1 ||
		billing.settleAmounts[0] != 500 {
		t.Fatalf("消失重现导致重复结算: settle=%d amounts=%v recorder=%d",
			billing.settleCalls, billing.settleAmounts, recorder.calls)
	}
}

// TestMissingSnapshotTaskResumeWithSubmissionOnlyVerifies 验证消失但已有最终提交
// 事实的任务，用户恢复/重新调度时只回到核验，而不是重新执行或提交。
func TestMissingSnapshotTaskResumeWithSubmissionOnlyVerifies(t *testing.T) {
	service, executor, platform, answers, job, _ := setupWorkTask(t)
	platform.unknown = true
	result, err := executor.Execute(context.Background(), workRequest(job))
	requireWorkFailure(t, err, FailureSubmissionUnknown)
	if err := service.applyExecutionResult(context.Background(), job, result, err, executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = nil
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, job.UserID)
	actor := Actor{ID: job.UserID, Kind: "user"}
	if _, err := service.ControlTask(context.Background(), actor, job.TaskID,
		"pause-missing-submit-0001", "pause", "暂停"); err != nil {
		t.Fatal(err)
	}
	control, err := service.ControlTask(context.Background(), actor, job.TaskID,
		"resume-missing-submit-0001", "resume", "继续")
	if err != nil {
		t.Fatal(err)
	}
	if control.State != StateWaitingVerification {
		t.Fatalf("有最终提交事实的任务恢复后未进入核验: %+v", control)
	}
	var reason string
	var nextVerify *time.Time
	if err := service.pool.QueryRow(context.Background(), `SELECT verification_reason, next_verify_at
		FROM public.learning_tasks WHERE id=$1`, job.TaskID).Scan(&reason, &nextVerify); err != nil {
		t.Fatal(err)
	}
	if reason != "question_submission" || nextVerify == nil {
		t.Fatalf("恢复后未保留只核验事实: %s/%v", reason, nextVerify)
	}
	if platform.posts != 1 || answers.modelCalls != 2 {
		t.Fatalf("恢复后重新做题或提交: posts=%d calls=%d", platform.posts, answers.modelCalls)
	}
}
