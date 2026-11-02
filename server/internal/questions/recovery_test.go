//go:build integration

package questions

import (
	"context"
	"testing"
	"time"
)

// 等待窗口到点后查询保持 pending；回收器向题库确认结果后按正常链路完成结算。
func TestRecoveryUsesForkStateInsteadOfRetrying(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	cacheRead, cacheWrite := Micros(0), Micros(0)
	price := &OfferingPricing{Version: "recovery-v1", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		CacheWriteMicrosPerMillionTokens: &cacheWrite, MicrosPerMinor: 10000,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	offering := env.createOffering(t, "回收模型", "internal-recovery", price, true)

	var request ForkAnswerRequest
	env.fork.answer = func(value ForkAnswerRequest) (ForkAnswerResponse, error) {
		request = value
		return ForkAnswerResponse{}, &ForkError{Code: "fork_pending", Message: "题库仍在处理", Status: 504, Retryable: true}
	}
	input := QueryInput{ModelOfferingID: offering.ID, Mode: "always_llm"}
	input.Question.Title = "回收器测试题"
	input.Question.Options = "A. 甲\nB. 乙"
	input.Question.Type = "single"
	view, err := env.service.Answer(context.Background(), env.userID, "recovery-1", input)
	if err == nil {
		t.Fatal("题库仍在处理时不应返回成功")
	}
	if view.ID == "" {
		t.Fatal("等待窗口到点必须保留查询记录")
	}
	var status string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status FROM public.question_queries WHERE id = $1`, view.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "pending" {
		t.Fatalf("等待窗口到点不应判失败：%s", status)
	}
	// 模拟已经超过等待上限，题库此时已经完成。
	env.fork.statusByKey = map[string]ForkRequestStatus{
		view.ID: {State: "completed", Response: func() *ForkAnswerResponse {
			answer := answeredForkResponse(request)
			return &answer
		}()},
	}
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE public.question_queries SET updated_at = now() - interval '30 minutes' WHERE id = $1`, view.ID); err != nil {
		t.Fatal(err)
	}
	handled, err := env.service.RecoverStaleQueries(context.Background())
	if err != nil || handled != 1 {
		t.Fatalf("回收失败：%d/%v", handled, err)
	}
	env.service.recordRecoveryRun(handled, nil)
	if stats := env.service.recoveryStats(); stats.Runs != 1 || stats.Handled != 1 || stats.LastRunAt.IsZero() {
		t.Fatalf("回收观测值不符：%+v", stats)
	}
	env.service.recordRecoveryRun(0, &ForkError{Code: "service_unavailable"})
	if stats := env.service.recoveryStats(); stats.Failed != 1 || stats.LastError != "service_unavailable" {
		t.Fatalf("回收失败观测值不符：%+v", stats)
	}
	var finalStatus, answer string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(answer, '') FROM public.question_queries WHERE id = $1`, view.ID).
		Scan(&finalStatus, &answer); err != nil {
		t.Fatal(err)
	}
	if finalStatus != "answered" || answer != "A" {
		t.Fatalf("回收没有按题库结果完成：%s/%s", finalStatus, answer)
	}
	// 题库已经给出结果时不应再次调用模型。
	env.fork.mu.Lock()
	answerCalls := len(env.fork.requests)
	env.fork.mu.Unlock()
	if answerCalls != 1 {
		t.Fatalf("回收触发了重复模型调用：%d", answerCalls)
	}
}

// 题库记录消失时转人工复核，不能静默失败或重复调用模型。
func TestRecoveryMissingForkRecordGoesToReview(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	cacheRead, cacheWrite := Micros(0), Micros(0)
	price := &OfferingPricing{Version: "recovery-v2", InputMicrosPerMillionTokens: 1_000_000,
		OutputMicrosPerMillionTokens: 2_000_000, CacheReadMicrosPerMillionTokens: &cacheRead,
		CacheWriteMicrosPerMillionTokens: &cacheWrite, MicrosPerMinor: 10000,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	offering := env.createOffering(t, "回收模型二", "internal-recovery-2", price, true)
	env.fork.answer = func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{}, &ForkError{Code: "fork_pending", Message: "题库仍在处理", Status: 504, Retryable: true}
	}
	input := QueryInput{ModelOfferingID: offering.ID, Mode: "always_llm"}
	input.Question.Title = "回收器测试题二"
	input.Question.Options = "A. 甲\nB. 乙"
	input.Question.Type = "single"
	view, err := env.service.Answer(context.Background(), env.userID, "recovery-2", input)
	if err == nil || view.ID == "" {
		t.Fatal("等待窗口到点必须保留查询记录")
	}
	env.fork.statusErr = &ForkError{Code: "request_not_found", Message: "没有该请求记录", Status: 404}
	if _, err := env.pool.Exec(context.Background(),
		`UPDATE public.question_queries SET updated_at = now() - interval '30 minutes' WHERE id = $1`, view.ID); err != nil {
		t.Fatal(err)
	}
	if handled, err := env.service.RecoverStaleQueries(context.Background()); err != nil || handled != 1 {
		t.Fatalf("回收失败：%d/%v", handled, err)
	}
	var status, code string
	if err := env.pool.QueryRow(context.Background(),
		`SELECT status, COALESCE(error_code, '') FROM public.question_queries WHERE id = $1`, view.ID).
		Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" || code != "pending_record_missing" {
		t.Fatalf("记录消失时处理不符：%s/%s", status, code)
	}
	if time.Since(time.Now()) > time.Minute {
		t.Fatal("unreachable")
	}
}
