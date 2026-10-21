package ledger

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type billingSettings struct {
	ServiceChargeEnabled bool
	TikuChargeEnabled    bool
	ZPayCheckoutEnabled  bool
	ZPayRefundEnabled    bool
	DefaultChannel       string
	MinRechargeMinor     int64
	MaxRechargeMinor     int64
	Version              int64
}

type priceSnapshot struct {
	ID                        string
	Version                   int64
	Scope                     string
	Status                    string
	UnitLabel                 string
	UnitPrecision             int
	PriceMicros               int64
	InputMicrosPerMillion     int64
	OutputMicrosPerMillion    int64
	SettlementRule            string
	Rounding                  string
	MinChargeMinor            int64
	SettlementThresholdMicros int64
}

type Quote struct {
	Scope                     string `json:"scope"`
	Currency                  string `json:"currency"`
	State                     string `json:"state"`
	Chargeable                bool   `json:"chargeable"`
	ChargingAllowed           bool   `json:"charging_allowed"`
	PriceVersionID            string `json:"price_version_id,omitempty"`
	PriceVersion              int64  `json:"price_version,omitempty"`
	UnitLabel                 string `json:"unit_label,omitempty"`
	UnitPrecision             int    `json:"unit_precision"`
	PriceMicros               string `json:"price_micros,omitempty"`
	InputMicrosPerMillion     string `json:"input_micros_per_million,omitempty"`
	OutputMicrosPerMillion    string `json:"output_micros_per_million,omitempty"`
	SettlementRule            string `json:"settlement_rule,omitempty"`
	Rounding                  string `json:"rounding,omitempty"`
	MinChargeMinor            string `json:"min_charge_minor,omitempty"`
	SettlementThresholdMicros string `json:"settlement_threshold_micros,omitempty"`
	QuantityScaled            int64  `json:"quantity_scaled"`
	InputTokens               int64  `json:"input_tokens"`
	OutputTokens              int64  `json:"output_tokens"`
	TotalTokens               int64  `json:"total_tokens"`
	CostMicros                string `json:"cost_micros"`
	ChargeMinor               string `json:"charge_minor"`
	MaxChargeMinor            string `json:"max_charge_minor"`
	Reason                    string `json:"reason,omitempty"`
}

type QuoteInput struct {
	Scope          string
	UserID         string
	StableKey      string
	QuantityScaled int64
	InputTokens    int64
	OutputTokens   int64
	PriceVersionID string
}

func (s *Service) loadBillingSettings(ctx context.Context, query rowQuerier) (billingSettings, error) {
	var settings billingSettings
	if err := query.QueryRow(ctx, `SELECT service_charge_enabled, tiku_charge_enabled,
		zpay_checkout_enabled, zpay_refund_enabled, default_channel,
		min_recharge_minor, max_recharge_minor, version
		FROM public.billing_settings WHERE id = true FOR SHARE`).Scan(
		&settings.ServiceChargeEnabled, &settings.TikuChargeEnabled,
		&settings.ZPayCheckoutEnabled, &settings.ZPayRefundEnabled, &settings.DefaultChannel,
		&settings.MinRechargeMinor, &settings.MaxRechargeMinor, &settings.Version); err != nil {
		return billingSettings{}, fault.New("service_unavailable", "无法读取计费设置")
	}
	return settings, nil
}

