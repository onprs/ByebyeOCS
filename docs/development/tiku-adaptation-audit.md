# 主项目与题库 fork 适配与修复验收

本文面向开发维护环境。公开用户的安装与配置分别见主项目和题库的 README。本记录保留上一轮适配验收；后续提供商、缓存与分页更新见[题库性能与维护面板复查](tiku-performance-panel-audit.md)。

## 当前结论

此前确认的正式面板缺失、缓存有效期未贯通、旧兼容入口的密钥回传与访问边界、面板外观不一致均已处理。

正式 PostgreSQL fork 现在提供 `/panel/`，维护面板直接读取正式模型、公开条目、查询、用量、精确缓存和维护操作记录。公开模型、价格版本与缓存策略只在题库面板维护；主管理端只读目录、核对查询/用量与答案报告，并提供面板跳转。钱包、收款与退款仍在主管理端。

题库公开条目发布1天缓存期限，实际 fork 数据库保存约24小时。该期限、是否允许缓存与查询快照绑定，模型条目后来修改或进程恢复不会改变同一内部请求的摘要。旧在途查询继续省略新策略字段，保留原协议恢复。

本轮从主仓库 `92e4778e73d9ad4fc21bb13bc74e7e443e6afe50`、题库子模块 `9060d5f48fa8d033d8529b4527b83dc139dcb47b` 开始修复；本报告对应其上的当前工作区变更。

## 修复内容

| 原审计问题 | 当前行为与验证 |
| --- | --- |
| 正式服务没有面板，旧管理 API 为404 | 正式 `/panel/`、脚本和样式均200；独立 `/panel/api/` 直接访问 PostgreSQL |
| 发布1天，实际保存7天 | 请求明确传递 `cache_policy`；数据库实际期限约24小时，超龄缓存不命中并可写入新答案 |
| 策略修改可能改变恢复请求 | 主库保存策略快照；新查询恢复保持原策略，旧 NULL 快照保持原请求格式，两种情况都有回归断言 |
| 管理访问与密钥边界不明确 | 独立维护账号、维护员/只读角色、Cookie会话、来源与CSRF校验；服务令牌不用于网页登录；响应不返回密钥、哈希或私密文件路径 |
| 旧网页回传完整模型密钥 | 设置遮蔽密钥；同端点/协议编辑留空保留，显式移除生效，更换端点不携带旧密钥 |
| 旧兼容工具可能被当作远程管理入口 | 兼容进程限制回环监听；管理 API 拒绝非本机 Host 和跨站来源；v1 搜索接入保持兼容 |
| 旧面板数据与外观不符合当前项目 | 正式面板包含总览、模型、查询、缓存、操作记录和设置，默认石墨，保留青禾/靛蓝与深浅色，六个手机页面通过浏览器检查 |

正式面板由 [panel 模块](../../services/tiku/internal/platform/panel/server.go)提供，静态资源随 Go 二进制构建。上游 React 页面作为 v1 本地工具保留，正式 PostgreSQL 服务不导入 SQLite。

维护员可以新增、编辑和删除上游模型及密钥，发布、编辑和下架公开条目，维护价格版本/缓存策略与默认项，启停模型、做连通/功能测试、使错误缓存失效；上游模型保存写回题库配置并热加载，密钥只写入私密文件；只读账号只允许查看。功能测试必须单独确认会产生模型用量，正常自动做题流程仍然没有人工提交确认。

模型启停和缓存失效与维护审计在同一事务中提交。测试在调用模型前记录操作，结束后保存结果。维护审计不保存凭据，保留180天并按500行有界清理。

维护账号使用独立 bcrypt 哈希文件，Unix下要求私密普通文件；Cookie使用 HttpOnly、SameSite=Strict，HTTPS入口设置Secure。会话最多256个，8小时到期、退出或重启后失效；登录尝试窗口与校验并发有上限。所有写操作检查准确的 `panel.origin` 和会话CSRF。面板默认关闭，没有默认账号。

## 缓存协议与迁移

主服务 [ForkAnswerRequest](../../server/internal/questions/fork.go) 新增可选字段：

```json
"cache_policy": { "allow": true, "ttl_days": 1 }
```

主服务将该对象写入查询行，恢复按原快照重建请求。NULL 对应此前已创建的请求，不补默认字段。fork 在模型调用前校验1到配置上限的期限，显式策略按 TTL 分开缓存；读取检查实际过期时间和创建时间。`allow=false` 禁止缓存写入，配合 `cache_first` 时明确拒绝；未提供策略的旧客户端继续使用旧格式与全局默认值。

新增迁移：

- 主库：[000023_question_cache_policy.sql](../../server/internal/platform/postgres/migrations/000023_question_cache_policy.sql) 保留；公开目录迁移为 [000024_question_catalogue_mirror.sql](../../server/internal/platform/postgres/migrations/000024_question_catalogue_mirror.sql)，当前24个连续迁移。
- 独立题库：[000004_panel_audit.sql](../../services/tiku/internal/platform/store/migrations/000004_panel_audit.sql) 保留；公开条目为 [000005_offerings.sql](../../services/tiku/internal/platform/store/migrations/000005_offerings.sql)，当时5个连续迁移。

