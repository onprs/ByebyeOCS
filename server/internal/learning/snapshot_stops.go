package learning

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// requestStaleSnapshotStops 通过现有停止握手终止已从完整快照消失的在途任务。
// 保留运行意图、租约与财务事实，等待执行器实际停止后再确定任务状态。
func requestStaleSnapshotStops(ctx context.Context, tx pgx.Tx, userID, courseID string, now time.Time) error {
	rows, err := tx.Query(ctx, `WITH stale AS (
		UPDATE public.learning_tasks t SET last_error_code = $3, last_error_scope = 'task',
			stop_requested_at = COALESCE(t.stop_requested_at, $4), version = t.version + 1, updated_at = $4
		FROM public.learning_attachments a, public.learning_courses c
		WHERE t.user_id = $1 AND t.course_id = $2
		  AND a.id = t.attachment_id AND a.user_id = t.user_id
		  AND c.id = t.course_id AND c.user_id = t.user_id
		  AND a.snapshot_version <> c.snapshot_version
		  AND t.state IN ('preparing', 'running') AND t.desired_state = 'run'
		  AND t.lease_attempt_id IS NOT NULL AND t.last_error_code <> $3
		RETURNING t.id, t.user_id, t.state, t.lease_attempt_id
	), stopped AS (
		UPDATE public.learning_task_attempts a SET cancel_requested = true
		FROM stale WHERE a.id = stale.lease_attempt_id AND a.ended_at IS NULL
		RETURNING a.id
	)
	SELECT id::text, user_id::text, state FROM stale`, userID, courseID, snapshotMissingCode, now)
	if err != nil {
		return errServiceUnavailable
	}
	type task struct{ id, user, state string }
	var items []task
	for rows.Next() {
		var item task
		if err := rows.Scan(&item.id, &item.user, &item.state); err != nil {
			rows.Close()
			return errServiceUnavailable
		}
		items = append(items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return errServiceUnavailable
	}
	for _, item := range items {
		if err := writeOutbox(ctx, tx, outboxEvent{Topic: "task.state_changed", ResourceType: "task", ResourceID: item.id, UserID: item.user,
			Payload: map[string]any{"state": item.state, "stop_requested": true, "error_code": snapshotMissingCode}}); err != nil {
			return err
		}
	}
	return nil
}
