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
	offering_public_id, offering_display_name, COALESCE(offering_provider_ref, ''), mode, cache_scope,
	scope_key_hash, COALESCE(question_title, ''),
	COALESCE(question_options, ''), question_type, image_count, status, COALESCE(answer, ''), cache_hit,
	COALESCE(cache_entry_id, ''), cache_created_at, usage, pricing, billing_state,
	COALESCE(billing_hold_id, ''), reserved_minor, content_expired,
	COALESCE(error_code, ''), fork_request_id, version, created_at, updated_at, completed_at, fork_cache_policy`

const (
	// cleanupTimeout 限制回滚、补偿释放等后台清理；请求取消后也必须有界完成。
	cleanupTimeout = 5 * time.Second
	// finalizeTimeout 限制结果事实与账本提交，不继承已取消的请求上下文。
	finalizeTimeout = 10 * time.Second
)

// rollbackTx 以有界上下文回滚事务；连接异常时不能让处理协程无限等待。
func rollbackTx(tx pgx.Tx) {
	if tx == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

// finalizeContext 让结算/释放与结果事实脱离已取消的调用方上下文。
// 上游结果和预占补偿必须提交，不能因为客户端断开而丢失。
func finalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finalizeTimeout)
}

type queryRecord struct {
	ID                  string
	UserID              string
	IdempotencyKey      string
	Fingerprint         string
	OfferingID          string
	OfferingPublicID    string
	OfferingDisplayName string
	OfferingProviderRef string
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
	ForkCachePolicy     *ForkCachePolicy
}

func scanQuery(row pgx.Row) (*queryRecord, error) {
	var q queryRecord
	var cachePolicy []byte
	err := row.Scan(&q.ID, &q.UserID, &q.IdempotencyKey, &q.Fingerprint, &q.OfferingID,
		&q.OfferingPublicID, &q.OfferingDisplayName, &q.OfferingProviderRef, &q.Mode, &q.CacheScope,
		&q.ScopeKeyHash, &q.QuestionTitle,
		&q.QuestionOptions, &q.QuestionType, &q.ImageCount, &q.Status, &q.Answer, &q.CacheHit,
		&q.CacheEntryID, &q.CacheCreatedAt, &q.Usage, &q.Pricing, &q.BillingState,
		&q.BillingHoldID, &q.ReservedMinor, &q.ContentExpired,
		&q.ErrorCode, &q.ForkRequestID, &q.Version, &q.CreatedAt, &q.UpdatedAt, &q.CompletedAt, &cachePolicy)
	if err != nil {
		return nil, err
	}
	if len(cachePolicy) > 0 {
		if err := json.Unmarshal(cachePolicy, &q.ForkCachePolicy); err != nil {
			return nil, errors.New("题库缓存策略快照无效")
		}
	}
	return &q, nil
}

type pricingSnapshot struct {
	State                 string           `json:"state"`
	Pricing               *OfferingPricing `json:"pricing,omitempty"`
	PriceVersion          string           `json:"price_version,omitempty"`
	Rule                  string           `json:"rule,omitempty"`
	Rounding              string           `json:"rounding,omitempty"`
	EstimatedInputTokens  int64            `json:"estimated_input_tokens,omitempty"`
	EstimatedOutputTokens int64            `json:"estimated_output_tokens,omitempty"`
	EstimatedCostMicros   int64            `json:"estimated_cost_micros,omitempty"`
	EstimatedAmountMinor  int64            `json:"estimated_amount_minor,omitempty"`
	InputTokens           int64            `json:"input_tokens,omitempty"`
	OutputTokens          int64            `json:"output_tokens,omitempty"`
	CostMicros            int64            `json:"cost_micros,omitempty"`
	AmountMinor           int64            `json:"amount_minor,omitempty"`
	ReservedMinor         int64            `json:"reserved_minor,omitempty"`
	Charged               bool             `json:"charged"`
	BillingState          string           `json:"billing_state"`
	HoldID                string           `json:"hold_id,omitempty"`
	// BillingKey 是本次预占使用的账本业务键；旧快照为空时回退到基础业务键。
	BillingKey string `json:"billing_key,omitempty"`
	// Frozen 是新一代预占前冻结的原始收费事实；金额、价格与收费决定不再重算。
	Frozen                   *pricingSnapshot `json:"frozen,omitempty"`
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

func snapshotFromQuote(quote Quote, reserved int64, holdID, billingState, billingKey string) pricingSnapshot {
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
		BillingKey:               billingKey,
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
	// 主库条目是题库目录的镜像；写入查询前先同步本次使用的条目。
	if err := s.syncOffering(ctx, offering); err != nil {
		return false, nil, err
	}
	cachePolicy, _ := json.Marshal(forkCachePolicyFor(offering))
	tag, err := s.pool.Exec(ctx, `INSERT INTO public.question_queries
		(id, user_id, idempotency_key, request_fingerprint, offering_id, offering_public_id, offering_display_name,
		 offering_provider_ref, mode, cache_scope, scope_key_hash, question_title, question_options, question_type,
		 image_count, status, usage, pricing, billing_state, fork_request_id, fork_cache_policy)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,'pending','{}','{}','pending',$1,$16)
		ON CONFLICT (user_id, idempotency_key) DO NOTHING`,
		queryID, actorID, idem, fp, offering.ID, offering.PublicID, offering.DisplayName,
		offering.ProviderRef, input.Mode, input.CacheScope, scopeHash, input.Question.Title,
		input.Question.Options, input.Question.Type, images.Count, string(cachePolicy))
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
		if s.now().Sub(q.UpdatedAt) > s.stalePendingAge() {
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

// Answer 执行一次完整的题库作答业务链，供 HTTP 入口和后台自动做题直接调用。
//
// userID 必须是平台内部用户 UUID。idempotencyKey 必须在同一用户内稳定且唯一；
// 后台恢复同一任务、同一试卷、同一题时应复用同一键。重复调用返回已有结果、
// 短暂 request_in_progress，或对陈旧 pending 做一次恢复；恢复复用同一 fork
// request_id，不产生第二次模型调用、预占或扣费。
//
// 返回的错误均为 *QueryError，可按 Code 分类；Message 不含 fork 原始响应或密钥。
func (s *Service) Answer(ctx context.Context, userID, idempotencyKey string, input QueryInput) (QueryView, error) {
	if !web.ValidID(userID) {
		return QueryView{}, invalidUserError()
	}
	key := strings.TrimSpace(idempotencyKey)
	if key == "" || len(key) > 200 {
		return QueryView{}, idempotencyKeyError()
	}
	images, err := s.validateQuery(&input)
	if err != nil {
		return QueryView{}, queryErrorFromFault(err, "题库请求无法处理")
	}
	fp := fingerprint(userID, input)
	// 幂等重放先于模型可用性检查：模型下架后，已完成的同一请求仍返回原结果。
	existing, err := s.loadQueryByUserKey(ctx, userID, key)
	if err != nil {
		return QueryView{}, queryErrorFromFault(err, "无法读取幂等记录")
	}
	if existing != nil {
		return s.replayQuery(ctx, userID, key, fp, input, images.Count, existing)
	}
	offering, err := s.resolveOffering(ctx, input.ModelOfferingID)
	if err != nil {
		return QueryView{}, queryErrorFromFault(err, "无法读取模型条目")
	}
	if err := checkCachePolicy(offering, input); err != nil {
		return QueryView{}, err
	}
	scopeKey := s.scopeKeyFor(userID)
	scopeHash := hashHex(scopeKey)
	queryID := newPrefixedID("qq_")
	forkRequest := forkRequestFor(queryID, offering, input, scopeKey)
	created, existing, err := s.insertPendingQuery(ctx, queryID, userID, key, fp, offering, input, scopeHash, images)
	if err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法保存题库查询", http.StatusServiceUnavailable, true)
	}
	if !created {
		return s.replayQuery(ctx, userID, key, fp, input, images.Count, existing)
	}
	return s.dispatchQuery(ctx, queryID, userID, offering, forkRequest, input, images.Count, 1, nil)
}

// checkCachePolicy 复用创建与前置失败重驱动的缓存策略校验。
func checkCachePolicy(offering *Offering, input QueryInput) error {
	if input.Mode == "cache_first" && !offering.CachePolicy.Allow {
		return newQueryError("cache_not_allowed", "该模型未启用答案缓存，请选择每次都使用模型", http.StatusUnprocessableEntity, false)
	}
	if input.CacheScope == "shared" && offering.CachePolicy.Scope != "shared" {
		return newQueryError("shared_cache_not_allowed", "该模型未允许共享缓存", http.StatusUnprocessableEntity, false)
	}
	return nil
}

// forkRequestFor 按冻结输入构造一次 fork 请求；恢复与重驱动沿用同一查询编号。
func forkRequestFor(queryID string, offering *Offering, input QueryInput, scopeKey string) ForkAnswerRequest {
	return ForkAnswerRequest{
		RequestID: queryID, ModelOfferingID: offering.ProviderRef, Mode: input.Mode,
		CacheScope: input.CacheScope, CacheScopeKey: scopeKey,
		Question:    ForkQuestion{Title: input.Question.Title, Options: input.Question.Options, Type: input.Question.Type, Images: input.Question.Images},
		Budget:      ForkBudget{MaxOutputTokens: input.Budget.MaxOutputTokens},
		CachePolicy: forkCachePolicyFor(offering),
	}
}

func forkCachePolicyFor(offering *Offering) *ForkCachePolicy {
	return &ForkCachePolicy{Allow: offering.CachePolicy.Allow, TTLDays: offering.CachePolicy.TTLDays}
}

// dispatchQuery 在已存在或被重驱动的查询行上完成预占与 fork 调用。
// generation 决定账本业务键：首次尝试用基础键，新一代重驱动用后缀键，
// 避免复用已经关闭的预占；frozen 不为空时沿用原冻结报价开新预占，不重算。
func (s *Service) dispatchQuery(ctx context.Context, queryID, userID string, offering *Offering,
	request ForkAnswerRequest, input QueryInput, imageCount int, generation int64, frozen *frozenDecision) (QueryView, error) {
	if frozen != nil {
		copy := *offering
		copy.Pricing = frozen.quote.Pricing
		offering = &copy
	}
	billingKey := s.nextBillingKey(queryID, generation)
	var estimate Quote
	var holdID string
	var reservedMinor int64
	var err error
	switch {
	case frozen != nil && frozen.reserve:
		estimate, holdID, reservedMinor, err = s.reserveFrozenBilling(ctx, queryID, userID, frozen.quote, billingKey)
	case frozen != nil:
		// 原尝试没有形成预占（收费关闭/未定价/免费）：保留原不收费决定，不再新开预占。
		estimate, holdID, reservedMinor, err = s.persistFrozenBilling(ctx, queryID, frozen.quote, billingKey)
	default:
		estimate, holdID, reservedMinor, err = s.prepareBilling(ctx, queryID, userID, offering, input.Budget.MaxOutputTokens, imageCount, billingKey)
	}
	if err != nil {
		code, _ := fault.Describe(err)
		if markErr := s.markReserveFailed(queryID, code); markErr != nil {
			return QueryView{}, newQueryError("service_unavailable", "无法保存题库查询失败状态，请人工核对", http.StatusServiceUnavailable, true)
		}
		return QueryView{}, reserveQueryError(code)
	}
	return s.completeAnswer(ctx, answerCompletion{
		queryID: queryID, userID: userID, offering: offering, request: request,
		estimate: estimate, holdID: holdID, reservedMinor: reservedMinor,
		billingConnected: s.billing != nil, billingKey: billingKey,
	})
}

// answerCompletion 是一次已创建查询的冻结输入；恢复路径复用同一结构和同一完成链。
type answerCompletion struct {
	queryID            string
	userID             string
	offering           *Offering
	request            ForkAnswerRequest
	estimate           Quote
	holdID             string
	reservedMinor      int64
	billingConnected   bool
	billingKey         string
	frozenBillingState string
	recovered          bool
}

// recoveryUncertain 表示恢复时无法确定 fork 是否已产生结果或费用，必须停止自动收费。
func recoveryUncertain(code string) bool {
	switch code {
	case "upstream_timeout", "request_cancelled", "request_unknown", "request_in_progress",
		"idempotency_conflict", "service_unavailable", "model_unavailable", "internal_error":
		return true
	}
	return false
}

// completeAnswer 在已冻结的查询行上调用 fork 并完成结算/释放。
func (s *Service) completeAnswer(ctx context.Context, c answerCompletion) (QueryView, error) {
	queryID, userID := c.queryID, c.userID
	offering, estimate := c.offering, c.estimate
	holdID, reservedMinor := c.holdID, c.reservedMinor
	billingConnected := c.billingConnected
	if !c.recovered {
		billingConnected = s.billing != nil
	}
	releaseState := func(defaultState string) string {
		if holdID == "" && c.recovered && c.frozenBillingState != "" {
			return c.frozenBillingState
		}
		return defaultState
	}
	if holdID != "" && s.billing == nil {
		// 没有账本连接时不能安全结算或释放既有预占，保留事实转待复核。
		s.markFinalizeFailed(queryID)
		return QueryView{}, newQueryError("service_unavailable", "题库账本未连接，预占待人工核对", http.StatusServiceUnavailable, true)
	}
	if err := s.requireFork(); err != nil {
		if _, finalizeErr := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: "failed", ErrorCode: "not_configured", Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: releaseState("released"), PreserveBillingState: c.recovered,
		}); finalizeErr != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		return QueryView{}, queryErrorFromFault(err, "题库服务尚未配置")
	}
	forkCtx, cancel := context.WithTimeout(ctx, s.timeout)
	response, forkErr := s.fork.Answer(forkCtx, c.request, queryID)
	cancel()
	if forkErr != nil {
		code := "service_unavailable"
		var value *ForkError
		if errors.As(forkErr, &value) {
			code = safeForkCode(value.Code)
		}
		status := "failed"
		// 超时或取消时上游可能已产生费用；保留查询事实并进入待复核，不自动重放。
		if code == "upstream_timeout" || code == "request_cancelled" || code == "request_unknown" {
			status = "needs_review"
		}
		if c.recovered && recoveryUncertain(code) {
			status = "needs_review"
		}
		view, finalizeErr := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: status, ErrorCode: code, Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: releaseState("released"), PreserveBillingState: c.recovered,
		})
		if finalizeErr != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		if c.recovered && status == "needs_review" {
			return view, nil
		}
		return QueryView{}, forkQueryError(forkErr)
	}
	if err := normalizeForkResponse(&response, queryID, offering.ProviderRef, c.request.Mode, s.maxImg+1); err != nil {
		view, finalizeErr := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: "needs_review", ErrorCode: "invalid_fork_response", Estimate: estimate,
			Quote: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: releaseState("released"), PreserveBillingState: c.recovered,
		})
		if finalizeErr != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		if c.recovered {
			return view, nil
		}
		return QueryView{}, newQueryError("invalid_fork_response", "题库响应事实不完整，查询已进入待复核", http.StatusBadGateway, false)
	}
	if response.Status == "cache_hit" {
		quote := Quote{State: "cache_hit", Pricing: offering.Pricing,
			ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt}
		view, err := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: "cache_hit", Response: response, Usage: response.Usage, Quote: quote,
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: "no_charge", PreserveBillingState: c.recovered,
		})
		if err != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		return view, nil
	}
	if response.Status == "usage_incomplete" || !response.Usage.Complete {
		view, err := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: "needs_review", ErrorCode: "usage_incomplete", Response: response,
			Usage: response.Usage, Quote: Quote{State: "usage_incomplete", Pricing: offering.Pricing,
				ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt},
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: releaseState("released"), PreserveBillingState: c.recovered,
		})
		if err != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		return view, nil
	}
	actual, err := s.actualQuote(offering, response.Usage)
	if err == nil {
		actual.ChargingAllowed = estimate.ChargingAllowed
		actual.ChargingCheckedAt = estimate.ChargingCheckedAt
	}
	if err != nil {
		view, finalizeErr := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
			Status: "needs_review", ErrorCode: "pricing_error", Response: response,
			Usage: response.Usage, Quote: Quote{State: "unpriced", Pricing: offering.Pricing,
				ChargingAllowed: estimate.ChargingAllowed, ChargingCheckedAt: estimate.ChargingCheckedAt},
			Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor, BillingKey: c.billingKey,
			BillingState: releaseState("released"), PreserveBillingState: c.recovered,
		})
		if finalizeErr != nil {
			return QueryView{}, s.finalizeFailure(queryID)
		}
		if c.recovered {
			return view, nil
		}
		return QueryView{}, newQueryError("pricing_error", "价格计算失败，查询已进入待复核", http.StatusBadGateway, false)
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
	case !billingConnected:
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
	view, err := s.finalizeQuery(ctx, queryID, userID, finalizeInput{
		Status: status, ErrorCode: errorCode, Response: response, Usage: response.Usage,
		Quote: finalQuote, Estimate: estimate, HoldID: holdID, ReservedMinor: reservedMinor,
		BillingKey: c.billingKey, Settle: settle, BillingState: releaseState("released"), PreserveBillingState: c.recovered,
	})
	if err != nil {
		return QueryView{}, s.finalizeFailure(queryID)
	}
	return view, nil
}

const minStalePendingAge = 2 * time.Minute

// stalePendingAge 至少为两分钟；fork 超时更长时顺延，避免恢复在途请求。
func (s *Service) stalePendingAge() time.Duration {
	threshold := minStalePendingAge
	if candidate := s.timeout + 30*time.Second; candidate > threshold {
		threshold = candidate
	}
	return threshold
}

// replayQuery 处理同用户同幂等键的重放；只要 pending 过期就尝试恢复。
func (s *Service) replayQuery(ctx context.Context, userID, key, fp string, input QueryInput, imageCount int, existing *queryRecord) (QueryView, error) {
	if existing.Fingerprint != fp {
		return QueryView{}, idempotencyConflictError()
	}
	if existing.Status == "pending" {
		if s.pendingStale(existing) {
			return s.recoverPendingQuery(ctx, userID, key, fp, input, existing)
		}
		q, err := s.waitForQuery(ctx, userID, key)
		if err != nil {
			var safe *fault.Error
			if errors.As(err, &safe) && safe.Code == "conflict" {
				return QueryView{}, requestInProgressError()
			}
			return QueryView{}, queryErrorFromFault(err, "无法读取题库查询")
		}
		existing = q
		if existing.Status == "pending" {
			if s.pendingStale(existing) {
				return s.recoverPendingQuery(ctx, userID, key, fp, input, existing)
			}
			return QueryView{}, requestInProgressError()
		}
	}
	if preCallFailure(existing) {
		return s.retryPreCallFailure(ctx, userID, fp, input, imageCount, existing)
	}
	return replayStoredQuery(fp, existing)
}

// preCallFailureCodes 是 fork 在写入请求记录、调用模型之前返回的错误码。
// 这些失败没有产生模型调用、token 或上游费用，配置或余额修复后可以用同一幂等键重试。
var preCallFailureCodes = map[string]bool{
	// 主平台未配置 fork 客户端。
	"not_configured": true,
	// fork 入口校验：模型不存在或已停用、服务令牌无效、题目或预算超出当前服务配置。
	"model_not_configured":  true,
	"model_disabled":        true,
	"unauthenticated":       true,
	"forbidden":             true,
	"invalid_question":      true,
	"budget_exceeded":       true,
	"shared_cache_disabled": true,
	"invalid_cache_policy":  true,
	"cache_not_allowed":     true,
}

// preCallFailure 判断一条已保存的失败是否可证明从未调用模型。
//
// 两类事实：预占阶段失败（预占行未形成）和 fork 入口阶段拒绝。已形成预占但已全额释放的
// 前置失败也允许重驱动：原预占保留在账本，新一代预占用新的业务键重新创建，不复用已关闭的预占。
// 超时、取消、未知、有 usage 与通用 503 可能已产生模型费用，一律保留原失败事实。
func preCallFailure(q *queryRecord) bool {
	if q.Status != "failed" {
		return false
	}
	if q.BillingHoldID == "" {
		switch q.BillingState {
		case "reserve_failed", "charging_setting_unavailable":
			return true
		}
		return preCallFailureCodes[q.ErrorCode]
	}
	// 已形成预占：只接受已释放、且冻结快照自洽的前置拒绝。
	if q.BillingState != "released" || q.ReservedMinor <= 0 || !preCallFailureCodes[q.ErrorCode] {
		return false
	}
	snapshot := parsePricingSnapshot(q.Pricing)
	if snapshot.HoldID != q.BillingHoldID || snapshot.ReservedMinor != q.ReservedMinor {
		return false
	}
	decision, ok := frozenDecisionFrom(snapshot)
	return ok && decision.reserve
}

// claimRetryableFailure 用行锁把可证明未调用模型的失败重新置为 pending，
// 保证并发重放只有一个请求重驱动。恢复冻结的模型引用与范围键，
// 以使进程在重驱动中崩溃时仍走同一恢复链。
func (s *Service) claimRetryableFailure(ctx context.Context, queryID, userID string, offering *Offering,
	scopeHash string, input QueryInput) (*queryRecord, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackTx(tx)
	q, err := scanQuery(tx.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries
		WHERE id = $1 AND user_id = $2 FOR UPDATE`, queryID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !preCallFailure(q) {
		return nil, nil
	}
	claimed, err := scanQuery(tx.QueryRow(ctx, `UPDATE public.question_queries SET status = 'pending',
		error_code = NULL, billing_state = 'pending',
		pricing = CASE WHEN pricing ? 'frozen' THEN pricing
		              WHEN pricing = '{}'::jsonb THEN pricing
		              ELSE jsonb_build_object('frozen', pricing) END,
		billing_hold_id = NULL, reserved_minor = 0,
		offering_provider_ref = $2, scope_key_hash = $3, question_title = $4, question_options = $5,
		content_expired = false, completed_at = NULL, updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'failed' RETURNING `+queryColumns,
		queryID, offering.ProviderRef, scopeHash, input.Question.Title, input.Question.Options))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return claimed, nil
}

