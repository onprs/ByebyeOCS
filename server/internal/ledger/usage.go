package ledger

import (
	"context"
	"math/big"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type UsageRecord struct {
	ID                        string `json:"id"`
	UserID                    string `json:"user_id,omitempty"`
	Scope                     string `json:"scope"`
	StableKey                 string `json:"stable_key"`
	SourceType                string `json:"source_type,omitempty"`
	SourceID                  string `json:"source_id,omitempty"`
	Status                    string `json:"status"`
	QuantityScaled            int64  `json:"quantity_scaled"`
	InputTokens               int64  `json:"input_tokens"`
	OutputTokens              int64  `json:"output_tokens"`
	TotalTokens               int64  `json:"total_tokens"`
	UsageComplete             bool   `json:"usage_complete"`
	PlatformConfirmedAt       string `json:"platform_confirmed_at,omitempty"`
	PlatformEvidenceRef       string `json:"platform_evidence_ref,omitempty"`
	PriceVersionID            string `json:"price_version_id,omitempty"`
	PriceVersion              int64  `json:"price_version,omitempty"`
	QuoteMicros               string `json:"quote_micros"`
	QuoteMinor                string `json:"quote_minor"`
	CostMicros                string `json:"cost_micros"`
	ChargedMinor              string `json:"charged_minor"`
	SettlementThresholdMicros string `json:"settlement_threshold_micros,omitempty"`
	HoldID                    string `json:"hold_id,omitempty"`
	SettleJournalID           string `json:"settle_journal_id,omitempty"`
	ReleaseJournalID          string `json:"release_journal_id,omitempty"`
	LastErrorCode             string `json:"last_error_code,omitempty"`
	CreatedAt                 string `json:"created_at"`
	UpdatedAt                 string `json:"updated_at"`
	SettledAt                 string `json:"settled_at,omitempty"`
	createdAtValue            time.Time
}

type UsagePage struct {
	Items      []UsageRecord `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

type RecordUsageInput struct {
	UserID         string
	Scope          string
	StableKey      string
	SourceType     string
	SourceID       string
	QuantityScaled int64
	InputTokens    int64
	OutputTokens   int64
	UsageComplete  bool
	// Uncharged 表示平台已确认、但既定不扣费的事实。只能由服务端内部调用设置，
	// 不得从 HTTP 请求解码。true 时不读取当前新发布价格，也不检查收费 Gate。
	Uncharged           bool `json:"-"`
	PriceVersionID      string
	PlatformConfirmedAt *time.Time
	PlatformEvidenceRef string
}

type UsageSettlement struct {
	UsageID          string `json:"usage_id"`
	State            string `json:"state"`
	CostMicros       string `json:"cost_micros"`
	ChargedMinor     string `json:"charged_minor"`
	HoldID           string `json:"hold_id,omitempty"`
	SettleJournalID  string `json:"settle_journal_id,omitempty"`
	ReleaseJournalID string `json:"release_journal_id,omitempty"`
	ErrorCode        string `json:"error_code,omitempty"`
}

func scanUsage(row pgx.Row) (*UsageRecord, error) {
	var record UsageRecord
	var confirmedAt, settledAt pgtype.Timestamptz
	var priceVersion, threshold int64
	var quoteMicros, quoteMinor, costMicros, chargedMinor int64
	var createdAt, updatedAt time.Time
	if err := row.Scan(&record.ID, &record.UserID, &record.Scope, &record.StableKey, &record.Status,
		&record.QuantityScaled, &record.InputTokens, &record.OutputTokens, &record.TotalTokens,
		&record.UsageComplete, &confirmedAt, &record.PlatformEvidenceRef,
		&record.PriceVersionID, &priceVersion, &quoteMicros, &quoteMinor, &costMicros,
		&chargedMinor, &threshold, &record.HoldID, &record.SettleJournalID,
		&record.ReleaseJournalID, &record.LastErrorCode, &createdAt, &updatedAt, &settledAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "用量记录不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取用量记录")
	}
	record.PriceVersion = priceVersion
	record.QuoteMicros = formatAmount(quoteMicros)
	record.QuoteMinor = formatAmount(quoteMinor)
	record.CostMicros = formatAmount(costMicros)
	record.ChargedMinor = formatAmount(chargedMinor)
	if threshold > 0 {
		record.SettlementThresholdMicros = formatAmount(threshold)
	}
	if confirmedAt.Valid {
		record.PlatformConfirmedAt = utcTime(confirmedAt.Time)
	}
	record.CreatedAt = utcTime(createdAt)
	record.UpdatedAt = utcTime(updatedAt)
	record.createdAtValue = createdAt
	if settledAt.Valid {
		record.SettledAt = utcTime(settledAt.Time)
	}
	return &record, nil
}

const usageSelect = `SELECT id, user_id, scope, stable_key, status, quantity_scaled,
	input_tokens, output_tokens, total_tokens, usage_complete, platform_confirmed_at,
	coalesce(platform_evidence_ref, ''), coalesce(price_version_id::text, ''),
	coalesce((SELECT pv.version FROM public.price_versions pv WHERE pv.id = usage_records.price_version_id), 0),
	coalesce(quote_micros, 0), coalesce(quote_minor, 0),
	coalesce(cost_micros, 0), coalesce(charged_minor, 0), coalesce(settlement_threshold_micros, 0),
	coalesce(hold_id::text, ''), coalesce(settle_journal_id::text, ''),
	coalesce(release_journal_id::text, ''), coalesce(last_error_code, ''),
	created_at, updated_at, settled_at
	FROM public.usage_records`

func (s *Service) RecordUsage(ctx context.Context, tx pgx.Tx, input RecordUsageInput) (*UsageRecord, error) {
	if err := requireTx(tx); err != nil {
		return nil, err
	}
	if !web.ValidID(input.UserID) {
		return nil, fault.New("validation_failed", "用户编号无效")
	}
	if input.Scope != ScopeServiceTaskPoint && input.Scope != ScopeTikuTokens {
		return nil, fault.New("validation_failed", "计费范围无效")
	}
	if err := validKey(input.StableKey); err != nil {
		return nil, err
	}
	if input.Scope == ScopeServiceTaskPoint {
		if err := validateQuantity(input.QuantityScaled); err != nil {
			return nil, err
		}
		if input.InputTokens != 0 || input.OutputTokens != 0 {
			return nil, fault.New("validation_failed", "任务点用量不能包含 token")
		}
	} else {
		if err := validateTokens(input.InputTokens, input.OutputTokens); err != nil {
			return nil, err
		}
	}
	confirmedAt := ""
	if input.PlatformConfirmedAt != nil {
		confirmedAt = input.PlatformConfirmedAt.UTC().Format(time.RFC3339Nano)
	}
	requestDigest := digest("usage", input.Scope, input.UserID, input.StableKey,
		formatAmount(int64(input.QuantityScaled)), formatAmount(input.InputTokens),
		formatAmount(input.OutputTokens), boolText(input.UsageComplete),
		boolText(input.Uncharged), input.PriceVersionID,
		input.SourceType, input.SourceID, confirmedAt, input.PlatformEvidenceRef)
	if err := s.advisoryLocks(ctx, tx, "usage:"+input.Scope+":"+input.UserID+":"+input.StableKey); err != nil {
		return nil, err
	}
	existing, err := scanUsage(tx.QueryRow(ctx, usageSelect+` WHERE scope = $1 AND user_id = $2 AND stable_key = $3`,
		input.Scope, input.UserID, input.StableKey))
	if err == nil {
		var storedDigest string
		if queryErr := tx.QueryRow(ctx, `SELECT request_digest FROM public.usage_records
			WHERE id = $1`, existing.ID).Scan(&storedDigest); queryErr != nil {
			return nil, fault.New("service_unavailable", "无法核对用量幂等键")
		}
		if storedDigest != requestDigest {
			return nil, fault.New("conflict", "相同用量业务键已用于不同内容")
		}
		return existing, nil
	}
	if code, _ := fault.Describe(err); code != "not_found" {
		return nil, err
	}
	settings, err := s.loadBillingSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	runtimeAllowed := s.chargingAuthorized()
	var price *priceSnapshot
	if input.Uncharged {
		// 不扣费事实不读取当前新发布价格；只有调用方显式冻结的原版本才可用于展示成本。
		runtimeAllowed = false
		if input.PriceVersionID != "" {
			price, err = s.loadPrice(ctx, tx, input.Scope, input.PriceVersionID)
			if err != nil {
				return nil, err
			}
			if price == nil {
				return nil, fault.New("not_configured", "冻结价格版本不存在或不属于该计费范围")
			}
		}
	} else {
		price, err = s.loadPrice(ctx, tx, input.Scope, input.PriceVersionID)
		if err != nil {
			return nil, err
		}
	}
	quote, err := buildQuote(input.Scope, price, settings, runtimeAllowed,
		input.QuantityScaled, input.InputTokens, input.OutputTokens)
	if err != nil {
		return nil, err
	}
	status := "quoted"
	switch quote.State {
	case "unpriced":
		status = "unpriced"
	case "disabled":
		status = "disabled"
	}
	quoteMinor, err := parseZeroAmount(quote.MaxChargeMinor)
	if err != nil {
		return nil, err
	}
	quoteMicros, err := parseMicros(quote.CostMicros)
	if err != nil {
		return nil, err
	}
	usageID := newUUID()
	var priceID any
	var threshold any
	if quote.PriceVersionID != "" {
		priceID = quote.PriceVersionID
	}
	if quote.State != "unpriced" && quote.SettlementThresholdMicros != "" {
		threshold = quote.SettlementThresholdMicros
	}
	var confirmed any
	if input.PlatformConfirmedAt != nil {
		confirmed = input.PlatformConfirmedAt.UTC()
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.usage_records (
		id, user_id, scope, stable_key, source_type, source_id, status, quantity_scaled,
		input_tokens, output_tokens, total_tokens, usage_complete, platform_confirmed_at,
		platform_evidence_ref, price_version_id, quote_micros, quote_minor,
		settlement_threshold_micros, request_digest, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$20)`,
		usageID, input.UserID, input.Scope, input.StableKey, nullableText(input.SourceType),
		nullableText(input.SourceID), status, input.QuantityScaled, input.InputTokens,
		input.OutputTokens, input.InputTokens+input.OutputTokens, input.UsageComplete, confirmed,
		nullableText(input.PlatformEvidenceRef), priceID, quoteMicros, quoteMinor, threshold,
		requestDigest, s.now()); err != nil {
		return nil, fault.New("service_unavailable", "无法写入用量记录")
	}
	return scanUsage(tx.QueryRow(ctx, usageSelect+` WHERE id = $1`, usageID))
}

