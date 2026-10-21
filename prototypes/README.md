# 产品原型工程（开发维护环境）

整体 UI 已获确认。工程包含默认石墨、可选青禾/靛蓝的独立管理端与用户端，以及 React 手机视觉原型。正式目标已确定 Flutter Web + Flutter App、独立 React 管理端；当前没有 Flutter 工程或正式业务后端。

从项目根目录启动：

```bash
npm --prefix prototypes ci
npm --prefix prototypes run dev
```

入口：<http://127.0.0.1:5186/review>。开发服务仅监听回环。

- 管理端：`/admin/dashboard`、`/admin/accounts`、`/admin/tasks`、`/admin/questions`、`/admin/finance`、`/admin/settings`。
- 用户端：`/client/login`、`/client/home`、`/client/courses`、`/client/courses/1`、`/client/plans`、`/client/wallet`、`/client/settings`。
- 手机端：将用户路径前缀换为 `/mobile`。宽屏提供手机容器，窄屏使用整个视口。
- 参数：`design=graphite|grove|indigo`，`theme=light|dark`，`embed=1` 隐藏评审条。

## 工程边界

`src/admin` 与 `src/client` 保持产品分离；`ui.tsx` 是共享组件；`data.ts` 是合成数据与公开模型条目；`mocks/api.ts` 是延迟返回的本地模拟 API；`store.tsx` 负责原型状态、模型可见性、模拟用量/账务与版本冲突。

状态仅保存在当前浏览器，跨标签页同步；不会发起真实登录、任务、题库或付款请求。配色和浅深模式在用户/管理员独立命名空间持久化，显式预览 URL 不自动覆盖用户选择。登录输入只在临时 React 状态中存在，不存入 localStorage。当前文案已同步主服务按用量、题库单独收费、ZPay = zpayz.cn 和正常余额原则不退回；价格与用量口径未定，未启用真实扣费。顶部“演示场景”可重置数据。

管理角色、密码加密、账本事务与支付验签均为产品边界表达，没有正式服务端实现。不要将此工程直接部署为商业服务。

## 验证与截图

```bash
npm --prefix prototypes run build
npm --prefix prototypes run test:ui
```

第二条命令需要已运行预览服务和本机 Chrome。可设置 `PROTOTYPE_CHROME` 指定浏览器、`PROTOTYPE_URL` 指定预览地址。Chrome 仅为本地截图验证工具，Go 正式部署没有此默认依赖。

脚本检查关键交互与 120 个页面变体，在本地生成 `artifacts/ui-verification.json` 和 `screenshots/`。截图与 `artifacts/` 下的验证报告不进入版本管理或正式发布包，可通过脚本重新生成。设置 `SKIP_SCREENSHOTS=1` 可只检查交互；`CAPTURE_ONLY=1` 可只更新截图。

更多说明见 [设计基准](../DESIGN.md)、[信息架构](../docs/product/information-architecture.md) 与 [交互验收](../docs/design/interaction-spec.md)。
