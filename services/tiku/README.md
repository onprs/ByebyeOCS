# fork 题库服务

本目录是 ByebyeOCS 的独立题库服务模块，基于相邻 OCS-Tiku 固定版本
`aedb64c55952f7914e4c46d59c732018605a9921` 的接口事实重新实现，不修改上游工作区。
它提供 v1 `/api/search` 兼容、v2 精确缓存作答、内部模型目录/失效/测试接口和独立
PostgreSQL 存储。

## 与上游 OCS-Tiku 的差异

- 保留上游 v1 `POST /api/search` 的成功响应字段 `question`、`answer`（字符串），以及
  `GET`/`HEAD /api/search` 探测；v1 固定使用配置的默认模型并每次调用模型。
- 新增 `POST /api/v2/answers`：请求级公开模型、`cache_first` / `always_llm`、精确规范化
  缓存、幂等、服务端 token 用量和调用分解。
- 新增 `/internal/v1/*`：受服务令牌保护的模型目录、启停、可达性/功能性测试、缓存失效
  和用量汇总。
- 存储改为独立 PostgreSQL 数据库中的 `tiku` schema。上游 SQLite 管理页、前端资源、
  Agent 模式、历史模糊检索和完整设置页没有复制到本模块；这些上游功能不能视为已实现。
- 模型调用只实现 OpenAI-compatible 非流式 `chat/completions`，不包含 Anthropic、
  Gemini、Ollama 等上游协议适配。需要其他协议时应在 fork 内新增适配层，不能靠改主平台。
- 上游图片支持 WebP 和超大图压缩；本模块按配置限制 PNG/JPEG/GIF，超过上限直接拒绝，
  不保证与上游处理结果相同。

## 配置与密钥

示例见 `ocs-tiku-fork.example.json`。示例不包含模型、默认模型或真实密钥，服务不会自动
创建默认模型。每个模型需要单独密钥文件；服务令牌也来自文件。配置只保存文件路径，不保存
密钥值。

- `database_dsn_file`：独立 PostgreSQL 连接串文件。建议使用独立数据库和角色（例如
  `ocs_tiku`），不要指向主平台数据库，也不要在主平台 schema 中建表。
- `database_max_connections`：连接池上限，默认4，可配置1到32；低内存部署可从2开始。
- `service_token_file`：至少 32 字符的内部服务令牌；主平台用同一令牌调用 v2/内部接口。
- 连接、服务令牌和模型密钥的相对路径均以配置文件目录为基准。
- `models[]`：`id` 即主平台保存的 `provider_ref`；`base_url`、`model`、`api_key_file`
  只存在于 fork 配置。`vision` 表示是否直接把题目图片交给回答模型。
- `default_model_id` 可为空；为空时 v2 请求必须显式选择模型，v1 会返回未配置错误。
- `allow_loopback_upstreams` 只用于本地受控测试；生产上游必须使用 HTTPS 公网地址。

## 命令

在 `services/tiku` 目录执行：

```sh
go run ./cmd/ocs-tiku-fork config check -config ocs-tiku-fork.json
go run ./cmd/ocs-tiku-fork migrate -config ocs-tiku-fork.json
go run ./cmd/ocs-tiku-fork serve -config ocs-tiku-fork.json
```

`migrate` 显式执行；`serve` 不自动迁移，也不创建默认账号或模型。项目没有提供本模块的
一键安装包或 Release 下载命令；部署由维护者按实际二进制和 systemd/反代配置完成。

## v2 契约要点

请求头必须带 `Authorization: Bearer <service token>` 和 `Idempotency-Key`。请求体字段：

```json
{
  "request_id": "qq_...",
  "model_offering_id": "standard",
  "mode": "cache_first",
  "cache_scope": "user",
  "cache_scope_key": "<主平台计算的不可逆范围键>",
  "question": {"title": "...", "options": "...", "type": "single", "images": []},
  "budget": {"max_output_tokens": 1024}
}
```

- 精确缓存键包含规范化题、选项顺序、题型、图片摘要、公开模型 ID、提示词版本、
  规范化版本和范围键；只做完全一致命中，不做模糊历史匹配。
- 默认 `user` 范围；`shared` 必须在配置中显式开启。TTL 默认 7 天，并受 `max_ttl_days`
  限制。仅完整成功、答案非空、用量完整的响应可写入缓存。
- 缓存命中返回 `status: "cache_hit"`、`usage.total_tokens: 0`、`usage.calls: []`，并带
  缓存条目 ID 和来源时间。未知 token 不补 0。
- 同一 `Idempotency-Key` 与 `request_id` 返回同一结果；摘要不同返回 409；并发同请求由
  数据库幂等记录合并为一次模型调用。
- 不自动重试模型调用；超时返回 `upstream_timeout`，幂等记录保存为 `unknown`，同键重试
  返回 `request_unknown` 供人工核对，不会再次调用模型。模型被禁用返回
  `model_disabled`，不静默切换到其他模型。
- 幂等记录按配置保留期清理已结束记录；`unknown` 与 `pending` 记录不会在没有人工核对时
  被当作确定失败自动重放。
- 图片模型不支持视觉时，必须配置 `caption_model_id`，否则返回 `vision_unsupported`；
  转述与回答调用分别列入 `calls` 并计 token。
- 服务端返回的 usage 缺字段或 `total != input + output` 时标记 `usage_incomplete`，
  不写缓存，供主平台进入待复核。
- 价格、钱包、预占与结算只在主平台配置；fork 只回传 token 事实，不保存钱包金额，也不
  按自己的价格扣费。

## 测试

```sh
go test ./...
```

PostgreSQL 集成测试需要显式提供隔离实例连接串文件：

```sh
BYEBYEOCS_TIKU_TEST_DATABASE_DSN_FILE=/path/to/test-admin.dsn go test ./...
```

测试使用 `httptest` 模拟 OpenAI-compatible 上游，只连接隔离临时数据库；没有环境变量时
集成测试跳过。测试不会向真实模型发送请求。
