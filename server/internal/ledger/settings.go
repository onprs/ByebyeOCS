package ledger

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type PriceView struct {
	Scope                     string `json:"scope"`
	Configured                bool   `json:"configured"`
	State                     string `json:"state"`
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
}

type RechargePolicyView struct {
	Provider       string   `json:"provider"`
	Enabled        bool     `json:"enabled"`
	State          string   `json:"state"`
	Channels       []string `json:"channels"`
	DefaultChannel string   `json:"default_channel"`
	MinMinor       string   `json:"min_minor"`
	MaxMinor       string   `json:"max_minor"`
}

type PricingView struct {
	Currency          string             `json:"currency"`
	ChargingAllowed   bool               `json:"charging_allowed"`
	AutomaticCharging bool               `json:"automatic_charging"`
	Service           PriceView          `json:"service"`
	Tiku              PriceView          `json:"tiku"`
	Recharge          RechargePolicyView `json:"recharge"`
}

type BillingSettingsView struct {
	Version                       int64     `json:"version"`
	UpdatedAt                     string    `json:"updated_at"`
	ServiceChargeEnabled          bool      `json:"service_charge_enabled"`
	TikuChargeEnabled             bool      `json:"tiku_charge_enabled"`
	ChargingAllowed               bool      `json:"charging_allowed"`
	EffectiveServiceChargeEnabled bool      `json:"effective_service_charge_enabled"`
	EffectiveTikuChargeEnabled    bool      `json:"effective_tiku_charge_enabled"`
	AutomaticCharging             bool      `json:"automatic_charging"`
	ZPayCheckoutEnabled           bool      `json:"zpay_checkout_enabled"`
	ZPayRefundEnabled             bool      `json:"zpay_refund_enabled"`
	DefaultChannel                string    `json:"default_channel"`
	MinRechargeMinor              string    `json:"min_recharge_minor"`
	MaxRechargeMinor              string    `json:"max_recharge_minor"`
	Service                       PriceView `json:"service"`
	Tiku                          PriceView `json:"tiku"`
	PaymentProvider               string    `json:"payment_provider"`
	CheckoutReady                 bool      `json:"checkout_ready"`
	RefundReady                   bool      `json:"refund_ready"`
}

type ServicePricePatch struct {
	PriceMicros    string `json:"price_micros"`
	UnitPrecision  *int   `json:"unit_precision"`
	SettlementRule string `json:"settlement_rule"`
	Rounding       string `json:"rounding"`
	MinChargeMinor string `json:"min_charge_minor"`
	UnitLabel      string `json:"unit_label"`
}

type TikuPricePatch struct {
	InputMicrosPerMillion     string `json:"input_micros_per_million"`
	OutputMicrosPerMillion    string `json:"output_micros_per_million"`
	SettlementRule            string `json:"settlement_rule"`
	SettlementThresholdMicros string `json:"settlement_threshold_micros"`
	Rounding                  string `json:"rounding"`
	UnitLabel                 string `json:"unit_label"`
}

type BillingPatch struct {
	ServiceChargeEnabled *bool              `json:"service_charge_enabled"`
	TikuChargeEnabled    *bool              `json:"tiku_charge_enabled"`
	ZPayCheckoutEnabled  *bool              `json:"zpay_checkout_enabled"`
	ZPayRefundEnabled    *bool              `json:"zpay_refund_enabled"`
	DefaultChannel       *string            `json:"default_channel"`
	MinRechargeMinor     *string            `json:"min_recharge_minor"`
	MaxRechargeMinor     *string            `json:"max_recharge_minor"`
	ServicePrice         *ServicePricePatch `json:"service_price"`
	TikuPrice            *TikuPricePatch    `json:"tiku_price"`
}

