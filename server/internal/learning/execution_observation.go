package learning

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"time"
)

// recordExecutionObservation 在当前租约内记录执行阶段，不改写独立平台结论或账本。
func (s *Service) recordExecutionObservation(ctx context.Context, job executionJob, observation Observation) error {
	observeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	body, _ := evidenceJSON(ExecutionResult{Observation: observation})
	return s.withTx(observeCtx, func(tx pgx.Tx) error {
		var version int64
		err := tx.QueryRow(observeCtx, `UPDATE public.learning_tasks SET state='running',
   observation=CASE WHEN $4::jsonb='{}'::jsonb THEN observation ELSE $4::jsonb END,version=version+1,updated_at=$5
   WHERE id=$1 AND lease_attempt_id=$2 AND fence_token=$3 AND desired_state='run'
   AND state IN('preparing','running') AND stop_requested_at IS NULL RETURNING version`, job.TaskID, job.AttemptID, job.FenceToken, body, s.now()).Scan(&version)
		if errors.Is(err, pgx.ErrNoRows) {
			return Fail(FailureStopped, "任务租约或运行状态已变化")
		}
		if err != nil {
			return errServiceUnavailable
		}
		if _, err = tx.Exec(observeCtx, `UPDATE public.learning_task_attempts SET state='running',observation=$2 WHERE id=$1 AND ended_at IS NULL`, job.AttemptID, body); err != nil {
			return errServiceUnavailable
		}
		return writeOutbox(observeCtx, tx, outboxEvent{Topic: "task.observation_updated", ResourceType: "task", ResourceID: job.TaskID, UserID: job.UserID, ResourceVersion: version, Payload: map[string]any{"state": StateRunning}})
	})
}
