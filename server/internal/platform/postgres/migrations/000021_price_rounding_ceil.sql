-- 价格版本允许“向上取整”；保留 floor/half_up 以兼容已发布和冻结的历史价格。
-- 旧迁移不可修改，本迁移只放宽现有 CHECK 约束。
ALTER TABLE public.price_versions
    DROP CONSTRAINT IF EXISTS price_versions_rounding_check;
ALTER TABLE public.price_versions
    ADD CONSTRAINT price_versions_rounding_check
    CHECK (rounding IN ('floor', 'half_up', 'ceil'));
