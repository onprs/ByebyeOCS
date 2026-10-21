//go:build integration

package ledger

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestServicePriceQuoteAndSettlement(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	fundedWallet(t, service, admin, userID, 1000)
	publishServicePrice(t, service, admin, 1_000_000) // 1 元/任务点

	quote, err := service.QuoteServiceTaskPoint(ctx, userID, "task:1", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "quoted" || quote.ChargeMinor != "100" || quote.PriceVersion != 1 {
		t.Fatalf("服务报价错误: %+v", quote)
	}
	confirmedAt := clock.Now()
	var usage *UsageRecord
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:1",
			QuantityScaled: 1, UsageComplete: true, PlatformConfirmedAt: &confirmedAt,
			PlatformEvidenceRef: "platform-query:1", PriceVersionID: quote.PriceVersionID,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if usage.Status != "quoted" || usage.QuoteMinor != "100" {
		t.Fatalf("用量记录错误: %+v", usage)
	}
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.ReserveUsage(ctx, tx, usage.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var settlement UsageSettlement
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		settlement, err = service.SettleUsage(ctx, tx, usage.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if settlement.State != "settled" || settlement.ChargedMinor != "100" || settlement.SettleJournalID == "" {
		t.Fatalf("服务结算错误: %+v", settlement)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "900" || wallet.ReservedMinor != "0" {
		t.Fatalf("结算后钱包错误: %+v", wallet)
	}
	var holdStatus string
	var settled, released int64
	if err := pool.QueryRow(ctx, `SELECT status, settled_minor, released_minor FROM public.holds WHERE id = $1`, holdID).
		Scan(&holdStatus, &settled, &released); err != nil || holdStatus != "settled" || settled != 100 || released != 0 {
		t.Fatalf("预占状态错误: %s %d %d %v", holdStatus, settled, released, err)
	}
	entries, err := service.ListWalletEntries(ctx, wallet.ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	foundCapture := false
	for _, entry := range entries.Items {
		if entry.Kind == "capture" {
			if entry.UsageID != usage.ID || entry.PriceVersionID != quote.PriceVersionID {
				t.Fatalf("结算流水缺少用量/价格关联: %+v", entry)
			}
			foundCapture = true
		}
	}
	if !foundCapture {
		t.Fatal("钱包流水缺少结算记录")
	}
	// 重复记录相同用量复用；不同数量返回冲突。
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		again, err := service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:1",
			QuantityScaled: 1, UsageComplete: true, PlatformConfirmedAt: &confirmedAt,
			PlatformEvidenceRef: "platform-query:1", PriceVersionID: quote.PriceVersionID,
		})
		if err != nil {
			return err
		}
		if again.ID != usage.ID {
			t.Fatalf("相同用量键未复用: %s != %s", again.ID, usage.ID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:1",
			QuantityScaled: 2, UsageComplete: true, PlatformConfirmedAt: &confirmedAt,
		})
		return err
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("相同用量键不同内容未冲突: %v", err)
	}
}

func TestPriceVersionImmutableAndETagVersion(t *testing.T) {
	service, pool, _, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	publishServicePrice(t, service, admin, 1_000_000)
	settings, err := service.GetBillingSettings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBillingSettings(ctx, admin, settings.Version-1, BillingPatch{}); err == nil {
		t.Fatal("过期版本未冲突")
	} else if code, _ := fault.Describe(err); code != "version_conflict" {
		t.Fatalf("过期版本错误码: %v", err)
	}
	// 发布第二版后旧版只允许 retired，价格字段不可改。
	publishServicePrice(t, service, admin, 2_000_000)
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.price_versions WHERE scope = 'service_task_point'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("价格版本数量错误: %d,%v", count, err)
	}
	var oldID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.price_versions WHERE scope = 'service_task_point' AND status = 'retired'`).Scan(&oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.price_versions SET price_micros = 1 WHERE id = $1`, oldID); err == nil {
		t.Fatal("价格版本被修改")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.price_versions WHERE id = $1`, oldID); err == nil {
		t.Fatal("价格版本被删除")
	}
}

func TestUnconfiguredPriceDoesNotCharge(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 1000)
	quote, err := service.QuoteServiceTaskPoint(ctx, userID, "task:none", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "unpriced" || quote.Chargeable {
		t.Fatalf("未配置价格报价错误: %+v", quote)
	}
	var usage *UsageRecord
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
			UserID: userID, Scope: ScopeServiceTaskPoint, StableKey: "task:none", QuantityScaled: 1,
			UsageComplete: true,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if usage.Status != "unpriced" {
		t.Fatalf("未定价用量状态错误: %+v", usage)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.ReserveUsage(ctx, tx, usage.ID)
		return err
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("未定价用量仍可预占: %v", err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("未定价产生扣费: %+v", wallet)
	}
	var journals int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.journals WHERE kind IN ('capture','hold')`).Scan(&journals); err != nil || journals != 0 {
		t.Fatalf("未定价产生账本分录: %d,%v", journals, err)
	}
}

func TestTikuThresholdAccrualAndIncompleteUsage(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	fundedWallet(t, service, admin, userID, 1000)
	publishTikuPrice(t, service, admin, 1_000_000, 0, 10000)

	run := func(key string, tokens int64, complete bool) (UsageSettlement, *UsageRecord) {
		var usage *UsageRecord
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			var err error
			usage, err = service.RecordUsage(ctx, tx, RecordUsageInput{
				UserID: userID, Scope: ScopeTikuTokens, StableKey: key,
				InputTokens: tokens, OutputTokens: 0, UsageComplete: complete,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			_, err := service.ReserveUsage(ctx, tx, usage.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var settlement UsageSettlement
		if err := service.inTx(ctx, func(tx pgx.Tx) error {
			var err error
			settlement, err = service.SettleUsage(ctx, tx, usage.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return settlement, usage
	}
	first, _ := run("q1", 6000, true)
	if first.State != "settled" || first.ChargedMinor != "0" {
		t.Fatalf("首次题库结算错误: %+v", first)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.TikuAccruedMicros != "6000" || wallet.AvailableMinor != "1000" {
		t.Fatalf("首次尾差错误: %+v", wallet)
	}
	second, _ := run("q2", 6000, true)
	if second.State != "settled" || second.ChargedMinor != "1" {
		t.Fatalf("第二次题库结算错误: %+v", second)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.TikuAccruedMicros != "2000" || wallet.AvailableMinor != "999" {
		t.Fatalf("第二次尾差错误: %+v", wallet)
	}
	incomplete, _ := run("q3", 6000, false)
	if incomplete.State != "needs_review" || incomplete.ChargedMinor != "0" || incomplete.ErrorCode != "usage_incomplete" {
		t.Fatalf("不完整用量结算错误: %+v", incomplete)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.TikuAccruedMicros != "2000" || wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" {
		t.Fatalf("不完整用量产生扣费: %+v", wallet)
	}
	var holds int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.holds WHERE status = 'open'`).Scan(&holds); err != nil || holds != 0 {
		t.Fatalf("仍有未释放预占: %d,%v", holds, err)
	}
}

func TestQuoteOverflowRejected(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	publishTikuPrice(t, service, adminActor(service, adminID), 1_000_000, 1_000_000, 10000)
	if _, err := service.QuoteTikuUsage(context.Background(), userID, "overflow", math.MaxInt64, math.MaxInt64, ""); err == nil {
		t.Fatal("超范围 token 报价未失败")
	}
}
