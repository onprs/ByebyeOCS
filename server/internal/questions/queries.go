package questions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const queryColumns = `id, user_id::text, idempotency_key, request_fingerprint, offering_id::text,
	offering_public_id, offering_display_name, mode, cache_scope, scope_key_hash, COALESCE(question_title, ''),
	COALESCE(question_options, ''), question_type, image_count, status, COALESCE(answer, ''), cache_hit,
	COALESCE(cache_entry_id, ''), cache_created_at, usage, pricing, billing_state,
	COALESCE(billing_hold_id, ''), reserved_minor, content_expired,
	COALESCE(error_code, ''), fork_request_id, version, created_at, updated_at, completed_at`

type queryRecord struct {
	ID                  string
	UserID              string
	IdempotencyKey      string
	Fingerprint         string
	OfferingID          string
	OfferingPublicID    string
	OfferingDisplayName string
	Mode                string
	CacheScope          string
	ScopeKeyHash        string
	QuestionTitle       string
	QuestionOptions     string
	QuestionType        string
	ImageCount          int
	Status              string
	Answer              string
	CacheHit            bool
	CacheEntryID        string
	CacheCreatedAt      *time.Time
	Usage               []byte
	Pricing             []byte
	BillingState        string
	BillingHoldID       string
	ReservedMinor       int64
	ContentExpired      bool
	ErrorCode           string
	ForkRequestID       string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CompletedAt         *time.Time
}

func scanQuery(row pgx.Row) (*queryRecord, error) {
	var q queryRecord
	err := row.Scan(&q.ID, &q.UserID, &q.IdempotencyKey, &q.Fingerprint, &q.OfferingID,
		&q.OfferingPublicID, &q.OfferingDisplayName, &q.Mode, &q.CacheScope, &q.ScopeKeyHash, &q.QuestionTitle,
		&q.QuestionOptions, &q.QuestionType, &q.ImageCount, &q.Status, &q.Answer, &q.CacheHit,
		&q.CacheEntryID, &q.CacheCreatedAt, &q.Usage, &q.Pricing, &q.BillingState,
		&q.BillingHoldID, &q.ReservedMinor, &q.ContentExpired,
		&q.ErrorCode, &q.ForkRequestID, &q.Version, &q.CreatedAt, &q.UpdatedAt, &q.CompletedAt)
	if err != nil {
		return nil, err
	}
	return &q, nil
}

type pricingSnapshot struct {
	State                    string           `json:"state"`
	Pricing                  *OfferingPricing `json:"pricing,omitempty"`
	PriceVersion             string           `json:"price_version,omitempty"`
	Rule                     string           `json:"rule,omitempty"`
	Rounding                 string           `json:"rounding,omitempty"`
	EstimatedInputTokens     int64            `json:"estimated_input_tokens,omitempty"`
	EstimatedOutputTokens    int64            `json:"estimated_output_tokens,omitempty"`
	EstimatedCostMicros      int64            `json:"estimated_cost_micros,omitempty"`
	EstimatedAmountMinor     int64            `json:"estimated_amount_minor,omitempty"`
	InputTokens              int64            `json:"input_tokens,omitempty"`
	OutputTokens             int64            `json:"output_tokens,omitempty"`
	CostMicros               int64            `json:"cost_micros,omitempty"`
	AmountMinor              int64            `json:"amount_minor,omitempty"`
	ReservedMinor            int64            `json:"reserved_minor,omitempty"`
	Charged                  bool             `json:"charged"`
	BillingState             string           `json:"billing_state"`
	HoldID                   string           `json:"hold_id,omitempty"`
	AutomaticChargingAllowed bool             `json:"automatic_charging_allowed"`
	ChargingCheckedAt        time.Time        `json:"charging_checked_at,omitempty"`
}

type pricingView struct {
	PriceVersion             string         `json:"price_version,omitempty"`
	State                    string         `json:"state"`
	Rule                     string         `json:"rule,omitempty"`
	Rounding                 string         `json:"rounding,omitempty"`
	Unit                     string         `json:"unit,omitempty"`
	Pricing                  *PublicPricing `json:"pricing,omitempty"`
	CostMicros               string         `json:"cost_micros,omitempty"`
	AmountMinor              string         `json:"amount_minor,omitempty"`
	Charged                  bool           `json:"charged"`
	BillingState             string         `json:"billing_state"`
	AutomaticChargingAllowed bool           `json:"automatic_charging_allowed"`
	ChargingCheckedAt        *time.Time     `json:"charging_checked_at,omitempty"`
}

