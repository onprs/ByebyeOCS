//go:build integration

package learning

import (
	"context"
	"testing"
)

func TestClosedChapterPreservesKnownAttachmentsAndCompletion(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, userID, taskID := prepareTask(t, pool, platform)
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments, PlatformAttachment{ObjectID: "completed-media", TaskPointID: "9003", Kind: "video", Title: "已完成内容", Passed: true, PassedKnown: true})
	snapshot.Chapters = append(snapshot.Chapters, PlatformChapter{ExternalID: "5002", Title: "开放内容", Attachments: []PlatformAttachment{{ObjectID: "open-media", TaskPointID: "9002", Kind: "video", Title: "开放任务", PassedKnown: true}}})
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	courseID := courseIDForUser(t, pool, userID)
	var oldChecked any
	if err := pool.QueryRow(ctx, `SELECT progress_checked_at FROM public.learning_attachments a JOIN public.learning_tasks t ON t.attachment_id=a.id WHERE t.id=$1`, taskID).Scan(&oldChecked); err != nil {
		t.Fatal(err)
	}
	snapshot.Chapters[0].Closed = true
	snapshot.Chapters[0].Attachments = nil
	snapshot.Chapters = append(snapshot.Chapters, PlatformChapter{ExternalID: "5003", Title: "尚未读取的关闭内容", Closed: true})
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	sync, err := service.LatestCourseSync(ctx, actor)
	if err != nil || sync.Status != "succeeded" {
		t.Fatalf("关闭目录仍被当成同步失败：%+v/%v", sync, err)
	}
	detail, err := service.GetCourse(ctx, actor, courseID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Chapters) != 3 || detail.Chapters[0].Availability != "closed" || len(detail.Chapters[0].Attachments) != 3 || detail.Chapters[2].Availability != "closed" || len(detail.Chapters[2].Attachments) != 0 {
		t.Fatalf("关闭目录或历史附件丢失：%+v", detail)
	}
	if detail.TaskPointCount != 3 || detail.IncompleteTaskPointCount != 2 || detail.ClosedTaskPointCount != 1 {
		t.Fatalf("关闭被当作完成或计数丢失：%+v", detail.CourseSummary)
	}
	task, err := service.GetTask(ctx, actor, taskID)
	if err != nil || task.State != StateNeedsAttention || task.LastErrorCode != "chapter_closed" || task.Availability != "closed" || task.Platform.Status == CheckConfirmed {
		t.Fatalf("关闭任务事实错误：%+v/%v", task, err)
	}
	var unchanged bool
	if err := pool.QueryRow(ctx, `SELECT a.progress_checked_at IS NOT DISTINCT FROM $2::timestamptz FROM public.learning_attachments a JOIN public.learning_tasks t ON t.attachment_id=a.id WHERE t.id=$1`, taskID, oldChecked).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("历史核验时间被伪造刷新：%v/%v", unchanged, err)
	}
	plans, err := service.ListPlans(ctx, actor, PlanFilter{})
	if err != nil || len(plans.Items) != 1 {
		t.Fatal(err)
	}
	progress := plans.Items[0].Progress
	if progress.Closed != 1 || progress.Attention != 0 || progress.Completed != 1 || progress.Active != 1 {
		t.Fatalf("安排把关闭任务计为异常：%+v", progress)
	}
	job, err := service.claimNext(ctx)
	if err != nil || job == nil || job.ChapterExternal != "5002" {
		t.Fatalf("关闭章节仍被派发：%+v/%v", job, err)
	}
	snapshot.Chapters[0].Closed = false
	snapshot.Chapters[0].Attachments = defaultFakePlatform().snapshots["101/202"].Chapters[0].Attachments
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	task, err = service.GetTask(ctx, actor, taskID)
	if err != nil || task.State != StatePending || task.LastErrorCode != "" || task.Availability != "open" {
		t.Fatalf("重新开放没有恢复：%+v/%v", task, err)
	}
}

func TestClosedChapterRecheckQueuesDiscoveryInsteadOfExecution(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, userID, taskID := prepareTask(t, pool, platform)
	original := platform.snapshots["101/202"]
	closed := original
	closed.Chapters = append([]PlatformChapter(nil), original.Chapters...)
	closed.Chapters[0].Closed = true
	closed.Chapters[0].Attachments = nil
	platform.snapshots["101/202"] = closed
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	key := newID()
	for _, requestKey := range []string{key, key, newID()} {
		result, err := service.ControlTask(ctx, actor, taskID, requestKey, "redispatch", "重新检查章节可用性")
		if err != nil || result.State != StateNeedsAttention {
			t.Fatalf("关闭章节直接进入执行：%+v/%v", result, err)
		}
	}
	var queued int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.learning_course_syncs WHERE user_id=$1 AND status='queued'`, userID).Scan(&queued); err != nil || queued != 1 {
		t.Fatalf("目录复查未正确去重：%d/%v", queued, err)
	}
	if job, err := service.claimNext(ctx); err != nil || job != nil {
		t.Fatalf("复查前领取了关闭任务：%+v/%v", job, err)
	}
	platform.snapshots["101/202"] = original
	syncAll(t, service, userID)
	task, err := service.GetTask(ctx, actor, taskID)
	if err != nil || task.State != StatePending || task.Availability != "open" {
		t.Fatalf("目录重新开放未恢复：%+v/%v", task, err)
	}
}

func TestClosedChapterStopsInFlightWithoutConfirming(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, userID, taskID := prepareTask(t, pool, platform)
	job := claimOne(t, service)
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Closed = true
	snapshot.Chapters[0].Attachments = nil
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	var cancelRequested bool
	if err := pool.QueryRow(ctx, `SELECT cancel_requested FROM public.learning_task_attempts WHERE id=$1`, job.AttemptID).Scan(&cancelRequested); err != nil || !cancelRequested {
		t.Fatalf("在途任务未请求停止：%v/%v", cancelRequested, err)
	}
	service.finish(ctx, job, ExecutionResult{}, Fail(FailureStopped, "章节关闭后停止"), service.opts.Executor.Capabilities())
	task, err := service.GetTask(ctx, Actor{ID: userID, Kind: "user"}, taskID)
	if err != nil || task.State != StateNeedsAttention || task.LastErrorCode != "chapter_closed" || task.Platform.Status == CheckConfirmed {
		t.Fatalf("关闭停止结果错误：%+v/%v", task, err)
	}
}
