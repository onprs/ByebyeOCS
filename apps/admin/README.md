# ByebyeOCS 管理员控制台（React）

> **适用范围**：开发维护与构建环境。本目录包含正式管理员控制台前端源码，基于 React 19、TypeScript 与 Vite 构建，编译为静态资源由主服务或反向代理托管。

## 功能与路由

管理员控制台对接主服务管理监听端口（`/admin/api/v1`），按当前登录管理员的 RBAC 权限（`owner`、`operator`、`support`、`finance`、`observer`）动态呈现导航与操作：

- `/login`：管理员密码 + TOTP 动态验证码登录。
- `/dashboard`：系统运行总览、关键指标、资源水位、活跃异常（区分 `closed` 与 `actionable_attention`）与实时事件流。
- `/users`：用户列表、会话状态筛选、账号并发与限制批量操作、脱敏主体与钱包详情。
- `/tasks`：全局任务队列监控、多维状态筛选、处理观察与平台确认对照、批量暂停/继续/取消，以及底部待处理题库答案报告复核面板。
- `/finance`：充值订单、不可变账本流水、渠道对账、异常退款审批与人工余额调整（需步进验证与原因审计）。
- `/administrators`：管理员账号创建、角色分配、状态启停与最后一个有效 `owner` 保护。
- `/privacy-requests`：用户个人数据导出与闭户删除申请人工复核。
- `/settings`：系统设置（外观偏好、计费与价格、运行安全并发与请求节奏热调、系统维护与备份状态）。
- 侧栏外部同源入口 `/panel/`：跳转独立题库维护面板（可通过构建环境变量 `VITE_TIKU_PANEL_URL` 覆盖目标地址）。

## 环境要求

- Node.js `22.12+` 与 npm

## 本地开发与检查

日常联调建议在仓库根目录运行 `node scripts/dev.mjs`，脚本会自动拉起管理端 Vite HMR 服务（默认 `http://127.0.0.1:5187`，开启开发环境回环免登录并同域代理 `/admin/api` 与 `/panel/`）。

单独执行依赖安装、类型检查、单元测试、端到端测试与生产构建：

```bash
npm --prefix apps/admin ci
npm --prefix apps/admin run check      # TypeScript 编译检查 + Vitest 单元测试
npm --prefix apps/admin test           # 仅运行 Vitest 单元测试
npm --prefix apps/admin run test:e2e   # 运行 Playwright 浏览器测试
npm --prefix apps/admin run build      # 生成生产静态产物至 apps/admin/dist/
```

共享设计令牌修改后（编辑仓库根下 `design/tokens.json`），在仓库根目录运行 `python3 scripts/generate-design-tokens.py` 即可同步更新 `src/styles/tokens.css`。
