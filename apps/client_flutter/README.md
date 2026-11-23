# ByebyeOCS 用户客户端（Flutter）

> **适用范围**：开发维护与客户端构建环境。本目录包含正式用户客户端源码，使用同一套 Flutter 工程构建 Web、Android 与 iOS 应用。

## 功能概览

- **身份与会话**：通过学习通账号登录；Web 端使用同源 `HttpOnly` Cookie 与 CSRF 请求头，Android/iOS 原生端使用系统安全存储保存短期访问令牌与单次轮换刷新令牌。
- **连接管理**：Web 构建固定连接同源 `/api/v1`；原生端默认不硬编码服务器地址，首次启动可在“连接服务”页面填写自托管的用户端 HTTPS 地址（切换服务地址会自动清空本机会话与用户态缓存）。
- **核心模块**：首页概览、课程列表与多级目录详情（含受保护封面缓存）、多课程学习安排与实时任务进度、钱包与充值对账、独立题库查询与问题报告、个人设置（配色与主题、会话管理、通知偏好、数据导出与删除申请）。

## 环境要求

- Flutter `3.47.7`（Dart SDK `>=3.4.0 <4.0.0`）
- 构建 Android 包需配置 Android SDK 与 JDK；构建 iOS 包需使用 macOS 与 Xcode。

## 本地联调

推荐在仓库根目录使用跨平台开发脚本一键拉起后端与用户端热重载网关（默认访问 `http://127.0.0.1:5188`）：

```bash
# Ubuntu / macOS
./scripts/dev.sh

# Windows PowerShell
.\scripts\dev.ps1
```

详细热重载参数（如 `--flutter-device` 指定原生调试设备并自动注入 `API_BASE_URL`）见 [本地全栈开发指南](../../docs/development/local-dev.md)。

## 静态检查、测试与构建

在 `apps/client_flutter` 目录下执行：

```bash
# 安装依赖、静态分析与组件/单元测试
flutter pub get
flutter analyze
flutter test

# 构建生产级 Flutter Web 静态产物（离线打包字体与资源，不依赖外部 CDN）
flutter build web --release --no-web-resources-cdn

# 构建 Android 调试安装包
flutter build apk --debug
```

构建原生正式版时，可通过 `--dart-define=API_BASE_URL=https://learn.example.com/api/v1/` 注入默认服务地址。Android 正式发布签名与 iOS 签名/分发证书不在仓库内保存，相关合规与分发要求见 [客户端分发架构](../../docs/architecture/distribution.md)。
