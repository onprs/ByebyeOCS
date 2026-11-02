package config

import "testing"

func TestLocalAdminAutoLoginRestrictedToDevelopmentLoopback(t *testing.T) {
	cfg := Defaults()
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 1
	cfg.Auth.UserOrigin = "http://127.0.0.1:8090"
	cfg.Auth.AdminOrigin = "http://127.0.0.1:8091"
	cfg.Mode = "development"
	cfg.Auth.CookieSecure = false
	cfg.Auth.LocalAdminAutoLogin = true
	if err := validateRuntime(&cfg); err != nil {
		t.Fatalf("开发回环入口应允许管理员自动登录：%v", err)
	}
	cfg.Mode = "production"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("生产模式允许管理员自动登录")
	}
	cfg.Mode = "development"
	cfg.Auth.CookieSecure = false
	cfg.Auth.AdminOrigin = "https://admin.example.com"
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("非回环入口允许管理员自动登录")
	}
}
