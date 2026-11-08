//go:build integration

package questions

import (
	"context"
	"testing"
)

func TestWorkOutputBudgetFreezesNewQueriesAndPreservesLegacy(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.service.maxOut = 32768
	model := env.createOffering(t, "预算模型", "budget-model", nil, true)
	env.catalogue.mu.Lock()
	env.catalogue.offerings[0].MaxOutputTokens = 32000
	env.catalogue.mu.Unlock()
	const key = "work-budget-frozen-001"
	ctx := context.Background()
	budget, err := env.service.WorkOutputBudget(ctx, env.userID, key, model.ID)
	if err != nil || budget != 32000 {
		t.Fatalf("未使用模型能力：%d/%v", budget, err)
	}
	input := queryBody("完整预算题", "always_llm")
	input.ModelOfferingID = model.ID
	input.Budget.MaxOutputTokens = budget
	mustAnswer(t, env, key, input)
	env.catalogue.mu.Lock()
	env.catalogue.offerings[0].MaxOutputTokens = 16000
	env.catalogue.mu.Unlock()
	frozen, err := env.service.WorkOutputBudget(ctx, env.userID, key, model.ID)
	if err != nil || frozen != 32000 {
		t.Fatalf("历史预算改变：%d/%v", frozen, err)
	}
	mustAnswer(t, env, key, input)
	if env.fork.callCount() != 1 {
		t.Fatal("读取冻结预算重复调用模型")
	}
	next, err := env.service.WorkOutputBudget(ctx, env.userID, "work-budget-new-002", model.ID)
	if err != nil || next != 16000 {
		t.Fatalf("新查询未使用更新后的上限：%d/%v", next, err)
	}
	if _, err = env.pool.Exec(ctx, `UPDATE public.question_queries SET budget_output_tokens=NULL WHERE user_id=$1 AND idempotency_key=$2`, env.userID, key); err != nil {
		t.Fatal(err)
	}
	legacy, err := env.service.WorkOutputBudget(ctx, env.userID, key, model.ID)
	if err != nil || legacy != 2048 {
		t.Fatalf("历史自动做题预算未保留：%d/%v", legacy, err)
	}
}
