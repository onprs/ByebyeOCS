# Linux 原生部署工具

这些脚本面向公开的 Linux + systemd + PostgreSQL 16 部署环境，不依赖维护者主机、SSH
别名、代理或固定私有路径。任务通过原生HTTP执行，不需要浏览器、播放器、媒体解码器、Docker或Flutter运行环境。

部署脚本需要Bash、GNU coreutils、findutils、tar及util-linux提供的`flock`。同一安装目录上的安装、升级、回滚和卸载会互斥执行。

## 1. 构建发布目录

在源码根目录使用明确版本号：

```sh
make server tiku admin flutter-web
make bundle VERSION=1.2.3 GOARCH=amd64
```

`bundle` 生成 `dist/byebyeocs-1.2.3-linux-amd64/`，包含两个 Go 二进制、管理员/Flutter Web
静态应用、配置和部署示例、文档、`release.json`、`checksums.txt` 与项目 `LICENSE`。
项目采用 [MIT 许可证](../LICENSE)，第三方许可证和 `NOTICES` 随应用及依赖声明保留。
当前未提供 Release 下载地址或自动下载命令；Android 调试 APK 单独构建。

Flutter Web 构建需要 Flutter SDK；Go 服务本身不需要 Flutter、Node、Chrome 或 Docker。

## 2. 安装

把发布目录放在目标 Linux 主机后：

```sh
cd byebyeocs-1.2.3-linux-amd64
sudo ./deploy/install.sh --user-origin https://learn.example.com \
  --admin-origin https://admin.example.com
```

安装器会：

- 校验 `checksums.txt`、Linux 架构和 `release.json`；
- 创建非 root 用户 `byebyeocs`（题库为独立 `ocs-tiku`）、目录和示例配置；
- 安装 `/opt/byebyeocs/releases/<version>`，原子更新 `/opt/byebyeocs/current`；
- 写入 systemd 单元但**不 enable/start**；
- 不覆盖已有 config/keys，不迁移数据库，不创建管理员，不开启真实收费。

默认布局：

| 内容 | 路径 |
| --- | --- |
| 版本程序 | `/opt/byebyeocs/releases/<version>` / `current` |
| 主配置与私钥 | `/etc/byebyeocs/config.yaml`、`/etc/byebyeocs/secrets/` |
| 数据 | `/var/lib/byebyeocs/` |
| 题库配置 | `/etc/ocs-tiku/ocs-tiku-fork.json`、`secrets/` |
| 服务用户 | `byebyeocs`、`ocs-tiku` |
| 监听 | `127.0.0.1:8090`、`127.0.0.1:8091`、题库 `127.0.0.1:8092` |

安装不会自动生成活动配置；可复制 `config.yaml.example` 并填写自己的 HTTPS 域名和数据库
DSN，或提供两个 Origin 让安装器调用正式 `config init` 生成配置和独立加密/身份索引密钥。
`config init` 会拒绝覆盖现有文件。手工使用示例配置时，还要用 `keys generate` 分别生成
`secrets/application-v1.key` 和独立的 `secrets/identity.key`，并创建仅服务用户可读的
`secrets/database.dsn`。身份索引密钥、解密 keyring 和数据库连接文件都位于 `secrets/`，
必须与配置一起备份。

后续步骤：

```sh
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs config check \
  --config /etc/byebyeocs/config.yaml
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs migrate up \
  --config /etc/byebyeocs/config.yaml
sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs admin create \
  --config /etc/byebyeocs/config.yaml --username ... --password-file ... \
  --totp-output-file /etc/byebyeocs/secrets/admin-totp-setup.json
sudo systemctl enable --now byebyeocs.service
```

数据库必须先由用户安装为原生 PostgreSQL 16，并创建独立主库/角色；题库需要独立数据库和
角色，不能复用主库 schema。安装器不假定数据库单元名、不创建数据库。

启用题库时还要执行题库自己的迁移；主服务 `migrate up` 不会创建题库 schema，题库 `serve`
也不会自动迁移：

```sh
sudo -u ocs-tiku /opt/byebyeocs/current/bin/ocs-tiku-fork migrate \
  -config /etc/ocs-tiku/ocs-tiku-fork.json
```

