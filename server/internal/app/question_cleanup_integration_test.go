//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/questions"
)

func TestOperationsCleanupUsesQuestionRetention(t *testing.T) {
	fork := &accountedFork{}
	fixture := newRuntimeFixture(t, fixtureOptions{fork: fork})
	client := fixtureClient()
	defer client.CloseIdleConnections()
	csrf, _ := loginFixtureUser(t, fixture, client, "learner-a")
	adminClient := fixtureClient()
	defer adminClient.CloseIdleConnections()
	code, _, _ := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
	if code != 200 {
		t.Fatalf("测试管理员登录失败：%d", code)
	}
	modelID := seedOffering(t, fork, "内容保留联调模型", "retention-fixture-model",
		questions.CachePolicy{Allow: true, Scope: "user", TTLDays: 7}, nil, true)
	body := map[string]any{"model_offering_id": modelID, "mode": "always_llm", "question": map[string]any{"title": "内容保留联调题", "options": "A. 第一项\nB. 第二项", "type": "single", "images": []string{}}, "budget": map[string]int{"max_output_tokens": 100}}
	code, _, query := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, body, map[string]string{"Idempotency-Key": "retention-runtime-query"})
	if code != 200 {
		t.Fatalf("受控查询失败：%d", code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := fixture.runtime.db.Pool().Exec(ctx, `UPDATE public.question_queries SET completed_at=now()-interval '8 days' WHERE id=$1`, query["id"]); err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.Operations.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	code, _, detail := requestJSON(t, client, "GET", fixture.user.URL+"/api/v1/question-queries/"+query["id"].(string), "", nil, nil)
	if code != 200 || detail["content_expired"] != true || detail["status"] != "answered" {
		t.Fatalf("实际装配未保留查询状态并标记内容过期：%d/%v/%v", code, detail["status"], detail["content_expired"])
	}
	if detail["answer"] != "" && detail["answer"] != nil {
		t.Fatal("过期答案仍可读")
	}
}
