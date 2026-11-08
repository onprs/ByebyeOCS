package learning

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const planViewSelect = `SELECT p.id::text, p.name, p.status, p.version, p.priority,
	p.current_revision_id::text, p.created_at, p.updated_at,
	r.course_ids::text[], r.scope_kind, r.task_types,
	r.question_policy_enabled, r.question_model_offering_id, r.question_mode
	FROM public.learning_plans p
	JOIN public.learning_plan_revisions r ON r.id=p.current_revision_id AND r.user_id=p.user_id`

func (s *Service) scanPlanView(row pgx.Row) (PlanView, error) {
	var item PlanView
	err := row.Scan(&item.ID, &item.Name, &item.Status, &item.Version, &item.Priority,
		&item.CurrentRevisionID, &item.CreatedAt, &item.UpdatedAt,
		&item.Scope.CourseIDs, &item.Scope.Kind, &item.TaskTypes,
		&item.QuestionPolicy.Enabled, &item.QuestionPolicy.ModelOfferingID, &item.QuestionPolicy.AnswerMode)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, errNotFound
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	item.QuestionPolicy.ExecutionAvailable = s.questionExecutionAvailable()
	item.QuestionPolicy.SubmissionAvailable = s.questionExecutionAvailable()
	return item, nil
}

// populatePlanChapters 把整页选定章节一次解析到当前快照，保持稳定外部章节身份。
func populatePlanChapters(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, user string, items []PlanView) error {
	ids := make([]string, 0, len(items))
	index := make(map[string]int, len(items))
	for i := range items {
		if items[i].Scope.Kind == "selected" {
			ids = append(ids, items[i].CurrentRevisionID)
			index[items[i].CurrentRevisionID] = i
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.Query(ctx, `SELECT DISTINCT r.id::text, c.course_id::text, COALESCE(cur.id,c.id)::text
		FROM public.learning_plan_revisions r
		JOIN public.learning_chapters c ON c.id=ANY(r.chapter_ids) AND c.user_id=r.user_id
		LEFT JOIN LATERAL (
			SELECT ch.id FROM public.learning_chapters ch
			JOIN public.learning_courses lc ON lc.id=ch.course_id AND lc.user_id=ch.user_id
			WHERE ch.course_id=c.course_id AND ch.user_id=c.user_id AND ch.external_id=c.external_id
			  AND ch.snapshot_version=lc.snapshot_version LIMIT 1
		) cur ON true
		WHERE r.id=ANY($1::uuid[]) AND r.user_id=$2 ORDER BY 1,2,3`, ids, user)
	if err != nil {
		return errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var chapter PlanChapter
		if err := rows.Scan(&id, &chapter.CourseID, &chapter.ChapterID); err != nil {
			return errServiceUnavailable
		}
		i, ok := index[id]
		if !ok {
			return errServiceUnavailable
		}
		items[i].Scope.Chapters = append(items[i].Scope.Chapters, chapter)
	}
	if rows.Err() != nil {
		return errServiceUnavailable
	}
	return nil
}

// populatePlanProgress 批量汇总当前版本绑定任务的状态，避免任务列表分页造成计数偏差。
func populatePlanProgress(ctx context.Context, q interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, user string, items []PlanView) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, 0, len(items))
	index := make(map[string]int, len(items))
	for i := range items {
		items[i].Progress = PlanProgressView{}
		ids = append(ids, items[i].ID)
		index[items[i].ID] = i
	}
	rows, err := q.Query(ctx, `SELECT b.plan_id::text,`+taskProgressTotalsSQL+`
		FROM public.learning_plan_task_bindings b
		JOIN public.learning_tasks t ON t.id = b.task_id AND t.user_id = b.user_id
		JOIN public.learning_plans p ON p.id = b.plan_id AND p.user_id = b.user_id
			AND p.current_revision_id = b.plan_revision_id
		JOIN public.learning_plan_revisions r ON r.id=p.current_revision_id AND r.user_id=p.user_id
		WHERE p.user_id = $1 AND p.id = ANY($2::uuid[]) AND `+taskMatchesRevisionSQL+`
		GROUP BY b.plan_id`, user, ids)
	if err != nil {
		return errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var progress PlanProgressView
		if err := rows.Scan(&id, &progress.Total, &progress.Completed, &progress.Active,
			&progress.Attention, &progress.Closed, &progress.Cancelled); err != nil {
			return errServiceUnavailable
		}
		progress.Percent = planProgressPercent(progress)
		if i, ok := index[id]; ok {
			items[i].Progress = progress
		}
	}
	if rows.Err() != nil {
		return errServiceUnavailable
	}
	return nil
}

// planProgressPercent 按可执行任务计算完成率，取消和平台已关闭的任务不计入分母。
func planProgressPercent(progress PlanProgressView) int {
	denominator := progress.Total - progress.Cancelled - progress.Closed
	if denominator <= 0 {
		return 0
	}
	return (progress.Completed*100 + denominator/2) / denominator
}
