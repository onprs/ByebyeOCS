package questions

import (
	"errors"
	"net/http"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// QueryError 是题库查询的可分类错误。
//
// Message 只包含平台生成的中文说明，不包含 fork 原始响应、上游密钥或请求原文。
// 后台任务可按 Code 判断结果，不应依赖 Message 文本。
type QueryError struct {
	Code      string
	Message   string
	Status    int
	Retryable bool
}

func (e *QueryError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// AsQueryError 取回 Answer 返回的可分类错误。
func AsQueryError(err error) (*QueryError, bool) {
	var target *QueryError
	if errors.As(err, &target) && target != nil {
		return target, true
	}
	return nil, false
}

func newQueryError(code, message string, status int, retryable bool) *QueryError {
	return &QueryError{Code: code, Message: message, Status: status, Retryable: retryable}
}

func queryStatusForCode(code string) int {
	switch code {
	case "validation_failed", "invalid_amount":
		return http.StatusUnprocessableEntity
	case "not_found":
		return http.StatusNotFound
	case "conflict", "idempotency_conflict", "insufficient_balance":
		return http.StatusConflict
	case "rate_limited":
		return http.StatusTooManyRequests
	case "not_configured", "service_unavailable", "external_disabled", "provider_unavailable",
		"usage_incomplete", "backup_unavailable", "storage_pressure", "recovery_required":
		return http.StatusServiceUnavailable
	default:
		return http.StatusServiceUnavailable
	}
}

// queryErrorFromFault 只透传平台 fault 的安全文本；未知错误一律回退为 503。
func queryErrorFromFault(err error, fallback string) *QueryError {
	code, message := fault.Describe(err)
	if code == "internal_error" {
		return newQueryError("service_unavailable", fallback, http.StatusServiceUnavailable, true)
	}
	status := queryStatusForCode(code)
	if message == "" {
		message = fallback
	}
	return newQueryError(code, message, status, status == http.StatusServiceUnavailable || status == http.StatusTooManyRequests)
}

func invalidUserError() *QueryError {
	return newQueryError("invalid_user", "用户编号无效", http.StatusBadRequest, false)
}

func idempotencyKeyError() *QueryError {
	return newQueryError("idempotency_required", "缺少有效的 Idempotency-Key", http.StatusBadRequest, false)
}

func idempotencyConflictError() *QueryError {
	return newQueryError("idempotency_conflict", "相同 Idempotency-Key 对应不同请求", http.StatusConflict, false)
}

func requestInProgressError() *QueryError {
	return newQueryError("request_in_progress", "相同请求仍在处理中，请稍后重试", http.StatusConflict, true)
}

// storedQueryError 把已保存的失败查询映射为稳定错误。未知码不回显内部细节。
func storedQueryError(code string) *QueryError {
	switch code {
	case "model_disabled":
		return newQueryError(code, "该模型已下架，请重新选择", http.StatusConflict, false)
	case "vision_unsupported", "budget_exceeded", "invalid_question":
		return newQueryError(code, "题库请求无法处理", http.StatusUnprocessableEntity, false)
	case "upstream_timeout":
		return newQueryError(code, "题库服务超时，结果待核对", http.StatusGatewayTimeout, false)
	case "upstream_rate_limited":
		return newQueryError(code, "题库服务限流，请稍后重试", http.StatusTooManyRequests, true)
	case "insufficient_balance":
		return newQueryError(code, "余额不足，未发起模型调用", http.StatusConflict, false)
	case "request_unknown":
		return newQueryError(code, "题库上游结果未知，已保留待复核事实", http.StatusConflict, false)
	default:
		return newQueryError("service_unavailable", "题库查询失败", http.StatusServiceUnavailable, true)
	}
}

// forkQueryError 使用固定文案，避免把 fork 或上游原始响应回给调用方。
func forkQueryError(err error) *QueryError {
	var forkErr *ForkError
	if errors.As(err, &forkErr) {
		switch safeForkCode(forkErr.Code) {
		case "model_disabled":
			return newQueryError(forkErr.Code, "该模型已下架，请重新选择", http.StatusConflict, false)
		case "vision_unsupported", "budget_exceeded", "invalid_question", "invalid_mode", "invalid_scope":
			return newQueryError(forkErr.Code, "题库请求无法处理", http.StatusUnprocessableEntity, false)
		case "upstream_timeout":
			return newQueryError(forkErr.Code, "题库服务超时，结果未知；不会自动重试", http.StatusGatewayTimeout, false)
		case "upstream_rate_limited":
			return newQueryError(forkErr.Code, "题库服务限流，请稍后重试", http.StatusTooManyRequests, true)
		case "request_unknown":
			return newQueryError(forkErr.Code, "题库上游结果未知，已保留待复核事实", http.StatusConflict, false)
		}
	}
	return newQueryError("service_unavailable", "题库服务不可用", http.StatusServiceUnavailable, true)
}

func reserveQueryError(code string) *QueryError {
	switch code {
	case "insufficient_balance":
		return newQueryError(code, "余额不足，未发起模型调用", http.StatusConflict, false)
	case "charging_setting_unavailable":
		return newQueryError(code, "收费开关状态不可用，未发起付费调用", http.StatusServiceUnavailable, true)
	default:
		return newQueryError("service_unavailable", "无法完成题库预占，未发起模型调用", http.StatusServiceUnavailable, true)
	}
}

// writeQueryError 是 HTTP 层唯一的查询错误映射；保留原有状态码和错误码。
func writeQueryError(w http.ResponseWriter, r *http.Request, err error) {
	queryErr, ok := AsQueryError(err)
	if !ok {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "题库查询失败")
		return
	}
	if queryErr.Code == "request_in_progress" {
		id := web.RequestID(r.Context())
		if id == "" {
			id = web.NewRequestID()
			r = r.WithContext(web.WithRequestID(r.Context(), id))
		}
		web.JSON(w, r, queryErr.Status, map[string]any{"error": map[string]any{
			"code": queryErr.Code, "message": queryErr.Message, "request_id": id,
			"retryable": true, "scope": "request",
		}})
		return
	}
	web.Error(w, r, queryErr.Status, queryErr.Code, queryErr.Message)
}
