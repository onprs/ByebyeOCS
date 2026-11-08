package learning

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// requestChapterAvailabilityCheck 在任务控制事务内去重排队目录复查；不直接访问隐藏卡片。
func requestChapterAvailabilityCheck(ctx context.Context, tx pgx.Tx, userID string, actor Actor, now time.Time) error {
	id := newID()
	err := tx.QueryRow(ctx, `INSERT INTO public.learning_course_syncs
  (id,user_id,dedupe_key,status,requested_by_kind,requested_by_id,requested_at,updated_at)
  VALUES($1,$2,'all','queued',$3,$4,$5,$5) ON CONFLICT DO NOTHING RETURNING id::text`,
		id, userID, actor.Kind, actor.ID, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errServiceUnavailable
	}
	return writeOutbox(ctx, tx, outboxEvent{Topic: "course.sync_requested", ResourceType: "course_sync", ResourceID: id, UserID: userID, Payload: map[string]any{"status": "queued"}})
}
