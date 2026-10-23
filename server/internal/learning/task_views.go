package learning

import "context"

// taskPlanRefsBatch 每页统一读取安排关联，避免每个任务额外占用一次数据库往返。
func (s *Service) taskPlanRefsBatch(ctx context.Context, ids []string) (map[string][]TaskPlanRef, error) {
	refs := make(map[string][]TaskPlanRef, len(ids))
	for _, id := range ids {
		refs[id] = []TaskPlanRef{}
	}
	if len(ids) == 0 {
		return refs, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT b.task_id::text, p.id::text, p.current_revision_id::text, r.version
		FROM public.learning_plan_task_bindings b
		JOIN public.learning_plans p ON p.id=b.plan_id AND p.user_id=b.user_id
		JOIN public.learning_plan_revisions r ON r.id=p.current_revision_id AND r.user_id=p.user_id
		WHERE b.task_id=ANY($1::uuid[]) AND b.plan_revision_id=p.current_revision_id
		ORDER BY b.task_id, p.created_at`, ids)
	if err != nil {
		return nil, errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var ref TaskPlanRef
		if err := rows.Scan(&id, &ref.PlanID, &ref.RevisionID, &ref.Version); err != nil {
			return nil, errServiceUnavailable
		}
		refs[id] = append(refs[id], ref)
	}
	if rows.Err() != nil {
		return nil, errServiceUnavailable
	}
	return refs, nil
}
