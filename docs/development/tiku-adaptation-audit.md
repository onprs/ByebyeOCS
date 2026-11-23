# 独立题库适配与跨服务联调规范

> **适用范围**：开发维护环境，说明主服务（`server/`）与独立题库子模块（`services/tiku/`）之间的协议适配、公开条目唯一绑定、完整选项保留及跨服务集成验证方法。

## 1. 公开目录唯一绑定与价格快照收敛

在完成 [决策 0011](../decisions/0011-tiku-panel-catalogue.md) 收敛后，模型与目录数据的所有权边界如下：

1. **一对一绑定与软删除释放（题库迁移 05, 07, 08）**：在题库库 `tiku.offerings` 中，每个上游配置模型在同一时刻最多只能绑定一个未删除的公开条目（重复发布或改绑至已占用模型会被数据库约束与业务层双重拒绝）。维护员删除公开条目时执行软删除，立即释放该上游模型的绑定槽位，同时完整保留历史 `offering_price_versions` 与既有查询账务引用。
2. **主平台目录镜像同步（主库迁移 24）**：主平台定期及按需通过 Bearer 服务令牌请求 `GET /internal/v1/offerings` 更新本地 `question_offerings` 只读镜像；主平台自身不提供任何创建/修改模型或题库价格的接口。
3. **查询级冻结不可变快照**：主平台发起查题前，将目标条目的 UUID、`provider_ref`、当前价格版本与四项微元费率（普通输入、输出、缓存读取、缓存创建）、有效输出预算 `max_output_tokens` 及请求级 `cache_policy` 冻结写入 `question_queries`。即使题库面板随后调价或下架条目，已在途查询的幂等重放与最终结算仍严格依据首次冻结快照执行。

## 2. 选项全文保留与精确缓存兼容

- **防止英文与布尔答案截断**：针对选项内容本身为英文缩写或单词（如 `HTTP`、`WHO`、`true`）的单选题与判断题，题库 v2 解析器完整保留 `<answer>` 标签内的选项全文（v1 `/api/search` 兼容接口仍维持原字母格式）。
- **规范化版本隔离（`/choice-text-v2`）**：v2 精确缓存键自动附加 `/choice-text-v2` 规范化后缀，并纳入作答模型与视觉转述模型的端点、密钥指纹及有效输出预算，确保旧版或不同连接配置的缓存不会污染新查询；仅修改模型展示名称不会导致已有精确缓存失效。

## 3. 跨服务联调与真实面板浏览器回归

在配置好隔离测试 PostgreSQL 连接文件 `BYEBYEOCS_TEST_DATABASE_DSN_FILE`（测试角色需具备 `CREATEDB` 权限）后，可在仓库根目录执行跨服务全链路联调测试：

```bash
# 1. 双模块静态检查与跨服务 PostgreSQL 集成测试
go -C server vet -tags=integration,tiku_fork ./...
BYEBYEOCS_TEST_DATABASE_DSN_FILE="$PWD/.local/test.dsn" \
  go -C server test -race -count=1 -p 2 -tags=integration,tiku_fork ./...

BYEBYEOCS_TEST_DATABASE_DSN_FILE="$PWD/.local/test.dsn" \
  go -C services/tiku test -race -count=1 ./...
```

若本机已安装 Node.js `22+` 与 Chrome/Chromium，还可挂载独立题库面板的真实浏览器探针脚本执行端到端回归（无需预先构建上游 v1 React 兼容目录）：

```bash
BYEBYEOCS_TEST_DATABASE_DSN_FILE="$PWD/.local/test.dsn" \
BYEBYEOCS_TIKU_PANEL_PROBE_SCRIPT="$PWD/services/tiku/internal/platform/panel/tests/browser.test.mjs" \
  go -C server test -race -count=1 -tags=integration,tiku_fork ./internal/app -run '^TestTikuFork' -v
```

该联调测试会在进程内真实拉起 `ocs-tiku-fork` 与主服务 HTTP 实例，完整验证面板独立登录、CSRF/Origin 防护、只读角色拦截、提供商与上游模型热保存、公开条目发布、五类题型自动交卷、交卷回执丢失后只读复核、错误缓存报告失效及复式账本微元结算。
