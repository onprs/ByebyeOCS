package operations

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 删除复核决定；defer 是旧 {reason} 请求的兼容动作，不代表删除完成。
const (
	deletionDeferred  = "deferred"
	deletionRejected  = "rejected"
	deletionCompleted = "completed"
	deletionBlocked   = "needs_review"
)

// closureBlockers 只包含计数/布尔值，不暴露金额、题目或身份细节。
type closureBlockers struct {
	WalletBalances      bool `json:"wallet_balances"`
	OpenHolds           int  `json:"open_holds"`
	UnsettledOrders     int  `json:"unsettled_orders"`
	OpenRefunds         int  `json:"open_refunds"`
	OpenReconciliations int  `json:"open_reconciliations"`
	UnsettledUsage      int  `json:"unsettled_usage"`
	QuestionUsage       int  `json:"question_usage"`
	PendingCacheReports int  `json:"pending_cache_reports"`
	PendingPaymentInbox int  `json:"pending_payment_inbox"`
	ActiveAttempts      int  `json:"active_attempts"`
	ActiveTasks         int  `json:"active_tasks"`
	BillingBlockedTasks int  `json:"billing_blocked_tasks"`
	ActiveCourseSyncs   int  `json:"active_course_syncs"`
}

func (blockers closureBlockers) empty() bool {
	return !blockers.WalletBalances &&
		blockers.OpenHolds == 0 &&
		blockers.UnsettledOrders == 0 &&
		blockers.OpenRefunds == 0 &&
		blockers.OpenReconciliations == 0 &&
		blockers.UnsettledUsage == 0 &&
		blockers.QuestionUsage == 0 &&
		blockers.PendingCacheReports == 0 &&
		blockers.PendingPaymentInbox == 0 &&
		blockers.ActiveAttempts == 0 &&
		blockers.ActiveTasks == 0 &&
		blockers.BillingBlockedTasks == 0 &&
		blockers.ActiveCourseSyncs == 0
}

func (blockers closureBlockers) summary() string {
	parts := make([]string, 0, 6)
	if blockers.WalletBalances {
		parts = append(parts, "钱包仍有可用/预占/未结微元或已冻结")
	}
	for _, item := range []struct {
		count int
		label string
	}{
		{blockers.OpenHolds, "未释放预占"},
		{blockers.UnsettledOrders, "未决订单"},
		{blockers.OpenRefunds, "未决退款"},
		{blockers.OpenReconciliations, "未决对账"},
		{blockers.UnsettledUsage, "未结用量"},
		{blockers.QuestionUsage, "未结题库用量"},
		{blockers.PendingCacheReports, "待处理题库报告"},
		{blockers.PendingPaymentInbox, "未处理支付通知"},
		{blockers.ActiveAttempts, "运行中尝试"},
		{blockers.ActiveTasks, "运行中任务"},
		{blockers.BillingBlockedTasks, "计费待处理任务"},
		{blockers.ActiveCourseSyncs, "运行中同步"},
	} {
		if item.count > 0 {
			parts = append(parts, fmt.Sprintf("%s=%d", item.label, item.count))
		}
	}
	return strings.Join(parts, "；")
}

