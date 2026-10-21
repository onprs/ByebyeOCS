# 本轮实现协作契约

环境：开发维护；生产接口、部署说明与本地测试工具分开。用户已授权完整实现，不重复询问已确认技术方案。研究目录原样保留，不读取其凭据和运行数据。

- 正式 Go 模块为 `server/`，路径 `github.com/onprs/ByebyeOCS/server`；Go 命令使用 `go -C server`。
- PostgreSQL 为唯一业务数据库。SQL 全限定 `public.`，ID 用 UUID / API 字符串，金额用整数分、API 十进制字符串；微元同理。时间 UTC RFC3339。
- 用户 API `/api/v1`，管理员 API `/admin/api/v1`。各业务服务提供 `Register(mux *http.ServeMux)` 注册完整路径。两端由入口严格分离。
- 公共 HTTP 支撑位于 `internal/web`，提供 `Actor{ID, Kind, SessionID string; Permissions []string; AuthenticatedAt time.Time}`、`WithActor(ctx, actor)`、`ActorFrom(ctx) (Actor,bool)`、`Require(w,r,kind,permission) (Actor,bool)`；业务代码绝不信任请求 body 的 user_id。
- `web.JSON(w,r,status,value)`、`web.Error(w,r,status,code,message)`、`web.Fail(w,r,err)`、`web.Decode(w,r,target) bool`、`web.DecodeLimit(w,r,target,limit) bool`、`web.NewID() string`、`web.RequestID(ctx) string`、`web.WithRequestID(ctx,id)`、`web.ETag(w,version)`、`web.MatchVersion(r) (int64,error)` 由协调者提供。版本 ETag 为双引号包围 v+十进制版本。
- 错误为 `{error:{code,message,request_id,retryable,scope}}`。使用 `fault.New` 只返回中文安全错误。HTTP 错误码映射：unauthenticated 401，forbidden/csrf_invalid 403，not_found 404，conflict/insufficient_balance 409，version_conflict 412，validation_failed 422，precondition_required 428，rate_limited 429，not_configured/external_disabled/service_unavailable 503，未知错误 500。
- 认证服务拥有 `auth.Service`，至少导出 `Register(*http.ServeMux)`、`Middleware(kind string,next http.Handler) http.Handler`；构造签名与配置见其实际代码。Web 使用 HttpOnly Cookie+CSRF+Origin，App 访问/轮换刷新令牌，管理员独立 Argon2id + TOTP。测试身份仅来自明确的本地测试种子，生产无默认账号。
- `internal/platform/audit.Record(ctx,tx,Event)` 由协调者提供；Event 含 `Actor web.Actor; Action, ResourceType, ResourceID, Reason, RequestID string`。只保存脱敏事实，不放原始请求、密钥或上游响应。
- 各服务直接接收 `*pgxpool.Pool`，明确事务。数据库入口会提供 `Database.Pool()`，业务服务不得改连接配置。
- 数据库迁移编号独占：000003 认证，000004 课程/安排/任务，000005 账本/支付，000006 题库主平台，000007 由协调者预留。不得修改已应用的 000001/000002。
- 各 worker 只编辑分配目录及其独占迁移；不要改公共 web/config/command/app、OpenAPI、根文档、其他 worker 文件。完成后写 `.local/reports/<任务名>.md`，列出构造/导出接口、必要配置、测试及未完成项；不声称实现未验证部分。
- PostgreSQL 集成测试可使用 `internal/testsupport`（协调者提供），不得访问真实业务库。真实支付、平台执行和模型调用默认关闭，不暴露测试成功接口到生产。正式前端不能出现评审工具或假成功控制。
- 管理员正式 React 工程 `apps/admin`，用户正式 Flutter 工程 `apps/client_flutter`；`prototypes/` 继续保留视觉原型。
