package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// WorkQueryResult 引用题库模块已经持久化的结果，不重复保存原题和答案。
type WorkQueryResult struct {
	QueryID string
	Answer  string
}

type WorkAnswerer interface {
	AnswerWorkQuestion(ctx context.Context, userID, idempotencyKey string, policy QuestionPolicy, question WorkQuestion) (WorkQueryResult, error)
}

type AutomaticExecutorOptions struct {
	Enabled         bool
	Platform        WorkPlatform
	Answerer        WorkAnswerer
	Fallback        Executor
	BeforeExternal  func(context.Context) error
	AcquireQuestion func(context.Context) (func(), error)
}

// AutomaticExecutor 在同一调度租约下运行视频执行器与全自动章节测验。
// 模型查询使用稳定幂等键，最终提交先写入持久状态，再发起一次 POST。
type AutomaticExecutor struct {
	pool *pgxpool.Pool
	opts AutomaticExecutorOptions
}

func NewAutomaticExecutor(pool *pgxpool.Pool, options AutomaticExecutorOptions) (*AutomaticExecutor, error) {
	if pool == nil || options.Platform == nil || options.Answerer == nil {
		return nil, validation("自动做题要求数据库、平台适配器和题库服务")
	}
	return &AutomaticExecutor{pool: pool, opts: options}, nil
}

func (e *AutomaticExecutor) Capabilities() ExecutorCapabilities {
	result := ExecutorCapabilities{Name: "chaoxing-http-automatic", Enabled: e.opts.Enabled,
		TaskKinds: []string{"quiz"}, SafeLeaseRecovery: true}
	if e.opts.Fallback != nil {
		fallback := e.opts.Fallback.Capabilities()
		if fallback.Enabled {
			result.TaskKinds = append(result.TaskKinds, fallback.TaskKinds...)
			result.SafeLeaseRecovery = fallback.SafeLeaseRecovery
			result.ProducesMediaEvidence = fallback.ProducesMediaEvidence
		}
	}
	if !result.Enabled {
		result.DisabledReason = "自动执行尚未启用"
	}
	return result
}

func (e *AutomaticExecutor) Execute(ctx context.Context, req ExecutionRequest) (ExecutionResult, error) {
	if !e.opts.Enabled {
		return ExecutionResult{}, Fail(FailureCapabilityMissing, "自动执行尚未启用")
	}
	if req.TaskKind != "quiz" {
		if e.opts.Fallback != nil && e.opts.Fallback.Capabilities().Supports(req.TaskKind) {
			return e.opts.Fallback.Execute(ctx, req)
		}
		return ExecutionResult{}, Fail(FailureCapabilityMissing, "当前任务类型没有执行器")
	}
	if req.StopRequested || ctx.Err() != nil {
		return ExecutionResult{StopAcknowledged: true}, nil
	}
	if !req.QuestionPolicy.Enabled || req.QuestionPolicy.ModelOfferingID == "" {
		return ExecutionResult{}, Fail(FailureCapabilityMissing, "安排尚未启用题库模型")
	}
	if req.QuestionPolicy.Mode != "cache_first" && req.QuestionPolicy.Mode != "always_llm" {
		return ExecutionResult{}, Fail(FailureInvalidTask, "安排的作答模式无效")
	}
	if err := e.beforeExternal(ctx, req); err != nil {
		return ExecutionResult{}, err
	}
	if scoped, ok := e.opts.Platform.(WorkSessionPlatform); ok {
		var cleanup func()
		var err error
		ctx, cleanup, err = scoped.BeginWorkSession(ctx, req.Session)
		if err != nil {
			return ExecutionResult{}, err
		}
		defer cleanup()
	}
	sheet, err := e.opts.Platform.ReadWork(ctx, req.Session, req.Task)
	if err != nil {
		return ExecutionResult{}, err
	}
	if sheet.Submitted {
		return workCompleted("平台显示已提交，等待核验"), nil
	}
	if err := validateWorkSheet(sheet); err != nil {
		return ExecutionResult{}, err
	}
	run, err := e.prepareRun(ctx, req, sheet.Fingerprint)
	if err != nil {
		return ExecutionResult{}, err
	}
	if run.State != "querying" {
		if run.State == "submitted" {
			return workCompleted("答案已提交，等待平台核验"), nil
		}
		return ExecutionResult{}, Fail(FailureSubmissionUnknown, "前次提交结果尚未确认，正在核验")
	}

	answers := make([]WorkAnswer, 0, len(sheet.Questions))
	for index, question := range sheet.Questions {
		questionHash := sha256.Sum256([]byte(question.ID))
		key := "learning-quiz/" + run.ID + "/" + hex.EncodeToString(questionHash[:])
		response, err := e.queryQuestion(ctx, req, key, question)
		if err != nil {
			return ExecutionResult{}, err
		}
		answer, err := MapWorkAnswer(question, response.Answer)
		if err != nil {
			return ExecutionResult{}, err
		}
		if response.QueryID == "" || len(response.QueryID) > 128 {
			return ExecutionResult{}, Fail(FailureAnswerUnavailable, "题库结果缺少可追溯的查询记录")
		}
		if err := e.recordAnswer(ctx, req, run.ID, response.QueryID, index+1, len(sheet.Questions)); err != nil {
			return ExecutionResult{}, err
		}
		answers = append(answers, answer)
	}
	if err := e.beforeExternal(ctx, req); err != nil {
		return ExecutionResult{}, err
	}
	begun := false
	response, submitErr := e.opts.Platform.SubmitWork(ctx, req.Session, req.Task, sheet.Fingerprint, answers, func(postCtx context.Context) error {
		if err := e.beforeExternal(postCtx, req); err != nil {
			return err
		}
		if err := e.beginSubmission(postCtx, req, run.ID, answers); err != nil {
			return err
		}
		begun = true
		return nil
	})
	if !begun {
		if submitErr != nil {
			return ExecutionResult{}, submitErr
		}
		if response.AlreadySubmitted {
			return workCompleted("平台显示已提交，等待核验"), nil
		}
		return ExecutionResult{}, Fail(FailureSubmissionUnknown, "平台提交缺少持久状态，正在核验")
	}
	state := "submitted"
	if submitErr != nil || (!response.Accepted && !response.AlreadySubmitted) {
		state = "submission_unknown"
		var failure *Failure
		if errors.As(submitErr, &failure) && failure.Class != FailureSubmissionUnknown {
			// 适配器只有在能确认没有产生最终提交时，才报告已知失败。
			state = "querying"
		}
	}
	// POST 已经结束时，即使原租约被取消，也要尽力保留实际提交事实。
	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = e.finishSubmission(persistCtx, req, run.ID, state)
	cancel()
	if err != nil {
		return ExecutionResult{}, Fail(FailureSubmissionUnknown, "提交状态尚未保存，正在核验平台结果")
	}
	if state == "submission_unknown" {
		return ExecutionResult{}, Fail(FailureSubmissionUnknown, "提交结果尚未明确，正在核验")
	}
	if submitErr != nil {
		return ExecutionResult{}, submitErr
	}
	return workCompleted("答案已提交，等待平台核验"), nil
}

