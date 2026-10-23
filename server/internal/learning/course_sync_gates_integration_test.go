//go:build integration

package learning

import (
	"context"
	"errors"
	"testing"
)

func TestQueuedCourseSyncWaitsForAccountRecovery(t *testing.T) {
	pool := testPool(t)
	service := newVideoLearningService(t, pool, defaultFakePlatform())
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	if _, _, err := service.RequestCourseSync(ctx, Actor{Kind: "user", ID: user}); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='admin_blocked' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job != nil {
		t.Fatalf("排队同步绕过账号停止：%#v/%v", job, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	job, err = service.claimCourseSync(ctx)
	if err != nil || job == nil || job.AttemptCount != 1 {
		t.Fatalf("恢复后无法领取原同步或限制期间消耗尝试：%#v/%v", job, err)
	}
}

func TestCourseSnapshotCannotWriteAfterUserRestriction(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	if _, _, err := service.RequestCourseSync(ctx, Actor{Kind: "user", ID: user}); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	account, err := service.accountForUser(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.users SET status='restricted' WHERE id=$1`, user); err != nil {
		t.Fatal(err)
	}
	_, err = service.applyCourseSnapshot(ctx, job, account, platform.snapshots["101/202"])
	if !errors.Is(err, errLeaseLost) {
		t.Fatalf("受限后仍保存外部课程快照：%v", err)
	}
	var courses int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.learning_courses WHERE user_id=$1`, user).Scan(&courses); err != nil || courses != 0 {
		t.Fatalf("受限用户产生新的课程数据：%d/%v", courses, err)
	}
}

func TestEmptyCourseListIsSuccessfulSync(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.courses = nil
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	operation, _, err := service.RequestCourseSync(ctx, Actor{Kind: "user", ID: user})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	service.runCourseSync(ctx, *job)
	var status string
	var count int
	if err := pool.QueryRow(ctx, `SELECT status,course_count FROM public.learning_course_syncs WHERE id=$1`, operation.ID).Scan(&status, &count); err != nil || status != "succeeded" || count != 0 {
		t.Fatalf("正常空课程列表被记作故障：%s/%d/%v", status, count, err)
	}
}
