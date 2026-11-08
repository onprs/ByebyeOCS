package learning

// taskClosedSQL 与所有任务分类、安排汇总、分页元数据共用同一关闭判定。
const taskClosedSQL = `(EXISTS(SELECT 1 FROM public.learning_courses tc
 WHERE tc.id=t.course_id AND tc.user_id=t.user_id AND tc.platform_state='closed')
 OR EXISTS(SELECT 1 FROM public.learning_chapters tch JOIN public.learning_courses tc
 ON tc.id=tch.course_id AND tc.user_id=tch.user_id
 WHERE tch.user_id=t.user_id AND tch.course_id=t.course_id AND tch.external_id=t.chapter_external_id
 AND tch.snapshot_version=tc.snapshot_version AND tch.availability='closed')
 OR EXISTS(SELECT 1 FROM public.learning_task_point_availability tv
 WHERE tv.user_id=t.user_id AND tv.course_id=t.course_id AND tv.chapter_external_id=t.chapter_external_id
 AND tv.task_point_id=t.task_point_id AND tv.cycle_key=t.cycle_key AND tv.availability='closed')
 OR t.last_error_code IN ('quiz_closed','course_closed','chapter_closed'))`

const taskCategorySQL = `(CASE WHEN t.state IN ('confirmed','cancelled') THEN 'history'
 WHEN ` + taskClosedSQL + ` THEN 'closed'
 WHEN t.state IN ('needs_attention','failed') THEN 'attention' ELSE 'current' END)`

// 当前版本的绑定还必须符合该版本的课程、任务类型和稳定章节范围。
const taskMatchesRevisionSQL = `t.course_id=ANY(r.course_ids) AND t.task_kind=ANY(r.task_types)
 AND (r.scope_kind='all' OR EXISTS(SELECT 1 FROM public.learning_chapters sc
 WHERE sc.id=ANY(r.chapter_ids) AND sc.course_id=t.course_id
 AND sc.user_id=t.user_id AND sc.external_id=t.chapter_external_id))`

const currentTaskBindingSQL = `EXISTS(SELECT 1 FROM public.learning_plan_task_bindings b
 JOIN public.learning_plans p ON p.id=b.plan_id AND p.user_id=b.user_id
 JOIN public.learning_plan_revisions r ON r.id=p.current_revision_id AND r.user_id=p.user_id
 WHERE b.task_id=t.id AND b.user_id=t.user_id AND b.plan_revision_id=p.current_revision_id
 AND p.id=$4::uuid AND ` + taskMatchesRevisionSQL + `)`

type TaskPageMetadata struct {
	PlanRevisionID string           `json:"plan_revision_id"`
	Category       string           `json:"category"`
	Progress       PlanProgressView `json:"progress"`
}
