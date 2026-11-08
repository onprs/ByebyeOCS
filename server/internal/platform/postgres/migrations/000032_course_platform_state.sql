-- 课程级状态：学习通课程列表会把已结束课程标记为 not-open-tip。
-- 课程关闭后不再派发任务、不绑定新安排，也不计入待完成。
ALTER TABLE public.learning_courses
    ADD COLUMN platform_state text NOT NULL DEFAULT 'unknown'
        CHECK (platform_state IN ('active', 'closed', 'unknown')),
    ADD COLUMN platform_state_checked_at timestamp with time zone;

CREATE INDEX learning_courses_platform_state_idx
    ON public.learning_courses (user_id, platform_state);
