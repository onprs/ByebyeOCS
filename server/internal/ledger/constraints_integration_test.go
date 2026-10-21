//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestJournalAndPostingAreAppendOnlyAndBalanced(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 1000)
	var journalID, postingID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.journals WHERE kind = 'manual_credit' LIMIT 1`).Scan(&journalID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.postings WHERE journal_id = $1 LIMIT 1`, journalID).Scan(&postingID); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE public.journals SET reason = 'changed' WHERE id = '` + journalID + `'`,
		`DELETE FROM public.journals WHERE id = '` + journalID + `'`,
		`UPDATE public.postings SET amount_minor = amount_minor + 1 WHERE id = '` + postingID + `'`,
		`DELETE FROM public.postings WHERE id = '` + postingID + `'`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Fatalf("账本记录被修改或删除: %s", statement)
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	journalID = newUUID()
	if _, err := tx.Exec(ctx, `INSERT INTO public.journals (id, kind, business_key, request_digest,
		currency, amount_minor, reason) VALUES ($1,'manual_credit',$2,repeat('a',64),'CNY',100,'不平衡测试')`,
		journalID, "unbalanced-"+journalID); err != nil {
		t.Fatal(err)
	}
	var accountID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM public.ledger_accounts WHERE kind = 'platform_adjustment'`).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.postings (id, journal_id, account_id, amount_minor)
		VALUES ($1,$2,$3,100)`, newUUID(), journalID, accountID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("不平衡分录被提交")
	}
}

func TestWalletEntriesCursorAndOrderFilters(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "entry-hold", 400)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "entry-settle", 200)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "entry-release")
	}); err != nil {
		t.Fatal(err)
	}
	first, err := service.ListWalletEntries(ctx, wallet.ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("钱包流水分页错误: %+v", first)
	}
	second, err := service.ListWalletEntries(ctx, wallet.ID, first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) == 0 {
		t.Fatal("第二页为空")
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.JournalID] {
			t.Fatalf("分页重复记录: %s", item.JournalID)
		}
		seen[item.JournalID] = true
	}
	enableCheckout(t, pool)
	createOrder(t, service, userID, "filter-1", 3000)
	createOrder(t, service, userID, "filter-2", 4000)
	createOrder(t, service, userID, "filter-3", 5000)
	page, err := service.ListAdminOrders(ctx, admin, "", "unpaid", "", "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("管理员订单分页错误: %+v", page)
	}
	if page.Items[0].AmountMinor == "" || page.Items[0].PaymentStatus != "unpaid" {
		t.Fatalf("订单字段错误: %+v", page.Items[0])
	}
}

func TestRechargeOrderServerValidationAndOwnership(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	otherUser := newUser(t, pool)
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "disabled", 3000, "alipay"); err == nil {
		t.Fatal("支付未配置时创建订单")
	} else if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("未配置支付错误: %v", err)
	}
	enableCheckout(t, pool)
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "small", 99, "alipay"); err == nil {
		t.Fatal("低于下限的充值被接受")
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "large", 100001, "alipay"); err == nil {
		t.Fatal("高于上限的充值被接受")
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "channel", 3000, "card"); err == nil {
		t.Fatal("未知渠道被接受")
	}
	order := createOrder(t, service, userID, "ownership", 3000)
	if _, err := service.GetRechargeOrder(ctx, userActor(otherUser), order.ID); err == nil {
		t.Fatal("其他用户读取了订单")
	} else if code, _ := fault.Describe(err); code != "not_found" {
		t.Fatalf("订单归属错误: %v", err)
	}
	admin := adminActor(service, adminID)
	page, err := service.ListAdminOrders(ctx, admin, "", "", "", otherUser, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatal("管理员用户过滤返回了他人订单")
	}
}

func TestWalletNeverNegativeAndHoldOverdraw(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 100)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "small-hold", 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "over-settle", 101)
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("超额结算未拒绝: %v", err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.wallets SET available_minor = -1 WHERE id = $1`, wallet.ID); err == nil {
		t.Fatal("钱包可用余额允许负数")
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "no-funds", 3000, "alipay"); err == nil {
		t.Fatal("支付未启用时创建订单")
	}
}
