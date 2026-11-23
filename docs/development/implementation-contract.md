# 跨模块工程实现契约

> **适用范围**：开发维护环境。本文规定 `server/`、`services/tiku/`、`apps/admin/` 与 `apps/client_flutter/` 在新增或修改业务功能时必须共同遵守的底层编码契约。

## 1. 模块边界与构建隔离

1. **双 Go 模块严格隔离**：主服务模块路径为 `github.com/onprs/ByebyeOCS/server`（使用 `go -C server` 构建测试），独立题库模块位于 `services/tiku`（使用 `go -C services/tiku` 构建测试）。保留的早期协议研究目录不纳入任何正式模块或打包流程。
2. **双端口路由注册边界**：用户接口统一挂载于 `/api/v1/*`，管理员接口统一挂载于 `/admin/api/v1/*`，ZPay 支付回调仅挂载于管理监听 `/payments/callbacks/zpay`。严禁将管理接口注册到用户监听 `ServeMux`，未匹配的保留前缀一律返回 JSON `404`。
3. **跨平台文件兼容**：仓库同时支持在 Ubuntu/Linux 与 Windows 下开发测试。路径拼接统一使用 `filepath.Join` / `path.join`；Go 配置模块对私钥/DSN 文件的 `0600` POSIX 权限位检查在非 Windows 系统强制执行，在 Windows 开发环境按操作系统语义豁免。

## 2. 身份、权限与请求编解码契约（`server/internal/web`）

1. **禁止信任请求体身份**：所有用户态读写必须从认证中间件注入的 `web.ActorFrom(r.Context())` 获取 `Actor.ID`，并在 SQL `WHERE` 子句中显式限定 `user_id`；严禁从 JSON 请求体或查询参数读取 `user_id` 作为归属依据。
2. **明确角色与步进验证**：管理端写接口通过 `web.Require(w, r, KindAdmin, "<permission>")` 校验细分权限；涉及余额调整、退款审批、计费变更、管理员变更与账号删除的高危操作必须调用 `web.Fresh(actor, now)` 校验 `5 分钟` 内的近期身份验证，超时返回 `403 step_up_required`。
3. **严格 JSON 解码与大小限制**：统一调用 `web.Decode`（默认上限 `1 MiB`）或 `web.DecodeLimit` 解析请求体，强制校验 `Content-Type: application/json`、开启 `DisallowUnknownFields()` 并拒绝拼接多个 JSON 对象。
4. **乐观锁版本条件**：支持并发编辑的资源通过 `web.ETag(w, version)` 输出 `"v<十进制版本>"`，更新时通过 `web.MatchVersion(r)` 强制校验 `If-Match` 头——未携带返回 `428 precondition_required`，版本不匹配返回 `412 version_conflict`。

## 3. 错误包络与 HTTP 状态码映射

业务层统一使用 `internal/platform/fault.New(code, safeChineseMessage)` 构造面向用户可读的安全中文错误，由 `web.Fail` 映射为标准包络 `{ "error": { "code", "message", "request_id", "retryable", "scope" } }`：

| 错误码类别 | HTTP 状态码 | `retryable` |
| --- | :---: | :---: |
| `unauthenticated`、`session_expired`、`invalid_credentials` | `401` | `false` |
| `forbidden`、`csrf_invalid`、`origin_invalid`、`step_up_required` | `403` | `false` |
| `not_found` | `404` | `false` |
| `conflict`、`insufficient_balance`、`idempotency_conflict` | `409` | `false` |
| `version_conflict` | `412` | `false` |
| `validation_failed`、`invalid_amount` | `422` | `false` |
| `precondition_required` | `428` | `false` |
| `rate_limited` | `429` | `true` |
| `not_configured`、`external_disabled`、`service_unavailable`、`provider_unavailable`、`usage_incomplete`、`backup_unavailable`、`storage_pressure`、`recovery_required` | `503` | `true` |
| 未预期内部错误（屏蔽底层详情，仅记脱敏日志） | `500` | `false` |

## 4. 数据库、资金与审计铁律

1. **只追加迁移与全限定表名**：主库 SQL 统一显式书写 `public.<table_name>`，题库库统一书写 `tiku.<table_name>`。已提交发布的迁移文件永不原地修改（服务端启动会校验历史迁移 SHA-256），一切结构调整必须新增连续编号迁移文件。
2. **整数金额与 UTC 时间**：数据库与 Go 内部使用 `int64` 整数分与整数微元，API 传输一律使用十进制字符串；时间戳统一使用 `TIMESTAMPTZ` 与 UTC `RFC3339` 格式。
3. **同事务审计留痕**：管理员与关键状态写操作在同一数据库事务内调用 `audit.Record(ctx, tx, audit.Event{...})` 落库，审计载荷仅记录脱敏标识、变更前后状态与操作原因，严禁写入密码、Cookie、令牌或完整上游报文。
4. **严禁伪造成功**：正式生产代码与前端构建产物中不得包含模拟支付入账、模拟平台确认或跳过外部探针校验的后门分支。
