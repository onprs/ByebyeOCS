package learning

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// recoverOnce 处理过期租约与没有活跃尝试的执行状态。
// 旧执行器即使仍然存活，其回写也会被 fencing token 与租约条件拒绝。
func (s *Service) recoverOnce(ctx context.Context) {
	for range 20 {
		handled, err := s.recoverOneExpired(ctx)
		if err != nil {
			s.opts.Logger.Warn("恢复过期租约失败", "code", describe(err))
			return
		}
		if !handled {
			break
		}
	}
	for range 20 {
		handled, err := s.repairOrphanedTask(ctx)
		if err != nil {
			s.opts.Logger.Warn("修复孤立任务状态失败", "code", describe(err))
			return
		}
		if !handled {
			break
		}
	}
	for range 20 {
		handled, err := s.repairMissingSnapshotTask(ctx)
		if err != nil {
			s.opts.Logger.Warn("标记已消失快照任务失败", "code", describe(err))
			return
		}
		if !handled {
			break
		}
	}
}

// repairMissingSnapshotTask 处理同步之外重新进入待执行、但附件已不在最新
// 完整快照中的任务，避免它们静默停留。已有最终提交事实的任务只回到核验。
func (s *Service) repairMissingSnapshotTask(ctx context.Context) (bool, error) {
	handled := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		count, err := markStaleSnapshotTasks(ctx, tx, "", "", s.now())
		if err != nil {
			return err
		}
		handled = count > 0
		return nil
	})
	return handled, err
}

func (s *Service) recoverOneExpired(ctx context.Context) (bool, error) {
	handled := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var attemptID, taskID string
		err := tx.QueryRow(ctx, `SELECT a.id::text, a.task_id::text FROM public.learning_task_attempts a
			JOIN public.learning_tasks t ON t.id = a.task_id
			WHERE a.ended_at IS NULL AND a.lease_expires_at <= now()
			ORDER BY a.lease_expires_at LIMIT 1 FOR UPDATE OF t SKIP LOCKED`).Scan(&attemptID, &taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errServiceUnavailable
		}
		handled = true
		now := s.now()
		var endedCheck *time.Time
		if err := tx.QueryRow(ctx, `SELECT ended_at FROM public.learning_task_attempts
			WHERE id = $1 FOR UPDATE`, attemptID).Scan(&endedCheck); err != nil {
			return errServiceUnavailable
		}
		if endedCheck != nil {
			return nil
		}
		record, err := s.loadTask(ctx, tx, taskID, "", true)
		if err != nil {
			return err
		}
		superseded := record.State == StateConfirmed || record.State == StateCancelled ||
			record.LeaseAttempt == nil || *record.LeaseAttempt != attemptID
		result := "crashed"
		if superseded {
			result = "abandoned"
		}
		if _, err := tx.Exec(ctx, `UPDATE public.learning_task_attempts
			SET state = 'ended', ended_at = $2, result = $3, error_code = 'lease_expired',
			    lease_expires_at = $2
			WHERE id = $1 AND ended_at IS NULL`, attemptID, now, result); err != nil {
			return errServiceUnavailable
		}
		if superseded {
			_, err := tx.Exec(ctx, `UPDATE public.learning_tasks
				SET lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				    fence_token = fence_token + 1, updated_at = $2
				WHERE id = $1 AND lease_attempt_id = $3`, taskID, now, attemptID)
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
			state = 'waiting_verification', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
			fence_token = fence_token + 1, next_verify_at = $2,
			verification_reason = 'crash_recovery', verification_attempts = 0,
			last_error_code = 'lease_expired', last_error_scope = 'task',
			version = version + 1, updated_at = $2
			WHERE id = $1 AND state IN ('preparing', 'running')`, taskID, now)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.recovery_verification", ResourceType: "task", ResourceID: taskID,
			UserID: record.UserID, Payload: map[string]any{"state": StateWaitingVerification,
				"reason": "crash_recovery", "attempt_id": attemptID},
		})
	})
	return handled, err
}

func (s *Service) repairOrphanedTask(ctx context.Context) (bool, error) {
	handled := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var taskID, userID string
		err := tx.QueryRow(ctx, `SELECT t.id::text, t.user_id::text FROM public.learning_tasks t
			WHERE t.state IN ('preparing', 'running')
			  AND NOT EXISTS (SELECT 1 FROM public.learning_task_attempts a
				WHERE a.task_id = t.id AND a.ended_at IS NULL)
			ORDER BY t.updated_at LIMIT 1 FOR UPDATE OF t SKIP LOCKED`).Scan(&taskID, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errServiceUnavailable
		}
		handled = true
		now := s.now()
		tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
			state = 'waiting_verification', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
			fence_token = fence_token + 1, next_verify_at = $2,
			verification_reason = 'crash_recovery', verification_attempts = 0,
			last_error_code = 'orphaned_attempt', last_error_scope = 'task',
			version = version + 1, updated_at = $2
			WHERE id = $1 AND state IN ('preparing', 'running')`, taskID, now)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.recovery_verification", ResourceType: "task", ResourceID: taskID,
			UserID: userID, Payload: map[string]any{"state": StateWaitingVerification,
				"reason": "crash_recovery"},
		})
	})
	return handled, err
}

