//go:build integration

package auth

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func handlersFor(t *testing.T, service *Service) (http.Handler, http.Handler) {
	t.Helper()
	mux := http.NewServeMux()
	service.Register(mux)
	return service.Middleware(KindUser, mux), service.Middleware(KindAdmin, mux)
}

// loginUserWith 通过指定处理器完成一次用户登录，便于在同一数据库上比较不同密钥配置。
func (harness *harness) loginUserWith(handler http.Handler) apiResponse {
	harness.t.Helper()
	create := harness.do(handler, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(harness.t, create, http.StatusCreated)
	decoded := create.json(harness.t)
	id, _ := decoded["id"].(string)
	binding := create.cookie(challengeCookie)
	if binding == "" {
		binding, _ = decoded["binding_token"].(string)
	}
	return harness.do(handler, http.MethodPost, "/api/v1/auth/chaoxing/challenges/"+id+"/verify",
		map[string]any{"school": testSchool, "account": testAccount, "password": testUserPassword},
		withOrigin("http://user.test"), withCookie(challengeCookie, binding))
}

func (harness *harness) insertPlatformCipher(id, userID, identityID string, version int, ciphertext []byte, createdAt time.Time, status string) {
	harness.t.Helper()
	if _, err := harness.pool.Exec(context.Background(), `INSERT INTO public.platform_sessions
		(id, user_id, external_identity_id, key_version, ciphertext, expires_at, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)`,
		id, userID, identityID, version, ciphertext, createdAt.Add(time.Hour), status, createdAt); err != nil {
		harness.t.Fatal(err)
	}
}

func TestIntegrationMasterKeyRotationPreservesIdentity(t *testing.T) {
	h := newHarness(t, func(options *Options) {
		options.IdentityKey = identityTestKey(0x42)
		options.ReencryptBatchSize = 200
	})
	adminSecret := h.createRoleAdmin("owner", RoleOwner)
	firstLogin := h.loginUserWith(h.user)
	requireStatus(t, firstLogin, http.StatusOK)
	firstUser, _ := firstLogin.json(t)["user"].(map[string]any)
	userID, _ := firstUser["id"].(string)
	if !web.ValidID(userID) {
		t.Fatalf("登录未返回有效用户编号：%v", firstUser)
	}
	ctx := context.Background()
	var identityID, subjectIndex string
	if err := h.pool.QueryRow(ctx, `SELECT id::text, subject_index FROM public.external_identities`).
		Scan(&identityID, &subjectIndex); err != nil {
		t.Fatal(err)
	}

	// 轮换主密钥到版本 2，保留版本 1 用于解密。
	options := baseTestOptions(h.clock.Now())
	options.Now = h.clock.Now
	options.Verifier = h.verifier
	options.IdentityKey = identityTestKey(0x42)
	options.EncryptionKey = identityTestKey(0x77)
	options.EncryptionKeyVersion = 2
	options.EncryptionKeys = map[int][]byte{1: testEncryptionKey()}
	options.ReencryptBatchSize = 200
	rotated, err := New(h.pool, options)
	if err != nil {
		t.Fatal(err)
	}
	rotatedUser, rotatedAdmin := handlersFor(t, rotated)

	secondLogin := h.loginUserWith(rotatedUser)
	requireStatus(t, secondLogin, http.StatusOK)
	secondUser, _ := secondLogin.json(t)["user"].(map[string]any)
	if secondUser["id"] != userID {
		t.Fatalf("主密钥轮换后同一 UID 绑定到新用户：%v / %v", secondUser["id"], userID)
	}
	if h.count(`SELECT count(*) FROM public.users`) != 1 {
		t.Fatal("主密钥轮换创建了额外内部用户")
	}
	var rotatedIndex string
	if err := h.pool.QueryRow(ctx, `SELECT subject_index FROM public.external_identities`).Scan(&rotatedIndex); err != nil {
		t.Fatal(err)
	}
	if rotatedIndex != subjectIndex {
		t.Fatal("主体索引随主密钥轮换变化")
	}

	// 旧版本平台会话仍可解密；新登录写入版本 2。
	var oldVersion int
	var oldCipher []byte
	if err := h.pool.QueryRow(ctx, `SELECT key_version, ciphertext FROM public.platform_sessions
		WHERE key_version = 1 ORDER BY created_at ASC, id LIMIT 1`).Scan(&oldVersion, &oldCipher); err != nil {
		t.Fatal(err)
	}
	if oldVersion != 1 {
		t.Fatalf("旧平台会话版本为 %d", oldVersion)
	}
	plaintext, err := rotated.open(purposePlatformSession, []string{userID, identityID}, oldVersion, oldCipher)
	if err != nil || string(plaintext) != testPlatformBody {
		t.Fatalf("旧密文无法通过 keyring 解密：%v", err)
	}
	if _, err := rotated.PlatformSession(ctx, userID); err != nil {
		t.Fatalf("最新平台会话解密失败：%v", err)
	}
	if h.count(`SELECT count(*) FROM public.platform_sessions WHERE key_version = 2`) != 1 {
		t.Fatal("新登录未写入当前密钥版本")
	}

	// 重封装全部密钥材料到当前版本，主体索引与用户不变。
	result, err := rotated.ReencryptSecrets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalIdentities != 1 || result.PlatformSessions != 1 || result.AdminTOTP != 1 {
		t.Fatalf("重封装统计异常：%+v", result)
	}
	if h.count(`SELECT count(*) FROM public.external_identities WHERE subject_key_version <> 2`) != 0 ||
		h.count(`SELECT count(*) FROM public.platform_sessions WHERE key_version <> 2`) != 0 ||
		h.count(`SELECT count(*) FROM public.admin_users WHERE totp_secret_key_version <> 2`) != 0 {
		t.Fatal("重封装后仍存在旧版本密文")
	}
	var finalIndex string
	if err := h.pool.QueryRow(ctx, `SELECT subject_index FROM public.external_identities`).Scan(&finalIndex); err != nil {
		t.Fatal(err)
	}
	if finalIndex != subjectIndex {
		t.Fatal("重封装修改了主体索引")
	}
	if _, err := rotated.PlatformSession(ctx, userID); err != nil {
		t.Fatalf("重封装后平台会话不可用：%v", err)
	}
	again, err := rotated.ReencryptSecrets(ctx)
	if err != nil || again.ExternalIdentities+again.PlatformSessions+again.AdminTOTP != 0 {
		t.Fatalf("重复重封装非幂等：%+v %v", again, err)
	}

	// 重封装后的管理员 TOTP 仍可用于登录。
	h.clock.Advance(30 * time.Second)
	adminLogin := h.do(rotatedAdmin, http.MethodPost, "/admin/api/v1/auth/login", map[string]any{
		"username": "owner", "password": testAdminPassword, "totp_code": h.totpCode(adminSecret),
	}, withOrigin("http://admin.test"))
	requireStatus(t, adminLogin, http.StatusOK)

	// 制造“成功一批后遇到未知版本”的中断：旧版本行与已迁移行都必须可解。
	legacyID := "00000000-0000-4000-8000-000000000001"
	unknownID := "00000000-0000-4000-8000-000000000002"
	remainingID := "00000000-0000-4000-8000-000000000003"
	legacyCipher, legacyVersion, err := h.service.seal(purposePlatformSession, []string{userID, identityID}, []byte("legacy-a"))
	if err != nil {
		t.Fatal(err)
	}
	remainingCipher, remainingVersion, err := h.service.seal(purposePlatformSession, []string{userID, identityID}, []byte("legacy-b"))
	if err != nil {
		t.Fatal(err)
	}
	if legacyVersion != 1 || remainingVersion != 1 {
		t.Fatal("测试密文未使用旧版本")
	}
	now := h.clock.Now()
	h.insertPlatformCipher(legacyID, userID, identityID, legacyVersion, legacyCipher, now, "active")
	h.insertPlatformCipher(unknownID, userID, identityID, 9, bytes.Repeat([]byte{0xab}, 48), now, "revoked")
	h.insertPlatformCipher(remainingID, userID, identityID, remainingVersion, remainingCipher, now.Add(time.Second), "active")
	rotated.options.ReencryptBatchSize = 1
	_, err = rotated.ReencryptSecrets(ctx)
	if err == nil {
		t.Fatal("未知密钥版本未导致失败关闭")
	}
	if code, _ := fault.Describe(err); code != "secret_key_version_unknown" {
		t.Fatalf("未知版本返回 %s", code)
	}
	// 第一批已迁移到版本 2，仍可解。
	var migratedVersion int
	var migratedCipher []byte
	if err := h.pool.QueryRow(ctx, `SELECT key_version, ciphertext FROM public.platform_sessions WHERE id = $1`, legacyID).
		Scan(&migratedVersion, &migratedCipher); err != nil {
		t.Fatal(err)
	}
	if migratedVersion != 2 {
		t.Fatalf("中断前已提交批次未被保留：版本 %d", migratedVersion)
	}
	if plain, err := rotated.open(purposePlatformSession, []string{userID, identityID}, migratedVersion, migratedCipher); err != nil || string(plain) != "legacy-a" {
		t.Fatalf("已迁移密文不可解：%v", err)
	}
	// 尚未处理的行保持旧版本且可解，未知版本行保持原状。
	var remainingStoredVersion int
	var remainingStoredCipher []byte
	if err := h.pool.QueryRow(ctx, `SELECT key_version, ciphertext FROM public.platform_sessions WHERE id = $1`, remainingID).
		Scan(&remainingStoredVersion, &remainingStoredCipher); err != nil {
		t.Fatal(err)
	}
	if remainingStoredVersion != 1 {
		t.Fatalf("中断后剩余行版本为 %d，期望 1", remainingStoredVersion)
	}
	if plain, err := rotated.open(purposePlatformSession, []string{userID, identityID}, remainingStoredVersion, remainingStoredCipher); err != nil || string(plain) != "legacy-b" {
		t.Fatalf("中断后旧版本行不可解：%v", err)
	}
	if h.count(`SELECT count(*) FROM public.platform_sessions WHERE id = $1 AND key_version = 9`, unknownID) != 1 {
		t.Fatal("未知版本行被意外修改")
	}
}

func TestIntegrationProviderFailureResponses(t *testing.T) {
	h := newHarness(t, nil)
	cases := []struct {
		kind       ProviderFailureKind
		status     int
		code       string
		retryAfter bool
	}{
		{ProviderVerificationRequired, http.StatusConflict, "verification_required", false},
		{ProviderPermissionDenied, http.StatusForbidden, "permission_denied", false},
		{ProviderIdentityExpired, http.StatusUnauthorized, "identity_expired", false},
		{ProviderRateLimited, http.StatusTooManyRequests, "rate_limited", true},
	}
	for _, item := range cases {
		h.verifier.setError(NewProviderFailure(item.kind, 30*time.Second))
		challengeID, binding := h.createChallenge(ChannelWeb)
		response := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
		requireStatus(t, response, item.status)
		if h.errorCode(response) != item.code {
			t.Fatalf("%s 返回错误码 %s", item.kind, h.errorCode(response))
		}
		if item.retryAfter && response.header.Get("Retry-After") == "" {
			t.Fatalf("%s 缺少 Retry-After", item.kind)
		}
	}
	// 未知错误继续 503 泛化，不回显上游文本。
	h.verifier.setError(errors.New("upstream failure with cookie=secret"))
	challengeID, binding := h.createChallenge(ChannelWeb)
	unknown := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
	requireStatus(t, unknown, http.StatusServiceUnavailable)
	if strings.Contains(string(unknown.body), "cookie") || strings.Contains(string(unknown.body), "upstream") {
		t.Fatalf("未知错误回显了上游内容：%s", string(unknown.body))
	}
	// 平台失败不累计本地持久化登录失败。
	if h.count(`SELECT count(*) FROM public.auth_rate_limits`) != 0 {
		t.Fatal("平台失败被计入本地登录限流")
	}
}
