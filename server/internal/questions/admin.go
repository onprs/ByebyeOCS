package questions

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type offeringUsage struct {
	OfferingID   string         `json:"offering_id"`
	DisplayName  string         `json:"display_name"`
	Queries      int64          `json:"queries"`
	CacheHits    int64          `json:"cache_hits"`
	InputTokens  int64          `json:"input_tokens"`
	OutputTokens int64          `json:"output_tokens"`
	TotalTokens  int64          `json:"total_tokens"`
	CostMicros   string         `json:"cost_micros"`
	Pricing      *PublicPricing `json:"pricing,omitempty"`
}

func (s *Service) adminUsage(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "question:read"); !ok {
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT o.public_id, o.display_name, o.pricing,
		count(q.id), count(q.id) FILTER (WHERE q.cache_hit),
		COALESCE(SUM(CASE WHEN q.usage->>'input_tokens' ~ '^[0-9]+$' THEN (q.usage->>'input_tokens')::bigint ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN q.usage->>'output_tokens' ~ '^[0-9]+$' THEN (q.usage->>'output_tokens')::bigint ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN q.pricing->>'state' = 'quoted' AND q.pricing->>'charged' = 'true'
			THEN (q.pricing->>'cost_micros')::bigint ELSE 0 END), 0)
		FROM public.question_offerings o LEFT JOIN public.question_queries q ON q.offering_id = o.id
		GROUP BY o.id, o.public_id, o.display_name ORDER BY o.sort_order, o.public_id`)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库用量")
		return
	}
	defer rows.Close()
	items := make([]offeringUsage, 0)
	for rows.Next() {
		var item offeringUsage
		var pricingJSON []byte
		if err := rows.Scan(&item.OfferingID, &item.DisplayName, &pricingJSON, &item.Queries, &item.CacheHits,
			&item.InputTokens, &item.OutputTokens, &item.CostMicros); err != nil {
			web.Error(w, r, 503, "service_unavailable", "题库用量格式无效")
			return
		}
		item.TotalTokens = item.InputTokens + item.OutputTokens
		if len(pricingJSON) > 0 && string(pricingJSON) != "null" {
			var pricing OfferingPricing
			if err := json.Unmarshal(pricingJSON, &pricing); err == nil {
				item.Pricing = pricing.Public()
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取题库用量")
		return
	}
	automatic := s.auto
	automaticUnavailable := false
	if allowed, err := s.resolveChargingAllowed(r.Context()); err != nil {
		automatic, automaticUnavailable = false, true
	} else {
		automatic = allowed
	}
	web.JSON(w, r, http.StatusOK, map[string]any{
		"items": items,
		"pricing": map[string]any{
			"automatic_charging": automatic, "automatic_charging_unavailable": automaticUnavailable,
			"unit": "micros_per_million_tokens", "per_model": true,
		},
		"next_cursor": nil,
	})
}

type invalidationInput struct {
	CacheEntryID string `json:"cache_entry_id"`
	EntryID      string `json:"entry_id"`
	QueryID      string `json:"query_id"`
	Reason       string `json:"reason"`
}

func (s *Service) adminInvalidateCache(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	var input invalidationInput
	if !web.Decode(w, r, &input) {
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	// 公开契约与数据库约束都按字符计数；不能把中文原因按字节提前拒绝。
	if input.Reason == "" || utf8.RuneCountInString(input.Reason) > 1000 {
		web.Error(w, r, 422, "validation_failed", "失效原因不能为空或超过 1000 字")
		return
	}
	if input.CacheEntryID == "" {
		input.CacheEntryID = input.EntryID
	}
	var offeringID string
	if input.CacheEntryID == "" && input.QueryID != "" {
		q, err := s.loadQueryAdmin(r.Context(), input.QueryID)
		if err != nil {
			web.Error(w, r, 503, "service_unavailable", "无法读取题库查询")
			return
		}
		if q == nil {
			web.Error(w, r, 404, "not_found", "题库查询不存在")
			return
		}
		input.CacheEntryID = q.CacheEntryID
		offeringID = q.OfferingID
	}
	if input.CacheEntryID == "" {
		web.Error(w, r, 422, "validation_failed", "缺少缓存条目编号")
		return
	}
	if err := s.requireFork(); err != nil {
		web.Fail(w, r, err)
		return
	}
	invalidated, err := s.fork.InvalidateCache(r.Context(), input.CacheEntryID, input.Reason)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "题库缓存失效未完成")
		return
	}
	id := web.NewID()
	var offering any
	if offeringID != "" {
		offering = offeringID
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "缓存已失效，但平台记录失败，请人工核对")
		return
	}
	defer rollbackTx(tx)
	if _, err := tx.Exec(r.Context(), `INSERT INTO public.question_cache_invalidations
		(id, offering_id, cache_entry_id, reason, actor_admin_id, fork_invalidated)
		VALUES ($1,$2,$3,$4,$5,$6)`, id, offering, input.CacheEntryID, input.Reason, actor.ID, invalidated); err != nil {
		web.Error(w, r, 503, "service_unavailable", "缓存已失效，但平台记录失败，请人工核对")
		return
	}
	// 失效后同一缓存条目的待处理报告已无事可做：无论 fork 实际标记还是条目已过期，
	// 旧答案都不会再被命中，一并结清，避免永久阻断删除复核。
	if _, err := tx.Exec(r.Context(), `UPDATE public.question_cache_reports r SET status = 'resolved',
		resolved_at = now() FROM public.question_queries q
		WHERE r.query_id = q.id AND r.status = 'pending' AND q.cache_entry_id = $1`, input.CacheEntryID); err != nil {
		web.Error(w, r, 503, "service_unavailable", "缓存已失效，但报告状态未保存，请人工核对")
		return
	}
	if err := audit.Record(r.Context(), tx, audit.Event{Actor: actor, Action: "question.cache.invalidate",
		ResourceType: "question_cache", ResourceID: id, Reason: input.Reason}); err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		web.Error(w, r, 503, "service_unavailable", "缓存失效记录结果待核对")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"invalidated": invalidated, "cache_entry_id": input.CacheEntryID})
}
