//go:build integration

package auth

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// 每个主体的会话配额是硬上限：并发登录串行检查，超限撤销最旧会话及刷新链。
func TestIntegrationUserSessionQuota(t *testing.T) {
	h := newHarness(t, func(options *Options) {
		options.MaxSessionsPerSubject = 3
		options.ChallengeRateLimit = ChallengeRateLimit{MaxPerClient: 100, Window: 10 * time.Minute, MaxPending: 500}
	})
	const parallel = 8
	challenges := make([]struct{ id, binding string }, 0, parallel)
	for i := 0; i < parallel; i++ {
		id, binding := h.createChallenge(ChannelWeb)
		challenges = append(challenges, struct{ id, binding string }{id: id, binding: binding})
	}
	cookies := make([]string, parallel)
	failures := make(chan string, parallel)
	var wait sync.WaitGroup
	for index, item := range challenges {
		wait.Add(1)
		go func(index int, id, binding string) {
			defer wait.Done()
			response := h.verifyChallenge(id, binding, testUserPassword, ChannelWeb)
			if response.status != http.StatusOK {
				failures <- string(response.body)
				return
			}
			cookies[index] = response.cookie(userSessionCookie)
		}(index, item.id, item.binding)
	}
	wait.Wait()
	close(failures)
	for message := range failures {
		t.Fatalf("并发登录失败：%s", message)
	}

	if active := h.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'active'`); active != 3 {
		t.Fatalf("并发登录后在用会话为 %d，期望 3", active)
	}
	if evicted := h.count(`SELECT count(*) FROM public.client_sessions WHERE revoke_reason = 'quota_evicted'`); evicted != parallel-3 {
		t.Fatalf("配额淘汰会话数为 %d，期望 %d", evicted, parallel-3)
	}
	valid := 0
	for _, cookie := range cookies {
		if cookie == "" {
			t.Fatal("并发登录缺少会话 Cookie")
		}
		response := h.do(h.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, cookie))
		if response.status == http.StatusOK {
			valid++
		}
	}
	if valid != 3 {
		t.Fatalf("并发登录后仍有效的 Cookie 为 %d，期望 3", valid)
	}

	// App 会话被配额淘汰时，刷新链必须同时失效。
	appHarness := newHarness(t, func(options *Options) { options.MaxSessionsPerSubject = 3 })
	challengeID, binding := appHarness.createChallenge(ChannelApp)
	login := appHarness.verifyChallenge(challengeID, binding, testUserPassword, ChannelApp)
	requireStatus(t, login, http.StatusOK)
	loginBody := login.json(t)
	access, _ := loginBody["access_token"].(string)
	refresh, _ := loginBody["refresh_token"].(string)
	for i := 0; i < 3; i++ {
		// 推进时间使 last_seen_at 严格递增，确定最旧会话为 App 会话。
		appHarness.clock.Advance(time.Second)
		_, _ = appHarness.userSession()
	}
	if appHarness.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'active'`) != 3 {
		t.Fatal("App 会话配额淘汰后在用会话数量异常")
	}
	if appHarness.count(`SELECT count(*) FROM public.client_sessions WHERE revoke_reason = 'quota_evicted' AND channel = 'app'`) != 1 {
		t.Fatal("最旧的 App 会话未被配额淘汰")
	}
	requireStatus(t, appHarness.do(appHarness.user, http.MethodGet, "/api/v1/me", nil, withBearer(access)), http.StatusUnauthorized)
	refreshed := appHarness.do(appHarness.user, http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refresh_token": refresh})
	requireStatus(t, refreshed, http.StatusUnauthorized)
}

func TestIntegrationAdminSessionQuota(t *testing.T) {
	h := newHarness(t, func(options *Options) { options.MaxSessionsPerSubject = 2 })
	secret := h.createRoleAdmin("owner", RoleOwner)
	firstCookie, firstCSRF := h.adminLoginAs("owner", secret)
	secondCookie, secondCSRF := h.adminLoginAs("owner", secret)
	thirdCookie, thirdCSRF := h.adminLoginAs("owner", secret)

	if h.count(`SELECT count(*) FROM public.admin_sessions WHERE status = 'active'`) != 2 {
		t.Fatal("管理员会话超过配额上限")
	}
	if h.count(`SELECT count(*) FROM public.admin_sessions WHERE revoke_reason = 'quota_evicted'`) != 1 {
		t.Fatal("最旧管理员会话未被配额淘汰")
	}
	requireStatus(t, h.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, firstCookie, firstCSRF), http.StatusUnauthorized)
	requireStatus(t, h.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, secondCookie, secondCSRF), http.StatusOK)
	requireStatus(t, h.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, thirdCookie, thirdCSRF), http.StatusOK)
}
