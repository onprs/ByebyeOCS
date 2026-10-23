package learning

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

var allowedTaskKinds = map[string]bool{
	"video": true, "document": true, "audio": true, "quiz": true, "other": true,
}

type PlanScopeInput struct {
	Kind     string        `json:"kind"`
	Chapters []PlanChapter `json:"chapters,omitempty"`
}

type QuestionPolicyInput struct {
	Enabled                   bool    `json:"enabled"`
	ModelOfferingID           *string `json:"model_offering_id"`
	AnswerMode                string  `json:"answer_mode"`
	RequireManualConfirmation *bool   `json:"require_manual_confirmation"`
}

type PlanInput struct {
	Name           string              `json:"name"`
	CourseIDs      []string            `json:"course_ids"`
	Scope          PlanScopeInput      `json:"scope"`
	TaskTypes      []string            `json:"task_types"`
	Priority       int                 `json:"priority"`
	QuestionPolicy QuestionPolicyInput `json:"question_policy"`
}

type QuestionPolicyView struct {
	Enabled                   bool    `json:"enabled"`
	ModelOfferingID           *string `json:"model_offering_id"`
	AnswerMode                string  `json:"answer_mode"`
	RequireManualConfirmation bool    `json:"require_manual_confirmation"`
	// 能力取决于当前部署的自动做题执行器，客户端不能写入。
	ExecutionAvailable  bool `json:"execution_available"`
	SubmissionAvailable bool `json:"submission_available"`
}

type PlanScopeView struct {
	Kind      string        `json:"kind"`
	CourseIDs []string      `json:"course_ids"`
	Chapters  []PlanChapter `json:"chapters,omitempty"`
}

