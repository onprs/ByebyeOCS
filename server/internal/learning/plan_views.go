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
