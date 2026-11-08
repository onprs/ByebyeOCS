//go:build integration

package learning

import (
	"context"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestSubmittedVerificationRecordsPlatformClosureWithoutConfirmation(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	job := claimOne(t, service)
	quote := `{"amount_minor":37,"price_version":"frozen-before-check"}`
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_tasks SET state='waiting_verification',
		verification_reason='question_submission',verification_attempts=1,billing_quote=$2::jsonb,
		lease_attempt_id=NULL,lease_owner='',lease_expires_at=NULL WHERE id=$1`, taskID, quote); err != nil {
		t.Fatal(err)
	}
	runID := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.learning_work_runs
		(id,task_id,user_id,plan_revision_id,paper_fingerprint,model_offering_id,answer_mode,state,submitted_at,submission_attempt_id,answer_digest)
		VALUES($1,$2,$3,$4,repeat('a',64),'test-model','cache_first','submitted',now(),$5,repeat('b',64))`, runID, taskID, userID, job.PlanRevisionID, job.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := service.applyVerification(context.Background(), taskID, PlatformCheck{Status: CheckUnknown, Source: "chaoxing.work"}, Fail(FailurePlatformClosed, "平台测验当前不可作答")); err != nil {
		t.Fatal(err)
	}
	var state, errorCode, platformStatus, availability, runState string
	var unconfirmed, unscheduled, quotePreserved bool
	if err := pool.QueryRow(context.Background(), `SELECT t.state,t.last_error_code,t.platform_status,v.availability,
		r.state,t.confirmed_at IS NULL,t.next_verify_at IS NULL AND t.next_attempt_at IS NULL,
		t.billing_quote=$2::jsonb
		FROM public.learning_tasks t JOIN public.learning_task_point_availability v
		ON v.user_id=t.user_id AND v.course_id=t.course_id AND v.chapter_external_id=t.chapter_external_id
		AND v.task_point_id=t.task_point_id AND v.cycle_key=t.cycle_key
		JOIN public.learning_work_runs r ON r.task_id=t.id WHERE t.id=$1`, taskID, quote).Scan(&state, &errorCode, &platformStatus, &availability, &runState, &unconfirmed, &unscheduled, &quotePreserved); err != nil {
		t.Fatal(err)
	}
	if state != "needs_attention" || errorCode != "quiz_closed" || platformStatus == "confirmed" || availability != "closed" || runState != "submitted" || !unconfirmed || !unscheduled || !quotePreserved {
		t.Fatalf("关卷核验错误: %s/%s/%s/%s/%s/%t/%t/%t", state, errorCode, platformStatus, availability, runState, unconfirmed, unscheduled, quotePreserved)
	}
	claimed, _, err := service.claimVerification(context.Background())
	if err != nil || claimed != "" {
		t.Fatalf("关卷后仍领取重复核验: %q/%v", claimed, err)
	}
}
