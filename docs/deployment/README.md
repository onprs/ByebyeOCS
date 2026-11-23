# 生产运维与灾难恢复手册

> **适用范围**：公开用户生产运维环境。首次安装、目录布局与反向代理配置请先阅读 [Linux 原生部署指南](../../deploy/README.md)；参数定义见 [主配置示例](../../configs/server.example.yaml) 与 [题库配置示例](../../configs/ocs-tiku.example.json)。

## 1. 安全基线与运行隔离

- **权限与网络隔离**：主服务（`byebyeocs`）与题库服务（`ocs-tiku`）分别使用独立的非 root 系统账号、独立的配置密钥目录（`0700`/`0600`）以及独立的 PostgreSQL 数据库与角色。业务进程仅监听 `127.0.0.1` 回环地址，公网 TLS 终结与 HTTP/2 由前置反向代理负责。
- **显式变更原则**：生产实例启动时只读核对数据库迁移版本与每条 SQL 的 SHA-256 摘要，遇到空库、缺号、摘要不一致或高于当前二进制的版本时立即拒绝启动。任何版本升级均须先备份再显式执行迁移。
- **凭据脱敏**：管理员密码、TOTP 种子、数据库 DSN、应用主密钥、身份索引密钥、ZPay 商户密钥与上游大模型 API 密钥均通过受保护文件注入，后台 API、前端界面与结构化日志均不回显明文秘密。

## 2. 业务能力分步启用清单

新部署实例默认关闭外部学习通连接、HTTP 任务执行、ZPay 支付与自动扣费，部署者按实际授权与准备情况逐项开启：

1. **学习通身份与只读同步（`learning.enabled`）**：
   - 须同时启用 `auth.enabled`，填写 `authorization_reference` 与稳定的 `subject_namespace`。
   - 支持两类主体探针：`json` 模式（校验 `subject_probe_url`、`subject_path` 与 `session_expiry_path`）或 `profile` 模式（读取服务端渲染账户资料页提取不可变 UID，配合 `school_probe_url` 与 1–168 小时的保守 `subject_ttl_hours`）。
2. **原生 HTTP 学习执行（`learning.execution_enabled`）**：
   - 依赖 `learning.enabled: true`，并填写独立的目标验证依据 `execution_evidence_reference`。
   - 所有者可在管理端“系统设置 → 运行安全”在线热调全局并发（`1–256`）、单账号并发（`1–global`）、单课同步章节并发（`1–64`）、上游最小请求间隔（`50ms–2s`）与自适应提速下限（`10ms–interval`），保存即时生效并原子写回主配置文件。
3. **独立题库与全自动测验（`questions.enabled`）**：
   - 在题库维护面板（管理端同域 `/panel/`）添加模型提供商、探测并保存上游模型（热加载并写回题库私密密钥文件），再为目标模型发布一对一的公开条目、微元价格版本与缓存期限。
   - 在主服务配置内网回环 `questions.url` 与至少 16 字节的 `questions.token_file`；跨用户共享缓存默认关闭（需主服务 `questions.allow_shared_cache`、题库 `cache.shared_enabled` 与条目策略同时允许才生效）。
4. **计费与 ZPay 在线收款（`payments.enabled` / `payments.charging_enabled`）**：
   - 生产模式开启收款或自动扣费时，强制要求 `operations.require_fresh_backup: true`。
   - 在主配置填写 ZPay（`zpayz.cn`）商户号、密钥文件及不含查询参数的 HTTPS `notify_url` / `return_url`；在管理端“系统设置 → 计费与价格”发布有效任务点单价（微元，1 分 = 10,000 微元）与舍入规则（`ceil` / `floor` / `half_up`），再开启对应扣费开关。未定价条目或题库返回 `usage_incomplete` 时系统自动跳过扣费并保留核对记录。

## 3. 异机加密备份与恢复演练

### 3.1 定期异机加密备份

`deploy/backup.sh` 读取 `/etc/byebyeocs/backup.env`（模板见 `deploy/backup.env.example`，文件须由 root 所有且权限 `0600`）：

1. 分别对主数据库和已启用的题库数据库执行 `pg_dump --format=custom` 一致性转储（两库各自一致，不跨库锁定同一物理时刻）。
2. 收集 `/etc/byebyeocs` 与 `/etc/ocs-tiku` 下的完整配置、当前主密钥、历史解密 keyring 及长期身份索引密钥，生成文件 SHA-256 清单。
3. 使用 `age` 公钥流式加密归档，经 `scp` 以 `.partial` 临时名上传至异机 SSH 目录，远端核对 SHA-256 一致并原子重命名后，才更新主服务读取的 `secrets/backup-status.json`（`last_recoverable_at` 保守记为转储开始时刻）。
4. 显式传入 `--local-only` 仅在本地生成加密归档用于临时维护，不会将 `offsite` 标为 `true`，也不会刷新生产恢复点状态。

主服务默认要求 `backup_max_age <= 15m`；一旦异机恢复点超时，系统自动阻断新充值下单与新自动扣费，保护未备份资金窗口。`age` 解密私钥必须脱离生产主机离线妥善保管。

### 3.2 隔离恢复演练

在任意具备 PostgreSQL 客户端工具与 `age` 的机器上，以**非 root 普通用户**执行隔离恢复演练：

```bash
/opt/byebyeocs/current/deploy/restore-drill.sh \
  --archive /path/to/byebyeocs-backup-YYYY-MM-DDTHH-MM-SSZ-PID.tar.age \
  --identity /path/to/offline-age-identity.txt \
  --report /tmp/restore-drill-report.json
```

