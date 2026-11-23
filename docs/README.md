# ByebyeOCS 文档中心

本目录收录 ByebyeOCS 的产品规范、界面设计、系统架构、架构决策记录（ADR）、生产运维与本地开发文档。

> **环境阅读提示**：各篇文档在首段标明适用环境。面向**公开用户环境**的文档仅依赖项目声明的通用系统与工具要求；面向**开发维护环境**的文档供代码贡献者理解内部机制、运行跨平台调试与执行回归测试。面向最终用户的快速入门请先阅读仓库根目录 [README.md](../README.md)，全局对齐基准见 [DESIGN.md](../DESIGN.md)。

## 1. 部署与生产运维（公开用户环境）

- [Linux 原生安装指南](../deploy/README.md)：从源码打包、目录布局、systemd 单元、PostgreSQL 初始化、管理员创建、反向代理配置及升级/回滚/卸载脚本用法。
- [生产运维与灾难恢复手册](deployment/README.md)：功能与计费启用核对、异机加密备份与恢复演练、灾难恢复对账解锁（`.recovery-required`）、主密钥在线轮换与故障排查。

## 2. 产品规范（`docs/product/`）

- [产品需求规范](product/requirements.md)：目标角色、功能范围、非功能指标与明确排除项。
- [三端信息架构](product/information-architecture.md)：Flutter 用户端、React 管理员控制台与独立题库维护面板（`/panel/`）的完整路由树、权限门控与视图职责。
- [核心业务流程](product/user-flows.md)：学习通登录与两阶段同步、学习安排与原生任务执行、全自动测验与精确缓存、充值与复式入账、异常处置及隐私闭户流程。
- [演进路线与验收门槛](product/roadmap.md)：阶段划分、完成基线与生产外部能力启用验收标准。
- [决策与启用核对清单](product/decision-checklist.md)：已锁定技术与产品方案汇总，以及自托管上线前的逐项配置清单。

## 3. 视觉与交互设计（`docs/design/`）

- [设计系统与共享令牌](design/design-system.md)：`design/tokens.json` 单一事实源、石墨/青禾/靛蓝三套配色、浅深主题、排版标尺、无障碍对比度与跨端生成机制。
- [用户客户端界面规范](design/client-ui.md)：Flutter Web 与移动端自适应导航、页脚滚动容器、首页、课程树、安排进度条、钱包及个人设置设计规范。
- [管理员端与题库面板界面规范](design/admin-ui.md)：管理端七大业务视图、四组系统设置、答案报告复核区及独立题库维护面板布局规范。
- [交互状态规范与原型对照](design/interaction-spec.md)：加载骨架、就地错误、乐观锁冲突、侧栏/底栏交互准则，以及 `prototypes/` 历史视觉原型的验证方式。
- [跨端视觉收敛准则](design/ui-review-online.md)：信息减法、表格内滚动、状态色克制与三端几何密度统一标准。

## 4. 系统架构（`docs/architecture/`）

- [系统架构总览](architecture/overview.md)：Go 模块化单体 + 独立题库服务拓扑、模块职责矩阵与核心数据流。
- [Go 后端服务架构](architecture/backend.md)：`server/` 与 `services/tiku/` 包结构、双监听隔离、配置校验、运行期热调参数（`runtimeconfig`）与优雅停机。
- [前端与客户端架构](architecture/frontend.md)：Flutter 单工程三端适配、Riverpod 状态管理、SSE 实时合并刷新与轮询降级、React 管理端与嵌入题库面板。
- [身份认证、会话与密钥](architecture/authentication.md)：学习通 JSON/Profile 双探针、Web 双端 Cookie/CSRF、原生旋转刷新令牌、管理员 Argon2id + TOTP + RBAC、双密钥分离与轮换。
- [数据库与迁移架构](architecture/database.md)：主库 34 个连续迁移与题库库 8 个连续迁移的领域表结构、锁顺序、事务隔离与 schema 完整性校验。
- [REST 与 SSE 接口设计](architecture/api.md)：[OpenAPI 契约](../api/openapi.yaml)、四类接口面（用户/管理/题库内部/题库面板）、幂等键、游标分页与事务 Outbox 有序投递流。
- [任务调度与执行状态机](architecture/scheduler.md)：单任务稳定键独占、全局/账号并发配额、租约续期与 fencing、执行前新鲜核验、冷却恢复及停止握手。
- [学习通与题库集成适配](architecture/integrations.md)：原生 HTTP 适配器、单次操作 Cookie 作用域、音视频/文档/测验协议、独立题库 v2 契约与视觉转述链路。
- [课程同步与共享目录缓存](architecture/course-resource-cache.md)：两阶段快照提交、自适应请求提速、结构指纹减半冷请求与 24 小时跨用户页码目录缓存。
- [课程封面代理与共享缓存](architecture/course-covers.md)：归属鉴权、无会话安全回源、静态 JPEG 归一化压缩与双层缓存淘汰策略。
- [题库超时、预算与幂等恢复](architecture/question-timeouts.md)：双服务超时包络、模型输出 token 预算冻结、前置失败白名单重驱动与 `request_unknown` 保护。
- [钱包、双轨计量与支付对账](architecture/billing.md)：整数分复式账本、微元单价与舍入、有效任务点单次结算、token 冻结快照计费、ZPay 验签查单及双类退款分离。
- [数据保留、隐私闭户与备份](architecture/data-retention.md)：分层 TTL 清理、加密数据导出、删除申请与财务保留匿名化、逻辑备份及持续 WAL 规划。
- [资源预算与容量规划](architecture/capacity.md)：2C4G / 30GB 目标预算拆解、连接池与协程池边界、磁盘三级水位保护。
- [生产部署拓扑与安全沙盒](architecture/deployment.md)：系统用户隔离、systemd 权限收敛、反向代理边界与版本切换契约。
- [客户端下载与分发架构](architecture/distribution.md)：Flutter Web 同源托管、自有下载站版本清单、Android 签名连续性及 iOS 合规分发边界。

