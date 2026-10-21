//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const (
	testAdminPassword = "correct-admin-passphrase"
	testUserPassword  = "correct-user-password"
	testSchool        = "示例学校"
	testAccount       = "student01"
	testNamespace     = "chaoxing-session-v1"
	testSubject       = "uid-000000001"
	testDisplayName   = "合成学生"
	testPlatformBody  = "cookie=platform-session-secret-value"
)

// fakeVerifier 模拟外部平台适配器，不发起任何真实请求。
type fakeVerifier struct {
	mutex       sync.Mutex
	clock       *adjustableClock
	calls       int
	forcedError error
	subject     string
}

func (verifier *fakeVerifier) setSubject(value string) {
	verifier.mutex.Lock()
	defer verifier.mutex.Unlock()
	verifier.subject = value
}

func (verifier *fakeVerifier) setError(err error) {
	verifier.mutex.Lock()
	defer verifier.mutex.Unlock()
	verifier.forcedError = err
}

func (verifier *fakeVerifier) Verify(_ context.Context, credentials Credentials) (VerifiedSubject, error) {
	verifier.mutex.Lock()
	defer verifier.mutex.Unlock()
	verifier.calls++
	if verifier.forcedError != nil {
		return VerifiedSubject{}, verifier.forcedError
	}
	if credentials.Password != testUserPassword {
		return VerifiedSubject{}, ErrInvalidCredentials
	}
	subject := verifier.subject
	if subject == "" {
		subject = testSubject
	}
	return VerifiedSubject{
		Provider:    ProviderChaoxing,
		Namespace:   testNamespace,
		Subject:     subject,
		DisplayName: testDisplayName,
		School:      credentials.School,
		Session:     []byte(testPlatformBody),
		ExpiresAt:   verifier.clock.Now().Add(time.Hour),
	}, nil
}

type harness struct {
	t        *testing.T
	pool     *pgxpool.Pool
	service  *Service
	clock    *adjustableClock
	verifier *fakeVerifier
	user     http.Handler
	admin    http.Handler
}

func newHarness(t *testing.T, mutate func(*Options)) *harness {
	t.Helper()
	pool := testsupport.NewPool(t)
	clock := newClock(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	options := baseTestOptions(clock.Now())
	options.Now = clock.Now
	verifier := &fakeVerifier{clock: clock, subject: testSubject}
	options.Verifier = verifier
	if mutate != nil {
		mutate(&options)
	}
	service, err := New(pool, options)
	if err != nil {
		t.Fatalf("构造集成测试认证服务失败：%v", err)
	}
	mux := http.NewServeMux()
	service.Register(mux)
	return &harness{
		t: t, pool: pool, service: service, clock: clock, verifier: verifier,
		user: service.Middleware(KindUser, mux), admin: service.Middleware(KindAdmin, mux),
	}
}

type apiResponse struct {
	status int
	header http.Header
	body   []byte
}

func (response apiResponse) json(t *testing.T) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(response.body, &decoded); err != nil {
		t.Fatalf("响应不是 JSON 对象：%s", string(response.body))
	}
	return decoded
}

func (response apiResponse) cookie(name string) string {
	for _, raw := range response.header.Values("Set-Cookie") {
		header := http.Header{"Set-Cookie": {raw}}
		response := &http.Response{Header: header}
		for _, cookie := range response.Cookies() {
			if cookie.Name == name {
				return cookie.Value
			}
		}
	}
	return ""
}

func requireStatus(t *testing.T, response apiResponse, want int) {
	t.Helper()
	if response.status != want {
		t.Fatalf("HTTP 状态为 %d，期望 %d；响应：%s", response.status, want, string(response.body))
	}
}

type requestOption func(*http.Request)

func withCookie(name, value string) requestOption {
	return func(request *http.Request) { request.AddCookie(&http.Cookie{Name: name, Value: value}) }
}
func withBearer(token string) requestOption {
	return func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+token) }
}
func withOrigin(origin string) requestOption {
	return func(request *http.Request) { request.Header.Set("Origin", origin) }
}
func withCSRF(token string) requestOption {
	return func(request *http.Request) { request.Header.Set(csrfHeader, token) }
}
func withIfMatch(version string) requestOption {
	return func(request *http.Request) { request.Header.Set("If-Match", version) }
}
func withHeader(name, value string) requestOption {
	return func(request *http.Request) { request.Header.Set(name, value) }
}

