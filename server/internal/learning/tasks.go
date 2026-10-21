package learning

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

var validStates = map[string]bool{
	StatePending: true, StatePreparing: true, StateRunning: true, StateWaitingVerification: true,
	StateConfirmed: true, StatePaused: true, StateNeedsAttention: true, StateFailed: true, StateCancelled: true,
}

type PlatformFactView struct {
	Status          string     `json:"status"`
	Passed          bool       `json:"passed"`
	CheckedAt       *time.Time `json:"checked_at,omitempty"`
	Source          string     `json:"source,omitempty"`
	PlayTimeSeconds *float64   `json:"play_time_seconds,omitempty"`
	Note            string     `json:"note,omitempty"`
}

type BillingView struct {
	State             string `json:"state"`
	Configured        bool   `json:"configured"`
	AmountKind        string `json:"amount_kind"`
	QuotedAmountMinor string `json:"quoted_amount_minor"`
	Currency          string `json:"currency,omitempty"`
	PriceVersion      string `json:"price_version,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
}

type Estimate struct {
	RemainingSecondsMin *int `json:"remaining_seconds_min"`
	RemainingSecondsMax *int `json:"remaining_seconds_max"`
	Estimated           bool `json:"estimated"`
}

type TaskPlanRef struct {
	PlanID     string `json:"plan_id"`
	RevisionID string `json:"revision_id"`
	Version    int64  `json:"version"`
}

type TaskSummary struct {
	ID                   string           `json:"id"`
	UserID               string           `json:"user_id,omitempty"`
	State                string           `json:"state"`
	DesiredState         string           `json:"desired_state"`
	Version              int64            `json:"version"`
	Priority             int              `json:"priority"`
	TaskKind             string           `json:"task_kind"`
	Title                string           `json:"title"`
	TaskPointID          string           `json:"task_point_id"`
	CourseID             string           `json:"course_id"`
	CourseTitle          string           `json:"course_title"`
	ChapterID            string           `json:"chapter_id"`
	ChapterTitle         string           `json:"chapter_title"`
	PlanRefs             []TaskPlanRef    `json:"plan_refs"`
	AttemptCount         int              `json:"attempt_count"`
	FailureCount         int              `json:"failure_count"`
	VerificationAttempts int              `json:"verification_attempts"`
	LastErrorCode        string           `json:"last_error_code,omitempty"`
	ConfirmationReason   string           `json:"confirmation_reason,omitempty"`
	PreCompletedNoCharge bool             `json:"pre_completed_no_charge"`
	NextAttemptAt        *time.Time       `json:"next_attempt_at,omitempty"`
	ConfirmedAt          *time.Time       `json:"confirmed_at,omitempty"`
	Platform             PlatformFactView `json:"platform"`
	Observation          *Observation     `json:"observation,omitempty"`
	MediaEvidence        MediaEvidence    `json:"media_evidence"`
	Billing              BillingView      `json:"billing"`
	Estimate             Estimate         `json:"estimate"`
	CreatedAt            time.Time        `json:"created_at"`
	UpdatedAt            time.Time        `json:"updated_at"`
}

type AttemptView struct {
	ID              string           `json:"id"`
	AttemptNo       int              `json:"attempt_no"`
	State           string           `json:"state"`
	FenceToken      int64            `json:"fence_token"`
	PlanRevisionID  string           `json:"plan_revision_id,omitempty"`
	StartedAt       time.Time        `json:"started_at"`
	HeartbeatAt     time.Time        `json:"heartbeat_at"`
	EndedAt         *time.Time       `json:"ended_at,omitempty"`
	CancelRequested bool             `json:"cancel_requested"`
	Result          string           `json:"result,omitempty"`
	ErrorCode       string           `json:"error_code,omitempty"`
	Observation     *Observation     `json:"observation,omitempty"`
	Platform        PlatformFactView `json:"platform"`
	MediaEvidence   MediaEvidence    `json:"media_evidence"`
}

type CommandView struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	ResultCode string     `json:"result_code,omitempty"`
	ActorKind  string     `json:"actor_kind"`
	Reason     string     `json:"reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	AppliedAt  *time.Time `json:"applied_at,omitempty"`
}

type TaskDetail struct {
	TaskSummary
	Attempts []AttemptView `json:"attempts"`
	Commands []CommandView `json:"commands"`
}

type TaskFilter struct {
	States   []string
	CourseID string
	PlanID   string
	UserID   string // 仅管理员接口使用
	Limit    int
	Cursor   string
}

type taskRecord struct {
	TaskSummary
	UserID             string
	CourseExternal     string
	ClassExternal      string
	ChapterExternal    string
	CycleKey           string
	CPI                string
	ObjectID           string
	VerificationReason string
	Desired            string
	FenceToken         int64
	LeaseAttempt       *string
	BillingHoldID      string
	BillingQuote       json.RawMessage
	ObservationRaw     json.RawMessage
	PlatformRaw        json.RawMessage
	MediaRaw           json.RawMessage
}

