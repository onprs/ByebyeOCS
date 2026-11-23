# 前端与客户端架构

> **适用范围**：开发维护环境，说明 Flutter 用户客户端（`apps/client_flutter/`）、React 管理员控制台（`apps/admin/`）与独立题库内嵌面板（`services/tiku/internal/platform/panel/`）的技术架构。

## 1. Flutter 单工程跨三端用户客户端

`apps/client_flutter/` 使用同一套 Dart（`>=3.4.0 <4.0.0`）与 Flutter `3.47.7` 源码构建 Web、Android 与 iOS 客户端，通过条件导出（Conditional Imports）封装平台差异，业务层与 UI 组件层完全共享：

### 1.1 平台抽象隔离层（`lib/core/platform/`）

| 抽象能力 | Web 实现（`*_web.dart`） | Android / iOS 原生实现（`*_native.dart`） |
| --- | --- | --- |
| 服务地址解析 | 固定使用同源相对路径 `/api/v1` | 读取 `SharedPreferences` 中用户在 `/connect` 配置的 HTTPS 地址（或构建时 `--dart-define=API_BASE_URL` 默认值） |
| 会话凭据存储 | 浏览器自动管理同源 `HttpOnly` Cookie，JS 内存维护 CSRF 令牌 | `flutter_secure_storage` 加密存储短期 Access Token 与单次轮换 Refresh Token |
| HTTP 客户端 | 基于 `package:web` 开启同源凭据携带（`withCredentials`） | 标准原生 TLS 客户端，拦截 `401` 单飞（Singleflight）执行刷新令牌轮换 |
| SSE 实时连接 | 封装流式 Fetch / EventSource 读取 | 原生 HTTP 长流逐行解析 `event:` / `id:` / `data:` |
| 隐私导出下载 | 创建受控 Blob / 受鉴权下载触发浏览器保存 | 写入应用受保护临时目录并调用系统文件分享/保存面板 |

### 1.2 状态管理、路由与代次保护

- **状态与仓储分层**：采用 `flutter_riverpod` 管理依赖注入与视图控制器（`AuthController`、`HomeController`、`CoursesController`、`CourseSyncController`、`PlansController`、`WalletController`、`AppearanceController` 等），下层通过无状态 `Repository` 与统一 `ApiClient` 通信。
- **防串号代次保护（Session Epoch）**：用户退出登录、切换学习通账号或在原生端更改连接的服务器地址时，`SessionManager` 递增会话代次并清空内存图片缓存（最多 64 张 / `8 MiB`）与各控制器状态；上一账号在途未完成的异步 HTTP 响应或过期游标返回后，因代次不匹配被直接丢弃，杜绝跨账号数据污染。
- **路由与壳层保持**：基于 `go_router` 的 `StatefulShellRoute.indexedStack` 维护首页、课程、安排、钱包四个主分支的组件树与 `PageStorageBucket` 滚动偏移，配合自定义轻量淡入微滑过渡（遵循系统减少动画设置）。

### 1.3 SSE 实时合并刷新与前台恢复

`RealtimeService` 统一维护单条用户态 SSE 长连接：

- **防抖合并（Coalescing）**：批量任务确认或课程快速同步产生密集事件时，前端按资源主题合并刷新请求；若某控制器的刷新请求已在途，后续新事件仅置位“待补刷”标记，待当前请求返回后补刷一次最新快照，避免并发请求风暴。
- **资源版本单调性**：客户端比较事件携带的 `resource_version` 与本地已加载模型的 `version`，自动忽略滞后于当前快照的旧事件。
- **断线轮询与生命周期感知**：`LifecycleScope` 监听浏览器标签页可见性及移动端前后台切换；应用切入后台暂停无效重连，回到前台或 SSE 游标过期返回 `resync_required` 时，立即重拉当前活动视图的一致快照；SSE 暂时不可用期间自动降级为有界低频轮询。

## 2. React 管理员控制台架构（`apps/admin/`）

管理端基于 React 19、TypeScript 5.9 与 Vite 7 构建为纯静态 SPA：

- **认证与权限门控**：`AuthProvider` 启动时请求 `/admin/api/v1/me` 初始化当前管理员身份、角色权限集与 CSRF 令牌；`RequireAuth` 拦截未登录会话，`PermissionGate` 在路由级与操作按钮级按权限字符串（如 `learning.read`、`billing.manage`、`admin.users.manage`）精确控制可见性，`LandingRedirect` 按当前角色自动导流至有权限的首个工作页。
- **步进验证与幂等封装**：针对人工调账、审批退款等高危写接口，前端拦截服务端返回的近期验证要求错误码，弹出 `StepUpDialog` 完成密码或 TOTP 二次核验后，沿用原业务 `Idempotency-Key` 安全重试，防止重复记账。
- **实时流与体验细节**：`useAdminEvents` 订阅 `/admin/api/v1/events` 驱动总览与列表增量刷新；内置 `useScrollMemory` 恢复跨页滚动位置，`useTableOverflowAffordance` 监听宽表滚动边界呈现渐变提示。

## 3. 题库双前端边界（`services/tiku/`）

独立题库仓库内包含两套用途严格区分的前端资源，互不混用：

1. **正式维护面板（`internal/platform/panel/assets/`）**：使用零构建运行时依赖的原生模块化脚本与共享 `tokens.css`，通过 Go `embed.FS` 直接编译进 `ocs-tiku-fork` 二进制并在 `/panel/` 路径提供服务（本地运行 `scripts/dev.mjs` 时通过热读代理直读磁盘实现 `0ms` 刷新）。
2. **上游 v1 兼容前端（`services/tiku/frontend/`）**：仅服务于上游根包本地 SQLite 兼容模式与独立兼容构建脚本，不参与正式平台 `/panel/` 部署，主平台构建无需编译该目录。
