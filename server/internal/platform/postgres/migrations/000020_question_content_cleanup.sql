-- 题库原题/答案内容按 7 天保留清理；只为可清理终态行建部分索引，
-- 避免每日批次在历史行上反复扫描。过滤条件与 questions.RunCleanup 及
-- operations 的账务异常保护保持一致：pending/needs_review 不在索引与清理范围内。
CREATE INDEX question_queries_content_cleanup_idx
    ON public.question_queries (completed_at)
    WHERE NOT content_expired
      AND status IN ('answered', 'cache_hit', 'failed')
      AND billing_state NOT IN ('pending', 'needs_review', 'usage_incomplete');
