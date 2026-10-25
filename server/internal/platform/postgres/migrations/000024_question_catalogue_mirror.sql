-- 公开模型条目与价格改由题库面板维护；主库只保留目录镜像与查询事实。
-- 价格版本与模型测试记录随题库面板迁移到题库数据库，旧表不再写入。
COMMENT ON TABLE public.question_offerings IS
    '题库公开模型条目的只读镜像；价格与缓存策略在题库面板维护，查询使用冻结快照。';

DROP TABLE IF EXISTS public.question_offering_price_versions;
DROP TABLE IF EXISTS public.question_model_tests;
