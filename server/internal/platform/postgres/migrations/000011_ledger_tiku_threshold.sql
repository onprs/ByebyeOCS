-- MVP 题库计费固定每累计 10000 微元结算 1 分（1 分 = 10000 微元）。
-- settlement_threshold_micros 不是“微元/分”的可配置汇率；大于 10000 会把 20000 微元错误算成 1 分。
-- 旧数据若存在其它门槛，迁移失败并阻止启动，由维护者停用/迁移，不在运行时猜换算。
ALTER TABLE public.price_versions
    ADD CONSTRAINT price_versions_tiku_threshold_mvp_check
    CHECK (scope <> 'tiku_tokens' OR settlement_threshold_micros = 10000);
