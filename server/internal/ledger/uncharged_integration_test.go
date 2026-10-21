//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestUnchargedUnpricedFactIsNotRepricedLater(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	confirmedAt := clock.Now()
	record := func(key string) *UsageRecord {
		var usage *UsageRecord
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			var err error
			usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
				UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: key,
				QuantityScaled: 1, UsageComplete: true, Uncharged: true,
				PlatformConfirmedAt: &confirmedAt, PlatformEvidenceRef: "platform-confirm",
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return usage
	}
	before := record("task:unpriced:before")
	if before.Status != "unpriced" || before.PriceVersionID != "" ||
		before.QuoteMicros != "0" || before.QuoteMinor != "0" {
		t.Fatalf("未定价确认事实错误: %+v", before)
	}
	publishServicePrice(t, service, admin, 1_000_000)
	after := record("task:unpriced:after")
	if after.Status != "unpriced" || after.PriceVersionID != "" {
		t.Fatalf("发布新价格后未定价确认事实被改价: %+v", after)
	}
	again := record("task:unpriced:before")
	if again.ID != before.ID || again.Status != "unpriced" {
		t.Fatalf("未定价确认事实重复记录未复用: %+v", again)
	}
	current, err := service.QuoteServiceTaskPoint(ctx, userID, "current", 1, "")
	if err != nil || current.State != "quoted" || current.ChargeMinor != "100" {
		t.Fatalf("当前价格应可单独报价: %+v %v", current, err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:unpriced:before",
			QuantityScaled: 1, UsageComplete: true, Uncharged: false,
			PlatformConfirmedAt: &confirmedAt,
		})
		return err
	}); err == nil {
		t.Fatal("相同用量键从 Uncharged 改为可收费未冲突")
	} else if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("Uncharged 摘要冲突错误: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.ReserveUsage(ctx, tx, before.ID)
		return err
	}); err == nil {
		t.Fatal("未定价确认事实仍可预占")
	}
	var settlement UsageSettlement
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		settlement, err = service.SettleUsage(ctx, tx, before.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if settlement.State == "settled" && settlement.ChargedMinor != "0" {
		t.Fatalf("未定价事实被扣费: %+v", settlement)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("未定价事实改变钱包: %+v %v", wallet, err)
	}
	var holds, captures int
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind = 'hold'), count(*) FILTER (WHERE kind = 'capture')
		FROM public.journals`).Scan(&holds, &captures); err != nil || holds != 0 || captures != 0 {
		t.Fatalf("未定价事实产生预占/扣费: %d %d %v", holds, captures, err)
	}
	_ = wallet
}

func TestUnchargedFrozenVersionShowsOriginalCostButNeverCharges(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	publishServicePrice(t, service, admin, 1_000_000)
	var v1 string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.price_versions
		WHERE scope = 'service_task_point' AND status = 'published'`).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	publishServicePrice(t, service, admin, 2_000_000)
	var v2 string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.price_versions
		WHERE scope = 'service_task_point' AND status = 'published'`).Scan(&v2); err != nil {
		t.Fatal(err)
	}
	if v1 == v2 {
		t.Fatal("价格版本未变化")
	}
	confirmedAt := clock.Now()
	record := func(key, priceVersionID string) *UsageRecord {
		var usage *UsageRecord
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			var err error
			usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
				UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: key,
				QuantityScaled: 1, UsageComplete: true, Uncharged: true,
				PriceVersionID: priceVersionID, PlatformConfirmedAt: &confirmedAt,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return usage
	}
	usage := record("task:frozen:v1", v1)
	if usage.Status != "disabled" || usage.PriceVersionID != v1 ||
		usage.PriceVersion != 1 || usage.QuoteMicros != "1000000" || usage.QuoteMinor != "100" {
		t.Fatalf("冻结原版未按 disabled 保留成本: %+v", usage)
	}
	again := record("task:frozen:v1", v1)
	if again.ID != usage.ID {
		t.Fatalf("冻结原版确认事实未幂等: %s %s", again.ID, usage.ID)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.ReserveUsage(ctx, tx, usage.ID)
		return err
	}); err == nil {
		t.Fatal("disabled 冻结原版仍可预占")
	}
	var settlement UsageSettlement
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		settlement, err = service.SettleUsage(ctx, tx, usage.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if settlement.ChargedMinor != "0" {
		t.Fatalf("disabled 冻结原版被扣费: %+v", settlement)
	}
	// 显式零价版本：仍以 disabled 记录事实，成本为 0，不产生扣费。
	publishServicePrice(t, service, admin, 0)
	var v3 string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.price_versions
		WHERE scope = 'service_task_point' AND status = 'published'`).Scan(&v3); err != nil {
		t.Fatal(err)
	}
	zero := record("task:frozen:zero", v3)
	if zero.Status != "disabled" || zero.QuoteMicros != "0" || zero.QuoteMinor != "0" {
		t.Fatalf("显式零价确认事实错误: %+v", zero)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.SettleUsage(ctx, tx, zero.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("冻结原版/零价改变钱包: %+v %v", wallet, err)
	}
	var holds, captures int
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind = 'hold'), count(*) FILTER (WHERE kind = 'capture')
		FROM public.journals`).Scan(&holds, &captures); err != nil || holds != 0 || captures != 0 {
		t.Fatalf("冻结原版/零价产生预占/扣费: %d %d %v", holds, captures, err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:frozen:missing",
			QuantityScaled: 1, UsageComplete: true, Uncharged: true,
			PriceVersionID: web.NewID(), PlatformConfirmedAt: &confirmedAt,
		})
		return err
	}); err == nil {
		t.Fatal("不存在的冻结价格版本被接受")
	} else if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("冻结版本缺失错误: %v", err)
	}
}

func TestUnchargedFactSurvivesDisabledChargingAndGateFailure(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	publishServicePrice(t, service, admin, 1_000_000)
	var priceID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.price_versions
		WHERE scope = 'service_task_point' AND status = 'published'`).Scan(&priceID); err != nil {
		t.Fatal(err)
	}
	service.chargingAllowed = boolPtr(false)
	service.gate = func(context.Context) error {
		return fault.New("service_unavailable", "备份新鲜度不足")
	}
	confirmedAt := clock.Now()
	record := func(key, version string) *UsageRecord {
		var usage *UsageRecord
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			var err error
			usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
				UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: key,
				QuantityScaled: 1, UsageComplete: true, Uncharged: true,
				PriceVersionID: version, PlatformConfirmedAt: &confirmedAt,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return usage
	}
	unpriced := record("disabled:unpriced", "")
	if unpriced.Status != "unpriced" {
		t.Fatalf("收费关闭时未定价事实被抹掉: %+v", unpriced)
	}
	frozen := record("disabled:frozen", priceID)
	if frozen.Status != "disabled" || frozen.PriceVersionID != priceID ||
		frozen.QuoteMinor != "100" {
		t.Fatalf("收费关闭时冻结原版事实错误: %+v", frozen)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("收费关闭时事实改变钱包: %+v %v", wallet, err)
	}
	var holds, captures int
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind = 'hold'), count(*) FILTER (WHERE kind = 'capture')
		FROM public.journals`).Scan(&holds, &captures); err != nil || holds != 0 || captures != 0 {
		t.Fatalf("收费关闭时事实产生预占/扣费: %d %d %v", holds, captures, err)
	}
}
