package questions

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const offeringColumns = `id::text, public_id, display_name, description, provider_ref, capabilities, enabled,
	is_default, sort_order, price_version, pricing, cache_policy, health, version, created_at, updated_at`

func scanOffering(row pgx.Row) (*Offering, error) {
	var o Offering
	var capabilities, pricing, policy []byte
	if err := row.Scan(&o.ID, &o.PublicID, &o.DisplayName, &o.Description, &o.ProviderRef, &capabilities,
		&o.Enabled, &o.Default, &o.SortOrder, &o.PriceVersion, &pricing, &policy, &o.Health, &o.Version, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	if len(pricing) > 0 && string(pricing) != "null" {
		if err := json.Unmarshal(pricing, &o.Pricing); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(capabilities, &o.Capabilities); err != nil || len(o.Capabilities) == 0 {
		o.Capabilities = []string{"text"}
	}
	if err := json.Unmarshal(policy, &o.CachePolicy); err != nil || o.CachePolicy.Scope == "" {
		o.CachePolicy = CachePolicy{Allow: true, Scope: "user", TTLDays: 7}
	}
	return &o, nil
}

func (s *Service) offeringByPublicID(ctx context.Context, publicID string, includeDisabled bool) (*Offering, error) {
	query := `SELECT ` + offeringColumns + ` FROM public.question_offerings WHERE public_id = $1`
	if !includeDisabled {
		query += ` AND enabled AND health <> 'unavailable'`
	}
	o, err := scanOffering(s.pool.QueryRow(ctx, query, publicID))
	if err == pgx.ErrNoRows {
		return nil, fault.New("not_found", "模型条目不存在或不可用")
	}
	if err != nil {
		return nil, fault.New("service_unavailable", "无法读取模型条目")
	}
	return o, nil
}

func (s *Service) defaultOffering(ctx context.Context) (*Offering, error) {
	o, err := scanOffering(s.pool.QueryRow(ctx, `SELECT `+offeringColumns+` FROM public.question_offerings
		WHERE is_default AND enabled AND health <> 'unavailable' LIMIT 1`))
	if err == pgx.ErrNoRows {
		return nil, fault.New("not_configured", "管理员尚未发布默认模型")
	}
	if err != nil {
		return nil, fault.New("service_unavailable", "无法读取默认模型")
	}
	return o, nil
}

func (s *Service) listPublicOfferings(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "user", "self"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	sortAfter, idAfter := -1<<31, ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		sortAfter, idAfter, err = decodeOfferingCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT `+offeringColumns+` FROM public.question_offerings
		WHERE enabled AND health <> 'unavailable' AND (sort_order, public_id) > ($1, $2)
		ORDER BY sort_order, public_id LIMIT $3`, sortAfter, idAfter, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取模型列表")
		return
	}
	defer rows.Close()
	items := make([]publicOfferingView, 0, limit)
	cursor := ""
	for rows.Next() {
		o, err := scanOffering(rows)
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "模型列表格式无效")
			return
		}
		if len(items) == limit {
			last := items[len(items)-1]
			cursor = encodeOfferingCursor(last.SortOrder, last.ID)
			break
		}
		items = append(items, o.Public())
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取模型列表")
		return
	}
	var next any
	if cursor != "" {
		next = cursor
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

type offeringInput struct {
	DisplayName  string           `json:"display_name"`
	Description  string           `json:"description"`
	ProviderRef  string           `json:"provider_ref"`
	Capabilities []string         `json:"capabilities"`
	Enabled      *bool            `json:"enabled"`
	Default      *bool            `json:"default"`
	SortOrder    *int             `json:"sort_order"`
	PriceVersion string           `json:"price_version"`
	Pricing      *OfferingPricing `json:"pricing"`
	CachePolicy  *CachePolicy     `json:"cache_policy"`
	Health       string           `json:"health"`
}

func validateCapabilities(values []string) ([]string, error) {
	if len(values) == 0 {
		values = []string{"text"}
	}
	if len(values) > 8 {
		return nil, fault.New("validation_failed", "模型能力项过多")
	}
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "text" && value != "vision" {
			return nil, fault.New("validation_failed", "模型能力只支持 text 与 vision")
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	if !seen["text"] {
		return nil, fault.New("validation_failed", "模型至少需要 text 能力")
	}
	return result, nil
}

func validateCachePolicy(policy CachePolicy) (CachePolicy, error) {
	if policy.Scope == "" {
		policy.Scope = "user"
	}
	if policy.Scope != "user" && policy.Scope != "shared" {
		return policy, fault.New("validation_failed", "缓存范围无效")
	}
	if policy.TTLDays == 0 {
		policy.TTLDays = 7
	}
	if policy.TTLDays < 1 || policy.TTLDays > 30 {
		return policy, fault.New("validation_failed", "缓存 TTL 必须在 1 到 30 天之间")
	}
	return policy, nil
}

func validateOfferingInput(input *offeringInput) error {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.ProviderRef = strings.TrimSpace(input.ProviderRef)
	if input.DisplayName == "" || len(input.DisplayName) > 80 {
		return fault.New("validation_failed", "公开名称不能为空或超过 80 字")
	}
	if input.ProviderRef == "" || len(input.ProviderRef) > 128 {
		return fault.New("validation_failed", "fork 模型引用不能为空或超过 128 字")
	}
	if len(input.Description) > 500 {
		return fault.New("validation_failed", "说明不能超过 500 字")
	}
	capabilities, err := validateCapabilities(input.Capabilities)
	if err != nil {
		return err
	}
	input.Capabilities = capabilities
	policy := CachePolicy{Allow: true, Scope: "user", TTLDays: 7}
	if input.CachePolicy != nil {
		policy = *input.CachePolicy
	}
	policy, err = validateCachePolicy(policy)
	if err != nil {
		return err
	}
	input.CachePolicy = &policy
	if input.Default != nil && *input.Default && input.Enabled != nil && !*input.Enabled {
		return fault.New("validation_failed", "已下架的模型不能设为默认")
	}
	if input.Health == "" {
		input.Health = "configured"
	}
	switch input.Health {
	case "configured", "healthy", "degraded", "unavailable":
	default:
		return fault.New("validation_failed", "模型健康状态无效")
	}
	if len(input.PriceVersion) > 64 {
		return fault.New("validation_failed", "价格版本过长")
	}
	if input.Pricing == nil && input.PriceVersion != "" {
		return fault.New("validation_failed", "价格版本必须与完整 pricing 配置一起提供")
	}
	if input.Pricing != nil {
		if err := input.Pricing.Validate(); err != nil {
			return err
		}
		input.PriceVersion = input.Pricing.Version
	}
	return nil
}

func (s *Service) adminCreateOffering(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	var input offeringInput
	if !web.Decode(w, r, &input) {
		return
	}
	if err := validateOfferingInput(&input); err != nil {
		web.Fail(w, r, err)
		return
	}
	enabled, isDefault, sortOrder := true, false, 100
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	if input.Default != nil {
		isDefault = *input.Default
	}
	if input.SortOrder != nil {
		sortOrder = *input.SortOrder
	}
	id := web.NewID()
	publicID := newPrefixedID("qm_")
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法保存模型条目")
		return
	}
	defer tx.Rollback(context.Background())
	if isDefault {
		if _, err := tx.Exec(r.Context(), `UPDATE public.question_offerings SET is_default = false,
			version = version + 1, updated_at = now() WHERE is_default`); err != nil {
			web.Error(w, r, 503, "service_unavailable", "无法更新默认模型")
			return
		}
	}
	capabilities, _ := json.Marshal(input.Capabilities)
	policy, _ := json.Marshal(input.CachePolicy)
	pricingJSON, _ := json.Marshal(input.Pricing)
	_, err = tx.Exec(r.Context(), `INSERT INTO public.question_offerings
		(id, public_id, display_name, description, provider_ref, capabilities, enabled, is_default, sort_order,
		 price_version, pricing, cache_policy, health)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,'null')::jsonb,$12,$13)`,
		id, publicID, input.DisplayName, input.Description, input.ProviderRef, capabilities, enabled, isDefault,
		sortOrder, input.PriceVersion, string(pricingJSON), policy, input.Health)
	if err != nil {
		if strings.Contains(err.Error(), "question_offerings_one_default_idx") {
			web.Error(w, r, 409, "conflict", "默认模型已被其他条目占用，请重新加载")
			return
		}
		web.Error(w, r, 503, "service_unavailable", "无法保存模型条目")
		return
	}
	if err := recordPriceVersion(r.Context(), tx, id, input.Pricing, actor); err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := audit.Record(r.Context(), tx, audit.Event{Actor: actor, Action: "question.model.create",
		ResourceType: "question_model", ResourceID: id}); err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		web.Error(w, r, 503, "service_unavailable", "模型条目保存结果待核对")
		return
	}
	offering := Offering{ID: id, PublicID: publicID, DisplayName: input.DisplayName, Description: input.Description,
		ProviderRef: input.ProviderRef, Capabilities: input.Capabilities, Enabled: enabled, Default: isDefault,
		SortOrder: sortOrder, PriceVersion: input.PriceVersion, Pricing: input.Pricing, CachePolicy: *input.CachePolicy,
		Health: input.Health, Version: 1, CreatedAt: s.now(), UpdatedAt: s.now()}
	web.ETag(w, offering.Version)
	web.JSON(w, r, http.StatusCreated, offering.Admin())
}

func (s *Service) adminListOfferings(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "question:read"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	sortAfter, idAfter := -1<<31, ""
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		sortAfter, idAfter, err = decodeOfferingCursor(raw)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT `+offeringColumns+` FROM public.question_offerings
		WHERE (sort_order, public_id) > ($1, $2) ORDER BY sort_order, public_id LIMIT $3`, sortAfter, idAfter, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取模型列表")
		return
	}
	defer rows.Close()
	items := make([]adminOfferingView, 0, limit)
	cursor := ""
	for rows.Next() {
		o, err := scanOffering(rows)
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "模型列表格式无效")
			return
		}
		if len(items) == limit {
			last := items[len(items)-1]
			cursor = encodeOfferingCursor(last.SortOrder, last.ID)
			break
		}
		items = append(items, o.Admin())
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取模型列表")
		return
	}
	var next any
	if cursor != "" {
		next = cursor
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func (s *Service) adminPatchOffering(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	version, err := web.MatchVersion(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	var body struct {
		DisplayName  *string          `json:"display_name"`
		Description  *string          `json:"description"`
		ProviderRef  *string          `json:"provider_ref"`
		Capabilities []string         `json:"capabilities"`
		Enabled      *bool            `json:"enabled"`
		Default      *bool            `json:"default"`
		SortOrder    *int             `json:"sort_order"`
		PriceVersion *string          `json:"price_version"`
		Pricing      *OfferingPricing `json:"pricing"`
		CachePolicy  *CachePolicy     `json:"cache_policy"`
		Health       *string          `json:"health"`
	}
	if !web.Decode(w, r, &body) {
		return
	}
	publicID := r.PathValue("id")
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法更新模型条目")
		return
	}
	defer tx.Rollback(context.Background())
	current, err := scanOffering(tx.QueryRow(r.Context(), `SELECT `+offeringColumns+`
		FROM public.question_offerings WHERE public_id = $1 FOR UPDATE`, publicID))
	if err == pgx.ErrNoRows {
		web.Error(w, r, 404, "not_found", "模型条目不存在")
		return
	}
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取模型条目")
		return
	}
	if current.Version != version {
		web.Error(w, r, 412, "version_conflict", "模型条目已更新，请重新加载")
		return
	}
	if body.DisplayName != nil {
		current.DisplayName = *body.DisplayName
	}
	if body.Description != nil {
		current.Description = *body.Description
	}
	if body.ProviderRef != nil {
		current.ProviderRef = *body.ProviderRef
	}
	if body.Capabilities != nil {
		current.Capabilities = body.Capabilities
	}
	if body.Enabled != nil {
		current.Enabled = *body.Enabled
		if !current.Enabled {
			current.Default = false
		}
	}
	if body.Default != nil {
		current.Default = *body.Default
	}
	if body.SortOrder != nil {
		current.SortOrder = *body.SortOrder
	}
	if body.PriceVersion != nil && body.Pricing != nil && *body.PriceVersion != body.Pricing.Version {
		web.Error(w, r, 422, "validation_failed", "价格版本必须与完整费率中的版本一致")
		return
	}
	if body.PriceVersion != nil && body.Pricing == nil && *body.PriceVersion != current.PriceVersion {
		web.Error(w, r, 422, "validation_failed", "修改价格版本须同时提供完整费率")
		return
	}
	if body.Pricing != nil {
		current.Pricing = body.Pricing
		current.PriceVersion = body.Pricing.Version
	}
	if body.PriceVersion != nil {
		current.PriceVersion = *body.PriceVersion
	}
	if body.CachePolicy != nil {
		current.CachePolicy = *body.CachePolicy
	}
	if body.Health != nil {
		current.Health = *body.Health
	}
	// 复用创建时的校验；先构造输入以避免部分字段绕过。
	check := offeringInput{DisplayName: current.DisplayName, Description: current.Description, ProviderRef: current.ProviderRef,
		Capabilities: current.Capabilities, Enabled: &current.Enabled, Default: &current.Default,
		SortOrder: &current.SortOrder, PriceVersion: current.PriceVersion, Pricing: current.Pricing,
		CachePolicy: &current.CachePolicy, Health: current.Health}
	if err := validateOfferingInput(&check); err != nil {
		web.Fail(w, r, err)
		return
	}
	current.CachePolicy = *check.CachePolicy
	current.PriceVersion = check.PriceVersion
	capabilities, _ := json.Marshal(check.Capabilities)
	policy, _ := json.Marshal(check.CachePolicy)
	currentPricingJSON, _ := json.Marshal(check.Pricing)
	if err := recordPriceVersion(r.Context(), tx, current.ID, check.Pricing, actor); err != nil {
		web.Fail(w, r, err)
		return
	}
	if current.Default {
		if _, err := tx.Exec(r.Context(), `UPDATE public.question_offerings SET is_default = false,
			version = version + 1, updated_at = now() WHERE is_default AND public_id <> $1`, publicID); err != nil {
			web.Error(w, r, 503, "service_unavailable", "无法更新默认模型")
			return
		}
	}
	_, err = tx.Exec(r.Context(), `UPDATE public.question_offerings SET display_name = $2, description = $3,
		provider_ref = $4, capabilities = $5, enabled = $6, is_default = $7, sort_order = $8,
		price_version = $9, pricing = NULLIF($10,'null')::jsonb, cache_policy = $11, health = $12,
		version = version + 1, updated_at = now()
		WHERE public_id = $1`, publicID, check.DisplayName, check.Description, check.ProviderRef, capabilities,
		current.Enabled, current.Default, current.SortOrder, current.PriceVersion,
		string(currentPricingJSON), policy, check.Health)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法更新模型条目")
		return
	}
	if err := audit.Record(r.Context(), tx, audit.Event{Actor: actor, Action: "question.model.update",
		ResourceType: "question_model", ResourceID: current.ID}); err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		web.Error(w, r, 503, "service_unavailable", "模型条目更新结果待核对")
		return
	}
	current.DisplayName, current.Description, current.ProviderRef = check.DisplayName, check.Description, check.ProviderRef
	current.Capabilities, current.Health, current.Version = check.Capabilities, check.Health, current.Version+1
	current.UpdatedAt = s.now()
	web.ETag(w, current.Version)
	web.JSON(w, r, http.StatusOK, current.Admin())
}

