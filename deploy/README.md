# Linux 原生部署指南

> **适用范围**：公开用户生产部署环境。本目录下的脚本面向自行管理 Linux（`amd64` 或 `arm64`）、systemd 与 PostgreSQL 16+ 的部署者，仅依赖通用系统工具，无维护者主机、代理或私有目录假设。

## 1. 环境与工具要求

- **目标主机运行要求**：
  - Linux `amd64` 或 `arm64`，启用 systemd。
  - 原生 PostgreSQL 16 及以上版本（提前为主服务创建独立数据库与角色；若启用题库，另建独立的题库数据库与角色）。
  - Bash、GNU coreutils、findutils、tar、sed、grep、`util-linux`（`flock`）。
  - 两个独立的 HTTPS 访问域名及反向代理（如 Nginx / OpenResty / Caddy），分别承载用户端与管理员控制台。
  - 启用备份功能时另需 PostgreSQL 客户端工具（`pg_dump`、`pg_restore`、`initdb`、`pg_ctl`、`psql`）、Python 3（带 `libpq5` 供 DSN 解析）、`age` 加密工具与 SSH/scp 客户端。
- **构建机器要求**：Git、Go 1.26+、Node.js 22.12+、npm、Flutter 3.47.7、GNU Make、Python 3。构建完成后，目标运行主机无需安装 Go、Node.js、Flutter、Docker、浏览器或媒体播放器。

## 2. 构建发布目录

在完整克隆（含 `services/tiku` 子模块）的源码根目录执行：

```bash
make bundle VERSION=1.0.0 GOARCH=amd64
```

`arm64` 服务器将 `GOARCH=amd64` 替换为 `GOARCH=arm64`。命令会在 `dist/byebyeocs-1.0.0-linux-amd64/` 生成自包含发布包，内含：

- `bin/byebyeocs` 与 `bin/ocs-tiku-fork` 两个静态编译 Linux 二进制；
- `apps/admin/` 管理员静态前端与 `apps/client-web/` Flutter Web 静态前端；
- `configs/` 配置与反向代理示例、`deploy/` 部署与备份脚本、`docs/` 与 `api/openapi.yaml`；
- `release.json`（含版本、架构、主库迁移版本 `main_schema` 与题库迁移版本 `tiku_schema`）、`checksums.txt`、`LICENSE` 及 `THIRD_PARTY_NOTICES/`。

## 3. 首次安装

将生成的发布目录上传至目标 Linux 主机，进入该目录执行 `install.sh`。传入 `--user-origin` 与 `--admin-origin` 时，安装器会自动调用 `byebyeocs config init` 初始化主配置文件与两把独立的 32 字节密钥（应用加密主密钥与长期身份索引密钥）：

```bash
cd byebyeocs-1.0.0-linux-amd64
sudo ./deploy/install.sh \
  --user-origin https://learn.example.com \
  --admin-origin https://admin.example.com
```

常用可选参数（完整列表执行 `./deploy/install.sh --help` 查看）：

- `--prefix DIR`：程序版本目录（默认 `/opt/byebyeocs`）
- `--config-dir DIR`：主服务配置目录（默认 `/etc/byebyeocs`）
- `--data-dir DIR`：主服务数据目录（默认 `/var/lib/byebyeocs`）
- `--tiku-config-dir DIR`：题库服务配置目录（默认 `/etc/ocs-tiku`）
- `--service-user NAME` / `--tiku-user NAME`：非 root 服务运行账号（默认 `byebyeocs` 与 `ocs-tiku`，二者必须分离）
- `--skip-tiku`：仅安装主服务与对应 systemd 单元，跳过题库目录与单元

默认安装布局与监听端口：

| 项目 | 默认路径 / 值 | 权限与边界 |
| --- | --- | --- |
| 程序版本与当前指针 | `/opt/byebyeocs/releases/<version>`、`/opt/byebyeocs/current` | root 所有，服务进程只读 |
| 主服务配置与密钥 | `/etc/byebyeocs/config.yaml`、`/etc/byebyeocs/secrets/` | `byebyeocs` 所有，权限 `0700`/`0600` |
| 主服务数据目录 | `/var/lib/byebyeocs/` | `byebyeocs` 所有，权限 `0700` |
| 题库配置与密钥 | `/etc/ocs-tiku/ocs-tiku-fork.json`、`/etc/ocs-tiku/secrets/` | `ocs-tiku` 所有，支持面板热保存回写 |
| 主服务监听地址 | 用户端 `127.0.0.1:8090`、管理端 `127.0.0.1:8091` | 仅限回环 IP，双端口必须不同 |
| 题库服务监听地址 | `127.0.0.1:8092` | 仅限内网或回环访问 |

**安装器的安全保证**：校验 `checksums.txt` 与 CPU 架构后才写入文件；使用 `flock` 保证同一安装前缀互斥操作；绝不覆盖已存在的配置文件或密钥文件；不自动执行数据库迁移，不创建默认管理员，不开启收费，也不自动启动 systemd 服务。

## 4. 数据库初始化、管理员创建与启动

### 4.1 配置并迁移主数据库

创建仅 `byebyeocs` 可读的数据库连接文件（本机回环数据库可显式指定 `sslmode=disable`，远程数据库必须使用 `sslmode=verify-full`；特殊字符需 URL 编码）：

```bash
sudo install -m 0600 -o byebyeocs -g byebyeocs /dev/null /etc/byebyeocs/secrets/database.dsn
sudo tee /etc/byebyeocs/secrets/database.dsn >/dev/null <<'EOF'
postgres://byebyeocs:URL_ENCODED_PASSWORD@127.0.0.1:5432/byebyeocs?sslmode=disable
EOF
```