func (harness *harness) do(handler http.Handler, method, path string, body any, options ...requestOption) apiResponse {
	harness.t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		encoded, err := json.Marshal(body)
		if err != nil {
			harness.t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request := httptest.NewRequest(method, path, reader)
	request = request.WithContext(web.WithRequestID(request.Context(), web.NewRequestID()))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for _, option := range options {
		option(request)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return apiResponse{status: recorder.Code, header: recorder.Result().Header, body: recorder.Body.Bytes()}
}

func (harness *harness) adminLogout(cookie, csrf string) apiResponse {
	return harness.do(harness.admin, http.MethodPost, "/admin/api/v1/auth/logout", map[string]any{},
		withCookie(adminSessionCookie, cookie), withOrigin("http://admin.test"), withCSRF(csrf))
}

func (harness *harness) totpCode(secret string) string {
	harness.t.Helper()
	decoded, err := decodeTOTPSecret(secret)
	if err != nil {
		harness.t.Fatal(err)
	}
	return totpAt(decoded, harness.clock.Now(), harness.service.options.TOTP.Period, harness.service.options.TOTP.Digits)
}

func (harness *harness) adminLogin(username, password, code string) apiResponse {
	return harness.do(harness.admin, http.MethodPost, "/admin/api/v1/auth/login", map[string]any{
		"username": username, "password": password, "totp_code": code,
	}, withOrigin("http://admin.test"))
}

// adminSession 建立管理员并完成登录，返回会话 Cookie 与 CSRF。
func (harness *harness) adminSession(secret string) (string, string) {
	harness.t.Helper()
	response := harness.adminLogin("owner", testAdminPassword, harness.totpCode(secret))
	requireStatus(harness.t, response, http.StatusOK)
	cookie := response.cookie(adminSessionCookie)
	csrf, _ := response.json(harness.t)["csrf_token"].(string)
	if cookie == "" || csrf == "" {
		harness.t.Fatal("管理员登录未返回会话 Cookie 或 CSRF")
	}
	return cookie, csrf
}

func (harness *harness) createChallenge(client string) (string, string) {
	harness.t.Helper()
	response := harness.do(harness.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": client}, withOrigin("http://user.test"))
	requireStatus(harness.t, response, http.StatusCreated)
	decoded := response.json(harness.t)
	id, _ := decoded["id"].(string)
	if id == "" {
		harness.t.Fatal("登录挑战缺少编号")
	}
	binding := response.cookie(challengeCookie)
	if binding == "" {
		if value, ok := decoded["binding_token"].(string); ok {
			binding = value
		}
	}
	if binding == "" {
		harness.t.Fatal("登录挑战缺少客户端绑定值")
	}
	return id, binding
}

func (harness *harness) verifyChallenge(id, binding, password, client string) apiResponse {
	harness.t.Helper()
	options := []requestOption{withOrigin("http://user.test"), withHeader(challengeHeader, binding)}
	if client == ChannelWeb {
		options = append(options, withCookie(challengeCookie, binding))
	}
	return harness.do(harness.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges/"+id+"/verify",
		map[string]any{"school": testSchool, "account": testAccount, "password": password}, options...)
}

// userSession 完成 Web 用户登录，返回会话 Cookie 与 CSRF。
func (harness *harness) userSession() (string, string) {
	harness.t.Helper()
	id, binding := harness.createChallenge(ChannelWeb)
	response := harness.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
	requireStatus(harness.t, response, http.StatusOK)
	cookie := response.cookie(userSessionCookie)
	csrf, _ := response.json(harness.t)["csrf_token"].(string)
	if cookie == "" || csrf == "" {
		harness.t.Fatal("用户登录未返回会话 Cookie 或 CSRF")
	}
	return cookie, csrf
}

func (harness *harness) count(query string, args ...any) int {
	harness.t.Helper()
	var value int
	if err := harness.pool.QueryRow(context.Background(), query, args...).Scan(&value); err != nil {
		harness.t.Fatalf("查询计数失败：%v", err)
	}
	return value
}

func (harness *harness) errorCode(response apiResponse) string {
	harness.t.Helper()
	decoded := response.json(harness.t)
	envelope, _ := decoded["error"].(map[string]any)
	code, _ := envelope["code"].(string)
	return code
}

func TestIntegrationAdminLifecycle(t *testing.T) {
	harness := newHarness(t, nil)
	ctx := context.Background()
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.service.CreateAdmin(ctx, "owner", testAdminPassword, secret); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.CreateAdmin(ctx, "owner", testAdminPassword, secret); err == nil {
		t.Fatal("重复管理员账号被接受")
	} else if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("重复账号返回错误码 %s", code)
	}
	if harness.count("SELECT count(*) FROM public.admin_users") != 1 {
		t.Fatal("管理员账号数量异常")
	}
	// 密码以 Argon2id PHC 保存，TOTP 只保存密文。
	var passwordHash string
	var totpCiphertext []byte
	if err := harness.pool.QueryRow(ctx, `SELECT password_hash, totp_secret_ciphertext FROM public.admin_users`).
		Scan(&passwordHash, &totpCiphertext); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(passwordHash, "$argon2id$") {
		t.Fatal("管理员密码未使用 Argon2id")
	}
	if bytes.Contains(totpCiphertext, []byte(secret)) {
		t.Fatal("TOTP 密钥以明文形式保存")
	}

	wrongPassword := harness.adminLogin("owner", "wrong-admin-password", harness.totpCode(secret))
	requireStatus(t, wrongPassword, http.StatusUnauthorized)
	wrongCode := harness.adminLogin("owner", testAdminPassword, "000000")
	requireStatus(t, wrongCode, http.StatusUnauthorized)
	harness.clock.Advance(30 * time.Second)
	cookie, csrf := harness.adminSession(secret)

	me := harness.do(harness.admin, http.MethodGet, "/admin/api/v1/me", nil, withCookie(adminSessionCookie, cookie))
	requireStatus(t, me, http.StatusOK)
	decoded := me.json(t)
	if decoded["username"] != "owner" {
		t.Fatalf("/me 返回的管理员不正确：%v", decoded)
	}
	permissions, _ := decoded["permissions"].([]any)
	if len(permissions) != 1 || permissions[0] != PermissionOwner {
		t.Fatalf("管理员权限不是固定 owner 权限：%v", permissions)
	}
	for _, leaked := range []string{"totp_ciphertext", "password_hash", "totp_secret"} {
		if _, ok := decoded[leaked]; ok {
			t.Fatalf("/me 返回了不应暴露的字段 %s", leaked)
		}
	}

	sessionStatus := harness.do(harness.admin, http.MethodGet, "/admin/api/v1/auth/session", nil,
		withCookie(adminSessionCookie, cookie))
	requireStatus(t, sessionStatus, http.StatusOK)
	statusDecoded := sessionStatus.json(t)
	if statusDecoded["authenticated"] != true || statusDecoded["csrf_token"] != csrf {
		t.Fatalf("会话状态未返回可用的 CSRF：%v", statusDecoded)
	}
	capabilities, _ := statusDecoded["capabilities"].(map[string]any)
	identity, _ := capabilities["identity"].(map[string]any)
	if identity["qr_scan"] != false {
		t.Fatal("能力接口错误地公布了扫码支持")
	}

	preferences := harness.do(harness.admin, http.MethodGet, "/admin/api/v1/me/preferences", nil,
		withCookie(adminSessionCookie, cookie))
	requireStatus(t, preferences, http.StatusOK)
	prefsDecoded := preferences.json(t)
	if prefsDecoded["color_scheme"] != "graphite" || prefsDecoded["theme_mode"] != "system" {
		t.Fatalf("管理员默认偏好不是石墨/system：%v", prefsDecoded)
	}
	if preferences.header.Get("ETag") != `"v1"` {
		t.Fatalf("偏好 ETag 为 %s", preferences.header.Get("ETag"))
	}
	missingPrecondition := harness.do(harness.admin, http.MethodPatch, "/admin/api/v1/me/preferences",
		map[string]any{"color_scheme": "grove"}, withCookie(adminSessionCookie, cookie),
		withOrigin("http://admin.test"), withCSRF(csrf))
	requireStatus(t, missingPrecondition, http.StatusPreconditionRequired)
	updated := harness.do(harness.admin, http.MethodPatch, "/admin/api/v1/me/preferences",
		map[string]any{"color_scheme": "grove", "notifications": map[string]any{"wallet_changes": false}},
		withCookie(adminSessionCookie, cookie), withOrigin("http://admin.test"), withCSRF(csrf), withIfMatch(`"v1"`))
	requireStatus(t, updated, http.StatusOK)
	if updated.json(t)["version"] != float64(2) || updated.header.Get("ETag") != `"v2"` {
		t.Fatalf("偏好版本未递增：%v %s", updated.json(t), updated.header.Get("ETag"))
	}
	stale := harness.do(harness.admin, http.MethodPatch, "/admin/api/v1/me/preferences",
		map[string]any{"theme_mode": "dark"}, withCookie(adminSessionCookie, cookie),
		withOrigin("http://admin.test"), withCSRF(csrf), withIfMatch(`"v1"`))
	requireStatus(t, stale, http.StatusPreconditionFailed)
	if harness.errorCode(stale) != "version_conflict" {
		t.Fatalf("过期版本返回 %s", harness.errorCode(stale))
	}
	invalid := harness.do(harness.admin, http.MethodPatch, "/admin/api/v1/me/preferences",
		map[string]any{"color_scheme": "neon"}, withCookie(adminSessionCookie, cookie),
		withOrigin("http://admin.test"), withCSRF(csrf), withIfMatch(`"v2"`))
	requireStatus(t, invalid, http.StatusUnprocessableEntity)

	crossEnd := harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie))
	requireStatus(t, crossEnd, http.StatusUnauthorized)

	// 步进验证需要新的时间窗口，避免同一验证码被防重放规则拒绝。
	harness.clock.Advance(30 * time.Second)
	stepUp := harness.do(harness.admin, http.MethodPost, "/admin/api/v1/auth/step-up",
		map[string]any{"password": testAdminPassword, "totp_code": harness.totpCode(secret)},
		withCookie(adminSessionCookie, cookie), withOrigin("http://admin.test"), withCSRF(csrf))
	requireStatus(t, stepUp, http.StatusOK)

	logout := harness.adminLogout(cookie, csrf)
	requireStatus(t, logout, http.StatusOK)
	afterLogout := harness.do(harness.admin, http.MethodGet, "/admin/api/v1/me", nil, withCookie(adminSessionCookie, cookie))
	requireStatus(t, afterLogout, http.StatusUnauthorized)
	if harness.count("SELECT count(*) FROM public.audit_events WHERE action = 'admin.login'") != 1 {
		t.Fatal("管理员登录未写入审计")
	}
}