const taskSelect = `SELECT t.id::text, t.user_id::text, t.state, t.desired_state, t.version, t.priority,
	t.task_kind, t.title, t.task_point_id, t.course_id::text, c.title, t.chapter_id::text,
	ch.title, t.created_at, t.updated_at, t.attempt_count, t.failure_count, t.verification_attempts,
	t.last_error_code, t.next_attempt_at, t.confirmed_at, t.platform_status, t.platform_checked_at,
	t.observation, t.platform_check, t.media_evidence, t.lease_attempt_id::text, t.billing_state,
	t.billing_quote, t.billing_hold_id, t.billing_error, t.course_external_id, t.chapter_external_id,
	t.cycle_key, c.external_cpi, a.external_object_id, t.fence_token, t.class_external_id,
	t.verification_reason, t.pre_completed_no_charge, t.confirmation_reason
	FROM public.learning_tasks t
	JOIN public.learning_courses c ON c.id = t.course_id AND c.user_id = t.user_id
	JOIN public.learning_chapters ch ON ch.id = t.chapter_id AND ch.user_id = t.user_id
	JOIN public.learning_attachments a ON a.id = t.attachment_id AND a.user_id = t.user_id AND a.course_id = t.course_id`

func scanTask(row pgx.Row) (taskRecord, error) {
	var item taskRecord
	var lease *string
	err := row.Scan(&item.ID, &item.UserID, &item.State, &item.DesiredState, &item.Version, &item.Priority,
		&item.TaskKind, &item.Title, &item.TaskPointID, &item.CourseID, &item.CourseTitle,
		&item.ChapterID, &item.ChapterTitle, &item.CreatedAt, &item.UpdatedAt, &item.AttemptCount,
		&item.FailureCount, &item.VerificationAttempts, &item.LastErrorCode, &item.NextAttemptAt,
		&item.ConfirmedAt, &item.Platform.Status, &item.Platform.CheckedAt, &item.ObservationRaw,
		&item.PlatformRaw, &item.MediaRaw, &lease, &item.Billing.State, &item.BillingQuote,
		&item.BillingHoldID, &item.Billing.ErrorCode, &item.CourseExternal, &item.ChapterExternal,
		&item.CycleKey, &item.CPI, &item.ObjectID, &item.FenceToken, &item.ClassExternal,
		&item.VerificationReason, &item.PreCompletedNoCharge, &item.ConfirmationReason)
	if err != nil {
		return item, err
	}
	item.LeaseAttempt = lease
	item.TaskSummary.UserID = item.UserID
	decorateTask(&item)
	return item, nil
}

