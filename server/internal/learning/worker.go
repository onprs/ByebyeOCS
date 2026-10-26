package learning

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type executionJob struct {
	AttemptID            string
	TaskID               string
	UserID               string
	PlanRevisionID       string
	TaskKind             string
	Priority             int
	FenceToken           int64
	CourseExternal       string
	ClassExternal        string
	CPI                  string
	CycleKey             string
	ChapterExternal      string
	ObjectID             string
	CardIndex            int
	TaskPointID          string
	MediaDurationSeconds float64
	QuestionPolicy       QuestionPolicy
	// 已冻结的计费事实；重试时原样延续，避免重新报价或丢失预占。
	BillingState    string
	BillingHoldID   string
	BillingQuoteRaw json.RawMessage
	PriceVersion    string
}

type runningExecution struct {
	cancel context.CancelFunc
	job    executionJob
}

// Run 启动恢复、调度、核验、同步和租约维护循环，直到上下文取消。
// 多实例正确性依赖数据库约束、租约和 fencing，不依赖进程内锁。
func (s *Service) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return conflict("学习服务已经启动")
	}
	s.started = true
	s.closed = false
	runCtx, cancel := context.WithCancel(ctx)
	s.runCtx = runCtx
	s.mu.Unlock()
	defer cancel()

	// 启动时先处理旧租约与待核验任务，再开放领取。
	s.recoverOnce(runCtx)

	var wait sync.WaitGroup
	wait.Add(5)
	go func() { defer wait.Done(); s.dispatchLoop(runCtx) }()
	go func() { defer wait.Done(); s.verificationLoop(runCtx) }()
	// 两个同步消费者避免单个大课程账号长时间阻塞其他用户；平台请求仍统一限速。
	for range 2 {
		go func() { defer wait.Done(); s.syncLoop(runCtx) }()
	}
	go func() { defer wait.Done(); s.heartbeatLoop(runCtx) }()
	<-runCtx.Done()
	s.mu.Lock()
	s.closed = true
	for _, running := range s.running {
		running.cancel()
	}
	s.mu.Unlock()
	finished := make(chan struct{})
	// closed 在同一把锁内阻止新的 Add，随后才能等待所有执行协程结束。
	go func() { wait.Wait(); s.workers.Wait(); close(finished) }()
	timer := time.NewTimer(s.opts.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-finished:
		return nil
	case <-timer.C:
		return fault.New("shutdown_timeout", "学习服务退出等待超时")
	}
}

func (s *Service) localRunning() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

func (s *Service) dispatchLoop(ctx context.Context) {
	ticker := time.NewTicker(s.opts.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		s.recoverOnce(ctx)
		if !s.opts.ExecutionEnabled || s.opts.Executor == nil || s.opts.Sessions == nil {
			continue
		}
		capabilities := s.opts.Executor.Capabilities()
		if !capabilities.Enabled {
			continue
		}
		for range 4 {
			if s.localRunning() >= s.opts.GlobalConcurrency {
				break
			}
			job, err := s.claimNext(ctx)
			if err != nil {
				s.opts.Logger.Warn("领取学习任务失败", "code", describe(err))
				break
			}
			if job == nil {
				break
			}
			s.startExecution(ctx, *job)
		}
	}
}

type claimCandidate struct {
	id           string
	userID       string
	taskKind     string
	planPriority int
}

