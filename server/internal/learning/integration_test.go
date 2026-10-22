//go:build integration

package learning

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testsupport.NewPool(t)
}

func seedUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.users (id) VALUES ($1)`, id); err != nil {
		t.Fatal("创建测试用户失败", err)
	}
	return id
}

func seedAdmin(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.admin_users (id, username, password_hash)
		VALUES ($1, $2, $3)`, id, "admin-"+id[:8], "test-only-hash"); err != nil {
		t.Fatal("创建测试管理员失败", err)
	}
	return id
}

func bindTestAccount(t *testing.T, service *Service, userID string) {
	t.Helper()
	if err := service.BindAccount(context.Background(), userID, AccountIdentity{
		Provider: "chaoxing", Namespace: "chaoxing-test", Subject: "subject-" + userID,
		School: "测试学校", DisplayName: "测试用户", VerifiedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal("绑定测试身份失败", err)
	}
}

type fakePlatform struct {
	mu        sync.Mutex
	courses   []PlatformCourse
	snapshots map[string]PlatformCourseSnapshot
	checks    map[string]PlatformCheck
	failure   error
	listCalls int
}

func (f *fakePlatform) Capabilities() PlatformCapabilities {
	return PlatformCapabilities{Adapter: "fake", CourseRead: true, TaskCheck: true}
}

func (f *fakePlatform) ListCourses(ctx context.Context, session []byte) ([]PlatformCourse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.failure != nil {
		return nil, f.failure
	}
	return append([]PlatformCourse(nil), f.courses...), nil
}

func (f *fakePlatform) DiscoverCourse(ctx context.Context, session []byte, ref PlatformCourseRef) (PlatformCourseSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return PlatformCourseSnapshot{}, f.failure
	}
	key := ref.ExternalCourseID + "/" + ref.ExternalClassID
	snapshot, ok := f.snapshots[key]
	if !ok {
		return PlatformCourseSnapshot{}, Fail(FailureCapabilityMissing, "missing fake course")
	}
	return snapshot, nil
}

func (f *fakePlatform) CheckTask(ctx context.Context, session []byte, ref PlatformTaskRef) (PlatformCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failure != nil {
		return PlatformCheck{Status: CheckUnknown}, f.failure
	}
	if check, ok := f.checks[ref.ObjectID]; ok {
		return check, nil
	}
	// 测试平台默认返回新鲜且未完成；只有显式构造的 check 才会变成已完成/未知。
	return PlatformCheck{Status: CheckUnconfirmed, CheckedAt: time.Now().UTC(), Source: "fake-check"}, nil
}

func defaultFakePlatform() *fakePlatform {
	course := PlatformCourse{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", Title: "测试课程"}
	return &fakePlatform{
		courses: []PlatformCourse{course},
		snapshots: map[string]PlatformCourseSnapshot{
			"101/202": {
				Course: PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", CycleKey: "current"},
				Title:  "测试课程", Adapter: "fake",
				Chapters: []PlatformChapter{{ExternalID: "5001", Title: "", SortOrder: 0,
					Attachments: []PlatformAttachment{
						{ObjectID: "video-1", TaskPointID: "9001", Kind: "video", Title: "视频任务",
							DurationSeconds: 120, PlayedSeconds: 10, Passed: false, PassedKnown: true},
						{ObjectID: "video-resource", TaskPointID: "", Kind: "video", Title: "普通视频资料",
							DurationSeconds: 60, Passed: false, PassedKnown: true},
					}}},
			},
		},
		checks: map[string]PlatformCheck{},
	}
}

type executorFunc struct {
	capabilities ExecutorCapabilities
	run          func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error)
}

func (e *executorFunc) Capabilities() ExecutorCapabilities { return e.capabilities }
func (e *executorFunc) Execute(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
	return e.run(ctx, request)
}

type verifierFunc func(ctx context.Context, request VerificationRequest) (PlatformCheck, error)

func (f verifierFunc) Verify(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
	return f(ctx, request)
}

type fakeResolver struct {
	session []byte
	err     error
}

func (f fakeResolver) PlatformSession(ctx context.Context, userID string) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.session, nil
}

func testIdentityFingerprint(namespace, subject string) string {
	mac := hmac.New(sha256.New, []byte("byebyeocs-learning-test-fingerprint-key"))
	mac.Write([]byte("bbo:learning:identity:v1"))
	mac.Write([]byte{0})
	mac.Write([]byte(namespace))
	mac.Write([]byte{0})
	mac.Write([]byte(subject))
	return hex.EncodeToString(mac.Sum(nil))
}

func fastOptions(platform Platform, executor Executor) *Options {
	return &Options{
		Platform: platform, Executor: executor, Sessions: fakeResolver{session: []byte("test-session")},
		IdentityFingerprint: testIdentityFingerprint,
		ExecutionEnabled:    executor != nil, GlobalConcurrency: 8, AccountConcurrency: 4,
		LeaseTTL: 5 * time.Second, HeartbeatInterval: 500 * time.Millisecond,
		PollInterval: 20 * time.Millisecond, VerificationInterval: 50 * time.Millisecond,
		RetryBase: 20 * time.Millisecond, MaxAttempts: 3,
	}
}

func syncAll(t *testing.T, service *Service, userID string) {
	t.Helper()
	actor := Actor{ID: userID, Kind: "user"}
	if _, _, err := service.RequestCourseSync(context.Background(), actor); err != nil {
		t.Fatal("请求课程同步失败", err)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal("领取课程同步失败", err)
	}
	service.runCourseSync(context.Background(), *job)
}