func (s *Service) loadPrice(ctx context.Context, query rowQuerier, scope, priceVersionID string) (*priceSnapshot, error) {
	if scope != ScopeServiceTaskPoint && scope != ScopeTikuTokens {
		return nil, fault.New("validation_failed", "计费范围无效")
	}
	var row pgx.Row
	if priceVersionID != "" {
		if !web.ValidID(priceVersionID) {
			return nil, fault.New("validation_failed", "价格版本编号无效")
		}
		row = query.QueryRow(ctx, `SELECT id, version, scope, status, unit_label, unit_precision,
			coalesce(price_micros, 0), coalesce(input_micros_per_million, 0),
			coalesce(output_micros_per_million, 0), settlement_rule, rounding,
			min_charge_minor, settlement_threshold_micros
			FROM public.price_versions
			WHERE id = $1 AND scope = $2 AND status IN ('published', 'retired')`, priceVersionID, scope)
	} else {
		row = query.QueryRow(ctx, `SELECT id, version, scope, status, unit_label, unit_precision,
			coalesce(price_micros, 0), coalesce(input_micros_per_million, 0),
			coalesce(output_micros_per_million, 0), settlement_rule, rounding,
			min_charge_minor, settlement_threshold_micros
			FROM public.price_versions WHERE scope = $1 AND status = 'published'`, scope)
	}
	var price priceSnapshot
	if err := row.Scan(&price.ID, &price.Version, &price.Scope, &price.Status, &price.UnitLabel,
		&price.UnitPrecision, &price.PriceMicros, &price.InputMicrosPerMillion,
		&price.OutputMicrosPerMillion, &price.SettlementRule, &price.Rounding,
		&price.MinChargeMinor, &price.SettlementThresholdMicros); err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fault.New("service_unavailable", "无法读取价格版本")
	}
	if price.Scope == ScopeTikuTokens && price.SettlementThresholdMicros != 10000 {
		return nil, fault.New("not_configured", "MVP 题库仅支持每累计 10000 微元结算 1 分，旧价格版本需停用")
	}
	return &price, nil
}

func buildQuote(scope string, price *priceSnapshot, settings billingSettings, runtimeAllowed bool, quantity, inputTokens, outputTokens int64) (Quote, error) {
	if inputTokens < 0 || outputTokens < 0 || inputTokens > maxAmountMinor || outputTokens > maxAmountMinor {
		return Quote{}, fault.New("validation_failed", "token 数量超出允许范围")
	}
	quote := Quote{
		Scope: scope, Currency: CurrencyCNY, State: "unpriced", ChargingAllowed: runtimeAllowed,
		QuantityScaled: quantity, InputTokens: inputTokens, OutputTokens: outputTokens,
		TotalTokens: inputTokens + outputTokens, CostMicros: "0", ChargeMinor: "0",
		MaxChargeMinor: "0",
	}
	if price == nil {
		quote.Reason = "未配置已发布价格"
		return quote, nil
	}
	quote.PriceVersionID = price.ID
	quote.PriceVersion = price.Version
	quote.UnitLabel = price.UnitLabel
	quote.UnitPrecision = price.UnitPrecision
	quote.PriceMicros = formatAmount(price.PriceMicros)
	quote.InputMicrosPerMillion = formatAmount(price.InputMicrosPerMillion)
	quote.OutputMicrosPerMillion = formatAmount(price.OutputMicrosPerMillion)
	quote.SettlementRule = price.SettlementRule
	quote.Rounding = price.Rounding
	quote.MinChargeMinor = formatAmount(price.MinChargeMinor)
	quote.SettlementThresholdMicros = formatAmount(price.SettlementThresholdMicros)
	enabled := settings.ServiceChargeEnabled
	if scope == ScopeTikuTokens {
		enabled = settings.TikuChargeEnabled
	}
	switch {
	case !runtimeAllowed:
		quote.State = "disabled"
		quote.Reason = "运营授权未开启，当前不会扣费"
	case !enabled:
		quote.State = "disabled"
		quote.Reason = "运营尚未启用该范围的自动扣费"
	default:
		quote.State = "quoted"
		quote.Chargeable = true
	}
	switch scope {
	case ScopeServiceTaskPoint:
		if quantity <= 0 {
			return Quote{}, fault.New("validation_failed", "任务点数量必须为正整数")
		}
		cost, err := serviceCostMicros(quantity, price.PriceMicros, price.UnitPrecision)
		if err != nil {
			return Quote{}, err
		}
		charge, err := microsToMinor(cost, price.Rounding, price.MinChargeMinor)
		if err != nil {
			return Quote{}, err
		}
		quote.CostMicros = cost.String()
		quote.ChargeMinor = formatAmount(charge)
		quote.MaxChargeMinor = formatAmount(charge)
	case ScopeTikuTokens:
		if price.SettlementThresholdMicros != 10000 {
			return Quote{}, fault.New("not_configured", "MVP 题库仅支持每累计 10000 微元结算 1 分")
		}
		cost, err := tikuCostMicros(inputTokens, outputTokens, price.InputMicrosPerMillion, price.OutputMicrosPerMillion)
		if err != nil {
			return Quote{}, err
		}
		// 当前未累计尾差时立即结算的部分；预占按向上取整的最坏值。
		costInt, err := bigInt64(cost, "题库成本")
		if err != nil {
			return Quote{}, err
		}
		threshold := price.SettlementThresholdMicros
		quote.CostMicros = formatAmount(costInt)
		quote.ChargeMinor = formatAmount(costInt / threshold)
		maxCharge, err := ceilDiv(cost, threshold)
		if err != nil {
			return Quote{}, err
		}
		if maxCharge > maxAmountMinor {
			return Quote{}, fault.New("validation_failed", "题库预估费用超出上限")
		}
		quote.MaxChargeMinor = formatAmount(maxCharge)
	default:
		return Quote{}, fault.New("validation_failed", "计费范围无效")
	}
	return quote, nil
}

