//go:build integration

package learning

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestClaimSkipsSaturatedAccountBeforeCandidateLimit(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	attachments := make([]PlatformAttachment, 130)
	for i := range attachments {
		attachments[i] = videoAttachment(fmt.Sprintf("video-%d", i), fmt.Sprintf("job-%d", i))
	}
	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001", attachments...))
	opts := fastOptions(platform, &executorFunc{capabilities: ExecutorCapabilities{Enabled: true, TaskKinds: []string{"video"}}})
	opts.AccountConcurrency = 1
	service, err := New(pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	busy := seedUser(t, pool)
	bindTestAccount(t, service, busy)
	syncAll(t, service, busy)
	createVideoPlan(t, service, busy, courseIDForUser(t, pool, busy))
	first := claimOne(t, service)
	if first.UserID != busy {
		t.Fatal("未占用测试账号名额")
	}
	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001", videoAttachment("available", "available-job")))
	available := seedUser(t, pool)
	bindTestAccount(t, service, available)
	syncAll(t, service, available)
	createVideoPlan(t, service, available, courseIDForUser(t, pool, available))
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_accounts SET last_claimed_at=
		CASE WHEN user_id=$1 THEN now()-interval '1 hour' ELSE now() END`, busy); err != nil {
		t.Fatal(err)
	}
	next, err := service.claimNext(context.Background())
	if err != nil || next == nil || next.UserID != available {
		t.Fatalf("并发已满账号的前100个候选阻塞了可执行账号: job=%+v err=%v", next, err)
	}
}

type slowCoursePlatform struct {
	*fakePlatform
	delay time.Duration
}

func (p slowCoursePlatform) DiscoverCourse(ctx context.Context, session []byte, ref PlatformCourseRef) (PlatformCourseSnapshot, error) {
	timer := time.NewTimer(p.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return PlatformCourseSnapshot{}, ctx.Err()
	case <-timer.C:
		return p.fakePlatform.DiscoverCourse(ctx, session, ref)
	}
}

func TestCourseSyncKeepsLeaseDuringSlowCourseDiscovery(t *testing.T) {
	pool := testPool(t)
	platform := slowCoursePlatform{fakePlatform: defaultFakePlatform(), delay: 400 * time.Millisecond}
	opts := fastOptions(platform, nil)
	opts.LeaseTTL = 150 * time.Millisecond
	opts.HeartbeatInterval = 30 * time.Millisecond
	service, err := New(pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := service.RequestCourseSync(ctx, Actor{ID: user, Kind: "user"}); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatalf("无法领取同步: %v", err)
	}
	service.runCourseSync(ctx, *job)
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM public.learning_course_syncs WHERE id=$1`, job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("单课程读取超过租约后无法同步: %s", status)
	}
}

func TestSyncCompletionAndOutboxRollbackTogether(t *testing.T) {
	pool := testPool(t)
	service := newVideoLearningService(t, pool, defaultFakePlatform())
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	if _, _, err := service.RequestCourseSync(ctx, Actor{ID: user, Kind: "user"}); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取同步失败: %v", err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION public.fixture_reject_sync_finished() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN IF NEW.topic='course.sync_finished' THEN RAISE EXCEPTION 'fixture'; END IF; RETURN NEW; END $$;
		CREATE TRIGGER fixture_reject_sync_finished BEFORE INSERT ON public.learning_outbox
		FOR EACH ROW EXECUTE FUNCTION public.fixture_reject_sync_finished()`); err != nil {
		t.Fatal(err)
	}
	if err := service.finishCourseSync(ctx, job, "succeeded", "", 1, 1); err == nil {
		t.Fatal("事件写入失败仍报告成功")
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM public.learning_course_syncs WHERE id=$1`, job.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("事件失败后同步状态未回滚: %s", status)
	}
}

func TestHeartbeatLocksTasksBeforeAttempts(t *testing.T) {
	pool := testPool(t)
	service := newVideoLearningService(t, pool, defaultFakePlatform())
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	createVideoPlan(t, service, user, courseIDForUser(t, pool, user))
	job := claimOne(t, service)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLearningTx(tx)
	if _, err := tx.Exec(ctx, `SELECT id FROM public.learning_tasks WHERE id=$1 FOR UPDATE`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	extended := make(chan error, 1)
	go func() { _, err := service.extendLeases(ctx); extended <- err }()
	waitFor(t, time.Second, func() bool {
		var waiting bool
		_ = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
			AND wait_event_type='Lock' AND query LIKE '%learning_task_attempts%')`).Scan(&waiting)
		return waiting
	})
	lockCtx, stopLock := context.WithTimeout(ctx, 250*time.Millisecond)
	_, err = tx.Exec(lockCtx, `SELECT id FROM public.learning_task_attempts WHERE id=$1 FOR UPDATE`, job.AttemptID)
	stopLock()
	if err != nil {
		t.Fatalf("心跳先锁尝试，和结果写回形成反向锁序: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-extended:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("心跳未在锁释放后恢复")
	}
}
