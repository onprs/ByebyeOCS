-- 执行前只读核验发现平台已完成时的不收费事实。
-- RecordConfirmed 必须优先读取 pre_completed_no_charge/confirmation_reason，
-- 即使 billing_hold_id 仍保留也必须按 Uncharged 记录中央用量。
ALTER TABLE public.learning_tasks
    ADD COLUMN pre_completed_no_charge boolean NOT NULL DEFAULT false,
    ADD COLUMN confirmation_reason text NOT NULL DEFAULT ''
        CHECK (length(confirmation_reason) <= 128);