func (s *Service) Quote(ctx context.Context, input QuoteInput) (Quote, error) {
	var result Quote
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var quoteErr error
		result, quoteErr = s.quoteWith(ctx, tx, input)
		return quoteErr
	})
	return result, err
}

// QuoteInTransaction 在调用方事务内读取计费设置和价格快照，不另占连接池连接。
func (s *Service) QuoteInTransaction(ctx context.Context, tx pgx.Tx, input QuoteInput) (Quote, error) {
	if err := requireTx(tx); err != nil {
		return Quote{}, err
	}
	return s.quoteWith(ctx, tx, input)
}

func (s *Service) quoteWith(ctx context.Context, query rowQuerier, input QuoteInput) (Quote, error) {
	if input.Scope != ScopeServiceTaskPoint && input.Scope != ScopeTikuTokens {
		return Quote{}, fault.New("validation_failed", "计费范围无效")
	}
	if input.UserID != "" && !web.ValidID(input.UserID) {
		return Quote{}, fault.New("validation_failed", "用户编号无效")
	}
	if input.StableKey != "" {
		if err := validKey(input.StableKey); err != nil {
			return Quote{}, err
		}
	}
	settings, err := s.loadBillingSettings(ctx, query)
	if err != nil {
		return Quote{}, err
	}
	price, err := s.loadPrice(ctx, query, input.Scope, input.PriceVersionID)
	if err != nil {
		return Quote{}, err
	}
	return buildQuote(input.Scope, price, settings, s.chargingAuthorized(),
		input.QuantityScaled, input.InputTokens, input.OutputTokens)
}

func (s *Service) QuoteServiceTaskPoint(ctx context.Context, userID, stableKey string, quantityScaled int64, priceVersionID string) (Quote, error) {
	return s.Quote(ctx, QuoteInput{Scope: ScopeServiceTaskPoint, UserID: userID, StableKey: stableKey,
		QuantityScaled: quantityScaled, PriceVersionID: priceVersionID})
}

func (s *Service) QuoteTikuUsage(ctx context.Context, userID, stableKey string, inputTokens, outputTokens int64, priceVersionID string) (Quote, error) {
	return s.Quote(ctx, QuoteInput{Scope: ScopeTikuTokens, UserID: userID, StableKey: stableKey,
		InputTokens: inputTokens, OutputTokens: outputTokens, PriceVersionID: priceVersionID})
}
