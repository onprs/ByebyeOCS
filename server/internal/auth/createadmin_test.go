package auth

import (
	"context"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestCreateAdminRequiresSecretDelivery(t *testing.T) {
	service, _ := newTestService(t, baseTestOptions(time.Unix(1700000000, 0).UTC()))
	err := service.CreateAdmin(context.Background(), "owner", "a-strong-admin-passphrase", "")
	if err == nil {
		t.Fatal("缺少 TOTP 密钥且未配置交付通道时仍创建了管理员")
	}
	if code, _ := fault.Describe(err); code != "totp_secret_required" {
		t.Fatalf("错误码为 %s", code)
	}
}

func TestCreateAdminValidatesInput(t *testing.T) {
	service, _ := newTestService(t, baseTestOptions(time.Unix(1700000000, 0).UTC()))
	if err := service.CreateAdmin(context.Background(), "owner name", "a-strong-admin-passphrase", "GEZDGNBVGY3TQOJQ"); err == nil {
		t.Fatal("包含空格的管理员账号被接受")
	}
	if err := service.CreateAdmin(context.Background(), "owner", "short", "GEZDGNBVGY3TQOJQ"); err == nil {
		t.Fatal("弱密码被接受")
	}
	if err := service.CreateAdmin(context.Background(), "owner", "a-strong-admin-passphrase", "not-base32-@@"); err == nil {
		t.Fatal("无效 TOTP 密钥被接受")
	}
}

func TestValidateAdminUsername(t *testing.T) {
	if err := validateAdminUsername(""); err == nil {
		t.Fatal("空管理员账号被接受")
	}
	if err := validateAdminUsername("运维 管理员"); err == nil {
		t.Fatal("含空白的管理员账号被接受")
	}
	if err := validateAdminUsername("运维管理员"); err != nil {
		t.Fatalf("中文管理员账号被拒绝：%v", err)
	}
}

func TestCapabilitiesDoNotClaimScan(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	disabled, _ := newTestService(t, baseTestOptions(now))
	identity, _ := disabled.capabilities()["identity"].(map[string]any)
	if identity["qr_scan"] != false {
		t.Fatal("未实现扫码却公布了扫码能力")
	}
	if identity["verification"] != "disabled" {
		t.Fatalf("未配置验证器时的能力状态为 %v", identity["verification"])
	}
	methods, _ := identity["methods"].([]string)
	if len(methods) != 0 {
		t.Fatalf("未配置验证器却公布了登录方式：%v", methods)
	}

	options := baseTestOptions(now)
	options.Verifier = staticVerifier{}
	enabled, err := newService(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	identity, _ = enabled.capabilities()["identity"].(map[string]any)
	methods, _ = identity["methods"].([]string)
	if len(methods) != 1 || methods[0] != "password" {
		t.Fatalf("已配置验证器时登录方式错误：%v", methods)
	}
	if identity["qr_scan"] != false {
		t.Fatal("扫码仍不应公布为支持能力")
	}
}

// staticVerifier 只用于能力与构造测试，不发起网络请求。
type staticVerifier struct{}

func (staticVerifier) Verify(context.Context, Credentials) (VerifiedSubject, error) {
	return VerifiedSubject{}, ErrInvalidCredentials
}
