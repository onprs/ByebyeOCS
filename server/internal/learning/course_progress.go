package learning

import (
	"context"
	"github.com/jackc/pgx/v5"
)

// 目录快照保持原始事实；课程读模型叠加同一稳定任务点更新的平台核验结果。
// 执行观察、播放器时长和任务运行状态均不能替代平台确认。
const courseTaskEvidenceNewerSQL = `(t.platform_checked_at IS NOT NULL
	AND (a.progress_checked_at IS NULL OR t.platform_checked_at>=a.progress_checked_at))`
const courseTaskProgressNewerSQL = `(` + courseTaskEvidenceNewerSQL + `
	AND t.platform_status IN ('confirmed','unconfirmed')
	AND (t.platform_status='confirmed' OR COALESCE(t.platform_check->>'source',t.platform_check->>'Source','')<>'course_snapshot'))`
const coursePlayValueSQL = `COALESCE(NULLIF(t.platform_check->'play_time_seconds','null'::jsonb),t.platform_check->'PlayTimeSeconds')`
const courseTaskPlayKnownSQL = `(` + courseTaskEvidenceNewerSQL + ` AND jsonb_typeof(` + coursePlayValueSQL + `)='number')`
const coursePlayedSQL = `(CASE WHEN ` + courseTaskPlayKnownSQL + `
	THEN GREATEST(0::double precision,(` + coursePlayValueSQL + ` #>> '{}')::double precision)
	ELSE a.played_seconds END)`

const coursePassedSQL = `(CASE WHEN ` + courseTaskProgressNewerSQL + `
	THEN t.platform_status='confirmed' ELSE a.platform_passed AND a.passed_known END)`
const coursePassedKnownSQL = `(a.passed_known OR ` + courseTaskProgressNewerSQL + `)`
const courseCheckedAtSQL = `(CASE WHEN ` + courseTaskProgressNewerSQL + ` OR ` + courseTaskPlayKnownSQL + `
	THEN t.platform_checked_at ELSE a.progress_checked_at END)`
const courseAvailabilitySQL = `(CASE WHEN ch.availability='closed' THEN 'closed' WHEN v.checked_at IS NOT NULL
	AND (a.availability_checked_at IS NULL OR v.checked_at>=a.availability_checked_at)
	THEN v.availability ELSE a.availability END)`

// 以用户、课程、章节、任务点及学期关联，避免同媒体多任务点或旧附件 UUID 混淆。
const courseProgressRelationsSQL = `FROM public.learning_attachments a
	JOIN public.learning_courses c ON c.id=a.course_id AND c.user_id=a.user_id
		AND a.snapshot_version=c.snapshot_version
	JOIN public.learning_chapters ch ON ch.id=a.chapter_id AND ch.user_id=a.user_id
	LEFT JOIN public.learning_tasks t ON t.user_id=a.user_id AND t.course_id=a.course_id
		AND t.chapter_external_id=ch.external_id AND t.task_point_id=a.task_point_id AND t.cycle_key=c.cycle_key
		AND a.is_task_point
	LEFT JOIN public.learning_task_point_availability v ON v.user_id=a.user_id AND v.course_id=a.course_id
		AND v.chapter_external_id=ch.external_id AND v.task_point_id=a.task_point_id AND v.cycle_key=c.cycle_key
		AND a.is_task_point`

// enrichCourseProgress 在一个有界批量查询中更新整页课程，列表与详情共用相同投影。
func enrichCourseProgress(ctx context.Context, q pgx.Tx, userID string, courses []CourseSummary) error {
	if len(courses) == 0 {
		return nil
	}
	ids := make([]string, len(courses))
	indices := make(map[string]int, len(courses))
	for index, course := range courses {
		ids[index] = course.ID
		indices[course.ID] = index
	}
	rows, err := q.Query(ctx, `SELECT c.id::text,
		count(*) FILTER(WHERE a.is_task_point),
		count(*) FILTER(WHERE a.is_task_point AND NOT `+coursePassedSQL+`),
		count(*) FILTER(WHERE a.is_task_point AND NOT `+coursePassedSQL+` AND `+courseAvailabilitySQL+`='closed'),
		count(*) FILTER(WHERE NOT a.is_task_point)
		`+courseProgressRelationsSQL+` WHERE c.id=ANY($1::uuid[]) AND c.user_id=$2 GROUP BY c.id`, ids, userID)
	if err != nil {
		return errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var total, incomplete, closed, resources int
		if err := rows.Scan(&id, &total, &incomplete, &closed, &resources); err != nil {
			return errServiceUnavailable
		}
		index, ok := indices[id]
		if !ok {
			continue
		}
		courses[index].TaskPointCount = total
		courses[index].IncompleteTaskPointCount = incomplete
		courses[index].ClosedTaskPointCount = closed
		courses[index].ResourceCount = resources
	}
	if rows.Err() != nil {
		return errServiceUnavailable
	}
	return nil
}
