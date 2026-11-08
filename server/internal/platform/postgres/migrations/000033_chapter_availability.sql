-- 平台明确关闭的章节仍属于目录，禁止按空白小节执行。
ALTER TABLE public.learning_chapters
    ADD COLUMN availability text NOT NULL DEFAULT 'unknown'
        CHECK (availability IN ('unknown', 'open', 'closed'));
