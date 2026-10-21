package authbridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

type fakeCredentialVerifier struct {
	subject auth.VerifiedSubject
	err     error
}

func (f fakeCredentialVerifier) Verify(ctx context.Context, credentials auth.Credentials) (auth.VerifiedSubject, error) {
	return f.subject, f.err
}

func testCredentials() auth.Credentials {
	return auth.Credentials{School: "1", Account: "account", Password: "password"}
}

func TestAuthVerifierMapsKnownProviderFailures(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		kind       auth.ProviderFailureKind
		retryAfter time.Duration
	}{
		{"验证码", learning.Fail(learning.FailureVerificationNeeded, "平台要求验证"), auth.ProviderVerificationRequired, 0},
		{"403", learning.Fail(learning.FailureForbidden, "平台拒绝访问"), auth.ProviderPermissionDenied, 0},
		{"身份失效", learning.Fail(learning.FailureIdentityInvalid, "会话失效"), auth.ProviderIdentityExpired, 0},
		{"429", &learning.Failure{Class: learning.FailureRateLimited, RetryAfter: 42 * time.Second}, auth.ProviderRateLimited, 42 * time.Second},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			verifier := AuthVerifier{Verifier: fakeCredentialVerifier{err: test.err}}
			_, err := verifier.Verify(context.Background(), testCredentials())
			var providerFailure *auth.ProviderFailure
			if !errors.As(err, &providerFailure) {
				t.Fatalf("错误未映射为 ProviderFailure: %v", err)
			}
			if providerFailure.Kind != test.kind {
				t.Fatalf("失败类别错误: %s != %s", providerFailure.Kind, test.kind)
			}
			if providerFailure.RetryAfter != test.retryAfter {
				t.Fatalf("限流退避未保留: %v != %v", providerFailure.RetryAfter, test.retryAfter)
			}
		})
	}
}

func TestAuthVerifierPreservesInvalidCredentials(t *testing.T) {
	verifier := AuthVerifier{Verifier: fakeCredentialVerifier{err: auth.ErrInvalidCredentials}}
	_, err := verifier.Verify(context.Background(), testCredentials())
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("凭据拒绝未保留: %v", err)
	}
	var providerFailure *auth.ProviderFailure
	if errors.As(err, &providerFailure) {
		t.Fatal("凭据拒绝被错误映射为 ProviderFailure")
	}
}

func TestAuthVerifierUnknownFailureIsGeneric(t *testing.T) {
	for _, input := range []error{
		errors.New("上游敏感文本：cookie=secret"),
		learning.Fail(learning.FailureCapabilityMissing, "上游敏感文本"),
		learning.Fail(learning.FailureTransient, "上游敏感文本"),
	} {
		verifier := AuthVerifier{Verifier: fakeCredentialVerifier{err: input}}
		_, err := verifier.Verify(context.Background(), testCredentials())
		if err == nil || strings.Contains(err.Error(), "敏感") || strings.Contains(err.Error(), "cookie") {
			t.Fatalf("未知错误传播了上游文本: %v", err)
		}
		var providerFailure *auth.ProviderFailure
		if errors.As(err, &providerFailure) {
			t.Fatal("未知错误被映射为已知 ProviderFailure")
		}
	}
}

func TestAuthVerifierSuccessAndMissingVerifier(t *testing.T) {
	expected := auth.VerifiedSubject{Provider: "chaoxing", Namespace: "ns", Subject: "stable",
		DisplayName: "学生", School: "学校", Session: []byte("session"), ExpiresAt: time.Now().Add(time.Hour)}
	verifier := AuthVerifier{Verifier: fakeCredentialVerifier{subject: expected}}
	subject, err := verifier.Verify(context.Background(), testCredentials())
	if err != nil || subject.Subject != "stable" || string(subject.Session) != "session" {
		t.Fatalf("成功验证结果被改变: %+v %v", subject, err)
	}
	empty := AuthVerifier{}
	if _, err := empty.Verify(context.Background(), testCredentials()); err == nil ||
		strings.Contains(err.Error(), "上游") {
		t.Fatalf("缺少验证器未安全失败: %v", err)
	}
}
