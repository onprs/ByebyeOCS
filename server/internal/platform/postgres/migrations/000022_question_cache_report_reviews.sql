-- 管理员缓存报告复核事实与幂等键。
-- 报告只能进入一个终态；同一管理员、幂等键只绑定一个报告/状态/理由。
CREATE TABLE public.question_cache_report_reviews (
    id uuid PRIMARY KEY,
    report_id uuid NOT NULL REFERENCES public.question_cache_reports(id) ON DELETE RESTRICT,
    actor_admin_id uuid NOT NULL REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    status text NOT NULL CHECK (status IN ('resolved', 'rejected')),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 5 AND 500),
    reviewed_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT question_cache_report_reviews_idempotency_uq UNIQUE (actor_admin_id, idempotency_key),
    CONSTRAINT question_cache_report_reviews_report_uq UNIQUE (report_id)
);

-- 管理员按状态分页读取报告；created_at 与 id 与游标顺序一致。
CREATE INDEX question_cache_reports_admin_idx
    ON public.question_cache_reports (status, created_at DESC, id DESC);