// retryPreCallFailure 重驱动可证明未调用模型的失败：同一查询编号、同一幂等记录。
// 已形成预占且已释放的前置失败先确认原预占全额释放，再用新一代业务键按冻结报价
// 新建预占；原预占与分录留在账本，不复用已关闭的预占。
func (s *Service) retryPreCallFailure(ctx context.Context, userID, fp string, input QueryInput,
	imageCount int, existing *queryRecord) (QueryView, error) {
	if existing.Fingerprint != fp {
		return QueryView{}, idempotencyConflictError()
	}
	// 先按创建时冻结的公开条目重新解析；条目仍不可用时保留原失败，稍后可再试。
	offering, err := s.offeringByPublicID(ctx, existing.OfferingPublicID, false)
	if err != nil {
		return QueryView{}, queryErrorFromFault(err, "无法读取模型条目")
	}
	if err := checkCachePolicy(offering, input); err != nil {
		return QueryView{}, err
	}
	if existing.BillingHoldID != "" {
		if s.billing == nil {
			return QueryView{}, newQueryError("service_unavailable", "题库账本未连接，预占待人工核对", http.StatusServiceUnavailable, true)
		}
		if err := s.releasePreviousHold(ctx, existing, parsePricingSnapshot(existing.Pricing)); err != nil {
			return QueryView{}, newQueryError("service_unavailable", "原预占尚未确认全部释放，已保留原失败供稍后重试", http.StatusServiceUnavailable, true)
		}
	}
	scopeKey := s.scopeKeyFor(userID)
	scopeHash := hashHex(scopeKey)
	claimed, err := s.claimRetryableFailure(ctx, existing.ID, userID, offering, scopeHash, input)
	if err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法重新驱动题库查询", http.StatusServiceUnavailable, true)
	}
	if claimed == nil {
		// 已被其他重放接管，或失败事实已不再满足前置条件；按最新状态返回。
		q, err := s.loadQueryByUserKey(ctx, userID, existing.IdempotencyKey)
		if err != nil {
			return QueryView{}, queryErrorFromFault(err, "无法读取题库查询")
		}
		if q == nil {
			return QueryView{}, newQueryError("service_unavailable", "查询记录丢失", http.StatusServiceUnavailable, true)
		}
		if q.Status == "pending" {
			return QueryView{}, requestInProgressError()
		}
		return replayStoredQuery(fp, q)
	}
	// 冻结发现来自原收费决定；没有有效冻结事实时回退到当前报价（仅预占阶段失败）。
	var frozen *frozenDecision
	if decision, ok := frozenDecisionFrom(parsePricingSnapshot(claimed.Pricing)); ok {
		frozen = &decision
	}
	request := forkRequestFor(claimed.ID, offering, input, scopeKey)
	request.CachePolicy = claimed.ForkCachePolicy
	return s.dispatchQuery(ctx, claimed.ID, userID, offering, request, input, imageCount, claimed.Version, frozen)
}

