-- 课程快照、安排版本、规范任务、租约尝试与业务事件。
-- 不建立资金账本，不复制认证表结构；外部身份命名空间在本模块内独立核对。
CREATE TABLE public.learning_accounts (
    user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE RESTRICT,
    provider text NOT NULL CHECK (provider IN ('chaoxing')),
    identity_namespace text NOT NULL CHECK (length(btrim(identity_namespace)) BETWEEN 1 AND 128),
    identity_fingerprint text NOT NULL CHECK (length(btrim(identity_fingerprint)) BETWEEN 32 AND 128),
    school text NOT NULL DEFAULT '',
    display_name text NOT NULL DEFAULT '',
    blocked_code text NOT NULL DEFAULT '',
    blocked_reason text NOT NULL DEFAULT '',
    blocked_at timestamptz,
    blocked_until timestamptz,
    last_claimed_at timestamptz,
    verified_at timestamptz NOT NULL,
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, provider, identity_namespace, identity_fingerprint),
    UNIQUE (provider, identity_namespace, identity_fingerprint)
);

CREATE TABLE public.learning_courses (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    provider text NOT NULL CHECK (provider = 'chaoxing'),
    identity_namespace text NOT NULL,
    identity_fingerprint text NOT NULL,
    external_course_id text NOT NULL CHECK (length(btrim(external_course_id)) BETWEEN 1 AND 128),
    external_class_id text NOT NULL CHECK (length(btrim(external_class_id)) BETWEEN 1 AND 128),
    external_cpi text NOT NULL DEFAULT '',
    cycle_key text NOT NULL DEFAULT 'current' CHECK (length(btrim(cycle_key)) BETWEEN 1 AND 64),
    title text NOT NULL DEFAULT '',
    teacher text NOT NULL DEFAULT '',
    term text NOT NULL DEFAULT '',
    snapshot_version bigint NOT NULL DEFAULT 0 CHECK (snapshot_version >= 0),
    synced_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    UNIQUE (id, user_id),
    UNIQUE (user_id, provider, external_course_id, external_class_id, cycle_key),
    FOREIGN KEY (user_id, provider, identity_namespace, identity_fingerprint)
        REFERENCES public.learning_accounts (user_id, provider, identity_namespace, identity_fingerprint)
        ON DELETE RESTRICT
);

CREATE INDEX learning_courses_user_updated_idx ON public.learning_courses (user_id, updated_at DESC, id DESC);

CREATE TABLE public.learning_course_snapshots (
    id uuid PRIMARY KEY,
    course_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    version bigint NOT NULL CHECK (version > 0),
    captured_at timestamptz NOT NULL,
    adapter text NOT NULL CHECK (length(btrim(adapter)) BETWEEN 1 AND 64),
    chapter_count integer NOT NULL DEFAULT 0 CHECK (chapter_count >= 0),
    attachment_count integer NOT NULL DEFAULT 0 CHECK (attachment_count >= 0),
    task_point_count integer NOT NULL DEFAULT 0 CHECK (task_point_count >= 0),
    incomplete_task_point_count integer NOT NULL DEFAULT 0 CHECK (incomplete_task_point_count >= 0),
    skipped_attachment_count integer NOT NULL DEFAULT 0 CHECK (skipped_attachment_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (course_id, version),
    UNIQUE (id, user_id),
    FOREIGN KEY (course_id, user_id) REFERENCES public.learning_courses (id, user_id) ON DELETE RESTRICT
);

CREATE TABLE public.learning_chapters (
    id uuid PRIMARY KEY,
    course_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    snapshot_version bigint NOT NULL CHECK (snapshot_version > 0),
    external_id text NOT NULL CHECK (length(btrim(external_id)) BETWEEN 1 AND 128),
    parent_external_id text NOT NULL DEFAULT '',
    title text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (course_id, snapshot_version, external_id),
    UNIQUE (id, user_id),
    UNIQUE (id, course_id, user_id),
    FOREIGN KEY (course_id, user_id) REFERENCES public.learning_courses (id, user_id) ON DELETE RESTRICT
);

CREATE TABLE public.learning_attachments (
    id uuid PRIMARY KEY,
    course_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    snapshot_version bigint NOT NULL CHECK (snapshot_version > 0),
    external_object_id text NOT NULL CHECK (length(btrim(external_object_id)) BETWEEN 1 AND 256),
    task_point_id text NOT NULL DEFAULT '',
    kind text NOT NULL CHECK (length(btrim(kind)) BETWEEN 1 AND 32),
    title text NOT NULL DEFAULT '',
    duration_seconds double precision NOT NULL DEFAULT 0 CHECK (duration_seconds >= 0),
    played_seconds double precision NOT NULL DEFAULT 0 CHECK (played_seconds >= 0),
    platform_passed boolean NOT NULL DEFAULT false,
    passed_known boolean NOT NULL DEFAULT false,
    is_task_point boolean NOT NULL,
    counted_incomplete boolean NOT NULL,
    source_fingerprint text NOT NULL DEFAULT '',
    sort_order integer NOT NULL DEFAULT 0,
    progress_checked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (is_task_point = (length(btrim(task_point_id)) > 0)),
    CHECK (NOT counted_incomplete OR (is_task_point AND NOT platform_passed)),
    UNIQUE (chapter_id, snapshot_version, external_object_id),
    UNIQUE (id, user_id),
    UNIQUE (id, course_id, user_id),
    FOREIGN KEY (course_id, user_id) REFERENCES public.learning_courses (id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (chapter_id, course_id, user_id) REFERENCES public.learning_chapters (id, course_id, user_id) ON DELETE RESTRICT
);

CREATE INDEX learning_attachments_incomplete_idx ON public.learning_attachments (user_id, course_id, counted_incomplete);

CREATE TABLE public.learning_plans (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'paused', 'archived')),
    version bigint NOT NULL DEFAULT 0 CHECK (version >= 0),
    current_revision_id uuid,
    priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, user_id)
);

