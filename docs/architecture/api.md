# REST 与 SSE 接口架构

> **适用范围**：全栈接口设计规范。主服务的精确路径、请求体、响应 Schema 与状态码定义以仓库根目录 [api/openapi.yaml](../../api/openapi.yaml) 为唯一机器可读契约（测试套件自动对实际 HTTP 响应执行 OpenAPI Schema 校验）。

## 1. 四类接口面与网络边界

系统按受众与信任级别将 HTTP 接口划分为四个互不穿透的边界：

| 接口面 | 默认监听与路径前缀 | 认证方式 | 用途说明 |
| --- | --- | --- | --- |
| 用户端接口 | `127.0.0.1:8090` `/api/v1/*` | Web 同源 Cookie + CSRF；原生端 Bearer 访问令牌 | 服务于 Flutter Web/Android/iOS 客户端的身份、课程、安排、任务、题库查询、钱包、SSE 与隐私请求 |
| 管理端接口 | `127.0.0.1:8091` `/admin/api/v1/*`（另含 `/health/*` 与 `/payments/callbacks/zpay`） | 管理员同源 Cookie + CSRF + RBAC（支付回调走渠道验签） | 服务于 React 管理员控制台的运行监控、用户与任务管控、对账调账、参数热调与隐私复核 |
| 题库内部与查询接口 | `127.0.0.1:8092` `/internal/v1/offerings`、`/api/v2/*`（兼容 `/api/search`） | `Authorization: Bearer <32+字符服务令牌>` | 仅供主服务在内网或回环网络同步公开模型目录、执行幂等查题与发起错误缓存失效 |
| 题库维护面板接口 | `127.0.0.1:8092` `/panel/api/*`（经管理端域名同源代理 `/panel/`） | 面板独立 `HttpOnly` `SameSite=Strict` Cookie + 精确 `Origin` + CSRF | 仅供题库维护员管理提供商、上游模型、公开价格版本、缓存与面板设置 |

## 2. 通用数据编码与幂等契约

1. **金额与费率零浮点传输**：所有货币最小单位（人民币整数分 `*_minor`）与微元费率（`*_micros`，`1 分 = 10,000 微元`）在 JSON 中一律编码为十进制整数字符串（如 `"available_minor": "5450"`、`"cost_micros": "4716"`），彻底消除 JavaScript `Number` 与双精度浮点舍入误差。
2. **明确区分“零费用”与“未定价”**：缓存命中时 `cache.hit = true` 且 `usage.total_tokens = 0`；尚未配置单价或模型未定价时明确返回 `pricing.state = "unpriced"`，上游返回 token 不完整时标记 `usage.complete = false`，客户端据此展示准确状态，绝不将未定价渲染为“免费 0.00 元”。
3. **写操作幂等与乐观锁**：创建充值订单、提交题库查询、执行人工调账、审批退款及复核答案报告均支持并校验 `Idempotency-Key` 请求头（相同键搭配不同参数返回 `409 idempotency_conflict`，相同参数返回首次处理结果）；配置、偏好与安排编辑通过 `ETag: "v<version>"` 与 `If-Match: "v<version>"` 实施条件更新（缺少请求头返回 `428 precondition_required`，版本失配返回 `412 version_conflict`）。
4. **快照一致游标分页**：列表接口统一采用不透明游标分页（`cursor` + `limit`）。尤其是任务安排列表接口，在同一可重复读数据库快照内同时计算分类汇总计数、当前安排版本与当前页记录，并在游标中绑定过滤条件与版本指纹，拒绝跨版本或跨账号混用游标。
5. **统一错误包络**：所有错误响应返回 `{ "error": { "code": "<稳定机器码>", "message": "<面向用户的中文说明>", "request_id": "req_...", "retryable": false, "scope": "request|service" } }`，不暴露 SQL 报错、文件路径或堆栈信息。

## 3. 事务 Outbox 与严格有序 SSE 实时流

### 3.1 为什么不直接使用自增主键做游标

在并发 PostgreSQL 事务中，两个事务分配 `BIGSERIAL` 自增 ID 的先后顺序可能与实际 `COMMIT` 可见顺序相反；若直接按自增 ID 轮询，后提交的小 ID 记录会被永久漏读。因此系统采用 **两阶段事务 Outbox 串行发布架构**：

1. **业务事务写入 Outbox**：课程、任务、钱包或隐私状态变更时，在同一数据库事务内向 `learning_outbox`（或直接调用事件写入器）插入带 `event_id`、受众、业务资源 `resource_id` 与递增 `resource_version` 的未发布记录。
2. **串行流头分配**：后台桥接器通过 `FOR UPDATE SKIP LOCKED` 批量取出已提交的 Outbox 记录，在同一短事务内锁定目标流头（`event_stream_heads`）分配严格单调递增的投递序号 `seq`，批量写入 `event_stream_entries` 并标记 Outbox 已发布，随后唤醒内存订阅通道推送给在线 SSE 连接。

### 3.2 连接配额、断线补偿与权限过滤

- **连接上限**：全局并发 SSE 连接受 `operations.max_event_streams`（默认 `64`）约束，同一用户或同一管理员最多同时建立 `3` 条并发流，防止慢连接耗尽文件描述符。
- **水位线与补偿**：常规快照接口返回当前流的水位线序号；客户端携带 `Last-Event-ID` 或游标重连时，服务端优先补发保留窗口内的增量事件（SSE 流条目默认保留 3 天，已投递 Outbox 默认保留 7 天）；若游标已早于最早保留序号，立即下发 `resync_required` 事件引导客户端重拉全量快照。
- **最小载荷与角色隔离**：SSE 载荷仅携带轻量通知事实与最新版本号（不含敏感凭据或长文本）；管理员 SSE 流（`/admin/api/v1/events`）在推送前按当前管理员角色逐条调用 `allowedAdminEvent` 过滤——例如仅具备 `learning.read` 的运营角色不会收到 `wallet.*` 或 `privacy.*` 事件。