func (s *Service) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		extended, err := s.extendLeases(ctx)
		if err != nil {
			// 数据库不可写时立即停止本机外部副作用，等待租约过期后先核验。
			s.cancelAllExecutions(nil)
			s.opts.Logger.Warn("租约续期失败，已停止本机执行", "code", describe(err))
			continue
		}
		s.cancelAllExecutions(extended)
		s.cancelStoppedLeases(ctx)
	}
}

// extendLeases 续租并返回仍由本实例持有的尝试编号。
func (s *Service) extendLeases(ctx context.Context) (map[string]bool, error) {
	now := s.now()
	result := map[string]bool{}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH owned AS MATERIALIZED (
			SELECT t.id AS task_id, a.id AS attempt_id
			FROM public.learning_tasks t JOIN public.learning_task_attempts a
				ON a.id = t.lease_attempt_id AND a.task_id = t.id AND a.fence_token = t.fence_token
			WHERE a.owner = $1 AND a.ended_at IS NULL AND a.lease_expires_at > $3
			ORDER BY t.id FOR UPDATE OF t
		)
		UPDATE public.learning_task_attempts a
			SET lease_expires_at = $2, heartbeat_at = $3
			FROM owned WHERE a.id = owned.attempt_id
			RETURNING a.id::text, a.task_id::text`, s.opts.InstanceID, now.Add(s.opts.LeaseTTL), now)
		if err != nil {
			return errServiceUnavailable
		}
		taskIDs := make([]string, 0)
		for rows.Next() {
			var attemptID, taskID string
			if err := rows.Scan(&attemptID, &taskID); err != nil {
				rows.Close()
				return errServiceUnavailable
			}
			result[attemptID] = true
			taskIDs = append(taskIDs, taskID)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return errServiceUnavailable
		}
		if len(taskIDs) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks
				SET lease_expires_at = $2 WHERE id = ANY($1::uuid[])`, taskIDs, now.Add(s.opts.LeaseTTL)); err != nil {
				return errServiceUnavailable
			}
		}
		return nil
	})
	return result, err
}

// cancelAllExecutions 取消不在 extended 中的本机执行；nil 表示全部取消。
func (s *Service) cancelAllExecutions(extended map[string]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for attemptID, running := range s.running {
		if extended != nil && extended[attemptID] {
			continue
		}
		running.cancel()
	}
}

