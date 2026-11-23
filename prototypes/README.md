# 视觉与交互设计原型（开发维护环境）

> **适用范围**：开发维护环境。本目录保留项目初期用于确立视觉风格与交互结构的 React 原型工程。正式生产代码位于 `server/`、`services/tiku/`、`apps/admin/` 与 `apps/client_flutter/`；切勿将本目录部署为生产服务。

## 工程定位

本工程使用本地内存与浏览器 `localStorage` 合成数据呈现石墨（`graphite`，默认）、青禾（`grove`）、靛蓝（`indigo`）三套配色及浅深色模式，包含管理端、桌面用户端与移动端视口容器。

- `src/admin/` 与 `src/client/` 分别模拟管理端与用户端界面，不发起任何真实网络认证、平台同步、题库调用或支付请求。
- 外观偏好分别存储在 `byebyeocs-appearance-v1:client` 与 `byebyeocs-appearance-v1:admin` 键下；URL 显式传入的预览参数仅对当前视图生效，不会覆盖持久化的用户偏好。
- 顶部“演示场景”工具栏仅用于在本地重置合成状态或演示两阶段充值入账变化。

## 启动预览

需要 Node.js `22.12+` 与 npm。在仓库根目录执行：

```bash
npm --prefix prototypes ci
npm --prefix prototypes run dev
```

开发服务器仅监听回环地址，默认评审入口为 <http://127.0.0.1:5186/review>（也可在运行全栈脚本 `node scripts/dev.mjs --with-prototypes` 时一并拉起）：

- **管理端原型路径**：`/admin/dashboard`、`/admin/accounts`、`/admin/tasks`、`/admin/questions`、`/admin/finance`、`/admin/settings`
- **桌面用户端路径**：`/client/login`、`/client/home`、`/client/courses`、`/client/courses/1`、`/client/plans`、`/client/wallet`、`/client/settings`
- **移动视口原型路径**：将 `/client` 前缀替换为 `/mobile`
- **URL 预览参数**：`design=graphite|grove|indigo`、`theme=light|dark`、`embed=1`（隐藏顶部评审工具条）

## 构建与界面自检

```bash
npm --prefix prototypes run build
npm --prefix prototypes run test:ui
```

`test:ui` 需先保持 `run dev` 预览服务在线，并使用本机 Chrome/Chromium（可通过 `PROTOTYPE_CHROME` 指定可执行文件路径，`PROTOTYPE_URL` 指定预览地址，`SKIP_SCREENSHOTS=1` 跳过截图，`CAPTURE_ONLY=1` 仅更新截图）。生成的报告与截图保存在被 `.gitignore` 忽略的 `prototypes/artifacts/` 与 `prototypes/screenshots/` 中。

相关规范见 [设计系统](../docs/design/design-system.md) 与 [交互状态规范](../docs/design/interaction-spec.md)。
