package learning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type courseSyncJob struct {
	ID           string
	UserID       string
	FenceToken   int64
	AttemptCount int
}

func (s *Service) claimCourseSync(ctx context.Context) (*courseSyncJob, error) {
	now := s.now()
	var job courseSyncJob
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `WITH candidate AS (
				SELECT id FROM public.learning_course_syncs
				WHERE (status = 'queued' AND (next_attempt_at IS NULL OR next_attempt_at <= $1))
				   OR (status = 'running' AND lease_expires_at <= $1)
				ORDER BY requested_at
				FOR UPDATE SKIP LOCKED
				LIMIT 1
			)
			UPDATE public.learning_course_syncs s
			SET status = 'running', attempt_count = s.attempt_count + 1,
			    fence_token = s.fence_token + 1, lease_owner = $2,
			    lease_expires_at = $3, started_at = COALESCE(s.started_at, $1), updated_at = $1
			FROM candidate WHERE s.id = candidate.id
			RETURNING s.id::text, s.user_id::text, s.fence_token, s.attempt_count`,
			now, s.opts.InstanceID, now.Add(s.opts.LeaseTTL)).
			Scan(&job.ID, &job.UserID, &job.FenceToken, &job.AttemptCount)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return errServiceUnavailable
		}
		return nil
	})
	if errors.Is(err, errNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// assertCourseSyncLease 续租并确认 fencing token 仍有效。
func (s *Service) assertCourseSyncLease(ctx context.Context, job *courseSyncJob) error {
	now := s.now()
	tag, err := s.pool.Exec(ctx, `UPDATE public.learning_course_syncs
		SET lease_expires_at = $3, updated_at = $4
		WHERE id = $1 AND fence_token = $2 AND status = 'running' AND lease_expires_at > $4`,
		job.ID, job.FenceToken, now.Add(s.opts.LeaseTTL), now)
	if err != nil {
		return errServiceUnavailable
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return nil
}

func (s *Service) finishCourseSync(ctx context.Context, job *courseSyncJob, status, errorCode string, courseCount int, snapshotVersion int64) error {
	now := s.now()
	tag, err := s.pool.Exec(ctx, `UPDATE public.learning_course_syncs
		SET status = $3, error_code = $4, course_count = $5, snapshot_version = $6,
		    lease_owner = '', lease_expires_at = NULL, finished_at = $7, updated_at = $7
		WHERE id = $1 AND fence_token = $2 AND status = 'running' AND lease_expires_at > $7`,
		job.ID, job.FenceToken, status, safeCode(errorCode), courseCount, snapshotVersion, now)
	if err != nil {
		return errServiceUnavailable
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "course.sync_finished", ResourceType: "course_sync", ResourceID: job.ID,
			UserID: job.UserID, Payload: map[string]any{"status": status, "error_code": safeCode(errorCode)},
		})
	})
}

func (s *Service) retryCourseSync(ctx context.Context, job *courseSyncJob, code string, at time.Time) error {
	now := s.now()
	tag, err := s.pool.Exec(ctx, `UPDATE public.learning_course_syncs
		SET status = 'queued', error_code = $3, next_attempt_at = $4,
		    lease_owner = '', lease_expires_at = NULL, updated_at = $5
		WHERE id = $1 AND fence_token = $2 AND status = 'running'`,
		job.ID, job.FenceToken, safeCode(code), at, now)
	if err != nil {
		return errServiceUnavailable
	}
	if tag.RowsAffected() != 1 {
		return errLeaseLost
	}
	return nil
}

