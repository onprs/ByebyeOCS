//go:build integration

package ledger

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
)

func TestRealZPaySignatureAndLedgerValidation(t *testing.T) {
	pool := testsupport.NewPool(t)
	clock := &testClock{at: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	userID := newUser(t, pool)
	provider, err := payments.NewZPay(payments.ZPayConfig{
		MerchantID: "20220715225121", Key: "testkey1234567890",
		NotifyURL: "https://example.com/pay/notify", ReturnURL: "https://example.com/pay/return",
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(pool, Options{Provider: provider, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	enableCheckout(t, pool)
	order := createOrder(t, service, userID, "real-zpay-order", 3000)
	values := url.Values{}
	values.Set("pid", "20220715225121")
	values.Set("type", "alipay")
	values.Set("out_trade_no", order.MerchantOrderNo)
	values.Set("money", "30.00")
	values.Set("trade_no", "2026102122001418111011411195")
	values.Set("trade_status", "TRADE_SUCCESS")
	values.Set("name", "账户充值")
	values.Set("sign_type", "MD5")
	values.Set("sign", payments.Sign(values, "testkey1234567890"))
	if err := service.ProcessZPayCallback(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	wallet, err := service.GetWallet(context.Background(), userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("真实签名回调未入账: %+v %v", wallet, err)
	}
	// 伪造商户编号后即使重新签名也必须被提供商验签拒绝。
	other := url.Values{}
	for key, list := range values {
		other[key] = append([]string(nil), list...)
	}
	other.Set("pid", "202207150000000000000000")
	other.Del("sign")
	other.Set("sign", payments.Sign(other, "testkey1234567890"))
	if err := service.ProcessZPayCallback(context.Background(), other); err == nil {
		t.Fatal("错误商户编号被接受")
	}
	// 金额被篡改但重新签名：验签通过，账本核对拒绝入账并隔离通知。
	amountOrder := createOrder(t, service, userID, "real-zpay-amount", 3000)
	tampered := url.Values{}
	for key, list := range values {
		tampered[key] = append([]string(nil), list...)
	}
	tampered.Set("out_trade_no", amountOrder.MerchantOrderNo)
	tampered.Set("money", "29.00")
	tampered.Del("sign")
	tampered.Set("sign", payments.Sign(tampered, "testkey1234567890"))
	if err := service.ProcessZPayCallback(context.Background(), tampered); err != nil {
		t.Fatal(err)
	}
	wallet, err = service.GetWallet(context.Background(), userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("金额不符仍入账: %+v %v", wallet, err)
	}
	var reason string
	if err := pool.QueryRow(context.Background(), `SELECT reason_code FROM public.payment_inbox
		WHERE merchant_order_no = $1 AND status = 'needs_review'`, amountOrder.MerchantOrderNo).Scan(&reason); err != nil || reason != "amount_mismatch" {
		t.Fatalf("金额不符未隔离: %s %v", reason, err)
	}
}
