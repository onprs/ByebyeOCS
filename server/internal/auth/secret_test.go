package auth

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestIssueTokenPrefixesAndDigest(t *testing.T) {
	service, _ := newTestService(t, baseTestOptions(time.Unix(1700000000, 0).UTC()))
	token, digest, err := issueToken(tokenPrefixUserWeb)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, tokenPrefixUserWeb) {
		t.Fatalf("令牌前缀错误：%s", token)
	}
	if len(digest) != 64 || digest != tokenDigest(token) {
		t.Fatal("令牌摘要不是 SHA-256 十六进制")
	}
	if strings.Contains(digest, token) {
		t.Fatal("令牌摘要包含令牌明文")
	}
	if !tokenMatches(token, KindUser, ChannelWeb) {
		t.Fatal("Web 用户令牌未匹配预期端")
	}
	if tokenMatches(token, KindAdmin, ChannelWeb) || tokenMatches(token, KindUser, ChannelApp) {
		t.Fatal("用户 Web 令牌被其他端或通道接受")
	}
	if !tokenMatches(token, KindUser, ChannelWeb) {
		t.Fatal("令牌前缀匹配失败")
	}
	_ = service
}

func TestCSRFTokenDerivation(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, _ := newTestService(t, baseTestOptions(now))
	first := service.csrfToken("session-a")
	if first != service.csrfToken("session-a") {
		t.Fatal("同一会话的 CSRF 令牌不稳定")
	}
	if first == service.csrfToken("session-b") {
		t.Fatal("不同会话生成了相同的 CSRF 令牌")
	}
	if service.csrfDigest("session-a") != tokenDigest(first) {
		t.Fatal("CSRF 摘要与令牌不一致")
	}

	otherOptions := baseTestOptions(now)
	otherOptions.EncryptionKey = bytes.Repeat([]byte{9}, 32)
	other, err := newService(nil, otherOptions)
	if err != nil {
		t.Fatal(err)
	}
	if other.csrfToken("session-a") == first {
		t.Fatal("不同主密钥生成了相同的 CSRF 令牌")
	}
}

func TestSealOpenBindsPurposeAndVersion(t *testing.T) {
	service, _ := newTestService(t, baseTestOptions(time.Unix(1700000000, 0).UTC()))
	plaintext := []byte("platform-session-secret-value")
	ciphertext, version, err := service.seal(purposePlatformSession, []string{"user-1", "identity-1"}, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("默认密钥版本为 %d", version)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("密文包含明文")
	}
	opened, err := service.open(purposePlatformSession, []string{"user-1", "identity-1"}, version, ciphertext)
	if err != nil || !bytes.Equal(opened, plaintext) {
		t.Fatalf("解密失败：%v", err)
	}
	if _, err := service.open(purposePlatformSession, []string{"user-1", "identity-2"}, version, ciphertext); err == nil {
		t.Fatal("关联数据不匹配仍解密成功")
	}
	if _, err := service.open(purposeSubject, []string{"user-1", "identity-1"}, version, ciphertext); err == nil {
		t.Fatal("不同用途标签仍解密成功")
	}
	if _, err := service.open(purposePlatformSession, []string{"user-1", "identity-1"}, version+1, ciphertext); err == nil {
		t.Fatal("密钥版本不匹配仍解密成功")
	} else if code, _ := fault.Describe(err); code != "secret_key_version_unknown" {
		t.Fatalf("密钥版本错误返回 %s", code)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^=
		0xff
	if _, err := service.open(purposePlatformSession, []string{"user-1", "identity-1"}, version, tampered); err == nil {
		t.Fatal("被篡改的密文仍解密成功")
	}
}

func TestParseOriginAndMatching(t *testing.T) {
	parsed, err := parseOrigin("HTTPS://App.Example.com/", "用户端")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.value != "https://App.Example.com" {
		t.Fatalf("Origin 规范化结果异常：%s", parsed.value)
	}
	if !parsed.matches("https://app.example.com") {
		t.Fatal("Origin 主机比较未忽略大小写")
	}
	if parsed.matches("https://app.example.com/path") {
		t.Fatal("带路径的来源被接受")
	}
	for _, invalid := range []string{"", "app.example.com", "ftp://app.example.com", "https://app.example.com/path", "https://user@app.example.com", "https://app.example.com?x=1"} {
		if _, err := parseOrigin(invalid, "用户端"); err == nil {
			t.Fatalf("无效 Origin 被接受：%q", invalid)
		}
	}
}

func TestNormalizeOptionsValidation(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	if _, err := newService(nil, func() Options {
		options := baseTestOptions(now)
		options.EncryptionKey = []byte("short")
		return options
	}()); err == nil {
		t.Fatal("非 32 字节加密密钥被接受")
	}
	if _, err := newService(nil, func() Options {
		options := baseTestOptions(now)
		options.AdminOrigin = options.UserOrigin
		return options
	}()); err == nil {
		t.Fatal("双端 Origin 相同时仍通过校验")
	}
	if _, err := newService(nil, func() Options {
		options := baseTestOptions(now)
		options.CookieSecure = true
		return options
	}()); err == nil {
		t.Fatal("安全 Cookie 与 http Origin 的组合被接受")
	}
	if _, err := newService(nil, func() Options {
		options := baseTestOptions(now)
		options.TOTP.Digits = 7
		return options
	}()); err == nil {
		t.Fatal("无效 TOTP 位数被接受")
	}
}

func TestUnsafeMethod(t *testing.T) {
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		if unsafeMethod(method) {
			t.Fatalf("%s 被判定为写请求", method)
		}
	}
	for _, method := range []string{"POST", "PATCH", "PUT", "DELETE"} {
		if !unsafeMethod(method) {
			t.Fatalf("%s 被判定为安全请求", method)
		}
	}
}
