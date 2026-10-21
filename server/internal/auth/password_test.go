package auth

import (
	"strings"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestPasswordHashAndVerify(t *testing.T) {
	hash, err := hashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("密码哈希不是预期的 Argon2id PHC 格式：%s", hash)
	}
	if strings.Contains(hash, "correct-horse-battery") {
		t.Fatal("密码哈希包含明文密码")
	}
	matched, err := verifyPassword(hash, "correct-horse-battery")
	if err != nil || !matched {
		t.Fatalf("正确密码未通过校验：%v", err)
	}
	matched, err = verifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Fatal("错误密码通过校验")
	}
}

func TestPasswordHashUsesRandomSalt(t *testing.T) {
	first, err := hashPassword("same-password-value")
	if err != nil {
		t.Fatal(err)
	}
	second, err := hashPassword("same-password-value")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("相同密码生成了完全相同的哈希，盐值未随机化")
	}
}

func TestPasswordHashRejectsMalformedPHC(t *testing.T) {
	for _, encoded := range []string{
		"",
		"plain-text",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=1,t=3,p=2$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=2$!!!$aGFzaA",
	} {
		if _, err := verifyPassword(encoded, "whatever"); err == nil {
			t.Fatalf("无效密码哈希被接受：%q", encoded)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if err := validatePassword("short-pass"); err == nil {
		t.Fatal("过短密码被接受")
	} else if code, _ := fault.Describe(err); code != "password_weak" {
		t.Fatalf("过短密码返回错误码 %s", code)
	}
	if err := validatePassword("aaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("字符种类过少的密码被接受")
	}
	if err := validatePassword("           "); err == nil {
		t.Fatal("全空白密码被接受")
	}
	if err := validatePassword("a-strong-admin-passphrase-2026"); err != nil {
		t.Fatalf("强密码被拒绝：%v", err)
	}
}
