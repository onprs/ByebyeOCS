//go:build integration

package learning

import (
	"context"
	"testing"
	"time"
)

// 单门课程瞬态失败时，同一个同步任务自动重试；已完成的课程不会被重新拉取。
func TestCourseSyncRetriesTransientPartialWithoutRefetching(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.courses = append(platform.courses, PlatformCourse{ExternalCourseID: "102", ExternalClassID: "203", CPI: "303", Title: "第二门"})
	platform.snapshots["102/203"] = PlatformCourseSnapshot{
		Course: PlatformCourseRef{ExternalCourseID: "102", ExternalClassID: "203", CPI: "303", CycleKey: "current"},
		Title:  "第二门", Adapter: "fake",
		Chapters: []PlatformChapter{{ExternalID: "5002", Title: "", SortOrder: 0,
			Attachments: []PlatformAttachment{{ObjectID: "video-2", TaskPointID: "9002", Kind: "video",
				Title: "第二门视频", DurationSeconds: 60, Passed: false, PassedKnown: true}}}},
	}
	platform.discoverFailures = map[string]int{"102/203": 1}
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx := context.Background()
	actor := Actor{ID: user, Kind: "user"}
	requested, _, err := service.RequestCourseSync(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatalf("领取同步失败：%v", err)
	}
	service.runCourseSync(ctx, *job)

	var status, errorCode string
	var nextAttempt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, error_code, next_attempt_at FROM public.learning_course_syncs WHERE id=$1`,
		requested.ID).Scan(&status, &errorCode, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || errorCode != "course_transient_partial" || nextAttempt == nil {
		t.Fatalf("瞬态失败没有自动排队重试：%s/%s/%v", status, errorCode, nextAttempt)
	}
	if got := platform.discoverCalls["101/202"]; got != 1 {
		t.Fatalf("已完成课程被重复读取：%d", got)
	}
	// 等待重试时间到点后再次领取并执行。
	if _, err := pool.Exec(ctx, `UPDATE public.learning_course_syncs SET next_attempt_at=now() WHERE id=$1`, requested.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := service.claimCourseSync(ctx)
	if err != nil || retry == nil || retry.ID != job.ID {
		t.Fatalf("重试没有被同一个任务领取：%#v/%v", retry, err)
	}
	service.runCourseSync(ctx, *retry)
	if err := pool.QueryRow(ctx, `SELECT status, error_code FROM public.learning_course_syncs WHERE id=$1`,
		requested.ID).Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || errorCode != "" {
		t.Fatalf("重试后仍未成功：%s/%s", status, errorCode)
	}
	if platform.discoverCalls["101/202"] != 1 {
		t.Fatalf("重试重新拉取了已完成课程：%d", platform.discoverCalls["101/202"])
	}
	if platform.discoverCalls["102/203"] != 2 {
		t.Fatalf("失败课程没有在重试中补齐：%d", platform.discoverCalls["102/203"])
	}
}
