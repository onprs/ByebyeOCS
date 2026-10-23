package config

import "testing"

func TestPaymentURLValidationMatchesChannelAndKeepsProxyRouting(t *testing.T) {
	cfg := Defaults()
	cfg.Database.DSN = testDSN
	cfg.Mode = "production"
	cfg.Auth.Enabled = true
	cfg.Auth.UserOrigin = "https://learn.example.com"
	cfg.Auth.AdminOrigin = "https://admin.example.com"
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.EncryptionKey[0] = 1
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 2
	cfg.Payments.Enabled = true
	cfg.Payments.MerchantID = "fixture-merchant"
	cfg.Payments.Key = string([]byte{1, 2, 3, 4})
	cfg.Payments.NotifyURL = "https://callback.example.com/custom-public-path"
	cfg.Payments.ReturnURL = "https://learn.example.com/wallet"
	cfg.Operations.RequireFreshBackup = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("自管代理的独立公网路径被拒绝：%v", err)
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Payments.NotifyURL += "?source=wallet" },
		func(c *Config) { c.Payments.ReturnURL += "?tab=wallet" },
	} {
		copy := cfg
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Fatal("渠道不支持的地址通过配置检查，到启动时才失败")
		}
	}
}