// decideDeletion 在同一事务内完成 defer/reject/complete 复核。
// 返回值为最终请求状态；blocked 时 blockers 非空且请求保持 needs_review。
func (s *Service) decideDeletion(ctx context.Context, actor web.Actor, requestID, action, reason string) (string, closureBlockers, error) {
	var result string
	var blockers closureBlockers
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", blockers, fault.New("service_unavailable", "暂时无法处理删除复核")
	}
	defer tx.Rollback(context.Background())

	var userID, current string
	if err := tx.QueryRow(ctx, `SELECT user_id::text, status FROM public.data_requests
		WHERE id = $1 AND kind = 'deletion' FOR UPDATE`, requestID).Scan(&userID, &current); err != nil {
		if err == pgx.ErrNoRows {
			return "", blockers, fault.New("not_found", "删除申请不存在")
		}
		return "", blockers, fault.New("service_unavailable", "暂时无法读取删除申请")
	}
	if current != "needs_review" && current != "deferred" {
		return "", blockers, fault.New("conflict", "当前删除申请状态不允许该复核动作")
	}

	var userStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM public.users WHERE id = $1 FOR UPDATE`, userID).Scan(&userStatus); err != nil {
		return "", blockers, fault.New("service_unavailable", "暂时无法读取账号状态")
	}

	switch action {
	case "defer":
		if _, err := tx.Exec(ctx, `UPDATE public.data_requests
			SET status = 'deferred', review_reason = $2, error_code = NULL, updated_at = now()
			WHERE id = $1`, requestID, reason); err != nil {
			return "", blockers, fault.New("service_unavailable", "暂时无法保存延后决定")
		}
		if err := s.record(ctx, tx, actor, "privacy.deletion_deferred", requestID, reason); err != nil {
			return "", blockers, err
		}
		result = deletionDeferred

	case "reject":
		if userStatus == "closed" {
			return "", blockers, fault.New("conflict", "账号已关闭，不能驳回删除申请")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.data_requests
			SET status = 'rejected', review_reason = $2, error_code = NULL, completed_at = now(), updated_at = now()
			WHERE id = $1`, requestID, reason); err != nil {
			return "", blockers, fault.New("service_unavailable", "暂时无法保存驳回决定")
		}
		if userStatus == "deletion_requested" {
			if _, err := tx.Exec(ctx, `UPDATE public.users
				SET status = 'active', version = version + 1, updated_at = now()
				WHERE id = $1 AND status = 'deletion_requested'`, userID); err != nil {
				return "", blockers, fault.New("service_unavailable", "暂时无法恢复账号状态")
			}
		}
		// 只清除本次删除流程写入的阻断；不解除其他管理风控，也不恢复任何会话或执行。
		if _, err := tx.Exec(ctx, `UPDATE public.learning_accounts
			SET blocked_code = '', blocked_reason = '', blocked_at = NULL, blocked_until = NULL,
			    version = version + 1, updated_at = now()
			WHERE user_id = $1 AND blocked_code = 'deletion_requested'`, userID); err != nil {
			return "", blockers, fault.New("service_unavailable", "暂时无法解除删除阻断")
		}
		if err := s.record(ctx, tx, actor, "privacy.deletion_rejected", requestID, reason); err != nil {
			return "", blockers, err
		}
		result = deletionRejected

	case "complete":
		if userStatus == "closed" {
			return "", blockers, fault.New("conflict", "账号已关闭，删除已完成")
		}
		// 钱包行锁与财务回调/结算串行化；随后在同一快照内重新核对所有阻断。
		var walletID string
		walletErr := tx.QueryRow(ctx, `SELECT id::text FROM public.wallets WHERE user_id = $1 FOR UPDATE`, userID).Scan(&walletID)
		if walletErr != nil && walletErr != pgx.ErrNoRows {
			return "", blockers, fault.New("service_unavailable", "暂时无法锁定钱包")
		}
		blockers, err = s.closureBlockers(ctx, tx, userID)
		if err != nil {
			return "", blockers, err
		}
		if !blockers.empty() {
			if _, err := tx.Exec(ctx, `UPDATE public.data_requests
				SET status = 'needs_review', review_reason = $2, error_code = 'closure_blocked', updated_at = now()
				WHERE id = $1`, requestID, reason); err != nil {
				return "", blockers, fault.New("service_unavailable", "暂时无法记录阻断结果")
			}
			// 理由上限 2000 字节；为阻断摘要预留空间，避免审计写入失败导致回滚。
			auditReason := truncateUTF8(strings.TrimSpace(reason), 1500) + "；" + blockers.summary()
			if err := s.record(ctx, tx, actor, "privacy.deletion_blocked", requestID, auditReason); err != nil {
				return "", blockers, err
			}
			if err := events.Record(ctx, tx, events.Event{
				Audience: "admin", Type: "privacy.deletion_blocked",
				ResourceID: requestID, Version: 1, Data: map[string]any{"status": "needs_review"},
			}); err != nil {
				return "", blockers, err
			}
			result = deletionBlocked
			break
		}
		if _, err := tx.Exec(ctx, `UPDATE public.users
			SET status = 'closed', version = version + 1, updated_at = now()
			WHERE id = $1`, userID); err != nil {
			return "", blockers, fault.New("service_unavailable", "暂时无法关闭账号")
		}
		if err := auth.EraseUserSecrets(ctx, tx, userID); err != nil {
			return "", blockers, err
		}
		if _, err := tx.Exec(ctx, `SELECT public.scrub_closed_user_data($1)`, userID); err != nil {
			return "", blockers, fault.New("service_unavailable", "无法清理关闭账号内容")
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.privacy_closures (user_id, request_id)
			VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`, userID, requestID); err != nil {
			return "", blockers, fault.New("service_unavailable", "无法保存关闭标记")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.data_requests
			SET status = 'completed', review_reason = $2, error_code = NULL, completed_at = now(), updated_at = now()
			WHERE id = $1`, requestID, reason); err != nil {
			return "", blockers, fault.New("service_unavailable", "无法保存完成决定")
		}
		if err := s.record(ctx, tx, actor, "privacy.deletion_completed", requestID, reason); err != nil {
			return "", blockers, err
		}
		if err := events.Record(ctx, tx, events.Event{
			Audience: "admin", Type: "privacy.deletion_completed",
			ResourceID: requestID, Version: 1, Data: map[string]any{"status": "completed"},
		}); err != nil {
			return "", blockers, err
		}
		result = deletionCompleted

	default:
		return "", blockers, fault.New("validation_failed", "复核动作只支持 defer、reject 或 complete")
	}

	if err := tx.Commit(ctx); err != nil {
		return "", blockers, fault.New("service_unavailable", "删除复核结果待核对")
	}
	return result, blockers, nil
}

