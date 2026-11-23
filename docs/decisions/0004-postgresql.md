# 决策 0004：采用 PostgreSQL 作为正式服务唯一主数据库

- **状态**：已采纳并落地

## 背景

核心业务包含多用户任务公平抢占、租约续期与 fencing 保护、复式记账借贷平衡事务、ZPay 异步回调幂等入账以及有序事件 Outbox 发布，需要成熟的行级并发锁（`FOR UPDATE SKIP LOCKED`）、可重复读事务隔离与严格的约束检查能力。

## 决策

1. 确立 **PostgreSQL（16 及以上版本）** 为 Go 主服务的唯一正式数据库。
2. 不开发 SQLite 生产存储后端、不抽象双数据库兼容层，也不维护跨数据库方言测试。
3. 数据库结构演进采用编译内置的连续编号 SQL 迁移文件，仅在显式执行 `migrate up` 时通过事务咨询锁串行变更；服务启动时严格校验全部历史迁移文件的 SHA-256 摘要，杜绝静默建表或带病启动。
4. 独立题库服务（见 [决策 0006](0006-tiku-fork-token-billing.md)）同样采用独立的 PostgreSQL 数据库与角色，与主库保持物理与权限隔离。

## 当前落点与影响

- 主服务内置 34 个连续 SQL 迁移（`server/internal/platform/postgres/migrations/`），题库服务内置 8 个连续 SQL 迁移（`services/tiku/internal/platform/store/migrations/`）。
- 本地全栈开发脚本（`scripts/dev.mjs`）与 CI 均针对真实隔离 PostgreSQL 实例执行迁移与竞态集成测试。
