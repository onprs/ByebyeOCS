package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// ProviderFailureKind 是可信验证器可报告的外部平台失败类别。
// 只有下列类别会被映射为可读错误；其他错误一律 503 泛化。
type ProviderFailureKind string

const (
	// ProviderVerificationRequired 表示平台要求持有人完成验证码/二次验证。
	ProviderVerificationRequired ProviderFailureKind = "verification_required"
	// ProviderPermissionDenied 表示平台拒绝该账号或操作，通常需要人工处理。
	ProviderPermissionDenied ProviderFailureKind = "permission_denied"
	// ProviderIdentityExpired 表示平台会话/身份已失效，需要重新验证。
	ProviderIdentityExpired ProviderFailureKind = "identity_expired"
	// ProviderRateLimited 表示平台限流，调用方应按 RetryAfter 退避。
	ProviderRateLimited ProviderFailureKind = "rate_limited"
)

// providerFailureMessages 固定文案，绝不拼接上游响应或任意源数据。
var providerFailureMessages = map[ProviderFailureKind]string{
	ProviderVerificationRequired: "需要完成平台验证后重新登录",
	ProviderPermissionDenied:     "外部平台拒绝了该验证请求，请联系人工支持",
	ProviderIdentityExpired:      "外部平台身份已失效，请重新验证",
	ProviderRateLimited:          "外部平台请求过于频繁，请稍后再试",
}

// ProviderFailure 由 Verifier 返回，表示平台层已知的安全失败类别。
// 它不携带上游响应、凭据或任意可展示文本。
type ProviderFailure struct {
	Kind       ProviderFailureKind
	RetryAfter time.Duration
}

func (failure *ProviderFailure) Error() string {
	if failure == nil {
		return "外部平台验证失败"
	}
	if message, ok := providerFailureMessages[failure.Kind]; ok {
		return message
	}
	return "外部平台验证失败"
}

// NewProviderFailure 构造可信平台失败；RetryAfter 仅对 rate_limited 生效。
func NewProviderFailure(kind ProviderFailureKind, retryAfter time.Duration) error {
	return &ProviderFailure{Kind: kind, RetryAfter: retryAfter}
}

// writeProviderFailure 把已知类别映射为安全 HTTP 错误。
// 返回 false 表示类别未知，由调用方按 503 泛化，不暴露上游细节。
func writeProviderFailure(w http.ResponseWriter, r *http.Request, err error) bool {
	var failure *ProviderFailure
	if !errors.As(err, &failure) || failure == nil {
		return false
	}
	switch failure.Kind {
	case ProviderVerificationRequired:
		web.Error(w, r, http.StatusConflict, "verification_required", providerFailureMessages[failure.Kind])
	case ProviderPermissionDenied:
		web.Error(w, r, http.StatusForbidden, "permission_denied", providerFailureMessages[failure.Kind])
	case ProviderIdentityExpired:
		web.Error(w, r, http.StatusUnauthorized, "identity_expired", providerFailureMessages[failure.Kind])
	case ProviderRateLimited:
		retryAfter := failure.RetryAfter
		if retryAfter <= 0 {
			retryAfter = time.Minute
		}
		if retryAfter > 10*time.Minute {
			retryAfter = 10 * time.Minute
		}
		writeRetryAfter(w, r, retryAfter, providerFailureMessages[failure.Kind])
	default:
		return false
	}
	return true
}
