# 后端 CLI 与隔离数据库测试手册

> **适用范围**：开发维护环境，详述主服务二进制 `byebyeocs` 与独立题库二进制 `ocs-tiku-fork` 的命令行工具集、配置覆盖优先级及 PostgreSQL 隔离集成测试机制。

## 1. 主服务 CLI 参考（`byebyeocs`）

构建命令：`make server`（生成 Linux 目标二进制至 `build/bin/byebyeocs`）或直接运行 `go -C server build -o ../bin/byebyeocs ./cmd/byebyeocs`。

### 1.1 子命令速查

| 命令 | 数据库依赖 | 用途说明 |
| --- | --- | --- |
| `byebyeocs config init [参数]` | 无 | 初始化新 YAML 配置、`application-v1.key` 与独立 `identity.key`（目标文件已存在时拒绝覆盖）；本地开发可加 `--development` |
| `byebyeocs config check --config PATH` | 无 | 校验 YAML 语法、未知字段、监听地址、Origin、DSN 格式与密钥权限，输出 JSON 能力摘要 |
| `byebyeocs migrate status --config PATH` | 只读连接 | 检查当前数据库已应用的迁移版本、连续性与 SHA-256 校验和 |
| `byebyeocs migrate up --config PATH` | 读写事务锁 | 获取咨询锁并显式执行所有待应用的内置 SQL 迁移（共 34 个版本） |
| `byebyeocs doctor --config PATH` | 只读连接 | 校验数据库连通性且全部迁移均已就绪（返回 `{"status":"ok","database":"ok","schema":"current"}`） |
| `byebyeocs admin create --config PATH ...` | 读写连接 | 通过 `--username`、`--password-file` 及 `--totp-output-file`（或 `--totp-secret-file`）创建管理员 |
| `byebyeocs keys generate --output PATH` | 无 | 生成新的 Base64 编码 32 字节随机密钥文件（权限 `0600`，拒绝覆盖已有文件） |
| `byebyeocs keys reencrypt --config PATH` | 读写连接 | 将存量旧版本密文按小批事务平滑重封装为当前 `auth.encryption_key_version` |
| `byebyeocs serve --config PATH` | 读写连接 | 校验迁移与运行门控后启动用户监听、管理监听及后台调度/同步/事件协程 |
| `byebyeocs version` | 无 | 输出 JSON 格式的 `version`、`commit` 与 `build_time` |

### 1.2 环境变量与命令行覆盖优先级

配置合并遵循 **命令行标志 > `BYEBYEOCS_*` 环境变量 > YAML 配置文件 > 内置默认值**：

- 基础连接与监听：`BYEBYEOCS_CONFIG`（`--config`）、`BYEBYEOCS_USER_LISTEN`（`--user-listen`）、`BYEBYEOCS_ADMIN_LISTEN`（`--admin-listen`）、`BYEBYEOCS_LOG_LEVEL`（`--log-level`）、`BYEBYEOCS_DATABASE_DSN_FILE`（`--database-dsn-file`）、`BYEBYEOCS_DATABASE_DSN`（仅限环境变量，不提供 CLI 参数以防进程列表泄露密码；不可与 `BYEBYEOCS_DATABASE_DSN_FILE` 同时设置）。
- 运行与安全路径：`BYEBYEOCS_MODE`、`BYEBYEOCS_AUTH_ENABLED`、`BYEBYEOCS_COOKIE_SECURE`、`BYEBYEOCS_AUTH_KEY_FILE`、`BYEBYEOCS_AUTH_KEY_VERSION`、`BYEBYEOCS_IDENTITY_KEY_FILE`、`BYEBYEOCS_USER_ORIGIN`、`BYEBYEOCS_ADMIN_ORIGIN`、`BYEBYEOCS_DATA_DIR`、`BYEBYEOCS_ADMIN_ASSETS_DIR`、`BYEBYEOCS_CLIENT_ASSETS_DIR`、`BYEBYEOCS_BACKUP_STATUS_FILE`。
- 外部能力与凭据：`BYEBYEOCS_LEARNING_ENABLED`、`BYEBYEOCS_EXECUTION_ENABLED`、`BYEBYEOCS_PAYMENTS_ENABLED`、`BYEBYEOCS_CHARGING_ENABLED`、`BYEBYEOCS_ZPAY_MERCHANT_ID`、`BYEBYEOCS_ZPAY_KEY_FILE`、`BYEBYEOCS_ZPAY_NOTIFY_URL`、`BYEBYEOCS_ZPAY_RETURN_URL`、`BYEBYEOCS_TIKU_ENABLED`、`BYEBYEOCS_TIKU_URL`、`BYEBYEOCS_TIKU_TOKEN_FILE`。

## 2. 独立题库 CLI 参考（`ocs-tiku-fork`）

构建命令：`make tiku`（需已拉取 `services/tiku` 子模块）。

- `ocs-tiku-fork config check -config PATH`：校验 JSON 配置、数据库 DSN 文件、32+ 字符服务令牌、上游模型密钥文件与面板账号哈希文件。
- `ocs-tiku-fork migrate -config PATH`：显式执行题库库内置的 8 个连续 SQL 迁移。
- `ocs-tiku-fork panel-password -password-file IN -out OUT`：从私密密码文件生成 `/panel/` 维护账号密码哈希文件（权限 `0600`）。
- `ocs-tiku-fork serve -config PATH`：校验迁移就绪后启动题库 API、`/panel/` 维护面板（若开启）、价格参考预热及每 24 小时有界清理协程。

## 3. 单元、竞态与隔离 PostgreSQL 集成测试

### 3.1 无需外部数据库的快速检查与竞态单测

```bash
make check-go
go -C server test -race ./...
go -C services/tiku test -race ./...
```

### 3.2 隔离 PostgreSQL 16+ 完整集成测试

集成测试使用 `-tags=integration`（跨服务联调附加 `-tags=integration,tiku_fork`），通过环境变量 `BYEBYEOCS_TEST_DATABASE_DSN_FILE` 读取指向**本地隔离测试 PostgreSQL 实例**的私密连接文件（权限 `0600`，对应测试角色须具备 `CREATEDB` 权限）：

```bash
umask 077
printf '%s\n' 'postgres://byebyeocs_test:test_password@127.0.0.1:5432/byebyeocs_test?sslmode=disable' > .local/test.dsn

# 一键运行主服务与题库全部集成测试
BYEBYEOCS_TEST_DATABASE_DSN_FILE="$PWD/.local/test.dsn" make test-integration
```

**安全隔离机制**：`server/internal/testsupport` 不会在 DSN 指定的入口数据库中直接建表，每次测试都会动态创建带随机后缀的独立临时数据库（`byebyeocs_it_*`），运行全部迁移与并发断言后自动销毁临时库。切勿将测试 DSN 指向任何包含真实数据的生产或维护数据库。