func (s *Service) claimNext(ctx context.Context) (*executionJob, error) {
	if !s.opts.ExecutionEnabled || s.opts.Executor == nil || s.opts.Sessions == nil {
		return nil, nil
	}
	var claimed *executionJob
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		// 领取事务串行化，保证全局/账号名额计数在同一时刻准确。
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(726239184004)`); err != nil {
			return errServiceUnavailable
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.learning_task_attempts
			WHERE ended_at IS NULL AND lease_expires_at > now()`).Scan(&active); err != nil {
			return errServiceUnavailable
		}
		if active >= s.opts.GlobalConcurrency {
			return nil
		}
		rows, err := tx.Query(ctx, `SELECT t.id::text, t.user_id::text, t.task_kind, matching_plan.priority
			FROM public.learning_tasks t
			JOIN public.learning_accounts a ON a.user_id = t.user_id
			LEFT JOIN (
				SELECT user_id, count(*) AS active FROM public.learning_task_attempts
				WHERE ended_at IS NULL AND lease_expires_at > now() GROUP BY user_id
			) occupancy ON occupancy.user_id = t.user_id
			JOIN public.learning_courses course ON course.id = t.course_id AND course.user_id = t.user_id
			JOIN public.learning_attachments att ON att.id = t.attachment_id
				AND att.user_id = t.user_id AND att.course_id = t.course_id
			JOIN LATERAL (
				SELECT max(r.priority) AS priority
				FROM public.learning_plan_task_bindings b
				JOIN public.learning_plans p ON p.id = b.plan_id AND p.user_id = b.user_id AND p.status = 'active'
				JOIN public.learning_plan_revisions r ON r.id = p.current_revision_id AND r.user_id = p.user_id
				WHERE b.task_id = t.id AND b.plan_revision_id = p.current_revision_id
				  -- 绑定是历史关联；领取前按当前版本重新核对课程、类型与选定章节的外部标识。
				  AND t.course_id = ANY(r.course_ids)
				  AND t.task_kind = ANY(r.task_types)
				  AND (r.scope_kind = 'all' OR EXISTS (
					SELECT 1 FROM public.learning_chapters sc
					WHERE sc.id = ANY(r.chapter_ids) AND sc.course_id = t.course_id
					  AND sc.user_id = t.user_id AND sc.external_id = t.chapter_external_id))
			) matching_plan ON matching_plan.priority IS NOT NULL
			WHERE t.state IN ('pending', 'failed')
			  AND t.desired_state = 'run'
			  AND t.lease_attempt_id IS NULL
			  AND t.stop_requested_at IS NULL
			  AND (t.next_attempt_at IS NULL OR t.next_attempt_at <= now())
			  AND t.failure_count < $1
			  AND COALESCE(occupancy.active, 0) < $2
			  AND a.blocked_code = ''
			  AND (a.blocked_until IS NULL OR a.blocked_until <= now())
			  -- 最新完整快照中已不存在的附件不能再领取。
			  AND att.snapshot_version = course.snapshot_version
			ORDER BY COALESCE(a.last_claimed_at, '1970-01-01'::timestamptz),
				matching_plan.priority DESC, COALESCE(t.next_attempt_at, t.created_at), t.created_at
			LIMIT 100`, s.opts.MaxAttempts, s.opts.AccountConcurrency)
		if err != nil {
			return errServiceUnavailable
		}
		candidates := make([]claimCandidate, 0, 100)
		for rows.Next() {
			var item claimCandidate
			if err := rows.Scan(&item.id, &item.userID, &item.taskKind, &item.planPriority); err != nil {
				rows.Close()
				return errServiceUnavailable
			}
			candidates = append(candidates, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return errServiceUnavailable
		}
		capabilities := s.opts.Executor.Capabilities()
		for _, candidate := range candidates {
			var userActive int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.learning_task_attempts
				WHERE user_id = $1 AND ended_at IS NULL AND lease_expires_at > now()`, candidate.userID).Scan(&userActive); err != nil {
				return errServiceUnavailable
			}
			if userActive >= s.opts.AccountConcurrency {
				continue
			}
			var job executionJob
			job.TaskID = candidate.id
			job.UserID = candidate.userID
			err := tx.QueryRow(ctx, `SELECT t.course_external_id, t.class_external_id, c.external_cpi,
				t.cycle_key, t.chapter_external_id, a.external_object_id, a.card_index, t.task_point_id,
				t.task_kind, a.duration_seconds,
				t.billing_state, t.billing_hold_id, t.billing_quote, COALESCE(t.price_version_id::text, '')
				FROM public.learning_tasks t
				JOIN public.learning_courses c ON c.id = t.course_id AND c.user_id = t.user_id
				JOIN public.learning_attachments a ON a.id = t.attachment_id AND a.user_id = t.user_id
				JOIN public.learning_accounts ac ON ac.user_id = t.user_id
				WHERE t.id = $1 AND t.state IN ('pending', 'failed') AND t.desired_state = 'run'
				  AND t.lease_attempt_id IS NULL AND (t.next_attempt_at IS NULL OR t.next_attempt_at <= now())
				  AND ac.blocked_code = '' AND (ac.blocked_until IS NULL OR ac.blocked_until <= now())
				  -- 二次加锁核对最新快照，避免候选读取后状态变化。
				  AND a.snapshot_version = c.snapshot_version
				FOR UPDATE OF t SKIP LOCKED`, candidate.id).Scan(
				&job.CourseExternal, &job.ClassExternal, &job.CPI, &job.CycleKey, &job.ChapterExternal,
				&job.ObjectID, &job.CardIndex, &job.TaskPointID, &job.TaskKind,
				&job.MediaDurationSeconds, &job.BillingState, &job.BillingHoldID, &job.BillingQuoteRaw,
				&job.PriceVersion)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return errServiceUnavailable
			}
			if !capabilities.Supports(candidate.taskKind) {
				if err := markTaskAttention(ctx, tx, job.TaskID, "executor_capability_missing", s.now()); err != nil {
					return err
				}
				continue
			}
			var policyModel *string
			if err := tx.QueryRow(ctx, `SELECT r.id::text, r.priority, r.question_policy_enabled,
				r.question_model_offering_id, r.question_mode, r.require_manual_confirmation
				FROM public.learning_plan_task_bindings b
				JOIN public.learning_tasks t ON t.id = b.task_id AND t.user_id = b.user_id
				JOIN public.learning_plans p ON p.id = b.plan_id AND p.user_id = b.user_id AND p.status = 'active'
				JOIN public.learning_plan_revisions r ON r.id = p.current_revision_id AND r.user_id = p.user_id
				WHERE b.task_id = $1 AND b.plan_revision_id = p.current_revision_id
				  -- 与候选领取相同：历史绑定必须仍符合当前课程、类型和稳定章节范围。
				  AND t.course_id = ANY(r.course_ids)
				  AND t.task_kind = ANY(r.task_types)
				  AND (r.scope_kind = 'all' OR EXISTS (
					SELECT 1 FROM public.learning_chapters sc
					WHERE sc.id = ANY(r.chapter_ids) AND sc.course_id = t.course_id
					  AND sc.user_id = t.user_id AND sc.external_id = t.chapter_external_id))
				-- 测验任务优先采用已启用题库策略的有效安排，不能让未启用策略的高优先级安排阻塞。
				ORDER BY CASE WHEN $2 = 'quiz' AND r.question_policy_enabled THEN 0 ELSE 1 END,
				         r.priority DESC, r.created_at DESC LIMIT 1`, job.TaskID, candidate.taskKind).Scan(
				&job.PlanRevisionID, &job.Priority, &job.QuestionPolicy.Enabled, &policyModel,
				&job.QuestionPolicy.Mode, &job.QuestionPolicy.RequireManualConfirmation); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				return errServiceUnavailable
			}
			if policyModel != nil {
				job.QuestionPolicy.ModelOfferingID = *policyModel
			}
			job.QuestionPolicy.RequireManualConfirmation = false
			if candidate.taskKind == "quiz" && !job.QuestionPolicy.Enabled {
				if err := markTaskAttention(ctx, tx, job.TaskID, "question_policy_disabled", s.now()); err != nil {
					return err
				}
				continue
			}
			now := s.now()
			billingState := "disabled"
			billingHold := ""
			billingQuote := []byte("{}")
			var priceVersion any
			state, hold, quoteJSON, storedPrice, billingCode := frozenClaimBilling(job)
			if billingCode != "" {
				if err := markTaskAttention(ctx, tx, job.TaskID, billingCode, now); err != nil {
					return err
				}
				continue
			}
			if len(job.BillingQuoteRaw) > 2 {
				// 任务已有冻结报价：重试不重新报价，也不覆盖原预占引用。
				billingState, billingHold, billingQuote, priceVersion = state, hold, quoteJSON, storedPrice
			} else if s.opts.Billing == nil {
				// 未接线账本也要冻结“未启用扣费”，避免后续接线后追溯报价已启动任务。
				billingQuote, _ = json.Marshal(BillingQuote{Configured: false, State: "disabled"})
			} else {
				var quote BillingQuote
				var reservedHold string
				billingCode := "billing_quote_failed"
				billingErr := withSavepoint(ctx, tx, func(sp pgx.Tx) error {
					parsed, err := s.opts.Billing.Quote(ctx, sp, job.UserID, job.TaskID, job.TaskKind)
					if err != nil {
						return err
					}
					if parsed.Configured && parsed.AmountMinor > 0 {
						billingCode = "billing_reserve_failed"
						reserved, err := s.opts.Billing.Reserve(ctx, sp, job.UserID, job.TaskID,
							"learning-task-"+job.TaskID, parsed)
						if err != nil {
							return err
						}
						reservedHold = reserved
					}
					quote = parsed
					return nil
				})
				if billingErr != nil {
					if err := markTaskAttention(ctx, tx, job.TaskID, billingCode, now); err != nil {
						return err
					}
					continue
				}
				billingState = billingQuoteState(quote)
				if quote.Configured && quote.AmountMinor > 0 {
					billingState = "reserved"
				}
				billingHold = reservedHold
				billingQuote, _ = json.Marshal(quote)
				if web.ValidID(quote.PriceVersion) {
					priceVersion = quote.PriceVersion
				}
			}
			job.AttemptID = newID()
			var attemptNo int
			err = tx.QueryRow(ctx, `UPDATE public.learning_tasks SET
				state = 'preparing', lease_attempt_id = $2, lease_owner = $3, lease_expires_at = $4,
				fence_token = fence_token + 1, attempt_count = attempt_count + 1,
				stop_requested_at = NULL, last_error_code = '', last_error_scope = '',
				billing_state = $5, billing_hold_id = $6, billing_quote = $7, billing_error = '',
				price_version_id = $8,
				version = version + 1, updated_at = $9
				WHERE id = $1 AND state IN ('pending', 'failed') AND desired_state = 'run'
				  AND lease_attempt_id IS NULL
				RETURNING fence_token, attempt_count`,
				job.TaskID, job.AttemptID, s.opts.InstanceID, now.Add(s.opts.LeaseTTL),
				billingState, billingHold, billingQuote, priceVersion, now).
				Scan(&job.FenceToken, &attemptNo)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return errServiceUnavailable
			}
			_, err = tx.Exec(ctx, `INSERT INTO public.learning_task_attempts
				(id, task_id, user_id, plan_revision_id, attempt_no, owner, fence_token, state,
				 lease_expires_at, started_at, heartbeat_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, 'preparing', $8, $9, $9)`,
				job.AttemptID, job.TaskID, job.UserID, job.PlanRevisionID, attemptNo,
				s.opts.InstanceID, job.FenceToken, now.Add(s.opts.LeaseTTL), now)
			if err != nil {
				return errServiceUnavailable
			}
			if _, err := tx.Exec(ctx, `UPDATE public.learning_accounts SET last_claimed_at = $2, updated_at = $2
				WHERE user_id = $1`, job.UserID, now); err != nil {
				return errServiceUnavailable
			}
			if err := writeOutbox(ctx, tx, outboxEvent{
				Topic: "task.claimed", ResourceType: "task", ResourceID: job.TaskID, UserID: job.UserID,
				Payload: map[string]any{"attempt_id": job.AttemptID, "state": StatePreparing},
			}); err != nil {
				return err
			}
			claimed = &job
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func markTaskAttention(ctx context.Context, tx pgx.Tx, taskID, code string, now time.Time) error {
	tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks
		SET state = 'needs_attention', last_error_code = $2, last_error_scope = 'task',
		    version = version + 1, updated_at = $3
		WHERE id = $1 AND state IN ('pending', 'failed')`, taskID, safeCode(code), now)
	if err != nil {
		return errServiceUnavailable
	}
	if tag.RowsAffected() == 1 {
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.state_changed", ResourceType: "task", ResourceID: taskID,
			Payload: map[string]any{"state": StateNeedsAttention, "error_code": safeCode(code)},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) startExecution(parent context.Context, job executionJob) {
	ctx, cancel := context.WithCancel(parent)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		return
	}
	s.running[job.AttemptID] = runningExecution{cancel: cancel, job: job}
	s.workers.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.workers.Done()
		defer func() {
			s.mu.Lock()
			delete(s.running, job.AttemptID)
			s.mu.Unlock()
			cancel()
		}()
		s.execute(ctx, job)
	}()
}