// releasePreviousHold 在开新一代预占前确认原预占已全额释放。账本按业务键幂等，
// 重复释放不会产生第二笔分录；旧预占与分录仍可按 source_id 查回。
func (s *Service) releasePreviousHold(ctx context.Context, q *queryRecord, snapshot pricingSnapshot) error {
	if q.BillingHoldID == "" {
		return nil
	}
	if s.billing == nil {
		return errors.New("题库账本未连接")
	}
	releaseCtx, cancel := finalizeContext(ctx)
	defer cancel()
	tx, err := s.pool.Begin(releaseCtx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	if err := s.billing.Release(releaseCtx, tx, q.BillingHoldID, resolvedBillingKey(q.ID, snapshot.BillingKey)); err != nil {
		return err
	}
	return tx.Commit(releaseCtx)
}

// frozenDecision 是重驱动沿用的原收费决定。reserve 为真表示原尝试形成过预占，
// 需要用新一代业务键重新预占；为假表示原决定不收费，继续不收费。
type frozenDecision struct {
	quote   Quote
	reserve bool
}

// frozenDecisionFrom 取回原冻结收费事实；新一代预占沿用原金额与价格，不按当前条目重算。
// 重驱动后旧快照可能嵌套在 frozen 中，两种形态都接受。
func frozenDecisionFrom(snapshot pricingSnapshot) (frozenDecision, bool) {
	source := snapshot
	if snapshot.Frozen != nil {
		source = *snapshot.Frozen
	}
	if source.ChargingCheckedAt.IsZero() {
		return frozenDecision{}, false
	}
	// 有定价或明确未定价才算保存过收费决定；其余空快照回退当前报价。
	if source.Pricing == nil && source.State != "unpriced" {
		return frozenDecision{}, false
	}
	quote := Quote{
		State: source.State, Pricing: source.Pricing,
		InputTokens: source.EstimatedInputTokens, OutputTokens: source.EstimatedOutputTokens,
		CostMicros: source.EstimatedCostMicros, AmountMinor: source.EstimatedAmountMinor,
		Estimated: true, ChargingAllowed: source.AutomaticChargingAllowed,
		ChargingCheckedAt: source.ChargingCheckedAt,
	}
	reserve := source.ReservedMinor > 0 && source.HoldID != "" && source.Pricing != nil &&
		source.AutomaticChargingAllowed && source.EstimatedAmountMinor > 0
	if reserve {
		if source.EstimatedAmountMinor != source.ReservedMinor {
			return frozenDecision{}, false
		}
		quote.State = "quoted"
	}
	return frozenDecision{quote: quote, reserve: reserve}, true
}

func (s *Service) pendingStale(q *queryRecord) bool {
	return q.Status == "pending" && s.now().Sub(q.UpdatedAt) > s.stalePendingAge()
}

func replayStoredQuery(fp string, q *queryRecord) (QueryView, error) {
	if q.Fingerprint != fp {
		return QueryView{}, idempotencyConflictError()
	}
	if q.Status == "failed" {
		return QueryView{}, storedQueryError(q.ErrorCode)
	}
	return buildQueryView(q), nil
}

// claimStaleQuery 用单条 UPDATE 原子抢占陈旧 pending；只有一个恢复者能成功。
func (s *Service) claimStaleQuery(ctx context.Context, queryID, userID string) (*queryRecord, error) {
	q, err := scanQuery(s.pool.QueryRow(ctx, `UPDATE public.question_queries
		SET updated_at = now(), version = version + 1
		WHERE id = $1 AND user_id = $2 AND status = 'pending'
		  AND updated_at < now() - make_interval(secs => $3)
		RETURNING `+queryColumns, queryID, userID, s.stalePendingAge().Seconds()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return q, nil
}

func (s *Service) recoverPendingQuery(ctx context.Context, userID, key, fp string, input QueryInput, existing *queryRecord) (QueryView, error) {
	claimed, err := s.claimStaleQuery(ctx, existing.ID, userID)
	if err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法抢占待恢复的题库查询", http.StatusServiceUnavailable, true)
	}
	if claimed == nil {
		q, err := s.loadQueryByUserKey(ctx, userID, key)
		if err != nil {
			return QueryView{}, queryErrorFromFault(err, "无法读取题库查询")
		}
		if q == nil {
			return QueryView{}, newQueryError("service_unavailable", "查询记录丢失", http.StatusServiceUnavailable, true)
		}
		if q.Status == "pending" {
			return QueryView{}, requestInProgressError()
		}
		return replayStoredQuery(fp, q)
	}
	return s.resumeQuery(ctx, claimed, fp, input)
}

// resumeQuery 使用已冻结的模型引用、价格和收费决策重建同一 fork 请求。
// 题目、选项、图片和预算来自调用方再次提供的同一 input；fingerprint 已先校验。
func (s *Service) resumeQuery(ctx context.Context, q *queryRecord, fp string, input QueryInput) (QueryView, error) {
	if q.Fingerprint != fp {
		return QueryView{}, idempotencyConflictError()
	}
	if q.BillingHoldID != "" && s.billing == nil {
		s.markFinalizeFailed(q.ID)
		return QueryView{}, newQueryError("service_unavailable", "题库账本未连接，预占待人工核对", http.StatusServiceUnavailable, true)
	}
	snapshot := parsePricingSnapshot(q.Pricing)
	estimate, estimateErr := recoveryEstimate(snapshot)
	if estimateErr != nil {
		// 没有冻结的收费决策与预占，说明 prepareBilling 尚未完成，fork 一定没有被调用：
		// 这是崩溃或写库失败遗留的在途行，可按当前配置重新驱动，不必进人工复核。
		// prepareBilling 在返回前总会写入 pricing 快照，因此该前提由代码顺序保证。
		if q.BillingHoldID == "" && snapshot.State == "" && snapshot.ChargingCheckedAt.IsZero() && matchesRecoveryInput(q, input) {
			return s.redriveInterruptedQuery(ctx, q, fp, input)
		}
		return s.finalizeRecoveryReview(ctx, q, Quote{}, "recovery_state_incomplete")
	}
	if !matchesRecoveryInput(q, input) {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_input_mismatch")
	}
	if q.OfferingProviderRef == "" {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_model_reference_missing")
	}
	scopeKey := s.scopeKeyFor(q.UserID)
	if hashHex(scopeKey) != q.ScopeKeyHash {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_cache_scope_changed")
	}
	if (q.BillingHoldID == "") != (q.ReservedMinor == 0) {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_reservation_state_invalid")
	}
	if snapshot.HoldID != "" && snapshot.HoldID != q.BillingHoldID {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_reservation_state_invalid")
	}
	if snapshot.ReservedMinor != q.ReservedMinor {
		return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_reservation_state_invalid")
	}
	billingConnected := snapshot.BillingState != "billing_not_connected"
	if estimate.State == "quoted" && estimate.AmountMinor > 0 && estimate.ChargingAllowed && q.BillingHoldID == "" {
		switch snapshot.BillingState {
		case "billing_not_connected", "automatic_charging_disabled":
			// 冻结决策本就不收费，允许无预占恢复。
		default:
			return s.finalizeRecoveryReview(ctx, q, estimate, "recovery_without_reservation")
		}
	}
	offering := &Offering{ID: q.OfferingID, PublicID: q.OfferingPublicID, DisplayName: q.OfferingDisplayName,
		ProviderRef: q.OfferingProviderRef, Pricing: snapshot.Pricing}
	request := ForkAnswerRequest{
		RequestID: q.ID, ModelOfferingID: q.OfferingProviderRef, Mode: q.Mode,
		CacheScope: q.CacheScope, CacheScopeKey: scopeKey,
		Question:    ForkQuestion{Title: input.Question.Title, Options: input.Question.Options, Type: input.Question.Type, Images: input.Question.Images},
		Budget:      ForkBudget{MaxOutputTokens: input.Budget.MaxOutputTokens},
		CachePolicy: q.ForkCachePolicy,
	}
	return s.completeAnswer(ctx, answerCompletion{
		queryID: q.ID, userID: q.UserID, offering: offering, request: request,
		estimate: estimate, holdID: q.BillingHoldID, reservedMinor: q.ReservedMinor,
		billingConnected: billingConnected, billingKey: snapshot.BillingKey,
		frozenBillingState: snapshot.BillingState, recovered: true,
	})
}

func matchesRecoveryInput(q *queryRecord, input QueryInput) bool {
	return q.Mode == input.Mode && q.CacheScope == input.CacheScope &&
		q.QuestionTitle == input.Question.Title && q.QuestionOptions == input.Question.Options &&
		q.QuestionType == input.Question.Type && q.ImageCount == len(input.Question.Images)
}

func parsePricingSnapshot(raw []byte) pricingSnapshot {
	var snapshot pricingSnapshot
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &snapshot)
	}
	return snapshot
}

