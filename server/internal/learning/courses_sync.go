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
		seenChapterExternal := make(map[string]bool, len(snapshot.Chapters))
		for position, chapter := range snapshot.Chapters {
			if chapter.ExternalID == "" || seenChapterExternal[chapter.ExternalID] {
				return Fail(FailureInvalidTask, "课程快照章节标识缺失或重复")
			}
			seenChapterExternal[chapter.ExternalID] = true
			// 章节行以账号内稳定外部章节标识复用，避免每次同步重生成 UUID
			// 让“选定章节”安排和历史关联失效。快照版本只表示最近一次出现。
			var chapterID string
			err := tx.QueryRow(ctx, `SELECT id::text FROM public.learning_chapters
				WHERE course_id = $1 AND user_id = $2 AND external_id = $3
				ORDER BY snapshot_version DESC LIMIT 1 FOR UPDATE`,
				courseID, account.UserID, chapter.ExternalID).Scan(&chapterID)
			switch {
			case err == nil:
				_, err = tx.Exec(ctx, `UPDATE public.learning_chapters
					SET snapshot_version = $3, parent_external_id = $4, title = $5, sort_order = $6
					WHERE id = $1 AND user_id = $2`,
					chapterID, account.UserID, version, truncate(chapter.ParentExternalID, 128),
					truncate(chapter.Title, 200), position)
			case errors.Is(err, pgx.ErrNoRows):
				chapterID = newID()
				_, err = tx.Exec(ctx, `INSERT INTO public.learning_chapters
					(id, course_id, user_id, snapshot_version, external_id, parent_external_id, title, sort_order)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
					chapterID, courseID, account.UserID, version, chapter.ExternalID,
					truncate(chapter.ParentExternalID, 128), truncate(chapter.Title, 200), position)
			}
			if err != nil {
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
				if attachment.CardIndex < 0 || attachment.CardIndex > 31 {
					return Fail(FailureInvalidTask, "课程快照附件卡片页码无效")
				}
				_, err := tx.Exec(ctx, `INSERT INTO public.learning_attachments
					(id, course_id, chapter_id, user_id, snapshot_version, external_object_id, task_point_id,
					 kind, title, duration_seconds, played_seconds, platform_passed, passed_known,
					 is_task_point, counted_incomplete, sort_order, card_index, progress_checked_at)
					VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
					attachmentID, courseID, chapterID, account.UserID, version, attachment.ObjectID,
					attachment.TaskPointID, truncate(attachment.Kind, 32), truncate(attachment.Title, 200),
					nonNegative(attachment.DurationSeconds), played, attachment.Passed, attachment.PassedKnown,
					taskPoint, taskPoint && !attachment.Passed, index, attachment.CardIndex,
					checkedAt(attachment.PassedKnown, now))
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
		if err := requestStaleSnapshotStops(ctx, tx, account.UserID, courseID, now); err != nil {
			return err
		}
		// 最新快照已不存在的未运行任务先转人工；随后把重新出现在快照中的
		// 同类任务恢复为待执行，保留用户明确的暂停/取消意图。
		if _, err := restoreSnapshotTasks(ctx, tx, account.UserID, courseID, version, now); err != nil {
			return err
		}
		for {
			marked, err := markStaleSnapshotTasks(ctx, tx, account.UserID, courseID, now)
			if err != nil {
				return err
			}
			if marked < 50 {
				break
			}
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
		  AND (r.scope_kind = 'all' OR EXISTS (
			SELECT 1 FROM public.learning_chapters sc
			WHERE sc.id = ANY(r.chapter_ids) AND sc.course_id = t.course_id
			  AND sc.user_id = t.user_id AND sc.external_id = t.chapter_external_id))
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

const snapshotMissingCode = "snapshot_task_missing"

// restoreSnapshotTasks 把重新出现在最新完整快照中的任务从本模块造成的
// needs_attention 恢复为待执行。只处理 desired_state=run 的任务。
func restoreSnapshotTasks(ctx context.Context, tx pgx.Tx, userID, courseID string, version int64, now time.Time) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE public.learning_tasks t SET
			state = 'pending', last_error_code = '', last_error_scope = '',
			failure_count = 0, verification_attempts = 0, verification_reason = '',
			next_attempt_at = NULL, next_verify_at = NULL, version = version + 1, updated_at = $4
		FROM public.learning_attachments a
		WHERE t.user_id = $1 AND t.course_id = $2 AND a.id = t.attachment_id
		  AND a.user_id = t.user_id AND a.course_id = t.course_id AND a.snapshot_version = $3
		  AND t.state = 'needs_attention' AND t.last_error_code = $5
		  AND t.desired_state = 'run'
		RETURNING t.id::text`, userID, courseID, version, now, snapshotMissingCode)
	if err != nil {
		return 0, errServiceUnavailable
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, errServiceUnavailable
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, errServiceUnavailable
	}
	for _, id := range ids {
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.state_changed", ResourceType: "task", ResourceID: id, UserID: userID,
			Payload: map[string]any{"state": StatePending, "reason": snapshotMissingCode + "_resolved"},
		}); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// markStaleSnapshotTasks 把附件已不在课程最新完整快照中的待执行任务转为人工。
// 终态失败保留原分类；已有最终提交事实的测验只回到核验，不重新执行、不取消资金事实。
func markStaleSnapshotTasks(ctx context.Context, tx pgx.Tx, userID, courseID string, now time.Time) (int, error) {
	rows, err := tx.Query(ctx, `WITH candidates AS (
			SELECT t.id, t.user_id,
			       EXISTS (SELECT 1 FROM public.learning_work_runs w
			               WHERE w.task_id = t.id
			                 AND w.state IN ('submitting', 'submitted', 'submission_unknown')) AS has_submission
			FROM public.learning_tasks t
			JOIN public.learning_courses c ON c.id = t.course_id AND c.user_id = t.user_id
			JOIN public.learning_attachments a ON a.id = t.attachment_id
				AND a.user_id = t.user_id AND a.course_id = t.course_id
			WHERE ($1::uuid IS NULL OR t.user_id = $1::uuid)
			  AND ($2::uuid IS NULL OR t.course_id = $2::uuid)
			  AND t.state = 'pending' AND t.desired_state = 'run'
			  AND a.snapshot_version <> c.snapshot_version
			ORDER BY t.updated_at
			LIMIT 50
			FOR UPDATE OF t SKIP LOCKED
		)
		UPDATE public.learning_tasks t SET
			state = CASE WHEN candidates.has_submission THEN 'waiting_verification' ELSE 'needs_attention' END,
			last_error_code = CASE WHEN candidates.has_submission THEN '' ELSE $3::text END,
			last_error_scope = CASE WHEN candidates.has_submission THEN '' ELSE 'task' END,
			next_attempt_at = NULL,
			next_verify_at = CASE WHEN candidates.has_submission THEN $4::timestamptz ELSE NULL END,
			verification_reason = CASE WHEN candidates.has_submission THEN 'question_submission' ELSE '' END,
			version = version + 1, updated_at = $4::timestamptz
		FROM candidates WHERE t.id = candidates.id
		RETURNING t.id::text, t.user_id::text, t.state, candidates.has_submission`,
		nullableID(userID), nullableID(courseID), snapshotMissingCode, now)
	if err != nil {
		return 0, errServiceUnavailable
	}
	type markedTask struct {
		id, userID, state string
		hasSubmission     bool
	}
	marked := make([]markedTask, 0)
	for rows.Next() {
		var item markedTask
		if err := rows.Scan(&item.id, &item.userID, &item.state, &item.hasSubmission); err != nil {
			rows.Close()
			return 0, errServiceUnavailable
		}
		marked = append(marked, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, errServiceUnavailable
	}
	for _, item := range marked {
		event := outboxEvent{Topic: "task.state_changed", ResourceType: "task",
			ResourceID: item.id, UserID: item.userID,
			Payload: map[string]any{"state": item.state, "error_code": snapshotMissingCode}}
		if item.hasSubmission {
			event.Topic = "task.recovery_verification"
			event.Payload = map[string]any{"state": StateWaitingVerification, "reason": snapshotMissingCode}
		}
		if err := writeOutbox(ctx, tx, event); err != nil {
			return 0, err
		}
	}
	return len(marked), nil
}
