-- 运营关闭收费或未定价结算时，账本可能已经释放 hold 并返回 released。
-- 学习任务必须能表达“已释放、未扣费”，不能误标为 settled。
ALTER TABLE public.learning_tasks
    DROP CONSTRAINT IF EXISTS learning_tasks_billing_state_check;

ALTER TABLE public.learning_tasks
    ADD CONSTRAINT learning_tasks_billing_state_check CHECK (billing_state IN (
        'disabled', 'unpriced', 'quoted', 'reserved', 'settled', 'released',
        'release_pending', 'review_required', 'settlement_failed'));