func (s *Service) cancelStoppedLeases(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM public.learning_task_attempts
		WHERE owner = $1 AND ended_at IS NULL AND cancel_requested = true`, s.opts.InstanceID)
	if err != nil {
		return
	}
	defer rows.Close()
	stop := map[string]bool{}
	for rows.Next() {
		var attemptID string
		if rows.Scan(&attemptID) == nil {
			stop[attemptID] = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for attemptID := range stop {
		if running, ok := s.running[attemptID]; ok {
			running.cancel()
		}
	}
}

func (s *Service) verifier() Verifier {
	if s.opts.Verifier != nil {
		return s.opts.Verifier
	}
	if s.opts.Platform == nil {
		return nil
	}
	if verifier, ok := s.opts.Platform.(Verifier); ok {
		return verifier
	}
	// Platform 接口本身承诺 CheckTask；仅实现 Platform 的受信适配器也应可用于核验。
	return platformVerifier{platform: s.opts.Platform}
}

type platformVerifier struct{ platform Platform }

func (p platformVerifier) Verify(ctx context.Context, request VerificationRequest) (PlatformCheck, error) {
	return p.platform.CheckTask(ctx, request.Session, request.Task)
}

func (s *Service) verificationLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.VerificationInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		verifier := s.verifier()
		if verifier == nil || s.opts.Sessions == nil {
			continue
		}
		for range 2 {
			taskID, userID, err := s.claimVerification(ctx)
			if err != nil {
				s.opts.Logger.Warn("领取核验任务失败", "code", describe(err))
				break
			}
			if taskID == "" {
				break
			}
			s.runVerification(ctx, verifier, taskID, userID)
		}
	}
}

func (s *Service) claimVerification(ctx context.Context) (string, string, error) {
	var taskID, userID string
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		now := s.now()
		err := tx.QueryRow(ctx, `UPDATE public.learning_tasks SET
			next_verify_at = $1, verification_attempts = verification_attempts + 1,
			version = version + 1, updated_at = $2
			WHERE id = (
				SELECT t.id FROM public.learning_tasks t
				JOIN public.users u ON u.id=t.user_id
				JOIN public.learning_accounts ac ON ac.user_id=t.user_id
				WHERE t.state = 'waiting_verification' AND t.desired_state='run'
				  AND u.status='active' AND ac.blocked_code=''
				  AND (ac.blocked_until IS NULL OR ac.blocked_until <= $2)
				  AND (t.next_verify_at IS NULL OR t.next_verify_at <= $2)
				ORDER BY t.next_verify_at NULLS FIRST, t.updated_at
				LIMIT 1 FOR UPDATE OF t SKIP LOCKED)
			RETURNING id::text, user_id::text`,
			now.Add(2*time.Minute), now).Scan(&taskID, &userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return errServiceUnavailable
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return taskID, userID, nil
}

func (s *Service) verificationAllowed(ctx context.Context, taskID, userID string) (bool, error) {
	var allowed bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM public.learning_tasks t
		JOIN public.users u ON u.id=t.user_id
		JOIN public.learning_accounts ac ON ac.user_id=t.user_id
		WHERE t.id=$1 AND t.user_id=$2 AND t.state='waiting_verification' AND t.desired_state='run'
		  AND u.status='active' AND ac.blocked_code=''
		  AND (ac.blocked_until IS NULL OR ac.blocked_until <= now()))`, taskID, userID).Scan(&allowed)
	return allowed, err
}

