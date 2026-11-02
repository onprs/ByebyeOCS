package config

import (
	"testing"
	"time"
)

func TestLearningRequestPacingBounds(t *testing.T) {
	cfg := Defaults()
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 1
	cfg.Auth.UserOrigin = "http://127.0.0.1:8090"
	cfg.Auth.AdminOrigin = "http://127.0.0.1:8091"
	cfg.Auth.CookieSecure = false
	cfg.Mode = "development"
	cfg.Learning.RequestInterval = 20 * time.Millisecond
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("过快的上游请求间隔被接受")
	}
	cfg.Learning.RequestInterval = 150 * time.Millisecond
	cfg.Learning.ChapterConcurrency = 8
	if err := validateRuntime(&cfg); err != nil {
		t.Fatalf("合法的本地请求节奏被拒绝：%v", err)
	}
	cfg.Learning.ChapterConcurrency = 20
	if err := validateRuntime(&cfg); err == nil {
		t.Fatal("过高的章节并发被接受")
	}
}
