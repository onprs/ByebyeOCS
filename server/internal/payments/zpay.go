package payments

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

const (
	providerName       = "zpay"
	fixedGatewayOrigin = "https://zpayz.cn"
	defaultSubject     = "ByebyeOCS 账户充值"
	maxResponseBytes   = 64 * 1024
	maxNotifyParams    = 32
)

// ZPayConfig 由服务端维护配置构造；商户密钥只保存在进程内。
type ZPayConfig struct {
	GatewayURL string
	MerchantID string
	Key        string
	NotifyURL  string
	ReturnURL  string
	Subject    string
	// Client 仅允许受控测试或已审计的出站客户端；默认使用有限超时的独立客户端。
	Client *http.Client
}

type ZPay struct {
	baseURL    *url.URL
	merchantID string
	key        string
	notifyURL  string
	returnURL  string
	subject    string
	client     *http.Client
}

func NewZPay(config ZPayConfig) (*ZPay, error) {
	gateway := strings.TrimSpace(config.GatewayURL)
	if gateway == "" {
		gateway = fixedGatewayOrigin
	}
	base, err := url.Parse(gateway)
	if err != nil || base.Scheme != "https" || base.Host != "zpayz.cn" ||
		(base.Path != "" && base.Path != "/") || base.RawQuery != "" ||
		base.Fragment != "" || base.User != nil {
		return nil, fault.New("not_configured", "ZPay 网关必须固定为 https://zpayz.cn")
	}
	if !validMerchantID(config.MerchantID) {
		return nil, fault.New("not_configured", "ZPay 商户编号未配置或格式无效")
	}
	if len(config.Key) < 8 || len(config.Key) > 128 {
		return nil, fault.New("not_configured", "ZPay 商户密钥未配置或长度无效")
	}
	notify, err := validatedCallbackURL(config.NotifyURL)
	if err != nil {
		return nil, err
	}
	returnURL, err := validatedCallbackURL(config.ReturnURL)
	if err != nil {
		return nil, err
	}
	subject := strings.TrimSpace(config.Subject)
	if subject == "" {
		subject = defaultSubject
	}
	if len(subject) > 100 {
		return nil, fault.New("not_configured", "ZPay 商品名称超出限制")
	}
	client := config.Client
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          4,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 8 * time.Second,
			},
		}
	} else {
		copied := *client
		if copied.Timeout <= 0 || copied.Timeout > 30*time.Second {
			copied.Timeout = 10 * time.Second
		}
		client = &copied
	}
	copiedClient := *client
	copiedClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &ZPay{
		baseURL: base, merchantID: config.MerchantID, key: config.Key,
		notifyURL: notify, returnURL: returnURL, subject: subject, client: &copiedClient,
	}, nil
}

func validMerchantID(value string) bool {
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func validatedCallbackURL(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fault.New("not_configured", "ZPay 回调地址必须是已配置的 HTTPS 地址且不能带参数")
	}
	return parsed.String(), nil
}

func (z *ZPay) Name() string { return providerName }

func (z *ZPay) Capabilities() Capabilities {
	// 退款协议尚未完成商户侧能力核实，不能把存在文档字段当作已启用。
	return Capabilities{Checkout: true, Query: true, Refund: false}
}

