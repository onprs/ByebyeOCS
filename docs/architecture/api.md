# REST API、实时事件与版本兼容

已实现双端认证、业务、幂等、版本、隐私和SSE，机器可读契约见 [`api/openapi.yaml`](../../api/openapi.yaml)。React管理端和Flutter客户端调用正式Go服务，设计原型继续独立。下文说明接口设计规则；确切路径、字段和响应以当前OpenAPI与服务路由为准。

## 1. 共同约定

- 用户路径 `/api/v1`，管理员路径 `/admin/api/v1`，不同认证与权限。
- ID、金额最小单位和游标使用字符串；时间为 UTC RFC 3339，展示时转换时区。
- 修改安排和偏好使用 `ETag` / `If-Match`；缺失必要条件返回 428，冲突返回 412。
- 下单、人工调整、退款、提交测验等命令要求 `Idempotency-Key`。按主体、方法、路径和请求摘要作用域隔离，相同键不同请求返回 409。
- 幂等记录与业务事务共同保存。保留期覆盖业务最长生命周期；过期意图需要重新确认，不能把旧键悄悄当新请求执行。账务业务唯一键永久保护资金记录。
- 分页支持有界 limit 与稳定游标，默认不返回全量记录；管理员多选仅影响显式 ID 集。
- 认证失败 401、权限不足 403、输入无效 422；错误包含业务码、可读信息、request_id、可重试性和影响范围，不包含上游敏感内容。

```json
{
  "error": {
    "code": "plan_version_conflict",
    "message": "安排已更新，请重新加载后编辑。",
    "request_id": "req_...",
    "retryable": false,
    "scope": "request"
  }
}
```

## 2. 用户 API

| 方法与路径 | 作用 | 关键约束 |
| --- | --- | --- |
| POST `/auth/chaoxing/challenges` | 创建已批准方式的登录挑战 | 返回能力、到期与隐私版本，不假设 OAuth |
| GET `/auth/chaoxing/challenges/{id}` | 查询扫码/验证状态 | 挑战绑定当前客户端、限次与过期 |
| POST `/auth/chaoxing/challenges/{id}/verify` | 提交持有人完成的验证 | 敏感输入不记录；验证后关联内部用户 |
| POST `/auth/refresh` | 刷新 App 会话或轮换 Web 会话 | 一次性刷新链，复用撤销 |
| POST `/auth/logout` | 撤销客户端会话 | 明确是否同时撤销云端执行授权 |
| GET `/me` | 身份摘要与会话提示 | 无密码、Cookie 或外部令牌 |
| GET/PATCH `/me/preferences` | 配色、浅深模式、通知 | 默认石墨，版本更新 |
| POST `/me/data-exports` | 请求导出 | 新鲜验证、异步、受限下载 |
| POST `/me/deletion-requests` | 请求删除 | 资金和必要保留记录先复核 |
| POST `/me/identity-appeals` | 身份变更申诉 | 不自动转移余额 |
| GET `/courses` | 课程摘要 | 平台已确认和待完成分别统计 |
| POST `/course-syncs` | 发起同步 | 202，后台任务、账号去重和限速 |
| GET `/courses/{id}` | 章节、任务点与普通资源 | 校验用户归属 |
| GET/POST `/plans` | 列表或建立安排 | 不可变版本、范围校验 |
| GET/PATCH `/plans/{id}` | 查看和编辑 | If-Match，返回新版本及生效边界 |
| POST `/plans/{id}/controls` | 启停后续安排 | 202，不伪称当前任务已停止 |
| GET `/tasks` | 当前任务、异常、历史 | 按用户、安排和状态过滤 |
| GET `/tasks/{id}` | 任务证据与时间线 | 观察/平台/媒体字段独立 |
| POST `/tasks/{id}/controls` | 暂停、继续、取消、重调度 | 状态条件、命令版本和幂等 |
| GET `/question-models` | 管理员已发布且用户可用的模型条目 | 不含上游模型名、服务密钥或内部健康细节 |
| POST `/question-queries` | 查询答案，提交模型条目、`cache_first`/`always_llm`、预算 | 服务端价格快照、预占、`Idempotency-Key`；不能提交任意上游模型 |
| GET `/question-queries/{id}` | 原题、答案、模型、缓存命中、token 分解与状态 | 无虚构置信度；不返回上游密钥 |
| POST `/question-cache-reports` | 报告错误缓存/答案 | 标记待复核，不直接删除财务证据 |
| 安排中的题库策略 | 后台自动取题、作答、提交与复核 | 不提供人工提交API；按冻结版本、试卷指纹和持久提交状态执行 |
| GET `/pricing`、GET `/usage` | 已确定的用量模式、有效价格/计量版本、个人用量和结算 | 单位或价格未配置时明确不可收费，不能当 0 元价格 |
| GET `/wallet` | 可用、预占与币种 | 字符串整数分 |
| GET `/wallet/entries` | 充值/消费/释放/退款 | 可解释业务关联与价格版本 |
| GET/POST `/recharge-orders` | 查询或创建充值 | 服务端校验金额、渠道和唯一意图 |
| GET `/recharge-orders/{id}` | 支付与到账独立状态 | 不依赖返回页事实 |
| GET `/capabilities` | 版本、授权模式、渠道和可用任务类型 | 服务端策略，客户端不能自己启用能力 |
| GET `/events` | 用户 SSE | 同源 Cookie 或 App 认证头 |

