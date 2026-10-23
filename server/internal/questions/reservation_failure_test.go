//go:build integration

package questions

import (
	"context"
	"testing"
)

// 数据库已提交预占但调用方未收到提交确认时，失败处理必须核对持久化事实并释放原预占。
func TestReservationFailureReleasesDurableHold(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true})
	book := attachedLedger(t, env)
	fundWallet(t, book, env, "1000")
	model := env.createOffering(t, "预占恢复模型", "reservation-recovery", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("预占提交确认中断", "always_llm")
	input.ModelOfferingID = model.ID
	_, _, queryID := seedStalePending(t, env, model, &input, "reservation-response-lost")
	ctx := context.Background()
	before, err := env.service.loadQueryAdmin(ctx, queryID)
	if err != nil || before == nil || before.BillingHoldID == "" {
		t.Fatalf("持久预占夹具未建立：%v", err)
	}
	frozen := parsePricingSnapshot(before.Pricing)
	wallet, err := book.GetWallet(ctx, env.userID)
	if err != nil || wallet.ReservedMinor == "0" {
		t.Fatalf("真实账本没有形成预占：%v", err)
	}
	if err := env.service.markReserveFailed(queryID, "service_unavailable"); err != nil {
		t.Fatal(err)
	}
	wallet, err = book.GetWallet(ctx, env.userID)
	if err != nil || wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("预占失败后资金未恢复：available=%s reserved=%s err=%v", wallet.AvailableMinor, wallet.ReservedMinor, err)
	}
	after, err := env.service.loadQueryAdmin(ctx, queryID)
	if err != nil || after == nil || after.Status != "failed" || after.BillingState != "released" || after.BillingHoldID != before.BillingHoldID {
		t.Fatalf("查询未保留已释放的原预占事实：%#v/%v", after, err)
	}
	updated := parsePricingSnapshot(after.Pricing)
	if updated.Pricing == nil || frozen.Pricing == nil || updated.Pricing.Version != frozen.Pricing.Version || updated.EstimatedAmountMinor != frozen.EstimatedAmountMinor {
		t.Fatalf("预占释放丢失冻结价格：%#v/%#v", updated, frozen)
	}
	var journalsBefore, journalsAfter int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM public.journals`).Scan(&journalsBefore); err != nil {
		t.Fatal(err)
	}
	if err := env.service.markReserveFailed(queryID, "service_unavailable"); err != nil {
		t.Fatalf("重复失败处理应保持幂等：%v", err)
	}
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM public.journals`).Scan(&journalsAfter); err != nil {
		t.Fatal(err)
	}
	_, err = env.service.Answer(ctx, env.userID, "reservation-response-lost", input)
	if err == nil || journalsBefore != journalsAfter || env.fork.callCount() != 0 {
		t.Fatalf("失败重放产生新资金变动或模型调用：journals=%d/%d fork=%d err=%v", journalsBefore, journalsAfter, env.fork.callCount(), err)
	}
}