func (e *AutomaticExecutor) queryQuestion(ctx context.Context, req ExecutionRequest, key string, question WorkQuestion) (WorkQueryResult, error) {
	if e.opts.AcquireQuestion != nil {
		release, err := e.opts.AcquireQuestion(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return WorkQueryResult{}, Fail(FailureStopped, "做题任务已停止")
			}
			return WorkQueryResult{}, Fail(FailureCapabilityMissing, "当前运行状态暂不允许题库请求")
		}
		defer release()
	}
	// 等待名额期间可能发生暂停、闭户或租约变更，付费调用前必须重新核对。
	if err := e.beforeExternal(ctx, req); err != nil {
		return WorkQueryResult{}, err
	}
	return e.opts.Answerer.AnswerWorkQuestion(ctx, req.UserID, key, req.QuestionPolicy, question)
}

func workCompleted(note string) ExecutionResult {
	fraction := 1.0
	return ExecutionResult{Completed: true, Observation: Observation{Fraction: &fraction,
		Source: "chaoxing-question", ObservedAt: time.Now().UTC(), Note: note}, Media: unavailableMedia()}
}

func validateWorkSheet(sheet WorkSheet) error {
	digest, err := hex.DecodeString(sheet.Fingerprint)
	if err != nil || len(digest) != sha256.Size || strings.ToLower(sheet.Fingerprint) != sheet.Fingerprint {
		return Fail(FailureInvalidTask, "平台试卷缺少有效指纹")
	}
	if len(sheet.Questions) < 1 || len(sheet.Questions) > 100 {
		return Fail(FailureCapabilityMissing, "本次试卷题目数量不在支持范围")
	}
	seen := map[string]bool{}
	totalImageBytes := 0
	for _, question := range sheet.Questions {
		if question.ID == "" || len(question.ID) > 128 || seen[question.ID] || strings.TrimSpace(question.Title) == "" {
			return Fail(FailureInvalidTask, "平台题目编号或内容无效")
		}
		seen[question.ID] = true
		switch question.Type {
		case "single", "multiple":
			if len(question.Options) < 2 || len(question.Options) > 26 {
				return Fail(FailureCapabilityMissing, "选择题选项数量不在支持范围")
			}
		case "judgement":
			if len(question.Options) != 0 && len(question.Options) != 2 {
				return Fail(FailureInvalidTask, "判断题选项数量不明确")
			}
		case "shortanswer":
		case "completion":
			if question.Blanks < 1 || question.Blanks > 64 {
				return Fail(FailureCapabilityMissing, "填空题空格数量不在支持范围")
			}
		default:
			return Fail(FailureCapabilityMissing, "本次试卷包含尚未支持的题型")
		}
		if len(question.Images) > 8 {
			return Fail(FailureCapabilityMissing, "单题图片数量超出支持范围")
		}
		for _, image := range question.Images {
			totalImageBytes += len(image.Base64)
			if totalImageBytes > (16<<20)*4/3+8 {
				return Fail(FailureCapabilityMissing, "试卷图片总体积超出支持范围")
			}
		}
		labels, values := map[string]bool{}, map[string]bool{}
		for _, option := range question.Options {
			label := strings.ToUpper(option.Label)
			if label == "" || option.Value == "" || labels[label] || values[option.Value] {
				return Fail(FailureInvalidTask, "平台选项标识缺失或重复")
			}
			labels[label], values[option.Value] = true, true
		}
	}
	return nil
}

