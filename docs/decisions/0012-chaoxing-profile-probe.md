# 0012 学习通账户资料页主体探针

## 背景

用户登录要求服务端确认稳定的外部主体。既有验证器只接受返回稳定主体与会话到期的 JSON 信任探针。2026-10-25 核查实际账户会话后确认：

- `passport2.chaoxing.com/mooc/accountManage` 由服务端渲染 `id="uid"` 与 `id="messageName"`，UID 在同一账户的 `i.chaoxing.com/base` 页面保持一致；
- `i.chaoxing.com/base/cacheUserOrg` 返回 `site[].schoolname` 与 `fid`，可用于学校信息；
- 网页端没有同时返回稳定主体与会话到期的 JSON 资料端点。Cookie 中的 `_uid` 仍不作为主体依据。

## 决定

新增 `profile` 主体探针模式，与 JSON 探针并存：

- 只读取固定 HTTPS 账户资料地址，失败关闭；
- 从服务端渲染页面提取 UID；缺少 UID 或格式异常时拒绝登录，不读取 Cookie 自述 UID；
- 学校从 `school_probe_url` 的 JSON 读取，优先匹配会话 `fid`；学校不可用时保留登录，学校留空；
- 会话到期使用配置的保守 TTL，允许 1–168 小时；当前生产使用 24 小时；
- 稳定命名空间与授权依据仍为必填；执行、支付与收费开关不受影响。

## 取舍与风险

- 页面结构变化会让登录失败关闭，不会误用其他字段。探针测试覆盖缺少 UID、非 HTML、非 200、学校不可用和越界 TTL。
- TTL 是内部策略，不是平台到期时间；更短的内部会话迫使重新登录，避免超出平台真实会话。
- 学校接口不可用时登录仍完成，学校显示为空，后续平台读取可再次尝试。
- 该模式不改变“平台确认、执行观察和媒体证据分离”的边界，也不启用真实学习执行。

## 证据

- 登录与读取证据：`chaoxing-progress-research/data/native-http/login-summary.json`（2026-10-20，账号密码登录成功，未要求验证码或二次验证）。
- 账户页 UID 与 `base` 页 UID 一致；学校 JSON 结构经实际会话读取确认，未记录账号、UID、Cookie 或学校原值。