## 3. 管理员 API

| 方法与路径 | 权限和用途 |
| --- | --- |
| GET `/capabilities` | 管理员API版本、实际可用能力、配置缺失原因与外观选项 |
| POST `/auth/login`、POST `/auth/step-up` | 独立管理员认证与敏感操作提升验证 |
| GET/PATCH `/me/preferences` | 当前管理员外观偏好，与普通用户隔离 |
| GET `/dashboard` | 聚合运行、财务和服务摘要 |
| GET `/users`、GET `/users/{id}` | 脱敏用户/账号摘要 |
| POST `/account-operations` | 显式账号集合的暂停、限制、配置；原因必填 |
| GET `/tasks`、GET `/tasks/{id}` | 跨用户任务与归属，权限控制 |
| POST `/task-operations` | 逐项控制和冲突结果，审计 |
| GET `/orders`、GET `/wallets/{id}/entries` | 订单与账本，限制财务可见范围 |
| POST `/wallet-adjustments` | 金额、原因、关联单据、提升验证和专用权限 |
| POST `/refunds` | 资金来源核对、渠道能力和审批 |
| GET/POST `/reconciliations` | 对账状态、复核与异步扫描 |
| GET/PATCH `/settings/{group}` | 配置版本与校验；密钥只接受安全写入，不返回原值 |
| GET/POST/PATCH `/question-models` | 发布、下架、排序、能力、默认项、价格版本和缓存策略 | 下架不删除历史；已引用条目不能静默切换 |
| POST `/question-models/{id}/tests` | 可达性/功能性测试分开 | 功能性测试会产生 token 费用并需确认 |
| GET `/question-usage` | 查询次数、缓存命中、输入/输出 token、估算微元成本 | 价格未发布时显示“未定价”，不显示虚假收入 |
| GET `/question-queries` | 跨用户查询与用量，权限受限 | 题目内容按权限和保留期脱敏 |
| POST `/question-cache/invalidations` | 标记错误缓存失效 | 记录操作者、原因、影响条目 |
| POST `/providers/{id}/tests` | 可达性/功能测试分开，功能测试需成本确认 |
| GET `/audit-events` | 脱敏审计、分页和导出权限 |
| POST `/backups`、POST `/maintenance/updates` | 提升验证、备份/签名/维护流程，不执行任意 shell |
| GET `/events` | 管理员事件流，受角色过滤 |

支付回调使用单独 `/payments/callbacks/{provider}` 入口，不使用用户 Cookie；通过提供商验签、商户与订单校验认证。路径命中不代表可以写钱包。

## 4. 代表性数据

```json
{
  "id": "task_...",
  "state": "waiting_verification",
  "desired_state": "run",
  "version": 7,
  "observation": { "fraction": 1.0, "source": "executor", "observed_at": "2026-10-20T06:30:00Z" },
  "platform": { "status": "unconfirmed", "checked_at": "2026-10-20T06:30:02Z" },
  "media_evidence": { "available": false },
  "estimate": { "remaining_seconds_min": null, "remaining_seconds_max": null, "estimated": true }
}
```

