# 功能实现全景与验证矩阵

> **适用范围**：开发维护环境。本文汇总仓库内四大工程（`server/`、`services/tiku/`、`apps/admin/`、`apps/client_flutter/`）与配套脚本当前的完整落地能力、自动化验证入口及受控夹具与生产环境的客观边界。

## 1. 全栈已落地能力总览

| 子系统 | 核心已实现功能 | 数据库 / 产物基线 |
| --- | --- | --- |
| 身份、权限与密钥（`server/internal/auth`） | 学习通 `json` / `profile` 双主体探针、HMAC 身份索引、Web 双端 Cookie/CSRF、原生单次轮换刷新令牌与重放熔断、管理员 Argon2id+TOTP+五角色 RBAC、最后 `owner` 保护、5 分钟步进验证、8 版本主密钥在线轮换 | 主库迁移 01, 03, 08 |
| 课程同步与封面（`server/internal/learning`） | 两阶段概要/快照同步、`chaoxing-read-v2` 多级章节树与稳定小节 UUID、24h 跨用户页码目录共享缓存、课程结构指纹减半冷请求、自适应请求提速、无会话安全封面重编码缓存 | 主库迁移 04, 18, 25–28 |
| 三级关闭识别与调度执行（`learning` + `chaoxing`） | 关课（`course_closed`）/ 关章（`chapter_availability`）/ 关卷（`quiz_closed`）三级持久状态与重新检查可用性；单任务稳定键租约与 fencing；执行前新鲜只读复核；原生 HTTP 视频/音频/文档执行与两轮瞬态冷却恢复 | 主库迁移 09, 13, 30–34 |
| 全自动测验与字形解码（`chaoxing` + `questions`） | Source Han Sans 1.000 几何轮廓参考库（SFR2）+ Unicode 17.0 部首归并；单选/多选/判断/填空/简答全自动查题、重排核对与先落库防重放最终提交；三代显式答案重查；在途查询每 30s 自动回收与 `request_unknown` 受控释放 | 主库迁移 06, 10, 12, 16, 17, 20, 22–24, 34 |
| 独立题库与维护面板（`services/tiku`） | 兼容 v1 `/api/search` 与正式 v2 `/api/v2/answers`；提供商在线探测、模型热加载、一对一公开条目与只追加价格版本、输出 token 预算收敛、精确缓存与视觉转述槽位统计、外部参考价与汇率缓存、内嵌 `/panel/` 面板 | 题库库迁移 01–08 |
| 复式账本与支付对账（`ledger` + `payments`） | 人民币整数分不可变 `journals`/`postings`、微元单价与三种整数舍入、有效任务点单次结算、token 冻结快照结算、ZPay 管理口验签 Inbox、主动查单对账、服务退回钱包与渠道原路退款分离 | 主库迁移 05, 11, 14, 21 |
| 实时流、隐私与部署备份（`events`、`operations`、`deploy`） | 事务 Outbox 串行 SSE 流（按管理员角色过滤）、受控加密导出、防复活隐私闭户（`reapply_privacy_closures`）、磁盘三级水位熔断、15m 异机 `age` 加密备份门控、`.recovery-required` 恢复锁定、Linux 安装/升级/回滚/卸载与 Windows/Ubuntu 热重载开发脚本 | 主库迁移 02, 07, 15, 19；`make bundle` |
| 正式前端三端（`apps/admin`、`apps/client_flutter`） | 共享 `design/tokens.json`（石墨/青禾/靛蓝 × 浅深主题）；React 管理端 7 大业务视图 + 运行参数热调；Flutter Web/Android/iOS 共享壳层、`PageScrollView` 贴底随滚页脚、双层任务进度条、快照游标分页与会话代次缓存隔离 | Web release、Admin SPA、Android Debug APK |

## 2. 标准自动化验证命令矩阵

在源码根目录（已执行 `git submodule update --init --recursive`）可按层级运行以下验证命令，与 `.github/workflows/check.yml` 保持完全一致：

| 验证层级 | 执行命令 | 覆盖内容 |
| --- | --- | --- |
| 设计令牌一致性与对比度 | `python3 scripts/generate-design-tokens.py --check` | 校验三端生成的 CSS/Dart 无漂移，且 6 套色板全部通过 4.5:1 对比度 |
| Go 格式与静态分析 | `make check-go` | 检查 `server/` 与 `services/tiku/` 的 `gofmt` 与 `go vet ./...` |
| Go 并发竞态单元测试 | `make test-go` | 在开启 `-race` 下运行主服务与题库全部单元测试及 OpenAPI 契约测试 |
| 隔离 PostgreSQL 集成测试 | `BYEBYEOCS_TEST_DATABASE_DSN_FILE=... make test-integration` | 验证双库全部迁移、行锁、账本平衡、崩溃恢复、题库联调与闭户重放 |
| 管理员控制台检查与构建 | `make check-admin && make admin` | 执行 TypeScript 严格编译检查、Vitest 单元测试与生产静态构建（另有 `npm --prefix apps/admin run test:e2e` 浏览器测试） |
| Flutter 客户端检查与构建 | `make check-flutter && make flutter-web` | 执行 `flutter analyze`、全量组件/控制器测试及离线字体 Web release 构建 |
| 公开配置样例实测校验 | `make test-configs` | 使用真实编译出的两个二进制对 `configs/` 下公开配置模板执行 `config check` |
| 部署、加密备份与恢复演练测试 | `make test-deploy` | 在临时沙盒与伪 SSH 下测试安装、升级、回滚、卸载、`pg_dump`+`age` 备份与 `restore-drill.sh` |
| 跨平台热重载脚本测试 | `make test-dev`（及 `node scripts/dev/ci-smoke.mjs`） | 验证 CLI 解析、端口避让、全栈一键拉起、自动登录与 Go 热编译平滑重启 |
| 完整 Linux 发布归档打包 | `make bundle VERSION=0.1.0 GOARCH=amd64` | 构建全部四个产物，收集第三方许可证，生成 `release.json` 并自校验 `checksums.txt` |

## 3. 本地验证与生产现场验收的客观边界

本仓库遵循严格的工程诚实准则，明确区分**代码级已验证事实**与**待部署方现场验收事项**：

1. **学校与课程差异**：受控协议夹具与特定授权账号的测试通过，仅代表已覆盖的普通视频、明确音频、标准文档及五类常规测验题型协议；不同高校定制网关、特殊互动课件、直播、口语及强制人脸核验仍需按实际授权课程独立验证。
2. **模型质量与计费完整性**：本地联调验证了精确缓存、多槽位 token 记账与异常免扣逻辑；真实上游大模型的答题准确率、并发限流配额及元数据完整性取决于部署者在 `/panel/` 接入的具体服务商。
3. **真实支付与容灾 SLA**：ZPay 签名、幂等 Inbox、对账与双类退款已在隔离数据库事务中完整验证；线上真实收款与退款通道可用性须使用真实商户号在公网 HTTPS 回调下联调，且 `RPO <= 15m / RTO <= 4h` 目标取决于部署者配置的异机存储带宽与定期恢复演练。
4. **移动客户端原生分发**：仓库已验证 Flutter Web 生产构建与 Android Debug APK 构建；面向公众分发 Android Release APK 需部署者自备正式签名密钥库，iOS 原生构建与分发需具备 macOS/Xcode 环境及合规 Apple 开发者分发资格。