若安装时未传 `--user-origin` 与 `--admin-origin`，可按 `/etc/byebyeocs/config.yaml.example` 手动创建 `config.yaml`，并分别执行两次 `byebyeocs keys generate --output ...` 生成 `secrets/application-v1.key` 与独立的 `secrets/identity.key`。

校验配置、执行显式迁移并创建首个 `owner` 管理员：

```bash
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs config check --config /etc/byebyeocs/config.yaml
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs migrate up --config /etc/byebyeocs/config.yaml
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs admin create \
  --config /etc/byebyeocs/config.yaml \
  --username admin \
  --password-file /root/admin-init.password \
  --totp-output-file /etc/byebyeocs/secrets/admin-totp-setup.json
```

使用身份验证器扫描或录入 `admin-totp-setup.json` 中的密钥后，立即安全删除 `/root/admin-init.password` 与 `admin-totp-setup.json`。

### 4.2 配置并迁移独立题库（可选）

题库使用独立的 PostgreSQL 数据库与角色：

1. 将 `/etc/ocs-tiku/ocs-tiku-fork.json.example` 复制为 `/etc/ocs-tiku/ocs-tiku-fork.json`（属主 `ocs-tiku`，权限 `0600`）。
2. 在 `/etc/ocs-tiku/secrets/database.dsn` 写入题库数据库连接串，在 `/etc/ocs-tiku/secrets/service-token` 写入至少 32 字符的随机服务令牌（若主服务启用题库，主服务侧 `questions.token_file` 须保存相同令牌）。
3. 如需启用 `/panel/` 独立维护面板，使用 CLI 生成面板账号密码哈希文件，并在 `ocs-tiku-fork.json` 中设置 `panel.enabled: true`、`panel.origin: "https://admin.example.com"` 及 `panel.users`：
   ```bash
   sudo -u ocs-tiku /opt/byebyeocs/current/bin/ocs-tiku-fork panel-password \
     -password-file /root/panel-init.password \
     -out /etc/ocs-tiku/secrets/panel-admin.hash
   ```
4. 校验题库配置并执行题库迁移：
   ```bash
   sudo -u ocs-tiku /opt/byebyeocs/current/bin/ocs-tiku-fork config check -config /etc/ocs-tiku/ocs-tiku-fork.json
   sudo -u ocs-tiku /opt/byebyeocs/current/bin/ocs-tiku-fork migrate -config /etc/ocs-tiku/ocs-tiku-fork.json
   ```

### 4.3 启动服务

```bash
sudo systemctl enable --now ocs-tiku.service
sudo systemctl enable --now byebyeocs.service
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs doctor --config /etc/byebyeocs/config.yaml
```

内存紧张（如 1GB–2GB 主机）时，可将发布包 `deploy/systemd/byebyeocs-low-memory.conf` 与 `ocs-tiku-low-memory.conf` 放入对应 systemd drop-in 目录以收敛 Go 运行时内存水位。

## 5. 反向代理、SSE 与支付回调

参考 `configs/nginx-user.example.conf` 与 `configs/nginx-admin.example.conf` 配置反向代理：

- **用户端域名**（如 `https://learn.example.com`）转发至 `127.0.0.1:8090`；对 `/api/v1/events` 关闭 `proxy_buffering`、`proxy_cache` 与 `gzip`，并将读超时延长至 `3600s`。
- **管理端域名**（如 `https://admin.example.com`）转发至 `127.0.0.1:8091`；对 `/admin/api/v1/events` 同样关闭缓冲与压缩；将 `location ^~ /panel/` 转发至题库服务 `127.0.0.1:8092`，且题库 `panel.origin` 必须与该 HTTPS 域名完全一致。
- **支付异步通知路由**：内部回调路由固定为 `/payments/callbacks/zpay`，仅注册在管理监听（`127.0.0.1:8091`）上。若管理端启用了来源 IP 限制，须为 `/payments/callbacks/zpay` 单独放行公网 POST 或通过独立回调入口转发至 `127.0.0.1:8091/payments/callbacks/zpay`；`payments.notify_url` 与 `return_url` 必须是不含查询参数的 HTTPS 地址。

## 6. 升级、回滚、备份与卸载

- **升级程序**：在新版发布目录执行 `sudo ./deploy/upgrade.sh`。脚本校验新包并更新 `/opt/byebyeocs/current`。若新版 `release.json` 中的 `main_schema` 或 `tiku_schema` 增加，需先完成备份，再建议以 `--no-restart` 切换二进制、手动执行两库 `migrate` 后重启服务（先启动 `ocs-tiku`，再启动 `byebyeocs`）。
- **版本回滚**：执行 `sudo /opt/byebyeocs/current/deploy/rollback.sh --version <已安装旧版本号>`。回滚仅切换程序与静态资源，不会降级数据库 schema；跨不兼容 schema 变更请走备份恢复流程。
- **加密备份与隔离演练**：按 `/etc/byebyeocs/backup.env.example` 配置 `/etc/byebyeocs/backup.env`（填入 `age` 公钥与异机 SSH 目标），通过 `sudo /opt/byebyeocs/current/deploy/backup.sh` 或启用 `byebyeocs-backup.timer` 定期执行；在普通用户下运行 `/opt/byebyeocs/current/deploy/restore-drill.sh --archive <归档.tar.age> --identity <age私钥>` 可在临时 `initdb` 集群中验证归档可恢复性。
- **卸载程序**：执行 `sudo /opt/byebyeocs/current/deploy/uninstall.sh` 移除程序目录与 systemd 单元（默认保留全部配置、密钥、数据目录与数据库；加 `--purge --yes` 才会清理配置与数据目录，任何情况下均不删除 PostgreSQL 数据库）。

灾难恢复、`.recovery-required` 对账解锁与密钥轮换细则见 [生产运维与恢复手册](../docs/deployment/README.md)。
