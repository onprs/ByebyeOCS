# 学习通与 OCS-Tiku 适配

## 1. 研究代码复用

本轮核查了六份研究报告以及 `chaoxing-progress-research/tools/` 中的原生读取、发现、批量和登录入口。正式项目提取已验证逻辑并补齐契约测试，保留研究目录原状，不重复开发基础发现能力。

| 研究入口 | 提取范围 | 正式化限制 |
| --- | --- | --- |
| `native-runner/discovery.go` | 课程参数、章节顺序、任务点编号、普通附件排除 | 用稳定组合键、带版本快照和脱敏输入样本验证 |
| `native-runner/main.go` | 原生查询、结果解析、新鲜状态复核 | 文件同时包含 mpv、合成、探测与实验开关；不能作为正式引擎整包暴露 |
| `native-batch/main.go` | 有界批量、进程生命周期、文件锁经验 | 研究中的整账号排他锁不满足正式同账号多任务并发 |
| `login_account.py` | 受授权环境中的常规登录和课程读取线索 | 不等于官方 OAuth，也不证明商业授权 |

报告中已有同账号不同任务并发的证据；相同任务多实例不能视为进度叠加。缺少真实媒体记录的任务，即使平台返回通过也不能称为真实观看。

## 2. 学习通 Adapter 的契约

建议提供：身份验证、课程与章节发现、任务点解析、能力检查、授权执行、只读平台核验、会话失效分类。领域层不直接拼上游 URL 或解析页面私有字段。

每个任务携带能力描述：可原生读取、可执行且已验证、是否需要真实播放、是否需要人工交互、是否允许最终提交。能力未知时转人工处理，不通过猜测加请求。

原生执行器为默认轻量实现，不强依赖 Chromium、mpv 或解码器。需要真实播放或特殊页面时使用可选兼容执行器，单独安装、单独资源池、单独权限与启用开关。没有安装时展示“此任务需要兼容处理”，不悄悄改为模拟完成。

真实播放证据只由已验证的播放执行器产生；原生请求读取媒体字节、时钟推算和合成位置不自动获得该证据类型。研究探测、重复参数、伪造时长及计时绕过开关不进入产品能力。

## 3. 上游不可信输入

课程页、附件链接和图片 URL 不是任意代理权限。校验可信域名、TLS、重定向和解析后的目标，限制体积与时间；防止课程数据诱导访问本机、云元数据或管理网络。

上游响应保留必要摘要和来源版本，错误不直接原样返回用户。发现字段变化时让该适配器降级只读或停止，不能把解析失败当作任务已完成。

## 4. OCS-Tiku 的已核查版本与 fork 决定

仓库：<https://github.com/onprs/OCS-Tiku>。

相邻源码工作区无未提交修改，本地 HEAD 与本轮远程 HEAD 均为 `aedb64c55952f7914e4c46d59c732018605a9921`。已核查 README、`server.go`、`question.go`、`preprocess.go`、`solver.go`、`usage.go`、`history.go`、`store.go`。

现有能力：管理员配置上游服务和回答/图片转述模型；每次模型调用已记录 prompt/completion/total tokens、模型、协议、耗时和状态；历史检索使用单字/双字倒排索引与全文 LIKE，属于模糊候选。

现有缺口：`/api/search` 固定使用全局回答模型，不接受请求级模型；不读取精确缓存；响应只有 `question` 和 `answer`，不返回 token、模型、缓存命中或调用分解。

因此采用所有者维护的独立 fork，保留 upstream remote，新增能力以模块和 v2 API 为主。主平台不复制题库后端，不直接访问题库数据库。

## 5. fork 的服务边界与数据存储

```
主平台 Go 服务
  └─ OCSTikuProvider（服务端）
       └─ HTTPS + 服务间认证 / 私有网络
            └─ fork 题库服务
                 ├─ 上游模型服务（密钥只在 fork）
                 └─ PostgreSQL（独立数据库/角色）
```

- 普通用户和浏览器不直接调用 fork；管理员控制台通过主平台 API 操作公开条目，不接触上游密钥。
- fork 使用独立 PostgreSQL 数据库（建议 `ocs_tiku`）和独立角色；主平台数据库不复用其表，不做跨数据库 join。
- fork 的 v1 `/api/search` 保留兼容；主平台使用 v2 接口。
- 生产环境只监听私有地址或受保护入口；题库自带管理页和调试接口不暴露给最终用户。