func (s *Service) execute(ctx context.Context, job executionJob) {
	capabilities := s.opts.Executor.Capabilities()
	sessionCtx, cancelSession := context.WithTimeout(ctx, 15*time.Second)
	session, err := s.resolveSession(sessionCtx, job.UserID)
	cancelSession()
	if err != nil {
		if s.runCtx != nil && s.runCtx.Err() != nil {
			return
		}
		finishCtx, cancelFinish := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFinish()
		s.finish(finishCtx, job, ExecutionResult{}, err, capabilities)
		return
	}
	preflightCompleted, preflightErr := s.preflight(ctx, job, session)
	if preflightErr != nil {
		if ctx.Err() != nil && s.runCtx != nil && s.runCtx.Err() != nil {
			return
		}
		finishCtx, cancelFinish := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelFinish()
		s.finish(finishCtx, job, ExecutionResult{}, preflightErr, capabilities)
		return
	}
	if preflightCompleted {
		return
	}
	var stopRequested bool
	_ = s.pool.QueryRow(ctx, `SELECT cancel_requested FROM public.learning_task_attempts WHERE id = $1`, job.AttemptID).Scan(&stopRequested)
	result, err := s.opts.Executor.Execute(ctx, ExecutionRequest{
		AttemptID: job.AttemptID, TaskID: job.TaskID, UserID: job.UserID, FenceToken: job.FenceToken,
		Session: session, TaskKind: job.TaskKind,
		Task: PlatformTaskRef{
			Course: PlatformCourseRef{ExternalCourseID: job.CourseExternal, ExternalClassID: job.ClassExternal,
				CPI: job.CPI, CycleKey: job.CycleKey},
			CardIndex: job.CardIndex, ChapterExternalID: job.ChapterExternal, ObjectID: job.ObjectID,
			TaskPointID: job.TaskPointID,
		},
		PlanRevision: job.PlanRevisionID, Priority: job.Priority, StopRequested: stopRequested,
		QuestionPolicy: job.QuestionPolicy, MediaDurationSeconds: job.MediaDurationSeconds,
	})
	if ctx.Err() != nil {
		// 服务停止时保留租约，由恢复循环先核验；控制取消则按停止确认写入。
		if s.runCtx != nil && s.runCtx.Err() != nil {
			return
		}
		if err == nil {
			result.StopAcknowledged = true
		} else {
			var failure *Failure
			if !errors.As(err, &failure) {
				err = Fail(FailureStopped, "执行已按停止请求中断")
			}
		}
	}
	if err == nil && !result.Completed && !result.StopAcknowledged {
		err = Fail(FailureTransient, "执行器未报告完成")
	}
	if result.Media.Available && !capabilities.ProducesMediaEvidence {
		// 未声明播放能力的执行器不能产生媒体记录。
		result.Media = unavailableMedia()
	}
	finishCtx, cancelFinish := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelFinish()
	s.finish(finishCtx, job, result, err, capabilities)
}