func courseIDForUser(t *testing.T, pool *pgxpool.Pool, userID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM public.learning_courses
		WHERE user_id = $1 AND external_course_id = '101'`, userID).Scan(&id); err != nil {
		t.Fatal("读取课程失败", err)
	}
	return id
}

func taskIDsForUser(t *testing.T, pool *pgxpool.Pool, userID string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id::text FROM public.learning_tasks
		WHERE user_id = $1 ORDER BY task_point_id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func createVideoPlan(t *testing.T, service *Service, userID, courseID string) PlanView {
	t.Helper()
	plan, err := service.CreatePlan(context.Background(), Actor{ID: userID, Kind: "user"}, PlanInput{
		Name: "测试安排", CourseIDs: []string{courseID},
		Scope: PlanScopeInput{Kind: "all"}, TaskTypes: []string{"video"}, Priority: 10,
	})
	if err != nil {
		t.Fatal("创建测试安排失败", err)
	}
	return plan
}

func prepareTask(t *testing.T, pool *pgxpool.Pool, platform Platform) (*Service, string, string) {
	t.Helper()
	userID := seedUser(t, pool)
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}}
	service, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	createVideoPlan(t, service, userID, courseID)
	ids := taskIDsForUser(t, pool, userID)
	if len(ids) != 1 {
		t.Fatalf("任务数量错误: %d", len(ids))
	}
	return service, userID, ids[0]
}

func taskRow(t *testing.T, pool *pgxpool.Pool, taskID string) (state, desired, platformStatus string, confirmed *time.Time, mediaAvailable bool, observation []byte) {
	t.Helper()
	var observationRaw, mediaRaw []byte
	if err := pool.QueryRow(context.Background(), `SELECT state, desired_state, platform_status, confirmed_at,
		media_evidence, observation FROM public.learning_tasks WHERE id = $1`, taskID).Scan(
		&state, &desired, &platformStatus, &confirmed, &mediaRaw, &observationRaw); err != nil {
		t.Fatal(err)
	}
	var media struct {
		Available bool `json:"available"`
	}
	_ = json.Unmarshal(mediaRaw, &media)
	return state, desired, platformStatus, confirmed, media.Available, observationRaw
}

func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

func newTestMux(t *testing.T, service *Service, actors map[string]Actor) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	service.Register(mux)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actors[r.Header.Get("X-Test-Actor")]
		if !ok {
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "请先登录")
			return
		}
		ctx := web.WithActor(r.Context(), actor)
		ctx = web.WithRequestID(ctx, web.NewRequestID())
		mux.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(server.Close)
	return server
}

func requestJSON(t *testing.T, method, target string, body any, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader = bytes.NewReader(nil)
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 0)
	buffer := make([]byte, 4096)
	for {
		n, err := response.Body.Read(buffer)
		payload = append(payload, buffer[:n]...)
		if err != nil {
			break
		}
	}
	response.Body.Close()
	return response, payload
}

func TestIntegrationMigrationAndSync(t *testing.T) {
	pool := testPool(t)
	service, userID, _ := prepareTask(t, pool, defaultFakePlatform())
	detail, err := service.GetCourse(context.Background(), Actor{ID: userID, Kind: "user"},
		courseIDForUser(t, pool, userID))
	if err != nil {
		t.Fatal(err)
	}
	if detail.TaskPointCount != 1 || detail.IncompleteTaskPointCount != 1 || detail.ResourceCount != 1 {
		t.Fatalf("课程统计错误: %+v", detail.CourseSummary)
	}
	var incomplete bool
	if err := pool.QueryRow(context.Background(), `SELECT counted_incomplete FROM public.learning_attachments
		WHERE user_id = $1 AND external_object_id = 'video-resource'`, userID).Scan(&incomplete); err != nil {
		t.Fatal(err)
	}
	if incomplete {
		t.Fatal("没有任务点编号的附件被计入未完成")
	}
	if len(taskIDsForUser(t, pool, userID)) != 1 {
		t.Fatal("非任务点附件生成了任务")
	}
}

func claimOne(t *testing.T, service *Service) executionJob {
	t.Helper()
	job, err := service.claimNext(context.Background())
	if err != nil {
		t.Fatal("领取任务失败", err)
	}
	if job == nil {
		t.Fatal("没有可领取任务")
	}
	return *job
}

func waitingTask(t *testing.T, service *Service, taskID string, job executionJob) {
	t.Helper()
	caps := service.opts.Executor.Capabilities()
	if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{
		Completed:   true,
		Observation: Observation{Fraction: ptrFloat(1), Source: "fake", ObservedAt: time.Now().UTC()},
	}, nil, caps); err != nil {
		t.Fatal("写入执行结果失败", err)
	}
	state, _, _, _, _, _ := taskRow(t, service.pool, taskID)
	if state != StateWaitingVerification {
		t.Fatalf("执行完成后状态应为待核验，实际 %s", state)
	}
}

func TestExpiredLeaseWritebackBlocked(t *testing.T) {
	pool := testPool(t)
	service, _, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_task_attempts
		SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, job.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks
		SET lease_expires_at = now() - interval '1 minute' WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}
	err := service.applyExecutionResult(context.Background(), job, ExecutionResult{Completed: true}, nil,
		service.opts.Executor.Capabilities())
	if !errors.Is(err, errLeaseLost) {
		t.Fatalf("过期租约写入未被拒绝: %v", err)
	}
	state, _, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
	if state != StatePreparing || platformStatus != CheckUnconfirmed || confirmed != nil {
		t.Fatal("过期租约改写了任务事实")
	}
	service.recoverOnce(context.Background())
	state, _, platformStatus, _, _, _ = taskRow(t, pool, taskID)
	if state != StateWaitingVerification || platformStatus != CheckUnconfirmed {
		t.Fatalf("恢复后应先待核验，实际 %s/%s", state, platformStatus)
	}
	var reason string
	if err := pool.QueryRow(context.Background(), `SELECT verification_reason FROM public.learning_tasks
		WHERE id = $1`, taskID).Scan(&reason); err != nil || reason != "crash_recovery" {
		t.Fatal("崩溃恢复原因未记录", err, reason)
	}
}

func TestObservationDoesNotConfirm(t *testing.T) {
	pool := testPool(t)
	service, _, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	result := ExecutionResult{Completed: true,
		Observation: Observation{Fraction: ptrFloat(1), Source: "fake", ObservedAt: time.Now().UTC()},
		Media:       MediaEvidence{Available: true, Source: "fake-playback", ObservedAt: time.Now().UTC()}}
	if err := service.applyExecutionResult(context.Background(), job, result, nil,
		service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, confirmed, media, _ := taskRow(t, pool, taskID)
	if state == StateConfirmed || platformStatus == CheckConfirmed || confirmed != nil {
		t.Fatal("执行观察被当成了平台确认")
	}
	if media {
		t.Fatal("未声明媒体能力的执行器写入了媒体证据")
	}
	check := PlatformCheck{Status: CheckUnconfirmed, Passed: false, CheckedAt: time.Now().UTC(),
		Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, confirmed, _, _ = taskRow(t, pool, taskID)
	if state != StatePending || platformStatus != CheckUnconfirmed || confirmed != nil {
		t.Fatalf("未确认任务不应进入确认: %s/%s", state, platformStatus)
	}
}

func TestVerificationCodesStopOperations(t *testing.T) {
	cases := []struct {
		name      string
		class     FailureClass
		wantState string
		wantBlock string
		max       int
	}{
		{"验证码", FailureVerificationNeeded, StateNeedsAttention, "verification_required", 3},
		{"403", FailureForbidden, StateNeedsAttention, "forbidden", 3},
		{"429", FailureRateLimited, StateNeedsAttention, "", 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pool := testPool(t)
			userID := seedUser(t, pool)
			executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
				TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
				return ExecutionResult{}, nil
			}}
			options := fastOptions(defaultFakePlatform(), executor)
			options.MaxAttempts = test.max
			service, err := New(pool, options)
			if err != nil {
				t.Fatal(err)
			}
			bindTestAccount(t, service, userID)
			syncAll(t, service, userID)
			courseID := courseIDForUser(t, pool, userID)
			createVideoPlan(t, service, userID, courseID)
			taskID := taskIDsForUser(t, pool, userID)[0]
			job := claimOne(t, service)
			if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{},
				Fail(test.class, "平台要求停止"), service.opts.Executor.Capabilities()); err != nil {
				t.Fatal(err)
			}
			state, _, _, _, _, _ := taskRow(t, pool, taskID)
			if state != test.wantState {
				t.Fatalf("状态错误: %s", state)
			}
			var blocked, blockedUntil *string
			if err := pool.QueryRow(context.Background(), `SELECT NULLIF(blocked_code, ''),
				blocked_until::text FROM public.learning_accounts WHERE user_id = $1`, userID).
				Scan(&blocked, &blockedUntil); err != nil {
				t.Fatal(err)
			}
			if test.wantBlock != "" && (blocked == nil || *blocked != test.wantBlock) {
				t.Fatalf("账号未按分类停止: %v", blocked)
			}
			if test.class == FailureRateLimited && blockedUntil == nil {
				t.Fatal("429 未设置账号冷却")
			}
			var nextAttempt *time.Time
			if err := pool.QueryRow(context.Background(), `SELECT next_attempt_at FROM public.learning_tasks
				WHERE id = $1`, taskID).Scan(&nextAttempt); err != nil {
				t.Fatal(err)
			}
			if test.class == FailureRateLimited && nextAttempt != nil {
				t.Fatal("限流耗尽后仍在自动重试")
			}
		})
	}
}

func TestControlAndConfirmationRace(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)
	started := make(chan struct{})
	release := make(chan struct{})
	verifier := verifierFunc(func(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
		close(started)
		<-release
		return PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(),
			Source: "fake-check"}, nil
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		service.runVerification(context.Background(), verifier, taskID, userID)
	}()
	<-started
	result, err := service.ControlTask(context.Background(), Actor{ID: userID, Kind: "user"},
		taskID, "pause-key-0001", "pause", "先暂停")
	if err != nil {
		t.Fatal("暂停请求失败", err)
	}
	if result.State != StatePaused {
		t.Fatalf("待核验任务暂停结果错误: %+v", result)
	}
	close(release)
	<-done
	state, desired, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || desired != DesiredPause || platformStatus != CheckConfirmed || confirmed == nil {
		t.Fatalf("确认竞态丢失事实: %s/%s/%s", state, desired, platformStatus)
	}
	if _, err := service.ControlTask(context.Background(), Actor{ID: userID, Kind: "user"},
		taskID, "pause-key-0002", "pause", "再次暂停"); err == nil {
		t.Fatal("已确认任务仍允许暂停")
	}
}

func TestCancelThenPlatformConfirms(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)
	if _, err := service.ControlTask(context.Background(), Actor{ID: userID, Kind: "user"},
		taskID, "cancel-key-0001", "cancel", "用户取消"); err != nil {
		t.Fatal(err)
	}
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, _, _, _ := taskRow(t, pool, taskID)
	if state != StateCancelled || platformStatus != CheckConfirmed {
		t.Fatalf("取消与后来确认应分别保留: %s/%s", state, platformStatus)
	}
}

func TestPlanRevisionOnlyAffectsFutureTasks(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	actor := Actor{ID: userID, Kind: "user"}
	courseID := courseIDForUser(t, pool, userID)
	plans, err := service.ListPlans(context.Background(), actor, PlanFilter{Limit: 10})
	if err != nil || len(plans.Items) != 1 {
		t.Fatalf("读取安排失败: %v", err)
	}
	plan := plans.Items[0]
	job := claimOne(t, service)
	if job.PlanRevisionID != plan.CurrentRevisionID {
		t.Fatal("尝试未绑定当前安排版本")
	}
	updated, err := service.UpdatePlan(context.Background(), actor, plan.ID, plan.Version, PlanInput{
		Name: "测试安排", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"video"}, Priority: 90,
	})
	if err != nil {
		t.Fatal("修改安排失败", err)
	}
	if updated.Version != plan.Version+1 {
		t.Fatal("安排版本未递增")
	}
	var attemptRevision string
	if err := pool.QueryRow(context.Background(), `SELECT plan_revision_id::text
		FROM public.learning_task_attempts WHERE id = $1`, job.AttemptID).Scan(&attemptRevision); err != nil {
		t.Fatal(err)
	}
	if attemptRevision != plan.CurrentRevisionID {
		t.Fatal("执行中尝试被安排修改改绑")
	}
	detail, err := service.GetTask(context.Background(), actor, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.PlanRefs) != 1 || detail.PlanRefs[0].Version != updated.Version {
		t.Fatal("未来任务没有绑定新版本")
	}
	if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{Completed: true},
		nil, service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
}

func TestRestartVerifiesWithoutReplay(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)

	options := fastOptions(defaultFakePlatform(), nil)
	options.ExecutionEnabled = false
	options.Verifier = verifierFunc(func(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
		return PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(),
			Source: "restart-check"}, nil
	})
	restarted, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go restarted.Run(ctx)
	waitFor(t, 3*time.Second, func() bool {
		state, _, _, _, _, _ := taskRow(t, pool, taskID)
		return state == StateConfirmed
	})
	_ = userID
}

func TestSameTaskOnlyOneExecutor(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	userID := seedUser(t, pool)
	var calls atomic.Int32
	release := make(chan struct{})
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ExecutionResult{Completed: true, Observation: Observation{Source: "fake",
			ObservedAt: time.Now().UTC()}}, nil
	}}
	first, err := New(pool, fastOptions(platform, executor))
	if err != nil {
		t.Fatal(err)
	}
	bindTestAccount(t, first, userID)
	syncAll(t, first, userID)
	createVideoPlan(t, first, userID, courseIDForUser(t, pool, userID))
	secondOptions := fastOptions(platform, executor)
	secondOptions.InstanceID = "second-instance"
	second, err := New(pool, secondOptions)
	if err != nil {
		t.Fatal(err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()
	go first.Run(ctx1)
	go second.Run(ctx2)
	waitFor(t, 3*time.Second, func() bool { return calls.Load() == 1 })
	time.Sleep(300 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatalf("同一任务被多个实例执行: %d", calls.Load())
	}
	close(release)
	waitFor(t, 3*time.Second, func() bool {
		state, _, _, _, _, _ := taskRow(t, pool, taskIDsForUser(t, pool, userID)[0])
		return state == StateWaitingVerification
	})
}

func TestSameAccountDifferentTasksConcurrent(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments,
		PlatformAttachment{ObjectID: "video-2", TaskPointID: "9002", Kind: "video", Title: "第二任务",
			DurationSeconds: 90, Passed: false, PassedKnown: true})
	platform.snapshots["101/202"] = snapshot
	userID := seedUser(t, pool)
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(platform, executor)
	options.AccountConcurrency = 2
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	for range 2 {
		select {
		case <-started:
		case <-time.After(4 * time.Second):
			t.Fatal("同账号不同任务没有并发执行")
		}
	}
	close(release)
	waitFor(t, 4*time.Second, func() bool {
		states := map[string]bool{}
		for _, taskID := range taskIDsForUser(t, pool, userID) {
			state, _, _, _, _, _ := taskRow(t, pool, taskID)
			states[state] = true
		}
		return states[StateWaitingVerification]
	})
}

func TestTaskControlIdempotency(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	actor := Actor{ID: userID, Kind: "user"}
	first, err := service.ControlTask(context.Background(), actor, taskID, "task-key-0001", "pause", "暂停")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ControlTask(context.Background(), actor, taskID, "task-key-0001", "pause", "暂停")
	if err != nil {
		t.Fatal("相同幂等键重放失败", err)
	}
	if first.State != second.State || first.Applied != second.Applied {
		t.Fatal("幂等重放返回不一致")
	}
	if _, err := service.ControlTask(context.Background(), actor, taskID, "task-key-0001", "resume", "继续"); err == nil {
		t.Fatal("相同幂等键不同请求未冲突")
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_task_commands
		WHERE task_id = $1`, taskID).Scan(&count); err != nil || count != 1 {
		t.Fatal("命令未按幂等键收敛", err, count)
	}
	if _, err := service.CreatePlan(context.Background(), actor, PlanInput{
		Name: "共享任务安排", CourseIDs: []string{courseIDForUser(t, pool, userID)},
		Scope: PlanScopeInput{Kind: "all"}, TaskTypes: []string{"video"}, Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if len(taskIDsForUser(t, pool, userID)) != 1 {
		t.Fatal("多个安排创建了第二个规范任务")
	}
}

func TestPermissionIsolationAndAdminAudit(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	serviceA, userA, _ := prepareTask(t, pool, platform)
	userB := seedUser(t, pool)
	if err := serviceA.BindAccount(context.Background(), userB, AccountIdentity{
		Provider: "chaoxing", Namespace: "chaoxing-test", Subject: "subject-" + userB,
		VerifiedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	syncAll(t, serviceA, userB)
	createVideoPlan(t, serviceA, userB, courseIDForUser(t, pool, userB))
	taskB := taskIDsForUser(t, pool, userB)[0]
	courseB := courseIDForUser(t, pool, userB)
	adminID := seedAdmin(t, pool)
	actors := map[string]Actor{
		"A":       {ID: userA, Kind: "user"},
		"B":       {ID: userB, Kind: "user"},
		"admin":   {ID: adminID, Kind: "admin", Permissions: []string{PermissionRead, PermissionAccounts, PermissionTasks}},
		"limited": {ID: adminID, Kind: "admin", Permissions: []string{}},
	}
	server := newTestMux(t, serviceA, actors)

	response, body := requestJSON(t, http.MethodGet, server.URL+"/api/v1/courses", nil, map[string]string{"X-Test-Actor": "A"})
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(courseIDForUser(t, pool, userA))) {
		t.Fatalf("用户课程列表错误: %d %s", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte(courseB)) {
		t.Fatal("用户看到了其他账号课程")
	}
	response, _ = requestJSON(t, http.MethodGet, server.URL+"/api/v1/courses/"+courseB, nil, map[string]string{"X-Test-Actor": "A"})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("跨用户课程读取返回 %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodGet, server.URL+"/api/v1/tasks/"+taskB, nil, map[string]string{"X-Test-Actor": "A"})
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("跨用户任务读取返回 %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/api/v1/plans", map[string]any{
		"name": "伪造归属", "user_id": userB, "course_ids": []string{courseIDForUser(t, pool, userA)},
		"scope": map[string]any{"kind": "all"}, "task_types": []string{"video"}},
		map[string]string{"X-Test-Actor": "A"})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("body 自报 user_id 未被拒绝: %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodGet, server.URL+"/admin/api/v1/dashboard", nil, map[string]string{"X-Test-Actor": "limited"})
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("无权限管理员读取返回 %d", response.StatusCode)
	}
	response, body = requestJSON(t, http.MethodGet, server.URL+"/admin/api/v1/dashboard", nil, map[string]string{"X-Test-Actor": "admin"})
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("execution_enabled")) {
		t.Fatalf("管理员运行概览错误: %d %s", response.StatusCode, body)
	}
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/admin/api/v1/account-operations", map[string]any{
		"user_ids": []string{userB}, "action": "pause_plans", "reason": ""},
		map[string]string{"X-Test-Actor": "admin", "Idempotency-Key": "admin-key-0001"})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("无理由管理员操作未被拒绝: %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/admin/api/v1/account-operations", map[string]any{
		"user_ids": []string{userB}, "action": "pause_plans", "reason": "运营核查"},
		map[string]string{"X-Test-Actor": "admin", "Idempotency-Key": "admin-key-0002"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("管理员账号操作失败: %d", response.StatusCode)
	}
	var action, reason string
	if err := pool.QueryRow(context.Background(), `SELECT action, COALESCE(reason, '') FROM public.audit_events
		WHERE actor_admin_id = $1 ORDER BY occurred_at DESC LIMIT 1`, adminID).Scan(&action, &reason); err != nil {
		t.Fatal("管理员操作未写审计", err)
	}
	if action != "learning.account.pause_plans" || reason != "运营核查" {
		t.Fatalf("审计内容错误: %s/%s", action, reason)
	}
}

func TestIdentityFingerprintPersisted(t *testing.T) {
	pool := testPool(t)
	_, userID, _ := prepareTask(t, pool, defaultFakePlatform())
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT identity_fingerprint FROM public.learning_accounts
		WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	rawSubject := "subject-" + userID
	if stored == rawSubject || strings.Contains(stored, rawSubject) {
		t.Fatal("真实主体明文进入了学习业务表")
	}
	if stored != testIdentityFingerprint("chaoxing-test", rawSubject) {
		t.Fatal("持久化身份键不是配置的 HMAC 指纹")
	}
	var taskFingerprint string
	if err := pool.QueryRow(context.Background(), `SELECT identity_fingerprint FROM public.learning_tasks
		WHERE user_id = $1 LIMIT 1`, userID).Scan(&taskFingerprint); err != nil {
		t.Fatal(err)
	}
	if taskFingerprint != stored {
		t.Fatal("任务稳定组合键未使用身份指纹")
	}
	noFingerprint, err := New(pool, &Options{Sessions: fakeResolver{session: []byte("s")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := noFingerprint.BindAccount(context.Background(), seedUser(t, pool), AccountIdentity{
		Provider: "chaoxing", Namespace: "n", Subject: "raw", VerifiedAt: time.Now()}); err == nil {
		t.Fatal("未配置身份指纹仍接受绑定")
	}
}

func TestCourseSyncDeduplicates(t *testing.T) {
	pool := testPool(t)
	service, userID, _ := prepareTask(t, pool, defaultFakePlatform())
	first, created, err := service.RequestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if err != nil || !created {
		t.Fatalf("首次同步未创建: %v", err)
	}
	second, created, err := service.RequestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("重复同步未去重: %v %+v", err, second)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_course_syncs
		WHERE user_id = $1 AND status IN ('queued', 'running')`, userID).Scan(&count); err != nil || count != 1 {
		t.Fatal("同步活跃记录未收敛", err, count)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal(err)
	}
	service.runCourseSync(context.Background(), *job)
	status, err := readCourseSync(context.Background(), pool, first.ID)
	if err != nil || status.Status != "succeeded" {
		t.Fatalf("同步未成功: %+v %v", status, err)
	}
}

func TestPlanETagAndControlIdempotency(t *testing.T) {
	pool := testPool(t)
	service, userID, _ := prepareTask(t, pool, defaultFakePlatform())
	courseID := courseIDForUser(t, pool, userID)
	plan := createVideoPlan(t, service, userID, courseID)
	actors := map[string]Actor{"A": {ID: userID, Kind: "user"}}
	server := newTestMux(t, service, actors)
	headers := map[string]string{"X-Test-Actor": "A"}
	response, _ := requestJSON(t, http.MethodGet, server.URL+"/api/v1/plans/"+plan.ID, nil, headers)
	if response.StatusCode != http.StatusOK || response.Header.Get("ETag") == "" {
		t.Fatalf("读取安排缺少 ETag: %d", response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	body := map[string]any{"name": "新名称", "course_ids": []string{courseID},
		"scope": map[string]any{"kind": "all"}, "task_types": []string{"video"}, "priority": 20}
	response, _ = requestJSON(t, http.MethodPatch, server.URL+"/api/v1/plans/"+plan.ID, body, headers)
	if response.StatusCode != http.StatusPreconditionRequired {
		t.Fatalf("缺少 If-Match 返回 %d", response.StatusCode)
	}
	headers["If-Match"] = `"v999"`
	response, _ = requestJSON(t, http.MethodPatch, server.URL+"/api/v1/plans/"+plan.ID, body, headers)
	if response.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("错误版本返回 %d", response.StatusCode)
	}
	headers["If-Match"] = etag
	response, payload := requestJSON(t, http.MethodPatch, server.URL+"/api/v1/plans/"+plan.ID, body, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("正确版本保存失败: %d %s", response.StatusCode, payload)
	}
	if response.Header.Get("ETag") != `"v2"` {
		t.Fatalf("新 ETag 错误: %s", response.Header.Get("ETag"))
	}
	control := map[string]string{"action": "pause"}
	headers["Idempotency-Key"] = "plan-key-0001"
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/api/v1/plans/"+plan.ID+"/controls", control, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("安排暂停失败: %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/api/v1/plans/"+plan.ID+"/controls", control, headers)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("安排暂停幂等重放失败: %d", response.StatusCode)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_plan_commands
		WHERE plan_id = $1`, plan.ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("安排命令未收敛", err, count)
	}
}

func TestPauseRunningRequiresAcknowledgement(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	result, err := service.ControlTask(context.Background(), Actor{ID: userID, Kind: "user"},
		taskID, "pause-run-0001", "pause", "暂停执行")
	if err != nil {
		t.Fatal(err)
	}
	if result.State != StateRunning && result.State != StatePreparing {
		t.Fatalf("暂停请求直接改变了真实状态: %+v", result)
	}
	if result.Applied || !result.StopRequested {
		t.Fatalf("运行中暂停未表现为停止请求: %+v", result)
	}
	state, desired, _, _, _, _ := taskRow(t, pool, taskID)
	if state == StatePaused || desired != DesiredPause {
		t.Fatalf("控制响应伪称安全停止: %s/%s", state, desired)
	}
	var cancelRequested bool
	if err := pool.QueryRow(context.Background(), `SELECT cancel_requested FROM public.learning_task_attempts
		WHERE id = $1`, job.AttemptID).Scan(&cancelRequested); err != nil || !cancelRequested {
		t.Fatal("执行器未收到停止请求", err)
	}
	if err := service.applyExecutionResult(context.Background(), job,
		ExecutionResult{StopAcknowledged: true}, nil, service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
	state, _, _, _, _, _ = taskRow(t, pool, taskID)
	if state != StatePaused {
		t.Fatalf("执行器确认停止后状态错误: %s", state)
	}
}

func TestNoFakeConfirmationEndpoint(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	server := newTestMux(t, service, map[string]Actor{"A": {ID: userID, Kind: "user"}})
	headers := map[string]string{"X-Test-Actor": "A", "Idempotency-Key": "confirm-key-0001"}
	response, _ := requestJSON(t, http.MethodPost, server.URL+"/api/v1/tasks/"+taskID+"/controls",
		map[string]string{"action": "confirm"}, headers)
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm 控制未被拒绝: %d", response.StatusCode)
	}
	response, _ = requestJSON(t, http.MethodPost, server.URL+"/api/v1/tasks/"+taskID+"/confirm", nil, headers)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("存在模拟确认入口: %d", response.StatusCode)
	}
}

func TestAdminTaskOperationAudit(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	adminID := seedAdmin(t, pool)
	server := newTestMux(t, service, map[string]Actor{"admin": {ID: adminID, Kind: "admin",
		Permissions: []string{PermissionRead, PermissionTasks}}})
	response, payload := requestJSON(t, http.MethodPost, server.URL+"/admin/api/v1/task-operations",
		map[string]any{"task_ids": []string{taskID}, "action": "pause", "reason": "运营核查"},
		map[string]string{"X-Test-Actor": "admin", "Idempotency-Key": "admin-task-0001"})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("管理员任务操作失败: %d %s", response.StatusCode, payload)
	}
	var action string
	if err := pool.QueryRow(context.Background(), `SELECT action FROM public.audit_events
		WHERE actor_admin_id = $1 AND resource_id = $2`, adminID, taskID).Scan(&action); err != nil {
		t.Fatal("管理员任务操作未写审计", err)
	}
	if action != "learning.task.control" {
		t.Fatal("任务审计动作错误", action)
	}
	state, _, _, _, _, _ := taskRow(t, pool, taskID)
	if state != StatePaused {
		t.Fatal("管理员暂停未生效", state)
	}
	_ = userID
}

func TestGlobalLimitAndFairClaim(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	userA := seedUser(t, pool)
	userB := seedUser(t, pool)
	started := make(chan string, 4)
	release := make(chan struct{})
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		started <- request.UserID
		select {
		case <-release:
		case <-ctx.Done():
		}
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(platform, executor)
	options.GlobalConcurrency = 1
	options.AccountConcurrency = 1
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	for _, userID := range []string{userA, userB} {
		bindTestAccount(t, service, userID)
		syncAll(t, service, userID)
		createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	var first string
	select {
	case first = <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("首个任务没有开始")
	}
	select {
	case second := <-started:
		t.Fatalf("全局并发上限未生效，同时启动了 %s 和 %s", first, second)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case second := <-started:
		if second == first {
			t.Fatalf("公平领取总在同一账号上选择: %s", second)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("释放名额后第二个账号未被领取")
	}
}

func TestBindAccountTxRollback(t *testing.T) {
	pool := testPool(t)
	service, err := New(pool, fastOptions(defaultFakePlatform(), nil))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	verifiedAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	identity := AccountIdentity{Provider: "chaoxing", Namespace: "chaoxing-test",
		Subject: "subject-" + userID, School: "学校", DisplayName: "用户", VerifiedAt: verifiedAt}
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BindAccountTx(context.Background(), tx, userID, identity); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_accounts
		WHERE user_id = $1`, userID).Scan(&count); err != nil || count != 0 {
		t.Fatal("事务回滚后身份绑定仍存在", err, count)
	}
	tx, err = pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.BindAccountTx(context.Background(), tx, userID, identity); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stored time.Time
	if err := pool.QueryRow(context.Background(), `SELECT verified_at FROM public.learning_accounts
		WHERE user_id = $1`, userID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Equal(verifiedAt) {
		t.Fatalf("验证时间未使用实际完成时间: %s != %s", stored, verifiedAt)
	}
	if err := service.BindAccountTx(context.Background(), nil, userID, identity); err == nil {
		t.Fatal("nil 事务被接受")
	}
}

func seedLearningUser(t *testing.T, service *Service, pool *pgxpool.Pool) (string, string) {
	t.Helper()
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	plan := createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	return userID, plan.ID
}

func adminActorForTest(t *testing.T, pool *pgxpool.Pool) Actor {
	t.Helper()
	return Actor{ID: seedAdmin(t, pool), Kind: "admin",
		Permissions: []string{PermissionRead, PermissionAccounts, PermissionTasks}}
}

func TestAccountOperationRequestIdempotency(t *testing.T) {
	pool := testPool(t)
	service, err := New(pool, fastOptions(defaultFakePlatform(), nil))
	if err != nil {
		t.Fatal(err)
	}
	userA, planA := seedLearningUser(t, service, pool)
	userB, planB := seedLearningUser(t, service, pool)
	actor := adminActorForTest(t, pool)
	request := AccountOperationsRequest{UserIDs: []string{userA, userB}, Action: "pause_plans", Reason: "运营核查"}
	results := make(chan AccountOperationResult, 2)
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(reverse bool) {
			defer wait.Done()
			body := request
			if reverse {
				body.UserIDs = []string{userB, userA}
			}
			result, err := service.AdminAccountOperations(context.Background(), actor, body, "account-key-0001")
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(index == 1)
	}
	wait.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	collected := make([]AccountOperationResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	if len(collected) != 2 {
		t.Fatal("并发幂等请求未全部返回")
	}
	if collected[0].CommandID != collected[1].CommandID || collected[0].Status != "applied" ||
		collected[1].Status != "applied" {
		t.Fatalf("并发幂等结果不一致: %+v / %+v", collected[0], collected[1])
	}
	replayed := 0
	for _, result := range collected {
		if result.Replayed {
			replayed++
		}
		if len(result.Items) != 2 {
			t.Fatalf("逐项结果缺失: %+v", result.Items)
		}
	}
	if replayed != 1 {
		t.Fatalf("并发重复请求的重放标记错误: %d", replayed)
	}
	var commands, items, audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_account_commands`).Scan(&commands); err != nil || commands != 1 {
		t.Fatal("幂等命令未收敛", err, commands)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_account_command_items
		WHERE status = 'applied'`).Scan(&items); err != nil || items != 2 {
		t.Fatal("逐项状态未完成", err, items)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events
		WHERE actor_admin_id = $1 AND action = 'learning.account.pause_plans'`, actor.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("重复请求产生了重复审计", err, audits)
	}
	for _, planID := range []string{planA, planB} {
		var status string
		if err := pool.QueryRow(context.Background(), `SELECT status FROM public.learning_plans WHERE id = $1`, planID).Scan(&status); err != nil || status != "paused" {
			t.Fatal("账号状态未生效", err, status)
		}
	}
}

func TestAccountOperationDifferentPayloadConflicts(t *testing.T) {
	pool := testPool(t)
	service, err := New(pool, fastOptions(defaultFakePlatform(), nil))
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := seedLearningUser(t, service, pool)
	actor := adminActorForTest(t, pool)
	request := AccountOperationsRequest{UserIDs: []string{userID}, Action: "pause_plans", Reason: "运营核查"}
	if _, err := service.AdminAccountOperations(context.Background(), actor, request, "account-key-0002"); err != nil {
		t.Fatal(err)
	}
	changed := request
	changed.Action = "resume_plans"
	_, err = service.AdminAccountOperations(context.Background(), actor, changed, "account-key-0002")
	if err == nil {
		t.Fatal("相同幂等键不同负载未冲突")
	}
	var apiError *Error
	if !errors.As(err, &apiError) || apiError.Code != "idempotency_conflict" {
		t.Fatalf("冲突错误码错误: %v", err)
	}
	changed = request
	changed.Reason = "不同理由"
	if _, err := service.AdminAccountOperations(context.Background(), actor, changed, "account-key-0002"); err == nil {
		t.Fatal("相同幂等键不同理由未冲突")
	}
	var commands, audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_account_commands`).Scan(&commands); err != nil || commands != 1 {
		t.Fatal("冲突请求创建了额外命令", err, commands)
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events
		WHERE actor_admin_id = $1`, actor.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("冲突请求产生了额外审计", err, audits)
	}
}

func TestAccountOperationPartialFailureReplay(t *testing.T) {
	pool := testPool(t)
	service, err := New(pool, fastOptions(defaultFakePlatform(), nil))
	if err != nil {
		t.Fatal(err)
	}
	userA, _ := seedLearningUser(t, service, pool)
	missingUser := web.NewID()
	actor := adminActorForTest(t, pool)
	request := AccountOperationsRequest{UserIDs: []string{userA, missingUser}, Action: "pause_plans", Reason: "部分失败核查"}
	result, err := service.AdminAccountOperations(context.Background(), actor, request, "account-key-0003")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "partially_applied" || len(result.Items) != 2 {
		t.Fatalf("部分失败结果错误: %+v", result)
	}
	applied, failed := 0, 0
	for _, item := range result.Items {
		if item.OK {
			applied++
		} else {
			failed++
			if item.Code != "identity_not_linked" {
				t.Fatalf("逐项失败码错误: %+v", item)
			}
		}
	}
	if applied != 1 || failed != 1 {
		t.Fatal("部分失败逐项结果数量错误")
	}
	replay, err := service.AdminAccountOperations(context.Background(), actor, request, "account-key-0003")
	if err != nil || !replay.Replayed || replay.Status != "partially_applied" {
		t.Fatalf("部分失败重放错误: %+v %v", replay, err)
	}
	for _, item := range replay.Items {
		if item.UserID == missingUser && item.OK {
			t.Fatal("失败项在重放中被重做")
		}
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events
		WHERE actor_admin_id = $1`, actor.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("部分失败重放重复审计", err, audits)
	}
	var failedAudits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events
		WHERE actor_admin_id = $1 AND action = 'learning.account.pause_plans_failed'`, actor.ID).Scan(&failedAudits); err != nil || failedAudits != 1 {
		t.Fatal("失败项未记录逐项审计", err, failedAudits)
	}
}

func TestAccountOperationPendingItemRecovery(t *testing.T) {
	pool := testPool(t)
	service, err := New(pool, fastOptions(defaultFakePlatform(), nil))
	if err != nil {
		t.Fatal(err)
	}
	userA, planA := seedLearningUser(t, service, pool)
	userB, planB := seedLearningUser(t, service, pool)
	actor := adminActorForTest(t, pool)
	reason := "中断恢复核查"
	const key = "account-key-0004"
	hash := accountOperationRequestHash("pause_plans", reason, []string{userA, userB})
	commandID := web.NewID()
	now := time.Now().UTC()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.learning_account_commands
		(id, actor_kind, actor_id, idempotency_key, request_hash, action, reason, user_ids)
		VALUES ($1, 'admin', $2, $3, $4, 'pause_plans', $5, $6::uuid[])`,
		commandID, actor.ID, key, hash, reason, []string{userA, userB}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.learning_account_command_items
		(command_id, user_id, position, status, result_status, applied_at)
		VALUES ($1, $2, 0, 'applied', 'paused', $3), ($1, $4, 1, 'pending', '', NULL)`,
		commandID, userA, now, userB); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_plans SET status = 'paused' WHERE id = $1`, planA); err != nil {
		t.Fatal(err)
	}
	requestID := web.NewRequestID()
	ctx := web.WithRequestID(context.Background(), requestID)
	if _, err := pool.Exec(ctx, `INSERT INTO public.audit_events
		(id, actor_kind, actor_admin_id, action, resource_type, resource_id, request_id, reason)
		VALUES ($1, 'admin', $2, 'learning.account.pause_plans', 'learning_account', $3, $4, $5)`,
		web.NewID(), actor.ID, userA, requestID, reason); err != nil {
		t.Fatal(err)
	}
	request := AccountOperationsRequest{UserIDs: []string{userA, userB}, Action: "pause_plans", Reason: reason}
	result, err := service.AdminAccountOperations(ctx, actor, request, key)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replayed || result.Status != "applied" {
		t.Fatalf("中断恢复结果错误: %+v", result)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events
		WHERE actor_admin_id = $1`, actor.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("中断恢复重复或遗漏审计", err, audits)
	}
	for _, planID := range []string{planA, planB} {
		var status string
		if err := pool.QueryRow(ctx, `SELECT status FROM public.learning_plans WHERE id = $1`, planID).Scan(&status); err != nil || status != "paused" {
			t.Fatal("中断恢复未完成账号状态", err, status)
		}
	}
}