进度观察可以为浮点比例，资金仍必须整数；观察结束不变成平台通过。没有足够依据时不输出预计时长，不能制造倒计时。

题库查询请求：

```json
{
  "model_offering_id": "qm_standard",
  "mode": "cache_first",
  "question": {
    "title": "下列说法中正确的是？",
    "options": "A. ...\nB. ...",
    "type": "single",
    "images": []
  },
  "budget": { "max_output_tokens": 1024 }
}
```

客户端传 `Idempotency-Key`；服务端校验配额、模型可见性、价格/预占和图片权限，再调用 fork。主平台不接受客户端自定义的上游模型名或服务地址。

题库查询响应：

```json
{
  "id": "qq_...",
  "status": "answered",
  "model": { "offering_id": "qm_standard", "display_name": "标准模型" },
  "mode": "cache_first",
  "cache": { "hit": false },
  "answer": "A",
  "usage": {
    "input_tokens": 1284,
    "output_tokens": 96,
    "total_tokens": 1380,
    "complete": true,
    "calls": [
      { "slot": "answer", "input_tokens": 1284, "output_tokens": 96, "total_tokens": 1380 }
    ]
  },
  "pricing": { "price_version": "v1", "cost_micros": "4716", "state": "quoted" }
}
```

缓存命中时 `cache.hit=true`、`usage.total_tokens=0`、`calls=[]`；价格未发布时 `pricing.state="unpriced"`，不能把 `cost_micros` 当 0 元账单。金额字段用十进制字符串，避免客户端数字精度问题。

```json
{
  "color_scheme": "graphite",
  "theme_mode": "system",
  "notifications": { "task_attention": true, "wallet_changes": true },
  "version": 1
}
```

正式外观模式支持 `system/light/dark`。当前原型提供 light/dark，本机保存配色和模式；跨设备同步由正式偏好接口实现。

## 5. SSE 持久化与顺序

业务事务只写 Outbox UUID 与资源版本，不直接发送事件。单实例投递器在业务提交后，将 Outbox 转成用户/管理员投递流，锁定流头递增序号，并在同一短事务提交流条目和新流头，再向连接推送。

**不能直接把 PostgreSQL 自增 ID 当作事务提交顺序。** 两个并发事务可能先分配 ID 后反序提交，导致按最大 ID 补偿漏事件。投递流序号在提交后的串行发布事务中分配；同一资源事件仍带版本，客户端丢弃旧版本。

```text
event: wallet.updated
id: 184
data: {"event_id":"evt_...","resource_id":"wallet_...","resource_version":12,"available_minor":"5450"}
```

首次快照包含流 watermark。客户端按 watermark 连接并补偿；快照可能领先尚未发布的业务事件，此时依靠资源版本忽略旧状态，不能回退余额。

重连使用 `Last-Event-ID` 或不含秘密的游标。游标过期返回 `resync_required`，客户端重新取快照。SSE 流默认保留 3 天、已投递 Outbox 7 天；客户端缓冲和补偿批量有限制；慢连接断开后补偿，不无限占内存。

心跳是进程内网络写入，不每次写数据库。认证到期、权限变化、账号撤销和服务更新触发重连/重验证。管理员事件按角色过滤，不携带其他角色不可见的账务或身份详情。

## 6. 版本与降级

能力接口包含 API 主版本、最小客户端版本和功能开关。尚未发布正式客户端时 `minimum_client_version` 为 null，不虚构兼容版本。新增字段保持兼容，未知枚举由客户端显示可理解的待处理状态，不能默认为成功。

SSE 不可用时对正在运行任务和待到账订单使用有界轮询；回到前台先取快照，不只补一串旧事件。客户端缓存的余额与进度不作为执行、扣费或提交依据。当前已确定主服务按有效任务点用量、题库独立收费；单价或题库价格未配置时 `capabilities` 必须关闭真实扣费。

Web 产物按内容哈希缓存，入口及时重新验证；Flutter/React 的旧资源与服务端兼容窗口需测试。API 不通过一条最低版本提示直接删除用户历史或让已创建订单失效。
