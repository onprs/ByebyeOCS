//go:build integration

package app

import (
	"context"
	"testing"
)

func TestRuntimePrivacyReviewRevokesAndPreventsResurrection(t *testing.T) {
	fixture := newRuntimeFixture(t)
	userClient := fixtureClient()
	defer userClient.CloseIdleConnections()
	csrf, userID := loginFixtureUser(t, fixture, userClient, "learner-a")
	adminClient := fixtureClient()
	defer adminClient.CloseIdleConnections()
	code, _, adminSession := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
	if code != 200 {
		t.Fatal("测试管理员无法登录", code)
	}
	adminCSRF := adminSession["csrf_token"].(string)
	code, _, request := requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/me/deletion-requests", csrf, map[string]bool{"confirm": true}, nil)
	if code != 202 || request["sessions_revoked"] != true {
		t.Fatal("删除申请未明确撤销会话", code, request)
	}
	requestID := request["id"].(string)
	if code, _, _ := requestJSON(t, userClient, "GET", fixture.user.URL+"/api/v1/me", "", nil, nil); code != 401 {
		t.Fatal("旧Cookie仍可使用", code)
	}
	for _, action := range []string{"defer", "reject"} {
		code, _, decision := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/data-requests/"+requestID+"/review", adminCSRF, map[string]string{"action": action, "reason": "隔离HTTP验证：先保留申请并复核用户重新进入"}, nil)
		expected := map[string]string{"defer": "deferred", "reject": "rejected"}[action]
		if code != 200 || decision["status"] != expected || decision["closed"] != false {
			t.Fatal("复核状态与关闭语义不符", action, code, decision)
		}
	}
	csrf, sameUserID := loginFixtureUser(t, fixture, userClient, "learner-a")
	if sameUserID != userID {
		t.Fatal("驳回后身份绑定到新用户")
	}
	code, _, request = requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/me/deletion-requests", csrf, map[string]bool{"confirm": true}, nil)
	if code != 202 {
		t.Fatal("重新申请删除失败", code, request)
	}
	secondID := request["id"].(string)
	if secondID == requestID {
		t.Fatal("已终结请求被复用")
	}
	code, _, decision := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/data-requests/"+secondID+"/review", adminCSRF, map[string]string{"action": "complete", "reason": "隔离HTTP验证：确认无未清资金与活动后完成关闭"}, nil)
	if code != 200 || decision["closed"] != true || decision["status"] != "completed" {
		t.Fatal("真实删除闭环未完成", code, decision)
	}
	if code, _, _ := requestJSON(t, userClient, "GET", fixture.user.URL+"/api/v1/me", "", nil, nil); code != 401 {
		t.Fatal("关闭后的旧会话仍有效", code)
	}
	code, _, challenge := requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/auth/chaoxing/challenges", "", map[string]string{"method": "password", "client": "web"}, nil)
	if code != 201 {
		t.Fatal("公开挑战接口异常", code)
	}
	id := challenge["id"].(string)
	code, _, _ = requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/auth/chaoxing/challenges/"+id+"/verify", "", map[string]string{"school": "", "account": "learner-a", "password": fixtureUserPassword}, nil)
	if code != 403 {
		t.Fatal("已关闭主体可以重新进入", code)
	}
	var count int
	var closed string
	if err := fixture.runtime.db.Pool().QueryRow(context.Background(), "SELECT count(*),min(status) FROM public.users WHERE id=$1", userID).Scan(&count, &closed); err != nil || count != 1 || closed != "closed" {
		t.Fatal("关闭身份标记未保留")
	}
}
