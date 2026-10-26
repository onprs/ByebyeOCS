-- 共享章节卡片目录仅保存页码，不保存账号、CPI、会话或个人学习状态。
CREATE TABLE public.learning_resource_cache (
    cache_key text PRIMARY KEY CHECK (cache_key ~ '^[0-9a-f]{64}$'),
    card_indexes integer[] NOT NULL CHECK (cardinality(card_indexes) BETWEEN 1 AND 32),
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX learning_resource_cache_expiry_idx ON public.learning_resource_cache (expires_at);