func recoveryEstimate(snapshot pricingSnapshot) (Quote, error) {
	if snapshot.ChargingCheckedAt.IsZero() {
		return Quote{}, errors.New("收费决策未冻结")
	}
	switch snapshot.State {
	case "quoted", "unpriced", "free":
	default:
		return Quote{}, errors.New("价格快照状态不可恢复")
	}
	return Quote{
		State: snapshot.State, Pricing: snapshot.Pricing,
		InputTokens: snapshot.EstimatedInputTokens, OutputTokens: snapshot.EstimatedOutputTokens,
		CostMicros: snapshot.EstimatedCostMicros, AmountMinor: snapshot.EstimatedAmountMinor,
		Estimated: true, ChargingAllowed: snapshot.AutomaticChargingAllowed,
		ChargingCheckedAt: snapshot.ChargingCheckedAt,
	}, nil
}

func (s *Service) finalizeRecoveryReview(ctx context.Context, q *queryRecord, estimate Quote, code string) (QueryView, error) {
	if q.BillingHoldID != "" && s.billing == nil {
		s.markFinalizeFailed(q.ID)
		return QueryView{}, newQueryError("service_unavailable", "题库账本未连接，预占待人工核对", http.StatusServiceUnavailable, true)
	}
	billingState := q.BillingState
	if q.BillingHoldID != "" {
		billingState = "released"
	} else {
		switch billingState {
		case "", "pending", "reserved":
			billingState = "released"
		}
	}
	view, err := s.finalizeQuery(ctx, q.ID, q.UserID, finalizeInput{
		Status: "needs_review", ErrorCode: code, Estimate: estimate,
		Quote: estimate, HoldID: q.BillingHoldID, ReservedMinor: q.ReservedMinor,
		BillingKey:           parsePricingSnapshot(q.Pricing).BillingKey,
		BillingState:         billingState,
		PreserveBillingState: true,
	})
	if err != nil {
		return QueryView{}, s.finalizeFailure(q.ID)
	}
	return view, nil
}