CREATE INDEX learning_plans_user_created_idx ON public.learning_plans (user_id, created_at DESC, id DESC);

CREATE TABLE public.learning_plan_revisions (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    version bigint NOT NULL CHECK (version > 0),
    course_ids uuid[] NOT NULL CHECK (cardinality(course_ids) BETWEEN 1 AND 20),
    scope_kind text NOT NULL CHECK (scope_kind IN ('all', 'selected')),
    chapter_ids uuid[] NOT NULL DEFAULT '{}',
    task_types text[] NOT NULL CHECK (cardinality(task_types) BETWEEN 1 AND 8),
    priority integer NOT NULL CHECK (priority BETWEEN 0 AND 100),
    created_by_kind text NOT NULL CHECK (created_by_kind IN ('user', 'admin', 'system')),
    created_by_id uuid,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (scope_kind <> 'selected' OR cardinality(chapter_ids) > 0),
    UNIQUE (plan_id, version),
    UNIQUE (id, user_id),
    FOREIGN KEY (plan_id, user_id) REFERENCES public.learning_plans (id, user_id) ON DELETE RESTRICT
);

CREATE INDEX learning_plan_revisions_plan_idx ON public.learning_plan_revisions (plan_id, version DESC);

ALTER TABLE public.learning_plans
    ADD CONSTRAINT learning_plans_current_revision_fk
    FOREIGN KEY (current_revision_id, user_id)
    REFERENCES public.learning_plan_revisions (id, user_id)
    ON DELETE RESTRICT;

