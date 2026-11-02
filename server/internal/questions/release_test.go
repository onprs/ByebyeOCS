//go:build integration

package questions

import (
	"context"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 结果未知的查询可以被管理员释放：旧事实保留、幂等键改名、新的同键请求可以重新作答。
func TestReleaseUnknownQueryFreesIdempotencyKey(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	cacheRead, cacheWrite := Micros(0), Micros(0)
	price := &OfferingPricing{Version: "release-v1", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		CacheWriteMicrosPerMillionTokens: &cacheWrite, MicrosPerMinor: 10000,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	offering := env.createOffering(t, "释放模型", "internal-release", price, true)

	env.fork.answer = func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{}, &ForkError{Code: "request_unknown", Message: "上游结果未知", Status: 504}
	}
	input := QueryInput{ModelOfferingID: offering.ID, Mode: "always_llm"}
	input.Question.Title = "释放测试题"
	input.Question.Options = "A. 甲\nB. 乙"
	input.Question.Type = "single"
	if _, err := env.service.Answer(context.Background(), env.userID, "release-1", input); err == nil {
		t.Fatal("未知结果必须返回错误")
	}
	var queryID, status, errorCode string
	if err := env.pool.QueryRow(context.Background(), `SELECT id, status, COALESCE(error_code,'')
		FROM public.question_queries WHERE user_id = $1 AND idempotency_key = 'release-1'`,
		env.userID).Scan(&queryID, &status, &errorCode); err != nil {
		t.Fatal("未知结果必须保留查询记录")
	}
	if status != "needs_review" || errorCode != "request_unknown" {
		t.Fatalf("未知结果状态不符：%s/%s", status, errorCode)
	}
	actor := web.Actor{ID: env.adminID, Kind: "admin"}
	released, err := env.service.releaseUnknownQuery(context.Background(), actor, queryID, "现场确认模型调用失败", "req_0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("释放失败：%v", err)
	}
	if released.Status != "failed" || released.Pricing.BillingState != "released" {
		t.Fatalf("释放后的状态不符：%+v", released)
	}
	var key string
	if err := env.pool.QueryRow(context.Background(), `SELECT idempotency_key
		FROM public.question_queries WHERE id = $1`, queryID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if key == "release-1" {
		t.Fatal("释放后旧记录仍占用原幂等键")
	}
	// 同一幂等键可以重新发起并成功。
	env.fork.answer = nil
	if _, err := env.service.Answer(context.Background(), env.userID, "release-1", input); err != nil {
		t.Fatalf("释放后重新作答失败：%v", err)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'release-1'`, env.userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("原键应只对应一条新记录：%d", count)
	}
	// 重复释放按已完成返回，不再改动事实。
	if _, err := env.service.releaseUnknownQuery(context.Background(), actor, queryID, "重复释放", "req_0123456789abcdef0123456789abcdee"); err != nil {
		t.Fatalf("重复释放应幂等：%v", err)
	}
}

// 已拿到部分用量的查询不能通过释放绕过复核。
func TestReleaseRejectsSettledUsage(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	cacheRead, cacheWrite := Micros(0), Micros(0)
	price := &OfferingPricing{Version: "release-v2", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		CacheWriteMicrosPerMillionTokens: &cacheWrite, MicrosPerMinor: 10000,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	offering := env.createOffering(t, "释放模型二", "internal-release-2", price, true)
	env.fork.answer = func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		response := answeredForkResponse(request)
		response.Usage.Complete = false
		return response, nil
	}
	input := QueryInput{ModelOfferingID: offering.ID, Mode: "always_llm"}
	input.Question.Title = "释放测试题二"
	input.Question.Options = "A. 甲\nB. 乙"
	input.Question.Type = "single"
	if _, err := env.service.Answer(context.Background(), env.userID, "release-2", input); err != nil {
		t.Fatalf("用量不完整应转待复核而不是直接失败：%v", err)
	}
	var queryID string
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'release-2'`, env.userID).Scan(&queryID); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: env.adminID, Kind: "admin"}
	if _, err := env.service.releaseUnknownQuery(context.Background(), actor, queryID, "试图释放", "req_0123456789abcdef0123456789abcdef"); err == nil {
		t.Fatal("用量不完整的查询不应允许释放")
	}
}