## 3. HTTPS 与 SSE

`configs/nginx-user.example.conf` 和 `configs/nginx-admin.example.conf` 是独立域名示例。
按实际证书、域名和代理网络替换；管理员入口应加 VPN、mTLS 或 IP allowlist。SSE 路径
`/api/v1/events` 与 `/admin/api/v1/events` 示例已关闭代理缓冲、缓存和 gzip，并延长读取超时。
反向代理不是安装前提；使用其他入口时同样要关闭 SSE 缓冲并设置合理请求体上限。

支付回调的内部路由是 `/payments/callbacks/zpay`，只注册在管理员监听上；用户监听不会处理
支付回调。按实际公网代理拓扑填写 `payments.notify_url`，确保渠道 POST 最终到达这个内部
路由（默认示例为 `https://admin.example.com/payments/callbacks/zpay`；使用单独的公网域名
或路径时由代理转发）。管理员入口设置 IP allowlist 时，要为回调保留公网访问（管理员示例
已给出不受 allowlist 限制的 `location = /payments/callbacks/zpay`）；使用 VPN 或 mTLS 保护
管理员入口时，要为回调单独提供一个不经该限制、转发到管理员监听的 HTTPS 入口。若渠道回调
返回 404/403 或订单长期未到账，先核对 notify_url 的主机/路径是否到达该内部路由以及代理
是否拦住了回调；回调被拒绝时不会入账，只能由管理员主动查单。

