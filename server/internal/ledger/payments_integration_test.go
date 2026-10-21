//go:build integration

package ledger

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func paidFact(order *RechargeOrder, tradeNo string, amount int64) paymentFact {
	return paymentFact{MerchantOrderNo: order.MerchantOrderNo, ProviderTradeNo: tradeNo,
		AmountMinor: amount, Channel: order.Channel, ProviderStatus: "TRADE_SUCCESS", Source: "callback"}
}

func TestDuplicatePaymentCreditsOnce(t *testing.T) {
	service, pool, userID, _, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	_ = provider
	order := createOrder(t, service, userID, "order-1", 3000)
	again := createOrder(t, service, userID, "order-1", 3000)
	if again.ID != order.ID {
		t.Fatalf("订单幂等未复用: %s != %s", again.ID, order.ID)
	}
	if _, err := service.CreateRechargeOrder(ctx, userActor(userID), "order-1", 2900, "alipay"); err == nil {
		t.Fatal("相同订单幂等键不同金额未冲突")
	} else if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("订单幂等冲突错误: %v", err)
	}
	fact := paidFact(order, "trade-1", 3000)
	if err := service.processPayment(ctx, fact); err != nil {
		t.Fatal(err)
	}
	if err := service.processPayment(ctx, fact); err != nil {
		t.Fatal(err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("重复通知重复入账: %+v", wallet)
	}
	var journals, inbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.journals WHERE kind = 'recharge'`).Scan(&journals); err != nil || journals != 1 {
		t.Fatalf("充值分录数量错误: %d,%v", journals, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.payment_inbox`).Scan(&inbox); err != nil || inbox != 1 {
		t.Fatalf("支付通知数量错误: %d,%v", inbox, err)
	}
	credited, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if credited.CreditStatus != "credited" || credited.PaymentStatus != "paid" || credited.Status != "completed" {
		t.Fatalf("订单状态错误: %+v", credited)
	}
	// 同一订单第二笔真实交易只进入待核对，不能吞钱或重复入账。
	second := paidFact(order, "trade-2", 3000)
	if err := service.processPayment(ctx, second); err != nil {
		t.Fatal(err)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("第二笔交易重复入账: %+v", wallet)
	}
	var review int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.payment_inbox WHERE status = 'needs_review' AND reason_code = 'duplicate_payment'`).Scan(&review); err != nil || review != 1 {
		t.Fatalf("第二笔交易未进入待核对: %d,%v", review, err)
	}
}

func TestPaymentAmountAndChannelMismatch(t *testing.T) {
	service, pool, userID, _, _, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "order-mismatch", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-amount", 2900)); err != nil {
		t.Fatal(err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "0" {
		t.Fatalf("金额不符仍入账: %+v", wallet)
	}
	var reason string
	if err := pool.QueryRow(ctx, `SELECT reason_code FROM public.payment_inbox WHERE provider_trade_no = 'trade-amount'`).Scan(&reason); err != nil || reason != "amount_mismatch" {
		t.Fatalf("金额不符未隔离: %s,%v", reason, err)
	}
	if err := service.processPayment(ctx, paidFact(order, "trade-channel", 3000)); err != nil {
		t.Fatal(err)
	}
	// 正确金额的迟到通知仍可入账。
	correct := paidFact(order, "trade-correct", 3000)
	if err := service.processPayment(ctx, correct); err != nil {
		t.Fatal(err)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("正确通知未入账: %+v", wallet)
	}
}

func TestInterruptedCreditRollsBackAndRetriesOnce(t *testing.T) {
	service, pool, userID, _, _, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "order-interrupt", 3000)
	service.testHook = func(stage string) error {
		if stage == "before_credit" {
			return fault.New("service_unavailable", "模拟到账前中断")
		}
		return nil
	}
	if err := service.processPayment(ctx, paidFact(order, "trade-interrupt", 3000)); err == nil {
		t.Fatal("模拟中断未生效")
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "0" {
		t.Fatalf("中断事务留下了余额: %+v", wallet)
	}
	var inbox int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.payment_inbox`).Scan(&inbox); err != nil || inbox != 0 {
		t.Fatalf("中断事务留下了通知: %d,%v", inbox, err)
	}
	service.testHook = nil
	if err := service.processPayment(ctx, paidFact(order, "trade-interrupt", 3000)); err != nil {
		t.Fatal(err)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("重试未入账: %+v", wallet)
	}
}

func TestCallbackEndpointAndSignatureFailure(t *testing.T) {
	service, pool, userID, _, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "order-callback", 3000)
	provider.notification = &payments.Notification{
		MerchantOrderNo: order.MerchantOrderNo, ProviderTradeNo: "trade-callback",
		AmountMinor: 3000, Channel: order.Channel, ProviderStatus: "TRADE_SUCCESS", Paid: true,
	}
	mux := http.NewServeMux()
	service.RegisterUser(mux)
	form := url.Values{}
	form.Set("pid", "20220715225121")
	form.Set("out_trade_no", order.MerchantOrderNo)
	form.Set("trade_no", "trade-callback")
	form.Set("money", "30.00")
	form.Set("trade_status", "TRADE_SUCCESS")
	form.Set("type", "alipay")
	form.Set("sign", "x")
	request := httptest.NewRequest(http.MethodPost, "/payments/callbacks/zpay", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != "success" {
		t.Fatalf("回调响应错误: %d %s", recorder.Code, recorder.Body.String())
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("回调未入账: %+v", wallet)
	}
	provider.verifyErr = fault.New("validation_failed", "签名无效")
	request = httptest.NewRequest(http.MethodPost, "/payments/callbacks/zpay", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("无效签名未被拒绝: %d", recorder.Code)
	}
}

