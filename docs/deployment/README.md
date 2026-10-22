# 部署与维护

本文面向公开用户自行管理的 Linux 生产环境。首次安装、PostgreSQL角色、HTTPS与静态应用步骤见 [安装指南](../../deploy/README.md)，配置见 [主服务示例](../../configs/server.example.yaml) 和 [独立题库示例](../../configs/ocs-tiku.example.json)。开发维护环境的验证记录见 [本地实现](../development/local-implementation.md)。

## 基础要求与隔离

运行发布目录需要 Linux amd64/arm64、PostgreSQL16+、systemd 和两个独立HTTPS入口。备份另需 PostgreSQL16+客户端工具、age、SSH/scp，以及脚本声明的通用工具。构建才需要Go、Node与Flutter。任务执行通过原生HTTP，服务运行不需要浏览器、播放器或媒体解码器。

主服务和题库分别使用系统用户、数据库角色与数据库。监听默认仅回环，TLS由反向代理提供；来源必须与实际HTTPS地址匹配。原生客户端连接自己的服务HTTPS地址，Web使用同源API。

生产启动不自动迁移、不生成账号、不写业务演示数据。首次迁移后通过CLI显式创建管理员，密码从私有文件读取；用 `--totp-secret-file` 提供自己的TOTP材料，或用 `--totp-output-file` 写入私有的一次性绑定文件。通过验证器保存TOTP后安全删除一次性文件。

## 功能与价格

初始配置关闭真实平台、HTTP学习执行、付款和自动收费。学习通接入须填写已核实的命名空间、授权依据和稳定主体/会话到期JSON探针。学习执行由`learning.execution_enabled`控制，启用前填写独立的`execution_evidence_reference`。执行器读取平台任务上下文并保持普通请求节奏，交互或验证需求转人工处理；独立平台核验决定完成状态。

管理员发布任务点价格及每个模型价格，配置单位精度、结算/舍入方式；每个模型必须使用新版本变更费率。部署授权开关和管理员收费开关共同决定新操作能否收费，关闭收费不把价格改成零。未定价、用量不完整和未知结果进入待定/复核。人民币每分固定10000微元。

ZPay商户信息、通道、通知域名和经营类别须按自己的商户实际能力配置。返回页不负责入账。回调验签/查单后的数据库事务负责订单和到账；未决支付和退款保留复核入口。

题库使用独立PostgreSQL和服务令牌，上游模型密钥仅在题库。主服务发布公开ID；`cache_first`和`always_llm`的缓存及token规则见 [题库适配](../architecture/integrations.md)。平台答案提交当前关闭，必要的人工确认始终保留。

## 备份与恢复点

`deploy/backup.sh`生成PostgreSQL一致性逻辑备份，将主/题库配置和私密keyring一起用age加密，并在异机传输与校验成功后更新私有状态。两个数据库分别取得一致快照，不宣称跨数据库原子时刻。本地归档不能写成异机成功恢复点。

age解密身份单独离线保管；归档中的配置须包含全部当前/旧解密密钥与长期身份索引密钥。启用题库后同时配置题库数据库和配置目录，不遗漏上游密钥。根据数据量、带宽和备份完成时间安排周期、空间预算与告警。默认新收费恢复点最大年龄15分钟，超时即保护新操作；实际RPO取决于成功恢复点，不能用计划时间代替。

使用 `deploy/restore-drill.sh` 在普通用户的临时initdb集群解密并恢复可信自有归档。它不会连接或写生产数据库，不更新生产备份状态。一次演练通过不证明长期可恢复性或2C4G容量；持续WAL/pgBackRest方案与目标见 [数据保留](../architecture/data-retention.md)。

## 生产恢复

保持应用停止和外部流量关闭，先在隔离环境验证归档、配置与keyring，再按数据库维护流程恢复到新库。不要覆盖仍在提供服务的实例。题库数据库与模型配置分别恢复和核对。

恢复后、应用启动前，在配置的data_dir创建 `.recovery-required` 文件。该标记阻止新外部工作和资金承诺；已有支付回调、释放及必要对账依旧由各自路径处理。配置中保持学习执行、付款和自动收费关闭，完成核对后再逐项开启。

备份可能包含曾被撤销的会话。通过私有连接在恢复库执行以下事务，使所有用户与管理员重新验证；命令仅用于已经停止服务的恢复环境：

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
UPDATE public.learning_plans SET status = 'paused', updated_at = now()
WHERE status = 'active';
UPDATE public.learning_tasks
SET desired_state = 'pause', stop_requested_at = now(), version = version + 1, updated_at = now()
WHERE state NOT IN ('confirmed', 'cancelled');
SELECT public.reapply_privacy_closures();
COMMIT;
```

若恢复点早于删除完成，函数无法推断随后关闭的用户。必须从备份之外保留的最小关闭清单补录：

```sql
SELECT public.reapply_privacy_closures(ARRAY['待重放的用户UUID']::uuid[]);
```

清单只保留内部用户UUID及必要闭户事实，按敏感维护数据隔离保管；它不重新结算资金，不删除账本。真实执行前替换占位值并核对清单来源。

随后核对未决支付、到账、预占、退款、题库用量、任务确认和外部副作用，处理恢复点之后的事实；重新验证学习账号，不自动继续旧任务。完成恢复检查、取得新鲜异机备份后移除标记，逐项恢复功能与流量。RPO≤15分钟/RTO≤4小时是待现场演练的目标。

## 加密主密钥轮换

长期 `identity_key_file` 用于稳定身份索引和用户隔离作用域，正常轮换不改变它。加密主密钥负责主体、平台会话、管理员TOTP与隐私导出。

先保存完整备份和旧密钥，再生成新文件；新文件不存在时才会写入：

```bash
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs keys generate \
  --output /etc/byebyeocs/secrets/application-v2.key
```

将配置调整为：

```yaml
auth:
  encryption_key_file: secrets/application-v2.key
  encryption_key_version: 2
  identity_key_file: secrets/identity.key
  decryption_key_files:
    1: secrets/application-v1.key
```

保留其余auth设置。重启服务后新密文使用v2，旧密文仍按记录版本读取。执行：

```bash
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs keys reencrypt \
  --config /etc/byebyeocs/config.yaml
```

命令按小批事务重封装，失败可重试，输出计数不输出秘密。短期隐私导出也可能引用旧版本；保留旧解密文件，直至相关导出已到期且旧备份已按保留策略退出。同步更新异机keyring，核对登录/TOTP和平台会话后再规划移除旧版本；最多保留8个解密版本。

## 日常维护

- 磁盘70%提醒，80%保护，90%维护；检查导出、日志、题库正文与备份空间。
- 运行总览与审计保留安全状态，不把密码、Cookie、支付密钥或完整上游响应放进工单。
- 删除申请立即撤销授权；资金或运行活动未清只能延后，完成闭户保留不可变财务/审计与去重标记。驳回后用户重新验证，任务保持暂停。
- 升级、回滚仅切程序与静态资源；数据库版本需要按兼容性检查和独立恢复计划处理。安装/升级脚本不隐式迁移，不覆盖配置和密钥。
- Android发布签名、iOS构建/签名与分发、下载站地址独立准备；没有通用IPA直装承诺。