func priceView(scope string, price *priceSnapshot, enabled bool) PriceView {
	if price == nil {
		return PriceView{Scope: scope, State: "unconfigured", Configured: false}
	}
	state := "active"
	if !enabled {
		state = "disabled"
	}
	return PriceView{
		Scope: price.Scope, Configured: true, State: state, PriceVersionID: price.ID,
		PriceVersion: price.Version, UnitLabel: price.UnitLabel, UnitPrecision: price.UnitPrecision,
		PriceMicros:            formatAmount(price.PriceMicros),
		InputMicrosPerMillion:  formatAmount(price.InputMicrosPerMillion),
		OutputMicrosPerMillion: formatAmount(price.OutputMicrosPerMillion),
		SettlementRule:         price.SettlementRule, Rounding: price.Rounding,
		MinChargeMinor:            formatAmount(price.MinChargeMinor),
		SettlementThresholdMicros: formatAmount(price.SettlementThresholdMicros),
	}
}

func (s *Service) pricingView(ctx context.Context, query rowQuerier, settings billingSettings) (PricingView, error) {
	service, err := s.loadPrice(ctx, query, ScopeServiceTaskPoint, "")
	if err != nil {
		return PricingView{}, err
	}
	tiku, err := s.loadPrice(ctx, query, ScopeTikuTokens, "")
	if err != nil {
		return PricingView{}, err
	}
	runtimeAllowed := s.chargingAuthorized()
	view := PricingView{Currency: CurrencyCNY, ChargingAllowed: runtimeAllowed}
	effectiveService := runtimeAllowed && settings.ServiceChargeEnabled
	effectiveTiku := runtimeAllowed && settings.TikuChargeEnabled
	view.Service = priceView(ScopeServiceTaskPoint, service, effectiveService)
	view.Tiku = priceView(ScopeTikuTokens, tiku, effectiveTiku)
	view.Recharge = RechargePolicyView{
		Provider: "zpay", Enabled: s.checkoutReady(settings),
		Channels: []string{ChannelAlipay, ChannelWxpay}, DefaultChannel: settings.DefaultChannel,
		MinMinor: formatAmount(settings.MinRechargeMinor), MaxMinor: formatAmount(settings.MaxRechargeMinor),
	}
	if view.Recharge.Enabled {
		view.Recharge.State = "available"
	} else {
		view.Recharge.State = "not_configured"
	}
	view.AutomaticCharging = (effectiveService && service != nil) || (effectiveTiku && tiku != nil)
	return view, nil
}

// PublicMetadata 返回与 GET /pricing 相同的安全公开计费元数据，不暴露价格密钥。
func (s *Service) PublicMetadata(ctx context.Context) (PricingView, error) {
	return s.GetPricing(ctx)
}

func (s *Service) GetPricing(ctx context.Context) (PricingView, error) {
	var view PricingView
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		settings, err := s.loadBillingSettings(ctx, tx)
		if err != nil {
			return err
		}
		view, err = s.pricingView(ctx, tx, settings)
		return err
	})
	return view, err
}

func (s *Service) GetBillingSettings(ctx context.Context, actor web.Actor) (BillingSettingsView, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return BillingSettingsView{}, err
	}
	return s.billingSettingsView(ctx)
}