CREATE TABLE public.learning_tasks (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    course_id uuid NOT NULL,
    chapter_id uuid NOT NULL,
    attachment_id uuid NOT NULL,
    provider text NOT NULL CHECK (provider = 'chaoxing'),
    identity_namespace text NOT NULL,
    identity_fingerprint text NOT NULL,
    cycle_key text NOT NULL CHECK (length(btrim(cycle_key)) BETWEEN 1 AND 64),
    course_external_id text NOT NULL,
    class_external_id text NOT NULL,
    chapter_external_id text NOT NULL,
    task_point_id text NOT NULL CHECK (length(btrim(task_point_id)) BETWEEN 1 AND 256),
    task_kind text NOT NULL CHECK (length(btrim(task_kind)) BETWEEN 1 AND 32),
    title text NOT NULL DEFAULT '',
    state text NOT NULL DEFAULT 'pending' CHECK (state IN (
        'pending', 'preparing', 'running', 'waiting_verification',
        'confirmed', 'paused', 'needs_attention', 'failed', 'cancelled')),
    desired_state text NOT NULL DEFAULT 'run' CHECK (desired_state IN ('run', 'pause', 'cancel')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    priority integer NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 100),
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    failure_count integer NOT NULL DEFAULT 0 CHECK (failure_count >= 0),
    verification_attempts integer NOT NULL DEFAULT 0 CHECK (verification_attempts >= 0),
    next_attempt_at timestamptz,
    next_verify_at timestamptz,
    last_error_code text NOT NULL DEFAULT '',
    last_error_scope text NOT NULL DEFAULT '',
    lease_attempt_id uuid,
    lease_owner text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    fence_token bigint NOT NULL DEFAULT 0 CHECK (fence_token >= 0),
    stop_requested_at timestamptz,
    confirmed_at timestamptz,
    platform_status text NOT NULL DEFAULT 'unconfirmed' CHECK (platform_status IN ('unconfirmed', 'confirmed', 'unknown')),
    platform_checked_at timestamptz,
    observation jsonb NOT NULL DEFAULT '{}'::jsonb,
    platform_check jsonb NOT NULL DEFAULT '{}'::jsonb,
    media_evidence jsonb NOT NULL DEFAULT '{"available": false}'::jsonb,
    verification_reason text NOT NULL DEFAULT '' CHECK (verification_reason IN ('', 'execution_completed', 'crash_recovery')),
    billing_state text NOT NULL DEFAULT 'disabled' CHECK (billing_state IN (
        'disabled', 'unpriced', 'quoted', 'reserved', 'settled', 'release_pending', 'review_required', 'settlement_failed')),
    billing_hold_id text NOT NULL DEFAULT '',
    billing_quote jsonb NOT NULL DEFAULT '{}'::jsonb,
    billing_error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider, identity_namespace, identity_fingerprint, course_external_id,
        class_external_id, chapter_external_id, task_point_id, cycle_key),
    UNIQUE (id, user_id),
    FOREIGN KEY (user_id, provider, identity_namespace, identity_fingerprint)
        REFERENCES public.learning_accounts (user_id, provider, identity_namespace, identity_fingerprint)
        ON DELETE RESTRICT,
    FOREIGN KEY (course_id, user_id) REFERENCES public.learning_courses (id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (chapter_id, course_id, user_id) REFERENCES public.learning_chapters (id, course_id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (attachment_id, course_id, user_id) REFERENCES public.learning_attachments (id, course_id, user_id) ON DELETE RESTRICT
);

CREATE INDEX learning_tasks_claim_idx ON public.learning_tasks (state, desired_state, next_attempt_at, priority DESC, created_at)
    WHERE state IN ('pending', 'failed');
CREATE INDEX learning_tasks_user_state_idx ON public.learning_tasks (user_id, state, created_at DESC);
CREATE INDEX learning_tasks_verify_idx ON public.learning_tasks (next_verify_at)
    WHERE state = 'waiting_verification';

CREATE TABLE public.learning_plan_task_bindings (
    plan_id uuid NOT NULL,
    plan_revision_id uuid NOT NULL,
    task_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, plan_revision_id, task_id),
    FOREIGN KEY (plan_id, user_id) REFERENCES public.learning_plans (id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (plan_revision_id, user_id) REFERENCES public.learning_plan_revisions (id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (task_id, user_id) REFERENCES public.learning_tasks (id, user_id) ON DELETE RESTRICT
);

CREATE INDEX learning_plan_task_bindings_task_idx ON public.learning_plan_task_bindings (task_id, plan_id);

CREATE TABLE public.learning_task_attempts (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    plan_revision_id uuid,
    attempt_no integer NOT NULL CHECK (attempt_no > 0),
    owner text NOT NULL CHECK (length(btrim(owner)) BETWEEN 1 AND 128),
    fence_token bigint NOT NULL CHECK (fence_token > 0),
    state text NOT NULL CHECK (state IN ('preparing', 'running', 'waiting_verification', 'ended')),
    lease_expires_at timestamptz NOT NULL,
    started_at timestamptz NOT NULL DEFAULT now(),
    heartbeat_at timestamptz NOT NULL DEFAULT now(),
    ended_at timestamptz,
    cancel_requested boolean NOT NULL DEFAULT false,
    observation jsonb NOT NULL DEFAULT '{}'::jsonb,
    platform_check jsonb NOT NULL DEFAULT '{}'::jsonb,
    media_evidence jsonb NOT NULL DEFAULT '{"available": false}'::jsonb,
    error_code text NOT NULL DEFAULT '',
    result text NOT NULL DEFAULT '' CHECK (result IN (
        '', 'succeeded', 'failed', 'cancelled', 'stopped', 'abandoned', 'crashed', 'superseded_confirmation')),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_id, attempt_no),
    FOREIGN KEY (task_id, user_id) REFERENCES public.learning_tasks (id, user_id) ON DELETE RESTRICT,
    FOREIGN KEY (plan_revision_id, user_id) REFERENCES public.learning_plan_revisions (id, user_id) ON DELETE RESTRICT
);

CREATE UNIQUE INDEX learning_task_attempts_one_active_idx ON public.learning_task_attempts (task_id)
    WHERE ended_at IS NULL;
CREATE INDEX learning_task_attempts_lease_idx ON public.learning_task_attempts (lease_expires_at)
    WHERE ended_at IS NULL;
CREATE INDEX learning_task_attempts_user_idx ON public.learning_task_attempts (user_id, started_at DESC);

CREATE TABLE public.learning_task_commands (
    id uuid PRIMARY KEY,
    task_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'admin', 'system')),
    actor_id uuid,
    kind text NOT NULL CHECK (kind IN ('pause', 'resume', 'cancel', 'redispatch')),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    status text NOT NULL CHECK (status IN ('accepted', 'applied', 'rejected')),
    result_code text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz,
    FOREIGN KEY (task_id, user_id) REFERENCES public.learning_tasks (id, user_id) ON DELETE RESTRICT,
    CHECK ((actor_kind = 'system' AND actor_id IS NULL) OR (actor_kind IN ('user', 'admin') AND actor_id IS NOT NULL))
);

