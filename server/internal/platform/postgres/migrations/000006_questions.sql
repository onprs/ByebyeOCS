-- 主平台题库模块：公开模型条目、查询事实、缓存报告与管理员测试/失效记录。
-- fork 题库使用独立数据库和 tiku schema；本迁移不创建跨库外键，也不存上游密钥。
CREATE TABLE public.question_offerings (
    id uuid PRIMARY KEY,
    public_id text NOT NULL UNIQUE CHECK (public_id ~ '^qm_[0-9a-f]{32}$'),
    display_name text NOT NULL CHECK (length(btrim(display_name)) BETWEEN 1 AND 80),
    description text NOT NULL DEFAULT '' CHECK (length(description) <= 500),
    provider_ref text NOT NULL CHECK (length(btrim(provider_ref)) BETWEEN 1 AND 128),
    capabilities jsonb NOT NULL DEFAULT '["text"]'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    is_default boolean NOT NULL DEFAULT false,
    sort_order integer NOT NULL DEFAULT 0,
    price_version text NOT NULL DEFAULT '' CHECK (length(price_version) <= 64),
    pricing jsonb CHECK (pricing IS NULL OR jsonb_typeof(pricing) = 'object'),
    cache_policy jsonb NOT NULL DEFAULT '{"allow":true,"scope":"user","ttl_days":7}'::jsonb,
    health text NOT NULL DEFAULT 'configured' CHECK (health IN ('configured', 'healthy', 'degraded', 'unavailable')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX question_offerings_one_default_idx
    ON public.question_offerings ((true)) WHERE is_default;
CREATE INDEX question_offerings_visible_idx
    ON public.question_offerings (enabled, health, sort_order, id);

CREATE TABLE public.question_queries (
    id text PRIMARY KEY CHECK (id ~ '^qq_[0-9a-f]{32}$'),
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 1 AND 200),
    request_fingerprint text NOT NULL CHECK (length(request_fingerprint) = 64),
    offering_id uuid NOT NULL REFERENCES public.question_offerings(id) ON DELETE RESTRICT,
    offering_public_id text NOT NULL,
    offering_display_name text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('cache_first', 'always_llm')),
    cache_scope text NOT NULL CHECK (cache_scope IN ('user', 'shared')),
    scope_key_hash text NOT NULL,
    question_title text,
    question_options text DEFAULT '',
    question_type text NOT NULL CHECK (question_type IN ('single', 'multiple', 'judgement', 'completion')),
    image_count integer NOT NULL DEFAULT 0 CHECK (image_count >= 0),
    status text NOT NULL CHECK (status IN ('pending', 'answered', 'cache_hit', 'needs_review', 'failed')),
    answer text,
    cache_hit boolean NOT NULL DEFAULT false,
    cache_entry_id text,
    cache_created_at timestamptz,
    usage jsonb NOT NULL DEFAULT '{}'::jsonb,
    pricing jsonb NOT NULL DEFAULT '{}'::jsonb,
    billing_state text NOT NULL DEFAULT 'pending',
    billing_hold_id text,
    reserved_minor bigint NOT NULL DEFAULT 0 CHECK (reserved_minor >= 0),
    content_expired boolean NOT NULL DEFAULT false,
    error_code text,
    fork_request_id text NOT NULL UNIQUE,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (user_id, idempotency_key)
);

CREATE INDEX question_queries_user_idx ON public.question_queries (user_id, created_at DESC, id DESC);
CREATE INDEX question_queries_admin_idx ON public.question_queries (created_at DESC, id DESC);
CREATE INDEX question_queries_offering_idx ON public.question_queries (offering_id, created_at DESC);

CREATE TABLE public.question_cache_reports (
    id uuid PRIMARY KEY,
    query_id text NOT NULL REFERENCES public.question_queries(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_at timestamptz
);

CREATE INDEX question_cache_reports_pending_idx ON public.question_cache_reports (status, created_at);

CREATE TABLE public.question_model_tests (
    id uuid PRIMARY KEY,
    offering_id uuid NOT NULL REFERENCES public.question_offerings(id) ON DELETE RESTRICT,
    actor_admin_id uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    kind text NOT NULL CHECK (kind IN ('reachability', 'functional')),
    status text NOT NULL CHECK (status IN ('ok', 'failed')),
    error_code text,
    latency_ms bigint NOT NULL DEFAULT 0 CHECK (latency_ms >= 0),
    cost_confirmed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (kind = 'functional' OR cost_confirmed = false)
);

CREATE INDEX question_model_tests_offering_idx ON public.question_model_tests (offering_id, created_at DESC);

CREATE TABLE public.question_cache_invalidations (
    id uuid PRIMARY KEY,
    offering_id uuid REFERENCES public.question_offerings(id) ON DELETE RESTRICT,
    cache_entry_id text NOT NULL CHECK (length(btrim(cache_entry_id)) BETWEEN 1 AND 200),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 1000),
    actor_admin_id uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    fork_invalidated boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX question_cache_invalidations_created_idx ON public.question_cache_invalidations (created_at DESC);

-- 平台提交需要独立的能力适配器与人工确认；当前只记录被门槛阻止的尝试，不写伪成功。
CREATE TABLE public.question_submissions (
    id uuid PRIMARY KEY,
    query_id text NOT NULL REFERENCES public.question_queries(id) ON DELETE RESTRICT,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    answer_sha256 text NOT NULL CHECK (length(answer_sha256) = 64),
    status text NOT NULL CHECK (status IN ('blocked', 'rejected')),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 500),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX question_submissions_query_idx ON public.question_submissions (query_id, created_at DESC);
