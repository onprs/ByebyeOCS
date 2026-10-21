// Package payments 实现固定 ZPay（zpayz.cn）协议的签名、查单与受控网络访问。
// 本包不持有订单、钱包或账本状态；到账由 ledger 在独立事务中完成。
package payments

import (
	"context"
	"net/url"
)

// Capabilities 描述当前提供商实际启用的能力，不能据此推断未核实能力已可用。
type Capabilities struct {
	Checkout bool
	Query    bool
	Refund   bool
}

// CheckoutRequest 是生成托管收银入口所需的最小事实。
type CheckoutRequest struct {
	MerchantOrderNo string
	AmountMinor     int64
	Channel         string
	Subject         string
}

// Notification 是已完成签名、商户、金额与状态解析的异步通知事实。
type Notification struct {
	MerchantOrderNo string
	ProviderTradeNo string
	AmountMinor     int64
	Channel         string
	ProviderStatus  string
	Paid            bool
}

// QueryRequest 使用本地商户单号主动查单。
type QueryRequest struct {
	MerchantOrderNo string
}

// QueryResult 是主动查单的已核验事实。Paid=false 表示未支付或已关闭，不代表失败。
type QueryResult struct {
	MerchantOrderNo string
	ProviderTradeNo string
	AmountMinor     int64
	Channel         string
	ProviderStatus  string
	Paid            bool
}

// RefundRequest 使用本地退款唯一键，避免结果不明时重复提交。
type RefundRequest struct {
	MerchantOrderNo  string
	ProviderTradeNo  string
	ProviderRefundNo string
	AmountMinor      int64
	Reason           string
}

// RefundResult 只表达提供商明确返回的结果；unknown 不得被当作成功。
type RefundResult struct {
	ProviderRefundNo string
	Status           string
}

// Provider 由真实 ZPay、测试模拟器或明确禁用的实现提供。
// 生产入口只接受服务端构造的实现，不能从 HTTP 请求选择提供商。
type Provider interface {
	Name() string
	Capabilities() Capabilities
	CheckoutURL(CheckoutRequest) (string, error)
	QueryOrder(context.Context, QueryRequest) (QueryResult, error)
	Refund(context.Context, RefundRequest) (RefundResult, error)
	VerifyNotification(url.Values) (Notification, error)
}
