# Go 后端服务架构

> **适用范围**：开发维护环境，说明主服务（`server/`）与独立题库服务（`services/tiku/`）的工程分层、双端监听生命周期、配置加载与运行期热调机制。

## 1. 独立 Go 模块组织

仓库包含两个相互独立的 Go `1.26` 模块，编译与测试均通过 `go -C server` 与 `go -C services/tiku` 隔离执行，静态编译关闭 CGO（`CGO_ENABLED=0`）：

### 1.1 主服务（`server/`）分层

- `cmd/byebyeocs`：薄入口，注入构建版本号、Git 提交哈希与构建时间后委托 `internal/command`。
- `internal/command`：解析 `config init`、`config check`、`migrate up`、`migrate status`、`doctor`、`admin create`、`keys generate`、`keys reencrypt`、`serve` 与 `version` 子命令，统一将内部错误映射为脱敏 JSON `{code, message}`。
- `internal/platform`：基础设施底座，包含严格 YAML 配置解析（`config`）、运行期原子可变参数（`runtimeconfig`）、PostgreSQL 连接池与迁移校验（`postgres`）、结构化安全错误（`fault`）及统一审计辅助（`audit`）。
- `internal/transport/httpapi` 与 `internal/web`：提供请求 ID 注入、安全响应头、Panic 恢复、保留前缀防穿透（未命中的 `/api/`、`/admin/api/`、`/health/`、`/payments/` 严格返回 JSON `404`，绝不回退至前端 SPA `index.html`）及静态资源缓存控制。
- `internal/auth`、`learning`、`ledger`、`payments`、`questions`、`events`、`operations`：核心业务领域包。
- `internal/adapters/chaoxing`：学习通原生 HTTP 协议实现与 Source Han Sans 几何字形解码器（`internal/adapters/browser` 仅保留作前端开发测试辅助，不接入生产执行链路）。
- `internal/app`：依赖装配（`dependencies.go`）、后台工作协程编排（`runtime.go`）与双端口 HTTP 服务器生命周期控制（`server.go`）。

### 1.2 独立题库服务（`services/tiku/`）分层

- 根目录保留上游 OCS v1 `/api/search` 兼容层与共享解题引擎（`internal/engine`）。
- `cmd/ocs-tiku-fork` 与 `internal/platform/*` 构成正式 v2 服务：涵盖 JSON 配置与密钥加载（`config`）、PostgreSQL 存储与定期清理（`store`）、多协议大模型客户端（`modelclient`，支持 OpenAI Chat、Anthropic Messages、Google Gemini、Ollama）、公开目录与价格版本（`catalog`）、精确缓存与多槽位作答（`answer`）、v2/内部 HTTP 接口（`httpapi`）及内嵌 `/panel/` 维护面板（`panel`）。

## 2. 双监听隔离与请求超时策略

主服务在单进程内启动两个独立的 `*http.Server` 实例，配置校验强制二者均为回环 IP 且端口互不相同：

- **用户监听（默认 `127.0.0.1:8090`）**：暴露 `/api/v1/capabilities`、`/api/v1/*` 用户端业务接口与 SSE 事件流，其余非保留路径托管 Flutter Web 静态资源。
- **管理监听（默认 `127.0.0.1:8091`）**：暴露 `/health/live`、`/health/ready`、`/payments/callbacks/zpay`（ZPay 异步通知唯一内部入口）、`/admin/api/v1/capabilities`、`/admin/api/v1/*` 管理接口与管理端 SSE 流，其余非保留路径托管 React 管理端静态资源。
- **分路径超时控制**：常规 API 遵循短超时（默认读头 `5s`、读 `10s`、写 `15s`）快速释放慢连接；针对长耗时请求通过 `http.ResponseController` 按连接单独放宽写期限——同步题库查询 `POST /api/v1/question-queries` 延长至 `questions.timeout + 45s`，静态大文件与已鉴权隐私导出下载延长至 `5m`，SSE 事件流独立管理心跳与生命周期。

## 3. 配置校验与运行期热更新

### 3.1 四级配置加载与私密文件防线

主服务配置按 **CLI 显式参数 > `BYEBYEOCS_*` 环境变量 > YAML 配置文件 > 内置保守默认值** 合并，YAML 解码开启 `KnownFields(true)` 拒绝拼写错误或未知字段：

- 数据库 DSN 严禁直接写在 YAML 正文中，仅从 `dsn_file` 或 `BYEBYEOCS_DATABASE_DSN` 读取；同时强制校验 `sslmode`（远程库必须为 `verify-full`，仅回环 IP 允许显式 `disable`），并拦截查询串夹带 `host`/`user` 等覆盖参数。
- 所有密钥与凭据文件读取前先 `stat` 排除命名管道与设备文件，非 Windows 平台强制要求文件权限不含组或其他用户读写执行位（`perm & 0077 == 0`）。

### 3.2 运行期原子热调（`runtimeconfig`）

`internal/platform/runtimeconfig.Learning` 通过 `sync/atomic.Int64` 保存学习执行与课程同步的五个高频调优参数：

- 全局任务并发上限 `GlobalConcurrency`（`1–256`，默认 `4`）
- 单账号任务并发上限 `AccountConcurrency`（`1–GlobalConcurrency`，默认 `4`）
- 单门课程并发读取章节上限 `ChapterConcurrency`（`1–64`，默认 `4`）
- 上游最小请求间隔 `RequestInterval`（`50ms–2s`，默认 `300ms`）
- 自适应提速下限 `RequestIntervalFloor`（`10ms–RequestInterval`，`0` 表示折半推导）

`owner` 管理员在后台“系统设置 → 运行安全”保存新参数后，内存原子值立即对调度器与同步器生效，同时将变更原子写回 `Config.SourcePath` 配置文件。

## 4. 后台协程与优雅停机顺序

`app.Runtime` 统一管理调度器、课程同步消费者（2 个并发课程工作协程）、Outbox 事件桥接器与定期数据保留清理任务。收到 `SIGINT` 或 `SIGTERM` 时：

1. 立即将 `readinessGate` 置为未就绪，使管理端 `/health/ready` 返回 `503 service_unavailable`，通知前置代理摘除流量；
2. 取消后台调度与同步上下文，在途执行尝试在安全检查点保存观测状态或释放未提交租约；
3. 在 `server.shutdown_timeout`（默认 `10s`）内并行执行双 HTTP 服务器的 `Shutdown`，超时后主动取消残余请求上下文并关闭套接字。