## 6. v2 查询契约

主平台向 fork 发起：

```http
POST /api/v2/answers
Authorization: Bearer <内部服务令牌>
Idempotency-Key: <主平台查询事件 ID>
Content-Type: application/json
```

```json
{
  "request_id": "qq_01J...",
  "model_offering_id": "qm_standard",
  "mode": "cache_first",
  "cache_scope": "user",
  "cache_scope_key": "hmac-opaque-user-key",
  "question": {
    "title": "下列说法中正确的是？[图片1]",
    "options": "A. ...\nB. ...",
    "type": "single",
    "images": ["data:image/png;base64,..."]
  },
  "budget": { "max_output_tokens": 1024 }
}
```

- `model_offering_id` 是管理员发布的公开条目，不是任意上游模型名；为空时使用管理员默认。
- `mode` 为 `cache_first` 或 `always_llm`；未知值拒绝。
- `cache_scope` 为 `user` 或 `shared`；`shared` 仅在管理员启用后允许。`cache_scope_key` 是主平台用服务端密钥计算的不可逆用户范围键，不包含姓名/账号。
- `Idempotency-Key` 与 `request_id` 绑定请求摘要；相同键返回同一结果和用量，不同负载返回 409。
- 图片仍使用 `[图片N]` 与 `images` 顺序对应；主平台和 fork 都校验数量、类型、像素和体积。
- 已核查现有上游限制：搜索请求上限 32 MiB，单张解码数据不超过 20 MiB，像素不超过 16,000,000，超过 5 MiB 时使用其压缩流程。SaaS 可设置更低限额，但需明确提示，不能伪称与上游相同。
- `suggestion_*` 非空时会替换预处理输入。默认不发送模型猜测文本；如使用优化结果，保留原题、转换版本和用户复核入口。

成功响应：

```json
{
  "request_id": "qq_01J...",
  "status": "answered",
  "question": "规范化后的题目",
  "answer": "A",
  "model": {
    "offering_id": "qm_standard",
    "display_name": "标准模型",
    "capabilities": ["text"]
  },
  "mode": "cache_first",
  "cache": { "hit": false, "entry_id": null },
  "usage": {
    "input_tokens": 1284,
    "output_tokens": 96,
    "total_tokens": 1380,
    "complete": true,
    "calls": [
      { "slot": "answer", "input_tokens": 1284, "output_tokens": 96, "total_tokens": 1380, "status": "ok" }
    ]
  },
  "latency_ms": 2431
}
```

缓存命中返回 `status: "cache_hit"`、`cache.hit: true`、`cache.entry_id` 和 `cache.created_at`，`usage.total_tokens` 为 0，`usage.calls` 为空。缓存条目同时带模型、提示词/规范化版本和来源时间；不得把模糊历史候选包装成缓存命中。

答案仍是字符串，没有结构化置信度或统一选项 ID；不要虚构置信度。只有契约能唯一对应选项时才生成候选，用户确认后才提交。

失败响应明确区分：`invalid_question`、`model_unavailable`、`model_disabled`、`vision_unsupported`、`budget_exceeded`、`upstream_timeout`、`upstream_rate_limited`、`usage_incomplete`、`internal_error`。错误不返回上游密钥或完整请求。

## 7. 模型目录与公开条目

fork 内部管理模型服务与上游模型；主平台管理员发布公开条目。建议字段：

| 字段 | 说明 |
| --- | --- |
| `offering_id` | 主平台生成的稳定公开标识 |
| `display_name` / `description` | 用户可见名称与说明 |
| `provider_model_ref` | fork 内部上游引用，不向用户暴露 |
| `capabilities` | 文本、视觉、工具调用、最大上下文等 |
| `enabled` / `default` / `sort_order` | 可见性、默认项和排序 |
| `price_version` | 主平台价格版本；fork 不拥有钱包价格 |
| `cache_policy` | 是否允许缓存、作用域、TTL |
| `health` | 健康、降级、不可用 |