func (s *Service) billingSettingsView(ctx context.Context) (BillingSettingsView, error) {
	var settings billingSettings
	var updatedAt time.Time
	if err := s.pool.QueryRow(ctx, `SELECT service_charge_enabled, tiku_charge_enabled,
		zpay_checkout_enabled, zpay_refund_enabled, default_channel,
		min_recharge_minor, max_recharge_minor, version, updated_at
		FROM public.billing_settings WHERE id = true`).Scan(
		&settings.ServiceChargeEnabled, &settings.TikuChargeEnabled,
		&settings.ZPayCheckoutEnabled, &settings.ZPayRefundEnabled, &settings.DefaultChannel,
		&settings.MinRechargeMinor, &settings.MaxRechargeMinor, &settings.Version, &updatedAt); err != nil {
		return BillingSettingsView{}, fault.New("service_unavailable", "无法读取计费设置")
	}
	service, err := s.loadPrice(ctx, s.pool, ScopeServiceTaskPoint, "")
	if err != nil {
		return BillingSettingsView{}, err
	}
	tiku, err := s.loadPrice(ctx, s.pool, ScopeTikuTokens, "")
	if err != nil {
		return BillingSettingsView{}, err
	}
	checkoutReady := s.checkoutReady(settings)
	refundReady := settings.ZPayRefundEnabled && s.provider != nil &&
		s.provider.Name() == "zpay" && s.provider.Capabilities().Refund
	runtimeAllowed := s.chargingAuthorized()
	effectiveService := runtimeAllowed && settings.ServiceChargeEnabled
	effectiveTiku := runtimeAllowed && settings.TikuChargeEnabled
	serviceView := priceView(ScopeServiceTaskPoint, service, effectiveService)
	tikuView := priceView(ScopeTikuTokens, tiku, effectiveTiku)
	return BillingSettingsView{
		Version: settings.Version, UpdatedAt: utcTime(updatedAt),
		ServiceChargeEnabled: settings.ServiceChargeEnabled, TikuChargeEnabled: settings.TikuChargeEnabled,
		ChargingAllowed:               runtimeAllowed,
		EffectiveServiceChargeEnabled: effectiveService,
		EffectiveTikuChargeEnabled:    effectiveTiku,
		AutomaticCharging:             (effectiveService && service != nil) || (effectiveTiku && tiku != nil),
		ZPayCheckoutEnabled:           settings.ZPayCheckoutEnabled, ZPayRefundEnabled: settings.ZPayRefundEnabled,
		DefaultChannel:   settings.DefaultChannel,
		MinRechargeMinor: formatAmount(settings.MinRechargeMinor),
		MaxRechargeMinor: formatAmount(settings.MaxRechargeMinor),
		Service:          serviceView,
		Tiku:             tikuView,
		PaymentProvider:  "zpay", CheckoutReady: checkoutReady, RefundReady: refundReady,
	}, nil
}

func parseNonNegative(value string) (int64, error) {
	if value == "" || len(value) > 18 {
		return 0, fault.New("validation_failed", "金额必须是非负十进制整数字符串")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fault.New("validation_failed", "金额必须是非负十进制整数字符串")
		}
	}
	parsed, err := parseZeroAmount(value)
	if err != nil || parsed < 0 || parsed > maxAmountMinor {
		return 0, fault.New("validation_failed", "金额超出允许范围")
	}
	return parsed, nil
}

func validateServicePrice(patch *ServicePricePatch) (priceSnapshot, error) {
	if patch == nil {
		return priceSnapshot{}, fault.New("validation_failed", "服务价格不能为空")
	}
	priceMicros, err := parseNonNegative(patch.PriceMicros)
	if err != nil {
		return priceSnapshot{}, err
	}
	if patch.UnitPrecision == nil {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置单位精度")
	}
	precision := *patch.UnitPrecision
	if precision < 0 || precision > 6 {
		return priceSnapshot{}, fault.New("validation_failed", "单位精度必须在 0 到 6 之间")
	}
	rule := patch.SettlementRule
	if rule == "" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置服务结算规则")
	}
	if rule != "per_event" && rule != "per_event_min_charge" {
		return priceSnapshot{}, fault.New("validation_failed", "服务结算规则未显式支持")
	}
	rounding := patch.Rounding
	if rounding == "" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置舍入规则")
	}
	if !validRounding(rounding) {
		return priceSnapshot{}, fault.New("validation_failed", "舍入规则无效")
	}
	if patch.MinChargeMinor == "" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置最低收费")
	}
	minCharge, err := parseNonNegative(patch.MinChargeMinor)
	if err != nil {
		return priceSnapshot{}, err
	}
	label := trimSpace(patch.UnitLabel)
	if label == "" {
		label = "有效任务点"
	}
	return priceSnapshot{Scope: ScopeServiceTaskPoint, UnitLabel: label, UnitPrecision: precision,
		PriceMicros: priceMicros, SettlementRule: rule, Rounding: rounding,
		MinChargeMinor: minCharge, SettlementThresholdMicros: 10000, Status: "published"}, nil
}

