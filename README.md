# ByebyeOCS

面向学习通用户的自托管课程与任务安排平台，提供独立管理员控制台，以及同一 Flutter 工程构建的 Web、Android 和 iOS 客户端。

- 查看课程、章节、任务点与学习资料，创建和修改多课程安排。
- 自动执行普通视频、音频、文档/阅读和支持题型的章节测验，查看状态与平台确认，暂停、继续或取消任务。
- 查看钱包、充值订单、账务明细和题库用量；付款与余额到账分别显示。
- 选择管理员发布的模型，按精确缓存优先或每次调用模型作答；安排启用后自动取题、填写、提交并复核结果。
- 管理会话、通知、数据导出与删除申请；默认石墨，支持青禾、靛蓝及浅色、深色主题。

管理员可管理用户与任务、公开模型、计费规则、订单、对账、异常退款复核、人工余额调整和权限。普通用户使用学习通身份进入，无需注册第二套账号。

## 安装与运行

服务端支持 Linux amd64/arm64，需要 PostgreSQL 16 或以上。生产访问使用两个独立 HTTPS 地址；管理员首次创建需要密码和 TOTP 动态验证码。学习任务通过原生请求执行，运行已构建的服务不需要浏览器、播放器、Node.js、Flutter、Docker或桌面环境。

从完整源码构建，需要 Git、Go 1.26 或以上、Node.js 22.12 或以上、npm、Flutter 3.47.7、GNU Make 和 Python 3：

```bash
git clone --recurse-submodules https://github.com/onprs/ByebyeOCS.git
cd ByebyeOCS
make bundle VERSION=0.1.0 GOARCH=amd64
```

已有 Git 源码先执行 `git submodule update --init --recursive`，再运行构建命令。

构建目录位于 `dist/byebyeocs-0.1.0-linux-amd64/`。将其复制到目标 Linux 主机后，按 [部署指南](deploy/README.md) 安装，配置自己的数据库和两个 HTTPS 地址，创建管理员，再启动服务。arm64 主机使用 `GOARCH=arm64`。

[配置示例](configs/server.example.yaml) 和 [题库配置](configs/ocs-tiku.example.json) 随程序提供；升级、备份、恢复和密钥轮换见 [部署与维护](docs/deployment/README.md)。

## 功能启用

学习通身份校验和学习执行需要部署者配置已获授权、经过核实的接入信息。默认关闭外部平台连接、学习执行、付款与自动收费；未定价和用量不完整时不自动扣费。

主服务以平台确认的有效任务点计量，题库按服务端回传的 token 单独计量。价格、舍入规则、模型条目和 ZPay 商户信息由部署者配置；服务不提供默认价格或默认管理员。题库使用独立数据库，上游模型密钥只保存在题库服务。

Android 工程可构建调试 APK。正式 APK 分发需要发布签名；iOS 原生构建和分发需要 macOS、签名及适用的分发条件。目前不提供已签名安装包或下载站地址。

## 设计原型

`prototypes/` 保留 React 设计原型，使用合成数据。安装 Node.js 依赖后执行 `npm --prefix prototypes run dev`，在默认的 <http://127.0.0.1:5186/review> 查看三套外观；原型与正式 Flutter 客户端分别运行。

产品范围和当前能力见 [DESIGN.md](DESIGN.md)。

项目采用 [MIT 许可证](LICENSE)。