func TestIntegrationAdminRateLimit(t *testing.T) {
	harness := newHarness(t, func(options *Options) {
		options.RateLimit = RateLimitOptions{MaxFailures: 3, Window: 10 * time.Minute, BlockFor: 5 * time.Minute}
	})
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.service.CreateAdmin(context.Background(), "owner", testAdminPassword, secret); err != nil {
		t.Fatal(err)
	}
	requireStatus(t, harness.adminLogin("owner", "wrong-password-1", "123456"), http.StatusUnauthorized)
	requireStatus(t, harness.adminLogin("owner", "wrong-password-2", "123456"), http.StatusUnauthorized)
	third := harness.adminLogin("owner", "wrong-password-3", "123456")
	requireStatus(t, third, http.StatusTooManyRequests)
	if third.header.Get("Retry-After") == "" {
		t.Fatal("限流响应缺少 Retry-After")
	}
	blocked := harness.adminLogin("owner", testAdminPassword, harness.totpCode(secret))
	requireStatus(t, blocked, http.StatusTooManyRequests)
	if harness.count("SELECT count(*) FROM public.auth_rate_limits WHERE scope = 'admin_login'") != 1 {
		t.Fatal("登录失败未持久化计入限流")
	}
	harness.clock.Advance(6 * time.Minute)
	harness.clock.Advance(30 * time.Second)
	recovered := harness.adminLogin("owner", testAdminPassword, harness.totpCode(secret))
	requireStatus(t, recovered, http.StatusOK)
	if harness.count("SELECT count(*) FROM public.auth_rate_limits WHERE scope = 'admin_login'") != 0 {
		t.Fatal("成功登录后未清除限流记录")
	}
}

