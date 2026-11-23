# 身份认证、会话与密钥架构

> **适用范围**：安全与认证架构规范，对应 `server/internal/auth`、`server/internal/platform/config` 及独立题库 `services/tiku/internal/platform/panel`。

## 1. 三类身份隔离模型

系统在数据库表、会话凭据、监听端口与权限校验四个层面将三类操作主体彻底隔离：

1. **普通学习用户（`users` / `external_identities`）**：仅在用户监听（`8090`）认证，权限固定为 `self`，所有业务查询强制绑定 `user_id` 归属链。
2. **平台管理员（`admin_users`）**：仅在管理监听（`8091`）通过 Argon2id 密码与 TOTP 动态码认证，按 RBAC 角色授予细分权限，无法伪装成普通用户，也无法读取用户明文密码或学习通 Cookie。
3. **题库面板维护员（`panel.users`）**：仅在独立题库服务 `/panel/api/*` 认证，使用独立密码哈希文件与进程内会话，主平台管理员会话与服务间 Bearer 令牌均不能跨用登录题库面板。

## 2. 学习通外部主体核验与双探针机制

用户提交学习通账号密码后，服务端拒绝信任客户端自报的 UID 或登录响应 Cookie 中的未核验 `_uid`，强制通过配置的受信任 HTTPS 探针读取不可变外部主体（见 [决策 0012](../decisions/0012-chaoxing-profile-probe.md)）：

- **`json` 探针模式**：向固定 `subject_probe_url` 发起请求，按配置的 JSON 路径提取稳定主体 ID、姓名、学校及平台会话到期时间。
- **`profile` 账户资料探针模式**：请求学习通服务端渲染的账户管理页（提取不可变 `uid` 与姓名，缺失或结构异常立即拒绝登录），同时从 `school_probe_url` 读取所属学校（若遇到上游下发 `ws` Cookie 的重定向，拒绝降级为明文 HTTP，仅在原 HTTPS 地址安全重试一次；学校接口暂不可用时不阻断登录），平台会话采用配置的保守有效期 `subject_ttl_hours`（`1–168` 小时）。

核验通过后，服务端使用独立的 32 字节长期身份索引密钥（`identity_key_file`）对 `subject_namespace + ":" + raw_uid` 计算不可逆 HMAC-SHA256 摘要，以此唯一定位 `external_identities` 与对应的内部 `user_id` 及钱包。平台会话 Cookie 经 AES-256-GCM 主密钥加密后存入 `platform_sessions`。

## 3. Web 与原生客户端双轨会话机制

### 3.1 Web 端（用户 Web 与管理员控制台）

- 用户端与管理端使用不同的 `HttpOnly`、生产强制 `Secure` 的会话 Cookie 名称，配置在不同的 HTTPS `Origin` 下，从源头阻断 Cookie 跨端混串。
- 所有非幂等写请求（`POST` / `PUT` / `PATCH` / `DELETE`）执行双重跨站防御：精确核对请求 `Origin`（及 `Sec-Fetch-Site`）与配置的 `user_origin` / `admin_origin` 完全匹配，并校验常数时间比对的 CSRF 令牌请求头。

### 3.2 Android / iOS 原生客户端

- 登录成功后签发短期有效的 Bearer 访问令牌与高熵随机刷新令牌（数据库仅存储刷新令牌的 SHA-256 摘要及令牌族编号）。
- **单次轮换与重放熔断**：每次调用刷新接口时，原刷新令牌立即标记已消费并签发新一对令牌；若已消费的旧刷新令牌被再次提交（表明疑似令牌泄露或并发重放攻击），服务端立即吊销该令牌族下的全部活跃会话，强制重新登录。
- 每个用户的并发活跃会话数受配额限制，超出上限自动淘汰最旧未锁定会话；用户可在设置页随时查看并单点吊销其他设备会话。

## 4. 管理员认证、五角色 RBAC 与步进验证

- **双因素与限流**：管理员密码采用 Argon2id 哈希存储，登录强制验证 TOTP 动态验证码；按真实客户端 IP（仅解析 `server.trusted_proxies` 回环代理转发的一层 `X-Forwarded-For`）与账号维度实施滑动窗口失败限流。
- **五种内置角色权限矩阵**：
  - `owner`：拥有通配权限 `*`（含 `admin.users.manage` 管理员管控与运行安全参数持久化热调）；数据库层与应用层双重保护，严禁停用、删除或降级系统中最后一个有效 `owner`。
  - `operator`：具备 `learning.read`、`learning.tasks.operate`、`learning.accounts.operate`、`operations.read`、`events.read`、`question:read` 与 `admin.self`。
  - `support`：具备 `learning.read`、`question:read`、`privacy.review` 与 `admin.self`。
  - `finance`：具备 `billing.read`、`billing.manage`、`wallet.adjust`、`wallet.refund`、`reconciliation.manage` 与 `admin.self`。
  - `observer`：只读观察角色，具备 `learning.read`、`billing.read`、`question:read`、`operations.read`、`audit.read`、`events.read` 与 `admin.self`。
- **敏感操作步进验证（Step-up Auth）**：人工调整用户余额、审批退款、修改核心计费策略、管理管理员账号以及普通用户发起账号闭户删除时，服务端校验会话近期验证时间戳，超时则要求重新验证密码/TOTP 或近期登录挑战。

## 5. 双密钥分离与在线无感轮换

系统强制要求 `identity_key_file`（HMAC 身份索引与用户缓存域派生密钥）与 `encryption_key_file`（AES-256-GCM 数据封装主密钥）使用两把完全不同的 32 字节密钥：

- 每条加密记录（平台会话密文、管理员 TOTP 种子、加密数据导出文件）均显式保存 `key_version` 版本号与独立随机 Nonce。
- 配置支持在 `auth.decryption_key_files` 中同时挂载最多 8 个历史版本的解密密钥。轮换主密钥时无需停机重写身份索引或变更用户/钱包 UUID，重启切换新写入版本后运行 `byebyeocs keys reencrypt` 即可按小事务逐批完成存量密文重封装。
