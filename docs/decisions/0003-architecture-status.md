# 决策 0003：架构基线与外部高风险能力启用门槛

- **状态**：已采纳并落地

## 背景

平台同时涉及外部学习通账号交互、大模型 API 计费调用与在线资金收付。若在缺少明确授权、未发布价格或未配置异机备份的情况下默认开启外部副作用，极易引发越权访问、账务错乱或数据丢失不可恢复风险。

## 决策

1. **模块化单体与原生部署基线**：采用 Linux 原生 Go 二进制、systemd 服务沙盒与 PostgreSQL 持久化，不引入微服务网格、Docker 运行时依赖或外部消息队列。
2. **默认安全关闭（Fail-Closed）**：外部学习通连接（`learning.enabled`）、原生任务执行（`learning.execution_enabled`）、独立题库调用（`questions.enabled`）、在线支付（`payments.enabled`）与自动扣费（`payments.charging_enabled`）在默认配置中全部关闭。
3. **显式准入门槛**：
   - 开启平台连接与执行必须显式配置授权依据、稳定主体命名空间、受信任探针及独立的目标验证依据；
   - 生产模式开启支付或自动扣费必须同时启用异机备份新鲜度门控（`require_fresh_backup: true`，恢复点不超过 15 分钟）；
   - 未发布有效单价或上游返回 `usage_incomplete` 时，严禁自动扣费或将其伪装为 0 元账单。

## 当前落点与影响

- 启动配置校验器（`server/internal/platform/config`）在进程启动阶段拦截一切缺项或矛盾开关组合；公开能力接口（`/api/v1/capabilities`）实时反映当前实例已启用的真实能力边界。