用户只能选择 `enabled` 且健康的条目。管理员不能删除已有安排引用的条目，只能下架；下架后相关后续任务进入“需要处理”，不静默回退到其他模型。

图片题：优先选择具备视觉能力的条目。若条目仅支持文本，fork 可使用管理员配置的图片转述模型，响应中包含 `caption` 调用和额外 token；没有可用转述模型时返回 `vision_unsupported`，不伪造答案。

## 8. 精确缓存规则

缓存键为规范化题目、选项顺序、题型、图片内容摘要、公开条目 ID、提示词版本、规范化版本和缓存作用域的 SHA-256。仅完整成功、非空、未报错的答案可写入。

- 默认 `user` 作用域；`shared` 需管理员显式启用并完成隐私评估。共享缓存不包含用户标识，但题目本身可能含敏感信息。
- 默认 TTL 7 天，管理员可在上限内调整；模型/提示词/规范化版本变化生成新键，旧条目自然过期。
- 缓存命中仍要展示原始题目、答案、来源模型和时间，由用户确认后才能提交。
- 用户报告答案错误时标记缓存条目无效并停止命中；不直接删除证据，保留最小审计。
- 现有倒排索引可用于“相似题目”提示或 agent 上下文，但不能自动作为精确缓存答案。
- 禁止缓存错误、超时、空答案、模型未完成或未经确认的用户输入。

缓存价值是减少 token，但不能牺牲正确性；共享缓存、缓存费用和失效策略仍需产品确认。

## 9. token 用量与计费连接

fork 已能按模型调用记录 prompt/completion/total tokens；v2 响应把这些事实回传并附 `complete` 标记。图片转述、agent 多轮和回答模型调用分别列在 `calls`，主平台可显示分解，但结算规则由价格服务决定。

主平台只信任服务端返回的用量，不能信任客户端上报。fork 是内部受信服务，仍要校验字段边界、总数为非负、`total = input + output`（若上游提供分明细）、调用数量上限和请求归属；缺失或矛盾时标记 `usage_incomplete`，不自动扣费。

token 数字来自上游模型响应，不是密码学证明；周期性用上游账单核对，发现差异进入人工复核。主平台账本记录查询 ID、价格版本、用量摘要和结算引用，不依赖 fork 数据库作为资金唯一来源。

## 10. 超时、并发、重试与幂等

题库有独立有界池，起始并发 2、请求超时建议 90 秒以覆盖模型延迟，主平台 Provider 超时略大于 fork 内部超时。超时取消会传递 HTTP 请求，但不能保证已开始的上游模型调用没有产生费用。

- 不自动重试结果不明的付费 POST；只有明确未发出、认证失败或可安全重放的场景才重试。
- 相同 `Idempotency-Key` 返回已保存结果；不重复调用模型、不重复计费。
- 同一用户的相同问题并发查询可合并等待，但不能把不同模型、不同模式或不同缓存作用域合并。
- 上游 429、余额不足或模型不可用停止相关查询并提示，不做无限退避。
- agent 迭代、图片转述和输出 token 受请求/服务端上限约束，超预算返回 `budget_exceeded`。

## 11. 管理、测试与维护

- 主平台管理员页“题库与模型”负责公开条目、默认项、价格版本、缓存策略和健康测试；fork 的内部密钥不进入主平台数据库。
- 可达性测试只检查网络与认证；功能性测试会真实调用模型并产生 token/费用，必须显式确认。
- 契约测试固定 v2 请求/响应、图片顺序、缓存命中、usage 分解、幂等和周时。
- fork 定期同步 upstream；新增代码放在独立模块/路由，保留 v1；每次同步运行既有 Go 测试、浏览器回归和新增契约测试。
- 上游没有 LICENSE 文件；作为所有者内部 fork 可使用，公开分发前补充许可证并核对依赖许可。

## 12. 数据保留

接口层不保存原始密码；题目原图/临时附件默认 24 小时，原题/答案缓存 7 天；请求幂等记录至少覆盖重试窗口，已结算用量和资金证据按财务层长期保留。主平台与 fork 各自遵守同一保留策略，不把题库日志中的完整题目无限复制。

题库失败、超时、缓存错误和答案争议进入待处理，不因保留期到期而丢失已结算财务证据。
