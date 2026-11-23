# 决策 0006：维护独立题库 Fork 实现精确缓存与服务端 Token 计费

- **状态**：已采纳并落地（目录与面板所有权由 [决策 0011](0011-tiku-panel-catalogue.md) 进一步收敛）

## 背景

上游原版 OCS-Tiku 主要面向个人本地单模型搜题设计，使用本地 SQLite 存储并依赖历史相似题模糊匹配，不支持按请求指定公开模型、不支持请求级精确缓存控制，也无法向主平台回传可信的分槽位大模型 token 消耗明细，无法直接支撑多用户按 token 计费与错误缓存治理。

## 决策

1. **维护独立模块化 Fork（`services/tiku` / `ByebyeOCS-Tiku`）**：保留根包上游 `/api/search` v1 兼容能力，在 `internal/platform` 下构建面向多租户计费的正式 v2 服务，使用独立 PostgreSQL 数据库与角色，上游大模型 Base URL 与 API 密钥仅保存在题库服务侧。
2. **严格区分精确缓存与每次调用**：
   - 支持请求级 `cache_first`（精确缓存优先）与 `always_llm`（每次调用模型，允许缓存时写入新结果）两种模式，以及请求级 `cache_policy: {allow, ttl_days}`；
   - 精确缓存键必须同时绑定规范化题型、题干、完整选项顺序、图片内容哈希、模型连接/密钥摘要、有效输出预算及提示词版本，**严禁将模糊历史检索结果当作缓存命中返回**；
   - 精确缓存命中时返回 `cache.hit = true` 且消耗 token 为 `0`；支持用户提交问题报告并由管理员联动失效错误缓存。
3. **服务端权威 Token 明细与安全免扣**：
   - 所有 token 消耗一律以题库服务端解析上游响应得到的真实统计为准（含非视觉模型自动串联 `caption` 图片转述槽位与 `answer` 作答槽位的分项明细）；
   - 当模型未发布价格或上游响应缺失/矛盾导致 `usage_incomplete` 时，保留答案与记录但禁止自动扣费。

## 当前落点与影响

- 主服务通过 Bearer 服务令牌调用题库 `/internal/v1/offerings` 与 `/api/v2/answers`，在主库冻结查询价格与预算快照并独立执行钱包预占与结算，详见 [外部集成架构](../architecture/integrations.md) 与 [题库超时与恢复](../architecture/question-timeouts.md)。
