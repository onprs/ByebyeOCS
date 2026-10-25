package config

import "testing"

func profileLearningConfig() Config {
	cfg := Defaults()
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 1
	cfg.Auth.UserOrigin = "https://learn.example.test"
	cfg.Auth.AdminOrigin = "https://admin.example.test"
	cfg.Learning.Enabled = true
	cfg.Learning.AuthorizationReference = "fixture-authorization"
	cfg.Learning.SubjectNamespace = "fixture-namespace"
	cfg.Learning.SubjectProbeFormat = "profile"
	cfg.Learning.SubjectProbeURL = "https://passport2.chaoxing.com/mooc/accountManage"
	cfg.Learning.SubjectTTLHours = 24
	return cfg
}

func TestProfileProbeConfiguration(t *testing.T) {
	cfg := profileLearningConfig()
	if err := validateRuntime(&cfg); err != nil {
		t.Fatal("账户资料探针配置被拒绝:", err)
	}
	cfg.Learning.SubjectTTLHours = 0
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("缺少资料探针会话时长仍被接受")
	}
	cfg = profileLearningConfig()
	cfg.Learning.SubjectTTLHours = 200
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("超长资料探针会话时长仍被接受")
	}
	cfg = profileLearningConfig()
	cfg.Learning.SubjectProbeFormat = "unknown"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("未知探针格式仍被接受")
	}
	cfg = profileLearningConfig()
	cfg.Learning.SubjectProbeURL = ""
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("缺少资料探针地址仍被接受")
	}
}