func validateTikuPrice(patch *TikuPricePatch) (priceSnapshot, error) {
	if patch == nil {
		return priceSnapshot{}, fault.New("validation_failed", "题库价格不能为空")
	}
	input, err := parseNonNegative(patch.InputMicrosPerMillion)
	if err != nil {
		return priceSnapshot{}, err
	}
	output, err := parseNonNegative(patch.OutputMicrosPerMillion)
	if err != nil {
		return priceSnapshot{}, err
	}
	if patch.SettlementRule != "accumulate_threshold" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置题库累计结算规则")
	}
	if patch.SettlementThresholdMicros == "" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置题库结算门槛")
	}
	threshold, err := parseNonNegative(patch.SettlementThresholdMicros)
	if err != nil || threshold != 10000 {
		return priceSnapshot{}, fault.New("validation_failed", "MVP 题库结算门槛必须为 10000 微元（每累计 1 分结算）")
	}
	rounding := patch.Rounding
	if rounding == "" {
		return priceSnapshot{}, fault.New("validation_failed", "必须显式配置舍入规则")
	}
	if !validRounding(rounding) {
		return priceSnapshot{}, fault.New("validation_failed", "舍入规则无效")
	}
	label := trimSpace(patch.UnitLabel)
	if label == "" {
		label = "token"
	}
	return priceSnapshot{Scope: ScopeTikuTokens, UnitLabel: label, UnitPrecision: 0,
		InputMicrosPerMillion: input, OutputMicrosPerMillion: output,
		SettlementRule: "accumulate_threshold", Rounding: rounding,
		SettlementThresholdMicros: threshold, Status: "published"}, nil
}

