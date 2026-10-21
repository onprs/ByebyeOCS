package learning

import (
	"errors"
	"fmt"
)

// Error 是可安全返回给客户端的业务错误，不包含上游原始内容或凭据。
type Error struct {
	Status    int
	Code      string
	Message   string
	Retryable bool
	Scope     string
}

func (e *Error) Error() string { return e.Message }

func apiErr(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message, Scope: "request"}
}

func (e *Error) withScope(scope string) *Error {
	e.Scope = scope
	return e
}

func (e *Error) retryable() *Error {
	e.Retryable = true
	return e
}

var (
	errUnauthenticated    = apiErr(401, "unauthenticated", "请先登录后再操作")
	errForbidden          = apiErr(403, "forbidden", "当前身份无权执行此操作")
	errNotFound           = apiErr(404, "not_found", "请求的资源不存在")
	errConflict           = apiErr(409, "conflict", "当前状态不允许此操作")
	errVersionConflict    = apiErr(412, "version_conflict", "数据已更新，请重新加载后再操作")
	errPrecondition       = apiErr(428, "precondition_required", "修改前必须提供当前版本")
	errValidation         = apiErr(422, "validation_failed", "请求内容不符合要求")
	errRateLimited        = apiErr(429, "rate_limited", "请求过于频繁，请稍后再试")
	errExternalDisabled   = apiErr(503, "external_disabled", "外部平台能力尚未启用")
	errServiceUnavailable = apiErr(503, "service_unavailable", "服务暂不可用，请稍后再试")
	errIdempotency        = apiErr(409, "idempotency_conflict", "相同幂等键已用于不同请求")
	errLeaseLost          = apiErr(409, "lease_lost", "执行租约已失效，结果未写入")
	errInsufficient       = apiErr(409, "insufficient_balance", "可用余额不足以预占本次任务")
)

func validation(message string) *Error {
	return apiErr(422, "validation_failed", message)
}

func conflict(message string) *Error {
	return apiErr(409, "conflict", message)
}

// ErrorCode 返回安全错误码；未知错误统一为 internal_error。
func ErrorCode(err error) (int, string, string, bool, string) {
	var target *Error
	if errors.As(err, &target) {
		return target.Status, target.Code, target.Message, target.Retryable, target.Scope
	}
	return 500, "internal_error", "操作失败，请检查服务状态", false, "service"
}

func wrapf(status int, code, format string, args ...any) *Error {
	return apiErr(status, code, fmt.Sprintf(format, args...))
}