func (s *Service) adminTestOffering(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	var body struct {
		Kind        string `json:"kind"`
		ConfirmCost bool   `json:"confirm_cost"`
	}
	if !web.Decode(w, r, &body) {
		return
	}
	if body.Kind != "reachability" && body.Kind != "functional" {
		web.Error(w, r, 422, "validation_failed", "测试类型必须是 reachability 或 functional")
		return
	}
	if body.Kind == "functional" && !body.ConfirmCost {
		web.Error(w, r, 428, "cost_confirmation_required", "功能性测试会真实调用模型并产生 token 费用，需要确认")
		return
	}
	if err := s.requireFork(); err != nil {
		web.Fail(w, r, err)
		return
	}
	offering, err := s.offeringByPublicID(r.Context(), r.PathValue("id"), true)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 35*time.Second)
	defer cancel()
	result, testErr := s.fork.TestModel(ctx, offering.ProviderRef, body.Kind, body.ConfirmCost)
	status := "ok"
	errorCode := ""
	if testErr != nil {
		status = "failed"
		errorCode = "service_unavailable"
		var forkErr *ForkError
		if ok := asForkError(testErr, &forkErr); ok {
			errorCode = forkErr.Code
		}
	}
	if result.Status == "failed" && status == "ok" {
		status = "failed"
		errorCode = result.ErrorCode
	}
	if body.Kind == "functional" && !result.Functional && status == "ok" {
		status = "failed"
		errorCode = "functional_test_failed"
	}
	if !result.Reachable && body.Kind == "reachability" && status == "ok" {
		status = "failed"
		errorCode = "reachability_failed"
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法记录模型测试结果")
		return
	}
	defer tx.Rollback(context.Background())
	testID := web.NewID()
	if _, err := tx.Exec(r.Context(), `INSERT INTO public.question_model_tests
		(id, offering_id, actor_admin_id, kind, status, error_code, latency_ms, cost_confirmed)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),$7,$8)`,
		testID, offering.ID, actor.ID, body.Kind, status, errorCode, result.LatencyMS,
		body.Kind == "functional" && body.ConfirmCost); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法记录模型测试结果")
		return
	}
	if err := audit.Record(r.Context(), tx, audit.Event{Actor: actor, Action: "question.model.test",
		ResourceType: "question_model", ResourceID: offering.ID}); err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		web.Error(w, r, 503, "service_unavailable", "模型测试记录结果待核对")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{
		"kind": body.Kind, "status": status, "reachable": result.Reachable, "functional": result.Functional,
		"latency_ms": result.LatencyMS, "error_code": errorCode, "cost_confirmed": body.Kind == "functional" && body.ConfirmCost,
	})
}

func asForkError(err error, target **ForkError) bool {
	value, ok := err.(*ForkError)
	if ok {
		*target = value
	}
	return ok
}

func encodeOfferingCursor(sortOrder int, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d|%s", sortOrder, id)))
}

func decodeOfferingCursor(raw string) (int, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	parts := strings.SplitN(string(data), "|", 2)
	if len(parts) != 2 {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	value, err := strconv.Atoi(parts[0])
	if err != nil || parts[1] == "" {
		return 0, "", fault.New("validation_failed", "分页游标无效")
	}
	return value, parts[1], nil
}