// applyCourseSnapshot 在事务内写一次完整快照和规范任务；调用方已在事务外完成网络读取。
func (s *Service) applyCourseSnapshot(ctx context.Context, job *courseSyncJob, account accountRecord, snapshot PlatformCourseSnapshot) (int64, error) {
	now := s.now()
	cycle := snapshot.Course.CycleKey
	if cycle == "" {
		cycle = "current"
	}
	var version int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		// 写前确认租约。网络读取不持有此锁。
		var fence int64
		if err := tx.QueryRow(ctx, `SELECT fence_token FROM public.learning_course_syncs
			WHERE id = $1 FOR UPDATE`, job.ID).Scan(&fence); err != nil {
			return errServiceUnavailable
		}
		if fence != job.FenceToken {
			return errLeaseLost
		}
		var alive bool
		if err := tx.QueryRow(ctx, `SELECT lease_expires_at > $2 FROM public.learning_course_syncs
			WHERE id = $1`, job.ID, now).Scan(&alive); err != nil {
			return errServiceUnavailable
		}
		if !alive {
			return errLeaseLost
		}
		courseID := newID()
		err := tx.QueryRow(ctx, `INSERT INTO public.learning_courses
			(id, user_id, provider, identity_namespace, identity_fingerprint, external_course_id,
			 external_class_id, external_cpi, cycle_key, title, teacher, term, snapshot_version, synced_at, updated_at)
			VALUES ($1, $2, 'chaoxing', $3, $4, $5, $6, $7, $8, $9, $10, $11, 1, $12, $12)
			ON CONFLICT (user_id, provider, external_course_id, external_class_id, cycle_key)
			DO UPDATE SET title = EXCLUDED.title, teacher = EXCLUDED.teacher, term = EXCLUDED.term,
				external_cpi = EXCLUDED.external_cpi,
				snapshot_version = public.learning_courses.snapshot_version + 1,
				synced_at = EXCLUDED.synced_at, updated_at = EXCLUDED.updated_at,
				version = public.learning_courses.version + 1
			RETURNING id::text, snapshot_version`,
			courseID, account.UserID, account.Namespace, account.Fingerprint,
			snapshot.Course.ExternalCourseID, snapshot.Course.ExternalClassID, snapshot.Course.CPI, cycle,
			truncate(firstNonEmpty(snapshot.Title, snapshot.Course.ExternalCourseID), 200),
			truncate(snapshot.Teacher, 128), truncate(snapshot.Term, 64), now).
			Scan(&courseID, &version)
		if err != nil {
			return errServiceUnavailable
		}
		chapterCount := len(snapshot.Chapters)
		attachmentCount, taskPointCount, incompleteCount := 0, 0, 0
		chapterIDs := make(map[string]string, len(snapshot.Chapters))
		for position, chapter := range snapshot.Chapters {
			chapterID := newID()
			chapterIDs[chapter.ExternalID] = chapterID
			if _, err := tx.Exec(ctx, `INSERT INTO public.learning_chapters
				(id, course_id, user_id, snapshot_version, external_id, parent_external_id, title, sort_order)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				chapterID, courseID, account.UserID, version, chapter.ExternalID,
				truncate(chapter.ParentExternalID, 128), truncate(chapter.Title, 200), position); err != nil {
				return errServiceUnavailable
			}
			for index, attachment := range chapter.Attachments {
				attachmentCount++
				taskPoint := attachment.TaskPointID != ""
				if taskPoint {
					taskPointCount++
					if !attachment.Passed {
						incompleteCount++
					}
				}
				attachmentID := newID()
				played := attachment.PlayedSeconds
				if played < 0 {
					played = 0
				}
				_, err := tx.Exec(ctx, `INSERT INTO public.learning_attachments
					(id, course_id, chapter_id, user_id, snapshot_version, external_object_id, task_point_id,
					 kind, title, duration_seconds, played_seconds, platform_passed, passed_known,
					 is_task_point, counted_incomplete, sort_order, progress_checked_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
					attachmentID, courseID, chapterID, account.UserID, version, attachment.ObjectID,
					attachment.TaskPointID, truncate(attachment.Kind, 32), truncate(attachment.Title, 200),
					nonNegative(attachment.DurationSeconds), played, attachment.Passed, attachment.PassedKnown,
					taskPoint, taskPoint && !attachment.Passed, index, checkedAt(attachment.PassedKnown, now))
				if err != nil {
					return errServiceUnavailable
				}
				if !taskPoint {
					continue
				}
				check := map[string]any{
					"status": CheckUnconfirmed, "passed": false, "source": "course_snapshot",
					"checked_at": now, "play_time_seconds": played,
				}
				state, platformStatus, confirmedAt := StatePending, CheckUnconfirmed, any(nil)
				if attachment.PassedKnown && attachment.Passed {
					check["status"] = CheckConfirmed
					check["passed"] = true
					state, platformStatus, confirmedAt = StateConfirmed, CheckConfirmed, now
				}
				checkJSON, _ := json.Marshal(check)
				taskTitle := firstNonEmpty(attachment.Title, attachment.ObjectID)
				_, err = tx.Exec(ctx, `INSERT INTO public.learning_tasks
					(id, user_id, course_id, chapter_id, attachment_id, provider, identity_namespace,
					 identity_fingerprint, cycle_key, course_external_id, class_external_id, chapter_external_id,
					 task_point_id, task_kind, title, state, platform_status, platform_checked_at,
					 confirmed_at, platform_check, updated_at)
					VALUES ($1, $2, $3, $4, $5, 'chaoxing', $6, $7, $8, $9, $10, $11, $12, $13, $14,
					        $15, $16, $17, $18, $19, $17)
					ON CONFLICT (provider, identity_namespace, identity_fingerprint, course_external_id,
					             class_external_id, chapter_external_id, task_point_id, cycle_key)
					DO UPDATE SET course_id = EXCLUDED.course_id, chapter_id = EXCLUDED.chapter_id,
						attachment_id = EXCLUDED.attachment_id, title = EXCLUDED.title,
						task_kind = EXCLUDED.task_kind, updated_at = EXCLUDED.updated_at`,
					newID(), account.UserID, courseID, chapterID, attachmentID, account.Namespace,
					account.Fingerprint, cycle, snapshot.Course.ExternalCourseID, snapshot.Course.ExternalClassID,
					chapter.ExternalID, attachment.TaskPointID, truncate(attachment.Kind, 32), taskTitle,
					state, platformStatus, now, confirmedAt, checkJSON)
				if err != nil {
					return errServiceUnavailable
				}
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO public.learning_course_snapshots
			(id, course_id, user_id, version, captured_at, adapter, chapter_count, attachment_count,
			 task_point_count, incomplete_task_point_count, skipped_attachment_count)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			newID(), courseID, account.UserID, version, now, truncate(snapshot.Adapter, 64),
			chapterCount, attachmentCount, taskPointCount, incompleteCount, snapshot.SkippedAttachments)
		if err != nil {
			return errServiceUnavailable
		}
		if err := bindMatchingTasks(ctx, tx, account.UserID); err != nil {
			return err
		}
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "course.snapshot_created", ResourceType: "course", ResourceID: courseID,
			UserID: account.UserID, ResourceVersion: version,
			Payload: map[string]any{"snapshot_version": version, "task_point_count": taskPointCount,
				"incomplete_task_point_count": incompleteCount},
		})
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// bindMatchingTasks 将当前安排版本与用户现有任务建立关联。
// 关联不改变任务状态；任务是否可领取仍由当前版本和租约决定。
func bindMatchingTasks(ctx context.Context, tx pgx.Tx, userID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO public.learning_plan_task_bindings
		(plan_id, plan_revision_id, task_id, user_id)
		SELECT p.id, r.id, t.id, t.user_id
		FROM public.learning_plans p
		JOIN public.learning_plan_revisions r ON r.id = p.current_revision_id AND r.user_id = p.user_id
		JOIN public.learning_tasks t ON t.user_id = p.user_id
		WHERE p.user_id = $1 AND p.status = 'active'
		  AND t.course_id = ANY(r.course_ids)
		  AND (r.scope_kind = 'all' OR t.chapter_id = ANY(r.chapter_ids))
		  AND t.task_kind = ANY(r.task_types)
		ON CONFLICT DO NOTHING`, userID)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

func nonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func checkedAt(known bool, at time.Time) any {
	if !known {
		return nil
	}
	return at
}

func intString(value int) string { return fmt.Sprintf("%d", value) }
