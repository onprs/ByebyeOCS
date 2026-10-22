package config

import "testing"

func TestHTTPExecutionAuthorizationWithoutBrowser(t *testing.T) {
	cfg := Defaults()
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 1
	cfg.Auth.UserOrigin = "https://learn.example.test"
	cfg.Auth.AdminOrigin = "https://admin.example.test"
	cfg.Learning.Enabled = true
	cfg.Learning.AuthorizationReference = "fixture-platform-authorization"
	cfg.Learning.SubjectNamespace = "fixture-namespace"
	cfg.Learning.SubjectProbeURL = "https://passport2.chaoxing.com/fixture-subject"
	cfg.Learning.SubjectPath = "subject"
	cfg.Learning.SessionExpiryPath = "expires_at"
	cfg.Learning.ExecutionEnabled = true
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("缺少独立执行验证依据仍允许执行")
	}
	cfg.Learning.ExecutionEvidenceReference = "fixture-http-protocol-evidence"
	if err := validateRuntime(&cfg); err != nil {
		t.Fatal("原生请求执行仍要求浏览器", err)
	}
	cfg.Mode = "development"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("开发模式允许了真实学习执行")
	}
}
