//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestTikuThresholdMustBe10000(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	settings, err := service.GetBillingSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.UpdateBillingSettings(ctx, admin, settings.Version, BillingPatch{
		TikuPrice: &TikuPricePatch{
			InputMicrosPerMillion: "1000000", OutputMicrosPerMillion: "1000000",
			SettlementRule: "accumulate_threshold", SettlementThresholdMicros: "20000",
			Rounding: "floor", UnitLabel: "token",
		},
	})
	if err == nil {
		t.Fatal("API 接受了 threshold=20000")
	} else if code, _ := fault.Describe(err); code != "validation_failed" {
		t.Fatalf("API 门槛错误码: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.price_versions
		WHERE scope = 'tiku_tokens'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("被拒绝的价格写入了版本: %d %v", count, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO public.price_versions (
		id, scope, version, status, currency, unit_label, unit_precision,
		input_micros_per_million, output_micros_per_million, settlement_rule, rounding,
		min_charge_minor, settlement_threshold_micros, published_at, published_by
	) VALUES ($1,'tiku_tokens',1,'published','CNY','token',0,1000000,1000000,
		'accumulate_threshold','floor',0,20000,now(),$2)`, web.NewID(), adminID)
	if err == nil {
		t.Fatal("数据库约束接受了 threshold=20000")
	}
	_ = userID
}

func TestLegacyTikuThresholdFailsClosed(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `ALTER TABLE public.price_versions
		DROP CONSTRAINT price_versions_tiku_threshold_mvp_check`); err != nil {
		t.Fatal(err)
	}
	priceID := web.NewID()
	if _, err := pool.Exec(ctx, `INSERT INTO public.price_versions (
		id, scope, version, status, currency, unit_label, unit_precision,
		input_micros_per_million, output_micros_per_million, settlement_rule, rounding,
		min_charge_minor, settlement_threshold_micros, published_at, published_by
	) VALUES ($1,'tiku_tokens',1,'published','CNY','token',0,1000000,1000000,
		'accumulate_threshold','floor',0,20000,now(),$2)`, priceID, adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.QuoteTikuUsage(ctx, userID, "legacy", 20000, 0, priceID); err == nil {
		t.Fatal("旧 threshold=20000 报价未 fail closed")
	} else if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("旧门槛报价错误: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeTikuTokens, StableKey: "legacy-usage",
			InputTokens: 20000, UsageComplete: true, PriceVersionID: priceID,
		})
		return err
	}); err == nil {
		t.Fatal("旧 threshold=20000 用量记录未 fail closed")
	} else if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("旧门槛用量记录错误: %v", err)
	}
}
