# 决策 0004：正式服务统一使用 PostgreSQL

状态：用户已确认。

## 依据

用户明确要求：“数据库换成postgres吧”。因此 PostgreSQL 从此前的推荐方案变为正式项目选型。

## 决策

- PostgreSQL 是 Go 服务端唯一主数据库，覆盖内部用户、学习通身份、课程、任务、账本、支付、审计和事件。
- 不再开发 SQLite 后端、双数据库兼容层或 SQLite → PostgreSQL 导入流程。
- 开发、集成测试和生产使用同一 PostgreSQL 主版本系列；受支持版本和补丁在实施时固定。
- 继续采用一个 Go 应用进程、Linux 原生二进制和 systemd，不增加 Docker 或微服务前提。
- PostgreSQL 可由用户原生安装或使用合适的托管实例，作为公开部署的明确依赖。

## 本次变更范围

当前只有前端模拟原型，没有正式业务 schema、运行中的业务库或待迁移生产数据。本次更新设计、数据库/部署规格与路线，不安装数据库、不连接实例，也不执行真实数据迁移。

研究目录不属于本次替换范围。独立 OCS-Tiku 在 [决策 0006](0006-tiku-fork-token-billing.md) 中决定 fork 并使用独立 PostgreSQL 数据库/角色；主平台仍只通过 Provider 接入，不跨库查询。

## 后续实施

连接池与事务明确面向 PostgreSQL；资金使用行锁、业务唯一键和原子分录；任务领取采用条件更新和必要的 `SKIP LOCKED`；事件顺序按提交后投递方案处理，不能直接使用自增 ID 推断提交顺序。

备份、恢复、迁移、大版本升级与性能参数通过 PostgreSQL 集成和故障注入验收。具体工程参数由实施和测试确定，不再次作为用户的数据库选型问题。

参见 [数据库设计](../architecture/database.md)、[部署规格](../architecture/deployment.md) 与 [开发路线](../product/roadmap.md)。