func boolText(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func parseZeroAmount(value string) (int64, error) {
	if value == "" || value == "0" {
		return 0, nil
	}
	return parseAmount(value)
}

func parseMicros(value string) (int64, error) {
	if value == "" || len(value) > 19 {
		return 0, fault.New("validation_failed", "微元金额格式无效")
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fault.New("validation_failed", "微元金额格式无效")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fault.New("validation_failed", "微元金额超出范围")
	}
	return parsed, nil
}

func (s *Service) getUsageForUpdate(ctx context.Context, tx pgx.Tx, usageID string) (*UsageRecord, error) {
	if !web.ValidID(usageID) {
		return nil, fault.New("validation_failed", "用量编号无效")
	}
	return scanUsage(tx.QueryRow(ctx, usageSelect+` WHERE id = $1 FOR UPDATE`, usageID))
}

// ReserveUsage 为已报价用量创建预占；同一用量只允许一个预占。
func (s *Service) ReserveUsage(ctx context.Context, tx pgx.Tx, usageID string) (string, error) {
	if err := requireTx(tx); err != nil {
		return "", err
	}
	if err := s.advisoryLocks(ctx, tx, "usage-reserve:"+usageID); err != nil {
		return "", err
	}
	usage, err := s.getUsageForUpdate(ctx, tx, usageID)
	if err != nil {
		return "", err
	}
	if usage.Status == "reserved" {
		return usage.HoldID, nil
	}
	if usage.Status != "quoted" {
		return "", fault.New("conflict", "当前用量状态不能创建预占")
	}
	amount, err := parseZeroAmount(usage.QuoteMinor)
	if err != nil {
		return "", err
	}
	if amount > maxAmountMinor {
		return "", fault.New("validation_failed", "预占金额超出上限")
	}
	holdID := ""
	if amount > 0 {
		holdID, err = s.Reserve(ctx, tx, usage.UserID, "usage:"+usageID, amount)
		if err != nil {
			return "", err
		}
		sourceType := "tiku_query"
		if usage.Scope == ScopeServiceTaskPoint {
			sourceType = ScopeServiceTaskPoint
		}
		if _, err := tx.Exec(ctx, `UPDATE public.holds SET source_type = $2, source_id = $3,
			version = version + 1, updated_at = $4 WHERE id = $1`,
			holdID, sourceType, usage.ID, s.now()); err != nil {
			return "", fault.New("service_unavailable", "无法标记预占用途")
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE public.usage_records SET hold_id = $2, status = 'reserved',
		updated_at = $3 WHERE id = $1`, usage.ID, nullableID(holdID), s.now()); err != nil {
		return "", fault.New("service_unavailable", "无法更新用量预占状态")
	}
	return holdID, nil
}

func usageJournalKey(usageID, stage string) string {
	return "u:" + usageID + ":" + stage
}

func (s *Service) releaseUsageHold(ctx context.Context, tx pgx.Tx, usage *UsageRecord) (string, error) {
	if usage.HoldID == "" {
		return "", nil
	}
	key := usageJournalKey(usage.ID, "release")
	if err := s.Release(ctx, tx, usage.HoldID, key); err != nil {
		return "", err
	}
	var journalID string
	if err := tx.QueryRow(ctx, `SELECT id FROM public.journals WHERE business_key = $1`,
		"release:"+key).Scan(&journalID); err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", fault.New("service_unavailable", "无法读取释放分录")
	}
	return journalID, nil
}

// SettleUsage 按已存价格与用量结算；未完整、未核验或未定价时只释放预占，不扣费。
func (s *Service) SettleUsage(ctx context.Context, tx pgx.Tx, usageID string) (UsageSettlement, error) {
	if err := requireTx(tx); err != nil {
		return UsageSettlement{}, err
	}
	if err := s.advisoryLocks(ctx, tx, "usage-settle:"+usageID); err != nil {
		return UsageSettlement{}, err
	}
	usage, err := s.getUsageForUpdate(ctx, tx, usageID)
	if err != nil {
		return UsageSettlement{}, err
	}
	if usage.Status == "settled" {
		return UsageSettlement{UsageID: usage.ID, State: "settled", CostMicros: usage.CostMicros,
			ChargedMinor: usage.ChargedMinor, HoldID: usage.HoldID,
			SettleJournalID: usage.SettleJournalID, ReleaseJournalID: usage.ReleaseJournalID}, nil
	}
	if usage.Status == "released" || usage.Status == "needs_review" {
		return UsageSettlement{UsageID: usage.ID, State: usage.Status, CostMicros: usage.CostMicros,
			ChargedMinor: usage.ChargedMinor, HoldID: usage.HoldID,
			SettleJournalID: usage.SettleJournalID, ReleaseJournalID: usage.ReleaseJournalID,
			ErrorCode: usage.LastErrorCode}, nil
	}
	if usage.PriceVersionID == "" {
		return s.finishWithoutCharge(ctx, tx, usage, "released", "price_not_configured", 0)
	}
	if usage.Status != "reserved" {
		status := "released"
		if usage.Status == "quoted" {
			status = "needs_review"
		}
		return s.finishWithoutCharge(ctx, tx, usage, status, "usage_not_reserved", 0)
	}
	settings, err := s.loadBillingSettings(ctx, tx)
	if err != nil {
		return UsageSettlement{}, err
	}
	price, err := s.loadPrice(ctx, tx, usage.Scope, usage.PriceVersionID)
	if err != nil {
		return UsageSettlement{}, err
	}
	if price == nil {
		return s.finishWithoutCharge(ctx, tx, usage, "released", "price_not_configured", 0)
	}
	enabled := settings.ServiceChargeEnabled
	if usage.Scope == ScopeTikuTokens {
		enabled = settings.TikuChargeEnabled
	}
	if !enabled || !s.chargingAuthorized() {
		return s.finishWithoutCharge(ctx, tx, usage, "released", "charging_disabled", 0)
	}
	if usage.Scope == ScopeTikuTokens && !usage.UsageComplete {
		cost, costErr := tikuCostMicros(usage.InputTokens, usage.OutputTokens,
			price.InputMicrosPerMillion, price.OutputMicrosPerMillion)
		if costErr != nil {
			return UsageSettlement{}, costErr
		}
		costInt, costErr := bigInt64(cost, "题库成本")
		if costErr != nil {
			return UsageSettlement{}, costErr
		}
		return s.finishWithoutCharge(ctx, tx, usage, "needs_review", "usage_incomplete", costInt)
	}
	if usage.Scope == ScopeServiceTaskPoint && usage.PlatformConfirmedAt == "" {
		cost, costErr := serviceCostMicros(usage.QuantityScaled, price.PriceMicros, price.UnitPrecision)
		if costErr != nil {
			return UsageSettlement{}, costErr
		}
		costInt, costErr := bigInt64(cost, "服务用量成本")
		if costErr != nil {
			return UsageSettlement{}, costErr
		}
		return s.finishWithoutCharge(ctx, tx, usage, "needs_review", "platform_unconfirmed", costInt)
	}
	wallet, err := s.ensureWallet(ctx, tx, usage.UserID)
	if err != nil {
		return UsageSettlement{}, err
	}
	var hold *holdRow
	if usage.HoldID != "" {
		hold, err = scanHold(tx.QueryRow(ctx, `SELECT id, user_id, wallet_id, business_key, request_digest,
			amount_minor, settled_minor, released_minor, status, source_type, coalesce(source_id, '')
			FROM public.holds WHERE id = $1 FOR UPDATE`, usage.HoldID))
		if err != nil {
			return UsageSettlement{}, err
		}
	}
	if usage.Scope == ScopeServiceTaskPoint {
		cost, err := serviceCostMicros(usage.QuantityScaled, price.PriceMicros, price.UnitPrecision)
		if err != nil {
			return UsageSettlement{}, err
		}
		charge, err := microsToMinor(cost, price.Rounding, price.MinChargeMinor)
		if err != nil {
			return UsageSettlement{}, err
		}
		return s.settleComputedUsage(ctx, tx, usage, wallet, hold, cost, charge, 0)
	}
	cost, err := tikuCostMicros(usage.InputTokens, usage.OutputTokens,
		price.InputMicrosPerMillion, price.OutputMicrosPerMillion)
	if err != nil {
		return UsageSettlement{}, err
	}
	total := new(big.Int).Add(big.NewInt(wallet.TikuAccruedMicros), cost)
	threshold := price.SettlementThresholdMicros
	charge := new(big.Int).Quo(new(big.Int).Set(total), big.NewInt(threshold))
	remainder := new(big.Int).Mod(new(big.Int).Set(total), big.NewInt(threshold))
	chargeMinor, err := bigInt64(charge, "题库结算金额")
	if err != nil {
		return UsageSettlement{}, err
	}
	remainderMicros, err := bigInt64(remainder, "题库尾差")
	if err != nil {
		return UsageSettlement{}, err
	}
	return s.settleComputedUsageWithAccrual(ctx, tx, usage, wallet, hold, cost, chargeMinor, remainderMicros)
}

func (s *Service) finishWithoutCharge(ctx context.Context, tx pgx.Tx, usage *UsageRecord,
	status, errorCode string, costMicros int64) (UsageSettlement, error) {
	releaseJournal, err := s.releaseUsageHold(ctx, tx, usage)
	if err != nil {
		return UsageSettlement{}, err
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE public.usage_records SET status = $2, cost_micros = $3,
		charged_minor = 0, release_journal_id = $4, last_error_code = $5,
		settled_at = $6, updated_at = $6 WHERE id = $1`,
		usage.ID, status, costMicros, nullableID(releaseJournal), errorCode, now); err != nil {
		return UsageSettlement{}, fault.New("service_unavailable", "无法更新用量结算状态")
	}
	if err := s.audit(ctx, tx, actorFromID("system", ""), "usage.review_required",
		"usage_record", usage.ID, errorCode); err != nil {
		return UsageSettlement{}, err
	}
	return UsageSettlement{UsageID: usage.ID, State: status, CostMicros: formatAmount(costMicros),
		ChargedMinor: "0", HoldID: usage.HoldID, ReleaseJournalID: releaseJournal,
		ErrorCode: errorCode}, nil
}

func (s *Service) settleComputedUsage(ctx context.Context, tx pgx.Tx, usage *UsageRecord,
	wallet *walletRow, hold *holdRow, cost *big.Int, charge int64, accrual int64) (UsageSettlement, error) {
	costMicros, err := bigInt64(cost, "用量成本")
	if err != nil {
		return UsageSettlement{}, err
	}
	if hold != nil && charge > s.holdRemaining(hold) {
		result, err := s.finishWithoutCharge(ctx, tx, usage, "needs_review", "hold_amount_exceeded",
			costMicros)
		if err != nil {
			return UsageSettlement{}, err
		}
		return result, nil
	}
	if hold == nil && charge > 0 {
		result, err := s.finishWithoutCharge(ctx, tx, usage, "needs_review", "hold_missing",
			costMicros)
		if err != nil {
			return UsageSettlement{}, err
		}
		return result, nil
	}
	settleJournal := ""
	if charge > 0 {
		settleJournal, err = s.settleHoldMeta(ctx, tx, usage.HoldID,
			usageJournalKey(usage.ID, "capture"), charge, usage.ID, usage.PriceVersionID)
		if err != nil {
			return UsageSettlement{}, err
		}
	}
	releaseJournal, err := s.releaseUsageHold(ctx, tx, usage)
	if err != nil {
		return UsageSettlement{}, err
	}
	if usage.Scope == ScopeTikuTokens {
		if err := s.setTikuAccrual(ctx, tx, wallet, accrual); err != nil {
			return UsageSettlement{}, err
		}
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE public.usage_records SET status = 'settled', cost_micros = $2,
		charged_minor = $3, settle_journal_id = $4, release_journal_id = $5,
		last_error_code = NULL, settled_at = $6, updated_at = $6 WHERE id = $1`,
		usage.ID, costMicros, charge, nullableID(settleJournal), nullableID(releaseJournal), now); err != nil {
		return UsageSettlement{}, fault.New("service_unavailable", "无法更新用量结算")
	}
	if charge > 0 {
		if err := s.audit(ctx, tx, actorFromID("system", ""), "usage.settled",
			"usage_record", usage.ID, "按价格版本结算已确认用量"); err != nil {
			return UsageSettlement{}, err
		}
	}
	return UsageSettlement{UsageID: usage.ID, State: "settled", CostMicros: formatAmount(costMicros),
		ChargedMinor: formatAmount(charge), HoldID: usage.HoldID,
		SettleJournalID: settleJournal, ReleaseJournalID: releaseJournal}, nil
}

func (s *Service) settleComputedUsageWithAccrual(ctx context.Context, tx pgx.Tx, usage *UsageRecord,
	wallet *walletRow, hold *holdRow, cost *big.Int, charge int64, accrual int64) (UsageSettlement, error) {
	costMicros, err := bigInt64(cost, "题库成本")
	if err != nil {
		return UsageSettlement{}, err
	}
	if hold != nil && charge > s.holdRemaining(hold) {
		result, err := s.finishWithoutCharge(ctx, tx, usage, "needs_review", "hold_amount_exceeded",
			costMicros)
		if err != nil {
			return UsageSettlement{}, err
		}
		return result, nil
	}
	if hold == nil && charge > 0 {
		result, err := s.finishWithoutCharge(ctx, tx, usage, "needs_review", "hold_missing",
			costMicros)
		if err != nil {
			return UsageSettlement{}, err
		}
		return result, nil
	}
	settleJournal := ""
	if charge > 0 {
		settleJournal, err = s.settleHoldMeta(ctx, tx, usage.HoldID,
			usageJournalKey(usage.ID, "capture"), charge, usage.ID, usage.PriceVersionID)
		if err != nil {
			return UsageSettlement{}, err
		}
	}
	releaseJournal, err := s.releaseUsageHold(ctx, tx, usage)
	if err != nil {
		return UsageSettlement{}, err
	}
	if err := s.setTikuAccrual(ctx, tx, wallet, accrual); err != nil {
		return UsageSettlement{}, err
	}
	if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
		"entry": "tiku_accrual", "usage_id": usage.ID,
	}); err != nil {
		return UsageSettlement{}, err
	}
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE public.usage_records SET status = 'settled', cost_micros = $2,
		charged_minor = $3, settle_journal_id = $4, release_journal_id = $5,
		last_error_code = NULL, settled_at = $6, updated_at = $6 WHERE id = $1`,
		usage.ID, costMicros, charge, nullableID(settleJournal), nullableID(releaseJournal), now); err != nil {
		return UsageSettlement{}, fault.New("service_unavailable", "无法更新题库用量结算")
	}
	if charge > 0 {
		if err := s.audit(ctx, tx, actorFromID("system", ""), "usage.settled",
			"usage_record", usage.ID, "按价格版本结算题库 token 用量"); err != nil {
			return UsageSettlement{}, err
		}
	}
	return UsageSettlement{UsageID: usage.ID, State: "settled", CostMicros: formatAmount(costMicros),
		ChargedMinor: formatAmount(charge), HoldID: usage.HoldID,
		SettleJournalID: settleJournal, ReleaseJournalID: releaseJournal}, nil
}

func (s *Service) ListUsage(ctx context.Context, userID, cursor string, limit int) (UsagePage, error) {
	if !web.ValidID(userID) {
		return UsagePage{}, fault.New("validation_failed", "用户编号无效")
	}
	if limit < 1 || limit > 100 {
		return UsagePage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return UsagePage{}, err
	}
	where := "user_id = $1"
	args := []any{userID}
	if decoded.id != "" {
		where += " AND (created_at < $2 OR (created_at = $2 AND id < $3))"
		args = append(args, decoded.createdAt, decoded.id)
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, usageSelect+` WHERE `+where+`
		ORDER BY created_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return UsagePage{}, fault.New("service_unavailable", "无法读取用量记录")
	}
	defer rows.Close()
	items := make([]UsageRecord, 0, limit+1)
	for rows.Next() {
		record, err := scanUsage(rows)
		if err != nil {
			return UsagePage{}, err
		}
		items = append(items, *record)
	}
	if err := rows.Err(); err != nil {
		return UsagePage{}, fault.New("service_unavailable", "无法读取用量记录")
	}
	page := UsagePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAtValue, id: last.ID})
	}
	return page, nil
}

func itoa(value int) string { return strconv.Itoa(value) }

func utcTime(value time.Time) string { return value.UTC().Format(time.RFC3339) }