func (s *Service) UpdateBillingSettings(ctx context.Context, actor web.Actor, expectedVersion int64,
	patch BillingPatch) (BillingSettingsView, error) {
	if err := requireAgent(actor, "admin", PermBillingManage); err != nil {
		return BillingSettingsView{}, err
	}
	if expectedVersion < 1 {
		return BillingSettingsView{}, fault.New("version_conflict", "计费设置版本已变化，请重新加载")
	}
	var servicePrice, tikuPrice *priceSnapshot
	var err error
	if patch.ServicePrice != nil {
		value, err := validateServicePrice(patch.ServicePrice)
		if err != nil {
			return BillingSettingsView{}, err
		}
		servicePrice = &value
	}
	if patch.TikuPrice != nil {
		value, err := validateTikuPrice(patch.TikuPrice)
		if err != nil {
			return BillingSettingsView{}, err
		}
		tikuPrice = &value
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var current billingSettings
		var updatedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT service_charge_enabled, tiku_charge_enabled,
			zpay_checkout_enabled, zpay_refund_enabled, default_channel,
			min_recharge_minor, max_recharge_minor, version, updated_at
			FROM public.billing_settings WHERE id = true FOR UPDATE`).Scan(
			&current.ServiceChargeEnabled, &current.TikuChargeEnabled,
			&current.ZPayCheckoutEnabled, &current.ZPayRefundEnabled, &current.DefaultChannel,
			&current.MinRechargeMinor, &current.MaxRechargeMinor, &current.Version, &updatedAt); err != nil {
			return fault.New("service_unavailable", "无法读取计费设置")
		}
		if current.Version != expectedVersion {
			return fault.New("version_conflict", "计费设置已被其他操作更新，请重新加载")
		}
		next := current
		if patch.ServiceChargeEnabled != nil {
			next.ServiceChargeEnabled = *patch.ServiceChargeEnabled
		}
		if patch.TikuChargeEnabled != nil {
			next.TikuChargeEnabled = *patch.TikuChargeEnabled
		}
		if patch.ZPayCheckoutEnabled != nil {
			next.ZPayCheckoutEnabled = *patch.ZPayCheckoutEnabled
			if next.ZPayCheckoutEnabled && (s.provider == nil || s.provider.Name() != "zpay" ||
				!s.provider.Capabilities().Checkout) {
				return fault.New("validation_failed", "ZPay 收银能力未配置，不能启用")
			}
		}
		if patch.ZPayRefundEnabled != nil {
			next.ZPayRefundEnabled = *patch.ZPayRefundEnabled
			if next.ZPayRefundEnabled && (s.provider == nil || s.provider.Name() != "zpay" ||
				!s.provider.Capabilities().Refund) {
				return fault.New("validation_failed", "ZPay 退款能力尚未完成核实，不能启用")
			}
		}
		if patch.DefaultChannel != nil {
			if *patch.DefaultChannel != ChannelAlipay && *patch.DefaultChannel != ChannelWxpay {
				return fault.New("validation_failed", "默认支付方式无效")
			}
			next.DefaultChannel = *patch.DefaultChannel
		}
		if patch.MinRechargeMinor != nil {
			value, err := parseAmount(*patch.MinRechargeMinor)
			if err != nil {
				return err
			}
			next.MinRechargeMinor = value
		}
		if patch.MaxRechargeMinor != nil {
			value, err := parseAmount(*patch.MaxRechargeMinor)
			if err != nil {
				return err
			}
			next.MaxRechargeMinor = value
		}
		if next.MinRechargeMinor > next.MaxRechargeMinor {
			return fault.New("validation_failed", "最低充值金额不能高于最高充值金额")
		}
		if servicePrice != nil {
			if err := s.publishPrice(ctx, tx, actor, *servicePrice); err != nil {
				return err
			}
		}
		if tikuPrice != nil {
			if err := s.publishPrice(ctx, tx, actor, *tikuPrice); err != nil {
				return err
			}
		}
		now := s.now()
		tag, err := tx.Exec(ctx, `UPDATE public.billing_settings SET
			service_charge_enabled = $1, tiku_charge_enabled = $2, zpay_checkout_enabled = $3,
			zpay_refund_enabled = $4, default_channel = $5, min_recharge_minor = $6,
			max_recharge_minor = $7, version = version + 1, updated_at = $8, updated_by = $9
			WHERE id = true AND version = $10`,
			next.ServiceChargeEnabled, next.TikuChargeEnabled, next.ZPayCheckoutEnabled,
			next.ZPayRefundEnabled, next.DefaultChannel, next.MinRechargeMinor,
			next.MaxRechargeMinor, now, actor.ID, expectedVersion)
		if err != nil {
			return fault.New("service_unavailable", "无法更新计费设置")
		}
		if tag.RowsAffected() != 1 {
			return fault.New("version_conflict", "计费设置已被其他操作更新，请重新加载")
		}
		return s.audit(ctx, tx, actor, "billing.settings.updated", "billing_settings", "",
			"更新计费开关与充值限制")
	})
	if err != nil {
		return BillingSettingsView{}, err
	}
	return s.billingSettingsView(ctx)
}

func (s *Service) publishPrice(ctx context.Context, tx pgx.Tx, actor web.Actor, price priceSnapshot) error {
	var nextVersion int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), 0) + 1 FROM public.price_versions
		WHERE scope = $1`, price.Scope).Scan(&nextVersion); err != nil {
		return fault.New("service_unavailable", "无法生成价格版本号")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.price_versions SET status = 'retired'
		WHERE scope = $1 AND status = 'published'`, price.Scope); err != nil {
		return fault.New("service_unavailable", "无法停用旧价格版本")
	}
	priceID := newUUID()
	if _, err := tx.Exec(ctx, `INSERT INTO public.price_versions (
		id, scope, version, status, currency, unit_label, unit_precision, price_micros,
		input_micros_per_million, output_micros_per_million, settlement_rule, rounding,
		min_charge_minor, settlement_threshold_micros, published_at, published_by, created_at
	) VALUES ($1,$2,$3,'published','CNY',$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$13)`,
		priceID, price.Scope, nextVersion, price.UnitLabel, price.UnitPrecision,
		nullablePrice(price.Scope, price.PriceMicros), nullablePriceTiku(price.Scope, price.InputMicrosPerMillion),
		nullablePriceTiku(price.Scope, price.OutputMicrosPerMillion), price.SettlementRule,
		price.Rounding, price.MinChargeMinor, price.SettlementThresholdMicros, s.now(), actor.ID); err != nil {
		return fault.New("service_unavailable", "无法发布价格版本")
	}
	return s.audit(ctx, tx, actor, "billing.price.published", "price_version", priceID,
		"发布不可变价格版本 "+formatAmount(nextVersion))
}

func nullablePrice(scope string, value int64) any {
	if scope == ScopeServiceTaskPoint {
		return value
	}
	return nil
}

func nullablePriceTiku(scope string, value int64) any {
	if scope == ScopeTikuTokens {
		return value
	}
	return nil
}
