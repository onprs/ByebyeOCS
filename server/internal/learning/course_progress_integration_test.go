//go:build integration

package learning

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestCourseProgressTracksTaskChecksWithoutResynchronizing(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, userID, _ := prepareTask(t, pool, platform)
	snapshot := platform.snapshots["101/202"]
	// 同媒体的不同任务点必须独立计数，普通资源不能混入任务点。
	snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments, PlatformAttachment{ObjectID: "video-1", TaskPointID: "9002", Kind: "video", Title: "另一任务点", PassedKnown: true})
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	job := claimOne(t, service)
	actor := Actor{ID: userID, Kind: "user"}
	task, err := service.GetTask(context.Background(), actor, job.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	courseID := task.CourseID
	read := func(incomplete, closed int) CourseDetail {
		t.Helper()
		page, err := service.ListCourses(context.Background(), actor, CourseFilter{})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("课程列表失败: %+v/%v", page, err)
		}
		detail, err := service.GetCourse(context.Background(), actor, courseID)
		if err != nil {
			t.Fatal(err)
		}
		for _, summary := range []CourseSummary{page.Items[0], detail.CourseSummary} {
			if summary.TaskPointCount != 2 || summary.ResourceCount != 1 || summary.IncompleteTaskPointCount != incomplete || summary.ClosedTaskPointCount != closed {
				t.Fatalf("列表与详情的进度不一致: %+v", summary)
			}
		}
		return detail
	}
	read(2, 0)
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks SET observation='{"progress":1,"state":"completed"}'::jsonb WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	read(2, 0)
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks SET state='waiting_verification',lease_attempt_id=NULL,lease_owner='',lease_expires_at=NULL WHERE id=$1`, job.TaskID); err != nil {
		t.Fatal(err)
	}
	checkedAt := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	playTime := 120.0
	if err := service.applyVerification(context.Background(), job.TaskID, PlatformCheck{Status: CheckConfirmed, Passed: true, Source: "platform-check", CheckedAt: checkedAt, PlayTimeSeconds: &playTime}, nil); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), `SELECT platform_checked_at FROM public.learning_tasks WHERE id=$1`, job.TaskID).Scan(&checkedAt); err != nil {
		t.Fatal(err)
	}
	detail := read(1, 0)
	var attachmentID string
	for _, chapter := range detail.Chapters {
		for _, item := range chapter.Attachments {
			if item.TaskPointID == task.TaskPointID {
				attachmentID = item.ID
				if !item.PlatformPassed || !item.PassedKnown || item.CountedIncomplete || item.PlayedSeconds != playTime || item.ProgressCheckedAt == nil || !item.ProgressCheckedAt.Equal(checkedAt) {
					t.Fatalf("附件没有投影最新核验: %+v", item)
				}
			}
		}
	}
	if attachmentID == "" {
		t.Fatal("没有找到核验对应的附件")
	}
	var rawIncomplete int
	if err := pool.QueryRow(context.Background(), `SELECT incomplete_task_point_count FROM public.learning_course_snapshots WHERE course_id=$1 ORDER BY version DESC LIMIT 1`, courseID).Scan(&rawIncomplete); err != nil || rawIncomplete != 2 {
		t.Fatalf("原始目录快照被改写: %d/%v", rawIncomplete, err)
	}
	var otherID string
	if err := pool.QueryRow(context.Background(), `SELECT id::text FROM public.learning_tasks WHERE course_id=$1 AND id<>$2`, courseID, job.TaskID).Scan(&otherID); err != nil {
		t.Fatal(err)
	}
	if err := service.withTx(context.Background(), func(tx pgx.Tx) error { return recordClosedTaskPoint(context.Background(), tx, otherID, checkedAt) }); err != nil {
		t.Fatal(err)
	}
	// 同名任务点在其他章节或其他学期关闭，不能影响当前任务点。
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.learning_task_point_availability(user_id,course_id,chapter_external_id,task_point_id,cycle_key,availability,checked_at)
		VALUES($1,$2,'other-chapter',$3,'current','closed',now()),($1,$2,'5001',$3,'other-cycle','closed',now())`, userID, courseID, task.TaskPointID); err != nil {
		t.Fatal(err)
	}
	detail = read(1, 1)
	closedAttachments := 0
	for _, chapter := range detail.Chapters {
		for _, item := range chapter.Attachments {
			if item.IsTaskPoint && item.Availability == "closed" && item.CountedIncomplete {
				closedAttachments++
			}
		}
	}
	if closedAttachments != 1 {
		t.Fatalf("附件关闭状态不一致: %d", closedAttachments)
	}
	// 更新的明确平台快照优先于旧核验；未知快照不能抹掉已核实的完成事实。
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_attachments SET platform_passed=false,passed_known=true,progress_checked_at=$2 WHERE id=$1`, attachmentID, checkedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	read(2, 1)
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_attachments SET platform_passed=false,passed_known=false,progress_checked_at=NULL WHERE id=$1`, attachmentID); err != nil {
		t.Fatal(err)
	}
	read(1, 1)
}
