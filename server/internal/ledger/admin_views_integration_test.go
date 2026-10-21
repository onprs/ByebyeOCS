//go:build integration

package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestAdminUserWalletCreatesRealWalletAndSearch(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	fundedWallet(t, service, admin, userID, 1000)
	addDisplayIdentity(t, pool, userID, "张三")
	otherUser := newUser(t, pool)
	fundedWallet(t, service, admin, otherUser, 2000)
	addDisplayIdentity(t, pool, otherUser, "李四")
	noWalletUser := newUser(t, pool)
	addDisplayIdentity(t, pool, noWalletUser, "王五")

	result, err := service.AdminUserWallet(ctx, admin, userID)
	if err != nil {
		t.Fatal(err)
	}
	if result.DisplayName != "张三" || result.WalletCreated || result.Wallet.AvailableMinor != "1000" {
		t.Fatalf("已有用户钱包读取错误: %+v", result)
	}
	result, err = service.AdminUserWallet(ctx, admin, noWalletUser)
	if err != nil {
		t.Fatal(err)
	}
	if !result.WalletCreated || result.Wallet.AvailableMinor != "0" || result.Wallet.ID == "" {
		t.Fatalf("无钱包用户未真实创建零余额钱包: %+v", result)
	}
	second, err := service.AdminUserWallet(ctx, admin, noWalletUser)
	if err != nil || second.WalletCreated {
		t.Fatalf("第二次读取不应重复创建: %+v %v", second, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.wallets WHERE user_id = $1`,
		noWalletUser).Scan(&count); err != nil || count != 1 {
		t.Fatalf("钱包创建数量错误: %d %v", count, err)
	}
	if _, err := service.AdminUserWallet(ctx, admin, web.NewID()); err == nil {
		t.Fatal("不存在用户返回了钱包")
	} else if code, _ := fault.Describe(err); code != "not_found" {
		t.Fatalf("不存在用户错误码: %v", err)
	}
	firstWallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, search := range []string{"张三", userID, firstWallet.ID, "张"} {
		page, err := service.ListAdminWallets(ctx, admin, search, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Items[0].UserID != userID ||
			page.Items[0].DisplayName != "张三" || page.Items[0].Wallet.ID != firstWallet.ID {
			t.Fatalf("搜索 %q 结果错误: %+v", search, page)
		}
	}
	page, err := service.ListAdminWallets(ctx, admin, "不存在", "", 20)
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("无匹配搜索错误: %+v %v", page, err)
	}
	page, err = service.ListAdminWallets(ctx, admin, "", "", 1)
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("钱包列表分页错误: %+v %v", page, err)
	}
	secondPage, err := service.ListAdminWallets(ctx, admin, "", page.NextCursor, 10)
	if err != nil || len(secondPage.Items) != 2 {
		t.Fatalf("钱包列表第二页错误: %+v %v", secondPage, err)
	}
	if _, err := service.ListAdminWallets(ctx, userActor(userID), "", "", 10); err == nil {
		t.Fatal("普通用户可读取管理员钱包列表")
	}
}

func insertFinanceOrder(t *testing.T, pool *pgxpool.Pool, userID, walletID string, index int, amount int64, createdAt time.Time,
	paidAt, creditedAt *time.Time, paymentStatus, creditStatus, status string) string {
	t.Helper()
	orderID := web.NewID()
	var paid, credited, trade any
	if paidAt != nil {
		paid = *paidAt
		trade = fmt.Sprintf("trade-finance-%d", index)
	}
	if creditedAt != nil {
		credited = *creditedAt
	}
	requestDigest := digest("finance-order", strconv.Itoa(index))
	merchantOrderNo := fmt.Sprintf("9%029d", index)
	expiresAt := createdAt.Add(30 * time.Minute)
	_, err := pool.Exec(context.Background(), `INSERT INTO public.recharge_orders (
		id, user_id, wallet_id, amount_minor, currency, channel, status, payment_status,
		credit_status, provider, merchant_order_no, provider_trade_no, idempotency_key,
		request_digest, late_payment, created_at, updated_at, expires_at, paid_at,
		credited_at, version
	) VALUES ($1,$2,$3,$4,'CNY','alipay',$5,$6,$7,'zpay',$8,$9,$10,$11,false,$12,$12,$13,$14,$15,1)`,
		orderID, userID, walletID, amount, status, paymentStatus, creditStatus,
		merchantOrderNo, trade, fmt.Sprintf("finance-finance-%d", index), requestDigest,
		createdAt, expiresAt, paid, credited)
	if err != nil {
		t.Fatal(err)
	}
	return orderID
}

func TestFinanceSummaryFullAndRange(t *testing.T) {
	service, pool, userID, adminID, provider, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	base := clock.Now()
	paid1 := base.Add(5 * time.Minute)
	credited1 := base.Add(5 * time.Minute)
	order1 := insertFinanceOrder(t, pool, userID, wallet.ID, 1, 3000, base, &paid1, &credited1,
		"paid", "credited", "completed")
	paid2 := base.Add(15 * time.Minute)
	insertFinanceOrder(t, pool, userID, wallet.ID, 2, 2000, base.Add(10*time.Minute),
		&paid2, nil, "paid", "not_credited", "needs_review")
	insertFinanceOrder(t, pool, userID, wallet.ID, 3, 1000, base.Add(20*time.Minute),
		nil, nil, "unpaid", "not_credited", "pending_payment")

	if _, err := service.AdjustWallet(ctx, admin, "finance-adjust", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "500", Reason: "财务汇总测试补记",
	}); err != nil {
		t.Fatal(err)
	}
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "finance-hold", 200)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "finance-capture", 100)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "finance-release")
	}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Hour)
	freshAdmin := adminActor(service, adminID)
	if _, err := service.CreateRefund(ctx, freshAdmin, "finance-refund", RefundInput{
		OrderID: order1, Kind: "abnormal_payment", AmountMinor: "100", Reason: "财务汇总异常退款",
	}); err != nil {
		t.Fatal(err)
	}

	all, err := service.FinanceSummary(ctx, admin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !all.Range.AllTime || !all.Range.ToExclusive || all.Range.From != "" || all.Range.To != "" {
		t.Fatalf("全量范围标记错误: %+v", all.Range)
	}
	if all.RangeMetrics.OrdersCreated.Count != 3 || all.RangeMetrics.OrdersCreated.AmountMinor != "6000" ||
		all.RangeMetrics.PaymentSucceeded.Count != 2 || all.RangeMetrics.PaymentSucceeded.AmountMinor != "5000" ||
		all.RangeMetrics.PaymentCredited.Count != 1 || all.RangeMetrics.PaymentCredited.AmountMinor != "3000" ||
		all.RangeMetrics.RefundSucceeded.Count != 1 || all.RangeMetrics.RefundSucceeded.AmountMinor != "100" ||
		all.RangeMetrics.LedgerCaptured.Count != 1 || all.RangeMetrics.LedgerCaptured.AmountMinor != "100" ||
		all.RangeMetrics.LedgerRefundPayout.Count != 1 || all.RangeMetrics.LedgerRefundPayout.AmountMinor != "100" ||
		all.RangeMetrics.LedgerManualCredit.Count != 2 || all.RangeMetrics.LedgerManualCredit.AmountMinor != "1500" {
		t.Fatalf("全量财务汇总错误: %+v", all.RangeMetrics)
	}
	if all.Current.PaidNotCredited.Count != 1 || all.Current.PaidNotCredited.AmountMinor != "2000" ||
		all.Current.OrdersNeedsReview.Count != 1 || all.Current.OrdersNeedsReview.AmountMinor != "2000" ||
		all.Current.Wallets.Count != 1 || all.Current.Wallets.AvailableMinor != "1300" ||
		all.Current.Wallets.ReservedMinor != "0" || all.Current.Wallets.FrozenCount != 0 {
		t.Fatalf("当前财务状态错误: %+v", all.Current)
	}

	from := base
	to := base.Add(15 * time.Minute)
	range1, err := service.FinanceSummary(ctx, admin, &from, &to)
	if err != nil {
		t.Fatal(err)
	}
	if range1.Range.AllTime || range1.Range.From == "" || range1.Range.To == "" ||
		range1.RangeMetrics.OrdersCreated.Count != 2 || range1.RangeMetrics.OrdersCreated.AmountMinor != "5000" ||
		range1.RangeMetrics.PaymentSucceeded.Count != 1 || range1.RangeMetrics.PaymentSucceeded.AmountMinor != "3000" ||
		range1.RangeMetrics.PaymentCredited.Count != 1 || range1.RangeMetrics.RefundSucceeded.Count != 0 ||
		range1.RangeMetrics.LedgerCaptured.Count != 1 || range1.RangeMetrics.LedgerRefundPayout.Count != 0 ||
		range1.RangeMetrics.LedgerManualCredit.Count != 2 {
		t.Fatalf("时间范围汇总错误: %+v", range1.RangeMetrics)
	}
	from2 := base.Add(15 * time.Minute)
	to2 := base.Add(61 * time.Minute)
	range2, err := service.FinanceSummary(ctx, admin, &from2, &to2)
	if err != nil {
		t.Fatal(err)
	}
	if range2.RangeMetrics.OrdersCreated.Count != 1 || range2.RangeMetrics.OrdersCreated.AmountMinor != "1000" ||
		range2.RangeMetrics.PaymentSucceeded.Count != 1 || range2.RangeMetrics.PaymentSucceeded.AmountMinor != "2000" ||
		range2.RangeMetrics.PaymentCredited.Count != 0 ||
		range2.RangeMetrics.RefundSucceeded.Count != 1 || range2.RangeMetrics.RefundSucceeded.AmountMinor != "100" ||
		range2.RangeMetrics.LedgerRefundPayout.Count != 1 || range2.RangeMetrics.LedgerCaptured.Count != 0 ||
		range2.RangeMetrics.LedgerManualCredit.Count != 0 {
		t.Fatalf("边界范围汇总错误: %+v", range2.RangeMetrics)
	}
	if _, err := service.FinanceSummary(ctx, admin, &to, &from); err == nil {
		t.Fatal("倒置时间范围被接受")
	}
}

func TestFinanceSummaryDoesNotCapAtRecent100(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 0)
	base := clock.Now()
	for index := 1; index <= 105; index++ {
		insertFinanceOrder(t, pool, userID, wallet.ID, index, 100, base.Add(time.Duration(index)*time.Second),
			nil, nil, "unpaid", "not_credited", "pending_payment")
	}
	summary, err := service.FinanceSummary(ctx, admin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RangeMetrics.OrdersCreated.Count != 105 {
		t.Fatalf("财务汇总被截断: %+v", summary.RangeMetrics.OrdersCreated)
	}
}

func TestAdminRefundListPaginationAndFilters(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order1 := createOrder(t, service, userID, "refund-list-1", 3000)
	if err := service.processPayment(ctx, paidFact(order1, "trade-refund-list-1", 3000)); err != nil {
		t.Fatal(err)
	}
	refund1, err := service.CreateRefund(ctx, admin, "refund-list-1", RefundInput{
		OrderID: order1.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "退款列表测试一",
	})
	if err != nil {
		t.Fatal(err)
	}
	order2 := createOrder(t, service, userID, "refund-list-2", 4000)
	if err := service.processPayment(ctx, paidFact(order2, "trade-refund-list-2", 4000)); err != nil {
		t.Fatal(err)
	}
	refund2, err := service.CreateRefund(ctx, admin, "refund-list-2", RefundInput{
		OrderID: order2.ID, Kind: "abnormal_payment", AmountMinor: "500", Reason: "退款列表测试二",
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.ListAdminRefunds(ctx, admin, "", "", userID, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatalf("退款列表分页错误: %+v", page)
	}
	second, err := service.ListAdminRefunds(ctx, admin, "", "", userID, page.NextCursor, 10)
	if err != nil || len(second.Items) != 1 {
		t.Fatalf("退款列表第二页错误: %+v %v", second, err)
	}
	seen := map[string]bool{page.Items[0].ID: true}
	if seen[second.Items[0].ID] {
		t.Fatal("退款列表分页重复")
	}
	if second.Items[0].ID != refund1.ID && second.Items[0].ID != refund2.ID {
		t.Fatalf("退款列表返回了错误记录: %+v", second.Items[0])
	}
	filtered, err := service.ListAdminRefunds(ctx, admin, "succeeded", order1.ID, userID, "", 10)
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ID != refund1.ID {
		t.Fatalf("退款订单过滤错误: %+v %v", filtered, err)
	}
	empty, err := service.ListAdminRefunds(ctx, admin, "failed", "", userID, "", 10)
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("退款状态过滤错误: %+v %v", empty, err)
	}
	if _, err := service.ListAdminRefunds(ctx, userActor(userID), "", "", "", "", 10); err == nil {
		t.Fatal("普通用户可读取管理员退款记录")
	}
}

func TestAdminViewRoutesRegistered(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	mux := http.NewServeMux()
	service.RegisterAdmin(mux)
	admin := adminActor(service, adminID)
	for _, path := range []string{
		"/admin/api/v1/users/" + userID + "/wallet",
		"/admin/api/v1/wallets?search=",
		"/admin/api/v1/refunds",
		"/admin/api/v1/finance/summary",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request = request.WithContext(web.WithActor(request.Context(), admin))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("路由 %s 返回 %d %s", path, recorder.Code, recorder.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("路由 %s 返回非 JSON: %v", path, err)
		}
	}
}