type CacheView struct {
	Hit       bool       `json:"hit"`
	EntryID   *string    `json:"entry_id,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type ModelRef struct {
	OfferingID  string `json:"offering_id"`
	DisplayName string `json:"display_name"`
}

type QuestionView struct {
	Title      string `json:"title"`
	Options    string `json:"options"`
	Type       string `json:"type"`
	ImageCount int    `json:"image_count"`
}

type QueryView struct {
	ID             string       `json:"id"`
	Status         string       `json:"status"`
	Mode           string       `json:"mode"`
	Model          ModelRef     `json:"model"`
	Question       QuestionView `json:"question"`
	ContentExpired bool         `json:"content_expired"`
	Cache          CacheView    `json:"cache"`
	Answer         string       `json:"answer,omitempty"`
	Usage          Usage        `json:"usage"`
	Pricing        pricingView  `json:"pricing"`
	ReviewReason   string       `json:"review_reason,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	CompletedAt    *time.Time   `json:"completed_at,omitempty"`
}

func buildQueryView(q *queryRecord) QueryView {
	var usage Usage
	if len(q.Usage) > 0 {
		_ = json.Unmarshal(q.Usage, &usage)
	}
	var snapshot pricingSnapshot
	if len(q.Pricing) > 0 {
		_ = json.Unmarshal(q.Pricing, &snapshot)
	}
	if snapshot.State == "" {
		snapshot.State = "unpriced"
	}
	createdAt := q.CreatedAt.UTC()
	var completedAt *time.Time
	if q.CompletedAt != nil {
		value := q.CompletedAt.UTC()
		completedAt = &value
	}
	expired := q.ContentExpired || ((q.Status == "answered" || q.Status == "cache_hit") && (q.QuestionTitle == "" || q.Answer == ""))
	view := QueryView{
		ID: q.ID, Status: q.Status, Mode: q.Mode,
		Model:          ModelRef{OfferingID: q.OfferingPublicID, DisplayName: q.OfferingDisplayName},
		Question:       QuestionView{Title: q.QuestionTitle, Options: q.QuestionOptions, Type: q.QuestionType, ImageCount: q.ImageCount},
		ContentExpired: expired,
		Cache:          CacheView{Hit: q.CacheHit},
		Answer:         q.Answer, Usage: usage,
		Pricing: pricingView{PriceVersion: snapshot.PriceVersion, State: snapshot.State, Rule: snapshot.Rule,
			Rounding: snapshot.Rounding, Unit: "micros_per_million_tokens", Pricing: snapshot.Pricing.Public(),
			Charged: snapshot.Charged, BillingState: snapshot.BillingState,
			AutomaticChargingAllowed: snapshot.AutomaticChargingAllowed},
		CreatedAt: createdAt, CompletedAt: completedAt,
	}
	if q.CacheEntryID != "" {
		view.Cache.EntryID = &q.CacheEntryID
	}
	if q.CacheCreatedAt != nil {
		value := q.CacheCreatedAt.UTC()
		view.Cache.CreatedAt = &value
	}
	if snapshot.State == "quoted" || snapshot.State == "free" || snapshot.State == "cache_hit" {
		view.Pricing.CostMicros = fmt.Sprintf("%d", snapshot.CostMicros)
		view.Pricing.AmountMinor = fmt.Sprintf("%d", snapshot.AmountMinor)
	}
	if !snapshot.ChargingCheckedAt.IsZero() {
		checked := snapshot.ChargingCheckedAt.UTC()
		view.Pricing.ChargingCheckedAt = &checked
	}
	if q.Status == "needs_review" && q.ErrorCode != "" {
		view.ReviewReason = q.ErrorCode
	}
	return view
}

func snapshotFromQuote(quote Quote, reserved int64, holdID, billingState string) pricingSnapshot {
	var priceVersion, rule, rounding string
	if quote.Pricing != nil {
		priceVersion, rule, rounding = quote.Pricing.Version, quote.Pricing.Rule, quote.Pricing.Rounding
	}
	snapshot := pricingSnapshot{
		State: quote.State, Pricing: quote.Pricing, PriceVersion: priceVersion,
		Rule: rule, Rounding: rounding,
		InputTokens: quote.InputTokens, OutputTokens: quote.OutputTokens,
		CostMicros: quote.CostMicros, AmountMinor: quote.AmountMinor,
		ReservedMinor: reserved, Charged: false, BillingState: billingState, HoldID: holdID,
		AutomaticChargingAllowed: quote.ChargingAllowed, ChargingCheckedAt: quote.ChargingCheckedAt.UTC(),
	}
	if quote.Estimated {
		snapshot.EstimatedInputTokens = quote.InputTokens
		snapshot.EstimatedOutputTokens = quote.OutputTokens
		snapshot.EstimatedCostMicros = quote.CostMicros
		snapshot.EstimatedAmountMinor = quote.AmountMinor
		snapshot.InputTokens = 0
		snapshot.OutputTokens = 0
		snapshot.CostMicros = 0
		snapshot.AmountMinor = 0
	}
	return snapshot
}