func (e *AutomaticExecutor) beforeExternal(ctx context.Context, req ExecutionRequest) error {
	if ctx.Err() != nil {
		return Fail(FailureStopped, "做题任务已停止")
	}
	if e.opts.BeforeExternal != nil {
		if err := e.opts.BeforeExternal(ctx); err != nil {
			return Fail(FailureCapabilityMissing, "当前运行状态暂不允许新平台请求")
		}
	}
	return e.withWorkTx(ctx, func(tx pgx.Tx) error { return e.checkLease(ctx, tx, req) })
}

func (e *AutomaticExecutor) withWorkTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return Fail(FailureTransient, "自动做题状态暂不可读取")
	}
	defer rollbackLearningTx(tx)
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return Fail(FailureTransient, "自动做题状态暂不可保存")
	}
	return nil
}

func (e *AutomaticExecutor) checkLease(ctx context.Context, tx pgx.Tx, req ExecutionRequest) error {
	if !web.ValidID(req.TaskID) || !web.ValidID(req.UserID) || !web.ValidID(req.AttemptID) || !web.ValidID(req.PlanRevision) {
		return Fail(FailureInvalidTask, "自动做题缺少有效任务上下文")
	}
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT
		t.state IN ('preparing','running') AND t.desired_state = 'run' AND t.stop_requested_at IS NULL
		AND t.lease_attempt_id = a.id AND t.fence_token = $4 AND a.fence_token = $4
		AND a.plan_revision_id = $5 AND a.ended_at IS NULL AND NOT a.cancel_requested
		AND t.lease_expires_at > now() AND a.lease_expires_at > now()
		AND u.status = 'active' AND ac.blocked_code = ''
		AND (ac.blocked_until IS NULL OR ac.blocked_until <= now())
		FROM public.learning_tasks t
		JOIN public.learning_task_attempts a ON a.task_id=t.id AND a.user_id=t.user_id AND a.id=$3
		JOIN public.users u ON u.id=t.user_id
		JOIN public.learning_accounts ac ON ac.user_id=t.user_id
		WHERE t.id=$1 AND t.user_id=$2 FOR UPDATE OF t,a`,
		req.TaskID, req.UserID, req.AttemptID, req.FenceToken, req.PlanRevision).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !allowed) {
		return Fail(FailureStopped, "做题任务的授权或租约已结束")
	}
	if err != nil {
		return Fail(FailureTransient, "无法核对做题任务租约")
	}
	return nil
}

type workRun struct {
	ID    string
	State string
}

func (e *AutomaticExecutor) prepareRun(ctx context.Context, req ExecutionRequest, fingerprint string) (workRun, error) {
	var run workRun
	err := e.withWorkTx(ctx, func(tx pgx.Tx) error {
		if err := e.checkLease(ctx, tx, req); err != nil {
			return err
		}
		// 先核对整个任务的最终提交事实，不能通过新安排版本绕过提交幂等。
		err := tx.QueryRow(ctx, `SELECT id::text,state FROM public.learning_work_runs
			WHERE task_id=$1 AND user_id=$2 AND state IN ('submitting','submitted','submission_unknown')`,
			req.TaskID, req.UserID).Scan(&run.ID, &run.State)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return Fail(FailureTransient, "无法核对既有提交状态")
		}
		_, err = tx.Exec(ctx, `INSERT INTO public.learning_work_runs
			(id,task_id,user_id,plan_revision_id,paper_fingerprint,model_offering_id,answer_mode)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (task_id,plan_revision_id,paper_fingerprint) DO NOTHING`, newID(), req.TaskID,
			req.UserID, req.PlanRevision, fingerprint, req.QuestionPolicy.ModelOfferingID, req.QuestionPolicy.Mode)
		if err != nil {
			return Fail(FailureTransient, "无法建立自动做题记录")
		}
		var modelID, mode string
		err = tx.QueryRow(ctx, `SELECT id::text,state,model_offering_id,answer_mode FROM public.learning_work_runs
			WHERE task_id=$1 AND user_id=$2 AND plan_revision_id=$3 AND paper_fingerprint=$4`,
			req.TaskID, req.UserID, req.PlanRevision, fingerprint).Scan(&run.ID, &run.State, &modelID, &mode)
		if err != nil {
			return Fail(FailureTransient, "无法读取自动做题记录")
		}
		if modelID != req.QuestionPolicy.ModelOfferingID || mode != req.QuestionPolicy.Mode {
			return Fail(FailureInvalidTask, "自动做题策略与冻结版本不一致")
		}
		return nil
	})
	return run, err
}

func (e *AutomaticExecutor) recordAnswer(ctx context.Context, req ExecutionRequest, runID, queryID string, count, total int) error {
	return e.withWorkTx(ctx, func(tx pgx.Tx) error {
		if err := e.checkLease(ctx, tx, req); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_work_runs SET
			query_ids=CASE WHEN $2=ANY(query_ids) THEN query_ids ELSE array_append(query_ids,$2) END,
			updated_at=now() WHERE id=$1 AND user_id=$3 AND state='querying'`, runID, queryID, req.UserID)
		if err != nil || tag.RowsAffected() != 1 {
			return Fail(FailureTransient, "无法保存题目查询进度")
		}
		fraction := float64(count) / float64(total)
		observation, _ := json.Marshal(Observation{Fraction: &fraction, Source: "chaoxing-question",
			ObservedAt: time.Now().UTC(), Note: fmt.Sprintf("已获取 %d/%d 题答案", count, total)})
		if _, err := tx.Exec(ctx, `UPDATE public.learning_tasks SET state='running',observation=$2,
			version=version+1,updated_at=now() WHERE id=$1`, req.TaskID, observation); err != nil {
			return Fail(FailureTransient, "无法保存自动做题进度")
		}
		return writeOutbox(ctx, tx, outboxEvent{Topic: "task.question_progress", ResourceType: "task",
			ResourceID: req.TaskID, UserID: req.UserID,
			Payload: map[string]any{"answered": count, "total": total, "state": StateRunning}})
	})
}