// redriveInterruptedQuery 重新驱动尚未进入模型调用的在途行：沿用同一查询编号与
// 同一幂等记录。存在冻结收费事实时用新一代预占沿用原报价，否则按当前配置重新报价。
func (s *Service) redriveInterruptedQuery(ctx context.Context, q *queryRecord, fp string, input QueryInput) (QueryView, error) {
	if q.Fingerprint != fp {
		return QueryView{}, idempotencyConflictError()
	}
	snapshot := parsePricingSnapshot(q.Pricing)
	var frozen *frozenDecision
	if snapshot.Frozen != nil {
		decision, ok := frozenDecisionFrom(snapshot)
		if !ok {
			return s.finalizeRecoveryReview(ctx, q, Quote{}, "recovery_state_incomplete")
		}
		frozen = &decision
	}
	offering, err := s.offeringByPublicID(ctx, q.OfferingPublicID, false)
	if err != nil {
		return s.finalizeRecoveryReview(ctx, q, Quote{}, "recovery_model_unavailable")
	}
	if err := checkCachePolicy(offering, input); err != nil {
		return s.finalizeRecoveryReview(ctx, q, Quote{}, "recovery_policy_changed")
	}
	scopeKey := s.scopeKeyFor(q.UserID)
	if _, err := s.pool.Exec(ctx, `UPDATE public.question_queries SET offering_provider_ref = $2,
		scope_key_hash = $3, question_title = $4, question_options = $5, updated_at = now()
		WHERE id = $1 AND status = 'pending' AND billing_hold_id IS NULL`,
		q.ID, offering.ProviderRef, hashHex(scopeKey), input.Question.Title, input.Question.Options); err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法重新驱动题库查询", http.StatusServiceUnavailable, true)
	}
	request := forkRequestFor(q.ID, offering, input, scopeKey)
	request.CachePolicy = q.ForkCachePolicy
	return s.dispatchQuery(ctx, q.ID, q.UserID, offering, request, input, len(input.Question.Images), q.Version, frozen)
}

