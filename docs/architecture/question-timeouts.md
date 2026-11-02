# 题库调用的超时与结果归属

本文记录题库调用链上「谁决定超时、谁判定结果」的约定。相关代码在主服务 `server/internal/questions/` 与题库 fork `services/tiku/internal/platform/httpapi/`。

## 问题

早期实现里三层超时互相打架：

| 位置 | 旧值 | 后果 |
| --- | --- | --- |
| 主服务 HTTP 客户端 | `questions.timeout`（30–60 秒） | 到点即取消请求，并据本地 deadline 判定“上游超时/结果未知” |
| fork 上游模型客户端 | `ResponseHeaderTimeout` 固定 85 秒 | 推理模型出首字节前就断言失败，即使配置的截止时间更长 |
| fork 请求截止 | `timeout_seconds` 上限 300 秒 | 长推理无法配置更长 |

更严重的是状态归属：客户端一断开，fork 就把请求记为 `unknown` 并返回 `request_unknown`，主服务把查询写成 `needs_review`。由于同幂等键重试会被拒绝（防止重复计费），**这张卷子从此再也不能自动作答**。超时是本地的判断，却产生了终态的账单与任务事实。

## 现在的约定

1. **题库拥有调用生命周期**。fork 的 `POST /api/v2/answers` 把作答放到后台 goroutine，使用脱离调用方连接的上下文执行；调用方断开只在服务端结束本次写响应，模型调用与结果落库继续完成。后续同幂等键请求读取已保存结果，不再把断连当成未知。
2. **主服务的等待窗口只是耐心**，不是判决。到点返回 `202 fork_pending`（`retryable=true`），查询保持 `pending`，预占保留；同键重试会重新投递并读取结果。
3. **截止时间由题库决定**：`limits.timeout_seconds` 上限从 300 放宽到 900；上游模型客户端的报头超时与整体截止时间一致，不再固定 85 秒。
4. **未知仍然终态**：只有 fork 自己确认“调用已发出且无法得知结果”才返回 `request_unknown`，主服务照旧记 `needs_review`，避免静默重复计费。断连、本地等不到响应都不再产生这个状态。
5. **预算由调用方给定**：单次请求的 `budget.max_output_tokens` 是模型侧主要变量；等待窗口按题库截止时间配置（主服务默认 330 秒，需大于 fork 的 `timeout_seconds`）。

## 验证

维护环境实测（主服务等待窗口临时压到 1 秒）：

- 首次请求：`HTTP 202`，`code=fork_pending`，查询状态 `pending`，无 `needs_review`
- 12 秒后同幂等键重试：`HTTP 200`，`status=answered`，答案正确，用时 32ms
- 全程只发生一次上游模型调用，没有重复计费事实

## 在途查询回收

等待窗口到点后查询保持 `pending`，但没有任何重试时也会终止：主服务每 30 秒扫描一次超过等待上限仍在途的查询，先向题库读取状态再决定归属。

- 题库接口 `GET /internal/v1/requests/{idempotency-key}` 只读状态，不等待、不触发模型调用，返回 `pending` / `completed`（带已保存响应）/ `failed` / `unknown` / `expired`。
- `completed`：用冻结的价格快照与题库响应走正常完成链，结算与缓存写入和普通请求完全一致，**不会再次调用模型**。
- `pending`：仍然在处理，延长在途时间，下一轮继续。
- `failed` / `unknown` / `expired`：按题库给出的错误码转待复核。
- 记录不存在：`pending_record_missing` 转待复核，不重发。
- 单轮最多 20 条，回收抢占使用与重放相同的原子更新，多个实例不会重复处理。

集成测试覆盖两条路径：已完成回收（断言只发生一次模型调用）与记录消失转复核。

## 释放“结果未知”的查询键

`request_unknown` 这类状态无法自动重发：上游可能已经计费，重发会产生第二笔调用。管理端提供受控释放：

```
POST /admin/api/v1/question-queries/{id}/releases
{"reason": "现场确认上游调用失败，允许重新作答"}
```

- 只接受 `request_unknown`、`request_cancelled`、`upstream_timeout`、`pending_record_missing`、`pending_state_unknown`
- 有用量事实（`usage_incomplete` 等）的查询拒绝释放，避免用释放绕过复核
- 释放预占（如有），旧记录改为 `failed / released_unknown_usage`、`billing_state=released`，幂等键追加 `#released-<version>` 保留审计
- 原幂等键因此可用，下一次同键请求创建新记录并重新调用模型
- 写入 `question.query.release` 审计事件，资源键是原查询编号

## 运行观测

`GET /admin/api/v1/question-recovery` 返回回收器计数：运行次数、处理条数、失败次数、最近运行时间与最近错误码。管理端任务页的题库区域展示同一份数据。

## 仍然待做

- 回收器没有暴露处理延迟直方图，只有累计计数。
- 释放操作尚未在前端提供查询列表入口，目前通过管理 API 调用。