type failingBilling struct {
	quoteFail   bool
	reserveFail bool
	settleFail  bool
}

func (b *failingBilling) Quote(ctx context.Context, tx pgx.Tx, userID, taskID, taskKind string) (BillingQuote, error) {
	if b.quoteFail {
		if err := b.mutate(ctx, tx); err != nil {
			return BillingQuote{}, err
		}
		return BillingQuote{}, errors.New("模拟报价 SQL 后失败")
	}
	return BillingQuote{Configured: true, AmountMinor: 500, Currency: "CNY", PriceVersion: "test-v1"}, nil
}

func (b *failingBilling) mutate(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `UPDATE public.learning_billing_test_marker SET value = 'mutated' WHERE id = 1`)
	return err
}

func (b *failingBilling) Reserve(ctx context.Context, tx pgx.Tx, userID, taskID, key string, quote BillingQuote) (string, error) {
	if err := b.mutate(ctx, tx); err != nil {
		return "", err
	}
	if b.reserveFail {
		return "", errors.New("模拟预占 SQL 后失败")
	}
	return "hold-test-1", nil
}

func (b *failingBilling) Settle(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) error {
	if err := b.mutate(ctx, tx); err != nil {
		return err
	}
	if b.settleFail {
		return errors.New("模拟结算 SQL 后失败")
	}
	return nil
}