// preflight 在执行器之前做一次有限时只读平台查询。返回 true 表示平台已确认完成，
// 已走不收费确认路径，绝不能继续调用执行器。
func (s *Service) preflight(ctx context.Context, job executionJob, session []byte) (bool, error) {
	verifier := s.verifier()
	if verifier == nil {
		return false, Fail(FailureCapabilityMissing, "执行前平台核验器未配置")
	}
	checkCtx, cancel := context.WithTimeout(ctx, s.opts.PreflightTimeout)
	defer cancel()
	check, err := verifier.Verify(checkCtx, VerificationRequest{
		TaskID: job.TaskID, UserID: job.UserID, AttemptID: job.AttemptID, Session: session,
		Task: PlatformTaskRef{
			Course: PlatformCourseRef{ExternalCourseID: job.CourseExternal,
				ExternalClassID: job.ClassExternal, CPI: job.CPI, CycleKey: job.CycleKey},
			CardIndex: job.CardIndex, ChapterExternalID: job.ChapterExternal, ObjectID: job.ObjectID,
			TaskPointID: job.TaskPointID,
		},
	})
	if err != nil {
		if checkCtx.Err() != nil {
			return false, Fail(FailureTransient, "执行前平台核验超时")
		}
		return false, err
	}
	if check.Status == CheckConfirmed && check.Passed {
		if err := s.applyPreflightConfirmed(ctx, job, check); err != nil {
			return false, err
		}
		return true, nil
	}
	if check.Status == CheckUnconfirmed && !check.Passed {
		return false, nil
	}
	return false, Fail(FailureCapabilityMissing, "执行前平台核验结果不明确，已停止外部执行")
}

