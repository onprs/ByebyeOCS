ALTER TABLE public.learning_chapters
    ADD COLUMN chapter_number text NOT NULL DEFAULT '' CHECK (length(chapter_number) <= 128),
    ADD COLUMN is_group boolean NOT NULL DEFAULT false;

-- 同一媒体可以对应多个任务点，分别保存其卡片位置与个人进度。
ALTER TABLE public.learning_attachments
    DROP CONSTRAINT learning_attachments_chapter_id_snapshot_version_external_o_key,
    ADD CONSTRAINT learning_attachments_object_task_unique
        UNIQUE (chapter_id, snapshot_version, external_object_id, task_point_id);

-- 按稳定外部标识复用章节，避免每个小节逐次扫描历史版本。
CREATE INDEX learning_chapters_external_version_idx
    ON public.learning_chapters (course_id, external_id, snapshot_version DESC);

-- 课程详情仅读取当前版本，历史附件增长不应放大每次读取成本。
CREATE INDEX learning_attachments_snapshot_order_idx
    ON public.learning_attachments (course_id, snapshot_version, chapter_id, sort_order, external_object_id);
