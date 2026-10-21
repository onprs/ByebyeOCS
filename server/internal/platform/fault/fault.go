// Package fault 定义可安全展示的错误，不携带底层连接信息或原始请求。
package fault

import "errors"

type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func New(code, message string) error { return &Error{Code: code, Message: message} }

func Describe(err error) (string, string) {
	var safe *Error
	if errors.As(err, &safe) {
		return safe.Code, safe.Message
	}
	return "internal_error", "操作失败，请检查服务状态"
}
