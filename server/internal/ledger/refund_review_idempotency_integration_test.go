//go:build integration

package ledger

import (
	"context"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestRefundReviewBindsKeyToRefundAndOutcome(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	_, first := prepareLegacyReservedRefund(t, service, pool, admin, userID, "refund-key-first", 1000)
	_, second := prepareLegacyReservedRefund(t, service, pool, admin, userID, "refund-key-second", 1000)
	input := RefundReviewInput{Outcome: "succeeded", Reason: "已核对渠道退款记录"}
	resolved, err := service.ResolveRefund(ctx, admin, "review-payload-bound", first.ID, input)
	if err != nil || resolved.Status != "succeeded" {
		t.Fatalf("首次复核失败：%v", err)
	}
	for _, change := range []struct {
		name  string
		id    string
		input RefundReviewInput
	}{
		{"变更退款", second.ID, input},
		{"变更结果", first.ID, RefundReviewInput{Outcome: "failed", Reason: input.Reason}},
		{"变更理由", first.ID, RefundReviewInput{Outcome: input.Outcome, Reason: "另一份渠道核对记录"}},
	} {
		t.Run(change.name, func(t *testing.T) {
			_, err := service.ResolveRefund(ctx, admin, "review-payload-bound", change.id, change.input)
			if code, _ := fault.Describe(err); code != "conflict" {
				t.Fatalf("同键不同复核负载未被拒绝：%v", err)
			}
		})
	}
	replayed, err := service.ResolveRefund(ctx, admin, "review-payload-bound", first.ID, input)
	if err != nil || replayed.ID != first.ID || replayed.Status != "succeeded" {
		t.Fatalf("相同负载不能幂等重放：%v", err)
	}
	if actions := auditCount(t, pool, "refund.reviewed"); actions != 1 {
		t.Fatalf("冲突或重复复核写入额外审计：%d", actions)
	}
	var secondStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM public.refunds WHERE id=$1`, second.ID).Scan(&secondStatus); err != nil {
		t.Fatal(err)
	}
	if secondStatus != "reserved" || providerRefundCalls(provider) != 0 {
		t.Fatal("冲突复核改变另一退款或调用了渠道")
	}
}

func TestRefundDoesNotSendAfterSubmissionWindow(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	order := createOrder(t, service, userID, "refund-expired-submit", 1000)
	if err := service.processPayment(ctx, paidFact(order, "trade-expired-submit", 1000)); err != nil {
		t.Fatal(err)
	}
	// 提交权取得后还需一次真实数据库回读，纳秒窗口在发送前已耗尽。
	service.queryTimeout = time.Nanosecond
	refund, err := service.CreateRefund(ctx, adminActor(service, adminID), "refund-send-window", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "提交窗口到期不发送渠道请求",
	})
	if err != nil || refund.Status != "unknown" {
		t.Fatalf("过期提交窗口未保留未知结果：%#v/%v", refund, err)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("超过原提交窗口仍调用渠道：%d", calls)
	}
}
