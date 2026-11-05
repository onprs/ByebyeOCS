-- 记录平台课程级修改时间，作为结构指纹：时间戳未变时可复用上一版的章节卡片页码。
ALTER TABLE public.learning_course_snapshots
    ADD COLUMN IF NOT EXISTS platform_modified_at bigint NOT NULL DEFAULT 0;
