package learning

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type RuntimeCapabilities struct {
	PlatformAdapter  string   `json:"platform_adapter"`
	CourseRead       bool     `json:"course_read"`
	PlatformCheck    bool     `json:"platform_check"`
	CredentialVerify bool     `json:"credential_verify"`
	ExecutionEnabled bool     `json:"execution_enabled"`
	Executor         string   `json:"executor"`
	ExecutorEnabled  bool     `json:"executor_enabled"`
	TaskKinds        []string `json:"task_kinds"`
	MediaEvidence    bool     `json:"media_evidence"`
	BillingConnected bool     `json:"billing_connected"`
	SessionResolver  bool     `json:"session_resolver"`
	DisabledReasons  []string `json:"disabled_reasons"`
}

func (s *Service) Capabilities() RuntimeCapabilities {
	capabilities := RuntimeCapabilities{
		SessionResolver:  s.opts.Sessions != nil,
		BillingConnected: s.opts.Billing != nil,
		DisabledReasons:  []string{},
		TaskKinds:        []string{},
	}
	verificationReady := s.verifier() != nil
	if s.opts.Platform != nil {
		platform := s.opts.Platform.Capabilities()
		capabilities.PlatformAdapter = platform.Adapter
		capabilities.CourseRead = platform.CourseRead
		capabilities.CredentialVerify = platform.CredentialVerify
		if platform.DisabledReason != "" {
			capabilities.DisabledReasons = append(capabilities.DisabledReasons, platform.DisabledReason)
		}
	} else {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "platform_not_configured")
	}
	if s.opts.Executor != nil {
		executor := s.opts.Executor.Capabilities()
		capabilities.Executor = executor.Name
		capabilities.ExecutorEnabled = executor.Enabled
		capabilities.TaskKinds = append(capabilities.TaskKinds, executor.TaskKinds...)
		capabilities.MediaEvidence = executor.ProducesMediaEvidence
		if !executor.Enabled && executor.DisabledReason != "" {
			capabilities.DisabledReasons = append(capabilities.DisabledReasons, executor.DisabledReason)
		}
	} else {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "executor_not_configured")
	}
	capabilities.PlatformCheck = verificationReady
	if !verificationReady {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "platform_verifier_not_configured")
	}
	// 只有配置打开且执行器、平台核验、会话解析同时就绪才公布真实执行能力。
	capabilities.ExecutionEnabled = s.opts.ExecutionEnabled && capabilities.ExecutorEnabled &&
		verificationReady && capabilities.SessionResolver
	if !s.opts.ExecutionEnabled {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "execution_disabled_by_configuration")
	} else if !capabilities.ExecutionEnabled {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "execution_not_ready")
	}
	if s.opts.Sessions == nil {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "session_resolver_not_configured")
	}
	if !capabilities.BillingConnected {
		capabilities.DisabledReasons = append(capabilities.DisabledReasons, "billing_not_connected")
	}
	return capabilities
}

type Dashboard struct {
	GeneratedAt time.Time `json:"generated_at"`
	Tasks       struct {
		Total          int            `json:"total"`
		ByState        map[string]int `json:"by_state"`
		ActiveAttempts int            `json:"active_attempts"`
		MediaEvidence  int            `json:"media_evidence_available"`
		AwaitingVerify int            `json:"awaiting_verification"`
	} `json:"tasks"`
	Courses struct {
		Total int `json:"total"`
		Stale int `json:"stale"`
	} `json:"courses"`
	Plans struct {
		Active int `json:"active"`
		Paused int `json:"paused"`
	} `json:"plans"`
	Syncs struct {
		Queued         int `json:"queued"`
		Running        int `json:"running"`
		NeedsAttention int `json:"needs_attention"`
	} `json:"syncs"`
	Accounts struct {
		Total   int `json:"total"`
		Bound   int `json:"bound"`
		Blocked int `json:"blocked"`
	} `json:"accounts"`
	Capabilities RuntimeCapabilities `json:"capabilities"`
}