// Sign 按 zpayz.cn 文档：排除 sign、sign_type 和空值，按参数名 ASCII 排序，
// 以 a=b&c=d 拼接后直接追加商户 KEY，取小写 MD5。
func Sign(values url.Values, key string) string {
	keys := make([]string, 0, len(values))
	for name := range values {
		if name == "sign" || name == "sign_type" {
			continue
		}
		if len(values[name]) == 0 || values[name][0] == "" {
			continue
		}
		keys = append(keys, name)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for i, name := range keys {
		if i > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(name)
		builder.WriteByte('=')
		builder.WriteString(values[name][0])
	}
	builder.WriteString(key)
	sum := md5.Sum([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

func (z *ZPay) CheckoutURL(request CheckoutRequest) (string, error) {
	if !validMerchantOrderNo(request.MerchantOrderNo) {
		return "", fault.New("validation_failed", "本地订单号必须为 1 到 32 位数字")
	}
	if request.AmountMinor <= 0 || request.AmountMinor > maxYuanMinor {
		return "", fault.New("validation_failed", "充值金额超出允许范围")
	}
	if request.Channel != "alipay" && request.Channel != "wxpay" {
		return "", fault.New("validation_failed", "不支持的支付方式")
	}
	subject := strings.TrimSpace(request.Subject)
	if subject == "" {
		subject = z.subject
	}
	if len(subject) > 100 {
		return "", fault.New("validation_failed", "商品名称超出限制")
	}
	values := url.Values{}
	values.Set("pid", z.merchantID)
	values.Set("type", request.Channel)
	values.Set("out_trade_no", request.MerchantOrderNo)
	values.Set("notify_url", z.notifyURL)
	values.Set("return_url", z.returnURL)
	values.Set("name", subject)
	values.Set("money", FormatYuan(request.AmountMinor))
	values.Set("sign", Sign(values, z.key))
	values.Set("sign_type", "MD5")
	endpoint := *z.baseURL
	endpoint.Path = "/submit.php"
	endpoint.RawQuery = values.Encode()
	return endpoint.String(), nil
}

func (z *ZPay) VerifyNotification(values url.Values) (Notification, error) {
	if len(values) == 0 || len(values) > maxNotifyParams {
		return Notification{}, fault.New("validation_failed", "支付通知参数数量无效")
	}
	for name, list := range values {
		if len(name) == 0 || len(name) > 64 || len(list) != 1 || len(list[0]) > 1024 {
			return Notification{}, fault.New("validation_failed", "支付通知包含重复或超限参数")
		}
	}
	signType := values.Get("sign_type")
	if signType != "" && !strings.EqualFold(signType, "MD5") {
		return Notification{}, fault.New("validation_failed", "不支持的支付通知签名算法")
	}
	provided := strings.ToLower(strings.TrimSpace(values.Get("sign")))
	if len(provided) != 32 {
		return Notification{}, fault.New("validation_failed", "支付通知签名缺失")
	}
	expected := Sign(values, z.key)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		return Notification{}, fault.New("validation_failed", "支付通知签名校验失败")
	}
	if subtle.ConstantTimeCompare([]byte(values.Get("pid")), []byte(z.merchantID)) != 1 {
		return Notification{}, fault.New("validation_failed", "支付通知商户编号不符")
	}
	merchantOrder := values.Get("out_trade_no")
	if !validMerchantOrderNo(merchantOrder) {
		return Notification{}, fault.New("validation_failed", "支付通知本地订单号无效")
	}
	tradeNo := values.Get("trade_no")
	if !validProviderTradeNo(tradeNo) {
		return Notification{}, fault.New("validation_failed", "支付通知渠道交易号无效")
	}
	amountMinor, err := ParseYuanToMinor(values.Get("money"))
	if err != nil {
		return Notification{}, fault.New("validation_failed", "支付通知金额无效")
	}
	channel := values.Get("type")
	if !validCertificateText(channel, 32) {
		return Notification{}, fault.New("validation_failed", "支付通知支付方式无效")
	}
	providerStatus := values.Get("trade_status")
	if !validProviderStatus(providerStatus) {
		return Notification{}, fault.New("validation_failed", "支付通知状态无效")
	}
	return Notification{
		MerchantOrderNo: merchantOrder,
		ProviderTradeNo: tradeNo,
		AmountMinor:     amountMinor,
		Channel:         channel,
		ProviderStatus:  providerStatus,
		Paid:            providerStatus == "TRADE_SUCCESS",
	}, nil
}

func validProviderStatus(value string) bool {
	if len(value) < 1 || len(value) > 32 {
		return false
	}
	for _, r := range value {
		if !((r >= 'A' && r <= 'Z') || r == '_') {
			return false
		}
	}
	return true
}

type queryPayload struct {
	Msg        string          `json:"msg"`
	TradeNo    string          `json:"trade_no"`
	OutTradeNo string          `json:"out_trade_no"`
	Type       string          `json:"type"`
	PID        json.RawMessage `json:"pid"`
	Money      string          `json:"money"`
	Status     json.RawMessage `json:"status"`
}

func (z *ZPay) QueryOrder(ctx context.Context, request QueryRequest) (QueryResult, error) {
	if !validMerchantOrderNo(request.MerchantOrderNo) {
		return QueryResult{}, fault.New("validation_failed", "本地订单号必须为 1 到 32 位数字")
	}
	endpoint := *z.baseURL
	endpoint.Path = "/api.php"
	query := url.Values{}
	query.Set("act", "order")
	query.Set("pid", z.merchantID)
	query.Set("key", z.key)
	query.Set("out_trade_no", request.MerchantOrderNo)
	endpoint.RawQuery = query.Encode()
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return QueryResult{}, fault.New("provider_unavailable", "无法准备支付查单请求")
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "ByebyeOCS/1")
	response, err := z.client.Do(httpRequest)
	if err != nil {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单暂时无法完成")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(body) > maxResponseBytes {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单响应无效")
	}
	if response.StatusCode != http.StatusOK {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单暂时无法完成")
	}
	var payload queryPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单响应格式无效")
	}
	pid := rawScalar(payload.PID)
	if subtle.ConstantTimeCompare([]byte(pid), []byte(z.merchantID)) != 1 {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单商户编号不符")
	}
	if payload.OutTradeNo != request.MerchantOrderNo {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单本地订单号不符")
	}
	amountMinor, err := ParseYuanToMinor(payload.Money)
	if err != nil {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单金额无效")
	}
	status, err := strconv.Atoi(rawScalar(payload.Status))
	if err != nil || (status != 0 && status != 1) {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单状态无法识别")
	}
	if status == 1 && !validProviderTradeNo(payload.TradeNo) {
		return QueryResult{}, fault.New("provider_unavailable", "支付查单缺少渠道交易号")
	}
	providerStatus := "UNPAID"
	if status == 1 {
		providerStatus = "TRADE_SUCCESS"
	}
	return QueryResult{
		MerchantOrderNo: request.MerchantOrderNo,
		ProviderTradeNo: payload.TradeNo,
		AmountMinor:     amountMinor,
		Channel:         payload.Type,
		ProviderStatus:  providerStatus,
		Paid:            status == 1,
	}, nil
}

func rawScalar(value json.RawMessage) string {
	if len(value) == 0 || string(value) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(value, &text); err == nil {
		return text
	}
	return string(value)
}

// Refund 明确不启用：zpayz.cn 文档列出退款字段，但商户资格、可退通道和结果语义尚未核实。
// 未核实前不能生成退款成功或失败事实。
func (z *ZPay) Refund(context.Context, RefundRequest) (RefundResult, error) {
	return RefundResult{}, fault.New("not_configured", "ZPay 退款能力尚未完成核实，当前未启用")
}
