# Go 基础服务开发与验证

本文面向开发维护环境，是阶段1A交付时的历史快照。下文能力限制描述当时状态；现有业务、双端应用、题库与最新验证请看 [当前实现](local-implementation.md)，公开安装看 [部署指南](../../deploy/README.md)。React设计原型继续独立保留。

## 已实现范围

- 独立的 `server/` Go 模块，避免构建和测试纳入保留的研究源码。
- 原生二进制及双端监听，用户端默认 `127.0.0.1:8090`、管理员默认 `127.0.0.1:8091`；目前仅接受回环 IP。
- 严格 YAML 配置、环境变量和命令行覆盖、受保护的连接文件、JSON 安全日志、服务端请求编号及限时优雅停止。
- PostgreSQL 连接、版本检查、显式迁移、事务级咨询锁、内置迁移 SHA-256 校验、未来版本拒绝运行。
- 内部用户、管理员、双端偏好和审计表结构；不创建任何账号，也未开放读写 API。
- 已实现接口的 OpenAPI 契约和自动响应校验。其余业务契约仍在阶段 0 待补。

这不构成阶段 1 的完整验收。管理员认证、用户授权、数据隔离权限、偏好接口、限流、幂等、完整审计写入和备份恢复仍需后续实现。

## 维护环境要求

- 构建：Go 1.26 或更高版本，首次解析依赖需要能访问 Go 模块服务。
- 运行：Linux、PostgreSQL 16 或更高版本，以及可连接到专用数据库的数据库角色。当前现场验证仅覆盖 PostgreSQL 16.15。
- 集成测试：隔离的 PostgreSQL 实例，测试角色还需要 `CREATEDB` 权限。测试随机创建并清理 `byebyeocs_it_*` 数据库，不在连接 URL 指定的基础库中执行迁移。
- 以下命令从仓库根目录执行；已取得源码是开发维护前提。Docker 和浏览器均不是 Go 服务运行依赖。

## 构建与配置

```bash
CGO_ENABLED=0 go -C server build -trimpath -o ../bin/byebyeocs ./cmd/byebyeocs
./bin/byebyeocs version
```

示例配置位于 `server/configs/byebyeocs.example.yaml`。创建本机配置和私有连接文件：

```bash
mkdir -p .local
chmod 700 .local
cp server/configs/byebyeocs.example.yaml .local/service.yaml
umask 077
printf '%s\n' 'postgres://service:REPLACE_WITH_URL_ENCODED_PASSWORD@127.0.0.1:5432/byebyeocs?sslmode=disable' > .local/database.dsn
chmod 600 .local/database.dsn
export BYEBYEOCS_DATABASE_DSN_FILE="$PWD/.local/database.dsn"
```

执行前，将示例 URL 改为本机专用 PostgreSQL 库和角色的实际连接信息；密码中的特殊字符使用 URL 编码。迁移角色须具备该库 `public` schema 的建表权限。当前命令不会创建数据库或数据库角色。连接串是私密配置，不应提交到仓库。

回环 IP 的本地数据库允许显式 `sslmode=disable`；远程数据库必须显式 `sslmode=verify-full`，并按 PostgreSQL 驱动要求配置可信根证书。远程 TLS 分支目前有配置校验测试，尚未进行外部数据库现场验证。

支持的配置覆盖如下：

| 配置 | 环境变量 | 命令行 |
| --- | --- | --- |
| 配置文件位置 | `BYEBYEOCS_CONFIG` | `--config` |
| 用户监听地址 | `BYEBYEOCS_USER_LISTEN` | `--user-listen` |
| 管理员监听地址 | `BYEBYEOCS_ADMIN_LISTEN` | `--admin-listen` |
| 日志级别 | `BYEBYEOCS_LOG_LEVEL` | `--log-level` |
| 数据库连接文件 | `BYEBYEOCS_DATABASE_DSN_FILE` | `--database-dsn-file` |
| 数据库连接 URL | `BYEBYEOCS_DATABASE_DSN` | 无，避免命令行历史与进程参数记录连接串 |

