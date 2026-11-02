//go:build integration

package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestLocalAdminAutoLoginCreatesAuditedSessionOnlyForAllowedRequest(t *testing.T) {
	harness := newHarness(t, func(options *Options) {
		options.LocalAdminAutoLogin = func(r *http.Request) bool { return r.Host == "127.0.0.1" }
	})
	if err := harness.service.CreateAdmin(context.Background(), "owner", testAdminPassword, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"); err != nil {
		t.Fatal(err)
	}
	denied := harness.do(harness.admin, "GET", "/admin/api/v1/auth/session", nil, func(r *http.Request) { r.Host = "admin.example.test" })
	requireStatus(t, denied, http.StatusOK)
	if denied.json(t)["authenticated"] != false || denied.cookie(adminSessionCookie) != "" {
		t.Fatal("非回环请求触发了管理员自动登录")
	}
	login := harness.do(harness.admin, "GET", "/admin/api/v1/auth/session", nil, func(r *http.Request) { r.Host = "127.0.0.1" })
	requireStatus(t, login, http.StatusOK)
	body := login.json(t)
	token := login.cookie(adminSessionCookie)
	if body["authenticated"] != true || body["kind"] != KindAdmin || token == "" || body["csrf_token"] == "" {
		t.Fatalf("回环管理员自动登录未建立会话：%s", string(login.body))
	}
	me := harness.do(harness.admin, "GET", "/admin/api/v1/me", nil, withCookie(adminSessionCookie, token))
	requireStatus(t, me, http.StatusOK)
	if me.json(t)["username"] != "owner" {
		t.Fatalf("自动会话无法读取管理员资料：%s", string(me.body))
	}
	stale := harness.do(harness.admin, "GET", "/admin/api/v1/auth/session", nil,
		func(r *http.Request) { r.Host = "127.0.0.1" }, withCookie(adminSessionCookie, "expired-session-value"))
	requireStatus(t, stale, http.StatusOK)
	var issuedCookie string
	for _, raw := range stale.header.Values("Set-Cookie") {
		for _, cookie := range (&http.Response{Header: http.Header{"Set-Cookie": {raw}}}).Cookies() {
			if cookie.Name == adminSessionCookie && cookie.Value != "" {
				issuedCookie = cookie.Value
			}
		}
	}
	if stale.json(t)["authenticated"] != true || issuedCookie == "" || issuedCookie == "expired-session-value" {
		t.Fatalf("旧管理员 Cookie 没有触发本地自动会话：%s", string(stale.body))
	}
	blockedStale := harness.do(harness.admin, "GET", "/admin/api/v1/auth/session", nil,
		func(r *http.Request) { r.Host = "admin.example.test" }, withCookie(adminSessionCookie, "expired-session-value"))
	requireStatus(t, blockedStale, http.StatusUnauthorized)
	harness.clock.Advance(11 * time.Minute)
	freshList := harness.do(harness.admin, "GET", "/admin/api/v1/admin-users", nil,
		func(r *http.Request) { r.Host = "127.0.0.1" }, withCookie(adminSessionCookie, token))
	requireStatus(t, freshList, http.StatusOK)
	var count int
	if err := harness.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.audit_events WHERE action='admin.local_login'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("管理员自动登录缺少审计：%d/%v", count, err)
	}
}

func TestLocalAdminAutoLoginWithoutAdminStaysAnonymous(t *testing.T) {
	harness := newHarness(t, func(options *Options) {
		options.LocalAdminAutoLogin = func(*http.Request) bool { return true }
	})
	response := harness.do(harness.admin, "GET", "/admin/api/v1/auth/session", nil)
	requireStatus(t, response, http.StatusOK)
	if response.json(t)["authenticated"] != false || response.cookie(adminSessionCookie) != "" {
		t.Fatal("没有管理员时仍建立了自动会话")
	}
}