`payments.notify_url`与`payments.return_url`均须为不带查询参数的HTTPS地址；这是[ZPay接口规则](https://zpayz.cn/doc.html)。付款返回地址填写对应页面的HTTPS路径。

## 4. 升级、回滚、卸载

```sh
sudo ./deploy/upgrade.sh --prefix /opt/byebyeocs
sudo ./deploy/rollback.sh --version 1.1.0
sudo ./deploy/uninstall.sh
```

- 升级/回滚只切换 `current`，保留配置、密钥、数据和旧版本目录；不会执行 migrate，也不会
  回退数据库 schema。需要迁移时必须先备份并显式运行 `migrate status/up`。
- 回滚前要人工确认旧程序能读取当前 schema；脚本不会自动降级数据库。
- 卸载默认保留 `/etc/byebyeocs`、`/etc/ocs-tiku`、`/var/lib/byebyeocs` 和数据库。只有显式
  `--purge --yes` 才删除配置/密钥/数据目录；PostgreSQL 数据库始终不由脚本删除。
- 安装状态记录在 `/opt/byebyeocs/install.env`（0600），升级和回滚需要该文件可读，以沿用原安装路径与服务用户。
- `--purge --yes`清除自定义配置或数据路径时需要有效安装记录；系统级目录会被拒绝。

## 5. 备份

需要公开依赖 `age`、PostgreSQL 客户端 `pg_dump`/`libpq` 和 Python 3（仅使用标准库
`ctypes` 调用 libpq 官方 `PQconninfoParse` 解析 URL/keyword DSN）。复制
`deploy/backup.env.example` 为
`/etc/byebyeocs/backup.env`，设置：

- 主/题库配置目录与 DSN 文件；
- age 公钥 `BYEBYEOCS_AGE_RECIPIENT`；
- 异机 SSH 目标与远端目录；
- operations 恢复点状态文件和其服务用户。

执行：

```sh
sudo ./deploy/backup.sh
```

脚本对主库和题库库分别执行 `pg_dump`（各自一致性快照；跨两个数据库不宣称单一原子快照），
把数据库 dump、完整配置目录、应用密钥、身份索引密钥和 keyring 放入临时目录，用 age 加密，
再通过 SSH 上传并用远端 `sha256sum` 校验。**只有远端校验通过后**才原子更新
`backup-status.json`：

```json
{"status":"ok","last_recoverable_at":"dump开始UTC","verified_at":"远端校验UTC","offsite":true,
 "archive_sha256":"...","restore_drill_verified":false}
```

- `last_recoverable_at` 是第一个 dump 开始时间，属于保守值。
- `status=ok` 只代表异机校验通过；`restore_drill_verified=false` 表示尚未完成恢复演练。
- 远端失败、本地复制、缺少 age/SSH 配置都不会写成功状态。
- 归档先以临时名写入，完整加密、上传和校验后才成为正式归档；中断产生的半成品不计入可恢复备份。
- 备份在暂存目录的私有工作目录内处理明文dump与配置，需预留磁盘空间；暂存目录不能位于被备份的配置目录内部。正常退出会清理明文工作目录，下次持锁运行会清理中断残留。
- DSN 通过 Python ctypes 调用 libpq 官方解析器，写入临时 0600 service file 和
  `PGPASSFILE`；DSN/密码不出现在 `pg_dump` argv。解析失败只输出固定文案，不转发可能含
  DSN 片段的 parser 原文。多 host DSN 生成对齐的逐 host pgpass；host/port 数量不一致或
  使用 `passfile`/`service`/`servicefile` 时失败关闭。`BYEBYEOCS_LIBPQ` 可指定非默认 libpq。
- `--local-only` 只保留本地加密归档，不写 offsite 成功状态；本地复制不能冒充异机备份。
- 备份包含主服务和题库的配置、私密keyring与数据库dump；临时导出和本地运行日志按保留策略管理。
- 异机目标必须能通过 DNS/hosts 解析且不能是本机；脚本还会比对远端 machine-id，SSH/scp
  使用 BatchMode、StrictHostKeyChecking 和连接/存活超时；运行备份的账号需预置可信
  known_hosts 和受限 SSH 密钥。目标不可解析或无法核对时失败关闭。
- 解密身份私钥必须离线保管，不能和备份归档放在同一主机。
- 安装器会安装但不启用 `byebyeocs-backup.service`（oneshot，TimeoutStartSec=20min）和
  `byebyeocs-backup.timer`（默认每 10 分钟，Persistent=true，随机延迟最多 1 分钟）。配置好
  backup.env、age 和 SSH 后执行 `systemctl enable --now byebyeocs-backup.timer`；首次建议先
  手动运行一次并查看 journal。修改 staging 路径时同步调整 unit 的 ReadWritePaths。
- systemd 备份以 root 运行，`/etc/byebyeocs/backup.env` 必须由 root 所有且 0600；不要把它
  放在服务用户可写的位置。备份脚本会拒绝符号链接、组/其他用户可访问的环境文件和 DSN。
- `identity.key` 是独立身份索引密钥，不能随加密主密钥轮换；备份会原样保存它和旧 keyring。
- 需要轮换加密密钥时使用正式 `keys generate`/`keys reencrypt` 流程；旧 keyring 文件保留在
  secrets 中并随本次配置目录备份，不能只备份当前主密钥。

## 6. 隔离恢复演练

在非 root 的普通用户环境中，且 PostgreSQL 的 `initdb`/`pg_ctl`/`pg_restore` 可用时：

```sh
./deploy/restore-drill.sh --archive /path/to/backup.tar.age \
  --identity /path/to/age-identity.txt --report /tmp/restore-drill.json
```

脚本创建临时 `initdb` 集群和临时数据库，分别恢复主库/题库 dump，执行最小连通查询，然后
停止并删除临时集群。大型归档需要为`TMPDIR`指定有足够空间的专用目录。它不连接真实数据库，不使用应用账号，不要求应用账号有 CREATEDB；报告
是独立文件，不写入生产 `backup-status.json`。报告通过只证明本次隔离恢复成功，不等于已完成
生产恢复演练审批。

## 7. 构建/检查目标

```sh
make server tiku admin flutter-web
make check-go check-admin        # 当前环境无 Flutter SDK 时可先做这两项
make check                       # 完整 Go + 管理端 + Flutter 检查
make test-go test-admin test-integration test-deploy
make bundle VERSION=1.2.3 GOARCH=arm64
```

`test-integration` 要求 `BYEBYEOCS_TEST_DATABASE_DSN_FILE` 指向隔离 PostgreSQL 连接文件，并
运行 server 与 tiku 的全部包，不只运行 PG 包。`scripts/test-deploy.sh` 只用临时目录和假
`pg_dump`/`age`/`scp`/`ssh` 验证幂等、失败和路径安全，不会实际 SSH 或部署。