func TestActiveQueryCreditsAndReconciliation(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "order-query", 3000)
	provider.queryPaid = true
	provider.queryAmount = 3000
	provider.queryChannel = "alipay"
	provider.queryTradeNo = "trade-query"
	admin := adminActor(service, adminID)
	record, err := service.CreateReconciliation(ctx, admin, "recon-query", ReconciliationInput{
		Kind: "payment_orders", Reason: "主动查单核验待支付订单",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "matched" {
		t.Fatalf("查单对账状态错误: %+v", record)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "3000" {
		t.Fatalf("主动查单未入账: %+v", wallet)
	}
	updated, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.CreditStatus != "credited" {
		t.Fatalf("查单订单未到账: %+v", updated)
	}
}

func TestRefundDisabledByDefaultAndEnabledWithExplicitProvider(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "order-refund", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-refund", 3000)); err != nil {
		t.Fatal(err)
	}
	admin := adminActor(service, adminID)
	_, err := service.CreateRefund(ctx, admin, "refund-disabled", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "异常资金核对后退款",
	})
	if code, _ := fault.Describe(err); code != "not_configured" {
		t.Fatalf("默认退款未明确禁用: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.refunds`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("禁用退款仍创建记录: %d,%v", count, err)
	}
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	enableRefund(t, pool)
	refund, err := service.CreateRefund(ctx, admin, "refund-enabled", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "重复付款核对后原路退回",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" {
		t.Fatalf("退款未成功: %+v", refund)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "2000" {
		t.Fatalf("退款后钱包错误: %+v", wallet)
	}
	var balanced int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM (
		SELECT journal_id FROM public.postings GROUP BY journal_id HAVING sum(amount_minor) <> 0
	) x`).Scan(&balanced); err != nil || balanced != 0 {
		t.Fatalf("存在不平衡分录: %d,%v", balanced, err)
	}
}

func TestRefundFailedReturnsFundsAndUnknownDoesNotReplay(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	admin := adminActor(service, adminID)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "failed"
	provider.mu.Unlock()
	failedOrder := createOrder(t, service, userID, "refund-failed-order", 3000)
	if err := service.processPayment(ctx, paidFact(failedOrder, "trade-refund-failed", 3000)); err != nil {
		t.Fatal(err)
	}
	failed, err := service.CreateRefund(ctx, admin, "refund-failed", RefundInput{
		OrderID: failedOrder.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道拒绝后退回余额",
	})
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" {
		t.Fatalf("退款失败状态错误: %+v", failed)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("失败退款未退回资金: %+v %v", wallet, err)
	}
	var payable int64
	if err := pool.QueryRow(ctx, `SELECT coalesce(sum(amount_minor) FILTER (WHERE a.kind='refund_payable'),0)::bigint
		FROM public.postings p JOIN public.ledger_accounts a ON a.id=p.account_id`).Scan(&payable); err != nil || payable != 0 {
		t.Fatalf("退款应付账户未清: %d %v", payable, err)
	}
	provider.mu.Lock()
	provider.refundStatus = "unknown"
	provider.mu.Unlock()
	unknownOrder := createOrder(t, service, userID, "refund-unknown-order", 3000)
	if err := service.processPayment(ctx, paidFact(unknownOrder, "trade-refund-unknown", 3000)); err != nil {
		t.Fatal(err)
	}
	unknown, err := service.CreateRefund(ctx, admin, "refund-unknown", RefundInput{
		OrderID: unknownOrder.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道结果不明待核对",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Status != "unknown" {
		t.Fatalf("结果不明退款状态错误: %+v", unknown)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "5000" {
		t.Fatalf("结果不明退款资金状态错误: %+v %v", wallet, err)
	}
	provider.mu.Lock()
	calls := provider.refundCalls
	provider.mu.Unlock()
	replayed, err := service.CreateRefund(ctx, admin, "refund-unknown", RefundInput{
		OrderID: unknownOrder.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道结果不明待核对",
	})
	if err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	after := provider.refundCalls
	provider.mu.Unlock()
	if replayed.ID != unknown.ID || after != calls {
		t.Fatalf("结果不明退款被自动重放: %s %d/%d", replayed.ID, after, calls)
	}
}

func TestExpiredOrderClosesAndLatePaymentCredits(t *testing.T) {
	service, pool, userID, _, _, clock := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "expiry-order", 3000)
	clock.Advance(31 * time.Minute)
	expired, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if expired.Status != "closed" || expired.CheckoutURL != "" {
		t.Fatalf("超时订单未关闭: %+v", expired)
	}
	if err := service.processPayment(ctx, paidFact(order, "trade-late", 3000)); err != nil {
		t.Fatal(err)
	}
	late, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if late.Status != "completed" || !late.LatePayment || late.CreditStatus != "credited" {
		t.Fatalf("迟到支付未入账: %+v", late)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("迟到支付余额错误: %+v %v", wallet, err)
	}
}

func TestFullRefundMarksOrderRefunded(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order := createOrder(t, service, userID, "full-refund-order", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-full-refund", 3000)); err != nil {
		t.Fatal(err)
	}
	refund, err := service.CreateRefund(ctx, admin, "full-refund", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "3000", Reason: "整笔异常付款原路退回",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "succeeded" {
		t.Fatalf("整笔退款未成功: %+v", refund)
	}
	updated, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.PaymentStatus != "refunded" {
		t.Fatalf("整笔退款后订单状态错误: %+v", updated)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "0" {
		t.Fatalf("整笔退款后钱包错误: %+v %v", wallet, err)
	}
	if _, err := service.CreateRefund(ctx, admin, "full-refund-again", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1", Reason: "重复退款测试",
	}); err == nil {
		t.Fatal("全额退款后仍可继续退款")
	}
}