const preflightNoChargeReason = "pre_completed_platform_confirmed_no_charge"

// applyPreflightConfirmed 把执行前已完成的平台事实写入任务：
// 不调用执行器、不伪造媒体、释放本次预占、按冻结报价记录 Uncharged 用量事实。
func (s *Service) applyPreflightConfirmed(ctx context.Context, job executionJob, check PlatformCheck) error {
	now := s.now()
	if check.CheckedAt.IsZero() || check.CheckedAt.After(now) {
		check.CheckedAt = now
	}
	checkJSON := mustJSON(map[string]any{
		"status": check.Status, "passed": true, "checked_at": check.CheckedAt,
		"source": check.Source, "play_time_seconds": check.PlayTimeSeconds,
		"note": truncate(check.Note, 300),
	})
	return s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := s.loadTask(ctx, tx, job.TaskID, "", true)
		if err != nil {
			return err
		}
		var ended *time.Time
		var cancelRequested bool
		var attemptFence int64
		if err := tx.QueryRow(ctx, `SELECT ended_at, cancel_requested, fence_token
			FROM public.learning_task_attempts WHERE id = $1 AND task_id = $2 FOR UPDATE`,
			job.AttemptID, job.TaskID).Scan(&ended, &cancelRequested, &attemptFence); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errLeaseLost
			}
			return errServiceUnavailable
		}
		if ended != nil || record.LeaseAttempt == nil || *record.LeaseAttempt != job.AttemptID ||
			record.FenceToken != job.FenceToken || attemptFence != job.FenceToken {
			return errLeaseLost
		}
		var leaseAlive bool
		if err := tx.QueryRow(ctx, `SELECT lease_expires_at > now() FROM public.learning_task_attempts WHERE id = $1`,
			job.AttemptID).Scan(&leaseAlive); err != nil {
			return errServiceUnavailable
		}
		if !leaseAlive {
			return errLeaseLost
		}
		if record.State == StateConfirmed {
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
			state = 'confirmed', confirmed_at = $2, platform_status = 'confirmed',
			platform_checked_at = $2, platform_check = $3, observation = '{}'::jsonb,
			media_evidence = '{"available": false}'::jsonb,
			pre_completed_no_charge = true, confirmation_reason = $4,
			lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
			fence_token = fence_token + 1, next_verify_at = NULL, next_attempt_at = NULL,
			verification_reason = '', last_error_code = '', last_error_scope = '',
			billing_error = '', version = version + 1, updated_at = $5
			WHERE id = $1 AND state <> 'confirmed'`, job.TaskID, check.CheckedAt, checkJSON, preflightNoChargeReason, now)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return nil
		}
		if err := closeActiveAttempt(ctx, tx, job.TaskID, "superseded_confirmation", "pre_completed"); err != nil {
			return err
		}
		// 本次预占释放；hold id 保留作审计，app recorder 以 pre_completed_no_charge 判断不收费。
		if record.BillingHoldID != "" && s.opts.Billing != nil {
			releaseErr := releaseBilling(ctx, tx, s.opts.Billing, record.UserID, job.TaskID,
				record.BillingHoldID, "learning-task-"+job.TaskID+"-preflight-release",
				"执行前平台已完成，不收费")
			if releaseErr != nil {
				if _, updateErr := tx.Exec(ctx, `UPDATE public.learning_tasks SET
					billing_state = 'review_required', billing_error = 'pre_completed_release_failed'
					WHERE id = $1`, job.TaskID); updateErr != nil {
					return errServiceUnavailable
				}
			} else if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
				billing_state = 'released', billing_error = '' WHERE id = $1`, job.TaskID); err != nil {
				return errServiceUnavailable
			}
		}
		if s.opts.BillingConfirmationRecorder != nil {
			meteringErr := withSavepoint(ctx, tx, func(sp pgx.Tx) error {
				return s.opts.BillingConfirmationRecorder.RecordConfirmed(ctx, sp, record.UserID, job.TaskID)
			})
			if meteringErr != nil {
				if _, updateErr := tx.Exec(ctx, `UPDATE public.learning_tasks SET
					billing_state = 'review_required', billing_error = 'metering_failed'
					WHERE id = $1`, job.TaskID); updateErr != nil {
					return errServiceUnavailable
				}
			}
		}
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.confirmed", ResourceType: "task", ResourceID: job.TaskID,
			UserID: record.UserID, Payload: map[string]any{
				"state": StateConfirmed, "platform_status": CheckConfirmed,
				"confirmation_reason":     preflightNoChargeReason,
				"pre_completed_no_charge": true, "charged": false,
			},
		})
	})
}

