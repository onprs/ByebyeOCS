-- 用户删除安排时按归档保留任务、账本与审计引用；启停命令表需要接受 delete。
ALTER TABLE public.learning_plan_commands
    DROP CONSTRAINT IF EXISTS learning_plan_commands_kind_check;

ALTER TABLE public.learning_plan_commands
    ADD CONSTRAINT learning_plan_commands_kind_check
    CHECK (kind IN ('pause', 'resume', 'delete'));