脚本仅在 `/tmp` 下初始化独立临时 PostgreSQL 集群验证解密、清单哈希、`pg_restore` 与关键表可读性，结束后自动销毁临时目录，绝不触碰生产数据库或生产状态文件。

## 4. 生产灾难恢复标准流程

发生主机损毁或数据库故障需要从异机归档恢复生产服务时，严格按以下顺序执行：

1. **切断入口与副作用**：关闭反向代理公网流量，停止目标主机上的 `byebyeocs.service` 与 `ocs-tiku.service`，切勿向受损实例继续写入。
2. **解密并还原至新库**：先用 `restore-drill.sh` 校验归档完好，随后解密归档，还原 `/etc/byebyeocs` 与 `/etc/ocs-tiku` 配置及全部密钥环，使用 `pg_restore` 分别恢复主数据库与题库数据库到洁净的新数据库实例。
3. **放置恢复门控并关闭外部开关**：在主服务 `storage.data_dir`（默认 `/var/lib/byebyeocs`）下创建空文件 `.recovery-required`（属主 `byebyeocs`），并在 `config.yaml` 中临时将 `learning.execution_enabled`、`payments.enabled` 与 `payments.charging_enabled` 设为 `false`。`.recovery-required` 存在期间，系统禁止派发新外部任务与产生新资金预占，仅开放账务核对、预占释放与已有支付回调收敛。
4. **吊销恢复点残留会话并重放隐私闭户**：备份快照可能包含恢复点之后已被撤销的会话或已闭户的用户。在启动服务前，连接恢复后的主数据库执行以下事务：
   ```sql
   BEGIN;
   UPDATE public.client_sessions
   SET status = 'revoked', revoked_at = now(), revoke_reason = 'restore_recovery', updated_at = now()
   WHERE status = 'active';

   UPDATE public.admin_sessions
   SET status = 'revoked', revoked_at = now(), revoke_reason = 'restore_recovery', updated_at = now()
   WHERE status = 'active';

   UPDATE public.platform_sessions
   SET status = 'revoked', revoked_at = now(), revoke_reason = 'restore_recovery', updated_at = now()
   WHERE status = 'active';

   DELETE FROM public.login_challenges;

   UPDATE public.learning_plans
   SET status = 'paused', updated_at = now()
   WHERE status = 'active';

   UPDATE public.learning_tasks
   SET desired_state = 'pause', stop_requested_at = now(), version = version + 1, updated_at = now()
   WHERE state NOT IN ('confirmed', 'cancelled');

   SELECT public.reapply_privacy_closures();
   COMMIT;
   ```
   若备份时刻之后有已完成的闭户删除记录保存在外部合规台账中，额外执行 `SELECT public.reapply_privacy_closures(ARRAY['用户UUID']::uuid[]);` 补齐匿名化。
5. **启动对账与解锁**：启动 `ocs-tiku` 与 `byebyeocs`，登录管理端核对账本借贷平衡、ZPay 渠道未决订单主动查单、在途预占释放与题库查询记录。确认无资金差异后，立即执行一次完整异机加密备份刷新恢复点，删除 `.recovery-required` 文件，按需恢复配置开关并重启服务开放公网访问。

## 5. 加密主密钥平滑轮换

`auth.identity_key_file` 用于计算稳定的外部主体 HMAC 索引与用户缓存隔离域，常规维护保持不变；`auth.encryption_key_file`（AES-GCM 主密钥，保护平台会话密文、管理员 TOTP 种子与加密导出包）支持在线平滑轮换：

1. 执行一次完整备份，确保旧密钥已离线留存。
2. 生成新版本密钥文件（命令拒绝覆盖已有文件）：
   ```bash
   sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs keys generate \
     --output /etc/byebyeocs/secrets/application-v2.key
   ```
3. 更新 `/etc/byebyeocs/config.yaml` 中的 `auth` 段落，将新密钥设为当前写入版本，同时把旧密钥保留在 `decryption_key_files` 解密环中（最多支持 8 个版本）：
   ```yaml
   auth:
     encryption_key_file: "secrets/application-v2.key"
     encryption_key_version: 2
     identity_key_file: "secrets/identity.key"
     decryption_key_files:
       1: "secrets/application-v1.key"
   ```
4. 重启 `byebyeocs.service`，使新写入数据立即采用版本 `2`，旧密文按版本号透明解密。
5. 执行批量重封装命令，将数据库中存量密文逐批迁移至版本 `2`：
   ```bash
   sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs keys reencrypt \
     --config /etc/byebyeocs/config.yaml
   ```
6. 待短期隐私导出文件全部过期、且依赖旧密钥的历史备份均退出保留周期后，再从 `decryption_key_files` 中移除旧版本配置；同步执行异机备份更新密钥环归档。

## 6. 日常水位监控与巡检要点

- **磁盘三级保护**：数据分区使用率达 `70%` 时在管理端运行总览发出提醒；达 `80%` 时拒绝新学习任务派发、新数据导出与新充值下单；达 `90%` 时进入严格维护保护，仅允许必要释放与恢复操作。发现水位升高时优先检查日志目录、本地临时备份归档与过期导出文件。
- **健康探针**：主服务管理监听（默认 `127.0.0.1:8091`）提供 `/health/live`（存活检查）与 `/health/ready`（数据库连通性及迁移就绪检查，停机排水期间返回 `503`）；题库服务提供 `/healthz` 与 `/readyz`。
- **隐私请求处理**：用户提交账号删除申请后，系统立即吊销其会话与平台执行授权；管理员在“隐私请求”页面核对无未决订单与在途预占后批准闭户，法定保留的不可变账本分录与审计记录自动脱离个人可识别信息。
