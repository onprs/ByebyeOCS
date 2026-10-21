package auth

import (
	"strings"
	"testing"
	"time"
)

// RFC 6238 附录 B 的 SHA1 测试向量（8 位验证码）。
func TestTOTPRFC6238Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	options := TOTPOptions{Issuer: "ByebyeOCS", Period: 30 * time.Second, Digits: 8, Skew: 0}
	cases := []struct {
		unix int64
		code string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, item := range cases {
		at := time.Unix(item.unix, 0).UTC()
		if got := totpAt(secret, at, options.Period, options.Digits); got != item.code {
			t.Fatalf("时间 %d 的 TOTP 为 %s，期望 %s", item.unix, got, item.code)
		}
		counter, matched := matchTOTP(secret, item.code, at, options)
		if !matched || counter != uint64(item.unix/30) {
			t.Fatalf("时间 %d 的验证码未能匹配计数器", item.unix)
		}
	}
}

func TestTOTPSkewWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1700000000, 0).UTC()
	code := totpAt(secret, now, 30*time.Second, 6)

	strict := TOTPOptions{Period: 30 * time.Second, Digits: 6, Skew: 0}
	if _, matched := matchTOTP(secret, code, now.Add(31*time.Second), strict); matched {
		t.Fatal("无时间容差时接受了下一窗口验证码")
	}
	tolerant := TOTPOptions{Period: 30 * time.Second, Digits: 6, Skew: 1}
	if _, matched := matchTOTP(secret, code, now.Add(31*time.Second), tolerant); !matched {
		t.Fatal("允许一个窗口容差时未接受上一窗口验证码")
	}
	if _, matched := matchTOTP(secret, code, now.Add(3*time.Minute), tolerant); matched {
		t.Fatal("超出容差窗口仍接受了验证码")
	}
}

func TestTOTPRejectsBadFormat(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1700000000, 0).UTC()
	options := TOTPOptions{Period: 30 * time.Second, Digits: 6, Skew: 1}
	for _, code := range []string{"", "12345", "1234567", "abcdef", "12 456"} {
		if _, matched := matchTOTP(secret, code, now, options); matched {
			t.Fatalf("无效验证码 %q 被接受", code)
		}
	}
}

func TestGenerateTOTPSecretAndNormalize(t *testing.T) {
	first, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("两次生成的 TOTP 密钥相同")
	}
	raw, err := decodeTOTPSecret(first)
	if err != nil || len(raw) != totpSecretBytes {
		t.Fatalf("生成的 TOTP 密钥无法解码：%v", err)
	}
	spaced := strings.ToLower(first[:4] + " " + first[4:8] + "-" + first[8:])
	normalized, err := normalizeTOTPSecret(spaced)
	if err != nil || normalized != first {
		t.Fatalf("带空格或短横线的密钥未正确规范化：%v", err)
	}
	if _, err := normalizeTOTPSecret("not-base32-@@"); err == nil {
		t.Fatal("无效 Base32 密钥被接受")
	}
}

func TestTOTPProvisioningURI(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	uri, err := TOTPProvisioningURI("ByebyeOCS", "owner", secret, TOTPOptions{Period: 30 * time.Second, Digits: 6})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"otpauth://totp/", "ByebyeOCS:owner", "secret=" + secret, "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(uri, fragment) {
			t.Fatalf("交付 URI 缺少 %q", fragment)
		}
	}
	if _, err := TOTPProvisioningURI("ByebyeOCS", "", secret, TOTPOptions{}); err == nil {
		t.Fatal("缺少账户名时仍生成了交付 URI")
	}
}
