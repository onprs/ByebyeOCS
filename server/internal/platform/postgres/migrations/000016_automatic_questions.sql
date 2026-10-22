-- 全自动章节测验：取消旧人工确认约束，保留旧版本快照不变。
ALTER TABLE public.learning_plan_revisions
    DROP CONSTRAINT learning_plan_revisions_manual_confirmation_check;
ALTER TABLE public.learning_plan_revisions
    ALTER COLUMN require_manual_confirmation SET DEFAULT false;

ALTER TABLE public.question_queries
    DROP CONSTRAINT question_queries_question_type_check;
ALTER TABLE public.question_queries
    ADD CONSTRAINT question_queries_question_type_check
    CHECK (question_type IN ('single', 'multiple', 'judgement', 'completion', 'shortanswer'));

ALTER TABLE public.learning_tasks
    DROP CONSTRAINT learning_tasks_verification_reason_check;
ALTER TABLE public.learning_tasks
    ADD CONSTRAINT learning_tasks_verification_reason_check
    CHECK (verification_reason IN ('', 'execution_completed', 'crash_recovery', 'question_submission'));

-- 只保存编排与幂等事实。原题、模型答案及用量仍由 question_queries 管理。
CREATE TABLE public.learning_work_runs (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    plan_revision_id uuid NOT NULL,
    paper_fingerprint text NOT NULL CHECK (paper_fingerprint ~ '^[0-9a-f]{64}$'),
    model_offering_id text NOT NULL CHECK (length(btrim(model_offering_id)) BETWEEN 1 AND 128),
    answer_mode text NOT NULL CHECK (answer_mode IN ('cache_first', 'always_llm')),
    state text NOT NULL DEFAULT 'querying'
        CHECK (state IN ('querying', 'submitting', 'submitted', 'submission_unknown')),
    query_ids text[] NOT NULL DEFAULT '{}' CHECK (cardinality(query_ids) <= 100),
    answer_digest text NOT NULL DEFAULT '' CHECK (answer_digest = '' OR answer_digest ~ '^[0-9a-f]{64}$'),
    submission_attempt_id uuid REFERENCES public.learning_task_attempts(id) ON DELETE RESTRICT,
    submitted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, plan_revision_id, paper_fingerprint),
    FOREIGN KEY (task_id, user_id) REFERENCES public.learning_tasks(id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (plan_revision_id, user_id) REFERENCES public.learning_plan_revisions(id, user_id) ON DELETE RESTRICT,
    CHECK (state = 'querying' OR (submission_attempt_id IS NOT NULL AND answer_digest <> ''))
);

-- 最终提交以任务为单位独占，跨安排版本、重启与租约恢复也不能重复提交。
CREATE UNIQUE INDEX learning_work_runs_submission_idx ON public.learning_work_runs(task_id)
    WHERE state IN ('submitting', 'submitted', 'submission_unknown');
CREATE INDEX learning_work_runs_user_idx ON public.learning_work_runs(user_id, created_at DESC);

COMMENT ON TABLE public.learning_work_runs IS
    '自动做题编排事实；最终提交结果不明时仅核验，不自动重放提交。';
