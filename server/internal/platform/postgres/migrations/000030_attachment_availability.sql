-- 任务点可用性：平台已关闭或未开放的测验不能当作可执行任务。
-- 卡片 JSON 只给出 jobid 与 0 值时间窗，状态由执行前核验回写。
ALTER TABLE public.learning_attachments
    ADD COLUMN availability text NOT NULL DEFAULT 'unknown'
        CHECK (availability IN ('unknown', 'open', 'closed')),
    ADD COLUMN availability_checked_at timestamp with time zone;

CREATE INDEX learning_attachments_availability_idx
    ON public.learning_attachments (user_id, course_id, availability)
    WHERE is_task_point;
