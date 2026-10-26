-- 课程封面来源仅由可信平台适配器写入；客户端只接收本站受保护的封面路径。
ALTER TABLE public.learning_courses ADD COLUMN cover_source_url text NOT NULL DEFAULT ''
    CHECK (octet_length(cover_source_url) <= 2048);

-- 图片按来源摘要跨账号共享。空内容代表短期不可用缓存，不包含账号和会话。
CREATE TABLE public.learning_course_cover_cache (
    source_hash text PRIMARY KEY CHECK (source_hash ~ '^[0-9a-f]{64}$'),
    content bytea,
    content_hash text NOT NULL DEFAULT '',
    expires_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((content IS NULL AND content_hash = '') OR
           (octet_length(content) BETWEEN 1 AND 262144 AND content_hash ~ '^[0-9a-f]{64}$'))
);
CREATE INDEX learning_course_cover_cache_expiry_idx ON public.learning_course_cover_cache (expires_at);
CREATE INDEX learning_course_cover_cache_updated_idx ON public.learning_course_cover_cache (updated_at DESC, source_hash DESC);
