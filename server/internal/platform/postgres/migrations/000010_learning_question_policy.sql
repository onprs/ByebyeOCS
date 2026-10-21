-- 安排级题库策略快照。题库执行尚未接入，策略只保存不可变偏好，不触发调用或自动提交。
ALTER TABLE public.learning_plan_revisions
    ADD COLUMN question_policy_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN question_model_offering_id text,
    ADD COLUMN question_mode text NOT NULL DEFAULT 'cache_first'
        CHECK (question_mode IN ('cache_first', 'always_llm')),
    ADD COLUMN require_manual_confirmation boolean NOT NULL DEFAULT true,
    ADD CONSTRAINT learning_plan_revisions_question_model_check
        CHECK (question_model_offering_id IS NULL
            OR length(btrim(question_model_offering_id)) BETWEEN 1 AND 128),
    ADD CONSTRAINT learning_plan_revisions_question_model_required_check
        CHECK (NOT question_policy_enabled OR question_model_offering_id IS NOT NULL),
    -- 当前版本没有答题执行/自动提交流程；先禁止绕过人工确认的策略。
    ADD CONSTRAINT learning_plan_revisions_manual_confirmation_check
        CHECK (require_manual_confirmation);
