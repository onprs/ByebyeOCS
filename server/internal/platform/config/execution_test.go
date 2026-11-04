package config

import (
	"testing"
	"time"
)

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

func TestDevelopmentExecutionRequiresExplicitLoopbackAuthorization(t *testing.T) {
	cfg := Defaults()
	cfg.Mode = "development"
	cfg.Auth.Enabled = true
	cfg.Auth.CookieSecure = false
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 1
	cfg.Auth.UserOrigin = "http://127.0.0.1:8090"
	cfg.Auth.AdminOrigin = "http://127.0.0.1:8091"
	cfg.Learning.Enabled = true
	cfg.Learning.AuthorizationReference = "fixture-platform-authorization"
	cfg.Learning.SubjectNamespace = "fixture-namespace"
	cfg.Learning.SubjectProbeURL = "https://passport2.chaoxing.com/fixture-subject"
	cfg.Learning.SubjectPath = "subject"
	cfg.Learning.SessionExpiryPath = "expires_at"
	cfg.Learning.ExecutionEnabled = true
	cfg.Learning.ExecutionEvidenceReference = "fixture-http-protocol-evidence"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("未显式授权的开发执行被接受")
	}
	cfg.Learning.DevelopmentExecutionAuthorized = true
	if err := validateRuntime(&cfg); err != nil {
		t.Fatalf("回环开发执行授权被拒绝：%v", err)
	}
	cfg.Auth.AdminOrigin = "https://admin.example.test"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("非回环入口的开发执行授权被接受")
	}
}

// 自适应提速下限不能超过请求间隔，也不能低于 20 毫秒。
func TestLearningRequestIntervalFloorValidation(t *testing.T) {
	base := func() Config {
		cfg := Defaults()
		cfg.Learning.RequestInterval = 150 * time.Millisecond
		return cfg
	}
	cfg := base()
	cfg.Learning.RequestIntervalFloor = 10 * time.Millisecond
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("低于 20 毫秒的下限被接受")
	}
	cfg = base()
	cfg.Learning.RequestIntervalFloor = 200 * time.Millisecond
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("超过请求间隔的下限被接受")
	}
	cfg = base()
	cfg.Learning.RequestIntervalFloor = 30 * time.Millisecond
	if err := validateRuntime(&cfg); err != nil {
		t.Fatalf("合法下限被拒绝：%v", err)
	}
}
