-- 隐私删除决定、用户关闭标记与恢复防复活支持。
-- 只扩展结构与清理函数，不删除或改写任何不可变财务/审计记录。

-- 用户关闭状态：保留 opaque user id，供账本、去重与审计继续引用。
ALTER TABLE public.users DROP CONSTRAINT IF EXISTS users_status_check;
ALTER TABLE public.users ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'restricted', 'deletion_requested', 'closed'));

-- 删除申请增加延后与驳回决定；完成后保留记录。
ALTER TABLE public.data_requests DROP CONSTRAINT IF EXISTS data_requests_status_check;
ALTER TABLE public.data_requests ADD CONSTRAINT data_requests_status_check
    CHECK (status IN (
        'pending', 'processing', 'ready', 'needs_review', 'deferred',
        'rejected', 'completed', 'failed', 'expired'
    ));

-- 同一用户同一类请求在待处理、复核中与延后期间只允许一条。
DROP INDEX IF EXISTS public.data_requests_active_idx;
CREATE UNIQUE INDEX data_requests_active_idx
    ON public.data_requests (user_id, kind)
    WHERE status IN ('pending', 'processing', 'needs_review', 'deferred');

-- 关闭后清除原始主体密文；保留 provider/namespace/subject_index 防止同一主体重新注册。
ALTER TABLE public.external_identities ALTER COLUMN subject_ciphertext DROP NOT NULL;

-- 最小不复活标记：只保存内部用户、删除申请与关闭时间，不含展示身份或凭据。
CREATE TABLE public.privacy_closures (
    user_id uuid PRIMARY KEY REFERENCES public.users(id) ON DELETE RESTRICT,
    request_id uuid NOT NULL REFERENCES public.data_requests(id) ON DELETE RESTRICT,
    closed_at timestamptz NOT NULL DEFAULT now(),
    policy_version text NOT NULL DEFAULT 'v1' CHECK (policy_version ~ '^v[0-9]{1,8}$')
);

CREATE INDEX privacy_closures_closed_idx ON public.privacy_closures (closed_at DESC);

-- 内容清理只处理非必要个人内容与通知载荷；不触碰钱包、账本、订单、支付、退款、对账与审计。
-- 仅供关闭流程与恢复重放调用；财务/争议检查由调用方负责。
CREATE FUNCTION public.scrub_closed_user_data(p_user uuid) RETURNS void
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
BEGIN
    UPDATE public.learning_accounts
       SET school = '', display_name = '',
           blocked_code = 'account_closed', blocked_reason = '账号已关闭',
           blocked_at = now(), blocked_until = NULL,
           version = version + 1, updated_at = now()
     WHERE user_id = p_user;
    UPDATE public.learning_plans
       SET name = '已关闭安排', status = 'archived', version = version + 1, updated_at = now()
     WHERE user_id = p_user;
    UPDATE public.learning_courses
       SET title = '', teacher = '', term = '', updated_at = now()
     WHERE user_id = p_user;
    UPDATE public.learning_chapters SET title = '' WHERE user_id = p_user;
    UPDATE public.learning_attachments SET title = '' WHERE user_id = p_user;
    UPDATE public.learning_tasks
       SET title = '', observation = '{}'::jsonb, platform_check = '{}'::jsonb,
           media_evidence = '{"available": false}'::jsonb,
           last_error_code = '', last_error_scope = '', verification_reason = '',
           confirmation_reason = '', desired_state = 'cancel',
           stop_requested_at = coalesce(stop_requested_at, now()),
           version = version + 1, updated_at = now()
     WHERE user_id = p_user;
    UPDATE public.learning_task_attempts
       SET observation = '{}'::jsonb, platform_check = '{}'::jsonb,
           media_evidence = '{"available": false}'::jsonb, error_code = ''
     WHERE user_id = p_user;
    UPDATE public.learning_task_commands SET reason = '' WHERE user_id = p_user;
    UPDATE public.learning_plan_commands SET reason = '' WHERE user_id = p_user;
    UPDATE public.learning_course_syncs SET error_code = '' WHERE user_id = p_user;
    DELETE FROM public.learning_outbox WHERE user_id = p_user;

    UPDATE public.question_queries
       SET question_title = '', question_options = '', answer = NULL,
           content_expired = true, updated_at = now()
     WHERE user_id = p_user;
    UPDATE public.question_cache_reports SET reason = '账号已关闭'
     WHERE user_id = p_user AND reason <> '账号已关闭';
    UPDATE public.data_requests
       SET result_ciphertext = NULL, status = 'expired', updated_at = now()
     WHERE user_id = p_user AND kind = 'export' AND status <> 'expired';

    UPDATE public.outbox_events SET payload = '{}'::jsonb WHERE user_id = p_user;
    UPDATE public.stream_entries SET payload = '{}'::jsonb
     WHERE stream_key IN (SELECT 'user/' || id::text FROM unnest(ARRAY[p_user]) AS id);
END;
$$;

COMMENT ON FUNCTION public.scrub_closed_user_data(uuid)
    IS '仅清理关闭用户的非必要内容与通知载荷；不处理资金、订单、退款、对账或审计。';

-- 恢复后由维护者显式执行：重新应用关闭标记并撤销可能随备份恢复的会话/授权。
-- extra_user_ids 用于恢复点早于关闭记录时，从外部保留的最小清单补录用户编号。
CREATE FUNCTION public.reapply_privacy_closures(extra_user_ids uuid[] DEFAULT '{}')
RETURNS integer
LANGUAGE plpgsql SET search_path = pg_catalog, public AS $$
DECLARE
    targets uuid[];
    closed_count integer := 0;
BEGIN
    SELECT coalesce(array_agg(user_id), '{}'::uuid[]) INTO targets FROM public.privacy_closures;
    targets := targets || coalesce(extra_user_ids, '{}'::uuid[]);
    IF cardinality(targets) = 0 THEN
        RETURN 0;
    END IF;

    UPDATE public.users
       SET status = 'closed', version = version + 1, updated_at = now()
     WHERE id = ANY(targets) AND status <> 'closed';
    GET DIAGNOSTICS closed_count = ROW_COUNT;

    DELETE FROM public.refresh_tokens
     WHERE session_id IN (SELECT id FROM public.client_sessions WHERE user_id = ANY(targets));
    DELETE FROM public.client_sessions WHERE user_id = ANY(targets);
    DELETE FROM public.platform_sessions WHERE user_id = ANY(targets);
    DELETE FROM public.login_challenges WHERE user_id = ANY(targets);
    DELETE FROM public.user_preferences WHERE user_id = ANY(targets);

    UPDATE public.external_identities
       SET subject_ciphertext = NULL, display_name = NULL, school = NULL,
           status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now()
     WHERE user_id = ANY(targets);

    PERFORM public.scrub_closed_user_data(id) FROM unnest(targets) AS id;

    RETURN closed_count;
END;
$$;

COMMENT ON FUNCTION public.reapply_privacy_closures(uuid[])
    IS '恢复后显式重新应用关闭/撤销标记；不结算资金、不删除账本与审计，也不替代法律保留判断。';
