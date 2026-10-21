package app

import (
	"context"
	"errors"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
)

func TestConfiguredVerifierReachesCredentialValidation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Auth.Enabled = true
	dependencies, err := defaultDependencies(cfg)
	if err != nil || dependencies.Verifier != nil {
		t.Fatal("未启用平台时不应安装登录验证器")
	}
	cfg.Learning.Enabled = true
	cfg.Learning.AuthorizationReference = "synthetic-construction-test"
	cfg.Learning.SubjectNamespace = "fixture-namespace"
	cfg.Learning.SubjectProbeURL = "https://passport2.chaoxing.com/fixture-subject"
	cfg.Learning.SubjectPath = "subject"
	cfg.Learning.SessionExpiryPath = "expires_at"
	dependencies, err = defaultDependencies(cfg)
	if err != nil || dependencies.Verifier == nil {
		t.Fatal("已配置验证器未装配", err)
	}
	// 空凭据在发出任何网络请求前被拒绝，可区分已启用与遗失内部授权开关。
	_, err = dependencies.Verifier.Verify(context.Background(), auth.Credentials{})
	var failure *auth.ProviderFailure
	if !errors.As(err, &failure) || failure.Kind != auth.ProviderVerificationRequired {
		t.Fatal("平台验证器仍被内部能力开关阻断")
	}
}