func (e *AutomaticExecutor) beginSubmission(ctx context.Context, req ExecutionRequest, runID string, answers []WorkAnswer) error {
	raw, err := json.Marshal(answers)
	if err != nil {
		return Fail(FailureInvalidTask, "无法建立提交摘要")
	}
	digest := sha256.Sum256(raw)
	return e.withWorkTx(ctx, func(tx pgx.Tx) error {
		if err := e.checkLease(ctx, tx, req); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM public.learning_work_runs
			WHERE task_id=$1 AND state IN ('submitting','submitted','submission_unknown'))`, req.TaskID).Scan(&exists); err != nil {
			return Fail(FailureTransient, "无法核对提交独占状态")
		}
		if exists {
			return Fail(FailureSubmissionUnknown, "该任务已有最终提交记录，正在核验")
		}
		tag, err := tx.Exec(ctx, `UPDATE public.learning_work_runs SET state='submitting',
			answer_digest=$2,submission_attempt_id=$3,updated_at=now()
			WHERE id=$1 AND user_id=$4 AND task_id=$5 AND state='querying'`, runID,
			hex.EncodeToString(digest[:]), req.AttemptID, req.UserID, req.TaskID)
		if err != nil || tag.RowsAffected() != 1 {
			return Fail(FailureTransient, "无法保存最终提交状态")
		}
		return writeOutbox(ctx, tx, outboxEvent{Topic: "task.question_submitting", ResourceType: "task",
			ResourceID: req.TaskID, UserID: req.UserID, Payload: map[string]any{"state": StateRunning, "phase": "submitting"}})
	})
}

func (e *AutomaticExecutor) finishSubmission(ctx context.Context, req ExecutionRequest, runID, state string) error {
	tag, err := e.pool.Exec(ctx, `UPDATE public.learning_work_runs SET state=$2,updated_at=now(),
		submitted_at=CASE WHEN $2='submitted' THEN now() ELSE submitted_at END
		WHERE id=$1 AND user_id=$3 AND submission_attempt_id=$4 AND state='submitting'`,
		runID, state, req.UserID, req.AttemptID)
	if err != nil || tag.RowsAffected() != 1 {
		return Fail(FailureSubmissionUnknown, "无法保存实际提交状态")
	}
	return nil
}
