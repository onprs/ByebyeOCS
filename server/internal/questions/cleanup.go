package questions

import (
	"context"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

const (
	// contentRetention 是原题与答案内容的保留期；用量、价格、幂等与账本关联不受影响。
	contentRetention = 7 * 24 * time.Hour
	// cleanupBatch 与数据保留默认方案一致：每批最多 500 行。
	cleanupBatch = 500
	// cleanupJobTimeout 是调用方未提供截止时间时的兜底作业上限。
	cleanupJobTimeout = 30 * time.Second
)

// CleanupResult 是本次清理实际脱敏的查询数。
type CleanupResult struct {
	QuestionsSanitized int64
}

// RunCleanup 清空超过内容保留期的原题、选项与答案，并显式标记 content_expired。
//
// 只处理已完成且账务已落定的查询：completed_at 到期、status 为 answered/cache_hit/failed，
// 且 billing_state 不在 pending/needs_review/usage_incomplete 中。不删除查询行，不改幂等键、
// 请求指纹、状态、usage、价格快照、预占与账本关联；pending、needs_review 与账务异常保留原内容。
// 按 500 行分批，直到不足一批或上下文结束；调用方应提供约 30 秒上限的上下文，
// 未提供时使用 30 秒兜底。
func (s *Service) RunCleanup(ctx context.Context) (CleanupResult, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cleanupJobTimeout)
		defer cancel()
	}
	cutoff := s.now().Add(-contentRetention)
	result := CleanupResult{}
	for {
		tag, err := s.pool.Exec(ctx, `UPDATE public.question_queries
			SET question_title = '', question_options = '', answer = NULL,
				content_expired = true, updated_at = now()
			WHERE id IN (
				SELECT id FROM public.question_queries
				WHERE completed_at < $1 AND NOT content_expired
					AND status IN ('answered', 'cache_hit', 'failed')
					AND billing_state NOT IN ('pending', 'needs_review', 'usage_incomplete')
				ORDER BY completed_at LIMIT $2)`, cutoff, cleanupBatch)
		if err != nil {
			return result, fault.New("retention_failed", "题库内容清理中断，请检查维护状态")
		}
		result.QuestionsSanitized += tag.RowsAffected()
		if tag.RowsAffected() < cleanupBatch {
			return result, nil
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
	}
}