// createQuery 只负责身份、请求头和请求体解码，业务链交由 Answer。
func (s *Service) createQuery(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "self")
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 200 {
		writeQueryError(w, r, idempotencyKeyError())
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
	view, err := s.Answer(r.Context(), actor.ID, idempotencyKey, input)
	if err != nil {
		writeQueryError(w, r, err)
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
	// BillingKey 是本次预占使用的账本业务键；空值回退到基础业务键（兼容旧快照）。
	BillingKey string
	// PreserveBillingState 用于恢复已冻结的无预占计费事实；新请求继续按当前账本连接重算。
	PreserveBillingState bool
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

// nextBillingKey 生成第 generation 代预占的业务键。第一代保持既有基础键，
// 后续代使用后缀键：账本保留已释放的旧预占，新预占不会复用已关闭的预占。
// 后缀不影响 app 适配器从键中提取 qq_ 公开编号绑定 hold.source_id。
func (s *Service) nextBillingKey(queryID string, generation int64) string {
	base := s.billingKey(queryID)
	if generation <= 1 {
		return base
	}
	return fmt.Sprintf("%s:g%d", base, generation)
}

// resolvedBillingKey 返回本次结算/释放应使用的业务键；旧快照为空时回退基础键。
func resolvedBillingKey(queryID, stored string) string {
	if stored != "" {
		return stored
	}
	return "question-query:" + queryID
}

// prepareBilling 在任何 fork/LLM 调用前完成价格快照和预算预占。
func (s *Service) prepareBilling(ctx context.Context, queryID, userID string, offering *Offering, outputBudget, imageCount int, billingKey string) (Quote, string, int64, error) {
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
		defer rollbackTx(tx)
		holdID, err := s.billing.Reserve(ctx, tx, userID, billingKey, estimate.AmountMinor)
		if err != nil {
			return estimate, "", 0, err
		}
		snapshot := snapshotFromQuote(estimate, estimate.AmountMinor, holdID, "reserved", billingKey)
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
	snapshot := snapshotFromQuote(estimate, 0, "", billingState, billingKey)
	pricingJSON, _ := json.Marshal(snapshot)
	if _, err := s.pool.Exec(ctx, `UPDATE public.question_queries SET pricing = $2, billing_state = $3,
		updated_at = now() WHERE id = $1 AND status = 'pending'`, queryID, pricingJSON, billingState); err != nil {
		return estimate, "", 0, err
	}
	return estimate, "", 0, nil
}

// reserveFrozenBilling 用重驱动前冻结的报价开新一代预占：金额、价格与收费决定
// 全部沿用原快照，不按当前条目重算，不因价格变化多占额度。
func (s *Service) reserveFrozenBilling(ctx context.Context, queryID, userID string, frozen Quote, billingKey string) (Quote, string, int64, error) {
	if frozen.Pricing == nil || frozen.AmountMinor <= 0 || !frozen.ChargingAllowed {
		return frozen, "", 0, errors.New("冻结收费事实不完整")
	}
	if s.billing == nil {
		return frozen, "", 0, errors.New("题库账本未连接")
	}
	frozen.Estimated = true
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return frozen, "", 0, err
	}
	defer rollbackTx(tx)
	holdID, err := s.billing.Reserve(ctx, tx, userID, billingKey, frozen.AmountMinor)
	if err != nil {
		return frozen, "", 0, err
	}
	snapshot := snapshotFromQuote(frozen, frozen.AmountMinor, holdID, "reserved", billingKey)
	pricingJSON, _ := json.Marshal(snapshot)
	tag, err := tx.Exec(ctx, `UPDATE public.question_queries SET pricing = $2, billing_hold_id = $3,
		reserved_minor = $4, billing_state = 'reserved', updated_at = now()
		WHERE id = $1 AND status = 'pending'`, queryID, pricingJSON, holdID, frozen.AmountMinor)
	if err != nil || tag.RowsAffected() == 0 {
		return frozen, "", 0, errors.New("无法保存题库预占事实")
	}
	if err := tx.Commit(ctx); err != nil {
		return frozen, "", 0, err
	}
	return frozen, holdID, frozen.AmountMinor, nil
}

// persistFrozenBilling 保存原不收费决定（收费关闭、未定价或免费）：不新开预占，
// 即使管理员后来开启收费或重新定价，本次查询仍按原冻结决定处理。
func (s *Service) persistFrozenBilling(ctx context.Context, queryID string, frozen Quote, billingKey string) (Quote, string, int64, error) {
	frozen.Estimated = true
	billingState := noReserveBillingState(frozen, s.billing != nil)
	snapshot := snapshotFromQuote(frozen, 0, "", billingState, billingKey)
	pricingJSON, _ := json.Marshal(snapshot)
	if _, err := s.pool.Exec(ctx, `UPDATE public.question_queries SET pricing = $2, billing_state = $3,
		updated_at = now() WHERE id = $1 AND status = 'pending'`, queryID, pricingJSON, billingState); err != nil {
		return frozen, "", 0, err
	}
	return frozen, "", 0, nil
}

// finalizeQuery 在同一个数据库事务中完成幂等结算/释放、查询事实和用户事件。
func (s *Service) finalizeQuery(ctx context.Context, queryID, userID string, in finalizeInput) (QueryView, error) {
	if in.HoldID != "" && s.billing == nil {
		return QueryView{}, errors.New("题库账本未连接，不能结算或释放既有预占")
	}
	usageJSON, err := json.Marshal(in.Usage)
	if err != nil {
		return QueryView{}, err
	}
	snapshot := snapshotFromQuote(in.Quote, in.ReservedMinor, in.HoldID, in.BillingState, in.BillingKey)
	if in.Estimate.Estimated {
		snapshot.EstimatedInputTokens = in.Estimate.InputTokens
		snapshot.EstimatedOutputTokens = in.Estimate.OutputTokens
		snapshot.EstimatedCostMicros = in.Estimate.CostMicros
		snapshot.EstimatedAmountMinor = in.Estimate.AmountMinor
	}
	if in.HoldID == "" && !in.PreserveBillingState {
		snapshot.BillingState = noReserveBillingState(in.Quote, s.billing != nil)
	}
	if in.Status == "failed" || (in.Status == "needs_review" && in.ErrorCode != "usage_incomplete") {
		// 未完成或待复核的结果不展示为已报价，避免把 0 费用误读成模型免费。
		snapshot.State = "unpriced"
	}
	// 结算/释放与结果事实不能因为调用方取消而丢失；清理路径使用有界后台上下文。
	finalCtx, cancel := finalizeContext(ctx)
	defer cancel()
	tx, err := s.pool.Begin(finalCtx)
	if err != nil {
		return QueryView{}, err
	}
	committed := false
	// 任一未提交路径都必须释放预占；Release 以业务键幂等，提交结果不明时重放也安全。
	// 清理顺序固定为先回滚再补偿，不依赖事务失败的中间状态。
	defer func() {
		rollbackTx(tx)
		if in.HoldID != "" && !committed {
			s.releaseAfterLedgerFailure(queryID, in.HoldID, resolvedBillingKey(queryID, in.BillingKey))
		}
	}()
	key := resolvedBillingKey(queryID, in.BillingKey)
	if in.HoldID != "" {
		if in.Settle && in.Quote.ChargingAllowed && in.Quote.State == "quoted" && in.Quote.AmountMinor > 0 {
			if err := s.billing.Settle(finalCtx, tx, in.HoldID, key, in.Quote.AmountMinor); err != nil {
				rollbackTx(tx)
				s.markLedgerFailure(queryID, userID, in, "billing_settlement_failed")
				return QueryView{}, err
			}
			if in.Quote.AmountMinor < in.ReservedMinor {
				if err := s.billing.Release(finalCtx, tx, in.HoldID, key); err != nil {
					rollbackTx(tx)
					s.markLedgerFailure(queryID, userID, in, "billing_release_failed")
					return QueryView{}, err
				}
			}
			snapshot.Charged = true
			snapshot.BillingState = "settled"
		} else {
			if err := s.billing.Release(finalCtx, tx, in.HoldID, key); err != nil {
				rollbackTx(tx)
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
	err = tx.QueryRow(finalCtx, `UPDATE public.question_queries SET status = $2, answer = NULLIF($3,''),
		cache_hit = $4, cache_entry_id = NULLIF($5,''), cache_created_at = $6, usage = $7, pricing = $8,
		billing_state = $9, billing_hold_id = NULLIF($10,''), reserved_minor = $11,
		error_code = NULLIF($12,''), offering_provider_ref = NULL, completed_at = now(), updated_at = now(),
		version = version + 1
		WHERE id = $1 AND status = 'pending' RETURNING version`,
		queryID, in.Status, in.Response.Answer, in.Response.Cache.Hit, valueOrEmpty(in.Response.Cache.EntryID),
		in.Response.Cache.CreatedAt, usageJSON, pricingJSON, snapshot.BillingState, in.HoldID,
		in.ReservedMinor, in.ErrorCode).Scan(&queryVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		rollbackTx(tx)
		q, err := s.loadQueryAdmin(finalCtx, queryID)
		if err != nil || q == nil {
			return QueryView{}, errors.New("查询状态已变化且无法读取")
		}
		return buildQueryView(q), nil
	}
	if err != nil {
		return QueryView{}, err
	}
	// 事件只含状态与计费摘要，不含原题、答案、图片或上游信息。
	if err := recordQuestionEvent(finalCtx, tx, userID, queryID, queryVersion, in.Status, in.Response.Cache.Hit,
		snapshot.State, snapshot.BillingState); err != nil {
		return QueryView{}, err
	}
	if err := tx.Commit(finalCtx); err != nil {
		return QueryView{}, err
	}
	committed = true
	q, err := s.loadQueryAdmin(finalCtx, queryID)
	if err != nil || q == nil {
		return QueryView{}, errors.New("无法读取已保存的查询")
	}
	return buildQueryView(q), nil
}

func (s *Service) releaseAfterLedgerFailure(queryID, holdID, key string) {
	if holdID == "" || s.billing == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer rollbackTx(tx)
	if err := s.billing.Release(ctx, tx, holdID, key); err != nil {
		return
	}
	_ = tx.Commit(ctx)
}

func (s *Service) markLedgerFailure(queryID, userID string, in finalizeInput, code string) {
	snapshot := snapshotFromQuote(in.Quote, in.ReservedMinor, in.HoldID, code, in.BillingKey)
	snapshot.Charged = false
	pricingJSON, _ := json.Marshal(snapshot)
	usageJSON, _ := json.Marshal(in.Usage)
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer rollbackTx(tx)
	var version int64
	err = tx.QueryRow(ctx, `UPDATE public.question_queries SET status = 'needs_review', usage = $2,
		pricing = $3, billing_state = $4, error_code = $5, offering_provider_ref = NULL,
		completed_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending' RETURNING version`,
		queryID, usageJSON, pricingJSON, code, code).Scan(&version)
	if err != nil {
		return
	}
	_ = recordQuestionEvent(ctx, tx, userID, queryID, version, "needs_review", false, "unpriced", code)
	_ = tx.Commit(ctx)
}

func (s *Service) markReserveFailed(queryID, code string) error {
	billingState := "reserve_failed"
	if code == "charging_setting_unavailable" {
		billingState = "charging_setting_unavailable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	// 等待先前的预占事务落定，再读取其实际结果；提交回执中断不代表事务回滚。
	q, err := scanQuery(tx.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries WHERE id = $1 FOR UPDATE`, queryID))
	if err != nil {
		return err
	}
	if q.Status != "pending" {
		return nil
	}
	if q.BillingHoldID != "" {
		snapshot := parsePricingSnapshot(q.Pricing)
		estimate, err := recoveryEstimate(snapshot)
		if err != nil {
			return err
		}
		// 复用结算链的原预占释放。先释放查询行锁，遵循账本、查询的现有加锁顺序。
		rollbackTx(tx)
		_, err = s.finalizeQuery(ctx, queryID, q.UserID, finalizeInput{
			Status: "failed", ErrorCode: code, Estimate: estimate, Quote: estimate,
			HoldID: q.BillingHoldID, ReservedMinor: q.ReservedMinor, BillingKey: snapshot.BillingKey,
			BillingState: "released",
		})
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE public.question_queries SET status = 'failed',
		error_code = $2, billing_state = $3, offering_provider_ref = NULL, completed_at = now(),
		updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'`, queryID, code, billingState)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("题库查询预占失败状态未保存")
	}
	return tx.Commit(ctx)
}

// markFinalizeFailed 只把仍处于 pending 的查询标记为待复核；已完成或已被其他路径
// 标记的记录不会被覆盖。
func (s *Service) markFinalizeFailed(queryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	_, _ = s.pool.Exec(ctx, `UPDATE public.question_queries SET status = 'needs_review',
		error_code = 'finalize_failed', billing_state = 'finalize_failed', offering_provider_ref = NULL,
		completed_at = now(), updated_at = now(), version = version + 1
		WHERE id = $1 AND status = 'pending'`, queryID)
}

func (s *Service) finalizeFailure(queryID string) *QueryError {
	s.markFinalizeFailed(queryID)
	return newQueryError("service_unavailable", "题库结果保存或计费失败，请人工核对", http.StatusServiceUnavailable, true)
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
	// 报告原因按字符计数，与公开契约和数据库长度检查一致。
	if input.Reason == "" || utf8.RuneCountInString(input.Reason) > 1000 {
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
