package questions

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

const (
	cacheReportPending  = "pending"
	cacheReportResolved = "resolved"
	cacheReportRejected = "rejected"
)

type cacheReportQueryRef struct {
	ID    string   `json:"id"`
	Model ModelRef `json:"model"`
}

// cacheReportItem 只包含报告、查询/模型引用和缓存条目编号；不返回题干、答案、
// 用量、令牌或用户标识。
type cacheReportItem struct {
	ID           string              `json:"id"`
	Status       string              `json:"status"`
	Reason       string              `json:"reason"`
	CreatedAt    time.Time           `json:"created_at"`
	ResolvedAt   *time.Time          `json:"resolved_at"`
	Query        cacheReportQueryRef `json:"query"`
	CacheEntryID *string             `json:"cache_entry_id"`
}

// adminListCacheReports 分页列出缓存报告；默认只看 pending，支持按终态筛选。
func (s *Service) adminListCacheReports(w http.ResponseWriter, r *http.Request) {
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
		if !web.ValidID(afterID) {
			web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "分页游标无效")
			return
		}
	} else {
		// 与既有管理员分页一致：以未来时间作为首屏上界；零 UUID 让同一时刻的
		// 报告仍按 id 降序进入第一页。
		after = s.now().Add(24 * time.Hour)
		afterID = "00000000-0000-0000-0000-000000000000"
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = cacheReportPending
	}
	switch status {
	case cacheReportPending, cacheReportResolved, cacheReportRejected:
	default:
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "状态筛选无效")
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT r.id::text, r.status, r.reason, r.created_at, r.resolved_at,
		q.id, q.offering_public_id, q.offering_display_name, q.cache_entry_id
		FROM public.question_cache_reports r
		JOIN public.question_queries q ON q.id = r.query_id
		WHERE r.status = $3 AND (r.created_at, r.id) < ($1, $2::uuid)
		ORDER BY r.created_at DESC, r.id DESC LIMIT $4`, after, afterID, status, limit+1)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取缓存报告")
		return
	}
	defer rows.Close()
	items := make([]cacheReportItem, 0, limit)
	var next any
	for rows.Next() {
		item, err := scanCacheReportItem(rows)
		if err != nil {
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "缓存报告格式无效")
			return
		}
		if len(items) == limit {
			last := items[len(items)-1]
			next = encodeCursor(last.CreatedAt, last.ID)
			break
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取缓存报告")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

type cacheReportScanner interface {
	Scan(dest ...any) error
}

func scanCacheReportItem(row cacheReportScanner) (cacheReportItem, error) {
	var item cacheReportItem
	var resolvedAt *time.Time
	var cacheEntryID *string
	if err := row.Scan(&item.ID, &item.Status, &item.Reason, &item.CreatedAt, &resolvedAt,
		&item.Query.ID, &item.Query.Model.OfferingID, &item.Query.Model.DisplayName, &cacheEntryID); err != nil {
		return cacheReportItem{}, err
	}
	item.CreatedAt = item.CreatedAt.UTC()
	if resolvedAt != nil {
		value := resolvedAt.UTC()
		item.ResolvedAt = &value
	}
	item.CacheEntryID = cacheEntryID
	return item, nil
}

type cacheReportReviewInput struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

type cacheReportReviewRecord struct {
	ID         string
	ReportID   string
	Status     string
	Reason     string
	ReviewedAt time.Time
}

type cacheReportReviewResult struct {
	ID         string    `json:"id"`
	ReviewID   string    `json:"review_id"`
	Status     string    `json:"status"`
	ResolvedAt time.Time `json:"resolved_at"`
}

// adminReviewCacheReport 将报告一次性结清为 resolved/rejected。报告进入终态后
// 不再改写；相同管理员、幂等键、报告、状态和理由的重放返回原结果，其他复用
// 返回冲突。复核本身不调用题库失效入口，失效仍由既有接口处理。
func (s *Service) adminReviewCacheReport(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if count := utf8.RuneCountInString(key); count < 8 || count > 128 {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "Idempotency-Key 必须为 8 到 128 个字符")
		return
	}
	var input cacheReportReviewInput
	if !web.Decode(w, r, &input) {
		return
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != cacheReportResolved && input.Status != cacheReportRejected {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "复核状态必须是 resolved 或 rejected")
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if count := utf8.RuneCountInString(input.Reason); count < 5 || count > 500 {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "复核理由必须为 5 到 500 个字符")
		return
	}
	reportID := r.PathValue("id")
	if !web.ValidID(reportID) {
		web.Error(w, r, http.StatusNotFound, "not_found", "缓存报告不存在")
		return
	}

	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法开始复核")
		return
	}
	defer rollbackTx(tx)

	var reportStatus string
	var reportResolvedAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT status, resolved_at FROM public.question_cache_reports
		WHERE id = $1::uuid FOR UPDATE`, reportID).Scan(&reportStatus, &reportResolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			web.Error(w, r, http.StatusNotFound, "not_found", "缓存报告不存在")
			return
		}
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取缓存报告")
		return
	}

	existing, err := loadCacheReportReview(ctx, tx, actor.ID, key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法核对复核幂等记录")
		return
	}
	if err == nil {
		writeCacheReportReviewReplay(w, r, existing, reportStatus, reportResolvedAt, reportID, input)
		return
	}
	if reportStatus != cacheReportPending {
		web.Error(w, r, http.StatusConflict, "conflict", "报告已处理，不能再次复核")
		return
	}

	// PostgreSQL timestamptz 保存微秒；先截断，使响应中的 resolved_at 与库内一致。
	now := s.now().UTC().Truncate(time.Microsecond)
	reviewID := web.NewID()
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO public.question_cache_report_reviews
		(id, report_id, actor_admin_id, idempotency_key, status, reason, reviewed_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, $6, $7)
		ON CONFLICT (actor_admin_id, idempotency_key) DO NOTHING
		RETURNING id::text`,
		reviewID, reportID, actor.ID, key, input.Status, input.Reason, now).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// 同一管理员用同一键并发处理不同报告：键已绑定胜出请求，绝不改写本次报告。
		winner, loadErr := loadCacheReportReview(ctx, tx, actor.ID, key)
		if loadErr != nil {
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法核对复核幂等记录")
			return
		}
		writeCacheReportReviewReplay(w, r, winner, reportStatus, reportResolvedAt, reportID, input)
		return
	}
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法保存复核记录")
		return
	}
	tag, err := tx.Exec(ctx, `UPDATE public.question_cache_reports SET status = $2, resolved_at = $3
		WHERE id = $1::uuid AND status = 'pending'`, reportID, input.Status, now)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法更新缓存报告")
		return
	}
	if tag.RowsAffected() != 1 {
		web.Error(w, r, http.StatusConflict, "conflict", "报告已处理，不能再次复核")
		return
	}
	if err := audit.Record(ctx, tx, audit.Event{Actor: actor, Action: "question.cache_report.review",
		ResourceType: "question_cache_report", ResourceID: reportID, Reason: input.Reason}); err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法写入复核审计")
		return
	}
	if err := tx.Commit(ctx); err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "复核结果待核对，请使用同一幂等键重试")
		return
	}
	web.JSON(w, r, http.StatusOK, cacheReportReviewResult{
		ID: reportID, ReviewID: inserted, Status: input.Status, ResolvedAt: now,
	})
}

// writeCacheReportReviewReplay 只在幂等键绑定的报告/状态/理由与本次完全一致时
// 返回原复核；其他复用一律冲突，终态不会被重写。
func writeCacheReportReviewReplay(w http.ResponseWriter, r *http.Request, existing cacheReportReviewRecord,
	reportStatus string, reportResolvedAt *time.Time, reportID string, input cacheReportReviewInput) {
	if existing.ReportID != reportID || existing.Status != input.Status || existing.Reason != input.Reason {
		web.Error(w, r, http.StatusConflict, "idempotency_conflict", "相同幂等键已用于不同的复核请求")
		return
	}
	if reportStatus != existing.Status {
		web.Error(w, r, http.StatusConflict, "conflict", "报告状态与复核记录不一致")
		return
	}
	resolvedAt := existing.ReviewedAt.UTC()
	if reportResolvedAt != nil {
		resolvedAt = reportResolvedAt.UTC()
	}
	web.JSON(w, r, http.StatusOK, cacheReportReviewResult{
		ID: reportID, ReviewID: existing.ID, Status: existing.Status, ResolvedAt: resolvedAt,
	})
}

func loadCacheReportReview(ctx context.Context, tx pgx.Tx, adminID, key string) (cacheReportReviewRecord, error) {
	var item cacheReportReviewRecord
	err := tx.QueryRow(ctx, `SELECT id::text, report_id::text, status, reason, reviewed_at
		FROM public.question_cache_report_reviews
		WHERE actor_admin_id = $1::uuid AND idempotency_key = $2`, adminID, key).
		Scan(&item.ID, &item.ReportID, &item.Status, &item.Reason, &item.ReviewedAt)
	return item, err
}
