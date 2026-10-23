//go:build integration

package auth

import (
	"net/http"
	"testing"
	"time"
)

// 访问令牌过期但刷新链仍有效的 App 会话依然可以续期，必须继续占用单主体会话配额。
// 若只统计 expires_at，客户端可以在访问令牌过期后反复登录，绕过会话上限并累积可续期会话。
func TestIntegrationAppSessionQuotaCoversRefreshableSessions(t *testing.T) {
	h := newHarness(t, func(options *Options) { options.MaxSessionsPerSubject = 2 })
	loginApp := func() string {
		id, binding := h.createChallenge(ChannelApp)
		response := h.verifyChallenge(id, binding, testUserPassword, ChannelApp)
		requireStatus(t, response, http.StatusOK)
		decoded := response.json(t)
		access, _ := decoded["access_token"].(string)
		refresh, _ := decoded["refresh_token"].(string)
		if access == "" || refresh == "" {
			t.Fatalf("App 登录缺少令牌：%v", decoded)
		}
		return refresh
	}
	firstRefresh := loginApp()
	h.clock.Advance(time.Second)
	loginApp()
	h.clock.Advance(h.service.options.AppAccessTTL + time.Minute)
	thirdRefresh := loginApp()

	if active := h.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'active'`); active != 2 {
		t.Fatalf("访问令牌过期后在用会话为 %d，期望 2", active)
	}
	if evicted := h.count(`SELECT count(*) FROM public.client_sessions WHERE revoke_reason = 'quota_evicted'`); evicted != 1 {
		t.Fatalf("配额淘汰会话数为 %d，期望 1", evicted)
	}
	// 被淘汰会话的刷新链必须同时失效；仍然保留的会话可以正常续期。
	requireStatus(t, h.do(h.user, http.MethodPost, "/api/v1/auth/refresh",
		map[string]any{"refresh_token": firstRefresh}), http.StatusUnauthorized)
	requireStatus(t, h.do(h.user, http.MethodPost, "/api/v1/auth/refresh",
		map[string]any{"refresh_token": thirdRefresh}), http.StatusOK)
}
