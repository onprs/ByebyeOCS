package operations

import (
	"context"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// Cleanup 只清理已解决的临时内容，保留资金、去重和未解决业务事实。
func (s *Service) Cleanup(ctx context.Context) error {
	// 投递事件与各流最早可续传序号必须同批推进，先做这一步，已投递 Outbox 才能按引用确认删除。
	if err := s.trimStreamEntries(ctx); err != nil {
		return err
	}
	if s.options.QuestionCleanup != nil {
		if err := s.options.QuestionCleanup(ctx); err != nil {
			return fault.New("retention_failed", "题库内容清理中断，请检查维护状态")
		}
	}
	statements := []string{
		`UPDATE public.data_requests SET result_ciphertext=NULL,status='expired',updated_at=now() WHERE id IN (SELECT id FROM public.data_requests WHERE kind='export' AND expires_at<now() AND result_ciphertext IS NOT NULL LIMIT 500)`,
		`UPDATE public.data_requests SET status='pending',updated_at=now() WHERE id IN (SELECT id FROM public.data_requests WHERE kind='export' AND status='processing' AND updated_at<now()-interval '5 minutes' LIMIT 500)`,
		`DELETE FROM public.outbox_events WHERE id IN (SELECT id FROM public.outbox_events e WHERE published_at<now()-interval '7 days' AND NOT EXISTS (SELECT 1 FROM public.stream_entries s WHERE s.event_id=e.id) LIMIT 500)`,
		`DELETE FROM public.learning_outbox WHERE event_id IN (SELECT event_id FROM public.learning_outbox WHERE published_at<now()-interval '7 days' LIMIT 500)`,
		`DELETE FROM public.login_challenges WHERE id IN (SELECT id FROM public.login_challenges WHERE expires_at<now()-interval '24 hours' LIMIT 500)`,
		`DELETE FROM public.auth_rate_limits WHERE id IN (SELECT id FROM public.auth_rate_limits WHERE updated_at<now()-interval '24 hours' AND (blocked_until IS NULL OR blocked_until<now()) LIMIT 500)`,
		`DELETE FROM public.admin_sessions WHERE id IN (SELECT id FROM public.admin_sessions WHERE absolute_expires_at<now()-interval '30 days' OR revoked_at<now()-interval '30 days' LIMIT 500)`,
		`DELETE FROM public.client_sessions WHERE id IN (SELECT id FROM public.client_sessions WHERE absolute_expires_at<now()-interval '30 days' OR revoked_at<now()-interval '30 days' LIMIT 500)`,
		`UPDATE public.learning_task_attempts SET observation='{}'::jsonb WHERE id IN (SELECT id FROM public.learning_task_attempts WHERE ended_at<now()-interval '30 days' AND observation<>'{}'::jsonb LIMIT 500)`,
		`DELETE FROM public.platform_sessions WHERE id IN (SELECT id FROM public.platform_sessions WHERE expires_at<now()-interval '24 hours' OR (status<>'active' AND updated_at<now()-interval '24 hours') LIMIT 500)`,
		`DELETE FROM public.learning_course_snapshots WHERE id IN (SELECT s.id FROM public.learning_course_snapshots s JOIN public.learning_courses c ON c.id=s.course_id WHERE s.captured_at<now()-interval '7 days' AND s.version<c.snapshot_version LIMIT 500)`,
	}
	for _, statement := range statements {
		for range 50 {
			tag, err := s.pool.Exec(ctx, statement)
			if err != nil {
				return fault.New("retention_failed", "临时内容清理中断，请检查维护状态")
			}
			if tag.RowsAffected() < 500 {
				break
			}
		}
	}
	return nil
}

// trimStreamEntries 按批删除过期投递事件，并在同一事务推进对应流的最早可续传序号。
//
// 删除与推进分离时，旧游标会在两者之间被接受且查不到任何事件，客户端因此静默漏读；
// 合并后旧游标只会收到 resync_required。每批仍是一个独立事务，单批上限 500 行。
func (s *Service) trimStreamEntries(ctx context.Context) error {
	for range 50 {
		more, err := s.trimStreamEntriesBatch(ctx)
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	return nil
}

func (s *Service) trimStreamEntriesBatch(ctx context.Context) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fault.New("retention_failed", "无法开启事件清理事务")
	}
	defer rollbackTx(tx)
	rows, err := tx.Query(ctx, `DELETE FROM public.stream_entries WHERE (stream_key,sequence) IN (SELECT stream_key,sequence FROM public.stream_entries WHERE created_at<now()-interval '3 days' LIMIT 500) RETURNING stream_key`)
	if err != nil {
		return false, fault.New("retention_failed", "临时内容清理中断，请检查维护状态")
	}
	keys := make([]string, 0, 8)
	seen := map[string]bool{}
	removed := 0
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return false, fault.New("retention_failed", "事件清理结果格式无效")
		}
		removed++
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, fault.New("retention_failed", "临时内容清理中断，请检查维护状态")
	}
	if removed == 0 {
		return false, nil
	}
	// 单流无剩余事件时最早可续传序号为 sequence+1：已追平的游标仍可续传，更早的游标要求重取快照。
	if _, err := tx.Exec(ctx, `UPDATE public.stream_heads h SET earliest_sequence=GREATEST(h.earliest_sequence,
		COALESCE((SELECT min(e.sequence) FROM public.stream_entries e WHERE e.stream_key=h.stream_key),h.sequence+1))
		WHERE h.stream_key=ANY($1::text[])`, keys); err != nil {
		return false, fault.New("retention_failed", "无法更新事件保留范围")
	}
	if tx.Commit(ctx) != nil {
		return false, fault.New("retention_failed", "事件清理结果待核对")
	}
	return removed == 500, nil
}
