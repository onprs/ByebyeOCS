# 设计系统与共享令牌规范

> **适用范围**：开发维护与三端界面设计基准，统一约束 Flutter 用户端（`apps/client_flutter/`）、React 管理员控制台（`apps/admin/`）与独立题库维护面板（`services/tiku/internal/platform/panel/`）。

## 1. 单一事实源与代码生成流水线

全平台视觉属性统一维护在 `design/tokens.json` 中，通过 `python3 scripts/generate-design-tokens.py` 自动分发为三端目标代码：

- `apps/admin/src/styles/tokens.css`（管理员控制台 CSS 变量）
- `services/tiku/internal/platform/panel/assets/tokens.css`（独立题库面板 CSS 变量）
- `apps/client_flutter/lib/design/palette_data.dart`（Flutter `AppMetrics`、`AppTypography` 与六套 `AppPalette` 常量）

执行 `python3 scripts/generate-design-tokens.py --check` 可只读校验三端生成产物是否与源定义一致；运行全栈开发脚本 `node scripts/dev.mjs` 时，`DesignTokensWatcher` 会监听 `design/tokens.json` 保存并自动重生成。严禁在业务组件内手工硬编码独立十六进制色值覆盖共享令牌。

## 2. 三套配色与浅深主题

三端统一采用相同的组件几何尺寸、圆角与信息密度，通过色相与冷暖调区分三套可选配色，每套均包含经过调校的 `light` 与 `dark` 双模式：

| 配色标识 | 中文名称 | 视觉定位 | 默认状态 |
| --- | --- | --- | --- |
| `graphite` | 石墨 | 冷灰底色配合沉稳蓝灰主色（浅色 `#4969ce` / 深色 `#91a7ed`），强调高密度运维与任务清晰度 | 系统默认 |
| `grove` | 青禾 | 温润纸感暖白底色配合森林绿主色（浅色 `#2d7159`），适合长时间课程目录浏览 | 用户/管理员可选 |
| `indigo` | 靛蓝 | 高对比清爽冷调配合靛青主色，强化层级边界与数据表格扫读 | 用户/管理员可选 |

- **偏好隔离与同步**：普通用户、主平台管理员与题库面板维护员各自独立保存配色方案及主题模式（跟随系统 `system`、浅色 `light`、深色 `dark`），互不串写；用户与管理员偏好通过带乐观锁版本的接口跨设备同步。
- **无障碍对比度门禁**：生成脚本在每次输出前自动计算全部 6 套色板（3 配色 × 2 明暗）中 13 组核心前景/背景组合的相对亮度比，强制满足 WCAG AA 级 `>= 4.5:1` 要求，低于阈值即报错终止生成。

## 3. 排版标尺与几何度量

### 3.1 字级标尺（像素 / 逻辑像素）

全平台最小字号严格控制在 `12px` 及以上；中文优先回退链为 `Inter, "Noto Sans CJK SC", "PingFang SC", "Microsoft YaHei", sans-serif`（Flutter Web 内置 `BboCjk` 字体资源保障离线中文与全角货币符号 `￥` 渲染）：

| 令牌名 | 尺寸 | 典型用途 |
| --- | --- | --- |
| `font-display` | `34` | 钱包核心余额、核心大数字展示 |
| `font-title` | `26` | 一级页面主标题 |
| `font-section` | `16` | 分区标题、卡片头部标题、抽屉标题 |
| `font-body` | `14` | 正文、表单输入、表格主体内容 |
| `font-secondary` | `13` | 次要说明、列表摘要、辅助元数据 |
| `font-caption` | `12` | 时间戳、徽标文字、表头小标签（全局最小字号下限） |

### 3.2 空间与容器尺寸

| 令牌名 | 数值 | 用途说明 |
| --- | --- | --- |
| `space-unit` | `8` | 基础间距模数 |
| `radius-sm` / `radius` / `radius-lg` | `6` / `10` / `14` | 小型标签、常规卡片与输入框、大型弹层圆角 |
| `control-height` / `touch-height` | `36` / `44` | 桌面标准控件高度与移动触控最小可点高度 |
| `topbar-height` / `sidebar-width` | `64` / `224` | 用户端顶栏高度与管理端侧边导航宽度 |
| `content-width` / `table-width` | `1280` / `1440` | 常规正文最大宽度，以及管理端任务/财务/题库宽表最大容器宽度 |
| `drawer-width` | `600` | 桌面端右侧详情与编辑抽屉标准宽度 |
| `course-card-height` | `330` | 课程卡片固定高度，保障网格对齐 |

## 4. 状态语义与动效准则

- **状态双通道表达**：绿色（已确认/已到账/正常）、琥珀色（执行中/待入账/冷却提醒）、红色（异常中断/失败/高水位）、蓝紫色（排队/已归档）用于辅助识别，所有状态必须同时配有明确的中文状态文本，禁止仅靠圆点颜色传达状态；单屏同时出现的高饱和状态色不超过两种。
- **连续动效与无障碍降级**：导航切换、分组页签与设置侧栏共享连续滑动选中指示器；骨架屏脉冲与路由过渡动画统一检测系统 `prefers-reduced-motion` / `MediaQuery.disableAnimationsOf`，开启减少动态效果时自动切换为无动画瞬时呈现。
