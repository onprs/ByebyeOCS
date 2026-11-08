package learning

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// closedTaskErrorCode 区分章节、课程关闭和单独测验关闭，不把整章关闭固化为测验关闭。
func closedTaskErrorCode(ctx context.Context, q rowQuerier, taskID string) (string, error) {
	var code string
	err := q.QueryRow(ctx, `SELECT CASE WHEN c.platform_state='closed' THEN 'course_closed'
   WHEN EXISTS(SELECT 1 FROM public.learning_chapters ch WHERE ch.course_id=t.course_id
    AND ch.user_id=t.user_id AND ch.external_id=t.chapter_external_id
    AND ch.snapshot_version=c.snapshot_version AND ch.availability='closed') THEN 'chapter_closed'
   ELSE 'quiz_closed' END FROM public.learning_tasks t
   JOIN public.learning_courses c ON c.id=t.course_id AND c.user_id=t.user_id WHERE t.id=$1`, taskID).Scan(&code)
	if err != nil {
		return "", errServiceUnavailable
	}
	return code, nil
}

// recordClosedTaskPoint 同步当前附件与稳定任务点可用性，供执行和独立核验共用。
// 不修改任务的完成事实、查询、最终提交、报价或账本。
func recordClosedTaskPoint(ctx context.Context, tx pgx.Tx, taskID string, checkedAt time.Time) error {
	code, err := closedTaskErrorCode(ctx, tx, taskID)
	if err != nil {
		return err
	}
	if code != "quiz_closed" {
		return nil
	}
	if _, err := tx.Exec(ctx, `UPDATE public.learning_attachments a SET
		availability='closed',availability_checked_at=$2
		FROM public.learning_tasks t
		WHERE t.id=$1 AND a.id=t.attachment_id AND a.user_id=t.user_id`, taskID, checkedAt); err != nil {
		return errServiceUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.learning_task_point_availability
		(user_id,course_id,chapter_external_id,task_point_id,cycle_key,availability,checked_at,updated_at)
		SELECT t.user_id,t.course_id,t.chapter_external_id,t.task_point_id,t.cycle_key,'closed',$2,now()
		FROM public.learning_tasks t WHERE t.id=$1
		ON CONFLICT(user_id,course_id,chapter_external_id,task_point_id,cycle_key)
		DO UPDATE SET availability='closed',checked_at=EXCLUDED.checked_at,updated_at=now()`, taskID, checkedAt); err != nil {
		return errServiceUnavailable
	}
	return nil
}
