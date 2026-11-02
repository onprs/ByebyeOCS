package questions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

var queryIDPattern = regexp.MustCompile(`^qq_[0-9a-f]{32}$`)

func validQueryID(value string) bool { return queryIDPattern.MatchString(value) }

// releaseableUnknownCodes 是“结果未知且没有产生可结算事实”的错误码。
// 只有这些查询允许管理员释放幂等键后重新作答；usage_incomplete 之类已经拿到部分
// 用量事实的状态不在其中，避免用释放绕过复核。
var releaseableUnknownCodes = map[string]bool{
	"request_unknown":        true,
	"request_cancelled":      true,
	"upstream_timeout":       true,
	"pending_record_missing": true,
	"pending_state_unknown":  true,
}

const releaseReasonLimit = 200

type releaseQueryRequest struct {
	Reason string `json:"reason"`
}

// releaseUnknownQuery 释放一组“结果未知”的查询键。
//
// 处理方式：释放预占（如有），把原记录改成 failed/released_unknown_usage 并把幂等键改成
// 带后缀的审计键，原键因此可以重新发起。旧记录、用量事实和审计事件全部保留。
func (s *Service) releaseUnknownQuery(ctx context.Context, actor web.Actor, queryID, reason, requestID string) (QueryView, error) {
	if !validQueryID(queryID) {
		return QueryView{}, errQueryNotFound
	}
	reason = strings.TrimSpace(reason)
	if count := utf8.RuneCountInString(reason); count < 2 || count > releaseReasonLimit {
		return QueryView{}, newQueryError("validation_failed", "释放理由必须为 2 到 200 个字符", http.StatusUnprocessableEntity, false)
	}
	var view QueryView
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法开启题库事务", http.StatusServiceUnavailable, true)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()
	{
		record, err := scanQuery(tx.QueryRow(ctx, `SELECT `+queryColumns+` FROM public.question_queries
			WHERE id = $1 FOR UPDATE`, queryID))
		if errors.Is(err, pgx.ErrNoRows) {
			return QueryView{}, errQueryNotFound
		}
		if err != nil {
			return QueryView{}, newQueryError("service_unavailable", "无法读取题库查询", http.StatusServiceUnavailable, true)
		}
		if record.Status == "failed" && record.ErrorCode == releasedUnknownCode {
			// 重复调用按已完成处理，返回现有事实。
			view = buildQueryView(record)
			committed = true
			_ = tx.Rollback(ctx)
			return view, nil
		}
		if record.Status != "needs_review" {
			return QueryView{}, newQueryError("conflict", "只有待复核的查询可以释放", http.StatusConflict, false)
		}
		if !releaseableUnknownCodes[record.ErrorCode] {
			return QueryView{}, newQueryError("conflict", "该查询已有可结算用量，不能通过释放绕过复核", http.StatusConflict, false)
		}
		if record.BillingHoldID != "" {
			if s.billing == nil {
				return QueryView{}, newQueryError("service_unavailable", "题库账本未连接，预占待人工核对", http.StatusServiceUnavailable, true)
			}
			billingKey := parsePricingSnapshot(record.Pricing).BillingKey
			if err := s.billing.Release(ctx, tx, record.BillingHoldID, resolvedBillingKey(record.ID, billingKey)); err != nil {
				return QueryView{}, newQueryError("service_unavailable", "释放预占失败，查询保持待复核", http.StatusServiceUnavailable, true)
			}
		}
		nextKey := releasedKey(record.IdempotencyKey, record.Version)
		snapshot := parsePricingSnapshot(record.Pricing)
		snapshot.BillingState = "released"
		pricingJSON, _ := json.Marshal(snapshot)
		updated, err := scanQuery(tx.QueryRow(ctx, `UPDATE public.question_queries SET
			status = 'failed', error_code = $2, billing_state = 'released', pricing = $3,
			idempotency_key = $4, completed_at = now(), updated_at = now(), version = version + 1
			WHERE id = $1 AND status = 'needs_review' RETURNING `+queryColumns,
			queryID, releasedUnknownCode, pricingJSON, nextKey))
		if err != nil {
			return QueryView{}, newQueryError("service_unavailable", "无法释放题库查询", http.StatusServiceUnavailable, true)
		}
		if err := audit.Record(ctx, tx, audit.Event{Actor: actor, Action: "question.query.release",
			ResourceType: "question_query", ResourceID: queryID, Reason: reason, RequestID: requestID}); err != nil {
			return QueryView{}, newQueryError("service_unavailable", "无法记录释放审计", http.StatusServiceUnavailable, true)
		}
		view = buildQueryView(updated)
	}
	if err := tx.Commit(ctx); err != nil {
		return QueryView{}, newQueryError("service_unavailable", "无法提交题库释放", http.StatusServiceUnavailable, true)
	}
	committed = true
	return view, nil
}

const releasedUnknownCode = "released_unknown_usage"

var errQueryNotFound = newQueryError("not_found", "题库查询不存在", http.StatusNotFound, false)

// releasedKey 把原幂等键改成审计键，长度受列约束限制。
func releasedKey(key string, version int64) string {
	suffix := "#released-" + strconv.FormatInt(version, 10)
	if len(key)+len(suffix) <= 200 {
		return key + suffix
	}
	trimmed := key
	if len(trimmed) > 200-len(suffix) {
		trimmed = trimmed[:200-len(suffix)]
	}
	return trimmed + suffix
}

func (s *Service) handleReleaseQuery(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "question:manage")
	if !ok {
		return
	}
	var input releaseQueryRequest
	if !web.Decode(w, r, &input) {
		return
	}
	queryID := r.PathValue("id")
	view, err := s.releaseUnknownQuery(r.Context(), actor, queryID, input.Reason, web.RequestID(r.Context()))
	if err != nil {
		writeQueryError(w, r, err)
		return
	}
	// 返回受影响的作答任务，便于管理员直接重新排队；释放本身不改任务状态。
	tasks := []string{}
	if rows, err := s.pool.Query(r.Context(), `SELECT DISTINCT task_id::text FROM public.learning_work_runs
		WHERE $1 = ANY(query_ids) ORDER BY 1`, queryID); err == nil {
		for rows.Next() {
			var taskID string
			if rows.Scan(&taskID) == nil {
				tasks = append(tasks, taskID)
			}
		}
		rows.Close()
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"query": view, "tasks": tasks})
}

// RecoveryStats 是在途查询回收器的运行观测值。
type RecoveryStats struct {
	Runs      int64      `json:"runs"`
	Handled   int64      `json:"handled"`
	Failed    int64      `json:"failed"`
	LastRunAt *time.Time `json:"last_run_at,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

func (s *Service) recoveryStats() RecoveryStats {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	return s.recovery
}

func (s *Service) recordRecoveryRun(handled int, err error) {
	s.recoveryMu.Lock()
	defer s.recoveryMu.Unlock()
	s.recovery.Runs++
	s.recovery.Handled += int64(handled)
	now := s.now().UTC()
	s.recovery.LastRunAt = &now
	if err != nil {
		s.recovery.Failed++
		s.recovery.LastError = safeRecoveryError(err)
	}
}

func safeRecoveryError(err error) string {
	var failure *ForkError
	if errors.As(err, &failure) {
		return safeForkCode(failure.Code)
	}
	return "recovery_unavailable"
}

func (s *Service) handleRecoveryStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "question:read"); !ok {
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"recovery": s.recoveryStats()})
}
