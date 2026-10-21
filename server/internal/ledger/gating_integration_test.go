//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestQuoteInTransactionUsesCallerTransaction(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	publishServicePrice(t, service, admin, 1_000_000)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	quote, err := service.QuoteInTransaction(ctx, tx, QuoteInput{
		Scope: ScopeServiceTaskPoint, UserID: userID, StableKey: "tx-quote", QuantityScaled: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "quoted" || quote.ChargeMinor != "100" || !quote.ChargingAllowed {
		t.Fatalf("事务内报价错误: %+v", quote)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.QuoteInTransaction(ctx, nil, QuoteInput{Scope: ScopeServiceTaskPoint}); err == nil {
		t.Fatal("nil 事务报价未被拒绝")
	}
}

func TestChargingAllowedFalseBlocksNewChargeButAllowsReleaseAndCallback(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	fundedWallet(t, service, admin, userID, 1000)
	publishServicePrice(t, service, admin, 1_000_000)
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "gated-order", 3000)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "gated-hold", 500)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	service.chargingAllowed = boolPtr(false)
	pricing, err := service.GetPricing(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pricing.ChargingAllowed || pricing.AutomaticCharging || pricing.Recharge.Enabled ||
		pricing.Service.State != "disabled" || (pricing.Tiku.Configured && pricing.Tiku.State != "disabled") {
		t.Fatalf("ChargingAllowed=false 未反映到公开元数据: %+v", pricing)
	}
	metadata, err := service.PublicMetadata(ctx)
	if err != nil || metadata.ChargingAllowed {
		t.Fatalf("PublicMetadata 未反映运行时授权: %+v %v", metadata, err)
	}
	quote, err := service.QuoteServiceTaskPoint(ctx, userID, "gated-quote", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if quote.Chargeable || quote.State != "disabled" || quote.ChargingAllowed {
		t.Fatalf("ChargingAllowed=false 仍可报价扣费: %+v", quote)
	}
	var usage *UsageRecord
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var usageErr error
		usage, usageErr = service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "gated-usage",
			QuantityScaled: 1, UsageComplete: true,
		})
		return usageErr
	}); err != nil {
		t.Fatal(err)
	}
	if usage.Status != "disabled" || usage.ChargedMinor != "0" {
		t.Fatalf("ChargingAllowed=false 仍记录为可扣费用量: %+v", usage)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.Reserve(ctx, tx, userID, "gated-new-hold", 100)
		return err
	})
	if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("ChargingAllowed=false 仍允许新预占: %v", err)
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "gated-new-order", 3000, "alipay"); err == nil {
		t.Fatal("ChargingAllowed=false 仍允许新充值订单")
	} else if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("新充值拒绝错误: %v", err)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "gated-capture", 100)
	})
	if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("ChargingAllowed=false 仍允许结算扣费: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "gated-release")
	}); err != nil {
		t.Fatalf("ChargingAllowed=false 阻止释放: %v", err)
	}
	if err := service.processPayment(ctx, paidFact(order, "trade-gated", 3000)); err != nil {
		t.Fatalf("ChargingAllowed=false 阻止已付款回调: %v", err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "4000" || wallet.ReservedMinor != "0" {
		t.Fatalf("释放/回调后余额错误: %+v", wallet)
	}
}

func TestGateFailureBlocksNewChargeButNotRelease(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	fundedWallet(t, service, admin, userID, 1000)
	publishServicePrice(t, service, admin, 1_000_000)
	enableCheckout(t, pool)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "gate-hold", 500)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	order := createOrder(t, service, userID, "gate-order", 3000)
	service.gate = func(context.Context) error {
		return fault.New("service_unavailable", "备份新鲜度不足")
	}
	err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.Reserve(ctx, tx, userID, "gate-new-hold", 100)
		return err
	})
	if code, _ := fault.Describe(err); code != "service_unavailable" {
		t.Fatalf("Gate 失败未阻止新预占: %v", err)
	}
	var usage *UsageRecord
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var usageErr error
		usage, usageErr = service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "gate-usage",
			QuantityScaled: 1, UsageComplete: true,
		})
		return usageErr
	}); err != nil {
		t.Fatalf("Gate 失败不应阻止记录用量事实: %v", err)
	}
	if usage.Status != "quoted" {
		t.Fatalf("Gate 失败时用量事实状态错误: %+v", usage)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, reserveErr := service.ReserveUsage(ctx, tx, usage.ID)
		return reserveErr
	}); err == nil {
		t.Fatal("Gate 失败未阻止用量预占")
	} else if code, _ := fault.Describe(err); code != "service_unavailable" {
		t.Fatalf("Gate 失败用量预占错误: %v", err)
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "gate-new-order", 3000, "alipay"); err == nil {
		t.Fatal("Gate 失败未阻止新充值")
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "gate-capture", 100)
	})
	if code, _ := fault.Describe(err); code != "service_unavailable" {
		t.Fatalf("Gate 失败未阻止结算扣费: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "gate-release")
	}); err != nil {
		t.Fatalf("Gate 失败阻止释放: %v", err)
	}
	if err := service.processPayment(ctx, paidFact(order, "trade-gate", 3000)); err != nil {
		t.Fatalf("Gate 失败阻止已付款回调: %v", err)
	}
}
