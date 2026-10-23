//go:build integration

package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type accountedFork struct {
	calls  atomic.Int64
	before func(context.Context)
}

func (f *accountedFork) Answer(ctx context.Context, r questions.ForkAnswerRequest, key string) (questions.ForkAnswerResponse, error) {
	f.calls.Add(1)
	if f.before != nil {
		f.before(ctx)
	}
	input, output, total := int64(100), int64(50), int64(150)
	return questions.ForkAnswerResponse{RequestID: r.RequestID, Status: "answered", Question: r.Question.Title, Answer: "A", Model: questions.ForkModel{OfferingID: r.ModelOfferingID, Capabilities: []string{"text"}}, Mode: r.Mode, Usage: questions.Usage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Complete: true, Calls: []questions.Call{{Slot: "answer", InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Status: "ok"}}}}, nil
}
func (*accountedFork) TestModel(context.Context, string, string, bool) (questions.ForkTestResult, error) {
	return questions.ForkTestResult{}, nil
}
func (*accountedFork) InvalidateCache(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestQuestionLedgerPreauthorizesAndRespectsOperatingSwitch(t *testing.T) {
	fork := &accountedFork{}
	fixture := newRuntimeFixture(t, fixtureOptions{fork: fork, charging: true})
	client := fixtureClient()
	defer client.CloseIdleConnections()
	csrf, userID := loginFixtureUser(t, fixture, client, "learner-a")
	ctx := context.Background()
	adminClient := fixtureClient()
	defer adminClient.CloseIdleConnections()
	code, _, session := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
	if code != 200 {
		t.Fatal("测试管理员登录失败", code)
	}
	admin := session["admin"].(map[string]any)
	actor := web.Actor{ID: admin["id"].(string), Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
	adminCSRF := session["csrf_token"].(string)
	code, _, model := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/question-models", adminCSRF, map[string]any{"display_name": "计费联调模型", "provider_ref": "controlled-fixture-model", "capabilities": []string{"text"}, "pricing": map[string]string{"version": "question-price-v1", "input_micros_per_million_tokens": "1000000", "output_micros_per_million_tokens": "2000000", "micros_per_minor": "10000", "rounding": "ceil", "rule": "tokens_x_micros_per_million_v1"}}, nil)
	if code != 201 {
		t.Fatal("价格发布失败", code, model)
	}
	modelID := model["id"].(string)
	wallet, err := fixture.runtime.Ledger.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.runtime.Ledger.AdjustWallet(ctx, actor, "question-flow-funds", ledger.WalletAdjustmentInput{WalletID: wallet.ID, Direction: "credit", AmountMinor: "1000", Reason: "隔离题库计费流程合成资金"}); err != nil {
		t.Fatal(err)
	}
	enabled := true
	settings, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, 1, ledger.BillingPatch{TikuChargeEnabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	requireReserve := true
	fork.before = func(ctx context.Context) {
		var amount int64
		if err := fixture.runtime.db.Pool().QueryRow(ctx, "SELECT reserved_minor FROM public.wallets WHERE user_id=$1", userID).Scan(&amount); err != nil {
			t.Fatal(err)
		}
		if requireReserve && amount <= 0 {
			t.Fatal("付费fork被调用前没有真实账本预占")
		}
		if !requireReserve && amount != 0 {
			t.Fatal("关闭自动收费后仍预占")
		}
	}
	body := map[string]any{"model_offering_id": modelID, "mode": "always_llm", "question": map[string]any{"title": "组合题库计量测试", "options": "A. 第一项\nB. 第二项", "type": "single", "images": []string{}}, "budget": map[string]int{"max_output_tokens": 100}}
	code, _, query := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, body, map[string]string{"Idempotency-Key": "real-ledger-question-001"})
	if code != 200 {
		t.Fatal("组合题库查询失败", code, query)
	}
	wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" {
		t.Fatal("实际token分账不正确", wallet.AvailableMinor, wallet.ReservedMinor)
	}
	requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, body, map[string]string{"Idempotency-Key": "real-ledger-question-001"})
	if fork.calls.Load() != 1 {
		t.Fatal("幂等重试重复调用了付费fork")
	}
	disabled := false
	if _, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, settings.Version, ledger.BillingPatch{TikuChargeEnabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	requireReserve = false
	code, _, query = requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, body, map[string]string{"Idempotency-Key": "real-ledger-question-002"})
	if code != 200 {
		t.Fatal("关闭收费被错误当作关闭查询", code, query)
	}
	wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" || fork.calls.Load() != 2 {
		t.Fatal("运营关闭收费后仍改变钱包")
	}
}