func (b *failingBilling) Release(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key, reason string) error {
	return nil
}

func setupBillingMarker(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `CREATE TABLE IF NOT EXISTS public.learning_billing_test_marker
		(id integer PRIMARY KEY, value text NOT NULL); INSERT INTO public.learning_billing_test_marker (id, value)
		VALUES (1, 'clean') ON CONFLICT (id) DO UPDATE SET value = 'clean'`); err != nil {
		t.Fatal(err)
	}
}

func billingMarker(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var value string
	if err := pool.QueryRow(context.Background(), `SELECT value FROM public.learning_billing_test_marker WHERE id = 1`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSettlementSavepointKeepsPlatformConfirmation(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &failingBilling{settleFail: true}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.Billing = billing
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)
	// 预占阶段会提交自己的账务 SQL；重置标记，单独验证结算失败的回滚。
	setupBillingMarker(t, pool)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || platformStatus != CheckConfirmed || confirmed == nil {
		t.Fatalf("账务失败回滚了平台确认事实: %s/%s", state, platformStatus)
	}
	var billingState, billingError string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_error
		FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&billingState, &billingError); err != nil {
		t.Fatal(err)
	}
	if billingState != "settlement_failed" || billingError == "" {
		t.Fatalf("结算失败未记录待核对状态: %s/%s", billingState, billingError)
	}
	if marker := billingMarker(t, pool); marker != "clean" {
		t.Fatalf("结算失败留下了部分账务 SQL: %s", marker)
	}
	detail, err := service.GetTask(context.Background(), Actor{ID: userID, Kind: "user"}, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Billing.QuotedAmountMinor != "500" || detail.Billing.AmountKind != "quote" {
		t.Fatalf("HTTP 计费字段未标记为预估字符串: %+v", detail.Billing)
	}
}

func TestReserveSavepointKeepsTaskUsable(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &failingBilling{reserveFail: true}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.Billing = billing
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	if job, err := service.claimNext(context.Background()); err != nil || job != nil {
		t.Fatalf("预占失败后仍领取任务: %+v %v", job, err)
	}
	state, _, _, _, _, _ := taskRow(t, pool, taskID)
	if state != StateNeedsAttention {
		t.Fatalf("预占失败未转人工: %s", state)
	}
	var lease *string
	var attempts int
	if err := pool.QueryRow(context.Background(), `SELECT lease_attempt_id::text,
		(SELECT count(*) FROM public.learning_task_attempts WHERE task_id = $1)
		FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&lease, &attempts); err != nil {
		t.Fatal(err)
	}
	if lease != nil || attempts != 0 {
		t.Fatal("预占失败留下了执行尝试或租约")
	}
	if marker := billingMarker(t, pool); marker != "clean" {
		t.Fatalf("预占失败留下了部分账务 SQL: %s", marker)
	}
}

func TestQuoteSavepointKeepsTaskUsable(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &failingBilling{quoteFail: true}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.Billing = billing
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	if job, err := service.claimNext(context.Background()); err != nil || job != nil {
		t.Fatalf("报价失败后仍领取任务: %+v %v", job, err)
	}
	state, _, _, _, _, _ := taskRow(t, pool, taskID)
	if state != StateNeedsAttention {
		t.Fatalf("报价失败未转人工: %s", state)
	}
	if marker := billingMarker(t, pool); marker != "clean" {
		t.Fatalf("报价失败留下了部分账务 SQL: %s", marker)
	}
}

type fakePolicyValidator struct {
	allowed string
	calls   int
}

func (v *fakePolicyValidator) ValidateQuestionPolicy(ctx context.Context, modelOfferingID, mode string) error {
	v.calls++
	if modelOfferingID != v.allowed || (mode != "cache_first" && mode != "always_llm") {
		return errors.New("模型未发布")
	}
	return nil
}

func policyPlanInput(courseID string, policy QuestionPolicyInput) PlanInput {
	return PlanInput{Name: "题库策略安排", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"},
		TaskTypes: []string{"video"}, Priority: 20, QuestionPolicy: policy}
}

func TestPlanQuestionPolicyPersistenceAndValidation(t *testing.T) {
	pool := testPool(t)
	validator := &fakePolicyValidator{allowed: "qm_allowed"}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.QuestionsPolicyValidator = validator
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}

	// 默认关闭、无模型、无需人工确认；不调用题库校验。
	defaultPlan, err := service.CreatePlan(context.Background(), actor, policyPlanInput(courseID, QuestionPolicyInput{}))
	if err != nil {
		t.Fatal(err)
	}
	if defaultPlan.QuestionPolicy.Enabled || defaultPlan.QuestionPolicy.ModelOfferingID != nil ||
		defaultPlan.QuestionPolicy.AnswerMode != "cache_first" || defaultPlan.QuestionPolicy.RequireManualConfirmation ||
		defaultPlan.QuestionPolicy.ExecutionAvailable || defaultPlan.QuestionPolicy.SubmissionAvailable {
		t.Fatalf("默认题库策略错误: %+v", defaultPlan.QuestionPolicy)
	}
	if validator.calls != 0 {
		t.Fatal("未选择模型时调用了题库校验")
	}

	invalid := []QuestionPolicyInput{
		{Enabled: true, AnswerMode: "cache_first"},
		{Enabled: true, ModelOfferingID: ptrString("qm_allowed"), AnswerMode: "invalid"},
		{Enabled: true, ModelOfferingID: ptrString("qm_unknown"), AnswerMode: "cache_first"},
	}
	for index, policy := range invalid {
		if _, err := service.CreatePlan(context.Background(), actor, policyPlanInput(courseID, policy)); err == nil {
			t.Fatalf("非法题库策略 %d 被接受", index)
		}
	}

	validator.calls = 0
	valid := policyPlanInput(courseID, QuestionPolicyInput{Enabled: true,
		ModelOfferingID: ptrString("qm_allowed"), AnswerMode: "always_llm", RequireManualConfirmation: ptrBool(false)})
	plan, err := service.CreatePlan(context.Background(), actor, valid)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.QuestionPolicy.Enabled || plan.QuestionPolicy.ModelOfferingID == nil ||
		*plan.QuestionPolicy.ModelOfferingID != "qm_allowed" || plan.QuestionPolicy.AnswerMode != "always_llm" ||
		plan.QuestionPolicy.RequireManualConfirmation || plan.QuestionPolicy.ExecutionAvailable ||
		plan.QuestionPolicy.SubmissionAvailable {
		t.Fatalf("有效题库策略回传错误: %+v", plan.QuestionPolicy)
	}
	if validator.calls != 1 {
		t.Fatalf("有效模型未调用一次校验: %d", validator.calls)
	}
	var enabled, manual bool
	var model, mode string
	if err := pool.QueryRow(context.Background(), `SELECT question_policy_enabled,
		question_model_offering_id, question_mode, require_manual_confirmation
		FROM public.learning_plan_revisions WHERE id = $1`, plan.CurrentRevisionID).
		Scan(&enabled, &model, &mode, &manual); err != nil {
		t.Fatal(err)
	}
	if !enabled || model != "qm_allowed" || mode != "always_llm" || manual {
		t.Fatal("题库策略未写入不可变版本")
	}

	updatedInput := valid
	updatedInput.QuestionPolicy.AnswerMode = "cache_first"
	updated, err := service.UpdatePlan(context.Background(), actor, plan.ID, plan.Version, updatedInput)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != plan.Version+1 || updated.QuestionPolicy.AnswerMode != "cache_first" {
		t.Fatalf("安排新版本未保存题库策略: %+v", updated)
	}
	var oldMode string
	if err := pool.QueryRow(context.Background(), `SELECT question_mode FROM public.learning_plan_revisions
		WHERE id = $1`, plan.CurrentRevisionID).Scan(&oldMode); err != nil || oldMode != "always_llm" {
		t.Fatal("旧不可变版本的题库策略被覆盖", err, oldMode)
	}

	withoutValidator, err := New(pool, fastOptions(defaultFakePlatform(), executor))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutValidator.CreatePlan(context.Background(), actor, valid); err == nil {
		t.Fatal("未接入题库校验时仍保存了模型策略")
	}
}

func TestQuestionPolicyFrozenForRunningAttempt(t *testing.T) {
	pool := testPool(t)
	validator := &fakePolicyValidator{allowed: "qm_allowed"}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.QuestionsPolicyValidator = validator
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	actor := Actor{ID: userID, Kind: "user"}
	input := policyPlanInput(courseID, QuestionPolicyInput{Enabled: true,
		ModelOfferingID: ptrString("qm_allowed"), AnswerMode: "cache_first"})
	plan, err := service.CreatePlan(context.Background(), actor, input)
	if err != nil {
		t.Fatal(err)
	}
	job := claimOne(t, service)
	if !job.QuestionPolicy.Enabled || job.QuestionPolicy.Mode != "cache_first" ||
		job.QuestionPolicy.ModelOfferingID != "qm_allowed" || job.QuestionPolicy.RequireManualConfirmation {
		t.Fatalf("领取尝试没有冻结题库策略: %+v", job.QuestionPolicy)
	}
	input.QuestionPolicy.AnswerMode = "always_llm"
	updated, err := service.UpdatePlan(context.Background(), actor, plan.ID, plan.Version, input)
	if err != nil {
		t.Fatal(err)
	}
	if job.QuestionPolicy.Mode != "cache_first" {
		t.Fatal("安排修改覆盖了运行中尝试的既定策略")
	}
	if updated.QuestionPolicy.AnswerMode != "always_llm" {
		t.Fatal("后续版本没有保存新策略")
	}
	if err := service.applyExecutionResult(context.Background(), job, ExecutionResult{Completed: true},
		nil, service.opts.Executor.Capabilities()); err != nil {
		t.Fatal(err)
	}
}

func TestQuestionPolicyHTTPRejectsInternalFields(t *testing.T) {
	pool := testPool(t)
	validator := &fakePolicyValidator{allowed: "qm_allowed"}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.QuestionsPolicyValidator = validator
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	courseID := courseIDForUser(t, pool, userID)
	server := newTestMux(t, service, map[string]Actor{"A": {ID: userID, Kind: "user"}})
	body := map[string]any{
		"name": "非法内部字段", "course_ids": []string{courseID},
		"scope": map[string]any{"kind": "all"}, "task_types": []string{"video"},
		"question_policy": map[string]any{"enabled": true, "model_offering_id": "qm_allowed",
			"answer_mode": "cache_first", "upstream_provider": "secret", "price": "1"},
	}
	response, _ := requestJSON(t, http.MethodPost, server.URL+"/api/v1/plans", body,
		map[string]string{"X-Test-Actor": "A"})
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("题库内部字段未被拒绝: %d", response.StatusCode)
	}
}

func ptrString(value string) *string { return &value }
func ptrBool(value bool) *bool       { return &value }

type stateBilling struct {
	quote         BillingQuote
	quoteCalls    int
	reserveCalls  int
	settleCalls   int
	releaseCalls  int
	settleAmounts []int64
}

func (b *stateBilling) Quote(ctx context.Context, tx pgx.Tx, userID, taskID, taskKind string) (BillingQuote, error) {
	b.quoteCalls++
	return b.quote, nil
}
func (b *stateBilling) Reserve(ctx context.Context, tx pgx.Tx, userID, taskID, key string, quote BillingQuote) (string, error) {
	b.reserveCalls++
	return "hold-state-1", nil
}
func (b *stateBilling) Settle(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) error {
	b.settleCalls++
	b.settleAmounts = append(b.settleAmounts, amountMinor)
	return nil
}
func (b *stateBilling) Release(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key, reason string) error {
	b.releaseCalls++
	return nil
}

type recordingConfirmation struct {
	calls int
	fail  bool
}

func (r *recordingConfirmation) RecordConfirmed(ctx context.Context, tx pgx.Tx, userID, taskID string) error {
	r.calls++
	if _, err := tx.Exec(ctx, `UPDATE public.learning_billing_test_marker SET value = 'recorded' WHERE id = 1`); err != nil {
		return err
	}
	if r.fail {
		return errors.New("模拟计量 SQL 后失败")
	}
	return nil
}

func prepareBillingTask(t *testing.T, pool *pgxpool.Pool, billing Billing, recorder BillingConfirmationRecorder) (*Service, string, string) {
	t.Helper()
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.Billing = billing
	options.BillingConfirmationRecorder = recorder
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	return service, userID, taskID
}

const testPriceVersion = "11111111-2222-4333-8444-555555555555"

func TestUnpricedConfirmationRecordsUsageWithoutSettle(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: false, State: "unpriced",
		PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{}
	service, userID, taskID := prepareBillingTask(t, pool, billing, recorder)
	job := claimOne(t, service)
	var state string
	var quoteJSON []byte
	var priceVersion *string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_quote, price_version_id::text
		FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&state, &quoteJSON, &priceVersion); err != nil {
		t.Fatal(err)
	}
	if state != "unpriced" || len(quoteJSON) <= 2 || priceVersion == nil || *priceVersion != testPriceVersion {
		t.Fatalf("未定价报价未持久化: %s %s %v", state, quoteJSON, priceVersion)
	}
	waitingTask(t, service, taskID, job)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || platformStatus != CheckConfirmed || confirmed == nil {
		t.Fatal("未定价确认未提交平台事实")
	}
	if recorder.calls != 1 || billing.reserveCalls != 0 || billing.settleCalls != 0 {
		t.Fatalf("未定价确认用量/结算调用错误: recorder=%d reserve=%d settle=%d",
			recorder.calls, billing.reserveCalls, billing.settleCalls)
	}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 {
		t.Fatal("重复核验重复记录用量事实")
	}
	detail, err := service.GetTask(context.Background(), Actor{ID: userID, Kind: "user"}, taskID)
	if err != nil || detail.Billing.AmountKind != "none" || detail.Billing.QuotedAmountMinor != "0" {
		t.Fatalf("未定价计费视图错误: %+v %v", detail.Billing, err)
	}
}

func TestDisabledQuotePersistedWithPriceVersion(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: false, State: "disabled",
		PriceVersion: testPriceVersion}}
	recorder := &recordingConfirmation{}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	job := claimOne(t, service)
	var state string
	var quoteJSON []byte
	var priceVersion *string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_quote, price_version_id::text
		FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&state, &quoteJSON, &priceVersion); err != nil {
		t.Fatal(err)
	}
	if state != "disabled" || len(quoteJSON) <= 2 || priceVersion == nil {
		t.Fatalf("关闭收费报价未持久化: %s %s %v", state, quoteJSON, priceVersion)
	}
	waitingTask(t, service, taskID, job)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 0 {
		t.Fatal("关闭收费任务没有记录未收费确认事实")
	}
}

func TestZeroPriceConfirmationRecordsWithoutHold(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 0, PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{}
	service, userID, taskID := prepareBillingTask(t, pool, billing, recorder)
	job := claimOne(t, service)
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state FROM public.learning_tasks
		WHERE id = $1`, taskID).Scan(&state); err != nil || state != "quoted" {
		t.Fatalf("显式 0 价状态错误: %s %v", state, err)
	}
	if billing.reserveCalls != 0 {
		t.Fatal("显式 0 价仍创建预占")
	}
	waitingTask(t, service, taskID, job)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 0 {
		t.Fatal("显式 0 价确认没有只记录用量事实")
	}
	detail, err := service.GetTask(context.Background(), Actor{ID: userID, Kind: "user"}, taskID)
	if err != nil || detail.Billing.AmountKind != "quote" || detail.Billing.QuotedAmountMinor != "0" {
		t.Fatalf("0 价计费视图错误: %+v %v", detail.Billing, err)
	}
}

func TestMeteringFailureKeepsConfirmationAndSkipsSettlement(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{fail: true}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)
	setupBillingMarker(t, pool)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	state, _, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || platformStatus != CheckConfirmed || confirmed == nil {
		t.Fatal("计量失败回滚了平台确认")
	}
	var billingState, billingError string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_error
		FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&billingState, &billingError); err != nil {
		t.Fatal(err)
	}
	if billingState != "review_required" || billingError == "" {
		t.Fatalf("计量失败未转人工复核: %s/%s", billingState, billingError)
	}
	if billing.settleCalls != 0 {
		t.Fatal("计量失败后仍执行了结算")
	}
	if marker := billingMarker(t, pool); marker != "clean" {
		t.Fatalf("计量失败留下部分 SQL: %s", marker)
	}
}

