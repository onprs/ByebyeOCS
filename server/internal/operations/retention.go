package operations

import (
	"context"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// Cleanup 只清理已解决的临时内容，保留资金、去重和未解决业务事实。
func (s *Service) Cleanup(ctx context.Context) error {
	statements := []string{
		`UPDATE public.data_requests SET result_ciphertext=NULL,status='expired',updated_at=now() WHERE id IN (SELECT id FROM public.data_requests WHERE kind='export' AND expires_at<now() AND result_ciphertext IS NOT NULL LIMIT 500)`,
		`UPDATE public.data_requests SET status='pending',updated_at=now() WHERE id IN (SELECT id FROM public.data_requests WHERE kind='export' AND status='processing' AND updated_at<now()-interval '5 minutes' LIMIT 500)`,
		`DELETE FROM public.stream_entries WHERE (stream_key,sequence) IN (SELECT stream_key,sequence FROM public.stream_entries WHERE created_at<now()-interval '3 days' LIMIT 500)`,
		`DELETE FROM public.outbox_events WHERE id IN (SELECT id FROM public.outbox_events e WHERE published_at<now()-interval '7 days' AND NOT EXISTS (SELECT 1 FROM public.stream_entries s WHERE s.event_id=e.id) LIMIT 500)`,
		`DELETE FROM public.learning_outbox WHERE event_id IN (SELECT event_id FROM public.learning_outbox WHERE published_at<now()-interval '7 days' LIMIT 500)`,
		`DELETE FROM public.login_challenges WHERE id IN (SELECT id FROM public.login_challenges WHERE expires_at<now()-interval '24 hours' LIMIT 500)`,
		`DELETE FROM public.auth_rate_limits WHERE id IN (SELECT id FROM public.auth_rate_limits WHERE updated_at<now()-interval '24 hours' AND (blocked_until IS NULL OR blocked_until<now()) LIMIT 500)`,
		`DELETE FROM public.admin_sessions WHERE id IN (SELECT id FROM public.admin_sessions WHERE absolute_expires_at<now()-interval '30 days' OR revoked_at<now()-interval '30 days' LIMIT 500)`,
		`DELETE FROM public.client_sessions WHERE id IN (SELECT id FROM public.client_sessions WHERE absolute_expires_at<now()-interval '30 days' OR revoked_at<now()-interval '30 days' LIMIT 500)`,
		`UPDATE public.learning_task_attempts SET observation='{}'::jsonb WHERE id IN (SELECT id FROM public.learning_task_attempts WHERE ended_at<now()-interval '30 days' AND observation<>'{}'::jsonb LIMIT 500)`,
		`DELETE FROM public.platform_sessions WHERE id IN (SELECT id FROM public.platform_sessions WHERE expires_at<now()-interval '24 hours' OR (status<>'active' AND updated_at<now()-interval '24 hours') LIMIT 500)`,
		`DELETE FROM public.learning_course_snapshots WHERE id IN (SELECT s.id FROM public.learning_course_snapshots s JOIN public.learning_courses c ON c.id=s.course_id WHERE s.captured_at<now()-interval '7 days' AND s.version<c.snapshot_version LIMIT 500)`,
		`UPDATE public.question_queries SET question_title='',question_options='',answer=NULL,updated_at=now() WHERE id IN (SELECT id FROM public.question_queries WHERE completed_at<now()-interval '7 days' AND status IN ('answered','cache_hit','failed') AND billing_state NOT IN ('pending','needs_review','usage_incomplete') AND (question_title<>'' OR question_options<>'' OR answer IS NOT NULL) LIMIT 500)`,
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
	_, err := s.pool.Exec(ctx, `UPDATE public.stream_heads h SET earliest_sequence=COALESCE((SELECT min(e.sequence) FROM public.stream_entries e WHERE e.stream_key=h.stream_key),h.sequence+1)`)
	if err != nil {
		return fault.New("retention_failed", "无法更新事件保留范围")
	}
	return nil
}
