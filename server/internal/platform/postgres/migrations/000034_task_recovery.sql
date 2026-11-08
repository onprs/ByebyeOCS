-- 新查询持久化原输出预算；旧自动做题查询沿用原有固定 2048 上限。
ALTER TABLE public.question_queries ADD COLUMN budget_output_tokens integer CHECK (budget_output_tokens > 0);

-- 自动网络恢复预算与显式题库重查代次分开；历史调用和提交事实保持。
ALTER TABLE public.learning_tasks
    ADD COLUMN recovery_cycles integer NOT NULL DEFAULT 0 CHECK (recovery_cycles BETWEEN 0 AND 2),
    ADD COLUMN answer_retry_generation integer NOT NULL DEFAULT 0 CHECK (answer_retry_generation BETWEEN 0 AND 3);