func TestRecorderThenSettlePaidTask(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{}
	service, _, taskID := prepareBillingTask(t, pool, billing, recorder)
	job := claimOne(t, service)
	waitingTask(t, service, taskID, job)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskID, check, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 1 {
		t.Fatalf("付费路径未先记录事实再结算: recorder=%d settle=%d", recorder.calls, billing.settleCalls)
	}
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state FROM public.learning_tasks
		WHERE id = $1`, taskID).Scan(&state); err != nil || state != "settled" {
		t.Fatalf("付费结算状态错误: %s %v", state, err)
	}
}

func TestPreCompletedSkipsExecutorAndCharge(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	platform := defaultFakePlatform()
	platform.checks["video-1"] = PlatformCheck{Status: CheckConfirmed, Passed: true,
		CheckedAt: time.Now().UTC(), Source: "fresh-preflight"}
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, PriceVersion: testPriceVersion, Currency: "CNY"}}
	recorder := &recordingConfirmation{}
	executorCalls := 0
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		executorCalls++
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(platform, executor)
	options.Billing = billing
	options.BillingConfirmationRecorder = recorder
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	job := claimOne(t, service)
	service.execute(context.Background(), job)
	if executorCalls != 0 {
		t.Fatal("执行前已完成仍调用了外部执行器")
	}
	state, _, platformStatus, confirmed, media, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || platformStatus != CheckConfirmed || confirmed == nil || media {
		t.Fatalf("执行前已完成事实错误: %s/%s media=%v", state, platformStatus, media)
	}
	var reason string
	var noCharge bool
	var billingState, holdID string
	if err := pool.QueryRow(context.Background(), `SELECT confirmation_reason, pre_completed_no_charge,
		billing_state, COALESCE(billing_hold_id, '') FROM public.learning_tasks WHERE id = $1`, taskID).
		Scan(&reason, &noCharge, &billingState, &holdID); err != nil {
		t.Fatal(err)
	}
	if reason != preflightNoChargeReason || !noCharge || billingState != "released" || holdID == "" {
		t.Fatalf("执行前完成不收费事实错误: %s/%v/%s/%s", reason, noCharge, billingState, holdID)
	}
	if billing.reserveCalls != 1 || billing.releaseCalls != 1 || billing.settleCalls != 0 {
		t.Fatalf("预占/释放/结算调用错误: reserve=%d release=%d settle=%d",
			billing.reserveCalls, billing.releaseCalls, billing.settleCalls)
	}
	if recorder.calls != 1 {
		t.Fatal("执行前已完成没有记录中央用量事实")
	}
	if err := service.applyVerification(context.Background(), taskID,
		PlatformCheck{Status: CheckConfirmed, Passed: true, Source: "repeat"}, nil); err != nil {
		t.Fatal(err)
	}
	if recorder.calls != 1 || billing.settleCalls != 0 {
		t.Fatal("重复核验导致重复收费或重复中央事实")
	}
}

func TestPreflightUnknownOrErrorStopsBeforeExecutor(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.checks["video-1"] = PlatformCheck{Status: CheckUnknown, CheckedAt: time.Now().UTC(), Source: "fresh-preflight"}
	executorCalls := 0
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		executorCalls++
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(platform, executor)
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	taskID := taskIDsForUser(t, pool, userID)[0]
	job := claimOne(t, service)
	service.execute(context.Background(), job)
	if executorCalls != 0 {
		t.Fatal("核验未知仍调用执行器")
	}
	state, _, platformStatus, _, _, _ := taskRow(t, pool, taskID)
	if state != StateNeedsAttention || platformStatus == CheckConfirmed {
		t.Fatalf("核验未知未停止复核: %s/%s", state, platformStatus)
	}
}

type stateSettlementReporter struct {
	state   string
	err     error
	calls   int
	markSQL bool
}

func (r *stateSettlementReporter) SettleState(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) (string, error) {
	r.calls++
	if r.markSQL {
		if _, err := tx.Exec(ctx, `UPDATE public.learning_billing_test_marker SET value = 'reporter-mutated' WHERE id = 1`); err != nil {
			return "", err
		}
	}
	return r.state, r.err
}

func confirmForSettlementReporter(t *testing.T, pool *pgxpool.Pool, reporter BillingSettlementReporter) (*Service, string, *stateBilling) {
	t.Helper()
	billing := &stateBilling{quote: BillingQuote{Configured: true, State: "quoted",
		AmountMinor: 500, PriceVersion: testPriceVersion, Currency: "CNY"}}
	executor := &executorFunc{capabilities: ExecutorCapabilities{Name: "fake", Enabled: true,
		TaskKinds: []string{"video"}}, run: func(ctx context.Context, request ExecutionRequest) (ExecutionResult, error) {
		return ExecutionResult{Completed: true}, nil
	}}
	options := fastOptions(defaultFakePlatform(), executor)
	options.Billing = billing
	options.BillingSettlementReporter = reporter
	service, err := New(pool, options)
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	createVideoPlan(t, service, userID, courseIDForUser(t, pool, userID))
	job := claimOne(t, service)
	waitingTask(t, service, taskIDsForUser(t, pool, userID)[0], job)
	check := PlatformCheck{Status: CheckConfirmed, Passed: true, CheckedAt: time.Now().UTC(), Source: "fake-check"}
	if err := service.applyVerification(context.Background(), taskIDsForUser(t, pool, userID)[0], check, nil); err != nil {
		t.Fatal(err)
	}
	return service, taskIDsForUser(t, pool, userID)[0], billing
}

func TestSettlementReporterStates(t *testing.T) {
	cases := []struct {
		state      string
		wantState  string
		wantError  string
		wantSettle int
	}{
		{"settled", "settled", "", 0},
		{"released", "released", "", 0},
		{"needs_review", "review_required", "needs_review", 0},
	}
	for _, test := range cases {
		t.Run(test.state, func(t *testing.T) {
			pool := testPool(t)
			setupBillingMarker(t, pool)
			reporter := &stateSettlementReporter{state: test.state}
			_, taskID, billing := confirmForSettlementReporter(t, pool, reporter)
			state, _, platformStatus, confirmed, _, _ := taskRow(t, pool, taskID)
			if state != StateConfirmed || platformStatus != CheckConfirmed || confirmed == nil {
				t.Fatal("结算报告器状态回滚了平台确认")
			}
			var billingState, billingError string
			if err := pool.QueryRow(context.Background(), `SELECT billing_state, billing_error
				FROM public.learning_tasks WHERE id = $1`, taskID).Scan(&billingState, &billingError); err != nil {
				t.Fatal(err)
			}
			if billingState != test.wantState || billingError != test.wantError {
				t.Fatalf("结算报告器状态错误: %s/%s，期望 %s/%s", billingState, billingError, test.wantState, test.wantError)
			}
			if reporter.calls != 1 || billing.settleCalls != test.wantSettle {
				t.Fatalf("报告器/兼容 Settle 调用错误: reporter=%d settle=%d", reporter.calls, billing.settleCalls)
			}
		})
	}
}

func TestSettlementReporterErrorRollsBackSQLOnly(t *testing.T) {
	pool := testPool(t)
	setupBillingMarker(t, pool)
	reporter := &stateSettlementReporter{state: "settled", err: errors.New("模拟结算状态 SQL 后失败"), markSQL: true}
	_, taskID, billing := confirmForSettlementReporter(t, pool, reporter)
	state, _, platformStatus, _, _, _ := taskRow(t, pool, taskID)
	if state != StateConfirmed || platformStatus != CheckConfirmed {
		t.Fatal("结算状态报告失败回滚了平台确认")
	}
	var billingState string
	if err := pool.QueryRow(context.Background(), `SELECT billing_state FROM public.learning_tasks
		WHERE id = $1`, taskID).Scan(&billingState); err != nil || billingState != "settlement_failed" {
		t.Fatalf("结算状态失败未标记待核对: %s %v", billingState, err)
	}
	if billing.settleCalls != 0 || reporter.calls != 1 {
		t.Fatal("报告器存在时仍调用了旧 Settle")
	}
	if marker := billingMarker(t, pool); marker != "clean" {
		t.Fatalf("结算状态失败留下部分 SQL: %s", marker)
	}
}