type PlanView struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Status            string             `json:"status"`
	Version           int64              `json:"version"`
	Priority          int                `json:"priority"`
	TaskTypes         []string           `json:"task_types"`
	Scope             PlanScopeView      `json:"scope"`
	QuestionPolicy    QuestionPolicyView `json:"question_policy"`
	CurrentRevisionID string             `json:"current_revision_id"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

type PlanFilter struct {
	Limit  int
	Cursor string
}

func normalizePlanInput(input PlanInput) (PlanInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if len(input.Name) < 1 || len(input.Name) > 80 {
		return input, validation("安排名称必须为 1 到 80 个字符")
	}
	if len(input.CourseIDs) < 1 || len(input.CourseIDs) > 20 {
		return input, validation("请选择 1 到 20 门课程")
	}
	seenCourses := map[string]bool{}
	for _, id := range input.CourseIDs {
		if len(id) != 36 || seenCourses[id] {
			return input, validation("课程选择无效或重复")
		}
		seenCourses[id] = true
	}
	if len(input.TaskTypes) < 1 || len(input.TaskTypes) > 8 {
		return input, validation("请选择 1 到 8 种任务类型")
	}
	seenTypes := map[string]bool{}
	for _, kind := range input.TaskTypes {
		if !allowedTaskKinds[kind] || seenTypes[kind] {
			return input, validation("任务类型无效或重复")
		}
		seenTypes[kind] = true
	}
	if input.Priority < 0 || input.Priority > 100 {
		return input, validation("优先级必须在 0 到 100 之间")
	}
	switch input.Scope.Kind {
	case "", "all":
		input.Scope.Kind = "all"
		input.Scope.Chapters = nil
	case "selected":
		if len(input.Scope.Chapters) < 1 || len(input.Scope.Chapters) > 200 {
			return input, validation("选定章节时必须提供 1 到 200 个章节")
		}
		seen := map[string]bool{}
		chapters := make([]PlanChapter, 0, len(input.Scope.Chapters))
		for _, chapter := range input.Scope.Chapters {
			if !seenCourses[chapter.CourseID] {
				return input, validation("章节所属课程不在本次选择中")
			}
			if len(chapter.ChapterID) != 36 {
				return input, validation("章节标识无效")
			}
			key := chapter.CourseID + "/" + chapter.ChapterID
			if seen[key] {
				continue
			}
			seen[key] = true
			chapters = append(chapters, chapter)
		}
		input.Scope.Chapters = chapters
	default:
		return input, validation("安排范围仅支持 all 或 selected")
	}
	return input, nil
}

func (s *Service) normalizeQuestionPolicy(ctx context.Context, input QuestionPolicyInput) (QuestionPolicy, error) {
	policy := QuestionPolicy{Enabled: input.Enabled, Mode: strings.TrimSpace(input.AnswerMode)}
	if policy.Mode == "" {
		policy.Mode = "cache_first"
	}
	if policy.Mode != "cache_first" && policy.Mode != "always_llm" {
		return QuestionPolicy{}, validation("题库模式仅支持 cache_first 或 always_llm")
	}
	// 旧客户端的确认字段仅作兼容解析，所有新安排均自动执行与提交。
	policy.RequireManualConfirmation = false
	if input.ModelOfferingID != nil {
		value := strings.TrimSpace(*input.ModelOfferingID)
		if value != "" {
			if !web.ValidResourceID(value) || len(value) > 128 {
				return QuestionPolicy{}, validation("题库模型条目编号无效")
			}
			policy.ModelOfferingID = value
		}
	}
	if policy.Enabled && policy.ModelOfferingID == "" {
		return QuestionPolicy{}, validation("启用题库策略必须选择一个已发布的模型条目")
	}
	if policy.ModelOfferingID != "" {
		if s.opts.QuestionsPolicyValidator == nil {
			return QuestionPolicy{}, apiErr(503, "questions_policy_validator_not_configured",
				"题库模型校验尚未配置，暂不能保存模型策略")
		}
		if err := s.opts.QuestionsPolicyValidator.ValidateQuestionPolicy(ctx, policy.ModelOfferingID, policy.Mode); err != nil {
			return QuestionPolicy{}, validation("题库模型条目不可用或模式未发布")
		}
	}
	return policy, nil
}

func (s *Service) validatePlanOwnership(ctx context.Context, tx pgx.Tx, userID string, input PlanInput) error {
	var courseCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.learning_courses
		WHERE user_id = $1 AND id = ANY($2::uuid[])`, userID, input.CourseIDs).Scan(&courseCount); err != nil {
		return errServiceUnavailable
	}
	if courseCount != len(input.CourseIDs) {
		return validation("课程不存在或不属于当前用户")
	}
	if input.Scope.Kind == "selected" {
		for _, chapter := range input.Scope.Chapters {
			if len(chapter.ChapterID) != 36 {
				return validation("章节标识无效")
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM public.learning_chapters
				WHERE id = $1 AND course_id = $2 AND user_id = $3)`,
				chapter.ChapterID, chapter.CourseID, userID).Scan(&exists); err != nil {
				return errServiceUnavailable
			}
			if !exists {
				return validation("章节不存在或不属于所选课程")
			}
		}
	}
	return nil
}

func (s *Service) CreatePlan(ctx context.Context, actor Actor, input PlanInput) (PlanView, error) {
	normalized, err := normalizePlanInput(input)
	if err != nil {
		return PlanView{}, err
	}
	policy, err := s.normalizeQuestionPolicy(ctx, input.QuestionPolicy)
	if err != nil {
		return PlanView{}, err
	}
	now := s.now()
	planID := newID()
	return s.createOrUpdatePlan(ctx, actor, actor.Kind, actor.ID, planID, 0, normalized, policy, now, true)
}

func (s *Service) UpdatePlan(ctx context.Context, actor Actor, planID string, expectedVersion int64, input PlanInput) (PlanView, error) {
	normalized, err := normalizePlanInput(input)
	if err != nil {
		return PlanView{}, err
	}
	policy, err := s.normalizeQuestionPolicy(ctx, input.QuestionPolicy)
	if err != nil {
		return PlanView{}, err
	}
	if expectedVersion < 1 {
		return PlanView{}, errPrecondition
	}
	return s.createOrUpdatePlan(ctx, actor, actor.Kind, actor.ID, planID, expectedVersion, normalized, policy, s.now(), false)
}

func (s *Service) createOrUpdatePlan(ctx context.Context, actor Actor, actorKind, actorID, planID string, expectedVersion int64, input PlanInput, policy QuestionPolicy, now time.Time, create bool) (PlanView, error) {
	var result PlanView
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if _, err := s.loadAccount(ctx, tx, actor.ID); err != nil {
			return err
		}
		if err := s.validatePlanOwnership(ctx, tx, actor.ID, input); err != nil {
			return err
		}
		var version int64
		if create {
			if _, err := tx.Exec(ctx, `INSERT INTO public.learning_plans
				(id, user_id, name, status, version, priority, created_at, updated_at)
				VALUES ($1, $2, $3, 'active', 0, $4, $5, $5)`,
				planID, actor.ID, input.Name, input.Priority, now); err != nil {
				return errServiceUnavailable
			}
			version = 1
		} else {
			var current int64
			err := tx.QueryRow(ctx, `SELECT version FROM public.learning_plans
				WHERE id = $1 AND user_id = $2 FOR UPDATE`, planID, actor.ID).Scan(&current)
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			if err != nil {
				return errServiceUnavailable
			}
			if current != expectedVersion {
				return errVersionConflict
			}
			version = current + 1
		}
		chapterIDs := make([]string, 0, len(input.Scope.Chapters))
		for _, chapter := range input.Scope.Chapters {
			chapterIDs = append(chapterIDs, chapter.ChapterID)
		}
		revisionID := newID()
		var modelOffering any
		if policy.ModelOfferingID != "" {
			modelOffering = policy.ModelOfferingID
		}
		_, err := tx.Exec(ctx, `INSERT INTO public.learning_plan_revisions
			(id, plan_id, user_id, version, course_ids, scope_kind, chapter_ids, task_types, priority,
			 question_policy_enabled, question_model_offering_id, question_mode,
			 require_manual_confirmation, created_by_kind, created_by_id, created_at)
			VALUES ($1, $2, $3, $4, $5::uuid[], $6, $7::uuid[], $8::text[], $9,
			        $10, $11, $12, $13, $14, $15, $16)`,
			revisionID, planID, actor.ID, version, input.CourseIDs, input.Scope.Kind, chapterIDs,
			input.TaskTypes, input.Priority, policy.Enabled, modelOffering, policy.Mode,
			policy.RequireManualConfirmation, actorKind, actorID, now)
		if err != nil {
			return errServiceUnavailable
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_plans
			SET name = $3, current_revision_id = $4, version = $5, priority = $6, updated_at = $7
			WHERE id = $1 AND user_id = $2`, planID, actor.ID, input.Name, revisionID, version, input.Priority, now)
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return errNotFound
		}
		if err := bindMatchingTasks(ctx, tx, actor.ID); err != nil {
			return err
		}
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "plan.revised", ResourceType: "plan", ResourceID: planID, UserID: actor.ID,
			ResourceVersion: version, Payload: map[string]any{"revision_id": revisionID, "version": version},
		}); err != nil {
			return err
		}
		if actorKind == "admin" {
			if err := writeAudit(ctx, tx, AuditEvent{Actor: actor, Action: "learning.plan.revise",
				ResourceType: "plan", ResourceID: planID, Reason: "管理员保存安排版本"}); err != nil {
				return err
			}
		}
		item, err := s.readPlanTx(ctx, tx, planID, actor.ID)
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	if err != nil {
		return PlanView{}, err
	}
	return result, nil
}