// closureBlockers 在账户/钱包行锁之后核对所有未结资金与运行中活动。
func (s *Service) closureBlockers(ctx context.Context, tx pgx.Tx, userID string) (closureBlockers, error) {
	var blockers closureBlockers
	err := tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM public.wallets WHERE user_id = $1 AND (
			available_minor <> 0 OR reserved_minor <> 0 OR tiku_accrued_micros <> 0 OR frozen)),
		(SELECT count(*) FROM public.holds WHERE user_id = $1 AND status = 'open'),
		(SELECT count(*) FROM public.recharge_orders WHERE user_id = $1 AND (
			status IN ('pending_payment', 'needs_review')
			OR payment_status = 'unknown'
			OR (payment_status = 'paid' AND credit_status <> 'credited')
			OR credit_status = 'needs_review')),
		(SELECT count(*) FROM public.refunds WHERE user_id = $1 AND status IN (
			'requested', 'reserved', 'submitted', 'unknown')),
		(SELECT count(*) FROM public.reconciliations WHERE status IN (
			'pending', 'running', 'mismatch', 'incomplete', 'failed') AND (
			target_user_id = $1
			OR target_wallet_id IN (SELECT id FROM public.wallets WHERE user_id = $1)
			OR target_order_id IN (SELECT id FROM public.recharge_orders WHERE user_id = $1))),
		(SELECT count(*) FROM public.usage_records WHERE user_id = $1 AND (
			status IN ('reserved', 'needs_review', 'failed')
			OR (status = 'quoted' AND coalesce(quote_minor, 0) > 0)
			OR (scope = 'tiku_tokens' AND usage_complete = false AND status NOT IN ('released', 'disabled')))),
		(SELECT count(*) FROM public.question_queries WHERE user_id = $1 AND (
			status IN ('pending', 'needs_review')
			OR billing_state IN ('pending', 'reserved', 'billing_settlement_failed', 'usage_incomplete')
			OR reserved_minor > 0)),
		(SELECT count(*) FROM public.question_cache_reports WHERE user_id = $1 AND status = 'pending'),
		(SELECT count(*) FROM public.payment_inbox pi
			JOIN public.recharge_orders ro ON ro.id = pi.order_id
			WHERE ro.user_id = $1 AND pi.status IN ('received', 'needs_review')),
		(SELECT count(*) FROM public.learning_task_attempts WHERE user_id = $1 AND ended_at IS NULL),
		(SELECT count(*) FROM public.learning_tasks WHERE user_id = $1 AND state IN (
			'preparing', 'running', 'waiting_verification')),
		(SELECT count(*) FROM public.learning_tasks WHERE user_id = $1 AND billing_state IN (
			'reserved', 'release_pending', 'review_required', 'settlement_failed')),
		(SELECT count(*) FROM public.learning_course_syncs WHERE user_id = $1 AND status IN (
			'queued', 'running'))`,
		userID).Scan(
		&blockers.WalletBalances, &blockers.OpenHolds, &blockers.UnsettledOrders,
		&blockers.OpenRefunds, &blockers.OpenReconciliations, &blockers.UnsettledUsage,
		&blockers.QuestionUsage, &blockers.PendingCacheReports, &blockers.PendingPaymentInbox,
		&blockers.ActiveAttempts, &blockers.ActiveTasks, &blockers.BillingBlockedTasks,
		&blockers.ActiveCourseSyncs)
	if err != nil {
		return blockers, fault.New("service_unavailable", "无法核对删除前置条件")
	}
	return blockers, nil
}

// truncateUTF8 按字节上限截断，但不切断 UTF-8 字符。
func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

// writeClosureBlocked 返回标准错误封套并附带仅含计数的阻断明细。
func writeClosureBlocked(w http.ResponseWriter, r *http.Request, blockers closureBlockers) {
	requestID := web.RequestID(r.Context())
	if requestID == "" {
		requestID = web.NewRequestID()
		w.Header().Set("X-Request-ID", requestID)
	}
	web.JSON(w, r, http.StatusConflict, map[string]any{
		"error": map[string]any{
			"code":       "privacy_blocked",
			"message":    "存在未结资金或运行中的活动，暂不能完成删除；申请已保留在 needs_review",
			"request_id": requestID,
			"retryable":  false,
			"scope":      "request",
		},
		"blockers": blockers,
	})
}
