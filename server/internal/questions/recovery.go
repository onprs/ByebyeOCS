package questions

import (
	"context"
	"errors"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

const (
	// recoveryBatch 限制单轮回收条数，避免一次扫描长期占用连接。
	recoveryBatch = 20
	// recoveryInterval 是回收周期；必须短于一次任务的等待上限，才能及时解锁队列。
	recoveryInterval = 30 * time.Second
	// recoveryStatusTimeout 是读取题库请求状态的上限；这里只读状态，不触发模型调用。
	recoveryStatusTimeout = 5 * time.Second
)

// Run 定期回收长期停留在 pending 的查询。
//
// 这类查询只会在两种情况下产生：调用方等到窗口上限仍未拿到响应，或者进程在
// 调用题库期间退出。回收器先向题库确认状态：已完成就按正常完成链结算，
// 仍在处理就延长在途时间，确认失败或记录消失才转人工复核。
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(recoveryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		// 单轮失败不影响下一轮；结果只进入观测计数。
		handled, err := s.RecoverStaleQueries(ctx)
		s.recordRecoveryRun(handled, err)
	}
}

// RecoverStaleQueries 回收超过等待上限仍为 pending 的查询，返回处理的条数。
func (s *Service) RecoverStaleQueries(ctx context.Context) (int, error) {
	if s.fork == nil {
		return 0, fault.New("not_configured", "题库服务尚未配置")
	}
	rows, err := s.pool.Query(ctx, `SELECT `+queryColumns+` FROM public.question_queries
		WHERE status = 'pending' AND updated_at < now() - make_interval(secs => $1)
		ORDER BY updated_at LIMIT $2`, s.stalePendingAge().Seconds(), recoveryBatch)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法读取待恢复的题库查询")
	}
	var records []*queryRecord
	for rows.Next() {
		record, err := scanQuery(rows)
		if err != nil {
			rows.Close()
			return 0, fault.New("service_unavailable", "无法读取待恢复的题库查询")
		}
		records = append(records, record)
	}
	rows.Close()
	if rows.Err() != nil {
		return 0, fault.New("service_unavailable", "无法读取待恢复的题库查询")
	}
	handled := 0
	for _, record := range records {
		claimed, err := s.claimStaleQuery(ctx, record.ID, record.UserID)
		if err != nil {
			return handled, fault.New("service_unavailable", "无法抢占待恢复的题库查询")
		}
		if claimed == nil {
			// 已被同键请求或其他实例处理。
			continue
		}
		s.recoverPending(claimed)
		handled++
	}
	return handled, nil
}

// recoverPending 以题库状态为准决定这条在途查询的最终归属。
func (s *Service) recoverPending(record *queryRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), recoveryStatusTimeout+5*time.Second)
	defer cancel()
	statusCtx, cancelStatus := context.WithTimeout(ctx, recoveryStatusTimeout)
	status, err := s.fork.RequestStatus(statusCtx, record.ID)
	cancelStatus()
	if err != nil {
		var failure *ForkError
		if errors.As(err, &failure) && failure.Code == "request_not_found" {
			// 题库没有这条记录：无法再取回结果，转人工；不会重新调用模型。
			_, _ = s.finalizeRecoveryReview(ctx, record, Quote{}, "pending_record_missing")
			return
		}
		// 题库暂时不可达：保留 pending，下一轮继续。
		return
	}
	switch status.State {
	case "pending":
		// 仍在处理；claim 已刷新 updated_at，下一轮再查。
		return
	case "completed":
		if status.Response == nil {
			_, _ = s.finalizeRecoveryReview(ctx, record, Quote{}, "pending_response_missing")
			return
		}
		completion, ok := s.completionForRecord(record)
		if !ok {
			_, _ = s.finalizeRecoveryReview(ctx, record, Quote{}, "recovery_state_incomplete")
			return
		}
		_, _ = s.finishWithForkResponse(ctx, completion, *status.Response)
	case "failed", "unknown", "expired":
		code := safeForkCode(status.ErrorCode)
		if code == "" {
			code = "pending_" + status.State
		}
		_, _ = s.finalizeRecoveryReview(ctx, record, Quote{}, code)
	default:
		_, _ = s.finalizeRecoveryReview(ctx, record, Quote{}, "pending_state_unknown")
	}
}

// completionForRecord 用冻结的价格快照重建完成链所需的输入，不再解析题目内容。
func (s *Service) completionForRecord(record *queryRecord) (answerCompletion, bool) {
	snapshot := parsePricingSnapshot(record.Pricing)
	estimate, err := recoveryEstimate(snapshot)
	if err != nil {
		return answerCompletion{}, false
	}
	if record.OfferingProviderRef == "" {
		return answerCompletion{}, false
	}
	offering := &Offering{ID: record.OfferingID, PublicID: record.OfferingPublicID,
		DisplayName: record.OfferingDisplayName, ProviderRef: record.OfferingProviderRef,
		Pricing: snapshot.Pricing}
	return answerCompletion{
		queryID: record.ID, userID: record.UserID, offering: offering,
		request:  ForkAnswerRequest{RequestID: record.ID, Mode: record.Mode},
		estimate: estimate, holdID: record.BillingHoldID, reservedMinor: record.ReservedMinor,
		billingConnected: s.billing != nil, billingKey: snapshot.BillingKey,
		frozenBillingState: record.BillingState, recovered: true,
	}, true
}
