package chaoxing

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// requestFailure 保留停止意图与可恢复网络错误，日志不包含请求地址或会话内容。
func requestFailure(err error, detail string) error {
	if errors.Is(err, context.Canceled) {
		return learning.Fail(learning.FailureStopped, "请求已停止")
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return &learning.Failure{Class: learning.FailureTransient, Code: "network_timeout", Detail: "平台请求超时，等待自动恢复"}
	}
	return &learning.Failure{Class: learning.FailureTransient, Code: "network_interrupted", Detail: detail}
}
func platformRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 && seconds < int64((365*24*time.Hour)/time.Second) {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 30 * time.Second
}
