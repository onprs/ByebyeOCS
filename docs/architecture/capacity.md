# 资源预算与容量规划架构

> **适用范围**：面向公开自托管生产环境的单机资源规划参考（默认基准目标约 **2 vCPU / 4 GiB 内存 / 30 GiB SSD**，并提供 1 GiB–2 GiB 低内存起步调优参数）。所有预算值均为工程边界约束，具体承载并发以目标硬件与上游网络实测为准。

## 1. 内存与并发池有界控制

系统在架构设计上杜绝与排队任务总数或注册用户总数成正比的内存增长——未领取的排队任务静态保存在 PostgreSQL 中，在线空闲客户端不独占数据库连接：

### 1.1 标准 2C4G 生产部署默认预算

| 组件 | 内存规划区间 / 限制 | 连接池与并发默认值 |
| --- | --- | --- |
| Go 主服务（`byebyeocs`） | `GOMEMLIMIT=512MiB`，systemd `MemoryMax=768M` | `database.max_connections: 12`；学习并发全局 `4` / 单账号 `4`；单课章节并发 `4`；题库客户端并发 `2`；SSE 流上限 `64` |
| 独立题库服务（`ocs-tiku-fork`） | systemd `MemoryMax=384M` | `database_max_connections: 4`；模型请求并发 `2`；单请求上限 `32 MiB`、最多 `8` 张图（单图 `5 MiB` / `16MP`） |
| PostgreSQL 16+（承载主库与题库库） | 约 `768–1024 MiB` | 同实例双独立数据库与角色，避免运行两套数据库守护进程 |
| 操作系统、systemd 与 HTTPS 反向代理 | 约 `512–768 MiB` | 负责 TLS 握手、静态缓存与访问日志 |
| 定时 `pg_dump` / `age` 加密流式缓冲与安全余量 | 预留约 `1 GiB` | 流式管道打包加密，不在内存驻留整份转储文件 |

注：主服务内置嵌入的 Source Han Sans 几何字形参考库（压缩包约 `14.1 MiB`）采用单次延迟初始化与 `4,096` 条有界轮廓结果缓存；课程页码目录内存 LRU 上限 `4,096` 条；客户端封面回源单图缩放上限 `768px` / `256 KiB`。

### 1.2 低内存主机（1 GiB–2 GiB）起步调优

针对轻量级低并发自托管实例，仓库随附 `deploy/systemd/byebyeocs-low-memory.conf` 与 `ocs-tiku-low-memory.conf` 可选覆盖配置，推荐搭配以下保守参数运行：

- **主服务**：`GOMEMLIMIT=192MiB`，`MemoryHigh=256M` / `MemoryMax=384M`；`database.max_connections: 4`，`learning.global_concurrency: 1`、`account_concurrency: 1`、`chapter_concurrency: 2`，`questions.concurrency: 1`。
- **题库服务**：`GOMEMLIMIT=96MiB`，`MemoryHigh=128M` / `MemoryMax=192M`；`database_max_connections: 2`，`limits.concurrency: 1`，单次请求收紧至 `8 MiB` / 最多 `2` 张 `2 MiB` 图片。
- **PostgreSQL**：`shared_buffers=64MB`、`work_mem=1MB`、`maintenance_work_mem=16MB`、`max_connections=20`（保持完整事务持久化与 Argon2id 安全参数不变）。

## 2. 30 GiB 磁盘空间分配基准

| 存储分区内容 | 规划容量（GiB） | 控制机制 |
| --- | ---: | --- |
| Linux 基础系统与 PostgreSQL 运行时 | `6` | 无桌面环境、无浏览器、无容器镜像层开销 |
| 主数据库与独立题库数据库（表与索引） | `8` | 过程日志、Outbox、缓存与查询正文按 TTL 自动分批清理 |
| PostgreSQL 本地 WAL 日志 | `2` | 正常检查点回收，严禁手动删除 WAL |
| 结构化应用日志与 `journald` | `1` | 配置 `journald` `SystemMaxUse` 滚动上限 |
| 加密数据导出、课程封面表与备份临时暂存区 | `3` | 导出单包 `<= 8 MiB` 限时清理；封面总预算 `<= 256 MiB`；异机备份校验成功后立即移除本地临时包 |
| 程序版本目录（`current` + 上一可回滚版本） | `2` | 清理更早历史发布目录，客户端 APK 放独立下载站 |
| 峰值缓冲与安全余量 | `8` | 配合 `70%` 告警、`80%` 阻断新写入、`90%` 维护保护 |

## 3. 扩容演进边界

在更高规格主机上，`owner` 可在 `config.yaml` 与管理端“运行安全”中按配置上限平滑上调连接池（主库最高 `100`、题库最高 `32`）、学习执行并发（最高 `256`）、章节同步并发（最高 `64`）与题库并发（两侧最高均为 `16`）；也可通过内网 HTTPS 将独立题库服务或 PostgreSQL 拆分至独立主机。当前架构面向单主服务进程设计，因外部学习通接口不识别内部 `fencing_token`，在未引入跨节点外部副作用协调层前不应水平启动多个主服务 `serve` 实例同时消费同一任务队列。
