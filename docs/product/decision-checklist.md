# 决策与启用核对清单

> **适用范围**：汇总项目已锁定的全部核心产品与技术决策，并提供自托管生产环境上线前的逐项配置核对表。详细决策背景见 `docs/decisions/` 目录下的各篇 ADR。

## 1. 已锁定方案基线

以下架构与交互方向已在工程实现中完全固化，日常迭代直接沿用：

| 领域 | 已确认基线 | 对应决策记录 |
| --- | --- | --- |
| 终端形态 | 用户端采用同一套 Flutter 工程构建 Web、Android、iOS；管理端采用独立 React 应用；`prototypes/` 仅作历史视觉参照 | [0001](../decisions/0001-ui-prototype.md)、[0005](../decisions/0005-product-baseline.md) |
| 视觉与主题 | 默认采用 `graphite`（石墨），保留 `grove`（青禾）与 `indigo`（靛蓝）可选；支持跟随系统、浅色与深色模式，三端统一几何密度 | [0002](../decisions/0002-default-graphite.md) |
| 数据存储 | PostgreSQL 16+ 为唯一正式数据库；主服务与独立题库分别使用独立数据库与角色，启动显式校验迁移，无 SQLite 生产分支 | [0004](../decisions/0004-postgresql.md)、[0007](../decisions/0007-server-foundation.md) |
| 身份模型 | 普通用户以学习通不可变主体（支持 JSON 探针与账户资料页 `profile` 探针）绑定唯一内部用户与钱包；管理员采用 Argon2id + TOTP + 五角色 RBAC | [0008](../decisions/0008-business-implementation.md)、[0012](../decisions/0012-chaoxing-profile-probe.md) |
| 执行方式 | 生产任务统一使用原生 HTTP 直接请求（`chaoxing.HTTPExecutor`），无浏览器或解码器依赖；单任务稳定键独占，观察进度、平台核验与真实播放证据分离 | [0009](../decisions/0009-native-http-execution.md) |
| 自动测验 | 安排启用题库后全自动取题、几何字形还原、查询、核对试卷、防重放提交并独立复核平台结果，无人工确认环节 | [0010](../decisions/0010-automatic-questions.md) |
| 题库治理 | 维护独立 `ByebyeOCS-Tiku` 子模块，支持请求级模型选择、`cache_first` 精确缓存与服务端 token 回传；提供商、上游模型、公开目录与价格统一在 `/panel/` 维护 | [0006](../decisions/0006-tiku-fork-token-billing.md)、[0011](../decisions/0011-tiku-panel-catalogue.md) |
| 计费与收款 | 人民币整数分复式记账，微元版本化单价；主服务按首次平台确认的有效任务点计费，题库按服务端完整 token 独立计费；收款对接 ZPay（`zpayz.cn`），正常充值原则上不自助退回 | [0003](../decisions/0003-architecture-status.md)、[0005](../decisions/0005-product-baseline.md) |
| 部署与容量 | Linux 原生二进制 + systemd 沙盒 + 双 HTTPS 反向代理，面向约 2C4G / 30GB 目标预算设计分层保留与异机加密备份 | [0005](../decisions/0005-product-baseline.md)、[0007](../decisions/0007-server-foundation.md) |

## 2. 生产上线配置核对表

部署者在自托管 Linux 主机开放真实业务前，应逐项核对以下清单：

### 2.1 基础安全与数据库

- [ ] 用户端 HTTPS 域名与管理端 HTTPS 域名彼此独立，已配置有效 TLS 证书，且与 `auth.user_origin`、`auth.admin_origin` 及题库 `panel.origin` 精确一致。
- [ ] 主服务与题库服务运行在各自独立的非 root 系统用户下，仅监听回环地址，反向代理已对 `/api/v1/events` 与 `/admin/api/v1/events` 关闭缓冲与压缩。
- [ ] `secrets/application-v1.key` 与 `secrets/identity.key` 为两把互不相同的 32 字节密钥，所有 `secrets/` 文件权限均为 `0600`。
- [ ] 主库（34 个迁移）与题库库（8 个迁移）均已显式执行迁移，`byebyeocs doctor` 返回 `status: ok`；已创建首个 `owner` 管理员并妥善绑定 TOTP。

### 2.2 备份与运行保护

- [ ] 已配置 `/etc/byebyeocs/backup.env` 的 `age` 公钥与异机 SSH 目录，定时任务运行正常，`secrets/backup-status.json` 中 `offsite: true` 且距今不超过 15 分钟。
- [ ] `age` 私钥已脱离生产服务器离线保存，且通过 `deploy/restore-drill.sh` 验证归档可完整解密恢复。
- [ ] 数据目录所在磁盘剩余空间充足（使用率低于 70%），不存在遗留的 `.recovery-required` 恢复门控文件。

### 2.3 平台、题库与支付参数

- [ ] 如开启学习通连接：已填入核实有效的 `subject_namespace`、探针地址与 `authorization_reference`；如需开启执行，已完成目标课程验证并填入 `execution_evidence_reference`。
- [ ] 如开启题库：已在 `/panel/` 添加上游模型并通过连通性与功能测试，已发布至少一个可见的公开模型条目及有效微元价格版本，主服务 `questions.url` 与 `token_file` 已对齐。
- [ ] 如开启计费与收款：已在管理端“系统设置 → 计费与价格”发布有效任务点单价与舍入方式，已配置 ZPay 商户号、密钥文件及无查询参数的 HTTPS `notify_url` / `return_url`，并完成真实回调验签与查单验证。
