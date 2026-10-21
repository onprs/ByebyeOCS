-- 审计保留原生 UUID 关联，并允许题库等模块的稳定公开编号。
ALTER TABLE public.audit_events ADD COLUMN resource_key text CHECK (resource_key IS NULL OR length(resource_key) BETWEEN 1 AND 200);

-- 提交后的投递顺序由独立流头生成，不能把序列主键当业务提交顺序。
CREATE TABLE public.outbox_events (
 id uuid PRIMARY KEY,
 audience text NOT NULL CHECK (audience IN ('user','admin')),
 user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
 event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 128),
 resource_id text CHECK (resource_id IS NULL OR length(resource_id) BETWEEN 1 AND 200),
 resource_version bigint NOT NULL CHECK (resource_version >= 0),
 payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(payload) = 'object'),
 occurred_at timestamptz NOT NULL DEFAULT now(),
 published_at timestamptz,
 CHECK ((audience='user' AND user_id IS NOT NULL) OR (audience='admin' AND user_id IS NULL))
);
CREATE INDEX outbox_events_pending_idx ON public.outbox_events(occurred_at,id) WHERE published_at IS NULL;
CREATE INDEX outbox_events_published_idx ON public.outbox_events(published_at) WHERE published_at IS NOT NULL;

CREATE TABLE public.stream_heads (
 stream_key text PRIMARY KEY,
 sequence bigint NOT NULL DEFAULT 0 CHECK (sequence >= 0),
 earliest_sequence bigint NOT NULL DEFAULT 1 CHECK (earliest_sequence > 0),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE public.stream_entries (
 stream_key text NOT NULL REFERENCES public.stream_heads(stream_key) ON DELETE RESTRICT,
 sequence bigint NOT NULL CHECK (sequence > 0),
 event_id uuid NOT NULL REFERENCES public.outbox_events(id) ON DELETE RESTRICT,
 event_type text NOT NULL,
 resource_id text,
 resource_version bigint NOT NULL,
 payload jsonb NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(stream_key, sequence),
 UNIQUE(stream_key,event_id)
);
CREATE INDEX stream_entries_created_idx ON public.stream_entries(created_at);

CREATE TABLE public.data_requests (
 id uuid PRIMARY KEY,
 user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
 kind text NOT NULL CHECK (kind IN ('export','deletion')),
 status text NOT NULL CHECK (status IN ('pending','processing','ready','needs_review','completed','failed','expired')),
 result_ciphertext bytea,
 expires_at timestamptz,
 error_code text,
 review_reason text,
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 completed_at timestamptz
);
CREATE UNIQUE INDEX data_requests_active_idx ON public.data_requests(user_id,kind) WHERE status IN ('pending','processing','needs_review');
CREATE INDEX data_requests_pending_idx ON public.data_requests(created_at,id) WHERE status='pending';
CREATE INDEX data_requests_expiry_idx ON public.data_requests(expires_at) WHERE expires_at IS NOT NULL;
