# 决策 0012：新增学习通账户资料页（Profile）主体探针模式

- **状态**：已采纳并落地

## 背景

系统绑定内部用户与钱包的前提是服务端独立核验不可变的学习通外部主体 ID。初期实现仅支持返回 JSON 结构的主体与会话过期时间探针；经核查实际学习通网页端协议发现：

1. 网页端账户管理页（`passport2.chaoxing.com/mooc/accountManage`）由服务端直接渲染稳定的用户 `id="uid"` 与姓名 `id="messageName"`（与 `i.chaoxing.com/base` 一致），并不提供同时包含稳定 UID 与精确会话过期时间戳的单一公开 JSON 接口；
2. 所属学校名称由独立的 `i.chaoxing.com/base/cacheUserOrg` JSON 接口返回；
3. 客户端或登录响应 Cookie 中的 `_uid` 字段缺乏服务端页面级绑定校验，不能单独作为资产归属依据。

## 决策

在保留原有 `json` 探针模式的基础上，新增并列受支持的 **`profile` 账户资料探针模式**：

1. **严格解析服务端渲染 UID**：向配置的固定 HTTPS `subject_probe_url` 发起携带新登录会话的请求，仅从服务端渲染 HTML 节点提取不可变 UID 与姓名；非 `200` 响应、非预期页面结构或缺失合法 UID 时立即按失败关闭（Fail-Closed）拒绝登录。
2. **学校信息补充与防明文降级**：从 `school_probe_url` 读取学校 JSON 并优先匹配当前会话 `fid`；若该接口返回携带 `ws` Cookie 的 `302` HTTP 重定向，客户端**绝不跟随明文 HTTP 跳转**，仅在原 HTTPS 地址安全重试一次；学校接口暂不可用时允许登录继续完成并将学校留空。
3. **保守内部会话有效期**：通过 `learning.subject_ttl_hours`（合法范围 `1–168` 小时，推荐 `24` 小时）设定保守的平台会话重验周期，到期前迫使用户刷新认证。

## 当前落点与影响

- 实现位于 `server/internal/adapters/chaoxing/` 与 `server/internal/platform/config/runtime.go`，配置校验与重定向防降级均由单元测试完整覆盖。
