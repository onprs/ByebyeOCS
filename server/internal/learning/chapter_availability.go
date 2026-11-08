package learning

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

func chapterAvailability(chapter PlatformChapter) string {
	if chapter.Closed {
		return "closed"
	}
	if chapter.IsGroup {
		return "unknown"
	}
	return "open"
}

// preserveClosedChapterAttachments 保留无法重新读取的章节历史，不用空列表抹掉已知任务和进度。
func preserveClosedChapterAttachments(ctx context.Context, tx pgx.Tx, userID, courseID string, previous int64, snapshot *PlatformCourseSnapshot) error {
	positions := map[string]int{}
	snapshot.Chapters = append([]PlatformChapter(nil), snapshot.Chapters...)
	for index, chapter := range snapshot.Chapters {
		if chapter.Closed && !chapter.IsGroup {
			positions[chapter.ExternalID] = index
			snapshot.Chapters[index].Attachments = nil
		}
	}
	if len(positions) == 0 || previous < 1 {
		return nil
	}
	ids := make([]string, 0, len(positions))
	for id := range positions {
		ids = append(ids, id)
	}
	rows, err := tx.Query(ctx, `SELECT ch.external_id,a.external_object_id,a.task_point_id,a.kind,a.title,
   a.duration_seconds,a.played_seconds,a.platform_passed,a.passed_known,a.sort_order,a.card_index,a.progress_checked_at
   FROM public.learning_attachments a JOIN public.learning_chapters ch ON ch.id=a.chapter_id AND ch.user_id=a.user_id
   WHERE a.user_id=$1 AND a.course_id=$2 AND a.snapshot_version=$3 AND ch.external_id=ANY($4::text[])
   ORDER BY ch.external_id,a.sort_order,a.id`, userID, courseID, previous, ids)
	if err != nil {
		return errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var attachment PlatformAttachment
		if err := rows.Scan(&id, &attachment.ObjectID, &attachment.TaskPointID, &attachment.Kind, &attachment.Title,
			&attachment.DurationSeconds, &attachment.PlayedSeconds, &attachment.Passed, &attachment.PassedKnown,
			&attachment.SortOrder, &attachment.CardIndex, &attachment.ProgressCheckedAt); err != nil {
			return errServiceUnavailable
		}
		index := positions[id]
		snapshot.Chapters[index].Attachments = append(snapshot.Chapters[index].Attachments, attachment)
	}
	if rows.Err() != nil {
		return errServiceUnavailable
	}
	return nil
}

// reconcileClosedChapterTasks 关闭只阻止后续执行，已有异常、完成、提交和资金事实保持。
// 重新开放时仅恢复由本模块停止的任务；已最终提交的任务只进入独立核验。
func reconcileClosedChapterTasks(ctx context.Context, tx pgx.Tx, userID, courseID string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks t SET state='needs_attention',last_error_code='chapter_closed',
   last_error_scope='task',next_attempt_at=NULL,next_verify_at=NULL,version=t.version+1,updated_at=$3
   FROM public.learning_chapters ch,public.learning_courses c
   WHERE t.user_id=$1 AND t.course_id=$2 AND t.state='pending' AND t.lease_attempt_id IS NULL
    AND c.id=t.course_id AND c.user_id=t.user_id AND ch.course_id=c.id AND ch.user_id=t.user_id
    AND ch.snapshot_version=c.snapshot_version AND ch.external_id=t.chapter_external_id AND ch.availability='closed'`, userID, courseID, now); err != nil {
		return errServiceUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks t SET
   state=CASE WHEN EXISTS(SELECT 1 FROM public.learning_work_runs w WHERE w.task_id=t.id
     AND w.state IN('submitting','submitted','submission_unknown')) THEN 'waiting_verification' ELSE 'pending' END,
   verification_reason=CASE WHEN EXISTS(SELECT 1 FROM public.learning_work_runs w WHERE w.task_id=t.id
     AND w.state IN('submitting','submitted','submission_unknown')) THEN 'question_submission' ELSE '' END,
   last_error_code='',last_error_scope='',next_attempt_at=$3,next_verify_at=$3,version=t.version+1,updated_at=$3
   FROM public.learning_chapters ch,public.learning_courses c
   WHERE t.user_id=$1 AND t.course_id=$2 AND t.state='needs_attention' AND t.last_error_code='chapter_closed'
    AND t.desired_state='run' AND c.id=t.course_id AND c.user_id=t.user_id
    AND ch.course_id=c.id AND ch.user_id=t.user_id AND ch.snapshot_version=c.snapshot_version
    AND ch.external_id=t.chapter_external_id AND ch.availability='open'`, userID, courseID, now); err != nil {
		return errServiceUnavailable
	}
	return nil
}
