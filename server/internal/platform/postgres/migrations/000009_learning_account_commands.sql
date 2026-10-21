-- 管理员账号批量操作的请求级与逐项幂等记录。
-- 每个命令绑定管理员、幂等键和规范请求摘要；逐项结果、账号状态与审计同事务提交。
CREATE TABLE public.learning_account_commands (
    id uuid PRIMARY KEY,
    actor_kind text NOT NULL CHECK (actor_kind = 'admin'),
    actor_id uuid NOT NULL REFERENCES public.admin_users(id) ON DELETE RESTRICT,
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
    request_hash text NOT NULL CHECK (length(request_hash) = 64),
    action text NOT NULL CHECK (action IN ('pause_plans', 'resume_plans', 'block', 'unblock')),
    reason text NOT NULL CHECK (length(btrim(reason)) BETWEEN 1 AND 500),
    user_ids uuid[] NOT NULL CHECK (cardinality(user_ids) BETWEEN 1 AND 100),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_kind, actor_id, idempotency_key)
);

CREATE INDEX learning_account_commands_created_idx
    ON public.learning_account_commands (created_at DESC, id DESC);

CREATE TABLE public.learning_account_command_items (
    command_id uuid NOT NULL REFERENCES public.learning_account_commands(id) ON DELETE RESTRICT,
    -- 允许记录已删除/不存在的用户编号为逐项失败；命令本身仍属于管理员审计范围。
    user_id uuid NOT NULL,
    position integer NOT NULL CHECK (position >= 0),
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'failed')),
    code text NOT NULL DEFAULT '',
    message text NOT NULL DEFAULT '',
    result_status text NOT NULL DEFAULT '',
    applied_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (command_id, user_id),
    UNIQUE (command_id, position)
);

CREATE INDEX learning_account_command_items_pending_idx
    ON public.learning_account_command_items (command_id, position)
    WHERE status = 'pending';

-- 任务价格版本由账本报价返回；仅保存不可变价格版本标识，不复制价格或余额。
-- 交易结算由 app 的 Billing 适配在同一父事务的保存点内使用。
ALTER TABLE public.learning_tasks ADD COLUMN price_version_id uuid;
