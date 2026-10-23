//go:build integration

package app

import "testing"

func TestCacheReportReviewIsReachableThroughAdminAPI(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{fork: &accountedFork{}})
	user, admin := fixtureClient(), fixtureClient()
	defer user.CloseIdleConnections()
	defer admin.CloseIdleConnections()
	csrf, _ := loginFixtureUser(t, fixture, user, "learner-a")
	status, _, login := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
	if status != 200 {
		t.Fatal("测试管理员未登录")
	}
	adminCSRF := login["csrf_token"].(string)
	status, _, model := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/question-models", adminCSRF, map[string]any{"display_name": "报告核对模型", "provider_ref": "report-review-fixture", "capabilities": []string{"text"}}, nil)
	if status != 201 {
		t.Fatalf("创建测试模型失败：%d", status)
	}
	status, _, query := requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, map[string]any{
		"model_offering_id": model["id"], "mode": "always_llm",
		"question": map[string]any{"title": "不得通过报告列表回传的题目", "type": "single", "options": "A. 第一项\nB. 第二项", "images": []string{}},
		"budget":   map[string]int{"max_output_tokens": 100},
	}, map[string]string{"Idempotency-Key": "report-review-query"})
	if status != 200 {
		t.Fatalf("受控题库查询失败：%d", status)
	}
	status, _, report := requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/question-cache-reports", csrf, map[string]any{"query_id": query["id"], "reason": "选项映射需要核对"}, nil)
	if status != 201 {
		t.Fatalf("提交非缓存报告失败：%d", status)
	}
	status, _, page := requestJSON(t, admin, "GET", fixture.admin.URL+"/admin/api/v1/question-cache-reports", "", nil, nil)
	if status != 200 || len(page["items"].([]any)) != 1 {
		t.Fatalf("管理员报告读取入口不可用：%d", status)
	}
	item := page["items"].([]any)[0].(map[string]any)
	if item["id"] != report["id"] || item["query"].(map[string]any)["id"] != query["id"] || item["question_title"] != nil || item["answer"] != nil {
		t.Fatal("报告列表没有返回最小引用或泄露了题目/答案字段")
	}
	status, _, reviewed := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/question-cache-reports/"+report["id"].(string)+"/review", adminCSRF,
		map[string]string{"status": "resolved", "reason": "已核对选项并完成处理"}, map[string]string{"Idempotency-Key": "report-review-runtime"})
	if status != 200 || reviewed["status"] != "resolved" || reviewed["resolved_at"] == nil {
		t.Fatalf("报告复核未完成：%d/%v", status, reviewed["status"])
	}
	status, _, page = requestJSON(t, admin, "GET", fixture.admin.URL+"/admin/api/v1/question-cache-reports?status=pending", "", nil, nil)
	if status != 200 || len(page["items"].([]any)) != 0 {
		t.Fatal("已复核的非缓存报告仍阻塞在待处理列表")
	}
}
