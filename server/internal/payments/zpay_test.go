package payments

import (
	"net/url"
	"strings"
	"testing"
)

func testZPay(t *testing.T) *ZPay {
	t.Helper()
	provider, err := NewZPay(ZPayConfig{
		MerchantID: "20220715225121",
		Key:        "testkey1234567890",
		NotifyURL:  "https://example.com/pay/notify",
		ReturnURL:  "https://example.com/pay/return",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func notificationValues() url.Values {
	values := url.Values{}
	values.Set("pid", "20220715225121")
	values.Set("type", "alipay")
	values.Set("out_trade_no", "202610211200001234567890")
	values.Set("money", "30.00")
	values.Set("trade_no", "2026102122001418111011411195")
	values.Set("trade_status", "TRADE_SUCCESS")
	values.Set("name", "测试充值")
	return values
}

func TestSignGoldenVector(t *testing.T) {
	values := notificationValues()
	values.Set("notify_url", "https://example.com/pay/notify")
	values.Set("return_url", "https://example.com/pay/return")
	values.Set("sign_type", "MD5")
	values.Set("sign", "ignored")
	const want = "30c12cfb46a0b1d031c267f7a386460a"
	if got := Sign(values, "testkey1234567890"); got != want {
		t.Fatalf("签名 %q，期望 %q", got, want)
	}
}

func TestVerifyNotification(t *testing.T) {
	provider := testZPay(t)
	values := notificationValues()
	values.Set("sign_type", "MD5")
	values.Set("sign", Sign(values, "testkey1234567890"))
	notification, err := provider.VerifyNotification(values)
	if err != nil {
		t.Fatal(err)
	}
	if !notification.Paid || notification.AmountMinor != 3000 ||
		notification.ProviderTradeNo != "2026102122001418111011411195" {
		t.Fatalf("通知解析错误: %+v", notification)
	}
	values.Set("money", "30.01")
	if _, err := provider.VerifyNotification(values); err == nil {
		t.Fatal("篡改金额后签名仍通过")
	}
	values = notificationValues()
	values.Set("pid", "other")
	values.Set("sign", Sign(values, "testkey1234567890"))
	if _, err := provider.VerifyNotification(values); err == nil {
		t.Fatal("错误商户编号被接受")
	}
	values = notificationValues()
	values.Set("sign", Sign(values, "testkey1234567890"))
	values.Add("out_trade_no", "202610211200001234567890")
	if _, err := provider.VerifyNotification(values); err == nil {
		t.Fatal("重复参数被接受")
	}
}

func TestCheckoutURLSignatureAndValidation(t *testing.T) {
	provider := testZPay(t)
	endpoint, err := provider.CheckoutURL(CheckoutRequest{
		MerchantOrderNo: "202610211200001234567890", AmountMinor: 3000, Channel: "alipay",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host != "zpayz.cn" || parsed.Path != "/submit.php" {
		t.Fatalf("收银地址错误: %s", endpoint)
	}
	query := parsed.Query()
	if query.Get("money") != "30.00" || query.Get("pid") != "20220715225121" ||
		query.Get("out_trade_no") != "202610211200001234567890" {
		t.Fatalf("收银参数错误: %v", query)
	}
	signature := query.Get("sign")
	query.Del("sign")
	query.Del("sign_type")
	if got := Sign(query, "testkey1234567890"); got != signature {
		t.Fatalf("收银签名不一致: %s != %s", got, signature)
	}
	if _, err := provider.CheckoutURL(CheckoutRequest{MerchantOrderNo: "abc", AmountMinor: 3000, Channel: "alipay"}); err == nil {
		t.Fatal("非数字订单号被接受")
	}
	if _, err := provider.CheckoutURL(CheckoutRequest{MerchantOrderNo: "123", AmountMinor: 3000, Channel: "card"}); err == nil {
		t.Fatal("未知支付方式被接受")
	}
}

func TestGatewayMustBeFixedZPay(t *testing.T) {
	for _, gateway := range []string{"https://evil.example", "http://zpayz.cn", "https://zpayz.cn/path", "https://zpayz.cn?x=1"} {
		_, err := NewZPay(ZPayConfig{GatewayURL: gateway, MerchantID: "2022", Key: "testkey1234567890",
			NotifyURL: "https://example.com/n", ReturnURL: "https://example.com/r"})
		if err == nil {
			t.Fatalf("网关 %q 未被拒绝", gateway)
		}
	}
}

func TestRefundIsExplicitlyDisabled(t *testing.T) {
	provider := testZPay(t)
	if provider.Capabilities().Refund {
		t.Fatal("ZPay 退款不应在未核实时启用")
	}
	if _, err := provider.Refund(t.Context(), RefundRequest{ProviderTradeNo: "1", AmountMinor: 1}); err == nil ||
		!strings.Contains(err.Error(), "退款") {
		t.Fatal("退款未明确拒绝")
	}
}
