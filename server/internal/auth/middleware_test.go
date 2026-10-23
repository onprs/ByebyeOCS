package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func testSession(service *Service, id, channel string, now time.Time) sessionState {
	return sessionState{
		ID:                id,
		SubjectID:         "8f14e45f-ea1a-4c1f-9b2c-3a6f2c1d0e11",
		Channel:           channel,
		CSRFHash:          service.csrfDigest(id),
		Status:            "active",
		AuthenticatedAt:   now.Add(-time.Minute),
		StepUpAt:          now.Add(-time.Minute),
		ExpiresAt:         now.Add(time.Hour),
		AbsoluteExpiresAt: now.Add(2 * time.Hour),
		LastSeenAt:        now,
	}
}

func TestMiddlewareAnonymousRules(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, _ := newTestService(t, baseTestOptions(now))
	user := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	recorder := httptest.NewRecorder()
	user.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("匿名受保护请求返回 %d", recorder.Code)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/chaoxing/challenges", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	user.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("匿名公共登录路径被拦截：%d", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/chaoxing/challenges", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "http://evil.test")
	recorder = httptest.NewRecorder()
	user.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("外部 Origin 的登录请求返回 %d", recorder.Code)
	}

	// 无效 Cookie 即使在公共路径也不能默默成为匿名。
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: tokenPrefixUserWeb + "unknown"})
	recorder = httptest.NewRecorder()
	user.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("无效 Cookie 的公共请求返回 %d", recorder.Code)
	}
	if !hasDeletionCookie(recorder, userSessionCookie) {
		t.Fatal("无效 Cookie 未下发删除指令")
	}
}

func hasDeletionCookie(recorder *httptest.ResponseRecorder, name string) bool {
	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name && (cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()))) {
			return true
		}
	}
	return false
}

func TestMiddlewareCookieCSRFAndOrigin(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, reader := newTestService(t, baseTestOptions(now))
	var actor web.Actor
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ := web.ActorFrom(r.Context())
		actor = captured
		w.WriteHeader(http.StatusOK)
	}))
	sessionID := "0b5f0b52-6f5c-4a1e-9d24-6f0f2f1c7d33"
	token := tokenPrefixUserWeb + "test-cookie-token"
	reader.add(KindUser, token, testSession(service, sessionID, ChannelWeb, now))

	build := func(method, path string, origin, csrf string) *http.Request {
		request := httptest.NewRequest(method, path, strings.NewReader("{}"))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if csrf != "" {
			request.Header.Set(csrfHeader, csrf)
		}
		return request
	}

	for _, testCase := range []struct {
		name   string
		origin string
		csrf   string
		status int
	}{
		{"缺少 Origin", "", service.csrfToken(sessionID), http.StatusForbidden},
		{"错误 Origin", "http://evil.test", service.csrfToken(sessionID), http.StatusForbidden},
		{"缺少 CSRF", "http://user.test", "", http.StatusForbidden},
		{"错误 CSRF", "http://user.test", "wrong-token", http.StatusForbidden},
		{"完整校验", "http://user.test", service.csrfToken(sessionID), http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, build(http.MethodPost, "/api/v1/auth/logout", testCase.origin, testCase.csrf))
		if recorder.Code != testCase.status {
			t.Fatalf("%s 返回 %d，期望 %d", testCase.name, recorder.Code, testCase.status)
		}
	}
	if actor.ID == "" || actor.Kind != KindUser || actor.SessionID != sessionID {
		t.Fatalf("Actor 未正确写入上下文：%+v", actor)
	}
	if !containsString(actor.Permissions, PermissionSelf) {
		t.Fatalf("用户权限集合错误：%v", actor.Permissions)
	}

	// 安全方法只校验会话，不要求 Origin 与 CSRF。
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("安全方法被 CSRF 拦截：%d", recorder.Code)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestMiddlewareBearerAndCrossEndIsolation(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, reader := newTestService(t, baseTestOptions(now))
	userHandler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	adminHandler := service.Middleware(KindAdmin, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	userSessionID := "3f5d3c30-2d4a-4a43-9c9f-0f6f4a4a2b11"
	appToken := tokenPrefixUserApp + "app-access-token"
	reader.add(KindUser, appToken, testSession(service, userSessionID, ChannelApp, now))

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+appToken)
	recorder := httptest.NewRecorder()
	userHandler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("App Bearer 请求返回 %d", recorder.Code)
	}

	// 用户令牌不能用于管理员端。
	request = httptest.NewRequest(http.MethodGet, "/admin/api/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: adminSessionCookie, Value: appToken})
	recorder = httptest.NewRecorder()
	adminHandler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("用户令牌在管理员端返回 %d", recorder.Code)
	}

	// 管理员令牌不能用于用户端。
	adminSessionID := "9a7b1c54-0d3f-4c2b-8a7e-1f2d3c4b5a66"
	adminToken := tokenPrefixAdminWeb + "admin-token"
	adminState := testSession(service, adminSessionID, ChannelWeb, now)
	adminState.Role = RoleOwner
	reader.add(KindAdmin, adminToken, adminState)
	request = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: adminToken})
	recorder = httptest.NewRecorder()
	userHandler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("管理员令牌在用户端返回 %d", recorder.Code)
	}

	// 管理员端不接受 Bearer。
	request = httptest.NewRequest(http.MethodGet, "/admin/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+adminToken)
	recorder = httptest.NewRecorder()
	adminHandler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("管理员端接受了 Bearer：%d", recorder.Code)
	}
}

