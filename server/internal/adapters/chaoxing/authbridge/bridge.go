package authbridge

import (
	"context"
	"errors"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/adapters/chaoxing"
	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// CredentialVerifier 是 AuthVerifier 的最小验证依赖；*chaoxing.Verifier 满足。
type CredentialVerifier interface {
	Verify(ctx context.Context, credentials auth.Credentials) (auth.VerifiedSubject, error)
}

// AuthVerifier 把 chaoxing 的安全失败分类映射为认证服务可识别的 ProviderFailure。
// 它不携带上游响应、页面内容或任意可展示文本。
type AuthVerifier struct {
	Verifier CredentialVerifier
}

var errProviderUnavailable = errors.New("外部平台验证失败")

func (v AuthVerifier) Verify(ctx context.Context, credentials auth.Credentials) (auth.VerifiedSubject, error) {
	if v.Verifier == nil {
		return auth.VerifiedSubject{}, errProviderUnavailable
	}
	subject, err := v.Verifier.Verify(ctx, credentials)
	if err == nil {
		return subject, nil
	}
	return auth.VerifiedSubject{}, mapProviderError(err)
}

// mapProviderError 只映射已确认的安全类别：
//   - 凭据拒绝原样保留 auth.ErrInvalidCredentials（认证层累计失败次数）；
//   - 验证码/二次验证、403/权限、身份失效、429 限流映射为 ProviderFailure；
//   - 其他错误统一泛化为 503，绝不传播上游文本。
func mapProviderError(err error) error {
	if errors.Is(err, auth.ErrInvalidCredentials) {
		return auth.ErrInvalidCredentials
	}
	var failure *learning.Failure
	if errors.As(err, &failure) && failure != nil {
		switch failure.Class {
		case learning.FailureVerificationNeeded:
			return auth.NewProviderFailure(auth.ProviderVerificationRequired, 0)
		case learning.FailureForbidden:
			return auth.NewProviderFailure(auth.ProviderPermissionDenied, 0)
		case learning.FailureIdentityInvalid:
			return auth.NewProviderFailure(auth.ProviderIdentityExpired, 0)
		case learning.FailureRateLimited:
			return auth.NewProviderFailure(auth.ProviderRateLimited, failure.RetryAfter)
		}
	}
	return errProviderUnavailable
}

// AccountIdentityFromAuth 把可信验证结果转换为学习业务绑定入参。
// verifiedAt 必须是本次可信验证完成时间；为零时由 BindAccount 记录绑定时刻。
// 调用方负责提供 IdentityFingerprint；真实主体不会写入学习业务表。
func AccountIdentityFromAuth(subject auth.VerifiedSubject, verifiedAt time.Time) learning.AccountIdentity {
	return learning.AccountIdentity{
		Provider: subject.Provider, Namespace: subject.Namespace, Subject: subject.Subject,
		School: subject.School, DisplayName: subject.DisplayName, VerifiedAt: verifiedAt.UTC(),
	}
}

var _ auth.Verifier = AuthVerifier{}
var _ auth.Verifier = (*chaoxing.Verifier)(nil)
var _ learning.SessionResolver = (*auth.Service)(nil)
