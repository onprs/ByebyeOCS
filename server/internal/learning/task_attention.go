package learning

// actionableTaskSQL 限定当前未归档安排的可处理异常，分类与安排进度共用相同判定。
const actionableTaskSQL = taskCategorySQL + `='attention'
 AND EXISTS(SELECT 1 FROM public.learning_plan_task_bindings b
 JOIN public.learning_plans p ON p.id=b.plan_id AND p.user_id=b.user_id
 JOIN public.learning_plan_revisions r ON r.id=p.current_revision_id AND r.user_id=p.user_id
 WHERE b.task_id=t.id AND b.user_id=t.user_id AND b.plan_revision_id=p.current_revision_id
 AND p.status<>'archived' AND ($4='' OR p.id=$4::uuid) AND ` + taskMatchesRevisionSQL + `)`