其余超时和连接池参数使用 YAML。优先级为命令行、环境变量、YAML、内置默认值。配置文件中的相对 `dsn_file` 以 YAML 文件目录为基准，环境变量与命令行路径以当前目录为基准。两个数据库连接环境变量同时设置会报错；显式 `--database-dsn-file` 可以覆盖它们。YAML 不接受直接写入连接 URL。

## 迁移与启动

```bash
./bin/byebyeocs config check --config .local/service.yaml
./bin/byebyeocs migrate status --config .local/service.yaml
./bin/byebyeocs migrate up --config .local/service.yaml
./bin/byebyeocs doctor --config .local/service.yaml
./bin/byebyeocs serve --config .local/service.yaml
```

`config check` 只核对配置，不连接数据库。`migrate status` 为只读检查，空库返回 `ready=false`。`migrate up` 显式执行迁移，并以事务锁串行处理并发调用；整批 SQL 和迁移记录共同提交，失败共同回滚。提交结果不明时应先重新查状态。

`serve` 和 `doctor` 都要求迁移全部完成。服务启动不会创建表或自动升级；记录缺失、编号不连续、名称或校验和不匹配、数据库版本高于程序时拒绝运行。校验针对迁移记录，不等同于检测所有手动 DDL 改动。历史迁移一旦应用，后续修改应新增递增文件，不改原 SQL。

当前迁移仅建立身份、偏好和审计基础表。审计触发器阻止普通 SQL 修改、删除和清空已有事件；数据库所有者仍可更改 DDL。这不代表应用权限、完整审计机制或保留期清理已经完成。

## 接口检查

在第二个终端执行：

```bash
curl --fail http://127.0.0.1:8090/api/v1/capabilities
curl --fail http://127.0.0.1:8091/admin/api/v1/capabilities
curl --fail http://127.0.0.1:8091/health/live
curl --fail http://127.0.0.1:8091/health/ready
```

能力接口的全部业务标志为 `false`，并公布石墨默认值与三套配色。用户监听地址不提供管理员 API 或健康检查，管理员监听地址不提供用户 API。未知路由返回统一 JSON 404；已知接口仅接受 GET、HEAD。接口不开放跨域。

`live` 不查询数据库；`ready` 每次读取数据库迁移状态，数据库不可用、迁移不匹配或正在停止时返回 503。响应头和响应体中的请求编号一致，编号由服务端生成；HEAD 不返回响应体。日志不记录 URL、请求头、正文、连接串或原始异常。

使用 Ctrl+C 或 SIGTERM 停止服务。两个监听同时关闭；正在处理的请求在配置的停止期限内完成，超时后取消并关闭连接。端口冲突会退出并释放已取得的监听，不会停止其他进程。

## 自动检查

```bash
go -C server vet ./...
go -C server test -race ./...
BYEBYEOCS_TEST_DATABASE_DSN_FILE="$PWD/.local/test-database.dsn" go -C server test -race -tags=integration ./internal/platform/postgres
npm --prefix prototypes run build
```

集成测试连接文件同样要求 0600 或更严格，必须指向隔离测试实例。显式启用 `integration` 标签却未配置连接文件时，测试失败，避免把未执行数据库验证写成通过。测试不会自动访问真实学习通、支付或题库服务。

根目录 Makefile 提供 `build`、`check`、`test`、`test-integration`。GitHub Actions 单独运行 Go/PostgreSQL 测试与 React 构建；CI 的 PostgreSQL 容器仅用于测试，不构成公开部署前提。

## 后续衔接

阶段 1B 先补管理员 Argon2id/TOTP 认证、独立会话、CSRF/限流、权限和审计服务，再实现经过身份验证的双端外观偏好。用户外部身份仍需先验证主体稳定性与授权方式。钱包、支付、学习执行和题库按后续阶段分别启用，继续保留价格与外部授权门槛。
