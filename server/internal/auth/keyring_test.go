package auth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func identityTestKey(seed byte) []byte { return bytes.Repeat([]byte{seed}, 32) }

func TestIdentityKeyDerivationIsIndependentOfEncryptionKey(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	options := baseTestOptions(now)
	options.IdentityKey = identityTestKey(0x42)
	first, err := newService(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	// 仅轮换当前加密密钥：索引与 CSRF 不应改变。
	rotated := options
	rotated.EncryptionKey = identityTestKey(0x77)
	rotated.EncryptionKeyVersion = 2
	rotated.EncryptionKeys = map[int][]byte{1: options.EncryptionKey}
	second, err := newService(nil, rotated)
	if err != nil {
		t.Fatal(err)
	}
	if first.subjectIndex("chaoxing", "ns", "uid-1") != second.subjectIndex("chaoxing", "ns", "uid-1") {
		t.Fatal("主密钥轮换改变了外部身份索引")
	}
	if first.csrfToken("session-id") != second.csrfToken("session-id") {
		t.Fatal("主密钥轮换改变了 CSRF 派生")
	}
	// 独立 IdentityKey 不同则索引不同。
	other := options
	other.IdentityKey = identityTestKey(0x43)
	third, err := newService(nil, other)
	if err != nil {
		t.Fatal(err)
	}
	if first.subjectIndex("chaoxing", "ns", "uid-1") == third.subjectIndex("chaoxing", "ns", "uid-1") {
		t.Fatal("不同 IdentityKey 生成了相同主体索引")
	}
	// 未配置 IdentityKey 时回退到加密密钥，保持旧测试语义。
	legacy := baseTestOptions(now)
	legacyService, err := newService(nil, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(legacyService.identityKey, legacy.EncryptionKey) {
		t.Fatal("未配置 IdentityKey 时未回退到当前加密密钥")
	}
}

func TestKeyringOptionsValidation(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	base := func() Options { return baseTestOptions(now) }

	tooMany := base()
	tooMany.EncryptionKeys = map[int][]byte{}
	for version := 1; version <= maxEncryptionKeys+1; version++ {
		tooMany.EncryptionKeys[version] = identityTestKey(byte(version))
	}
	if _, err := newService(nil, tooMany); err == nil {
		t.Fatal("超过 8 个解密密钥版本被接受")
	}

	zeroVersion := base()
	zeroVersion.EncryptionKeys = map[int][]byte{0: identityTestKey(1)}
	if _, err := newService(nil, zeroVersion); err == nil {
		t.Fatal("版本 0 被接受")
	}

	shortKey := base()
	shortKey.EncryptionKeys = map[int][]byte{2: []byte("short")}
	if _, err := newService(nil, shortKey); err == nil {
		t.Fatal("非 32 字节解密密钥被接受")
	}

	conflict := base()
	conflict.EncryptionKeyVersion = 1
	conflict.EncryptionKeys = map[int][]byte{conflict.EncryptionKeyVersion: identityTestKey(9)}
	if _, err := newService(nil, conflict); err == nil {
		t.Fatal("与当前版本不一致的密钥环条目被接受")
	}

	consistent := base()
	consistent.EncryptionKeyVersion = 1
	consistent.EncryptionKeys = map[int][]byte{consistent.EncryptionKeyVersion: append([]byte(nil), consistent.EncryptionKey...)}
	if _, err := newService(nil, consistent); err != nil {
		t.Fatalf("与当前密钥一致的条目被拒绝：%v", err)
	}

	shortIdentity := base()
	shortIdentity.IdentityKey = []byte("short")
	if _, err := newService(nil, shortIdentity); err == nil {
		t.Fatal("非 32 字节 IdentityKey 被接受")
	}
}

func TestOpenUsesEmbeddedKeyVersion(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	oldKey := identityTestKey(0x11)
	newKey := identityTestKey(0x22)

	writerOptions := baseTestOptions(now)
	writerOptions.EncryptionKey = oldKey
	writer, err := newService(nil, writerOptions)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, version, err := writer.seal(purposePlatformSession, []string{"user", "identity"}, []byte("platform-cookie"))
	if err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("写入版本为 %d", version)
	}

	readerOptions := baseTestOptions(now)
	readerOptions.EncryptionKey = newKey
	readerOptions.EncryptionKeyVersion = 2
	readerOptions.EncryptionKeys = map[int][]byte{1: oldKey}
	reader, err := newService(nil, readerOptions)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := reader.open(purposePlatformSession, []string{"user", "identity"}, version, ciphertext)
	if err != nil || string(opened) != "platform-cookie" {
		t.Fatalf("密钥环未能解密旧版本：%v", err)
	}
	// 新密封只写当前版本。
	fresh, freshVersion, err := reader.seal(purposePlatformSession, []string{"user", "identity"}, []byte("fresh"))
	if err != nil || freshVersion != 2 {
		t.Fatalf("seal 未使用当前版本：%v %d", err, freshVersion)
	}
	if _, err := reader.open(purposePlatformSession, []string{"user", "identity"}, 2, fresh); err != nil {
		t.Fatalf("当前版本密文无法解密：%v", err)
	}

	// 未携带旧密钥时失败关闭，不尝试当前密钥。
	missingOptions := baseTestOptions(now)
	missingOptions.EncryptionKey = newKey
	missingOptions.EncryptionKeyVersion = 2
	missing, err := newService(nil, missingOptions)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missing.open(purposePlatformSession, []string{"user", "identity"}, version, ciphertext); err == nil {
		t.Fatal("缺失旧密钥版本仍解密成功")
	} else if code, _ := fault.Describe(err); code != "secret_key_version_unknown" {
		t.Fatalf("未知版本返回 %s", code)
	}
}

func TestProviderFailureMapping(t *testing.T) {
	for _, item := range []struct {
		kind       ProviderFailureKind
		status     int
		code       string
		retryAfter bool
	}{
		{ProviderVerificationRequired, http.StatusConflict, "verification_required", false},
		{ProviderPermissionDenied, http.StatusForbidden, "permission_denied", false},
		{ProviderIdentityExpired, http.StatusUnauthorized, "identity_expired", false},
		{ProviderRateLimited, http.StatusTooManyRequests, "rate_limited", true},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/chaoxing/challenges/x/verify", nil)
		if !writeProviderFailure(recorder, request, NewProviderFailure(item.kind, 0)) {
			t.Fatalf("%s 未被识别", item.kind)
		}
		if recorder.Code != item.status {
			t.Fatalf("%s 映射为 %d，期望 %d", item.kind, recorder.Code, item.status)
		}
		body := recorder.Body.String()
		if item.retryAfter && recorder.Header().Get("Retry-After") == "" {
			t.Fatalf("%s 缺少 Retry-After", item.kind)
		}
		if !bytes.Contains([]byte(body), []byte(item.code)) {
			t.Fatalf("%s 响应缺少错误码：%s", item.kind, body)
		}
	}

	// 未知类别与任意错误不被映射，避免上游文本泄漏。
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/chaoxing/challenges/x/verify", nil)
	if writeProviderFailure(recorder, request, NewProviderFailure("upstream_says_hello", 0)) {
		t.Fatal("未知平台失败类别被映射")
	}
	if writeProviderFailure(recorder, request, &ProviderFailure{}) {
		t.Fatal("空类别被映射")
	}
	if writeProviderFailure(recorder, request, ErrInvalidCredentials) {
		t.Fatal("凭据拒绝被误认为平台失败")
	}

	// 错误文本固定，不包含上游细节。
	failure := &ProviderFailure{Kind: ProviderPermissionDenied}
	if bytes.Contains([]byte(failure.Error()), []byte("upstream")) {
		t.Fatal("平台失败文本包含任意上游内容")
	}
	if NewProviderFailure("custom", 0).Error() != "外部平台验证失败" {
		t.Fatal("未知类别的错误文本不固定")
	}
}
