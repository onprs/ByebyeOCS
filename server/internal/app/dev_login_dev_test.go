//go:build devfixture

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
)

func devLoginConfig() config.Config {
	return config.Config{Mode: "development", Auth: config.Auth{
		Enabled: true, UserOrigin: "http://127.0.0.1:8090", AdminOrigin: "http://127.0.0.1:8091",
	}}
}

func TestLocalDevLoginVerifierRequiresBuildOptInAndLoopback(t *testing.T) {
	t.Setenv("BYEBYEOCS_DEV_LOGIN", "")
	cfg := devLoginConfig()
	if _, enabled, _ := localDevLoginVerifier(cfg); enabled {
		t.Fatal("未显式启用时加载了本地学习通验证器")
	}
	t.Setenv("BYEBYEOCS_DEV_LOGIN", "1")
	verifier, enabled, err := localDevLoginVerifier(cfg)
	if err != nil || !enabled || verifier == nil {
		t.Fatalf("本地学习通验证器未启用：%v", err)
	}
	subject, err := verifier.Verify(context.Background(), auth.Credentials{School: "本地测试学校", Account: "learner-a", Password: "任意本地密码"})
	if err != nil || subject.Provider != auth.ProviderChaoxing || subject.Subject != "local:learner-a" {
		t.Fatalf("本地学习通验证结果错误：%+v/%v", subject, err)
	}
	if _, err := verifier.Verify(context.Background(), auth.Credentials{Account: "learner-a", Password: "wrong-password"}); err != nil {
		t.Fatalf("本地测试应接受任意非空密码：%v", err)
	}
	if _, err := verifier.Verify(context.Background(), auth.Credentials{Account: "learner-a", Password: ""}); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("空密码未被拒绝：%v", err)
	}
	public := devLoginConfig()
	public.Auth.AdminOrigin = "https://admin.example.com"
	if _, enabled, _ := localDevLoginVerifier(public); enabled {
		t.Fatal("公开入口加载了本地学习通验证器")
	}
	production := devLoginConfig()
	production.Mode = "production"
	if _, enabled, _ := localDevLoginVerifier(production); enabled {
		t.Fatal("生产模式加载了本地学习通验证器")
	}
}
