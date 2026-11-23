# 数据库与迁移架构

> **适用范围**：开发维护环境，说明主服务 PostgreSQL 数据库（34 个连续迁移）与独立题库 PostgreSQL 数据库（8 个连续迁移）的模型设计、事务锁顺序与演进规则。

## 1. PostgreSQL 唯一存储基线与显式迁移

系统将 **PostgreSQL 16+** 作为唯一正式持久化引擎（见 [决策 0004](../decisions/0004-postgresql.md)），利用行级锁（`FOR UPDATE` / `SKIP LOCKED`）、可重复读快照事务、部分唯一索引与延迟/检查约束保障高并发调度与资金账本严密一致，不维护 SQLite 生产分支或双库 ORM 抽象。

两套服务共享相同的迁移安全铁律：

- **显式执行与咨询锁**：仅 `byebyeocs migrate up` 与 `ocs-tiku-fork migrate` 会执行建表与结构变更，执行前获取 PostgreSQL 咨询锁串行化并发调用；单版本 SQL 与迁移元数据记录在同一事务中原子提交。
- **启动强校验**：`serve` 与 `doctor` 启动时逐条比对内置迁移文件的编号连续性、文件名与内容 SHA-256 校验和；若发现数据库未初始化、缺号、历史迁移被篡改或数据库版本高于当前二进制，立即报错退出。

## 2. 主数据库领域模型（34 个连续迁移）

主数据库位于 `server/internal/platform/postgres/migrations/`（`000001` 至 `000034`），按业务领域划分为六大表族：

### 2.1 身份、会话与隐私（迁移 01, 03, 08, 15）

- `users`、`external_identities`、`platform_sessions`：保存内部用户、HMAC 外部主体绑定及带密钥版本号的加密平台会话。
- `admin_users`、`client_sessions`、`admin_sessions`、`login_challenges`：保存管理员账号与五类角色、Web Cookie 会话、原生旋转刷新令牌摘要族及短期登录挑战。
- `user_preferences`、`admin_preferences`：隔离存储双端配色、主题模式与通知开关及乐观锁版本。
- `data_export_requests`、`account_deletion_requests`、`privacy_closures`：管理有界加密导出、删除审批工作流与重放恢复所需的持久闭户匿名化清单（含 `reapply_privacy_closures` 存储函数）。

### 2.2 课程目录、共享缓存与三级可用性（迁移 04, 18, 25–28, 30–33）

- `learning_courses`、`learning_course_chapters`、`learning_course_attachments`：保存 `chaoxing-read-v2` 多级章节树（父子关系、章分组、原始序号、稳定小节 UUID）、附件 `card_index` 及课程级平台状态 `platform_state`（含已结束课程标记与结构指纹提示）。
- `learning_resource_cache`、`learning_course_covers`：跨用户保存 24 小时有效的章节卡片页码目录缓存，以及无会话安全回源重编码的静态 JPEG 封面缓存。
- `learning_task_point_availability` 与章节 `availability`：独立于课程快照重建周期，持久记录章节关闭与测验/任务点关闭（`platform_closed`）事实及最近核验时间。

### 2.3 学习安排、任务调度与异常恢复（迁移 04, 09, 10, 13, 16–19, 29, 34）

- `learning_plans`、`learning_plan_revisions`、`learning_plan_tasks`：管理安排主记录（含归档删除时间戳）、不可变配置版本（课程范围、类型、题库模型与缓存模式）及当前版本任务绑定。
- `learning_tasks`、`learning_task_attempts`、`learning_account_commands`：以稳定任务键去重，记录期望状态（`desired_state`）、运行状态（`state`）、租约持有人与到期时间、`fencing_token`、观察进度、独立平台核验状态、真实媒体证据标记、两轮瞬态冷却恢复计数、三代显式答案重查代数及测验最终提交持久化标记。

### 2.4 复式账本、计费与支付（迁移 05, 11, 14, 21）

- `wallets`、`ledger_accounts`、`ledger_journals`、`ledger_postings`：每个用户钱包关联可用余额账户与预占账户，配合系统渠道清算、服务收入与退款应付账户；所有金额变动必须写入借贷和恒为零的不可变 `journals` + `postings`，钱包余额列仅为同一事务同步维护的受检缓存。
- `billing_settings`、`billing_price_versions`、`charge_intents`、`wallet_holds`、`usage_records`：管理微元单价、舍入方式（`ceil`/`floor`/`half_up`）、收费意图、冻结预占与唯一任务点结算凭证。
- `recharge_orders`、`payment_callbacks`、`refund_requests`：管理充值订单生命周期、ZPay 异步通知幂等 Inbox 及服务退回/渠道原路退款审批。

### 2.5 题库目录镜像、查询快照与报告复核（迁移 06, 12, 16, 17, 20, 22–24, 34）

- `question_offerings`：以题库公开条目 UUID 为主键的只读目录镜像表（主库旧价格版本表与旧测试表已在迁移 24 收敛移除）。
- `question_queries`：持久化每次题库查询的幂等请求键、冻结的公开条目与微元费率快照、冻结的输出 token 预算、缓存策略、调用槽位 token 汇总、计费状态及定期清理的正文载荷。
- `question_reports`、`question_report_reviews`：保存用户提交的答案问题报告及管理员带幂等键的复核与缓存失效记录。

### 2.6 审计与有序事件流（迁移 02, 07, 19）

- `audit_logs`：记录管理员与关键系统动作的不可变脱敏审计轨迹。
- `learning_outbox`、`event_stream_heads`、`event_stream_entries`：承接业务事务写入的 Outbox 事件，经串行桥接分配严格单调递增的投递流序号。

## 3. 独立题库数据库模型（8 个连续迁移）

题库库位于 `services/tiku/internal/platform/store/migrations/`（`000001` 至 `000008`），统一建于 `tiku` schema 下：

- `tiku.offerings` 与 `tiku.offering_price_versions`：维护公开模型条目（通过部分唯一索引强制每个活跃上游模型最多绑定一个未删除的公开条目，支持软删除释放绑定并保留历史引用）及只追加的微元价格版本历史。
- `tiku.model_overrides`、`tiku.model_tests`：记录模型启停覆盖状态与连通性/功能测试结果。
- `tiku.requests`、`tiku.queries`、`tiku.model_calls`：保存客户端幂等键与请求指纹摘要、规范化题目与响应载荷（到期自动脱敏清理正文，永久保留幂等摘要与 token 统计防重放）及作答/视觉转述槽位调用明细。
- `tiku.cache_entries`：按规范化题目、选项、图片哈希、模型连接/密钥指纹、输出预算与策略期限存储精确答案缓存及主动失效原因。
- `tiku.panel_audit`：记录 `/panel/` 维护面板的所有配置、发布与缓存失效审计日志。

## 4. 并发锁顺序与防死锁准则

涉及多实体更新的事务严格遵守固定加锁顺序：**用户/钱包主行 → 账本账户（按账户 UUID 字典序排序） → 收费意图 / 预占行 → 业务订单或学习任务行**；严禁在持有数据库事务锁期间发起学习通、ZPay 或大模型网络 I/O。
