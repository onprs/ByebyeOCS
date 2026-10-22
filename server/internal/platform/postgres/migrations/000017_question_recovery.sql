-- 陈旧题库请求恢复：只冻结内部模型引用，原输入由同一幂等请求重新提供。
ALTER TABLE public.question_queries
    ADD COLUMN offering_provider_ref text
    CHECK (offering_provider_ref IS NULL OR length(btrim(offering_provider_ref)) BETWEEN 1 AND 128);

COMMENT ON COLUMN public.question_queries.offering_provider_ref IS
    '创建时冻结的题库模型引用；恢复沿用原 request_id，完成或进入复核后清空，不保存题目或图片。';

CREATE INDEX question_queries_pending_recovery_idx
    ON public.question_queries (updated_at) WHERE status = 'pending';
