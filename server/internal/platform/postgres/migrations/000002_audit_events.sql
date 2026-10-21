-- 审计表不存放原始请求、配置、凭据和上游响应。
CREATE TABLE public.audit_events (
    id uuid PRIMARY KEY,
    actor_kind text NOT NULL CHECK (actor_kind IN ('user', 'admin', 'system')),
    actor_user_id uuid REFERENCES public.users(id) ON DELETE RESTRICT,
    actor_admin_id uuid REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    action text NOT NULL CHECK (length(btrim(action)) BETWEEN 1 AND 128),
    resource_type text NOT NULL CHECK (length(btrim(resource_type)) BETWEEN 1 AND 64),
    resource_id uuid,
    request_id text NOT NULL CHECK (request_id ~ '^req_[0-9a-f]{32}$'),
    reason text,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CHECK (
        (actor_kind = 'user' AND actor_user_id IS NOT NULL AND actor_admin_id IS NULL) OR
        (actor_kind = 'admin' AND actor_admin_id IS NOT NULL AND actor_user_id IS NULL) OR
        (actor_kind = 'system' AND actor_user_id IS NULL AND actor_admin_id IS NULL)
    )
);

CREATE INDEX audit_events_occurred_at_idx ON public.audit_events (occurred_at DESC, id);
CREATE INDEX audit_events_request_id_idx ON public.audit_events (request_id);

CREATE FUNCTION public.reject_audit_mutation() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append only';
END;
$$;

CREATE TRIGGER audit_events_append_only
BEFORE UPDATE OR DELETE ON public.audit_events
FOR EACH ROW EXECUTE FUNCTION public.reject_audit_mutation();

CREATE TRIGGER audit_events_no_truncate
BEFORE TRUNCATE ON public.audit_events
FOR EACH STATEMENT EXECUTE FUNCTION public.reject_audit_mutation();
