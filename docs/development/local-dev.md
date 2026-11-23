# 本地全栈热重载开发环境

本文面向在本地（Windows 或 Ubuntu / Linux）开发调试本仓库的开发者。所有运行态配置、随机密钥、开发数据库与临时构建产物均位于 `.local/dev-stack/`（已被 `.gitignore` 忽略），仅监听 `127.0.0.1` 回环地址，不依赖任何维护者私有配置或线上凭据。

## 前置条件

按需要启动的模块准备对应工具：

- **必选**：Git（并初始化子模块 `git submodule update --init --recursive`）、Node.js 22.12+（脚本零第三方依赖，仅用内置模块）、Go 1.26+、npm。
- **PostgreSQL（三选一）**：
  - 安装并运行 Docker（默认优先自动拉起本工作区专属隔离容器与具名数据卷）；
  - 或本机安装 PostgreSQL 16+（自动探测 `initdb` / `pg_ctl` / `psql`，在 `.local/dev-stack/pgdata` 创建隔离实例）；
  - 或通过 `--main-dsn` / `--tiku-dsn` 连接已有的两套独立数据库。
- **可选**：
  - Flutter 3.47.7（开启用户端热重载；未安装时自动回落到已构建的 `apps/client_flutter/build/web` 静态产物，不阻断后端与管理端调试）；
  - Python 3（修改 `design/tokens.json` 时自动同步三端 CSS 与 Dart 变量）。

## 快捷启动

先做环境自检（只读，不修改任何文件）：

```bash
node scripts/dev.mjs doctor
```

一键拉起全栈热重载环境：

```bash
# 跨平台统一命令（Windows / Ubuntu / macOS 通用）
node scripts/dev.mjs

# Ubuntu / macOS 快捷脚本或 Make
./scripts/dev.sh
make dev

# Windows PowerShell
.\scripts\dev.ps1
```

需要同时调试用户端登录页但尚未配置真实学习通接入时，可显式启用仓库内置的 `devfixture` 本地合成登录（仅用于本地 UI/API 联调，不连接学习通）：

```bash
node scripts/dev.mjs --dev-login
```

## 默认访问地址与热生效范围

| 入口 | 默认地址 | 实时修改热生效方式 |
|---|---|---|
| 用户端（同源入口） | `http://127.0.0.1:5188` | `/api/*` 代理至主服务（SSE 无缓冲透传），其余 HTTP/WebSocket 代理至 Flutter Dev；保存 `apps/client_flutter/lib/**` 自动触发热重载（`r`），修改 `pubspec.yaml` 自动 `pub get` 并热重启（`R`） |
| 管理员控制台 | `http://127.0.0.1:5187` | Vite 原生 HMR 毫秒级更新；已开启本地回环自动登录，同域代理 `/admin/api`、`/health` 与 `/panel/` |
| 题库维护面板 | `http://127.0.0.1:5187/panel/` | `services/tiku/internal/platform/panel/assets/*` 直读磁盘 **0ms** 刷新生效；修改题库 Go 源码自动增量编译重启；已开启本地回环自动登录 |
| 主服务 Go API | `127.0.0.1:8090` / `8091` | 监听 `server/` 下的 `.go` / `.sql` / `go.mod`，保存后增量编译并优雅重启；`.sql` 变更自动先执行 `migrate up`；编译失败不中断旧进程 |
| 独立题库 Go API | `127.0.0.1:8092` | 监听 `services/tiku/` 源码，保存后增量编译并优雅重启；`.sql` 变更自动先执行 `migrate` |
| 设计变量同步 | `design/tokens.json` | 修改后自动运行 `scripts/generate-design-tokens.py`，联动触发管理端、题库面板与用户端刷新 |
| 设计原型（可选） | `http://127.0.0.1:5186/review` | 传入 `--with-prototypes` 时启用 Vite HMR |
| 开发数据库 | `127.0.0.1:25435` | 隔离维护主库 `byebyeocs_dev` 与题库独立库 `byebyeocs_tiku_dev` 及各自角色 |

## 常用开发组合与快捷键

```bash
# 仅启动数据库 + 主服务 + 题库 + 管理端（跳过 Flutter 用户端）
node scripts/dev.mjs --skip client

# 多工作区并行调试：整体平移所有端口 +100
node scripts/dev.mjs --port-offset 100

# 指定本机 Flutter SDK 路径或调试桌面/移动设备
node scripts/dev.mjs --flutter /path/to/flutter --flutter-device linux

# 使用外部 PostgreSQL
node scripts/dev.mjs --main-dsn "postgres://main:pw@127.0.0.1:5432/byebyeocs_dev?sslmode=disable" \
  --tiku-dsn "postgres://tiku:pw@127.0.0.1:5432/byebyeocs_tiku_dev?sslmode=disable"

# 退出时保留开发数据库常驻，加速下次启动
node scripts/dev.mjs --keep-db
node scripts/dev.mjs down     # 手动停止常驻开发数据库

# 彻底重置本地开发数据库与配置（重新初始化）
node scripts/dev.mjs reset
```

运行中在交互终端可直接按键：

- `s`：手动重编并重启主服务
- `t`：手动重编并重启题库服务
- `r` / `R`：手动触发 Flutter 热重载 / 热重启
- `m`：立即执行主库与题库显式迁移
- `d`：手动同步 `design/tokens.json`
- `p`：重新打印所有访问地址
- `h`：显示快捷键说明
- `q` 或 `Ctrl+C`：优雅关闭全部子进程与开发数据库

## 配置持久化与自定义

常规重启不会覆盖已生成的 `.local/dev-stack/server.yaml` 与 `.local/dev-stack/ocs-tiku-fork.json`，在题库维护面板中添加的测试供应商/模型、在主管理端调整的运行参数或手动编辑的本地配置均会保留。若希望恢复默认开发配置而不清库，可追加 `--reinit-config`。