func (s *Service) runVerification(ctx context.Context, verifier Verifier, taskID, userID string) {
	verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx = verifyCtx
	if allowed, err := s.verificationAllowed(ctx, taskID, userID); err != nil || !allowed {
		return
	}
	record, err := s.loadTask(ctx, s.pool, taskID, userID, false)
	if err != nil {
		return
	}
	session, err := s.resolveSession(ctx, userID)
	if err != nil {
		_ = s.applyVerification(ctx, taskID, PlatformCheck{Status: CheckUnknown}, err)
		return
	}
	// 领取和会话读取之后也可能出现停止、闭户或平台冷却，外部读取前再次核对。
	if allowed, err := s.verificationAllowed(ctx, taskID, userID); err != nil || !allowed {
		return
	}
	check, err := verifier.Verify(verifyCtx, VerificationRequest{
		TaskID: taskID, UserID: userID, AttemptID: valueOrEmpty(record.LeaseAttempt),
		Session: session,
		Task: PlatformTaskRef{
			Course: PlatformCourseRef{ExternalCourseID: record.CourseExternal,
				ExternalClassID: record.ClassExternal, CPI: record.CPI, CycleKey: record.CycleKey},
			CardIndex: record.CardIndex, ChapterExternalID: record.ChapterExternal, ObjectID: record.ObjectID,
			TaskPointID: record.TaskPointID,
		},
		Verification: record.VerificationAttempts,
	})
	if err != nil {
		_ = s.applyVerification(ctx, taskID, PlatformCheck{Status: CheckUnknown}, err)
		return
	}
	_ = s.applyVerification(ctx, taskID, check, nil)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (s *Service) applyVerification(ctx context.Context, taskID string, check PlatformCheck, verifyErr error) error {
	now := s.now()
	if check.CheckedAt.IsZero() || check.CheckedAt.After(now) {
		check.CheckedAt = now
	}
	if check.Status == "" {
		check.Status = CheckUnknown
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := s.loadTask(ctx, tx, taskID, "", true)
		if err != nil {
			return err
		}
		checkJSON := mustJSON(map[string]any{
			"status": check.Status, "passed": check.Status == CheckConfirmed && check.Passed,
			"checked_at": check.CheckedAt, "source": check.Source,
			"play_time_seconds": check.PlayTimeSeconds, "note": truncate(check.Note, 300),
		})
		if record.State == StateCancelled {
			if check.Status == CheckConfirmed && check.Passed {
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					platform_status = 'confirmed', platform_checked_at = $2, platform_check = $3,
					version = version + 1, updated_at = $4
					WHERE id = $1`, taskID, check.CheckedAt, checkJSON, now)
				if err != nil {
					return errServiceUnavailable
				}
				if err := writeOutbox(ctx, tx, outboxEvent{
					Topic: "task.platform_confirmed_after_cancel", ResourceType: "task", ResourceID: taskID,
					UserID: record.UserID, Payload: map[string]any{"state": StateCancelled,
						"platform_status": CheckConfirmed},
				}); err != nil {
					return err
				}
				if record.BillingHoldID != "" {
					_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET billing_state = 'review_required',
						billing_error = 'confirmed_after_cancel' WHERE id = $1`, taskID)
				}
			}
			return err
		}
		if record.State == StateConfirmed {
			return nil
		}
		if check.Status == CheckConfirmed && check.Passed {
			tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'confirmed', confirmed_at = $2, platform_status = 'confirmed',
				platform_checked_at = $2, platform_check = $3, lease_attempt_id = NULL,
				lease_owner = '', lease_expires_at = NULL, fence_token = fence_token + 1,
				next_verify_at = NULL, next_attempt_at = NULL, verification_reason = '',
				last_error_code = '', last_error_scope = '', version = version + 1, updated_at = $4
				WHERE id = $1 AND state <> 'confirmed'`, taskID, check.CheckedAt, checkJSON, now)
			if err != nil {
				return errServiceUnavailable
			}
			if tag.RowsAffected() == 1 {
				if err := closeActiveAttempt(ctx, tx, taskID, "superseded_confirmation", "platform_confirmed"); err != nil {
					return err
				}
				meteringFailed := false
				if s.opts.BillingConfirmationRecorder != nil {
					meteringErr := withSavepoint(ctx, tx, func(sp pgx.Tx) error {
						return s.opts.BillingConfirmationRecorder.RecordConfirmed(ctx, sp, record.UserID, taskID)
					})
					if meteringErr != nil {
						meteringFailed = true
						// 用量事实 SQL 已回滚；平台确认仍提交，标记待人工复核。
						if _, updateErr := tx.Exec(ctx, `UPDATE public.learning_tasks SET
							billing_state = 'review_required', billing_error = $2 WHERE id = $1`,
							taskID, safeCode(describe(meteringErr))); updateErr != nil {
							return errServiceUnavailable
						}
					}
				}
				if !meteringFailed && record.BillingHoldID != "" &&
					(s.opts.Billing != nil || s.opts.BillingSettlementReporter != nil) {
					quote := BillingQuote{}
					_ = json.Unmarshal(record.BillingQuote, &quote)
					settlementState := ""
					var settleErr error
					if s.opts.BillingSettlementReporter != nil {
						settleErr = withSavepoint(ctx, tx, func(sp pgx.Tx) error {
							state, err := s.opts.BillingSettlementReporter.SettleState(ctx, sp,
								record.UserID, taskID, record.BillingHoldID,
								"learning-task-"+taskID+"-settle", quote.AmountMinor)
							if err != nil {
								return err
							}
							switch state {
							case "settled", "released", "needs_review":
								settlementState = state
								return nil
							default:
								return errors.New("结算状态未确认")
							}
						})
					} else {
						settleErr = withSavepoint(ctx, tx, func(sp pgx.Tx) error {
							return s.opts.Billing.Settle(ctx, sp, record.UserID, taskID, record.BillingHoldID,
								"learning-task-"+taskID+"-settle", quote.AmountMinor)
						})
						if settleErr == nil {
							settlementState = "settled"
						}
					}
					if settleErr != nil {
						// 保存点已回滚账本 SQL；父事务仍可提交平台确认和待核对状态。
						if _, updateErr := tx.Exec(ctx, `UPDATE public.learning_tasks SET
							billing_state = 'settlement_failed', billing_error = $2 WHERE id = $1`,
							taskID, safeCode(describe(settleErr))); updateErr != nil {
							return errServiceUnavailable
						}
					} else {
						switch settlementState {
						case "settled":
							if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
								billing_state = 'settled', billing_error = '' WHERE id = $1`, taskID); err != nil {
								return errServiceUnavailable
							}
						case "released":
							if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
								billing_state = 'released', billing_error = '' WHERE id = $1`, taskID); err != nil {
								return errServiceUnavailable
							}
						case "needs_review":
							if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
								billing_state = 'review_required', billing_error = 'needs_review'
								WHERE id = $1`, taskID); err != nil {
								return errServiceUnavailable
							}
						}
					}
				}
				if err := writeOutbox(ctx, tx, outboxEvent{
					Topic: "task.confirmed", ResourceType: "task", ResourceID: taskID, UserID: record.UserID,
					Payload: map[string]any{"state": StateConfirmed, "platform_status": CheckConfirmed},
				}); err != nil {
					return err
				}
			}
			return nil
		}
		if record.State != StateWaitingVerification {
			// 控制命令或重新调度已改变状态：只记录平台事实，不覆盖用户/管理员意图。
			status := CheckUnknown
			if check.Status == CheckUnconfirmed {
				status = CheckUnconfirmed
			}
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				platform_check = $2, platform_status = $3, platform_checked_at = $4,
				next_verify_at = NULL, version = version + 1, updated_at = $4
				WHERE id = $1`, taskID, checkJSON, status, now)
			return err
		}
		// 以下只记录平台事实，绝不凭执行观察或计时器确认。
		failureCount := record.FailureCount
		if check.Status == CheckUnconfirmed {
			failureCount++
		}
		errorCode := "platform_unconfirmed"
		if check.Status == CheckUnknown {
			errorCode = "verification_inconclusive"
		}
		verificationAttempts := record.VerificationAttempts
		if verifyErr != nil {
			failure := failureOf(verifyErr)
			errorCode = string(failure.Class)
			switch failure.Class {
			case FailureIdentityInvalid, FailureVerificationNeeded, FailureForbidden:
				if err := s.blockAccount(ctx, tx, record.UserID, string(failure.Class),
					"核验期间平台拒绝访问或要求重新验证", nil); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					state = 'needs_attention', platform_check = $2, platform_status = 'unknown',
					platform_checked_at = $3, last_error_code = $4, last_error_scope = 'account',
					next_verify_at = NULL, version = version + 1, updated_at = $3
					WHERE id = $1`, taskID, checkJSON, now, string(failure.Class))
				return err
			case FailureRateLimited:
				next := now.Add(s.retryDelay(verificationAttempts, failure))
				if _, err := tx.Exec(ctx, `UPDATE public.learning_accounts SET blocked_until = $2, updated_at = $3
					WHERE user_id = $1`, record.UserID, next, now); err != nil {
					return errServiceUnavailable
				}
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					platform_check = $2, platform_status = 'unknown', platform_checked_at = $3,
					last_error_code = 'rate_limited', next_verify_at = $4,
					version = version + 1, updated_at = $3 WHERE id = $1`, taskID, checkJSON, now, next)
				return err
			}
		}
		if check.Status == CheckUnconfirmed {
			if record.VerificationReason == "question_submission" {
				// 最终提交已经发起；平台尚未确认时只继续读取，不重新执行做题。
				target, code := StateWaitingVerification, "submission_unconfirmed"
				var next any = now.Add(s.retryDelay(verificationAttempts, nil))
				if verificationAttempts >= s.opts.MaxVerifications {
					target, next = StateNeedsAttention, nil
				}
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET state=$2,
					platform_check=$3,platform_status='unconfirmed',platform_checked_at=$4,
					last_error_code=$5,last_error_scope='platform',next_verify_at=$6,
					next_attempt_at=NULL,version=version+1,updated_at=$4 WHERE id=$1`,
					taskID, target, checkJSON, now, code, next)
				return err
			}
			if record.VerificationReason == "crash_recovery" {
				safeRecovery := s.opts.RecoveryRetry && s.opts.Executor != nil &&
					s.opts.Executor.Capabilities().SafeLeaseRecovery
				if !safeRecovery {
					_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
						state = 'needs_attention', platform_check = $2, platform_status = 'unconfirmed',
						platform_checked_at = $3, last_error_code = 'crash_recovery_requires_review',
						last_error_scope = 'task', next_verify_at = NULL,
						verification_reason = '', version = version + 1, updated_at = $3
						WHERE id = $1`, taskID, checkJSON, now)
					return err
				}
			}
			if failureCount >= s.opts.MaxAttempts {
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					state = 'failed', platform_check = $2, platform_status = 'unconfirmed',
					platform_checked_at = $3, failure_count = $4, last_error_code = 'platform_unconfirmed',
					last_error_scope = 'platform', next_verify_at = NULL, verification_reason = '',
					billing_state = CASE WHEN billing_hold_id <> '' THEN 'review_required' ELSE billing_state END,
					billing_error = CASE WHEN billing_hold_id <> '' THEN 'platform_unconfirmed' ELSE billing_error END,
					version = version + 1, updated_at = $3
					WHERE id = $1`, taskID, checkJSON, now, failureCount)
				return err
			}
			next := now.Add(s.retryDelay(failureCount, nil))
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'pending', platform_check = $2, platform_status = 'unconfirmed',
				platform_checked_at = $3, failure_count = $4, last_error_code = 'platform_unconfirmed',
				last_error_scope = 'platform', next_verify_at = NULL, next_attempt_at = $5,
				verification_reason = '', verification_attempts = 0,
				version = version + 1, updated_at = $3
				WHERE id = $1`, taskID, checkJSON, now, failureCount, next)
			return err
		}
		// 查询结果不明：限次退避，长期不明转人工，不重放执行。
		next := now.Add(s.retryDelay(verificationAttempts, nil))
		if verificationAttempts >= s.opts.MaxVerifications {
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'needs_attention', platform_check = $2, platform_status = 'unknown',
				platform_checked_at = $3, last_error_code = $4, last_error_scope = 'platform',
				next_verify_at = NULL, verification_reason = '',
				version = version + 1, updated_at = $3
				WHERE id = $1`, taskID, checkJSON, now, errorCode)
		} else {
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				platform_check = $2, platform_status = 'unknown', platform_checked_at = $3,
				last_error_code = $4, last_error_scope = 'platform', next_verify_at = $5,
				version = version + 1, updated_at = $3
				WHERE id = $1`, taskID, checkJSON, now, errorCode, next)
		}
		return err
	})
}

func (s *Service) syncLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * s.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if s.opts.Platform == nil {
			continue
		}
		job, err := s.claimCourseSync(ctx)
		if err != nil {
			s.opts.Logger.Warn("领取课程同步失败", "code", describe(err))
			continue
		}
		if job == nil {
			continue
		}
		s.runCourseSync(ctx, *job)
	}
}

type syncResult struct {
	status          string
	errorCode       string
	courseCount     int
	snapshotVersion int64
	blockAccount    bool
	retryable       bool
}

func (s *Service) runCourseSync(ctx context.Context, job courseSyncJob) {
	if err := s.assertCourseSyncLease(ctx, &job); err != nil {
		return
	}
	ctx, stopLease := s.courseSyncLeaseContext(ctx, job)
	defer stopLease()
	capabilities := s.opts.Platform.Capabilities()
	if !capabilities.CourseRead {
		s.finishSyncFailure(ctx, job, syncResult{status: "needs_attention", errorCode: "external_disabled"})
		return
	}
	if job.AttemptCount > s.opts.MaxAttempts+1 {
		s.finishSyncFailure(ctx, job, syncResult{status: "needs_attention", errorCode: "sync_attempts_exhausted"})
		return
	}
	session, err := s.resolveSession(ctx, job.UserID)
	if err != nil {
		s.finishSyncFailure(ctx, job, s.classifySyncError(err))
		return
	}
	s.opts.Logger.Info("课程同步会话已解析", "user_id", job.UserID, "bytes", len(session))
	if scoped, ok := s.opts.Platform.(ScopedSessionPlatform); ok {
		var cleanup func()
		ctx, cleanup, err = scoped.BeginSession(ctx, session)
		if err != nil {
			s.finishSyncFailure(ctx, job, s.classifySyncError(err))
			return
		}
		defer cleanup()
	}
	if err := s.assertCourseSyncLease(ctx, &job); err != nil {
		return
	}
	courses, err := s.opts.Platform.ListCourses(ctx, session)
	if err != nil {
		failure := failureOf(err)
		s.opts.Logger.Warn("课程同步读取失败", "user_id", job.UserID, "class", string(failure.Class), "detail", failure.Detail, "session_bytes", len(session))
		s.finishSyncFailure(ctx, job, s.classifySyncError(err))
		return
	}
	if len(courses) == 0 {
		if err := s.finishCourseSync(ctx, &job, "succeeded", "", 0, 0); err != nil {
			s.opts.Logger.Warn("空课程同步完成状态写入失败", "code", describe(err))
		}
		return
	}
	if s.opts.SyncBatch > 0 && len(courses) > s.opts.SyncBatch {
		// 不把被截断的目录记为成功；当前生产默认为 0，不额外限制课程数。
		s.finishSyncFailure(ctx, job, syncResult{status: "needs_attention", errorCode: "sync_batch_exceeded"})
		return
	}
	account, err := s.accountForUser(ctx, job.UserID)
	if err != nil {
		s.finishSyncFailure(ctx, job, syncResult{status: "needs_attention", errorCode: "identity_not_linked"})
		return
	}
	if err := s.saveCourseSummaries(ctx, &job, account, courses); err != nil {
		if !errors.Is(err, errLeaseLost) {
			failure := failureOf(err)
			s.opts.Logger.Warn("课程概要写入失败", "user_id", job.UserID, "class", string(failure.Class), "detail", failure.Detail)
			s.finishSyncFailure(ctx, job, s.classifySyncError(err))
		}
		return
	}
	completed, err := s.completedSyncCourses(ctx, job)
	if err != nil {
		s.opts.Logger.Warn("课程恢复进度读取失败", "user_id", job.UserID, "code", describe(err))
		s.finishSyncFailure(ctx, job, s.classifySyncError(err))
		return
	}
	var snapshotVersion int64
	processed := 0
	partial := 0
	transientPartial := 0
	for _, course := range courses {
		if version, ok := completed[course.ExternalCourseID+"/"+course.ExternalClassID]; ok {
			processed++
			snapshotVersion = max(snapshotVersion, version)
			continue
		}
		if err := s.assertCourseSyncLease(ctx, &job); err != nil {
			return
		}
		ref := PlatformCourseRef{
			ExternalCourseID: course.ExternalCourseID, ExternalClassID: course.ExternalClassID,
			CPI: course.CPI, CycleKey: "current",
		}
		started := s.now()
		snapshot, err := s.opts.Platform.DiscoverCourse(ctx, session, ref)
		if err != nil {
			failure := failureOf(err)
			s.opts.Logger.Warn("课程结构读取失败", "user_id", job.UserID, "course_index", processed, "class", string(failure.Class), "detail", failure.Detail)
			if failure.Class == FailureTransient {
				// 单门课程的瞬态失败不能终止整次同步；保留旧快照，继续处理其他课程。
				transientPartial++
				processed++
				if err := s.updateCourseSyncProgress(ctx, &job, processed); err != nil {
					return
				}
				continue
			}
			if failure.Class != FailureCapabilityMissing {
				s.finishSyncFailure(ctx, job, s.classifySyncError(err))
				return
			}
			// 保留已有完整快照，不用空快照覆盖个人进度和任务引用。
			partial++
			processed++
			if err := s.updateCourseSyncProgress(ctx, &job, processed); err != nil {
				return
			}
			continue
		}
		snapshot.Title = firstNonEmpty(snapshot.Title, course.Title)
		snapshot.Teacher = firstNonEmpty(snapshot.Teacher, course.Teacher)
		snapshot.Term = firstNonEmpty(snapshot.Term, course.Term)
		if snapshot.Adapter == "" {
			snapshot.Adapter = capabilities.Adapter
		}
		version, err := s.applyCourseSnapshot(ctx, &job, account, snapshot)
		if err != nil {
			if errors.Is(err, errLeaseLost) {
				return
			}
			failure := failureOf(err)
			s.opts.Logger.Warn("课程快照写入失败", "user_id", job.UserID, "course_index", processed,
				"class", string(failure.Class), "detail", failure.Detail)
			if failure.Class == FailureTransient {
				transientPartial++
				processed++
				if err := s.updateCourseSyncProgress(ctx, &job, processed); err != nil {
					return
				}
				continue
			}
			s.finishSyncFailure(ctx, job, s.classifySyncError(err))
			return
		}
		if version > snapshotVersion {
			snapshotVersion = version
		}
		processed++
		s.opts.Logger.Info("课程结构同步完成", "course_index", processed, "course_total", len(courses),
			"chapters", len(snapshot.Chapters), "elapsed_ms", s.now().Sub(started).Milliseconds())
		if err := s.updateCourseSyncProgress(ctx, &job, processed); err != nil {
			if errors.Is(err, errLeaseLost) {
				return
			}
			s.opts.Logger.Warn("课程同步进度写入失败", "user_id", job.UserID, "code", describe(err))
		}
	}
	status, errorCode := "succeeded", ""
	if partial > 0 || transientPartial > 0 {
		status = "needs_attention"
		errorCode = "course_structure_partial"
		if partial == 0 && transientPartial > 0 {
			errorCode = "course_transient_partial"
		}
		s.opts.Logger.Warn("课程同步部分完成", "user_id", job.UserID, "partial", partial,
			"transient", transientPartial, "total", len(courses))
	}
	if err := s.finishCourseSync(ctx, &job, status, errorCode, len(courses), snapshotVersion); err != nil {
		s.opts.Logger.Warn("课程同步完成状态写入失败", "code", describe(err))
	}
}

func (s *Service) accountForUser(ctx context.Context, userID string) (accountRecord, error) {
	var item accountRecord
	err := s.pool.QueryRow(ctx, `SELECT user_id::text, provider, identity_namespace, identity_fingerprint,
		school, display_name, blocked_code, blocked_reason, blocked_until, last_claimed_at, verified_at
		FROM public.learning_accounts WHERE user_id = $1`, userID).Scan(
		&item.UserID, &item.Provider, &item.Namespace, &item.Fingerprint, &item.School, &item.DisplayName,
		&item.BlockedCode, &item.BlockedReason, &item.BlockedUntil, &item.LastClaimedAt, &item.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return item, apiErr(409, "identity_not_linked", "账号尚未完成学习通身份绑定")
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

func (s *Service) classifySyncError(err error) syncResult {
	failure := failureOf(err)
	result := syncResult{status: "failed", errorCode: string(failure.Class)}
	switch failure.Class {
	case FailureIdentityInvalid, FailureVerificationNeeded, FailureForbidden:
		result.status = "needs_attention"
		result.blockAccount = true
	case FailureRateLimited, FailureTransient:
		result.status = "needs_attention"
		result.retryable = true
	case FailureCapabilityMissing:
		result.status = "needs_attention"
		result.errorCode = "external_disabled"
	}
	return result
}

func (s *Service) finishSyncFailure(ctx context.Context, job courseSyncJob, result syncResult) {
	if result.retryable && job.AttemptCount <= s.opts.MaxAttempts {
		delay := s.retryDelay(job.AttemptCount, nil)
		if err := s.retryCourseSync(ctx, &job, result.errorCode, s.now().Add(delay)); err != nil {
			s.opts.Logger.Warn("课程同步重试排队失败", "code", describe(err))
		}
		return
	}
	if result.blockAccount {
		_ = s.withTx(ctx, func(tx pgx.Tx) error {
			return s.blockAccount(ctx, tx, job.UserID, result.errorCode, "课程同步期间平台拒绝访问或要求重新验证", nil)
		})
	}
	if err := s.finishCourseSync(ctx, &job, result.status, result.errorCode, 0, 0); err != nil {
		s.opts.Logger.Warn("课程同步失败状态写入失败", "code", describe(err))
	}
}
