# Web、Flutter 与外观偏好

## 1. 已确认与尚未确认

已确认：用户端采用同一 Flutter 工程构建 Web、Android 和 iOS；管理端保持独立 React 产品；任务安排和实时状态同页；默认石墨，青禾/靛蓝可切换。

已实现的原型：React Web、React 手机视觉布局、浅深主题、用户设置与管理员基础设置中的配色选择、本机持久化，两端偏好隔离。当前无 Flutter 工程，也没有跨设备偏好同步。

正式用户 Web 不再保留 React/Flutter 二选一。现有 React 只用于已验收的视觉和交互原型，不表示 Flutter 已实现；后续按该基准重建 Flutter 组件。

## 2. 已确定的工程组织

用户 Web 和 App 采用 Flutter，共享 Dart 领域模型、API 类型、状态、设计变量和尽可能多的组件；桌面和手机仍有独立布局。管理员采用 React / TypeScript / Vite / Tailwind / shadcn/ui 目标组合，保持独立认证与导航。

保留 Flutter Web 首屏、桌面交互、插件、可访问性和缓存的专项验证。技术栈已定，测量用于优化和确定适配方式；若无法达到预算，应报告限制和方案，不自行切回 React 用户 Web。

官方 [Flutter Web FAQ](https://docs.flutter.dev/platform-integration/web/faq) 说明其应用型体验及 Web 插件、浏览器和缓存限制。固定 SDK、构建参数和设备后再判断实际行为，不能用历史版本印象代替测量。

## 3. Flutter 性能与体验验证

固定版本、构建参数、浏览器和参考设备，记录真实 release 产物：

- 登录、首页、课程详情、安排编辑和钱包五个代表页。
- 冷缓存与热缓存分别测量传输量、首个可理解内容、可交互时间、主线程占用和内存。
- 参考网络建议 4 Mbps / 150 ms，目标首个可操作登录界面约 3 秒内，作为工程体验目标，不当成已达成指标。
- 中文排版、输入法、文字选择复制、1000 行/节点的数据滚动、键盘 Tab/Esc、浏览器前进后退和屏幕阅读器。
- Android Chrome、桌面 Chrome/Firefox/Safari、iOS Safari 的真实设备检查；只在 Linux Chrome 通过不足以证明 iOS 体验。

本轮 React 原型的打包值包含评审入口、双端页面、模拟数据和所有方案，不作为正式 React 或 Flutter 首屏性能承诺。

优化手段通过 release 构建验证：路由拆分、静态资源内容哈希、压缩、避免远程大字体、首屏最少数据与占位；Flutter 的具体渲染/WebAssembly 选项随选定 SDK 固定，不在本文假设某一历史模式永远可用。

## 4. Flutter 建议组织

```text
apps/client_flutter/
  lib/app/                 # 路由、依赖、生命周期与外观
  lib/features/auth/
  lib/features/home/
  lib/features/courses/
  lib/features/plans/
  lib/features/wallet/
  lib/features/settings/
  lib/core/api/            # 由 OpenAPI 生成的类型与受控封装
  lib/core/events/         # SSE、轮询降级、版本合并
  lib/core/security/       # App 安全存储 / Web Cookie 差异
  lib/design/              # 三配色与浅深主题、公共组件
```

状态管理可使用 Riverpod，路由可使用 GoRouter；这些是实现建议，固定版本和依赖审查在技术验证时完成。平台差异集中在安全存储、支付回跳、通知、文件导出、窗口布局与可访问性适配。

Web 不能使用 `dart:io` 文件系统或假设可设置任意 Cookie 头。App 的刷新令牌在系统安全存储，Web 使用 HttpOnly Cookie；不为了共享代码把 Web 凭据改存 localStorage。

## 5. 设计变量与外观同步

标准属性：`color_scheme = graphite | grove | indigo`，`theme_mode = system | light | dark`。正式默认配色为 graphite；浅深模式可跟随系统，原型目前以 light 开始。

由一个版本化设计变量文件生成 CSS 和 Dart ThemeExtension/ColorScheme，覆盖背景、文字、边框、状态、图表、输入和弹层。切换视觉不能改变路由、任务内容、钱包金额或权限。

未登录使用当前设备的选择；登录后加载对应内部用户偏好，保留尚未同步的显式修改并做条件更新。退出后不把上一用户的任务/资金缓存留给下一人。管理员偏好使用独立命名空间和接口。

原型 URL 中 `design/theme` 是显式评审覆盖；只有用户主动选择才保存。评审页三个 iframe 不自动写入偏好。这一防覆盖规则已通过浏览器检查。

## 6. 前端状态与事件

服务端状态通过资源版本合并：REST 获取快照，SSE 更新，断线补偿；版本落后事件丢弃。React 可用查询缓存，Flutter 可用 Repository/状态提供器，双方都不根据本地计时把任务变为成功。

安排编辑保留草稿，412 冲突显示在编辑面板中，不能只依赖弹层外的短暂通知。暂停/取消需要显示“请求中”和安全停止确认。

钱包支付状态与账务状态独立。前台返回立即请求订单，App 被系统终止后从本地订单引用恢复查询；余额只取服务端事实。

## 7. 移动生命周期、通知和支付

任务在服务端运行，App 后台不承担学习执行。进入后台断开或降低事件连接；回前台先验证会话并读取快照，再接事件。Push 只作为提醒，APNs/FCM 或国内分发渠道均需按地区与隐私单独设计，锁屏默认不展示题目或完整身份。

App 托管于用户自有下载站，地址暂缓。Android 提供签名 APK；iOS 网站分发和签名需满足适用地区及开发者资格，未确定前先使用 Flutter Web，原生 App 目标保留。详见 [分发设计](distribution.md)。

支付回跳使用受控 App/Universal Link 和随机订单上下文，验证归属与防重放；URL 的成功字段不直接入账。ZPay 已确认为 zpayz.cn，App 支付能力仍按实际分发方式、地区和商户能力核对。

若启用 App Store/Google Play 内购，需要独立收据/交易核验、通知、退款及来源批次账务；不能只替换收银按钮。首期以 Web 闭环为先；是否需要商店内购实现由后续实际分发方式决定，主服务用量计费与题库独立收费已确定。

## 8. 发布边界

管理端、用户 Web 和 App 独立构建与版本。正式包不含评审工具栏、模拟身份、手动到账或实验执行开关。组件复用不等于把两套产品合并成管理模板。

正式 UI 应收敛原型的覆盖样式，补充组件状态、自动化可访问性和真实设备测试。原型通过只作为交互基准，不直接升级为生产权限和账务实现。