func TestIntegrationUserLoginAndSession(t *testing.T) {
	harness := newHarness(t, nil)
	ctx := context.Background()

	id, binding := harness.createChallenge(ChannelWeb)
	statusWithoutBinding := harness.do(harness.user, http.MethodGet,
		"/api/v1/auth/chaoxing/challenges/"+id, nil, withOrigin("http://user.test"))
	requireStatus(t, statusWithoutBinding, http.StatusNotFound)

	invalid := harness.verifyChallenge(id, binding, "wrong-user-password", ChannelWeb)
	requireStatus(t, invalid, http.StatusUnauthorized)
	if harness.errorCode(invalid) != "unauthenticated" {
		t.Fatalf("凭据失败返回 %s", harness.errorCode(invalid))
	}
	// 挑战在失败后仍可使用剩余额度。
	success := harness.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
	requireStatus(t, success, http.StatusOK)
	cookie := success.cookie(userSessionCookie)
	csrf, _ := success.json(t)["csrf_token"].(string)
	if cookie == "" || csrf == "" {
		t.Fatal("用户登录未返回会话 Cookie 或 CSRF")
	}
	verifiedAt := success.json(t)["user"].(map[string]any)
	if verifiedAt["display_name"] != testDisplayName || verifiedAt["school"] != testSchool {
		t.Fatalf("登录响应缺少安全展示身份：%v", verifiedAt)
	}
	// 挑战单次使用。
	reused := harness.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
	requireStatus(t, reused, http.StatusConflict)

	me := harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie))
	requireStatus(t, me, http.StatusOK)
	raw := string(me.body)
	for _, forbidden := range []string{"subject_index", "namespace", testSubject, testPlatformBody, "ciphertext"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("/me 返回了不应暴露的字段 %q：%s", forbidden, raw)
		}
	}
	meDecoded := me.json(t)
	platform, _ := meDecoded["platform"].(map[string]any)
	if platform["state"] != "active" {
		t.Fatalf("平台会话状态错误：%v", platform)
	}
	userID, _ := meDecoded["id"].(string)
	if !web.ValidID(userID) {
		t.Fatalf("用户编号无效：%q", userID)
	}
	platformSecret, err := harness.service.PlatformSession(ctx, userID)
	if err != nil {
		t.Fatalf("平台会话解密失败：%v", err)
	}
	if string(platformSecret) != testPlatformBody {
		t.Fatal("平台会话解密结果与验证器返回不一致")
	}

	// 会话只保存摘要；平台材料与主体只保存密文。
	var tokenHash string
	if err := harness.pool.QueryRow(ctx, `SELECT token_hash FROM public.client_sessions WHERE user_id = $1`, userID).
		Scan(&tokenHash); err != nil {
		t.Fatal(err)
	}
	if tokenHash != tokenDigest(cookie) || strings.Contains(tokenHash, cookie) {
		t.Fatal("会话令牌未以摘要形式保存")
	}
	var subjectCiphertext, platformCiphertext []byte
	var subjectIndex string
	if err := harness.pool.QueryRow(ctx, `SELECT subject_index, subject_ciphertext FROM public.external_identities`).
		Scan(&subjectIndex, &subjectCiphertext); err != nil {
		t.Fatal(err)
	}
	if len(subjectIndex) != 64 || bytes.Contains(subjectCiphertext, []byte(testSubject)) {
		t.Fatal("外部身份主体未按密文与索引保存")
	}
	if err := harness.pool.QueryRow(ctx, `SELECT ciphertext FROM public.platform_sessions`).Scan(&platformCiphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(platformCiphertext, []byte(testPlatformBody)) {
		t.Fatal("平台会话以明文形式保存")
	}
	if harness.count(`SELECT count(*) FROM public.audit_events WHERE action = 'user.login'`) != 1 {
		t.Fatal("用户登录未写入审计")
	}

	// 同一主体再次登录仍归属同一内部用户。
	_, _ = harness.userSession()
	if harness.count("SELECT count(*) FROM public.users") != 1 {
		t.Fatal("重复登录创建了额外内部用户")
	}
	if harness.count("SELECT count(*) FROM public.external_identities") != 1 {
		t.Fatal("重复登录创建了额外外部身份")
	}
	if harness.count("SELECT count(*) FROM public.platform_sessions") != 2 {
		t.Fatal("重复登录未保存新的平台会话")
	}

	// 过期会话被拒绝并标记。
	harness.clock.Advance(2*time.Hour + time.Minute)
	expired := harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie))
	requireStatus(t, expired, http.StatusUnauthorized)
	if harness.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'expired'`) < 1 {
		t.Fatal("过期会话未被标记")
	}
}

func TestIntegrationConcurrentIdentityBinding(t *testing.T) {
	harness := newHarness(t, nil)
	const parallel = 6
	type challenge struct{ id, binding string }
	challenges := make([]challenge, 0, parallel)
	for range parallel {
		id, binding := harness.createChallenge(ChannelWeb)
		challenges = append(challenges, challenge{id: id, binding: binding})
	}
	type result struct {
		userID string
		err    error
	}
	results := make(chan result, parallel)
	var wait sync.WaitGroup
	for _, item := range challenges {
		wait.Add(1)
		go func(item challenge) {
			defer wait.Done()
			response := harness.verifyChallenge(item.id, item.binding, testUserPassword, ChannelWeb)
			if response.status != http.StatusOK {
				results <- result{err: fault.New("test_failed", string(response.body))}
				return
			}
			var decoded struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
			}
			if err := json.Unmarshal(response.body, &decoded); err != nil {
				results <- result{err: err}
				return
			}
			results <- result{userID: decoded.User.ID}
		}(item)
	}
	wait.Wait()
	close(results)
	seen := map[string]struct{}{}
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		seen[item.userID] = struct{}{}
	}
	if len(seen) != 1 {
		t.Fatalf("并发首次登录产生 %d 个内部用户", len(seen))
	}
	if harness.count("SELECT count(*) FROM public.users") != 1 {
		t.Fatal("并发首次登录留下了多余内部用户")
	}
	if harness.count("SELECT count(*) FROM public.external_identities") != 1 {
		t.Fatal("并发首次登录创建了多条外部身份")
	}
	if harness.count("SELECT count(*) FROM public.platform_sessions") != parallel {
		t.Fatal("并发登录的平台会话数量异常")
	}
}

func TestIntegrationAppRefreshRotationAndReuse(t *testing.T) {
	harness := newHarness(t, nil)
	id, binding := harness.createChallenge(ChannelApp)
	login := harness.verifyChallenge(id, binding, testUserPassword, ChannelApp)
	requireStatus(t, login, http.StatusOK)
	decoded := login.json(t)
	access, _ := decoded["access_token"].(string)
	refresh, _ := decoded["refresh_token"].(string)
	if access == "" || refresh == "" || decoded["token_type"] != "Bearer" {
		t.Fatalf("App 登录响应缺少令牌：%v", decoded)
	}

	me := harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withBearer(access))
	requireStatus(t, me, http.StatusOK)

	// App 写请求不需要 CSRF 与 Origin；先读取以建立偏好版本。
	initialPreferences := harness.do(harness.user, http.MethodGet, "/api/v1/me/preferences", nil, withBearer(access))
	requireStatus(t, initialPreferences, http.StatusOK)
	preferences := harness.do(harness.user, http.MethodPatch, "/api/v1/me/preferences",
		map[string]any{"theme_mode": "dark"}, withBearer(access), withIfMatch(initialPreferences.header.Get("ETag")))
	requireStatus(t, preferences, http.StatusOK)

	rotated := harness.do(harness.user, http.MethodPost, "/api/v1/auth/refresh",
		map[string]any{"refresh_token": refresh})
	requireStatus(t, rotated, http.StatusOK)
	rotatedDecoded := rotated.json(t)
	newAccess, _ := rotatedDecoded["access_token"].(string)
	newRefresh, _ := rotatedDecoded["refresh_token"].(string)
	if newAccess == "" || newRefresh == "" || newAccess == access || newRefresh == refresh {
		t.Fatalf("刷新未轮换令牌：%v", rotatedDecoded)
	}
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withBearer(access)), http.StatusUnauthorized)
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withBearer(newAccess)), http.StatusOK)

	// 旧刷新令牌复用：撤销整条链。
	reuse := harness.do(harness.user, http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refresh_token": refresh})
	requireStatus(t, reuse, http.StatusUnauthorized)
	afterReuse := harness.do(harness.user, http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refresh_token": newRefresh})
	requireStatus(t, afterReuse, http.StatusUnauthorized)
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withBearer(newAccess)), http.StatusUnauthorized)
	if harness.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'revoked' AND revoke_reason = 'refresh_reuse'`) != 1 {
		t.Fatal("刷新令牌复用未撤销会话链")
	}
}

