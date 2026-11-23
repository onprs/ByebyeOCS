# ByebyeOCS

面向学习通用户的自托管课程与学习安排管理平台，包含用户端（Web、Android、iOS）、管理员控制台与独立题库维护面板。

## 主要功能

### 用户端

- 使用学习通账号直接登录，同步课程封面、多级章节目录、任务点与学习资料。
- 按多门课程创建和编辑学习安排，支持按课程名或教师搜索课程。
- 自动执行普通视频、音频、文档阅读与支持题型的章节测验，实时展示任务进度与平台确认状态，支持暂停、继续、取消和重新调度。
- 在安排中选择题库面板发布的答题模型与答案复用方式（优先复用答案或每次调用模型），自动完成取题、作答、提交与结果复核。
- 在个人设置中使用独立题库查询功能，查看模型答案并提交答案问题报告。
- 查看钱包余额、充值订单、账务明细与题库用量，管理登录会话、通知偏好、个人数据导出与账号删除申请。
- 默认采用石墨配色，可切换青禾、靛蓝配色及跟随系统、浅色、深色模式。

### 管理端与题库面板

- 查看系统运行总览、活跃任务、异常提醒、财务汇总与实时动态。
- 管理用户与学习通会话状态、全局任务队列、角色权限、数据导出与删除申请，复核用户提交的答案问题报告。
- 配置有效任务点价格、扣费开关、订单对账、异常退款复核与人工余额调整，在线调整任务并发与课程同步请求节奏。
- 通过独立题库维护面板配置模型服务商、上游模型与密钥，发布公开答题模型、价格版本与缓存期限，查看查询记录并清理错误缓存。

## 系统要求

- **运行环境**：Linux（`amd64` 或 `arm64`）、systemd、PostgreSQL 16 及以上版本，以及两个独立的 HTTPS 访问域名（分别用于用户端与管理员控制台）。启用题库功能时需在 PostgreSQL 中准备独立的题库数据库与角色。
- **源码构建环境**：Git、Go 1.26+、Node.js 22.12+、npm、Flutter 3.47.7、GNU Make、Python 3。

## 构建与安装

克隆仓库及子模块并构建 Linux 发布目录（`arm64` 主机将 `GOARCH=amd64` 替换为 `GOARCH=arm64`）：

```bash
git clone --recurse-submodules https://github.com/onprs/ByebyeOCS.git
cd ByebyeOCS
make bundle VERSION=0.1.0 GOARCH=amd64
```

若已克隆仓库但未拉取子模块，先执行 `git submodule update --init --recursive` 再运行构建。

构建完成后生成 `dist/byebyeocs-0.1.0-linux-amd64/`。将该目录上传至目标 Linux 主机，执行安装脚本并传入用户端与管理端的 HTTPS 地址：

```bash
cd byebyeocs-0.1.0-linux-amd64
sudo ./deploy/install.sh \
  --user-origin https://learn.example.com \
  --admin-origin https://admin.example.com
```

安装完成后按顺序完成数据库初始化、管理员创建与反向代理配置：

1. 在 `/etc/byebyeocs/secrets/database.dsn` 中写入主数据库连接串（权限 `0600`，属主 `byebyeocs`），核对 `/etc/byebyeocs/config.yaml`。
2. 执行主库迁移并创建首个管理员账号（需准备管理员密码文件并绑定 TOTP 动态验证码）：
   ```bash
   sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs migrate up --config /etc/byebyeocs/config.yaml
   sudo -u byebyeocs /opt/byebyeocs/current/bin/byebyeocs admin create \
     --config /etc/byebyeocs/config.yaml \
     --username admin \
     --password-file /path/to/admin.password \
     --totp-output-file /etc/byebyeocs/secrets/admin-totp-setup.json
   ```
3. 如启用题库，按 `/etc/ocs-tiku/ocs-tiku-fork.json.example` 配置 `/etc/ocs-tiku/ocs-tiku-fork.json` 与独立数据库连接文件，执行题库数据库迁移：
   ```bash
   sudo -u ocs-tiku /opt/byebyeocs/current/bin/ocs-tiku-fork migrate -config /etc/ocs-tiku/ocs-tiku-fork.json
   ```
4. 参考 `configs/nginx-user.example.conf` 与 `configs/nginx-admin.example.conf` 配置两个 HTTPS 站点的反向代理，随后启动服务：
   ```bash
   sudo systemctl enable --now ocs-tiku.service byebyeocs.service
   ```

完整的反向代理、备份、恢复与升级步骤见 [安装指南](deploy/README.md) 与 [运维手册](docs/deployment/README.md)。

## 配置与使用说明

- **默认安全状态**：新安装的实例默认关闭外部平台连接、自动学习执行、在线收款与自动扣费，不预设默认管理员账号或默认价格。
- **平台与执行配置**：在 `/etc/byebyeocs/config.yaml`（参考 [主配置示例](configs/server.example.yaml)）中填写已获授权的学习通连接参数后开启课程读取与任务执行。
- **题库与模型配置**：在 `/etc/ocs-tiku/ocs-tiku-fork.json`（参考 [题库配置示例](configs/ocs-tiku.example.json)）中开启维护面板并配置面板账号后，通过管理端域名的 `/panel/` 添加模型服务商与密钥，发布公开模型与价格后即可在主服务启用题库功能。
- **计费与收款配置**：主服务按平台确认的有效任务点计费，题库按模型实际消耗的 token 独立计费；收款支持配置 ZPay（`zpayz.cn`）商户参数，在管理端“系统设置 → 计费与价格”发布单价并开启扣费后生效。
- **客户端使用**：浏览器可直接访问用户端 HTTPS 域名；移动端客户端首次启动可在“连接服务”页面填写自托管的用户端 HTTPS 地址。客户端构建说明见 [apps/client_flutter/README.md](apps/client_flutter/README.md)。

更多文档见 [文档目录](docs/README.md)。

## 许可证

本项目采用 [MIT 许可证](LICENSE)，第三方字体与数据许可声明见 [NOTICE](NOTICE)。
