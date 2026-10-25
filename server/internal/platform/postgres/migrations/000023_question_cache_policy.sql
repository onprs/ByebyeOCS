-- 新查询冻结发给 fork 的缓存策略；旧在途查询保留 NULL，按旧协议幂等恢复。
ALTER TABLE public.question_queries ADD COLUMN fork_cache_policy jsonb
    CHECK (fork_cache_policy IS NULL OR
        (jsonb_typeof(fork_cache_policy) = 'object'
         AND fork_cache_policy ?& ARRAY['allow','ttl_days']
         AND jsonb_typeof(fork_cache_policy->'allow') = 'boolean'
         AND (fork_cache_policy->>'ttl_days')::integer BETWEEN 1 AND 365));

COMMENT ON COLUMN public.question_queries.fork_cache_policy IS
    '本次内部题库请求的缓存策略快照；恢复不读取后来发布的策略，NULL 对应旧协议。';