func TestIntegrationWebSessionRotation(t *testing.T) {
	harness := newHarness(t, nil)
	cookie, csrf := harness.userSession()
	rotated := harness.do(harness.user, http.MethodPost, "/api/v1/auth/refresh", map[string]any{},
		withCookie(userSessionCookie, cookie), withOrigin("http://user.test"), withCSRF(csrf))
	requireStatus(t, rotated, http.StatusOK)
	newCookie := rotated.cookie(userSessionCookie)
	if newCookie == "" || newCookie == cookie {
		t.Fatal("Web 会话未轮换")
	}
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie)), http.StatusUnauthorized)
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, newCookie)), http.StatusOK)
	// 旧会话被标记为轮换撤销。
	if harness.count(`SELECT count(*) FROM public.client_sessions WHERE revoke_reason = 'rotated'`) != 1 {
		t.Fatal("旧 Web 会话未被标记轮换")
	}
}

func TestIntegrationCSRFOriginAndPreferencesIsolation(t *testing.T) {
	harness := newHarness(t, nil)
	cookie, csrf := harness.userSession()
	adminSecret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.service.CreateAdmin(context.Background(), "owner", testAdminPassword, adminSecret); err != nil {
		t.Fatal(err)
	}
	adminCookie, adminCSRF := harness.adminSession(adminSecret)

	cases := []struct {
		name    string
		options []requestOption
	}{
		{"无 Origin", []requestOption{withCookie(userSessionCookie, cookie), withCSRF(csrf)}},
		{"错误 Origin", []requestOption{withCookie(userSessionCookie, cookie), withOrigin("http://evil.test"), withCSRF(csrf)}},
		{"缺少 CSRF", []requestOption{withCookie(userSessionCookie, cookie), withOrigin("http://user.test")}},
	}
	for _, testCase := range cases {
		response := harness.do(harness.user, http.MethodPatch, "/api/v1/me/preferences",
			map[string]any{"theme_mode": "dark"}, testCase.options...)
		requireStatus(t, response, http.StatusForbidden)
		if harness.errorCode(response) != "csrf_invalid" {
			t.Fatalf("%s 返回 %s", testCase.name, harness.errorCode(response))
		}
	}
	// 首次修改前先读取，使偏好记录与版本建立。
	initial := harness.do(harness.user, http.MethodGet, "/api/v1/me/preferences", nil,
		withCookie(userSessionCookie, cookie))
	requireStatus(t, initial, http.StatusOK)
	if initial.json(t)["version"] != float64(1) {
		t.Fatalf("用户偏好初始版本异常：%v", initial.json(t))
	}
	valid := harness.do(harness.user, http.MethodPatch, "/api/v1/me/preferences",
		map[string]any{"theme_mode": "dark"}, withCookie(userSessionCookie, cookie),
		withOrigin("http://user.test"), withCSRF(csrf), withIfMatch(`"v1"`))
	requireStatus(t, valid, http.StatusOK)

	userPreferences := harness.do(harness.user, http.MethodGet, "/api/v1/me/preferences", nil,
		withCookie(userSessionCookie, cookie))
	requireStatus(t, userPreferences, http.StatusOK)
	if userPreferences.json(t)["theme_mode"] != "dark" {
		t.Fatal("用户偏好未保存")
	}
	adminPreferences := harness.do(harness.admin, http.MethodGet, "/admin/api/v1/me/preferences", nil,
		withCookie(adminSessionCookie, adminCookie))
	requireStatus(t, adminPreferences, http.StatusOK)
	if adminPreferences.json(t)["theme_mode"] != "system" {
		t.Fatal("用户偏好影响了管理员偏好")
	}
	// 用户端与管理员端 CSRF 不通用。
	crossCSRF := harness.do(harness.admin, http.MethodPatch, "/admin/api/v1/me/preferences",
		map[string]any{"theme_mode": "grove"}, withCookie(adminSessionCookie, adminCookie),
		withOrigin("http://admin.test"), withCSRF(adminCSRF+"x"), withIfMatch(`"v1"`))
	requireStatus(t, crossCSRF, http.StatusForbidden)
}