func (s *Service) finish(ctx context.Context, job executionJob, result ExecutionResult, execErr error, capabilities ExecutorCapabilities) {
	if err := s.applyExecutionResult(ctx, job, result, execErr, capabilities); err != nil {
		s.opts.Logger.Warn("执行结果未写入", "task_id", job.TaskID, "code", describe(err))
	}
}

func (s *Service) applyExecutionResult(ctx context.Context, job executionJob, result ExecutionResult, execErr error, capabilities ExecutorCapabilities) error {
	if result.Media.Available && !capabilities.ProducesMediaEvidence {
		result.Media = unavailableMedia()
	}
	failure := (*Failure)(nil)
	if execErr != nil {
		failure = failureOf(execErr)
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := s.loadTask(ctx, tx, job.TaskID, "", true)
		if err != nil {
			return err
		}
		var cancelRequested bool
		var ended *time.Time
		var attemptFence int64
		err = tx.QueryRow(ctx, `SELECT cancel_requested, ended_at, fence_token
			FROM public.learning_task_attempts
			WHERE id = $1 AND task_id = $2 FOR UPDATE`, job.AttemptID, job.TaskID).
			Scan(&cancelRequested, &ended, &attemptFence)
		if errors.Is(err, pgx.ErrNoRows) {
			return errLeaseLost
		}
		if err != nil {
			return errServiceUnavailable
		}
		if ended != nil || record.LeaseAttempt == nil || *record.LeaseAttempt != job.AttemptID ||
			record.FenceToken != job.FenceToken || attemptFence != job.FenceToken {
			return errLeaseLost
		}
		var leaseAlive bool
		if err := tx.QueryRow(ctx, `SELECT lease_expires_at > now() FROM public.learning_task_attempts WHERE id = $1`,
			job.AttemptID).Scan(&leaseAlive); err != nil {
			return errServiceUnavailable
		}
		if !leaseAlive {
			return errLeaseLost
		}
		if failure == nil && result.StopAcknowledged {
			failure = Fail(FailureStopped, "执行器已确认停止")
		}
		now := s.now()
		observationJSON, mediaJSON := evidenceJSON(result)
		resultCode := "succeeded"
		attemptState := "ended"
		errorCode := ""
		if failure != nil {
			switch failure.Class {
			case FailureStopped, FailureCancelled:
				resultCode = "stopped"
			default:
				resultCode = "failed"
				errorCode = string(failure.Class)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE public.learning_task_attempts SET
			state = $2, ended_at = $3, result = $4, error_code = $5, observation = $6,
			media_evidence = $7, lease_expires_at = $3
			WHERE id = $1 AND ended_at IS NULL`,
			job.AttemptID, attemptState, now, resultCode, errorCode, observationJSON, mediaJSON); err != nil {
			return errServiceUnavailable
		}
		terminalBilling := false
		verificationReason := "execution_completed"
		if job.TaskKind == "quiz" {
			verificationReason = "question_submission"
		}
		switch {
		case failure == nil:
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'waiting_verification', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				stop_requested_at = NULL, observation = $2, media_evidence = $3,
				last_error_code = '', last_error_scope = '', next_attempt_at = NULL, next_verify_at = $4,
				verification_reason = $5, verification_attempts = 0,
				version = version + 1, updated_at = $4
				WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, now, verificationReason)
		case failure.Class == FailureSubmissionUnknown:
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'waiting_verification', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				stop_requested_at = NULL, observation = $2, media_evidence = $3,
				last_error_code = 'submission_unknown', last_error_scope = 'platform',
				next_attempt_at = NULL, next_verify_at = $4, verification_reason = 'question_submission',
				verification_attempts = 0, version = version + 1, updated_at = $4
				WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, now)
		case (failure.Class == FailureStopped || failure.Class == FailureCancelled) && cancelRequested &&
			record.DesiredState == DesiredRun:
			// 停止握手期间已恢复运行意图时保留原预占；已发送最终提交时只回到核验。
			var present, submitted bool
			if err := tx.QueryRow(ctx, `SELECT
				EXISTS (SELECT 1 FROM public.learning_attachments a
					JOIN public.learning_courses c ON c.id = a.course_id AND c.user_id = a.user_id
					WHERE a.id = t.attachment_id AND a.user_id = t.user_id
					  AND a.snapshot_version = c.snapshot_version),
				EXISTS (SELECT 1 FROM public.learning_work_runs w WHERE w.task_id = t.id
					AND w.state IN ('submitting', 'submitted', 'submission_unknown'))
				FROM public.learning_tasks t WHERE t.id = $1`, job.TaskID).Scan(&present, &submitted); err != nil {
				return errServiceUnavailable
			}
			target, code, reason := StateNeedsAttention, snapshotMissingCode, ""
			var nextVerify any
			if submitted {
				target, code, reason, nextVerify = StateWaitingVerification, "", "question_submission", now
			} else if present {
				// 停止握手结束且任务仍有效，沿用原报价及查询幂等键重新领取。
				target, code = StatePending, ""
			}
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = $2, lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				stop_requested_at = NULL, observation = $3, media_evidence = $4,
				last_error_code = $5, last_error_scope = CASE WHEN $5 = '' THEN '' ELSE 'task' END,
				next_attempt_at = NULL, next_verify_at = $6, verification_reason = $7,
				version = version + 1, updated_at = $8 WHERE id = $1`,
				job.TaskID, target, observationJSON, mediaJSON, code, nextVerify, reason, now)
		case failure.Class == FailureStopped || failure.Class == FailureCancelled:
			target := StatePaused
			desired := DesiredPause
			errorCode := "stopped"
			if record.DesiredState == DesiredCancel {
				target, desired = StateCancelled, DesiredCancel
			} else if record.DesiredState == DesiredRun {
				// 账号阻断导致执行器停止时保留用户运行意图，便于解除阻断后继续。
				desired = DesiredRun
				errorCode = "account_blocked"
			}
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = $2, desired_state = $3, lease_attempt_id = NULL, lease_owner = '',
				lease_expires_at = NULL, next_attempt_at = NULL, next_verify_at = NULL,
				stop_requested_at = NULL, observation = $4, media_evidence = $5, last_error_code = $6,
				last_error_scope = 'task', version = version + 1, updated_at = $7
				WHERE id = $1`, job.TaskID, target, desired, observationJSON, mediaJSON, errorCode, now)
			terminalBilling = true
		case failure.Class == FailureVerificationNeeded || failure.Class == FailureIdentityInvalid || failure.Class == FailureForbidden:
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'needs_attention', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				observation = $2, media_evidence = $3, last_error_code = $4, last_error_scope = 'account',
				version = version + 1, updated_at = $5
				WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, string(failure.Class), now)
			terminalBilling = true
			if err == nil {
				err = s.blockAccount(ctx, tx, record.UserID, string(failure.Class), "平台要求重新验证或拒绝访问", nil)
			}
		case failure.Class == FailureCapabilityMissing || failure.Class == FailureAnswerUnavailable:
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'needs_attention', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				observation = $2, media_evidence = $3, last_error_code = $5,
				last_error_scope = 'task', version = version + 1, updated_at = $4
				WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, now, string(failure.Class))
			terminalBilling = true
		case failure.Class == FailureInvalidTask || failure.Class == FailurePermanent:
			_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
				state = 'failed', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
				observation = $2, media_evidence = $3, last_error_code = $4, last_error_scope = 'task',
				failure_count = GREATEST(failure_count, $6), next_attempt_at = NULL,
				version = version + 1, updated_at = $5
				WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, string(failure.Class), now, s.opts.MaxAttempts)
			terminalBilling = true
		default:
			if record.DesiredState == DesiredCancel || record.DesiredState == DesiredPause {
				target := StatePaused
				if record.DesiredState == DesiredCancel {
					target = StateCancelled
				}
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					state = $2, lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
					next_attempt_at = NULL, next_verify_at = NULL, stop_requested_at = NULL,
					observation = $3, media_evidence = $4, last_error_code = $5,
					last_error_scope = 'task', version = version + 1, updated_at = $6
					WHERE id = $1`, job.TaskID, target, observationJSON, mediaJSON,
					string(failure.Class), now)
				terminalBilling = true
				break
			}
			failures := record.FailureCount + 1
			code := FailureTransient
			if failure.Class == FailureRateLimited {
				code = FailureRateLimited
			}
			delay := s.retryDelay(failures, failure)
			if failures >= s.opts.MaxAttempts {
				target := StateFailed
				if code == FailureRateLimited {
					target = StateNeedsAttention
				}
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					state = $2, lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
					observation = $3, media_evidence = $4, failure_count = $5,
					last_error_code = $6, last_error_scope = 'account', next_attempt_at = NULL,
					version = version + 1, updated_at = $7
					WHERE id = $1`, job.TaskID, target, observationJSON, mediaJSON, failures, string(code), now)
				terminalBilling = true
			} else {
				next := now.Add(delay)
				_, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET
					state = 'pending', lease_attempt_id = NULL, lease_owner = '', lease_expires_at = NULL,
					observation = $2, media_evidence = $3, failure_count = $4,
					last_error_code = $5, last_error_scope = 'account', next_attempt_at = $6,
					version = version + 1, updated_at = $7
					WHERE id = $1`, job.TaskID, observationJSON, mediaJSON, failures, string(code), next, now)
			}
			if code == FailureRateLimited && err == nil {
				next := now.Add(delay)
				_, err = tx.Exec(ctx, `UPDATE public.learning_accounts SET blocked_until = $2, updated_at = $3
					WHERE user_id = $1`, record.UserID, next, now)
			}
		}
		if err != nil {
			return errServiceUnavailable
		}
		if terminalBilling && record.BillingHoldID != "" {
			if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET billing_state = 'review_required',
				billing_error = 'attempt_stopped_or_failed' WHERE id = $1 AND billing_state IN ('quoted', 'reserved')`,
				job.TaskID); err != nil {
				return errServiceUnavailable
			}
		}
		return writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.attempt_finished", ResourceType: "task", ResourceID: job.TaskID, UserID: record.UserID,
			Payload: map[string]any{"attempt_id": job.AttemptID, "result": resultCode,
				"state": record.State, "confirmed": false},
		})
	})
}

func evidenceJSON(result ExecutionResult) ([]byte, []byte) {
	observation := result.Observation
	if observation.Source == "" && observation.Fraction == nil && observation.PositionSeconds == nil &&
		observation.DurationSeconds == nil && observation.ObservedAt.IsZero() {
		return []byte("{}"), mustJSON(unavailableMedia())
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = time.Now().UTC()
	}
	for _, field := range []*float64{observation.Fraction, observation.PositionSeconds, observation.DurationSeconds} {
		if field == nil {
			continue
		}
		if *field < 0 {
			*field = 0
		}
	}
	media := result.Media
	if !media.Available {
		media = unavailableMedia()
	}
	return mustJSON(observation), mustJSON(media)
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return []byte("{}")
	}
	return raw
}

// frozenClaimBilling 读取任务已持久化的报价与预占事实。重试必须沿用首次领取时
// 冻结的价格版本，不能因为改价、关闭收费或账本暂时不可用而重新报价或丢失预占。
func frozenClaimBilling(job executionJob) (state, hold string, quoteJSON []byte, priceVersion any, code string) {
	if len(job.BillingQuoteRaw) <= 2 {
		if job.BillingHoldID != "" {
			return "", "", nil, nil, "billing_hold_without_quote"
		}
		return "disabled", "", []byte("{}"), nil, ""
	}
	var quote BillingQuote
	if json.Unmarshal(job.BillingQuoteRaw, &quote) != nil || quote.AmountMinor < 0 {
		return "", "", nil, nil, "billing_quote_unreadable"
	}
	if job.PriceVersion != "" {
		priceVersion = job.PriceVersion
	}
	if quote.Configured && quote.AmountMinor > 0 {
		if job.BillingHoldID == "" {
			return "", "", nil, nil, "billing_hold_missing"
		}
		if job.BillingState == "released" {
			return "", "", nil, nil, "billing_hold_released"
		}
		return "reserved", job.BillingHoldID, job.BillingQuoteRaw, priceVersion, ""
	}
	if job.BillingHoldID != "" {
		return "", "", nil, nil, "billing_quote_inconsistent"
	}
	return billingQuoteState(quote), "", job.BillingQuoteRaw, priceVersion, ""
}

func billingQuoteState(quote BillingQuote) string {
	if quote.Configured {
		return "quoted"
	}
	if strings.TrimSpace(quote.State) == "disabled" {
		return "disabled"
	}
	return "unpriced"
}

func (s *Service) retryDelay(failures int, failure *Failure) time.Duration {
	if failures < 1 {
		failures = 1
	}
	base := s.opts.RetryBase
	if failure != nil && failure.RetryAfter > 0 {
		base = failure.RetryAfter
	}
	shift := failures - 1
	if shift > 4 {
		shift = 4
	}
	delay := base * time.Duration(1<<shift)
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	if delay < time.Second {
		delay = time.Second
	}
	return delay
}
