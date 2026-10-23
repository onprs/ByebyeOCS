//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/payments"
)

type cancelAfterRefund struct {
	payments.Provider
	cancel context.CancelFunc
}

func (p cancelAfterRefund) Refund(ctx context.Context, input payments.RefundRequest) (payments.RefundResult, error) {
	result, err := p.Provider.Refund(ctx, input)
	p.cancel()
	return result, err
}

func TestRefundResultPersistsAfterCallerCancellation(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	service.provider = cancelAfterRefund{Provider: provider, cancel: cancel}
	order := createOrder(t, service, userID, "refund-cancelled-result", 1000)
	if err := service.processPayment(ctx, paidFact(order, "trade-refund-cancelled", 1000)); err != nil {
		t.Fatal(err)
	}
	refund, err := service.CreateRefund(ctx, adminActor(service, adminID), "refund-cancelled-result", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道成功后调用方取消仍保存事实",
	})
	if err != nil || refund.Status != "succeeded" {
		t.Fatalf("渠道已退款但事实未落库：%#v/%v", refund, err)
	}
	if countJournals(t, pool, "refund-payout:"+refund.ID) != 1 || refundPayableTotal(t, pool) != 0 || providerRefundCalls(provider) != 1 {
		t.Fatal("退款取消边界未完成唯一结算")
	}
}