func decorateTask(item *taskRecord) {
	item.Billing.Configured = item.Billing.State != "disabled"
	item.Billing.AmountKind = "none"
	item.Billing.QuotedAmountMinor = "0"
	if len(item.BillingQuote) > 0 {
		var quote BillingQuote
		if json.Unmarshal(item.BillingQuote, &quote) == nil {
			item.Billing.QuotedAmountMinor = strconv.FormatInt(quote.AmountMinor, 10)
			item.Billing.Currency = quote.Currency
			item.Billing.PriceVersion = quote.PriceVersion
			item.Billing.Configured = quote.Configured
			if quote.Configured {
				item.Billing.AmountKind = "quote"
			}
		}
	}
	item.Platform.Passed = item.Platform.Status == CheckConfirmed
	if len(item.PlatformRaw) > 0 {
		var fact struct {
			Status          string   `json:"status"`
			Passed          bool     `json:"passed"`
			Source          string   `json:"source"`
			PlayTimeSeconds *float64 `json:"play_time_seconds"`
			Note            string   `json:"note"`
		}
		if json.Unmarshal(item.PlatformRaw, &fact) == nil {
			if fact.Status != "" {
				item.Platform.Status = fact.Status
			}
			item.Platform.Passed = fact.Status == CheckConfirmed
			item.Platform.Source = fact.Source
			item.Platform.PlayTimeSeconds = fact.PlayTimeSeconds
			item.Platform.Note = fact.Note
		}
	}
	if len(item.ObservationRaw) > 0 && string(item.ObservationRaw) != "{}" {
		var observation Observation
		if json.Unmarshal(item.ObservationRaw, &observation) == nil && observation.ObservedAt != (time.Time{}) {
			item.Observation = &observation
		}
	}
	item.MediaEvidence = unavailableMedia()
	if len(item.MediaRaw) > 0 {
		var media MediaEvidence
		if json.Unmarshal(item.MediaRaw, &media) == nil && media.Available {
			item.MediaEvidence = media
		}
	}
	item.Estimate = Estimate{Estimated: true}
	item.PlanRefs = []TaskPlanRef{}
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Service) loadTask(ctx context.Context, querier rowQuerier, taskID, userID string, lock bool) (taskRecord, error) {
	query := taskSelect + ` WHERE t.id = $1`
	args := []any{taskID}
	if userID != "" {
		query += ` AND t.user_id = $2`
		args = append(args, userID)
	}
	if lock {
		query += ` FOR UPDATE OF t`
	}
	item, err := scanTask(querier.QueryRow(ctx, query, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return item, errNotFound
	}
	if err != nil {
		return item, errServiceUnavailable
	}
	return item, nil
}

func (s *Service) taskPlanRefs(ctx context.Context, querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, taskID string) ([]TaskPlanRef, error) {
	rows, err := querier.Query(ctx, `SELECT p.id::text, p.current_revision_id::text, r.version
		FROM public.learning_plan_task_bindings b
		JOIN public.learning_plans p ON p.id = b.plan_id AND p.user_id = b.user_id
		JOIN public.learning_plan_revisions r ON r.id = p.current_revision_id AND r.user_id = p.user_id
		WHERE b.task_id = $1 AND b.plan_revision_id = p.current_revision_id
		ORDER BY p.created_at`, taskID)
	if err != nil {
		return nil, errServiceUnavailable
	}
	defer rows.Close()
	items := make([]TaskPlanRef, 0)
	for rows.Next() {
		var item TaskPlanRef
		if err := rows.Scan(&item.PlanID, &item.RevisionID, &item.Version); err != nil {
			return nil, errServiceUnavailable
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errServiceUnavailable
	}
	return items, nil
}

func (s *Service) ListTasks(ctx context.Context, actor Actor, filter TaskFilter) (Page[TaskSummary], error) {
	return s.listTasks(ctx, actor, filter, false)
}

func (s *Service) AdminListTasks(ctx context.Context, actor Actor, filter TaskFilter) (Page[TaskSummary], error) {
	return s.listTasks(ctx, actor, filter, true)
}

func (s *Service) listTasks(ctx context.Context, actor Actor, filter TaskFilter, admin bool) (Page[TaskSummary], error) {
	limit := clampLimit(filter.Limit)
	position, err := decodeCursor(filter.Cursor)
	if err != nil {
		return Page[TaskSummary]{}, err
	}
	for _, id := range []string{filter.CourseID, filter.PlanID, filter.UserID} {
		if id != "" && !web.ValidID(id) {
			return Page[TaskSummary]{}, validation("筛选标识无效")
		}
	}
	states := make([]string, 0, len(filter.States))
	for _, state := range filter.States {
		if !validStates[state] {
			return Page[TaskSummary]{}, validation("任务状态筛选无效")
		}
		states = append(states, state)
	}
	owner := actor.ID
	if admin {
		owner = filter.UserID
	}
	query := taskSelect + ` WHERE ($1 = '' OR t.user_id = $1::uuid)
		AND (cardinality($2::text[]) = 0 OR t.state = ANY($2::text[]))
		AND ($3 = '' OR t.course_id = $3::uuid)
		AND ($4 = '' OR EXISTS (SELECT 1 FROM public.learning_plan_task_bindings b
			WHERE b.task_id = t.id AND b.plan_id = $4::uuid))
		AND ($5::timestamptz IS NULL OR (t.created_at, t.id) < ($5::timestamptz, $6::uuid))
		ORDER BY t.created_at DESC, t.id DESC LIMIT $7`
	rows, err := s.pool.Query(ctx, query, owner, states, filter.CourseID, filter.PlanID,
		nullableTime(position.Time), nullableID(position.ID), limit+1)
	if err != nil {
		return Page[TaskSummary]{}, errServiceUnavailable
	}
	defer rows.Close()
	records := make([]taskRecord, 0, limit+1)
	for rows.Next() {
		item, err := scanTask(rows)
		if err != nil {
			return Page[TaskSummary]{}, errServiceUnavailable
		}
		records = append(records, item)
	}
	if err := rows.Err(); err != nil {
		return Page[TaskSummary]{}, errServiceUnavailable
	}
	items := make([]TaskSummary, 0, len(records))
	for _, record := range records {
		refs, err := s.taskPlanRefs(ctx, s.pool, record.ID)
		if err != nil {
			return Page[TaskSummary]{}, err
		}
		record.PlanRefs = refs
		items = append(items, record.TaskSummary)
	}
	page := Page[TaskSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := records[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func (s *Service) GetTask(ctx context.Context, actor Actor, taskID string) (TaskDetail, error) {
	if !web.ValidID(taskID) {
		return TaskDetail{}, errNotFound
	}
	return s.getTask(ctx, actor.ID, taskID)
}

func (s *Service) getTask(ctx context.Context, userID, taskID string) (TaskDetail, error) {
	var detail TaskDetail
	record, err := s.loadTask(ctx, s.pool, taskID, userID, false)
	if err != nil {
		return detail, err
	}
	refs, err := s.taskPlanRefs(ctx, s.pool, record.ID)
	if err != nil {
		return detail, err
	}
	record.PlanRefs = refs
	detail.TaskSummary = record.TaskSummary
	detail.Attempts = make([]AttemptView, 0)
	rows, err := s.pool.Query(ctx, `SELECT id::text, attempt_no, state, fence_token,
		COALESCE(plan_revision_id::text, ''), started_at, heartbeat_at, ended_at, cancel_requested,
		result, error_code, observation, platform_check, media_evidence
		FROM public.learning_task_attempts WHERE task_id = $1
		ORDER BY attempt_no DESC LIMIT 20`, taskID)
	if err != nil {
		return detail, errServiceUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var item AttemptView
		var observationRaw, platformRaw, mediaRaw json.RawMessage
		if err := rows.Scan(&item.ID, &item.AttemptNo, &item.State, &item.FenceToken, &item.PlanRevisionID,
			&item.StartedAt, &item.HeartbeatAt, &item.EndedAt, &item.CancelRequested, &item.Result,
			&item.ErrorCode, &observationRaw, &platformRaw, &mediaRaw); err != nil {
			return detail, errServiceUnavailable
		}
		if len(observationRaw) > 0 && string(observationRaw) != "{}" {
			var observation Observation
			if json.Unmarshal(observationRaw, &observation) == nil && observation.ObservedAt != (time.Time{}) {
				item.Observation = &observation
			}
		}
		item.MediaEvidence = unavailableMedia()
		if len(mediaRaw) > 0 {
			var media MediaEvidence
			if json.Unmarshal(mediaRaw, &media) == nil && media.Available {
				item.MediaEvidence = media
			}
		}
		item.Platform = platformFactFromJSON(platformRaw)
		detail.Attempts = append(detail.Attempts, item)
	}
	if err := rows.Err(); err != nil {
		return detail, errServiceUnavailable
	}
	detail.Commands = make([]CommandView, 0)
	commandRows, err := s.pool.Query(ctx, `SELECT id::text, kind, status, result_code, actor_kind, reason,
		created_at, applied_at FROM public.learning_task_commands
		WHERE task_id = $1 ORDER BY created_at DESC LIMIT 20`, taskID)
	if err != nil {
		return detail, errServiceUnavailable
	}
	defer commandRows.Close()
	for commandRows.Next() {
		var item CommandView
		if err := commandRows.Scan(&item.ID, &item.Kind, &item.Status, &item.ResultCode,
			&item.ActorKind, &item.Reason, &item.CreatedAt, &item.AppliedAt); err != nil {
			return detail, errServiceUnavailable
		}
		detail.Commands = append(detail.Commands, item)
	}
	if err := commandRows.Err(); err != nil {
		return detail, errServiceUnavailable
	}
	return detail, nil
}

func platformFactFromJSON(raw json.RawMessage) PlatformFactView {
	fact := PlatformFactView{Status: CheckUnconfirmed}
	if len(raw) == 0 {
		return fact
	}
	var parsed struct {
		Status          string   `json:"status"`
		Passed          bool     `json:"passed"`
		Source          string   `json:"source"`
		PlayTimeSeconds *float64 `json:"play_time_seconds"`
		Note            string   `json:"note"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return fact
	}
	if parsed.Status != "" {
		fact.Status = parsed.Status
	}
	fact.Passed = fact.Status == CheckConfirmed
	fact.Source = parsed.Source
	fact.PlayTimeSeconds = parsed.PlayTimeSeconds
	fact.Note = parsed.Note
	return fact
}

type TaskControlResult struct {
	TaskID        string `json:"task_id"`
	State         string `json:"state"`
	DesiredState  string `json:"desired_state"`
	Version       int64  `json:"version"`
	Applied       bool   `json:"applied"`
	StopRequested bool   `json:"stop_requested"`
	ResultCode    string `json:"result_code,omitempty"`
}

// ControlTask 是独立幂等命令。对运行中任务只记录停止请求，执行器确认后才改变真实状态。
func (s *Service) ControlTask(ctx context.Context, actor Actor, taskID, idempotencyKey, action, reason string) (TaskControlResult, error) {
	if !web.ValidID(taskID) {
		return TaskControlResult{}, errNotFound
	}
	if !validIdempotencyKey(idempotencyKey) {
		return TaskControlResult{}, validation("必须提供有效的 Idempotency-Key")
	}
	validAction := action == "pause" || action == "resume" || action == "cancel" || action == "redispatch"
	if !validAction {
		return TaskControlResult{}, validation("任务控制动作无效")
	}
	if len(strings.TrimSpace(reason)) > 500 {
		return TaskControlResult{}, validation("理由过长")
	}
	hash := requestHash(action, reason)
	var result TaskControlResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		record, err := s.loadTask(ctx, tx, taskID, "", true)
		if err != nil {
			return err
		}
		if actor.Kind == "user" && record.UserID != actor.ID {
			return errNotFound
		}
		var commandID, storedHash, storedResult, storedStatus string
		err = tx.QueryRow(ctx, `SELECT id::text, request_hash, result_code, status
			FROM public.learning_task_commands
			WHERE task_id = $1 AND actor_kind = $2 AND actor_id = $3 AND idempotency_key = $4
			FOR UPDATE`, taskID, actor.Kind, actor.ID, idempotencyKey).
			Scan(&commandID, &storedHash, &storedResult, &storedStatus)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return errServiceUnavailable
		}
		if commandID != "" {
			if storedHash != hash {
				return errIdempotency
			}
			result = TaskControlResult{TaskID: taskID, State: record.State, DesiredState: record.DesiredState,
				Version: record.Version, Applied: storedStatus == "applied",
				StopRequested: storedResult == "stop_requested", ResultCode: storedResult}
			return nil
		}
		now := s.now()
		active := record.LeaseAttempt != nil && (record.State == StatePreparing || record.State == StateRunning)
		desired := record.DesiredState
		state := record.State
		stopRequested := false
		applied := true
		resultCode := "applied"
		switch action {
		case "pause":
			switch {
			case record.State == StateConfirmed || record.State == StateCancelled:
				return conflict("已确认或已取消的任务不能暂停")
			case active:
				desired = DesiredPause
				stopRequested = true
				applied = false
				resultCode = "stop_requested"
			case record.State == StatePaused:
				desired = DesiredPause
			default:
				desired = DesiredPause
				state = StatePaused
				stopRequested = false
			}
		case "resume":
			switch record.State {
			case StateConfirmed, StateCancelled:
				return conflict("已确认或已取消的任务不能继续")
			case StateRunning, StatePreparing:
				desired = DesiredRun
			case StatePaused:
				desired = DesiredRun
				state = StatePending
				record.FailureCount = 0
				record.VerificationAttempts = 0
			default:
				return conflict("只有已暂停任务可以直接继续，失败任务请使用重新调度")
			}
		case "cancel":
			switch record.State {
			case StateConfirmed:
				return conflict("平台已确认的任务不能取消")
			case StateCancelled:
				desired = DesiredCancel
				applied = true
				resultCode = "already_cancelled"
			case StatePreparing, StateRunning:
				if active {
					desired = DesiredCancel
					stopRequested = true
					applied = false
					resultCode = "stop_requested"
				} else {
					desired = DesiredCancel
					state = StateCancelled
				}
			default:
				desired = DesiredCancel
				state = StateCancelled
			}
		case "redispatch":
			switch record.State {
			case StateConfirmed:
				return conflict("已确认任务不需要重新调度")
			case StatePreparing, StateRunning:
				return conflict("任务正在执行，必须先请求停止")
			case StateCancelled, StateFailed, StateNeedsAttention, StatePaused, StateWaitingVerification:
				desired = DesiredRun
				state = StatePending
				record.FailureCount = 0
				record.VerificationAttempts = 0
			case StatePending:
				desired = DesiredRun
			}
		}
		if action == "redispatch" || (action == "resume" && record.State == StatePaused) {
			state = StatePending
		}
		nowPtr := any(nil)
		if stopRequested {
			nowPtr = now
		}
		nextAttempt := any(nil)
		if state == StatePending {
			nextAttempt = now
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET
			state = $2, desired_state = $3, version = version + 1,
			failure_count = $4, verification_attempts = $5,
			stop_requested_at = CASE WHEN $6::timestamptz IS NOT NULL THEN $6
				WHEN $8 IN ('resume', 'redispatch') THEN NULL ELSE stop_requested_at END,
			next_attempt_at = CASE WHEN $2 = 'pending' THEN $7::timestamptz ELSE next_attempt_at END,
			next_verify_at = CASE WHEN $2 IN ('pending', 'paused', 'cancelled') THEN NULL ELSE next_verify_at END,
			lease_attempt_id = CASE WHEN $2 IN ('pending', 'paused', 'cancelled') THEN NULL ELSE lease_attempt_id END,
			lease_owner = CASE WHEN $2 IN ('pending', 'paused', 'cancelled') THEN '' ELSE lease_owner END,
			lease_expires_at = CASE WHEN $2 IN ('pending', 'paused', 'cancelled') THEN NULL ELSE lease_expires_at END,
			last_error_code = CASE WHEN $8 IN ('resume', 'redispatch') THEN '' ELSE last_error_code END,
			verification_reason = CASE WHEN $8 IN ('resume', 'redispatch') THEN '' ELSE verification_reason END,
			updated_at = $9
			WHERE id = $1`,
			taskID, state, desired, record.FailureCount, record.VerificationAttempts, nowPtr, nextAttempt,
			action, now)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return errNotFound
		}
		if active {
			_, err = tx.Exec(ctx, `UPDATE public.learning_task_attempts SET cancel_requested = true
				WHERE task_id = $1 AND ended_at IS NULL`, taskID)
			if err != nil {
				return errServiceUnavailable
			}
		}
		status := "applied"
		if !applied {
			status = "accepted"
		}
		commandID = newID()
		var appliedAt any
		if applied {
			appliedAt = now
		}
		_, err = tx.Exec(ctx, `INSERT INTO public.learning_task_commands
			(id, task_id, user_id, actor_kind, actor_id, kind, idempotency_key, request_hash,
			 status, result_code, reason, created_at, applied_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			commandID, taskID, record.UserID, actor.Kind, actor.ID, action, idempotencyKey, hash,
			status, resultCode, truncate(reason, 500), now, appliedAt)
		if err != nil {
			return errServiceUnavailable
		}
		if action == "cancel" && state == StateCancelled && record.BillingHoldID != "" {
			if record.AttemptCount == 0 {
				if err := releaseBilling(ctx, tx, s.opts.Billing, record.UserID, taskID,
					record.BillingHoldID, "learning-task-"+taskID+"-release", "任务取消且未执行"); err != nil {
					if _, updateErr := tx.Exec(ctx, `UPDATE public.learning_tasks SET
						billing_state = 'review_required', billing_error = 'release_failed' WHERE id = $1`, taskID); updateErr != nil {
						return errServiceUnavailable
					}
				} else if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET billing_state = 'released',
					billing_error = '' WHERE id = $1`, taskID); err != nil {
					return errServiceUnavailable
				}
			} else {
				if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET billing_state = 'review_required',
					billing_error = 'cancelled_after_start' WHERE id = $1`, taskID); err != nil {
					return errServiceUnavailable
				}
			}
		}
		if action == "cancel" && state == StateCancelled {
			if err := closeActiveAttempt(ctx, tx, taskID, "cancelled", "cancelled"); err != nil {
				return err
			}
		}
		if action == "pause" && state == StatePaused {
			if err := closeActiveAttempt(ctx, tx, taskID, "stopped", "paused"); err != nil {
				return err
			}
		}
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "task.control_" + status, ResourceType: "task", ResourceID: taskID,
			UserID: record.UserID, Payload: map[string]any{"action": action, "state": state,
				"result_code": resultCode},
		}); err != nil {
			return err
		}
		if actor.Kind == "admin" {
			if err := writeAudit(ctx, tx, AuditEvent{Actor: actor, Action: "learning.task.control",
				ResourceType: "task", ResourceID: taskID, Reason: reason}); err != nil {
				return err
			}
		}
		result = TaskControlResult{TaskID: taskID, State: state, DesiredState: desired,
			Version: record.Version + 1, Applied: applied, StopRequested: stopRequested, ResultCode: resultCode}
		return nil
	})
	if err != nil {
		return TaskControlResult{}, err
	}
	return result, nil
}

func closeActiveAttempt(ctx context.Context, tx pgx.Tx, taskID, result, errorCode string) error {
	now := time.Now().UTC()
	if _, err := tx.Exec(ctx, `UPDATE public.learning_task_attempts
		SET ended_at = $2, state = 'ended', result = $3, error_code = $4, lease_expires_at = $2
		WHERE task_id = $1 AND ended_at IS NULL AND result = ''`, taskID, now, result, errorCode); err != nil {
		return errServiceUnavailable
	}
	_, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET lease_attempt_id = NULL, lease_owner = '',
		lease_expires_at = NULL, fence_token = fence_token + 1 WHERE id = $1`, taskID)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

func releaseBilling(ctx context.Context, tx pgx.Tx, billing Billing, userID, taskID, holdID, key, reason string) error {
	if billing == nil || holdID == "" {
		return nil
	}
	return withSavepoint(ctx, tx, func(sp pgx.Tx) error {
		return billing.Release(ctx, sp, userID, taskID, holdID, key, reason)
	})
}

type TaskOperationItem struct {
	TaskID      string `json:"task_id"`
	OK          bool   `json:"ok"`
	State       string `json:"state,omitempty"`
	Code        string `json:"code,omitempty"`
	Message     string `json:"message,omitempty"`
	Applied     bool   `json:"applied"`
	StopRequest bool   `json:"stop_requested"`
}

type TaskOperationsRequest struct {
	TaskIDs []string `json:"task_ids"`
	Action  string   `json:"action"`
	Reason  string   `json:"reason"`
}

func (s *Service) AdminTaskOperations(ctx context.Context, actor Actor, request TaskOperationsRequest, idempotencyKey string) ([]TaskOperationItem, error) {
	reason := strings.TrimSpace(request.Reason)
	if reason == "" || len(reason) > 500 {
		return nil, validation("管理员任务操作必须填写 1 到 500 字的理由")
	}
	if len(request.TaskIDs) < 1 || len(request.TaskIDs) > 100 {
		return nil, validation("请选择 1 到 100 个任务")
	}
	if !validIdempotencyKey(idempotencyKey) {
		return nil, validation("必须提供有效的 Idempotency-Key")
	}
	items := make([]TaskOperationItem, 0, len(request.TaskIDs))
	for _, taskID := range request.TaskIDs {
		if !web.ValidID(taskID) {
			items = append(items, TaskOperationItem{TaskID: taskID, Code: "validation_failed", Message: "任务编号无效"})
			continue
		}
		commandKey := idempotencyKey + ":" + taskID
		if len(commandKey) > 128 {
			commandKey = "adm_" + requestHash(idempotencyKey, taskID)[:60]
		}
		result, err := s.ControlTask(ctx, actor, taskID, commandKey, request.Action, reason)
		item := TaskOperationItem{TaskID: taskID}
		if err != nil {
			item.Code = describe(err)
			_, _, item.Message, _, _ = ErrorCode(err)
			if item.Code == "not_found" {
				item.Message = "任务不存在"
			}
		} else {
			item.OK = true
			item.State = result.State
			item.Applied = result.Applied
			item.StopRequest = result.StopRequested
		}
		items = append(items, item)
	}
	return items, nil
}

type AccountOperationsRequest struct {
	UserIDs []string `json:"user_ids"`
	Action  string   `json:"action"`
	Reason  string   `json:"reason"`
}

type AccountOperationItem struct {
	UserID    string     `json:"user_id"`
	OK        bool       `json:"ok"`
	Code      string     `json:"code,omitempty"`
	Message   string     `json:"message,omitempty"`
	Status    string     `json:"status,omitempty"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

type AccountOperationResult struct {
	CommandID string                 `json:"command_id"`
	Action    string                 `json:"action"`
	Status    string                 `json:"status"`
	Replayed  bool                   `json:"replayed"`
	Items     []AccountOperationItem `json:"items"`
}

var accountOperationActions = map[string]string{
	"pause_plans":  "paused",
	"resume_plans": "active",
	"block":        "blocked",
	"unblock":      "unblocked",
}

func dedupeIDs(values []string) ([]string, error) {
	if len(values) < 1 || len(values) > 100 {
		return nil, validation("请选择 1 到 100 个账号")
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !web.ValidID(value) {
			return nil, validation("账号编号无效")
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	if len(result) == 0 {
		return nil, validation("请选择至少一个账号")
	}
	return result, nil
}

func accountOperationRequestHash(action, reason string, userIDs []string) string {
	sorted := append([]string(nil), userIDs...)
	sort.Strings(sorted)
	return requestHash("account-operation:v1", action, strings.TrimSpace(reason), strings.Join(sorted, "\x00"))
}

// AdminAccountOperations 是请求级幂等的账号批量操作。
// 同一管理员+Idempotency-Key 绑定规范化请求摘要；重复调用只处理 pending 项，
// 已完成/已失败项原样返回，不重复执行账号状态变更或审计。
func (s *Service) AdminAccountOperations(ctx context.Context, actor Actor, request AccountOperationsRequest, idempotencyKey string) (AccountOperationResult, error) {
	if actor.Kind != "admin" || !web.ValidID(actor.ID) {
		return AccountOperationResult{}, errForbidden
	}
	if !validIdempotencyKey(idempotencyKey) {
		return AccountOperationResult{}, validation("必须提供有效的 Idempotency-Key")
	}
	reason := strings.TrimSpace(request.Reason)
	if len(reason) < 1 || len(reason) > 500 {
		return AccountOperationResult{}, validation("管理员账号操作必须填写 1 到 500 字的理由")
	}
	action := request.Action
	if _, ok := accountOperationActions[action]; !ok {
		return AccountOperationResult{}, validation("账号操作动作无效")
	}
	userIDs, err := dedupeIDs(request.UserIDs)
	if err != nil {
		return AccountOperationResult{}, err
	}
	hash := accountOperationRequestHash(action, reason, userIDs)
	commandID, replayed, err := s.startAccountCommand(ctx, actor, idempotencyKey, hash, action, reason, userIDs)
	if err != nil {
		return AccountOperationResult{}, err
	}
	for _, userID := range userIDs {
		if _, err := s.processAccountCommandItem(ctx, actor, commandID, userID, action, reason); err != nil {
			return AccountOperationResult{}, err
		}
	}
	return s.readAccountCommandResult(ctx, commandID, action, replayed)
}

func (s *Service) startAccountCommand(ctx context.Context, actor Actor, key, hash, action, reason string, userIDs []string) (string, bool, error) {
	canonical := append([]string(nil), userIDs...)
	sort.Strings(canonical)
	commandID := newID()
	replayed := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `INSERT INTO public.learning_account_commands
			(id, actor_kind, actor_id, idempotency_key, request_hash, action, reason, user_ids)
			VALUES ($1, 'admin', $2, $3, $4, $5, $6, $7::uuid[])
			ON CONFLICT (actor_kind, actor_id, idempotency_key) DO NOTHING`,
			commandID, actor.ID, key, hash, action, reason, canonical)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() == 0 {
			var storedHash string
			if err := tx.QueryRow(ctx, `SELECT id::text, request_hash
				FROM public.learning_account_commands
				WHERE actor_kind = 'admin' AND actor_id = $1 AND idempotency_key = $2
				FOR UPDATE`, actor.ID, key).Scan(&commandID, &storedHash); err != nil {
				return errServiceUnavailable
			}
			if storedHash != hash {
				return errIdempotency
			}
			replayed = true
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.learning_account_command_items
				WHERE command_id = $1`, commandID).Scan(&count); err != nil {
				return errServiceUnavailable
			}
			if count == 0 {
				// 防御进程在命令与明细之间异常中断；正常插入在同一事务内。
				for position, userID := range userIDs {
					if _, err := tx.Exec(ctx, `INSERT INTO public.learning_account_command_items
						(command_id, user_id, position) VALUES ($1, $2, $3)`, commandID, userID, position); err != nil {
						return errServiceUnavailable
					}
				}
			}
			return nil
		}
		for position, userID := range userIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO public.learning_account_command_items
				(command_id, user_id, position) VALUES ($1, $2, $3)`, commandID, userID, position); err != nil {
				return errServiceUnavailable
			}
		}
		return nil
	})
	if err != nil {
		return "", false, err
	}
	return commandID, replayed, nil
}

func accountItemFailure(err error) (*Error, bool) {
	var target *Error
	if errors.As(err, &target) && target.Status != 503 {
		return target, true
	}
	return nil, false
}

func (s *Service) processAccountCommandItem(ctx context.Context, actor Actor, commandID, userID, action, reason string) (AccountOperationItem, error) {
	item := AccountOperationItem{UserID: userID}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var status, code, message, resultStatus string
		var appliedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT status, code, message, result_status, applied_at
			FROM public.learning_account_command_items
			WHERE command_id = $1 AND user_id = $2 FOR UPDATE`, commandID, userID).
			Scan(&status, &code, &message, &resultStatus, &appliedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		}
		if err != nil {
			return errServiceUnavailable
		}
		if status != "pending" {
			item.OK = status == "applied"
			item.Code, item.Message, item.Status, item.AppliedAt = code, message, resultStatus, appliedAt
			return nil
		}
		if _, err := s.loadAccount(ctx, tx, userID); err != nil {
			if failure, ok := accountItemFailure(err); ok {
				now := s.now()
				if err := markAccountCommandItemFailed(ctx, tx, commandID, userID, failure.Code, failure.Message, now); err != nil {
					return err
				}
				// 失败也记录一次逐项审计，但动作明确标记为失败，不冒充账号状态已改变。
				if err := writeAudit(ctx, tx, AuditEvent{Actor: actor,
					Action:       "learning.account." + action + "_failed",
					ResourceType: "learning_account", ResourceID: userID, Reason: reason}); err != nil {
					return err
				}
				item.Code, item.Message = failure.Code, failure.Message
				return nil
			}
			return err
		}
		now := s.now()
		resultStatus = accountOperationActions[action]
		switch action {
		case "pause_plans":
			if _, err := tx.Exec(ctx, `UPDATE public.learning_plans SET status = 'paused', updated_at = $2
				WHERE user_id = $1 AND status = 'active'`, userID, now); err != nil {
				return errServiceUnavailable
			}
		case "resume_plans":
			if _, err := tx.Exec(ctx, `UPDATE public.learning_plans SET status = 'active', updated_at = $2
				WHERE user_id = $1 AND status = 'paused'`, userID, now); err != nil {
				return errServiceUnavailable
			}
		case "block":
			if err := s.blockAccount(ctx, tx, userID, "admin_blocked", reason, nil); err != nil {
				return err
			}
		case "unblock":
			if _, err := tx.Exec(ctx, `UPDATE public.learning_accounts
				SET blocked_code = '', blocked_reason = '', blocked_at = NULL, blocked_until = NULL,
				    version = version + 1, updated_at = $2 WHERE user_id = $1`, userID, now); err != nil {
				return errServiceUnavailable
			}
		}
		if err := writeAudit(ctx, tx, AuditEvent{Actor: actor, Action: "learning.account." + action,
			ResourceType: "learning_account", ResourceID: userID, Reason: reason}); err != nil {
			return err
		}
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "account.operation_applied", ResourceType: "learning_account", ResourceID: userID,
			UserID: userID, Payload: map[string]any{"action": action, "command_id": commandID},
		}); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE public.learning_account_command_items
			SET status = 'applied', code = '', message = '', result_status = $3,
			    applied_at = $4, updated_at = $4
			WHERE command_id = $1 AND user_id = $2`, commandID, userID, resultStatus, now); err != nil {
			return errServiceUnavailable
		}
		item.OK = true
		item.Status = resultStatus
		item.AppliedAt = &now
		return nil
	})
	if err != nil {
		return AccountOperationItem{}, err
	}
	return item, nil
}

func markAccountCommandItemFailed(ctx context.Context, tx pgx.Tx, commandID, userID, code, message string, now time.Time) error {
	_, err := tx.Exec(ctx, `UPDATE public.learning_account_command_items
		SET status = 'failed', code = $3, message = $4, result_status = '', applied_at = $5, updated_at = $5
		WHERE command_id = $1 AND user_id = $2`, commandID, userID, safeCode(code), truncate(message, 300), now)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

func (s *Service) readAccountCommandResult(ctx context.Context, commandID, action string, replayed bool) (AccountOperationResult, error) {
	result := AccountOperationResult{CommandID: commandID, Action: action, Replayed: replayed,
		Items: make([]AccountOperationItem, 0)}
	rows, err := s.pool.Query(ctx, `SELECT user_id::text, status, code, message, result_status, applied_at
		FROM public.learning_account_command_items WHERE command_id = $1 ORDER BY position`, commandID)
	if err != nil {
		return result, errServiceUnavailable
	}
	defer rows.Close()
	applied, failed, pending := 0, 0, 0
	for rows.Next() {
		var item AccountOperationItem
		var status string
		if err := rows.Scan(&item.UserID, &status, &item.Code, &item.Message, &item.Status, &item.AppliedAt); err != nil {
			return result, errServiceUnavailable
		}
		switch status {
		case "applied":
			applied++
			item.OK = true
		case "failed":
			failed++
		default:
			pending++
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, errServiceUnavailable
	}
	switch {
	case pending > 0:
		result.Status = "pending"
	case failed == 0:
		result.Status = "applied"
	case applied == 0:
		result.Status = "failed"
	default:
		result.Status = "partially_applied"
	}
	return result, nil
}
