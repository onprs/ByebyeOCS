# 决策 0001：原型与正式客户端边界

状态：原型方案保留；正式用户端已由后续决策 0005 确认为 Flutter Web + Flutter App。

## 原型选择

管理端与用户视觉原型使用 React + TypeScript + Vite，配合 Tailwind、自建组件、Radix Dialog 和 Lucide。两个产品保持独立导航、权限语义和信息密度。

制作第一轮原型时未发现 Flutter SDK，因此手机采用可操作的 React 视觉布局。它是后续 Flutter 的交互基准，不能称为 Flutter App，也不能证明 Flutter 首屏、后台生命周期或支付回跳可用。

原型使用同一 Vite 工程便于预览与模拟状态共享。正式管理员、Flutter Web 与 App 保持适当的构建、认证和发布边界。

## 后续已确认

- 用户端使用同一 Flutter 工程构建 Web、Android 和 iOS，尽量共享业务与组件。
- 管理端保留 React 目标技术栈，与学生学习空间独立。
- Flutter Web 首屏、桌面交互、缓存和可访问性继续做 release 验证，优化结果不自动改变已选技术栈。
- 默认石墨，青禾/靛蓝可选；数据库 PostgreSQL。
- 主服务按用量、题库独立收费，价格暂缓；ZPay 为 zpayz.cn。
- 首版先 Web 和已验证原生能力，App 自有下载站，实际签名与分发条件分平台验收。

详细方案见 [Flutter 与 Web 架构](../architecture/frontend.md)、[最新产品基线](0005-product-baseline.md) 和 [当前配置清单](../product/decision-checklist.md)。正式业务与 Flutter 工程尚未实现。
