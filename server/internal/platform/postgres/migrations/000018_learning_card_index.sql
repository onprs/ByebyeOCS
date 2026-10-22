-- 记录章节内任务卡片页码，执行和独立核验使用同一张卡片。
ALTER TABLE public.learning_attachments
    ADD COLUMN card_index integer NOT NULL DEFAULT 0 CHECK (card_index BETWEEN 0 AND 31);
