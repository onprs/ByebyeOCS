-- 为投递记录的外键检查和保留清理提供按事件编号查找，避免逐条扫描整张流表。
CREATE INDEX stream_entries_event_id_idx ON public.stream_entries(event_id);

-- 管理员钱包、退款分页沿用创建时间和编号，账本范围汇总按类型与时间筛选。
CREATE INDEX wallets_created_id_idx ON public.wallets(created_at DESC, id DESC);
CREATE INDEX refunds_created_id_idx ON public.refunds(created_at DESC, id DESC);
CREATE INDEX journals_kind_created_idx ON public.journals(kind, created_at);