func TestIntegrationChallengeLimits(t *testing.T) {
	harness := newHarness(t, func(options *Options) {
		options.ChallengeMaxAttempts = 2
		options.RateLimit = RateLimitOptions{MaxFailures: 10, Window: 10 * time.Minute, BlockFor: time.Minute}
	})
	scan := harness.do(harness.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "scan", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, scan, http.StatusUnprocessableEntity)

	id, binding := harness.createChallenge(ChannelWeb)
	mismatch := harness.verifyChallenge(id, binding+"x", testUserPassword, ChannelWeb)
	requireStatus(t, mismatch, http.StatusNotFound)

	// 两次失败用尽额度。
	requireStatus(t, harness.verifyChallenge(id, binding, "wrong-1", ChannelWeb), http.StatusUnauthorized)
	requireStatus(t, harness.verifyChallenge(id, binding, "wrong-2", ChannelWeb), http.StatusUnauthorized)
	exhausted := harness.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
	requireStatus(t, exhausted, http.StatusConflict)

	id, binding = harness.createChallenge(ChannelWeb)
	harness.clock.Advance(3 * time.Minute)
	expired := harness.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
	requireStatus(t, expired, http.StatusNotFound)

	disabled := newHarness(t, func(options *Options) { options.Verifier = nil })
	unavailable := disabled.do(disabled.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, unavailable, http.StatusServiceUnavailable)
	if disabled.errorCode(unavailable) != "external_disabled" {
		t.Fatalf("未配置验证器返回 %s", disabled.errorCode(unavailable))
	}
}

