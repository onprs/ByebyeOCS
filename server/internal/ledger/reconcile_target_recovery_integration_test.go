//go:build integration

package ledger

import (
	"context"
	"testing"
	"time"
)

// 金额或渠道差异会把订单移入 needs_review；指定订单的主动查单必须仍能恢复，
// 不能因为订单不再是 pending_payment 就被扫描条件排除。
func TestReconciliationTargetRecoversNeedsReviewOrder(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "recon-needs-review-order", 3000)

	if err := service.processPayment(ctx, paidFact(order, "trade-mismatch", 2900)); err != nil {
		t.Fatal(err)
	}
	reviewed, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reviewed.Status != "needs_review" {
		t.Fatalf("差异通知未把订单置于待核对: %+v", reviewed)
	}

	provider.mu.Lock()
	provider.queryPaid = true
	provider.queryAmount = 3000
	provider.queryChannel = order.Channel
	provider.queryTradeNo = "trade-recovered"
	provider.mu.Unlock()

	admin := adminActor(service, adminID)
	record, err := service.CreateReconciliation(ctx, admin, "recon-target-needs-review", ReconciliationInput{
		Kind:    "payment_orders",
		OrderID: order.ID,
		Reason:  "金额差异后主动查单恢复",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "matched" || record.Findings["paid"] != "1" || record.Findings["orders_total"] != "1" {
		t.Fatalf("待核对订单未被主动查单恢复: %+v", record)
	}
	recovered, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "completed" || recovered.PaymentStatus != "paid" ||
		recovered.CreditStatus != "credited" {
		t.Fatalf("恢复后订单状态错误: %+v", recovered)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("恢复后到账金额错误: %+v %v", wallet, err)
	}
}

// 本地到期关闭只表示本地支付窗口结束；指定订单仍应能主动查单发现迟到付款。
func TestReconciliationTargetRecoversClosedLatePayment(t *testing.T) {
	service, pool, userID, adminID, provider, clock := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "recon-closed-order", 3000)
	clock.Advance(31 * time.Minute)
	closed, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" {
		t.Fatalf("订单未按本地窗口关闭: %+v", closed)
	}

	provider.mu.Lock()
	provider.queryPaid = true
	provider.queryAmount = 3000
	provider.queryChannel = order.Channel
	provider.queryTradeNo = "trade-late-recovered"
	provider.mu.Unlock()

	admin := adminActor(service, adminID)
	record, err := service.CreateReconciliation(ctx, admin, "recon-target-closed", ReconciliationInput{
		Kind:    "payment_orders",
		OrderID: order.ID,
		Reason:  "关闭后迟到付款主动查单",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "matched" || record.Findings["paid"] != "1" || record.Findings["orders_total"] != "1" {
		t.Fatalf("关闭订单未被主动查单恢复: %+v", record)
	}
	recovered, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != "completed" || !recovered.LatePayment ||
		recovered.CreditStatus != "credited" {
		t.Fatalf("迟到付款未正确入账: %+v", recovered)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("迟到付款到账金额错误: %+v %v", wallet, err)
	}
}
