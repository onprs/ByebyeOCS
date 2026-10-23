//go:build integration

package auth

import (
	"net/http"
	"testing"
	"time"
)

// 会话管理必须显示并允许撤销“访问令牌已过期、刷新链仍有效”的 App 会话。
// 否则用户看不到仍可续期的设备凭据，也无法从列表撤销它们。
func TestIntegrationSessionListIncludesRefreshableAppSessions(t *testing.T) {
	h := newHarness(t, nil)
	webCookie, webCSRF := h.userSession()
	challengeID, binding := h.createChallenge(ChannelApp)
	login := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelApp)
	requireStatus(t, login, http.StatusOK)
	loginBody := login.json(t)
	refresh, _ := loginBody["refresh_token"].(string)
	if refresh == "" {
		t.Fatal("App 登录缺少刷新令牌")
	}
	// 越过访问令牌有效期，刷新链仍可续期。
	h.clock.Advance(h.service.options.AppAccessTTL + time.Minute)

	list := h.do(h.user, http.MethodGet, "/api/v1/me/sessions", nil,
		withCookie(userSessionCookie, webCookie))
	requireStatus(t, list, http.StatusOK)
	sessions, _ := list.json(t)["sessions"].([]any)
	var appSession map[string]any
	for _, raw := range sessions {
		item, _ := raw.(map[string]any)
		if item["channel"] == ChannelApp {
			appSession = item
		}
	}
	if appSession == nil {
		t.Fatalf("会话列表未显示刷新链仍有效的 App 会话：%v", sessions)
	}
	expiresText, _ := appSession["expires_at"].(string)
	expires, err := time.Parse(time.RFC3339, expiresText)
	if err != nil {
		t.Fatalf("App 会话过期时间格式无效: %q", expiresText)
	}
	if !expires.After(h.clock.Now()) {
		t.Fatalf("App 会话过期时间仍是已过期的访问令牌期限: %s", expiresText)
	}
	appID, _ := appSession["id"].(string)
	if appID == "" {
		t.Fatal("App 会话缺少编号")
	}
	revoked := h.do(h.user, http.MethodDelete, "/api/v1/me/sessions/"+appID, nil,
		withCookie(userSessionCookie, webCookie), withOrigin("http://user.test"), withCSRF(webCSRF))
	requireStatus(t, revoked, http.StatusOK)
	refreshed := h.do(h.user, http.MethodPost, "/api/v1/auth/refresh",
		map[string]any{"refresh_token": refresh})
	requireStatus(t, refreshed, http.StatusUnauthorized)
}