func TestMiddlewareSessionExpiryAndIdle(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, reader := newTestService(t, baseTestOptions(now))
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	build := func(token string) *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
		return request
	}

	expiredID := "11111111-1111-4111-8111-111111111111"
	expiredToken := tokenPrefixUserWeb + "expired"
	expired := testSession(service, expiredID, ChannelWeb, now)
	expired.ExpiresAt = now.Add(-time.Second)
	reader.add(KindUser, expiredToken, expired)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, build(expiredToken))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("过期会话返回 %d", recorder.Code)
	}
	if !containsString(reader.expired, "user:"+expiredID+":expired") {
		t.Fatalf("过期会话未被标记撤销：%v", reader.expired)
	}

	idleID := "22222222-2222-4222-8222-222222222222"
	idleToken := tokenPrefixUserWeb + "idle"
	idle := testSession(service, idleID, ChannelWeb, now)
	idle.LastSeenAt = now.Add(-2 * time.Hour)
	reader.add(KindUser, idleToken, idle)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, build(idleToken))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("空闲过期会话返回 %d", recorder.Code)
	}
	if !containsString(reader.expired, "user:"+idleID+":idle") {
		t.Fatalf("空闲会话未被标记撤销：%v", reader.expired)
	}

	// App 访问令牌到期只拒绝请求，不撤销可继续刷新的会话。
	appID := "33333333-3333-4333-8333-333333333333"
	appToken := tokenPrefixUserApp + "expired-access"
	appState := testSession(service, appID, ChannelApp, now)
	appState.ExpiresAt = now.Add(-time.Second)
	reader.add(KindUser, appToken, appState)
	expiredBefore := len(reader.expired)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+appToken)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("过期 App 访问令牌返回 %d", recorder.Code)
	}
	if len(reader.expired) != expiredBefore {
		t.Fatal("App 访问令牌到期错误地撤销了会话")
	}
}

func TestMiddlewareRejectsMalformedAuthorization(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, _ := newTestService(t, baseTestOptions(now))
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Basic abc")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("无效认证头返回 %d", recorder.Code)
	}
}

func TestMiddlewareExtraPublicPaths(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	options := baseTestOptions(now)
	options.ExtraPublicPaths = []string{"/api/v1/capabilities"}
	service, _ := newTestService(t, options)
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("额外公共路径被拦截：%d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未列入的路径被匿名放行：%d", recorder.Code)
	}
}

func TestNewRejectsInvalidExtraPublicPath(t *testing.T) {
	options := baseTestOptions(time.Unix(1700000000, 0).UTC())
	options.ExtraPublicPaths = []string{"api/v1/capabilities"}
	if _, err := newService(nil, options); err == nil {
		t.Fatal("缺少前导 / 的额外公共路径被接受")
	}
}

func TestMiddlewareRefreshIgnoresExpiredBearer(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, _ := newTestService(t, baseTestOptions(now))
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/refresh", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+tokenPrefixUserApp+"expired-access-token")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("刷新请求因携带过期访问令牌被拦截：%d", recorder.Code)
	}

	// 其他路径仍按失败关闭处理。
	request = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.Header.Set("Authorization", "Bearer "+tokenPrefixUserApp+"expired-access-token")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("过期访问令牌在受保护路径返回 %d", recorder.Code)
	}
}

func TestMiddlewareDatabaseFailureIsNotUnauthenticated(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, reader := newTestService(t, baseTestOptions(now))
	reader.err = fault.New("service_unavailable", "会话存储暂不可用")
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	token := tokenPrefixUserWeb + "any"
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("会话存储故障返回 %d，期望 503", recorder.Code)
	}
}

// 数据库短暂故障返回 503 时不能删除仍然有效的会话 Cookie，否则服务恢复后
// 客户端仍会被当作未登录，必须重新完成外部身份验证。
func TestMiddlewareTransientFailureKeepsSessionCookie(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, reader := newTestService(t, baseTestOptions(now))
	token := tokenPrefixUserWeb + "transient"
	reader.add(KindUser, token, testSession(service, web.NewID(), ChannelWeb, now))
	reader.err = fault.New("service_unavailable", "会话存储暂不可用")
	handler := service.Middleware(KindUser, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("会话存储故障返回 %d，期望 503", recorder.Code)
	}
	if cookie := recorder.Header().Get("Set-Cookie"); cookie != "" {
		t.Fatalf("短暂故障清除了会话 Cookie: %s", cookie)
	}

	reader.err = nil
	request = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	request.AddCookie(&http.Cookie{Name: userSessionCookie, Value: token})
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("故障恢复后同一 Cookie 未继续可用: %d", recorder.Code)
	}
}
