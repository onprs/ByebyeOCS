# 本地全栈热重载开发指南

> **适用范围**：开发维护环境，支持 **Windows** 与 **Ubuntu / Linux**（及 macOS）跨平台开发调试。所有生成的本地配置、随机密钥、隔离数据库与临时编译产物统一保存在被 `.gitignore` 忽略的 `.local/dev-stack/` 中，仅监听 `127.0.0.1` 回环地址，零维护者私有环境或线上凭据依赖。

## 1. 开发工具要求

- **必选工具**：
  - Git（克隆后须初始化子模块：`git submodule update --init --recursive`）
  - Node.js `22.12+` 与 npm（`scripts/dev.mjs` 纯标准库实现，零第三方依赖）
  - Go `1.26+`
- **PostgreSQL 数据库（三选一，脚本自动按序探测）**：
  1. **Docker 模式**：自动拉起本工作区专属隔离容器与具名卷（优先复用本机已有 PostgreSQL 16/17 镜像，无缓存时拉取 `postgres:17-alpine`）；
  2. **本机原生工具模式（`local-pg`）**：自动探测 PATH 或系统安装目录下的 `initdb` / `pg_ctl` / `psql`，在 `.local/dev-stack/pgdata` 初始化独立数据库集群（已处理 Windows 下 `pg_ctl` 管道句柄继承与 UTF-8 编码兼容）；
  3. **外部数据库模式（`external`）**：通过 `--main-dsn` 与 `--tiku-dsn` 指向自行准备的两个独立 PostgreSQL 库。
- **按需可选工具**：
  - Flutter `3.47.7`：启用用户端 Dart 代码保存热重载；若本机未安装 Flutter，脚本会自动检测并回退至已编译的 `apps/client_flutter/build/web` 静态产物，不阻断后端与管理端联调。
  - Python 3：监听 `design/tokens.json` 变更并自动同步生成三端样式与色板文件。

## 2. 快速自检与一键启动

首次运行前可先执行只读环境与端口自检：

```bash
# 跨平台统一入口
node scripts/dev.mjs doctor

# Ubuntu / macOS 包装脚本或 Make
./scripts/dev.sh doctor
make dev-doctor

# Windows PowerShell 包装脚本
.\scripts\dev.ps1 doctor
```

一键拉起全栈热重载开发环境：

```bash
# Ubuntu / macOS
./scripts/dev.sh            # 等同于 make dev 或 node scripts/dev.mjs up

# Windows PowerShell
.\scripts\dev.ps1
```

若需在不接入真实学习通账号的情况下联调用户端登录与业务流程，可显式附加 `--dev-login`（使用仅限开发模式的 `devfixture` 编译标签启用本地合成测试身份，该标签绝不进入生产构建）：

```bash
node scripts/dev.mjs --dev-login
```

## 3. 默认端口矩阵与实时热生效机制

| 模块与入口 | 默认监听地址 | 文件变更热生效行为 |
| --- | --- | --- |
| 用户端同源入口 | `http://127.0.0.1:5188` | `/api/*` 无缓冲透传至主服务 `8090`（完整支持 SSE），其余 HTTP/WS 转发至内部 Flutter 开发端口（`5189`）；保存 `apps/client_flutter/lib/**` 自动发送热重载（`r`），修改 `pubspec.yaml` 自动执行 `pub get` 并热重启（`R`） |
| 管理员控制台 | `http://127.0.0.1:5187` | Vite HMR 毫秒级热替换；自动开启回环 `local_admin_auto_login`，同域代理 `/admin/api`、`/health` 与 `/panel/` |
| 题库维护面板 | `http://127.0.0.1:5187/panel/` | 经内部热读代理（`18092`）直读 `services/tiku/internal/platform/panel/assets/*` 磁盘源文件，刷新浏览器 **0ms** 生效；自动开启回环 `panel.local_auto_login` |
| Go 主服务 | `127.0.0.1:8090` / `8091` | 监听 `server/` 下 `.go`、`.sql` 与 `go.mod`；`.sql` 新增或变更时自动先执行 `migrate up`，增量编译成功后平滑替换进程，编译报错时保留旧进程在线 |
| 独立题库服务 | `127.0.0.1:8092` | 监听 `services/tiku/` 源码与迁移文件，保存后自动迁移（如有）并增量重编重启 |
| 设计令牌同步 | `design/tokens.json` | 保存后自动运行 `scripts/generate-design-tokens.py`，联动触发管理端、题库面板与 Flutter 刷新 |
| 视觉原型（可选） | `http://127.0.0.1:5186/review` | 附加 `--with-prototypes` 时拉起 `prototypes/` Vite 服务 |
| 隔离开发数据库 | `127.0.0.1:25435` | 自动创建独立主库 `byebyeocs_dev` 与独立题库库 `byebyeocs_tiku_dev` 及各自专属角色 |

## 4. 常用参数组合与终端交互快捷键

```bash
# 按需裁剪模块（可选模块：db,server,tiku,admin,client,tokens,prototypes）
node scripts/dev.mjs --skip client
node scripts/dev.mjs --only db,server,admin

# 多工作树并行开发：一键平移全部默认端口（例如整体 +100）
node scripts/dev.mjs --port-offset 100

# 指定 Flutter 路径并在桌面/移动真机调试（自动向原生端注入 API_BASE_URL）
node scripts/dev.mjs --flutter /path/to/flutter --flutter-device linux

# 退出脚本时保持隔离 PostgreSQL 继续后台运行以加速二次启动
node scripts/dev.mjs --keep-db
node scripts/dev.mjs down              # 手动停止后台开发数据库

# 保留数据库业务数据，仅重新生成默认开发配置文件
node scripts/dev.mjs --reinit-config

# 彻底销毁并重建本工作区的开发数据库与配置
node scripts/dev.mjs reset --yes
```

服务运行期间，可直接在终端按下单键执行快捷维护：

- `s`：重编并重启主服务
- `t`：重编并重启独立题库服务
- `m`：立即对主库与题库执行数据库迁移
- `r` / `R`：触发 Flutter 热重载 / 热重启
- `d`：重新生成三端设计令牌产物
- `p` / `h`：打印当前会话访问地址汇总 / 查看快捷键说明
- `q`（或 `Ctrl+C`）：优雅终止所有子进程并按策略停止开发数据库

常规重启 `scripts/dev.mjs` 会完整保留 `.local/dev-stack/server.yaml` 与 `ocs-tiku-fork.json` 中通过面板或管理端保存的本地调试配置。开发脚本自身的单元测试可通过 `make test-dev`（`node --test scripts/dev/dev.test.mjs`）运行。