func (s *Service) readPlanTx(ctx context.Context, tx pgx.Tx, planID, userID string) (PlanView, error) {
	item, err := s.scanPlanView(tx.QueryRow(ctx, planViewSelect+` WHERE p.id=$1 AND p.user_id=$2`, planID, userID))
	if err != nil {
		return item, err
	}
	items := []PlanView{item}
	if err := populatePlanChapters(ctx, tx, userID, items); err != nil {
		return PlanView{}, err
	}
	return items[0], nil
}

func (s *Service) ListPlans(ctx context.Context, actor Actor, filter PlanFilter) (Page[PlanView], error) {
	limit := clampLimit(filter.Limit)
	position, err := decodeCursor(filter.Cursor)
	if err != nil {
		return Page[PlanView]{}, err
	}
	rows, err := s.pool.Query(ctx, planViewSelect+`
		WHERE p.user_id=$1 AND ($2::timestamptz IS NULL OR (p.created_at,p.id)<($2::timestamptz,$3::uuid))
		ORDER BY p.created_at DESC,p.id DESC LIMIT $4`, actor.ID, nullableTime(position.Time), nullableID(position.ID), limit+1)
	if err != nil {
		return Page[PlanView]{}, errServiceUnavailable
	}
	defer rows.Close()
	items := make([]PlanView, 0, limit+1)
	for rows.Next() {
		item, err := s.scanPlanView(rows)
		if err != nil {
			return Page[PlanView]{}, err
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		return Page[PlanView]{}, errServiceUnavailable
	}
	page := Page[PlanView]{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	if err := populatePlanChapters(ctx, s.pool, actor.ID, page.Items); err != nil {
		return Page[PlanView]{}, err
	}
	return page, nil
}

func (s *Service) GetPlan(ctx context.Context, actor Actor, planID string) (PlanView, error) {
	var result PlanView
	if !web.ValidID(planID) {
		return result, errNotFound
	}
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		item, err := s.readPlanTx(ctx, tx, planID, actor.ID)
		if err != nil {
			return err
		}
		result = item
		return nil
	})
	if err != nil {
		return PlanView{}, err
	}
	return result, nil
}