旧迁移保持不变。升级时停止相关服务，显式执行两库迁移并配套更新主服务与 fork，再先启动 fork、后启动主服务。启动仍不自动迁移。维护账号与准确来源配置后才启用面板，完整步骤见[题库 README](../../services/tiku/README.md#维护面板)。

## 验证范围与结果

实际启动带竞态检测的 `cmd/ocs-tiku-fork`，迁移独立临时 PostgreSQL 库，通过主服务真实 HTTP 客户端、认证、账本和调度调用。浏览器直接加载正式服务提供的面板，不代理到模拟管理 API。只有上游模型、学习平台与登录身份采用受控夹具，资金为合成测试资金。

| 检查 | 当前结果 |
| --- | --- |
| 主服务 `vet -tags=integration,tiku_fork ./...` | 通过 |
| 主服务完整竞态与隔离 PostgreSQL 回归 | 通过，包含 `integration,tiku_fork` 全部包 |
| fork 完整 `vet`、竞态与 PostgreSQL 回归 | 通过 |
| 新增缓存策略测试 | 实际1天期限、不同期限隔离、超龄拒绝/重写、禁止缓存、无效期限在模型调用前拒绝均通过 |
| 新增维护接口测试 | 独立登录、服务令牌不能替代面板登录、只读权限、来源/CSRF、真实启停、失效、审计和退出均通过 |
| 新增公开条目测试 | 发布、编辑、默认/可见、价格版本冲突、只读写入拒绝、功能测试确认与价格版本记录均通过 |
| 新增上游模型测试 | 面板新增/更新/删除、密钥 0600 文件、配置文件原子写回、热加载作答、只读拒绝与删除引用保护均通过 |
| 新增认证/密钥测试 | 会话过期、登录限额、私密哈希文件、CLI不覆盖已有文件、旧入口密钥遮蔽及跨站/Host边界通过 |
| 主项目既有管理端测试 | 45项单元测试、12项浏览器测试与构建通过；目录只读并跳转题库面板 |
| v1 React兼容页面 | 本轮构建、lint和7项既有浏览器检查通过 |
| 正式面板真实浏览器回归 | 桌面关键页面与六个手机页面通过；公开条目编辑与价格字段可用；无页面脚本异常或整体横向溢出 |
| 正式主服务/fork二进制与公开配置样例 | 构建、正式CLI配置校验通过 |

跨服务回归继续覆盖13项接口与账本检查，以及五种题型自动提交、提交回执丢失后只独立复核的2项场景。原有模型选择、精确缓存、token、计费和自动做题边界保持回归约束。

浏览器操作额外验证：

- 独立维护账号登录、退出和只读账号登录。
- 设置响应不含上游密钥、服务令牌或私密路径。
- 实际发布/编辑公开条目、确认用量后进行功能测试。
- 查看实际查询详情，使实际缓存失效，并在操作记录中读取该原因。
- 青禾/靛蓝/石墨切换、深色偏好刷新后保留。
- 手机总览、模型与价格、查询、缓存、操作记录和设置全部可加载。
- 只读界面不显示写操作，直接发起写请求仍返回403。

## 维护复现

设置 `BYEBYEOCS_TEST_DATABASE_DSN_FILE` 指向独立测试 PostgreSQL 的私密连接文件，角色需要 `CREATEDB`。测试自行创建和清理临时数据库，不得指向真实业务库。

```bash
go -C server vet -tags=integration,tiku_fork ./...
go -C server test -race -count=1 -p 2 -tags=integration,tiku_fork ./...
go -C services/tiku vet ./...
go -C services/tiku test -race -count=1 ./...
```

定向跨服务与真实浏览器验证在主仓库根目录执行，需要 Node.js 22或更高版本与 Chrome/Chromium，不需要先构建 v1 React 页面：

```bash
BYEBYEOCS_TIKU_PANEL_PROBE_SCRIPT="$PWD/services/tiku/internal/platform/panel/tests/browser.test.mjs" \
  go -C server test -race -count=1 -tags=integration,tiku_fork ./internal/app -run '^TestTikuFork' -v
```

可选 `BYEBYEOCS_TIKU_BROWSER_ARTIFACT_DIR` 保存截图与浏览器报告，`BYEBYEOCS_TIKU_ADAPTATION_REPORT_FILE` 保存正式面板路由与缓存期限事实。本轮维护记录位于 `.local/logs/tiku-repair/`、`.local/reports/tiku-repair-checks.json` 和 `.local/screenshots/tiku-panel-repaired/`，不进入版本管理。

首次全量运行因原隔离测试实例停止而连接失败，恢复该专用实例后重跑数据库检查；新权限测试发现的哈希文件权限遗漏已补齐后重跑。最终结论以恢复后的完整结果为准。

本轮没有调用真实付费模型、真实学习通课程或支付渠道；也不证明生产容量和实机兼容性。临时测试库复用受限测试角色，正式独立数据库/角色要求仍由部署配置落实。