func TestIntegrationValidateActorAndRevokeUser(t *testing.T) {
	harness := newHarness(t, nil)
	ctx := context.Background()
	cookie, _ := harness.userSession()
	var sessionID, userID string
	if err := harness.pool.QueryRow(ctx, `SELECT id::text, user_id::text FROM public.client_sessions WHERE token_hash = $1`,
		tokenDigest(cookie)).Scan(&sessionID, &userID); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: userID, Kind: KindUser, SessionID: sessionID, Permissions: permissionsFor(KindUser, "")}
	if err := harness.service.ValidateActor(ctx, actor); err != nil {
		t.Fatalf("有效会话校验失败：%v", err)
	}
	if err := harness.service.ValidateActor(ctx, web.Actor{ID: "9f14e45f-ea1a-4c1f-9b2c-3a6f2c1d0e11", Kind: KindUser, SessionID: sessionID}); err == nil {
		t.Fatal("主体不匹配的 Actor 通过了校验")
	}
	if err := harness.service.ValidateActor(ctx, web.Actor{ID: userID, Kind: KindAdmin, SessionID: sessionID}); err == nil {
		t.Fatal("跨端 Actor 通过了校验")
	}

	if err := harness.service.inTx(ctx, func(tx pgx.Tx) error {
		return harness.service.RevokeUser(ctx, tx, userID)
	}); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.ValidateActor(ctx, actor); err == nil {
		t.Fatal("撤销后 Actor 仍然有效")
	}
	if _, err := harness.service.PlatformSession(ctx, userID); err == nil {
		t.Fatal("撤销后平台会话仍然可用")
	}
	if harness.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'revoked' AND revoke_reason = 'user_revoked'`) != 1 {
		t.Fatal("RevokeUser 未撤销客户端会话")
	}

	// 主体状态受限时，即使会话本身有效也拒绝（用于删除/申诉流程）。
	cookie, _ = harness.userSession()
	if err := harness.pool.QueryRow(ctx, `SELECT id::text FROM public.client_sessions WHERE token_hash = $1`,
		tokenDigest(cookie)).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.pool.Exec(ctx, `UPDATE public.users SET status = 'restricted' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if err := harness.service.ValidateActor(ctx, web.Actor{ID: userID, Kind: KindUser, SessionID: sessionID}); err == nil {
		t.Fatal("受限用户的会话通过了校验")
	}
	// 受限主体不能通过重新验证建立新会话。
	challengeID, binding := harness.createChallenge(ChannelWeb)
	blocked := harness.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
	requireStatus(t, blocked, http.StatusForbidden)
}

func TestIntegrationLogoutRevokesPlatformSession(t *testing.T) {
	harness := newHarness(t, nil)
	cookie, csrf := harness.userSession()
	me := harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie))
	requireStatus(t, me, http.StatusOK)
	userID, _ := me.json(t)["id"].(string)
	if _, err := harness.service.PlatformSession(context.Background(), userID); err != nil {
		t.Fatalf("登录后平台会话应可用：%v", err)
	}
	logout := harness.do(harness.user, http.MethodPost, "/api/v1/auth/logout", map[string]any{"platform": true},
		withCookie(userSessionCookie, cookie), withOrigin("http://user.test"), withCSRF(csrf))
	requireStatus(t, logout, http.StatusOK)
	if logout.json(t)["platform_revoked"] != true {
		t.Fatal("登出响应未标明平台会话已撤销")
	}
	if _, err := harness.service.PlatformSession(context.Background(), userID); err == nil {
		t.Fatal("登出后平台会话仍然可用")
	}
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie)), http.StatusUnauthorized)
}