## 5. 架构决策记录（`docs/decisions/`）

- [0001 原型与正式客户端边界](decisions/0001-ui-prototype.md)
- [0002 默认石墨配色与多主题保留](decisions/0002-default-graphite.md)
- [0003 架构基线与外部能力启用门槛](decisions/0003-architecture-status.md)
- [0004 采用 PostgreSQL 作为唯一正式数据库](decisions/0004-postgresql.md)
- [0005 用户端技术栈、双轨计量与支付渠道基线](decisions/0005-product-baseline.md)
- [0006 独立题库 Fork、精确缓存与服务端 Token 计费](decisions/0006-tiku-fork-token-billing.md)
- [0007 Go 主服务基础底座与回环双监听](decisions/0007-server-foundation.md)
- [0008 完整业务实现范围与受控验证边界](decisions/0008-business-implementation.md)
- [0009 正式学习任务采用原生 HTTP 直接执行](decisions/0009-native-http-execution.md)
- [0010 章节测验全自动作答、提交与独立复核](decisions/0010-automatic-questions.md)
- [0011 题库维护面板统一管理上游模型与公开目录](decisions/0011-tiku-panel-catalogue.md)
- [0012 学习通账户资料页（Profile）主体探针](decisions/0012-chaoxing-profile-probe.md)

## 6. 本地开发与工程专题（`docs/development/`）

- [本地全栈热重载开发指南](development/local-dev.md)：Windows / Ubuntu 一键脚本 `scripts/dev.mjs`、隔离数据库模式、热重载机制与调试快捷键。
- [跨模块工程实现契约](development/implementation-contract.md)：全栈在时间、金额、版本锁、鉴权、脱敏与真实性上的硬性编码准则。
- [后端 CLI 与测试基础手册](development/backend-foundation.md)：`byebyeocs` 与 `ocs-tiku-fork` 命令行参数、环境变量优先级及隔离 PostgreSQL 集成测试规范。
- [功能实现全景与验证矩阵](development/local-implementation.md)：各子系统已落地能力清单、自动化验证命令矩阵及待现场验收边界。
- [任务覆盖范围与完成判定规范](development/task-coverage.md)：视频、音频、文档、五类测验题型的执行与核验规则，以及关课/关章/关卷三级可用性模型。
- [课程目录树解析与读取一致性](development/course-reading-audit.md)：`chaoxing-read-v2` 多级章节建模、稳定小节 UUID、同媒体多任务点拆分与只读快照事务。
- [测验题面字形几何还原与索引维护](development/secretfont-index.md)：动态字体混淆解析、Source Han Sans 1.000 几何轮廓匹配、Unicode 17.0 部首归并与参考包构建。
- [独立题库适配与跨服务联调](development/tiku-adaptation-audit.md)：题库 v2 协议、公开条目唯一绑定、选项全文保留与端到端浏览器回归验证。
- [题库面板治理与价格参考缓存](development/tiku-performance-panel-audit.md)：提供商在线探测、配置原子回写热加载、参考价/汇率本地缓存及统计查询优化。
- [性能治理与并发优化](development/performance-audit.md)：课程同步自适应提速、结构指纹缓存、关键索引与前端高频事件合并刷新。
- [异常恢复与运行期边界防护](development/runtime-gaps.md)：租约崩溃接管、最终提交防重放、题库前置失败恢复、单次操作 Cookie 隔离与备份/磁盘熔断。
