# 决策 0001：视觉原型与正式客户端工程边界

- **状态**：已采纳并落地（正式客户端选型由 [决策 0005](0005-product-baseline.md) 细化固化）

## 背景

项目启动初期需要先快速对齐管理员控制台、桌面用户端与移动端的信息密度、导航结构及多套视觉风格，同时防止演示用的前端模拟状态混入后续正式账本与权限实现。

## 决策

1. 在 `prototypes/` 目录下使用 React、TypeScript、Vite 与 Tailwind 构建独立可交互的视觉原型，仅使用浏览器本地合成数据表达产品流程与界面层级。
2. 管理员控制台与普通用户客户端从信息架构、路由空间到会话语义保持完全分离，不共用同一套后台模板。
3. 正式生产工程另行独立建设（用户端位于 `apps/client_flutter/`，管理端位于 `apps/admin/`，服务端位于 `server/` 与 `services/tiku/`），严禁将 `prototypes/` 中的模拟存储、前端角色切换或演示充值逻辑带入正式生产包。

## 当前落点与影响

- `prototypes/` 作为历史视觉对照保留在仓库中（可通过 `npm --prefix prototypes run dev` 或 `node scripts/dev.mjs --with-prototypes` 本地预览），不进入 `make bundle` 生产发布归档。
- 正式三端通过根目录 `design/tokens.json` 与生成脚本共享统一的色彩与几何度量，业务状态全部对接真实服务端 REST 与 SSE 接口。