CREATE UNIQUE INDEX learning_task_commands_idempotency_idx
    ON public.learning_task_commands (task_id, actor_kind, COALESCE(actor_id, '00000000-0000-0000-0000-000000000000'::uuid), idempotency_key);

CREATE TABLE public.learning_plan_commands (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'admin', 'system')),
    actor_id uuid,
    kind text NOT NULL CHECK (kind IN ('pause', 'resume')),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    status text NOT NULL CHECK (status IN ('accepted', 'applied', 'rejected')),
    result_code text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    applied_at timestamptz,
    FOREIGN KEY (plan_id, user_id) REFERENCES public.learning_plans (id, user_id) ON DELETE RESTRICT,
    CHECK ((actor_kind = 'system' AND actor_id IS NULL) OR (actor_kind IN ('user', 'admin') AND actor_id IS NOT NULL))
);

CREATE UNIQUE INDEX learning_plan_commands_idempotency_idx
    ON public.learning_plan_commands (plan_id, actor_kind, COALESCE(actor_id, '00000000-0000-0000-0000-000000000000'::uuid), idempotency_key);

CREATE TABLE public.learning_course_syncs (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES public.users(id) ON DELETE RESTRICT,
    dedupe_key text NOT NULL CHECK (length(btrim(dedupe_key)) BETWEEN 1 AND 64),
    status text NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'succeeded', 'failed', 'needs_attention')),
    requested_by_kind text NOT NULL CHECK (requested_by_kind IN ('user', 'admin', 'system')),
    requested_by_id uuid,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    lease_owner text NOT NULL DEFAULT '',
    lease_expires_at timestamptz,
    fence_token bigint NOT NULL DEFAULT 0 CHECK (fence_token >= 0),
    error_code text NOT NULL DEFAULT '',
    snapshot_version bigint NOT NULL DEFAULT 0 CHECK (snapshot_version >= 0),
    course_count integer NOT NULL DEFAULT 0 CHECK (course_count >= 0),
    next_attempt_at timestamptz,
    requested_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, user_id),
    CHECK ((requested_by_kind = 'system' AND requested_by_id IS NULL) OR (requested_by_kind IN ('user', 'admin') AND requested_by_id IS NOT NULL))
);

CREATE UNIQUE INDEX learning_course_syncs_active_idx ON public.learning_course_syncs (user_id, dedupe_key)
    WHERE status IN ('queued', 'running');
CREATE INDEX learning_course_syncs_status_idx ON public.learning_course_syncs (status, requested_at);

CREATE TABLE public.learning_outbox (
    event_id uuid PRIMARY KEY,
    topic text NOT NULL CHECK (length(btrim(topic)) BETWEEN 1 AND 64),
    resource_type text NOT NULL CHECK (length(btrim(resource_type)) BETWEEN 1 AND 64),
    resource_id uuid NOT NULL,
    user_id uuid,
    resource_version bigint NOT NULL DEFAULT 0,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);

CREATE INDEX learning_outbox_pending_idx ON public.learning_outbox (created_at, event_id)
    WHERE published_at IS NULL;