func (s *Service) insertPendingQuery(ctx context.Context, queryID string, actorID string, idem, fp string,
	offering *Offering, input QueryInput, scopeHash string, images imageValidation) (bool, *queryRecord, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO public.question_queries
		(id, user_id, idempotency_key, request_fingerprint, offering_id, offering_public_id, offering_display_name,
		 mode, cache_scope, scope_key_hash, question_title, question_options, question_type, image_count,
		 status, usage, pricing, billing_state, fork_request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,'pending','{}','{}','pending',$1)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING`,
		queryID, actorID, idem, fp, offering.ID, offering.PublicID, offering.DisplayName,
		input.Mode, input.CacheScope, scopeHash, input.Question.Title, input.Question.Options,
		input.Question.Type, images.Count)
	if err != nil {
		return false, nil, err
	}
	q, err := s.loadQueryByUserKey(ctx, actorID, idem)
	if err != nil {
		return false, nil, err
	}
	if q == nil {
		return false, nil, errors.New("幂等记录缺失")
	}
	return tag.RowsAffected() == 1, q, nil
}

func (s *Service) loadQueryByUserKey(ctx context.Context, userID, key string) (*queryRecord, error) {
	q, err := scanQuery(s.pool.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = $2`, userID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return q, err
}

func (s *Service) loadQueryByID(ctx context.Context, userID, id string) (*queryRecord, error) {
	q, err := scanQuery(s.pool.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries
		WHERE id = $1 AND user_id = $2`, id, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return q, err
}

func (s *Service) loadQueryAdmin(ctx context.Context, id string) (*queryRecord, error) {
	q, err := scanQuery(s.pool.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return q, err
}

func (s *Service) waitForQuery(ctx context.Context, userID, key string) (*queryRecord, error) {
	deadline := time.Now().Add(8 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		q, err := s.loadQueryByUserKey(ctx, userID, key)
		if err != nil {
			return nil, err
		}
		if q == nil {
			return nil, errors.New("查询记录丢失")
		}
		if q.Status != "pending" {
			return q, nil
		}
		// 崩溃遗留的 pending 可以安全重驱动；fork 使用同一 request_id 幂等。
		if s.now().Sub(q.UpdatedAt) > 2*time.Minute {
			return q, nil
		}
		if time.Now().After(deadline) {
			return nil, fault.New("conflict", "相同请求仍在处理中，请稍后重试")
		}
		select {
		case <-ctx.Done():
			return nil, fault.New("conflict", "相同请求仍在处理中，请稍后重试")
		case <-ticker.C:
		}
	}
}

func (s *Service) createQuery(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		web.Error(w, r, 400, "idempotency_required", "缺少有效的 Idempotency-Key")
		return
	}
	var input QueryInput
	limit := int64(s.maxImg)*(s.maxBytes*4/3+8) + 2<<20
	if limit > s.maxBody {
		limit = s.maxBody
	}
	if !web.DecodeLimit(w, r, &input, limit) {
		return
	}
	images, err := s.validateQuery(&input)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	fp := fingerprint(actor.ID, input)
	// 幂等重放先于模型可用性检查：模型下架后，已完成的同一请求仍返回原结果。
	if existing, err := s.loadQueryByUserKey(r.Context(), actor.ID, idempotencyKey); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取幂等记录")
		return
	} else if existing != nil {
		if existing.Fingerprint != fp {
			web.Error(w, r, 409, "idempotency_conflict", "相同 Idempotency-Key 对应不同请求")
			return
		}
		if existing.Status == "pending" {
			existing, err = s.waitForQuery(r.Context(), actor.ID, idempotencyKey)
			if err != nil {
				web.Fail(w, r, err)
				return
			}
			if existing.Status == "pending" {
				web.Error(w, r, 409, "request_in_progress", "相同请求仍在处理中，请稍后重试")
				return
			}
		}
		if existing.Status == "failed" {
			writeStoredQueryError(w, r, existing.ErrorCode)
			return
		}
		web.JSON(w, r, http.StatusOK, buildQueryView(existing))
		return
	}
	offering, err := s.resolveOffering(r.Context(), input.ModelOfferingID)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if input.Mode == "cache_first" && !offering.CachePolicy.Allow {
		web.Error(w, r, 422, "cache_not_allowed", "该模型未启用答案缓存，请选择每次都使用模型")
		return
	}
	if input.CacheScope == "shared" && offering.CachePolicy.Scope != "shared" {
		web.Error(w, r, 422, "shared_cache_not_allowed", "该模型未允许共享缓存")
		return
	}
	scopeKey := s.scopeKeyFor(actor.ID)
	scopeHash := hashHex(scopeKey)
	queryID := newPrefixedID("qq_")
	created, existing, err := s.insertPendingQuery(r.Context(), queryID, actor.ID, idempotencyKey, fp, offering, input, scopeHash, images)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法保存题库查询")
		return
	}
	if !created {
		if existing.Fingerprint != fp {
			web.Error(w, r, 409, "idempotency_conflict", "相同 Idempotency-Key 对应不同请求")
			return
		}
		if existing.Status == "pending" {
			existing, err = s.waitForQuery(r.Context(), actor.ID, idempotencyKey)
			if err != nil {
				web.Fail(w, r, err)
				return
			}
			if existing.Status == "pending" {
				web.Error(w, r, 409, "request_in_progress", "相同请求仍在处理中，请稍后重试")
				return
			}
		}
		if existing.Status == "failed" {
			writeStoredQueryError(w, r, existing.ErrorCode)
			return
		}
		web.JSON(w, r, http.StatusOK, buildQueryView(existing))
		return
	}
	estimate, holdID, reservedMinor, err := s.prepareBilling(r.Context(), queryID, actor.ID, offering, input.Budget.MaxOutputTokens, images.Count)
	if err != nil {
		code, _ := fault.Describe(err)
		s.markReserveFailed(r.Context(), queryID, code)
		if code == "insufficient_balance" {
			web.Error(w, r, 409, "insufficient_balance", "余额不足，未发起模型调用")
		} else if code == "charging_setting_unavailable" {
			web.Error(w, r, 503, "service_unavailable", "收费开关状态不可用，未发起付费调用")
		} else {
			web.Error(w, r, 503, "service_unavailable", "无法完成题库预占，未发起模型调用")
		}
		return
	}
	if err := s.requireFork(); err != nil {
		s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: "failed", ErrorCode: "not_configured", Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "released",
		})
		web.Fail(w, r, err)
		return
	}
	forkRequest := ForkAnswerRequest{
		RequestID: queryID, ModelOfferingID: offering.ProviderRef, Mode: input.Mode,
		CacheScope: input.CacheScope, CacheScopeKey: scopeKey,
		Question: ForkQuestion{Title: input.Question.Title, Options: input.Question.Options, Type: input.Question.Type, Images: input.Question.Images},
		Budget:   ForkBudget{MaxOutputTokens: input.Budget.MaxOutputTokens},
	}
	forkCtx, cancel := context.WithTimeout(r.Context(), s.timeout)
	response, forkErr := s.fork.Answer(forkCtx, forkRequest, queryID)
	cancel()
	if forkErr != nil {
		code := "service_unavailable"
		var value *ForkError
		if errors.As(forkErr, &value) {
			code = value.Code
		}
		status := "failed"
		// 超时或取消时上游可能已产生费用；保留查询事实并进入待复核，不自动重放。
		if code == "upstream_timeout" || code == "request_cancelled" || code == "request_unknown" {
			status = "needs_review"
		}
		s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: status, ErrorCode: code, Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "released",
		})
		writeForkQueryError(w, r, forkErr)
		return
	}
	if err := normalizeForkResponse(&response, queryID, offering.ProviderRef, input.Mode); err != nil {
		s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: "needs_review", ErrorCode: "invalid_fork_response", Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "released",
		})
		web.Error(w, r, 502, "invalid_fork_response", "题库响应事实不完整，查询已进入待复核")
		return
	}
	if response.Status == "cache_hit" {
		quote := Quote{State: "cache_hit", Pricing: offering.Pricing,
			ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt}
		view, err := s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: "cache_hit", Response: response, Usage: response.Usage, Quote: quote,
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "no_charge",
		})
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "缓存结果保存或释放预占失败，已进入待复核")
			return
		}
		web.JSON(w, r, http.StatusOK, view)
		return
	}
	if response.Status == "usage_incomplete" || !response.Usage.Complete {
		view, err := s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: "needs_review", ErrorCode: "usage_incomplete", Response: response,
			Usage: response.Usage, Quote: Quote{State: "usage_incomplete", Pricing: offering.Pricing,
				ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt},
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "released",
		})
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "用量待复核查询保存失败")
			return
		}
		web.JSON(w, r, http.StatusOK, view)
		return
	}
	actual, err := s.actualQuote(offering, response.Usage)
	if err == nil {
		actual.ChargingAllowed = estimate.ChargingAllowed
		actual.ChargingCheckedAt = estimate.ChargingCheckedAt
	}
	if err != nil {
		s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
			Status: "needs_review", ErrorCode: "pricing_error", Response: response,
			Usage: response.Usage, Quote: Quote{State: "unpriced", Pricing: offering.Pricing,
				ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt},
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingState: "released",
		})
		web.Error(w, r, 502, "pricing_error", "价格计算失败，查询已进入待复核")
		return
	}
	status, errorCode := "answered", ""
	settle := false
	switch {
	case actual.State == "unpriced":
		actual.Pricing = offering.Pricing
	case actual.State == "free":
		actual.Pricing = offering.Pricing
	case !actual.ChargingAllowed:
		actual.Pricing = offering.Pricing
	case s.billing == nil:
		actual.Pricing = offering.Pricing
	case holdID == "":
		// 预估未形成有效预占但实际出现费用：不自动扣费，转人工复核。
		status, errorCode = "needs_review", "settlement_without_reservation"
	case actual.AmountMinor > reservedMinor:
		status, errorCode = "needs_review", "reserved_amount_exceeded"
	default:
		settle = true
	}
	finalQuote := actual
	finalQuote.Estimated = false
	view, err := s.finalizeQuery(r.Context(), queryID, actor.ID, finalizeInput{
		Status: status, ErrorCode: errorCode, Response: response, Usage: response.Usage,
		Quote: finalQuote, Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor,
		Settle: settle, BillingState: "released",
	})
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "题库结果保存或计费失败，查询已进入待复核")
		return
	}
	web.JSON(w, r, http.StatusOK, view)
}

func (s *Service) resolveOffering(ctx context.Context, publicID string) (*Offering, error) {
	if publicID == "" {
		return s.defaultOffering(ctx)
	}
	return s.offeringByPublicID(ctx, publicID, false)
}

type finalizeInput struct {
	Status        string
	ErrorCode     string
	Response      ForkAnswerResponse
	Usage         Usage
	Quote         Quote
	Estimate      Quote
	HoldID        string
	ReservedMinor int64
	Settle        bool
	BillingState  string
}

func noReserveBillingState(quote Quote, billing bool) string {
	switch {
	case quote.State == "unpriced":
		return "unpriced"
	case quote.State == "free":
		return "no_charge"
	case !quote.ChargingAllowed:
		return "automatic_charging_disabled"
	case !billing:
		return "billing_not_connected"
	case quote.AmountMinor == 0:
		return "no_charge"
	default:
		return "unreserved"
	}
}

// prepareBilling 在任何 fork/LLM 调用前完成价格快照和预算预占。
func (s *Service) prepareBilling(ctx context.Context, queryID, userID string, offering *Offering, outputBudget, imageCount int) (Quote, string, int64, error) {
	estimate, err := s.estimateQuote(offering, outputBudget, imageCount)
	if err != nil {
		return estimate, "", 0, err
	}
	allowed, err := s.resolveChargingAllowed(ctx)
	if err != nil {
		return estimate, "", 0, fault.New("charging_setting_unavailable", "题库收费开关暂不可用")
	}
	estimate.Estimated = true
	estimate.ChargingAllowed = allowed
	estimate.ChargingCheckedAt = s.now()
	billingState := noReserveBillingState(estimate, s.billing != nil)
	if estimate.State == "quoted" && estimate.AmountMinor > 0 && estimate.ChargingAllowed && s.billing != nil {
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return estimate, "", 0, err
		}
		defer tx.Rollback(context.Background())
		holdID, err := s.billing.Reserve(ctx, tx, userID, s.billingKey(queryID), estimate.AmountMinor)
		if err != nil {
			return estimate, "", 0, err
		}
		snapshot := snapshotFromQuote(estimate, estimate.AmountMinor, holdID, "reserved")
		pricingJSON, _ := json.Marshal(snapshot)
		tag, err := tx.Exec(ctx, `UPDATE public.question_queries SET pricing = $2, billing_hold_id = $3,
			reserved_minor = $4, billing_state = 'reserved', updated_at = now()
			WHERE id = $1 AND status = 'pending'`, queryID, pricingJSON, holdID, estimate.AmountMinor)
		if err != nil || tag.RowsAffected() == 0 {
			return estimate, "", 0, errors.New("无法保存题库预占事实")
		}
		if err := tx.Commit(ctx); err != nil {
			return estimate, "", 0, err
		}
		return estimate, holdID, estimate.AmountMinor, nil
	}
	snapshot := snapshotFromQuote(estimate, 0, "", billingState)
	pricingJSON, _ := json.Marshal(snapshot)
	if _, err := s.pool.Exec(ctx, `UPDATE public.question_queries SET pricing = $2, billing_state = $3,
		updated_at = now() WHERE id = $1 AND status = 'pending'`, queryID, pricingJSON, billingState); err != nil {
		return estimate, "", 0, err
	}
	return estimate, "", 0, nil
}

// finalizeQuery 在同一个数据库事务中完成幂等结算/释放、查询事实和用户事件。
func (s *Service) finalizeQuery(ctx context.Context, queryID, userID string, in finalizeInput) (QueryView, error) {
	usageJSON, err := json.Marshal(in.Usage)
	if err != nil {
		return QueryView{}, err
	}
	snapshot := snapshotFromQuote(in.Quote, in.ReservedMinor, in.HoldID, in.BillingState)
	if in.Estimate.Estimated {
		snapshot.EstimatedInputTokens = in.Estimate.InputTokens
		snapshot.EstimatedOutputTokens = in.Estimate.OutputTokens
		snapshot.EstimatedCostMicros = in.Estimate.CostMicros
		snapshot.EstimatedAmountMinor = in.Estimate.AmountMinor
	}
	if in.HoldID == "" {
		snapshot.BillingState = noReserveBillingState(in.Quote, s.billing != nil)
	}
	if in.Status == "failed" || (in.Status == "needs_review" && in.ErrorCode != "usage_incomplete") {
		// 未完成或待复核的结果不展示为已报价，避免把 0 费用误读成模型免费。
		snapshot.State = "unpriced"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return QueryView{}, err
	}
	defer tx.Rollback(context.Background())
	key := s.billingKey(queryID)
	if in.HoldID != "" {
		if in.Settle && in.Quote.ChargingAllowed && in.Quote.State == "quoted" && in.Quote.AmountMinor > 0 {
			if err := s.billing.Settle(ctx, tx, in.HoldID, key, in.Quote.AmountMinor); err != nil {
				_ = tx.Rollback(context.Background())
				s.releaseAfterLedgerFailure(queryID, in.HoldID)
				s.markLedgerFailure(queryID, userID, in, "billing_settlement_failed")
				return QueryView{}, err
			}
			if in.Quote.AmountMinor < in.ReservedMinor {
				if err := s.billing.Release(ctx, tx, in.HoldID, key); err != nil {
					_ = tx.Rollback(context.Background())
					s.releaseAfterLedgerFailure(queryID, in.HoldID)
					s.markLedgerFailure(queryID, userID, in, "billing_release_failed")
					return QueryView{}, err
				}
			}
			snapshot.Charged = true
			snapshot.BillingState = "settled"
		} else {
			if err := s.billing.Release(ctx, tx, in.HoldID, key); err != nil {
				_ = tx.Rollback(context.Background())
				s.markLedgerFailure(queryID, userID, in, "billing_release_failed")
				return QueryView{}, err
			}
			snapshot.BillingState = in.BillingState
			if snapshot.BillingState == "" {
				snapshot.BillingState = "released"
			}
		}
	}
	snapshot.HoldID = in.HoldID
	pricingJSON, _ := json.Marshal(snapshot)
	var queryVersion int64
	err = tx.QueryRow(ctx, `UPDATE public.question_queries SET status = $2, answer = NULLIF($3,''),
		cache_hit = $4, cache_entry_id = NULLIF($5,''), cache_created_at = $6, usage = $7, pricing = $8,
		billing_state = $9, billing_hold_id = NULLIF($10,''), reserved_minor = $11,
		error_code = NULLIF($12,''), completed_at = now(), updated_at = now(),
		version = version + 1
		WHERE id = $1 AND status = 'pending' RETURNING version`,
		queryID, in.Status, in.Response.Answer, in.Response.Cache.Hit, valueOrEmpty(in.Response.Cache.EntryID),
		in.Response.Cache.CreatedAt, usageJSON, pricingJSON, snapshot.BillingState, in.HoldID,
		in.ReservedMinor, in.ErrorCode).Scan(&queryVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		tx.Rollback(context.Background())
		q, err := s.loadQueryAdmin(context.Background(), queryID)
		if err != nil || q == nil {
			return QueryView{}, errors.New("查询状态已变化且无法读取")
		}
		return buildQueryView(q), nil
	}
	if err != nil {
		return QueryView{}, err
	}
	// 事件只含状态与计费摘要，不含原题、答案、图片或上游信息。
	if err := recordQuestionEvent(ctx, tx, userID, queryID, queryVersion, in.Status, in.Response.Cache.Hit,
		snapshot.State, snapshot.BillingState); err != nil {
		return QueryView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return QueryView{}, err
	}
	q, err := s.loadQueryAdmin(ctx, queryID)
	if err != nil || q == nil {
		return QueryView{}, errors.New("无法读取已保存的查询")
	}
	return buildQueryView(q), nil
}

func (s *Service) releaseAfterLedgerFailure(queryID, holdID string) {
	if holdID == "" || s.billing == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(context.Background())
	if err := s.billing.Release(ctx, tx, holdID, s.billingKey(queryID)); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

func (s *Service) markLedgerFailure(queryID, userID string, in finalizeInput, code string) {
	snapshot := snapshotFromQuote(in.Quote, in.ReservedMinor, in.HoldID, code)
	snapshot.Charged = false
	pricingJSON, _ := json.Marshal(snapshot)
	usageJSON, _ := json.Marshal(in.Usage)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer tx.Rollback(context.Background())
	var version int64
	err = tx.QueryRow(ctx, `UPDATE public.question_queries SET status = 'needs_review', usage = $2,
		pricing = $3, billing_state = $4, error_code = $5, completed_at = now(), updated_at = now(),
		version = version + 1 WHERE id = $1 AND status = 'pending' RETURNING version`,
		queryID, usageJSON, pricingJSON, code, code).Scan(&version)
	if err != nil {
		return
	}
	_ = recordQuestionEvent(ctx, tx, userID, queryID, version, "needs_review", false, "unpriced", code)
	_ = tx.Commit(ctx)
}

func (s *Service) markReserveFailed(ctx context.Context, queryID, code string) {
	billingState := "reserve_failed"
	if code == "charging_setting_unavailable" {
		billingState = "charging_setting_unavailable"
	}
	_, _ = s.pool.Exec(context.Background(), `UPDATE public.question_queries SET status = 'failed',
		error_code = $2, billing_state = $3, completed_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'`, queryID, code, billingState)
}

// recordQuestionEvent 直接写共享 outbox 结构，resource_id 使用 qq_ 公开编号。
// 不通过其他业务包，避免把题目原文或答案写入事件；由协调者统一投递。
func recordQuestionEvent(ctx context.Context, tx pgx.Tx, userID, queryID string, version int64,
	status string, cacheHit bool, pricingState, billingState string) error {
	payload, err := json.Marshal(map[string]any{
		"query_id": queryID, "status": status, "cache_hit": cacheHit,
		"pricing_state": pricingState, "billing_state": billingState,
	})
	if err != nil || len(payload) > 16384 {
		return errors.New("题库事件载荷无效")
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.outbox_events
		(id, audience, user_id, event_type, resource_id, resource_version, payload)
		VALUES ($1, 'user', $2, 'question.query.updated', $3, $4, $5)`,
		web.NewID(), userID, queryID, version, payload)
	if err != nil {
		return errors.New("无法写入题库事件")
	}
	return nil
}

func (s *Service) markQueryFailed(ctx context.Context, queryID, code string) {
	_, _ = s.pool.Exec(context.Background(), `UPDATE public.question_queries SET status = 'failed',
		error_code = $2, completed_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'`, queryID, code)
}

func (s *Service) markQueryNeedsReview(ctx context.Context, queryID, code, billingState string) {
	_, _ = s.pool.Exec(context.Background(), `UPDATE public.question_queries SET status = 'needs_review',
		error_code = $2, billing_state = $3, completed_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'`, queryID, code, billingState)
}

func writeStoredQueryError(w http.ResponseWriter, r *http.Request, code string) {
	switch code {
	case "model_disabled":
		web.Error(w, r, 409, code, "该模型已下架，请重新选择")
	case "vision_unsupported", "budget_exceeded", "invalid_question":
		web.Error(w, r, 422, code, "题库请求无法处理")
	case "upstream_timeout":
		web.Error(w, r, 504, code, "题库服务超时，结果待核对")
	case "upstream_rate_limited":
		web.Error(w, r, 429, code, "题库服务限流，请稍后人工重试")
	case "insufficient_balance":
		web.Error(w, r, 409, code, "余额不足，未发起模型调用")
	case "request_unknown":
		web.Error(w, r, 409, code, "题库上游结果未知，已保留待复核事实")
	default:
		web.Error(w, r, 503, "service_unavailable", "题库查询失败")
	}
}

func writeForkQueryError(w http.ResponseWriter, r *http.Request, err error) {
	var forkErr *ForkError
	if errors.As(err, &forkErr) {
		switch forkErr.Code {
		case "model_disabled":
			web.Error(w, r, 409, forkErr.Code, "该模型已下架，请重新选择")
			return
		case "vision_unsupported", "budget_exceeded", "invalid_question", "invalid_mode", "invalid_scope":
			web.Error(w, r, 422, forkErr.Code, forkErr.Message)
			return
		case "upstream_timeout":
			web.Error(w, r, 504, forkErr.Code, "题库服务超时，结果未知；不会自动重试")
			return
		case "upstream_rate_limited":
			web.Error(w, r, 429, forkErr.Code, "题库服务限流，请稍后人工重试")
			return
		case "request_unknown":
			web.Error(w, r, 409, forkErr.Code, "题库上游结果未知，已保留待复核事实")
			return
		}
	}
	web.Error(w, r, 503, "service_unavailable", "题库服务不可用")
}

func (s *Service) getUserQuery(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	q, err := s.loadQueryByID(r.Context(), actor.ID, r.PathValue("id"))
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	if q == nil {
		web.Error(w, r, 404, "not_found", "题库查询不存在")
		return
	}
	web.JSON(w, r, http.StatusOK, buildQueryView(q))
}

func (s *Service) listUserQueries(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	var after time.Time
	var afterID string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		after, afterID, err = decodeCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	} else {
		after = s.now().Add(24 * time.Hour)
	}
	status := r.URL.Query().Get("status")
	if status != "" {
		switch status {
		case "pending", "answered", "cache_hit", "needs_review", "failed":
		default:
			web.Error(w, r, 422, "validation_failed", "状态筛选无效")
			return
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT `+queryColumns+` FROM public.question_queries
		WHERE user_id = $1 AND (created_at, id) < ($2, $3) AND ($4 = '' OR status = $4)
		ORDER BY created_at DESC, id DESC LIMIT $5`, actor.ID, after, afterID, status, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	defer rows.Close()
	items := make([]QueryView, 0, limit)
	var next any
	for rows.Next() {
		q, err := scanQuery(rows)
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "题库查询格式无效")
			return
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		items = append(items, buildQueryView(q))
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Service) adminListQueries(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "question:read"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	var after time.Time
	var afterID string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		after, afterID, err = decodeCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	} else {
		after = s.now().Add(24 * time.Hour)
	}
	status := r.URL.Query().Get("status")
	if status != "" {
		switch status {
		case "pending", "answered", "cache_hit", "needs_review", "failed":
		default:
			web.Error(w, r, 422, "validation_failed", "状态筛选无效")
			return
		}
	}
	offering := r.URL.Query().Get("offering_id")
	rows, err := s.pool.Query(r.Context(), `SELECT `+queryColumns+` FROM public.question_queries
		WHERE (created_at, id) < ($1, $2) AND ($3 = '' OR status = $3) AND ($4 = '' OR offering_public_id = $4)
		ORDER BY created_at DESC, id DESC LIMIT $5`, after, afterID, status, offering, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	defer rows.Close()
	type adminQueryItem struct {
		ID              string      `json:"id"`
		UserRef         string      `json:"user_ref"`
		Model           ModelRef    `json:"model"`
		Mode            string      `json:"mode"`
		Status          string      `json:"status"`
		Cache           CacheView   `json:"cache"`
		Usage           Usage       `json:"usage"`
		Pricing         pricingView `json:"pricing"`
		QuestionPreview string      `json:"question_preview"`
		ContentExpired  bool        `json:"content_expired"`
		CreatedAt       time.Time   `json:"created_at"`
		CompletedAt     *time.Time  `json:"completed_at,omitempty"`
	}
	items := make([]adminQueryItem, 0, limit)
	var next any
	for rows.Next() {
		q, err := scanQuery(rows)
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "题库查询格式无效")
			return
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		view := buildQueryView(q)
		items = append(items, adminQueryItem{ID: q.ID, UserRef: "u_" + hashHex(q.UserID)[:12], Model: view.Model,
			Mode: q.Mode, Status: q.Status, Cache: view.Cache, Usage: view.Usage, Pricing: view.Pricing,
			QuestionPreview: truncateRunes(q.QuestionTitle, 80), ContentExpired: view.ContentExpired,
			CreatedAt: q.CreatedAt, CompletedAt: q.CompletedAt})
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

type cacheReportInput struct {
	QueryID string `json:"query_id"`
	Reason  string `json:"reason"`
}

func (s *Service) createCacheReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	var input cacheReportInput
	if !web.Decode(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.Reason == "" || len(input.Reason) > 1000 {
		web.Error(w, r, 422, "validation_failed", "报告原因不能为空或超过 1000 字")
		return
	}
	q, err := s.loadQueryByID(r.Context(), actor.ID, input.QueryID)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	if q == nil {
		web.Error(w, r, 404, "not_found", "题库查询不存在")
		return
	}
	id := web.NewID()
	_, err = s.pool.Exec(r.Context(), `INSERT INTO public.question_cache_reports (id, query_id, user_id, reason)
		VALUES ($1,$2,$3,$4)`, id, q.ID, actor.ID, input.Reason)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法保存缓存报告")
		return
	}
	web.JSON(w, r, http.StatusCreated, map[string]any{"id": id, "status": "pending", "created_at": s.now()})
}

type submissionInput struct {
	QueryID      string `json:"query_id"`
	AnswerSHA256 string `json:"answer_sha256"`
	Confirmed    bool   `json:"confirmed"`
}

func (s *Service) createSubmission(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	var input submissionInput
	if !web.Decode(w, r, &input) {
		return
	}
	if !input.Confirmed {
		web.Error(w, r, 428, "confirmation_required", "提交前必须人工确认题目与答案")
		return
	}
	if len(input.AnswerSHA256) != 64 {
		web.Error(w, r, 422, "validation_failed", "答案摘要无效")
		return
	}
	q, err := s.loadQueryByID(r.Context(), actor.ID, input.QueryID)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
		return
	}
	if q == nil {
		web.Error(w, r, 404, "not_found", "题库查询不存在")
		return
	}
	if q.Status != "answered" && q.Status != "cache_hit" {
		web.Error(w, r, 409, "conflict", "当前查询结果不可提交")
		return
	}
	if q.ContentExpired || q.QuestionTitle == "" || q.Answer == "" {
		web.Error(w, r, 409, "content_expired", "原题或答案已过保留期，不能提交；请重新查询并人工确认")
		return
	}
	if hashHex(q.Answer) != input.AnswerSHA256 {
		web.Error(w, r, 409, "conflict", "答案已变化，请重新确认")
		return
	}
	reason := "platform_submission_disabled"
	if s.submit {
		reason = "platform_submission_adapter_not_configured"
	}
	_, _ = s.pool.Exec(r.Context(), `INSERT INTO public.question_submissions
		(id, query_id, user_id, answer_sha256, status, reason) VALUES ($1,$2,$3,$4,'blocked',$5)`,
		web.NewID(), q.ID, actor.ID, input.AnswerSHA256, reason)
	web.Error(w, r, 503, "platform_submission_unavailable", "平台提交能力尚未配置，未执行任何提交")
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

func hashHex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