func (s *Service) AdminDashboard(ctx context.Context, actor Actor) (Dashboard, error) {
	result := Dashboard{GeneratedAt: s.now()}
	result.Tasks.ByState = map[string]int{}
	rows, err := s.pool.Query(ctx, `SELECT state, count(*) FROM public.learning_tasks GROUP BY state`)
	if err != nil {
		return result, errServiceUnavailable
	}
	for rows.Next() {
		var state string
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return result, errServiceUnavailable
		}
		result.Tasks.ByState[state] = count
		result.Tasks.Total += count
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return result, errServiceUnavailable
	}
	result.Tasks.AwaitingVerify = result.Tasks.ByState[StateWaitingVerification]
	if err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM public.learning_task_attempts WHERE ended_at IS NULL AND lease_expires_at > now()),
		(SELECT count(*) FROM public.learning_tasks WHERE (media_evidence->>'available')::boolean IS TRUE),
		(SELECT count(*) FROM public.learning_courses),
		(SELECT count(*) FROM public.learning_courses WHERE synced_at IS NULL OR synced_at < $1),
		(SELECT count(*) FROM public.learning_plans WHERE status = 'active'),
		(SELECT count(*) FROM public.learning_plans WHERE status = 'paused'),
		(SELECT count(*) FROM public.learning_course_syncs WHERE status = 'queued'),
		(SELECT count(*) FROM public.learning_course_syncs WHERE status = 'running'),
		(SELECT count(*) FROM public.learning_course_syncs WHERE status = 'needs_attention'),
		(SELECT count(*) FROM public.users),
		(SELECT count(*) FROM public.learning_accounts),
		(SELECT count(*) FROM public.learning_accounts WHERE blocked_code <> '' OR (blocked_until IS NOT NULL AND blocked_until > now()))`,
		result.GeneratedAt.Add(-s.opts.CourseSnapshotTTL)).Scan(
		&result.Tasks.ActiveAttempts, &result.Tasks.MediaEvidence,
		&result.Courses.Total, &result.Courses.Stale,
		&result.Plans.Active, &result.Plans.Paused,
		&result.Syncs.Queued, &result.Syncs.Running, &result.Syncs.NeedsAttention,
		&result.Accounts.Total, &result.Accounts.Bound, &result.Accounts.Blocked); err != nil {
		return result, errServiceUnavailable
	}
	result.Capabilities = s.Capabilities()
	return result, nil
}

type AdminUserSummary struct {
	UserID              string     `json:"user_id"`
	DisplayName         string     `json:"display_name"`
	School              string     `json:"school"`
	IdentityBound       bool       `json:"identity_bound"`
	IdentityNamespace   string     `json:"identity_namespace,omitempty"`
	BlockedCode         string     `json:"blocked_code,omitempty"`
	BlockedReason       string     `json:"blocked_reason,omitempty"`
	BlockedUntil        *time.Time `json:"blocked_until,omitempty"`
	CourseCount         int        `json:"course_count"`
	PlanCount           int        `json:"plan_count"`
	TaskCount           int        `json:"task_count"`
	IncompleteTaskCount int        `json:"incomplete_task_count"`
	LastSyncedAt        *time.Time `json:"last_synced_at,omitempty"`
	CreatedAt           time.Time  `json:"created_at"`
}

type AdminUserFilter struct {
	Search string
	Limit  int
	Cursor string
}

func (s *Service) AdminListUsers(ctx context.Context, actor Actor, filter AdminUserFilter) (Page[AdminUserSummary], error) {
	limit := clampLimit(filter.Limit)
	position, err := decodeCursor(filter.Cursor)
	if err != nil {
		return Page[AdminUserSummary]{}, err
	}
	search := "%" + truncate(filter.Search, 80) + "%"
	rows, err := s.pool.Query(ctx, `SELECT u.id::text, u.created_at, u.updated_at,
		COALESCE(a.display_name, ''), COALESCE(a.school, ''), a.user_id IS NOT NULL,
		COALESCE(a.identity_namespace, ''), COALESCE(a.blocked_code, ''), COALESCE(a.blocked_reason, ''),
		a.blocked_until,
		(SELECT count(*) FROM public.learning_courses c WHERE c.user_id = u.id),
		(SELECT count(*) FROM public.learning_plans p WHERE p.user_id = u.id),
		(SELECT count(*) FROM public.learning_tasks t WHERE t.user_id = u.id),
		(SELECT count(*) FROM public.learning_tasks t WHERE t.user_id = u.id AND t.state NOT IN ('confirmed', 'cancelled')),
		(SELECT max(c.synced_at) FROM public.learning_courses c WHERE c.user_id = u.id)
		FROM public.users u
		LEFT JOIN public.learning_accounts a ON a.user_id = u.id
		WHERE ($1 = '%%' OR u.id::text = $2 OR COALESCE(a.display_name, '') ILIKE $1 OR COALESCE(a.school, '') ILIKE $1)
		  AND ($3::timestamptz IS NULL OR (u.created_at, u.id) < ($3::timestamptz, $4::uuid))
		ORDER BY u.created_at DESC, u.id DESC LIMIT $5`,
		search, strings.Trim(filter.Search, "%"), nullableTime(position.Time), nullableID(position.ID), limit+1)
	if err != nil {
		return Page[AdminUserSummary]{}, errServiceUnavailable
	}
	defer rows.Close()
	items := make([]AdminUserSummary, 0, limit+1)
	times := make([]time.Time, 0, limit+1)
	for rows.Next() {
		var item AdminUserSummary
		var updated time.Time
		if err := rows.Scan(&item.UserID, &item.CreatedAt, &updated, &item.DisplayName, &item.School,
			&item.IdentityBound, &item.IdentityNamespace, &item.BlockedCode, &item.BlockedReason,
			&item.BlockedUntil, &item.CourseCount, &item.PlanCount, &item.TaskCount,
			&item.IncompleteTaskCount, &item.LastSyncedAt); err != nil {
			return Page[AdminUserSummary]{}, errServiceUnavailable
		}
		items = append(items, item)
		times = append(times, item.CreatedAt)
	}
	if err := rows.Err(); err != nil {
		return Page[AdminUserSummary]{}, errServiceUnavailable
	}
	page := Page[AdminUserSummary]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.NextCursor = encodeCursor(times[limit-1], items[limit-1].UserID)
	}
	return page, nil
}

type AdminUserDetail struct {
	AdminUserSummary
	Courses []CourseSummary `json:"courses"`
	Plans   []PlanView      `json:"plans"`
	Tasks   []TaskSummary   `json:"tasks"`
}

func (s *Service) AdminGetUser(ctx context.Context, actor Actor, userID string) (AdminUserDetail, error) {
	var detail AdminUserDetail
	if !web.ValidID(userID) {
		return detail, errNotFound
	}
	err := s.pool.QueryRow(ctx, `SELECT u.id::text, u.created_at, COALESCE(a.display_name, ''),
		COALESCE(a.school, ''), a.user_id IS NOT NULL, COALESCE(a.identity_namespace, ''),
		COALESCE(a.blocked_code, ''), COALESCE(a.blocked_reason, ''), a.blocked_until,
		(SELECT count(*) FROM public.learning_courses c WHERE c.user_id = u.id),
		(SELECT count(*) FROM public.learning_plans p WHERE p.user_id = u.id),
		(SELECT count(*) FROM public.learning_tasks t WHERE t.user_id = u.id),
		(SELECT count(*) FROM public.learning_tasks t WHERE t.user_id = u.id AND t.state NOT IN ('confirmed', 'cancelled')),
		(SELECT max(c.synced_at) FROM public.learning_courses c WHERE c.user_id = u.id)
		FROM public.users u LEFT JOIN public.learning_accounts a ON a.user_id = u.id
		WHERE u.id = $1`, userID).Scan(
		&detail.UserID, &detail.CreatedAt, &detail.DisplayName, &detail.School, &detail.IdentityBound,
		&detail.IdentityNamespace, &detail.BlockedCode, &detail.BlockedReason, &detail.BlockedUntil,
		&detail.CourseCount, &detail.PlanCount, &detail.TaskCount, &detail.IncompleteTaskCount,
		&detail.LastSyncedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return detail, errNotFound
	}
	if err != nil {
		return detail, errServiceUnavailable
	}
	owner := Actor{ID: userID, Kind: "user"}
	courses, err := s.ListCourses(ctx, owner, CourseFilter{Limit: 100})
	if err != nil {
		return detail, err
	}
	detail.Courses = courses.Items
	plans, err := s.ListPlans(ctx, owner, PlanFilter{Limit: 100})
	if err != nil {
		return detail, err
	}
	detail.Plans = plans.Items
	tasks, err := s.listTasks(ctx, Actor{ID: userID, Kind: "user"}, TaskFilter{Limit: 20}, false)
	if err != nil {
		return detail, err
	}
	detail.Tasks = tasks.Items
	return detail, nil
}