type PlanControlResult struct {
	PlanID  string `json:"plan_id"`
	Status  string `json:"status"`
	Applied bool   `json:"applied"`
}

// ControlPlan 幂等启停后续安排；不影响已领取尝试或已确认任务。
func (s *Service) ControlPlan(ctx context.Context, actor Actor, planID, idempotencyKey, action, reason string) (PlanControlResult, error) {
	if !web.ValidID(planID) {
		return PlanControlResult{}, errNotFound
	}
	if !validIdempotencyKey(idempotencyKey) {
		return PlanControlResult{}, validation("必须提供有效的 Idempotency-Key")
	}
	if action != "pause" && action != "resume" {
		return PlanControlResult{}, validation("安排控制仅支持 pause 或 resume")
	}
	if len(strings.TrimSpace(reason)) > 500 {
		return PlanControlResult{}, validation("理由过长")
	}
	hash := requestHash(action, reason)
	var result PlanControlResult
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var planUser string
		if err := tx.QueryRow(ctx, `SELECT user_id::text FROM public.learning_plans WHERE id = $1`, planID).Scan(&planUser); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errNotFound
			}
			return errServiceUnavailable
		}
		if actor.Kind == "user" && planUser != actor.ID {
			return errNotFound
		}
		var commandID, storedStatus, storedHash string
		err := tx.QueryRow(ctx, `SELECT id::text, status, request_hash FROM public.learning_plan_commands
			WHERE plan_id = $1 AND actor_kind = $2 AND actor_id = $3 AND idempotency_key = $4
			FOR UPDATE`, planID, actor.Kind, actor.ID, idempotencyKey).
			Scan(&commandID, &storedStatus, &storedHash)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return errServiceUnavailable
		}
		if commandID != "" {
			if storedHash != hash {
				return errIdempotency
			}
			item, err := s.readPlanTx(ctx, tx, planID, planUser)
			if err != nil {
				return err
			}
			result = PlanControlResult{PlanID: planID, Status: item.Status,
				Applied: storedStatus == "applied"}
			return nil
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM public.learning_plans WHERE id = $1 FOR UPDATE`, planID); err != nil {
			return errServiceUnavailable
		}
		status := "active"
		if action == "pause" {
			status = "paused"
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_plans SET status = $2, updated_at = $3
			WHERE id = $1 AND status <> 'archived'`, planID, status, s.now())
		if err != nil {
			return errServiceUnavailable
		}
		if tag.RowsAffected() != 1 {
			return conflict("已归档安排不能启停")
		}
		// 暂停期间同步发现的新任务不会绑定；恢复时按当前版本补齐关联。
		if status == "active" {
			if err := bindMatchingTasks(ctx, tx, planUser); err != nil {
				return err
			}
		}
		commandID = newID()
		_, err = tx.Exec(ctx, `INSERT INTO public.learning_plan_commands
			(id, plan_id, user_id, actor_kind, actor_id, kind, idempotency_key, request_hash,
			 status, result_code, reason, applied_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'applied', 'ok', $9, $10)`,
			commandID, planID, planUser, actor.Kind, actor.ID, action, idempotencyKey, hash,
			truncate(reason, 500), s.now())
		if err != nil {
			return errServiceUnavailable
		}
		if err := writeOutbox(ctx, tx, outboxEvent{
			Topic: "plan.control_applied", ResourceType: "plan", ResourceID: planID, UserID: planUser,
			Payload: map[string]any{"action": action, "status": status},
		}); err != nil {
			return err
		}
		if actor.Kind == "admin" {
			if err := writeAudit(ctx, tx, AuditEvent{Actor: actor, Action: "learning.plan.control",
				ResourceType: "plan", ResourceID: planID, Reason: reason}); err != nil {
				return err
			}
		}
		result = PlanControlResult{PlanID: planID, Status: status, Applied: true}
		return nil
	})
	if err != nil {
		return PlanControlResult{}, err
	}
	return result, nil
}

func validIdempotencyKey(value string) bool {
	return len(value) >= 8 && len(value) <= 128
}
