//go:build integration

package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func closureFixture(t *testing.T) (*Service, web.Actor) {
	t.Helper()
	pool := testsupport.NewPool(t)
	service, err := New(pool, Options{DataDir: t.TempDir(), Key: bytes.Repeat([]byte{11}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Prepare(); err != nil {
		t.Fatal(err)
	}
	adminID := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.admin_users(id,username,password_hash)
		VALUES($1,$2,'$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g')`,
		adminID, "admin-"+adminID[:8]); err != nil {
		t.Fatal(err)
	}
	return service, web.Actor{
		ID: adminID, Kind: "admin", SessionID: web.NewID(),
		Permissions: []string{"privacy.review"}, AuthenticatedAt: time.Now(),
	}
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func seedUser(t *testing.T, service *Service, status string) string {
	t.Helper()
	userID := web.NewID()
	if _, err := service.pool.Exec(context.Background(),
		`INSERT INTO public.users(id,status) VALUES($1,$2)`, userID, status); err != nil {
		t.Fatal(err)
	}
	return userID
}

func seedDeletionRequest(t *testing.T, service *Service, userID, status string) string {
	t.Helper()
	id := web.NewID()
	if _, err := service.pool.Exec(context.Background(),
		`INSERT INTO public.data_requests(id,user_id,kind,status) VALUES($1,$2,'deletion',$3)`,
		id, userID, status); err != nil {
		t.Fatal(err)
	}
	return id
}

func seedWallet(t *testing.T, service *Service, userID string, available, reserved, accrued int64, frozen bool) string {
	t.Helper()
	walletID := web.NewID()
	var frozenReason any
	if frozen {
		frozenReason = "风险冻结"
	}
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.wallets
		(id,user_id,available_minor,reserved_minor,tiku_accrued_micros,frozen,frozen_reason)
		VALUES($1,$2,$3,$4,$5,$6,$7)`,
		walletID, userID, available, reserved, accrued, frozen, frozenReason); err != nil {
		t.Fatal(err)
	}
	return walletID
}

// seedIdentity 建立最小外部身份；密文长度满足约束，subject_index 保留为 HMAC 样式索引。
func seedIdentity(t *testing.T, service *Service, userID, status string) (identityID, subjectIndex string) {
	t.Helper()
	identityID = web.NewID()
	subjectIndex = digest("subject:" + userID)
	revokedAt := any(nil)
	if status == "revoked" {
		revokedAt = time.Now()
	}
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.external_identities
		(id,user_id,provider,namespace,subject_index,subject_ciphertext,subject_key_version,
		 display_name,school,status,verified_at,revoked_at)
		VALUES($1,$2,'chaoxing','chaoxing-session-v1',$3,$4,1,'合成学生','示例学校',$5,now(),$6)`,
		identityID, userID, subjectIndex, bytes.Repeat([]byte{0x41}, 48), status, revokedAt); err != nil {
		t.Fatal(err)
	}
	return identityID, subjectIndex
}

func seedAuthSecrets(t *testing.T, service *Service, userID, identityID string) {
	t.Helper()
	platformID := web.NewID()
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.platform_sessions
		(id,user_id,external_identity_id,key_version,ciphertext,expires_at)
		VALUES($1,$2,$3,1,$4,now()+interval '2 hours')`,
		platformID, userID, identityID, bytes.Repeat([]byte{0x42}, 48)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.client_sessions
		(id,user_id,platform_session_id,channel,token_hash,csrf_hash,status,authenticated_at,
		 created_at,updated_at,last_seen_at,expires_at,absolute_expires_at)
		VALUES($1,$2,$3,'web',$4,$5,'active',now(),now(),now(),now(),now()+interval '1 hour',now()+interval '1 hour')`,
		web.NewID(), userID, platformID, digest("token:"+userID), digest("csrf:"+userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(context.Background(),
		`INSERT INTO public.user_preferences(user_id) VALUES($1)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.login_challenges
		(id,method,client_channel,binding_hash,client_key_hash,status,attempts,max_attempts,
		 created_at,expires_at,verified_at,consumed_at,user_id,external_identity_id)
		VALUES($1,'password','web',$2,$3,'consumed',1,5,now(),now()+interval '5 minutes',now(),now(),$4,$5)`,
		web.NewID(), digest("binding:"+userID), digest("client:"+userID), userID, identityID); err != nil {
		t.Fatal(err)
	}
}

type learningSeed struct {
	TaskID string
	PlanID string
}

func seedLearning(t *testing.T, service *Service, userID, taskState, billingState string, activeAttempt bool) learningSeed {
	t.Helper()
	ctx := context.Background()
	fingerprint := digest("learning:" + userID)
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_accounts
		(user_id,provider,identity_namespace,identity_fingerprint,school,display_name,
		 blocked_code,blocked_reason,blocked_at,verified_at)
		VALUES($1,'chaoxing','chaoxing-session-v1',$2,'示例学校','合成学生',
		 'deletion_requested','用户申请删除',now(),now())`, userID, fingerprint); err != nil {
		t.Fatal(err)
	}
	courseID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_courses
		(id,user_id,provider,identity_namespace,identity_fingerprint,external_course_id,
		 external_class_id,title,teacher,term)
		VALUES($1,$2,'chaoxing','chaoxing-session-v1',$3,$4,'class-1','课程标题','教师','2026 春')`,
		courseID, userID, fingerprint, "course-"+userID); err != nil {
		t.Fatal(err)
	}
	chapterID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_chapters
		(id,course_id,user_id,snapshot_version,external_id,title)
		VALUES($1,$2,$3,1,'chapter-1','第一章')`, chapterID, courseID, userID); err != nil {
		t.Fatal(err)
	}
	attachmentID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_attachments
		(id,course_id,chapter_id,user_id,snapshot_version,external_object_id,task_point_id,
		 kind,title,is_task_point,counted_incomplete)
		VALUES($1,$2,$3,$4,1,'object-1','tp-1','video','附件标题',true,false)`,
		attachmentID, courseID, chapterID, userID); err != nil {
		t.Fatal(err)
	}
	planID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_plans
		(id,user_id,name,status) VALUES($1,$2,'我的安排','paused')`, planID, userID); err != nil {
		t.Fatal(err)
	}
	taskID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_tasks
		(id,user_id,course_id,chapter_id,attachment_id,provider,identity_namespace,
		 identity_fingerprint,cycle_key,course_external_id,class_external_id,chapter_external_id,
		 task_point_id,task_kind,title,state,desired_state,observation,billing_state)
		VALUES($1,$2,$3,$4,$5,'chaoxing','chaoxing-session-v1',$6,'current',$7,'class-1','chapter-1',
		 'tp-1','video','任务标题',$8,'pause','{"progress":0.5,"source":"executor"}',$9)`,
		taskID, userID, courseID, chapterID, attachmentID, fingerprint, "course-"+userID, taskState, billingState); err != nil {
		t.Fatal(err)
	}
	attemptState := "ended"
	var endedAt any = time.Now()
	result := "stopped"
	if activeAttempt {
		attemptState = "running"
		endedAt = nil
		result = ""
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_task_attempts
		(id,task_id,user_id,attempt_no,owner,fence_token,state,lease_expires_at,started_at,
		 ended_at,observation,error_code,result)
		VALUES($1,$2,$3,1,'worker-1',1,$4,now()+interval '1 hour',now(),$5,
		 '{"platform_text":"合成观察"}','E_TEST',$6)`,
		web.NewID(), taskID, userID, attemptState, endedAt, result); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_task_commands
		(id,task_id,user_id,actor_kind,kind,idempotency_key,request_hash,status,reason,applied_at)
		VALUES($1,$2,$3,'system','pause',$4,$5,'applied','用户自定义备注',now())`,
		web.NewID(), taskID, userID, "task-cmd-"+userID, digest("task-cmd:"+userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_plan_commands
		(id,plan_id,user_id,actor_kind,kind,idempotency_key,request_hash,status,reason,applied_at)
		VALUES($1,$2,$3,'system','pause',$4,$5,'applied','安排备注',now())`,
		web.NewID(), planID, userID, "plan-cmd-"+userID, digest("plan-cmd:"+userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_course_syncs
		(id,user_id,dedupe_key,status,requested_by_kind,error_code,finished_at)
		VALUES($1,$2,$3,'succeeded','system','旧错误',now())`,
		web.NewID(), userID, "sync-"+userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_outbox
		(event_id,topic,resource_type,resource_id,user_id,resource_version,payload)
		VALUES($1,'task.updated','task',$2,$3,1,'{"title":"任务标题"}')`,
		web.NewID(), taskID, userID); err != nil {
		t.Fatal(err)
	}
	return learningSeed{TaskID: taskID, PlanID: planID}
}

func seedQuestion(t *testing.T, service *Service, userID, status, billingState string) string {
	t.Helper()
	ctx := context.Background()
	offeringID := web.NewID()
	publicID := "qm_" + digest("offering:" + userID)[:32]
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.question_offerings
		(id,public_id,display_name,provider_ref) VALUES($1,$2,'标准模型','ref-1')`,
		offeringID, publicID); err != nil {
		t.Fatal(err)
	}
	queryID := "qq_" + digest("query:" + userID)[:32]
	reserved := int64(0)
	if billingState == "reserved" {
		reserved = 10
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.question_queries
		(id,user_id,idempotency_key,request_fingerprint,offering_id,offering_public_id,
		 offering_display_name,mode,cache_scope,scope_key_hash,question_title,question_options,
		 question_type,status,answer,billing_state,reserved_minor,fork_request_id)
		VALUES($1,$2,$3,$4,$5,$6,'标准模型','cache_first','user',$7,'原题标题','A. 选项一',
		 'single',$8,'A',$9,$10,$11)`,
		queryID, userID, "idem-"+userID, digest("fingerprint:"+userID), offeringID, publicID,
		"scope-"+userID, status, billingState, reserved, "fork-"+userID); err != nil {
		t.Fatal(err)
	}
	return queryID
}

func seedExport(t *testing.T, service *Service, userID string) {
	t.Helper()
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.data_requests
		(id,user_id,kind,status,result_ciphertext,expires_at)
		VALUES($1,$2,'export','ready',$3,now()+interval '1 day')`,
		web.NewID(), userID, bytes.Repeat([]byte{0x44}, 48)); err != nil {
		t.Fatal(err)
	}
}

func seedOutbox(t *testing.T, service *Service, userID, taskID string) {
	t.Helper()
	ctx := context.Background()
	eventID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.outbox_events
		(id,audience,user_id,event_type,resource_id,resource_version,payload)
		VALUES($1,'user',$2,'task.updated',$3,1,'{"title":"任务标题"}')`,
		eventID, userID, taskID); err != nil {
		t.Fatal(err)
	}
	streamKey := "user/" + userID
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.stream_heads(stream_key) VALUES($1)`, streamKey); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.stream_entries
		(stream_key,sequence,event_id,event_type,resource_id,resource_version,payload)
		VALUES($1,1,$2,'task.updated',$3,1,'{"title":"任务标题"}')`,
		streamKey, eventID, taskID); err != nil {
		t.Fatal(err)
	}
}

func seedSettledOrder(t *testing.T, service *Service, userID, walletID string) string {
	t.Helper()
	orderID := web.NewID()
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.recharge_orders
		(id,user_id,wallet_id,amount_minor,channel,status,payment_status,credit_status,
		 merchant_order_no,idempotency_key,request_digest,expires_at,paid_at,credited_at)
		VALUES($1,$2,$3,1000,'alipay','completed','paid','credited',$4,$5,$6,
		 now()+interval '1 hour',now(),now())`,
		orderID, userID, walletID, strconv.FormatInt(time.Now().UnixNano(), 10),
		"order-"+userID, digest("order:"+userID)); err != nil {
		t.Fatal(err)
	}
	return orderID
}

func seedJournal(t *testing.T, service *Service, userID, walletID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	journalID := web.NewID()
	if _, err := tx.Exec(ctx, `INSERT INTO public.journals
		(id,kind,business_key,request_digest,user_id,wallet_id,currency,amount_minor,reason,actor_kind)
		VALUES($1,'manual_credit',$2,$3,$4,$5,'CNY',100,'关闭测试账本','system')`,
		journalID, "closure-journal-"+userID, digest("journal:"+userID), userID, walletID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.postings(id,journal_id,account_id,amount_minor)
		VALUES($1,$2,'00000000-0000-4000-8000-000005000001',100)`, web.NewID(), journalID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.postings(id,journal_id,account_id,amount_minor)
		VALUES($1,$2,'00000000-0000-4000-8000-000005000002',-100)`, web.NewID(), journalID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func seedSettledUsage(t *testing.T, service *Service, userID string) {
	t.Helper()
	if _, err := service.pool.Exec(context.Background(), `INSERT INTO public.usage_records
		(id,user_id,scope,stable_key,status,request_digest,quantity_scaled,usage_complete,settled_at)
		VALUES($1,$2,'service_task_point',$3,'settled',$4,1,true,now())`,
		web.NewID(), userID, "usage-"+userID, digest("usage:"+userID)); err != nil {
		t.Fatal(err)
	}
}

func reviewRequest(service *Service, actor web.Actor, requestID, body string) *httptest.ResponseRecorder {
	return operationRequest(service, actor, "POST", "/admin/api/v1/data-requests/"+requestID+"/review", body)
}

func decodeBlockers(t *testing.T, recorder *httptest.ResponseRecorder) (string, closureBlockers) {
	t.Helper()
	var payload struct {
		Error    map[string]any  `json:"error"`
		Blockers closureBlockers `json:"blockers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("阻断响应不是 JSON：%v", err)
	}
	code, _ := payload.Error["code"].(string)
	return code, payload.Blockers
}

func expectBlocked(t *testing.T, service *Service, actor web.Actor, userID, requestID string, check func(closureBlockers) bool) {
	t.Helper()
	recorder := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"核对资金后完成删除"}`)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("未阻断完成：%d %s", recorder.Code, recorder.Body.String())
	}
	code, blockers := decodeBlockers(t, recorder)
	if code != "privacy_blocked" {
		t.Fatalf("阻断错误码为 %s", code)
	}
	if !check(blockers) {
		t.Fatalf("阻断明细不符合预期：%+v", blockers)
	}
	var status, errorCode, userStatus string
	if err := service.pool.QueryRow(context.Background(), `SELECT status, COALESCE(error_code,'') FROM public.data_requests WHERE id=$1`, requestID).
		Scan(&status, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" || errorCode != "closure_blocked" {
		t.Fatalf("阻断后申请状态为 %s/%s", status, errorCode)
	}
	if err := service.pool.QueryRow(context.Background(), `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil {
		t.Fatal(err)
	}
	if userStatus != "deletion_requested" {
		t.Fatalf("阻断后用户状态变为 %s", userStatus)
	}
	var closed int
	if err := service.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.privacy_closures`).Scan(&closed); err != nil || closed != 0 {
		t.Fatal("阻断时写入了关闭标记")
	}
	var ciphertext []byte
	if err := service.pool.QueryRow(context.Background(), `SELECT subject_ciphertext FROM public.external_identities WHERE user_id=$1`, userID).
		Scan(&ciphertext); err != nil || len(ciphertext) == 0 {
		t.Fatal("阻断时清除了主体密文")
	}
}

func TestPrivacyDeletionFinancialAndActivityBlockers(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()

	type blockerCase struct {
		name  string
		seed  func(userID string)
		check func(closureBlockers) bool
	}
	cases := []blockerCase{
		{"wallet", func(userID string) {
			seedWallet(t, service, userID, 1, 0, 0, false)
		}, func(b closureBlockers) bool { return b.WalletBalances }},
		{"frozen_wallet", func(userID string) {
			seedWallet(t, service, userID, 0, 0, 0, true)
		}, func(b closureBlockers) bool { return b.WalletBalances }},
		{"accrued_wallet", func(userID string) {
			seedWallet(t, service, userID, 0, 0, 1, false)
		}, func(b closureBlockers) bool { return b.WalletBalances }},
		{"open_hold", func(userID string) {
			walletID := seedWallet(t, service, userID, 0, 10, 0, false)
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.holds
				(id,user_id,wallet_id,business_key,request_digest,amount_minor,status)
				VALUES($1,$2,$3,$4,$5,10,'open')`,
				web.NewID(), userID, walletID, "hold-"+userID, digest("hold:"+userID)); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.OpenHolds == 1 }},
		{"pending_order", func(userID string) {
			walletID := seedWallet(t, service, userID, 0, 0, 0, false)
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.recharge_orders
				(id,user_id,wallet_id,amount_minor,channel,status,payment_status,credit_status,
				 merchant_order_no,idempotency_key,request_digest,expires_at)
				VALUES($1,$2,$3,100,'alipay','pending_payment','unpaid','not_credited',$4,$5,$6,
				 now()+interval '1 hour')`,
				web.NewID(), userID, walletID, strconv.FormatInt(time.Now().UnixNano(), 10),
				"pending-"+userID, digest("pending:"+userID)); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.UnsettledOrders == 1 }},
		{"pending_refund", func(userID string) {
			walletID := seedWallet(t, service, userID, 0, 0, 0, false)
			orderID := seedSettledOrder(t, service, userID, walletID)
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.refunds
				(id,order_id,user_id,wallet_id,kind,amount_minor,status,reason,provider_refund_no,
				 requested_by,idempotency_key,request_digest)
				VALUES($1,$2,$3,$4,'manual_correction',10,'requested','测试退款',$5,$6,$7,$8)`,
				web.NewID(), orderID, userID, walletID, "refund-"+userID, actor.ID,
				"refund-idem-"+userID, digest("refund:"+userID)); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.OpenRefunds == 1 }},
		{"unsettled_usage", func(userID string) {
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.usage_records
				(id,user_id,scope,stable_key,status,request_digest,quantity_scaled)
				VALUES($1,$2,'service_task_point',$3,'reserved',$4,1)`,
				web.NewID(), userID, "usage-"+userID, digest("usage:"+userID)); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.UnsettledUsage == 1 }},
		{"pending_question", func(userID string) {
			seedQuestion(t, service, userID, "needs_review", "reserved")
		}, func(b closureBlockers) bool { return b.QuestionUsage == 1 }},
		{"active_execution", func(userID string) {
			seedLearning(t, service, userID, "running", "disabled", true)
		}, func(b closureBlockers) bool { return b.ActiveAttempts == 1 && b.ActiveTasks == 1 }},
		{"billing_blocked_task", func(userID string) {
			seedLearning(t, service, userID, "paused", "reserved", false)
		}, func(b closureBlockers) bool { return b.BillingBlockedTasks == 1 }},
		{"active_sync", func(userID string) {
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_course_syncs
				(id,user_id,dedupe_key,status,requested_by_kind) VALUES($1,$2,$3,'queued','system')`,
				web.NewID(), userID, "sync-active-"+userID); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.ActiveCourseSyncs == 1 }},
		{"pending_reconciliation", func(userID string) {
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.reconciliations
				(id,kind,status,target_user_id,requested_by,request_id,idempotency_key,request_digest)
				VALUES($1,'wallet_rebuild','pending',$2,$3,$4,$5,$6)`,
				web.NewID(), userID, actor.ID, "req_"+digest("reconcile:" + userID)[:32],
				"reconcile-"+userID, digest("reconcile-digest:"+userID)); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.OpenReconciliations == 1 }},
		{"pending_payment_inbox", func(userID string) {
			walletID := seedWallet(t, service, userID, 0, 0, 0, false)
			orderID := seedSettledOrder(t, service, userID, walletID)
			var merchant string
			if err := service.pool.QueryRow(ctx, `SELECT merchant_order_no FROM public.recharge_orders WHERE id=$1`, orderID).Scan(&merchant); err != nil {
				t.Fatal(err)
			}
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.payment_inbox
				(id,provider,source,dedupe_key,payload_digest,order_id,merchant_order_no,
				 provider_trade_no,amount_minor,channel,provider_status,status)
				VALUES($1,'zpay','callback',$2,$3,$4,$5,$6,1000,'alipay','TRADE_SUCCESS','received')`,
				web.NewID(), digest("inbox:"+userID), digest("inbox-payload:"+userID), orderID,
				merchant, "trade-"+userID); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.PendingPaymentInbox == 1 }},
		{"pending_cache_report", func(userID string) {
			queryID := seedQuestion(t, service, userID, "answered", "settled")
			if _, err := service.pool.Exec(ctx, `INSERT INTO public.question_cache_reports
				(id,query_id,user_id,reason,status) VALUES($1,$2,$3,'缓存答案有误','pending')`,
				web.NewID(), queryID, userID); err != nil {
				t.Fatal(err)
			}
		}, func(b closureBlockers) bool { return b.PendingCacheReports == 1 }},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			userID := seedUser(t, service, "deletion_requested")
			requestID := seedDeletionRequest(t, service, userID, "needs_review")
			seedIdentity(t, service, userID, "active")
			item.seed(userID)
			expectBlocked(t, service, actor, userID, requestID, item.check)
		})
	}
}

func TestPrivacyRejectPreservesOtherRiskControls(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()
	service.SetRevokeUser(func(context.Context, pgx.Tx, string) error { return nil })
	userID := seedUser(t, service, "restricted")
	fingerprint := digest("learning:" + userID)
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.learning_accounts
		(user_id,provider,identity_namespace,identity_fingerprint,school,display_name,
		 blocked_code,blocked_reason,blocked_at,verified_at)
		VALUES($1,'chaoxing','chaoxing-session-v1',$2,'示例学校','合成学生',
		 'admin_blocked','管理员风控',now(),now())`, userID, fingerprint); err != nil {
		t.Fatal(err)
	}
	userActor := web.Actor{ID: userID, Kind: "user", SessionID: web.NewID(), AuthenticatedAt: time.Now()}
	recorder := operationRequest(service, userActor, "POST", "/api/v1/me/deletion-requests", `{"confirm":true}`)
	if recorder.Code != 202 {
		t.Fatalf("受限用户删除申请失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var userStatus, blocked string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "restricted" {
		t.Fatalf("删除申请覆盖了其他风控状态：%s", userStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&blocked); err != nil || blocked != "admin_blocked" {
		t.Fatalf("删除申请覆盖了学习风控：%q", blocked)
	}
	var requestID, requestStatus string
	if err := service.pool.QueryRow(ctx, `SELECT id::text,status FROM public.data_requests WHERE user_id=$1 AND kind='deletion'`, userID).
		Scan(&requestID, &requestStatus); err != nil || requestStatus != "needs_review" {
		t.Fatalf("删除申请未进入复核：%s", requestStatus)
	}
	if recorder := reviewRequest(service, actor, requestID, `{"action":"reject","reason":"保留管理员风控"}`); recorder.Code != http.StatusOK {
		t.Fatalf("驳回失败：%d %s", recorder.Code, recorder.Body.String())
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "restricted" {
		t.Fatalf("驳回解除了其他风控：%s", userStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&blocked); err != nil || blocked != "admin_blocked" {
		t.Fatalf("驳回解除了学习风控：%q", blocked)
	}
}

func TestPrivacyBlockedLongReasonDoesNotRollback(t *testing.T) {
	service, actor := closureFixture(t)
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")
	seedIdentity(t, service, userID, "active")
	seedWallet(t, service, userID, 1, 0, 0, false)
	reason := strings.Repeat("核", 600) // 1800 字节，加上阻断摘要仍可安全写入审计
	recorder := reviewRequest(service, actor, requestID, fmt.Sprintf(`{"action":"complete","reason":%q}`, reason))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("长理由阻断返回 %d，可能回滚了阻断记录：%s", recorder.Code, recorder.Body.String())
	}
	var status, errorCode string
	if err := service.pool.QueryRow(context.Background(), `SELECT status, COALESCE(error_code,'') FROM public.data_requests WHERE id=$1`, requestID).
		Scan(&status, &errorCode); err != nil || status != "needs_review" || errorCode != "closure_blocked" {
		t.Fatalf("长理由阻断未持久化：%s/%s %v", status, errorCode, err)
	}
}

func TestPrivacyClosureCompleteErasesNonEssentialData(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")
	identityID, subjectIndex := seedIdentity(t, service, userID, "active")
	seedAuthSecrets(t, service, userID, identityID)
	learning := seedLearning(t, service, userID, "paused", "disabled", false)
	queryID := seedQuestion(t, service, userID, "answered", "settled")
	seedExport(t, service, userID)
	seedOutbox(t, service, userID, learning.TaskID)
	walletID := seedWallet(t, service, userID, 0, 0, 0, false)
	orderID := seedSettledOrder(t, service, userID, walletID)
	seedJournal(t, service, userID, walletID)
	seedSettledUsage(t, service, userID)

	recorder := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"资金已结清并完成法律核对"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("完成删除失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil || payload["status"] != "completed" || payload["closed"] != true {
		t.Fatalf("完成响应不诚实：%s", recorder.Body.String())
	}

	var userStatus string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "closed" {
		t.Fatalf("用户未关闭：%s %v", userStatus, err)
	}
	var markerRequest, storedIndex string
	var storedCipher []byte
	var identityStatus string
	var display, school *string
	if err := service.pool.QueryRow(ctx, `SELECT request_id::text FROM public.privacy_closures WHERE user_id=$1`, userID).Scan(&markerRequest); err != nil || markerRequest != requestID {
		t.Fatalf("关闭标记缺失：%v", err)
	}
	if err := service.pool.QueryRow(ctx, `SELECT subject_index, subject_ciphertext, status, display_name, school
		FROM public.external_identities WHERE user_id=$1`, userID).
		Scan(&storedIndex, &storedCipher, &identityStatus, &display, &school); err != nil {
		t.Fatal(err)
	}
	if storedIndex != subjectIndex || storedCipher != nil || identityStatus != "revoked" || display != nil || school != nil {
		t.Fatal("外部身份未按预期清理或索引被改动")
	}
	for _, table := range []string{"client_sessions", "platform_sessions", "login_challenges", "user_preferences"} {
		var count int
		if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.`+table+` WHERE user_id=$1`, userID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s 未清除：%d %v", table, count, err)
		}
	}

	var accountSchool, accountDisplay, blockedCode string
	if err := service.pool.QueryRow(ctx, `SELECT school, display_name, blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).
		Scan(&accountSchool, &accountDisplay, &blockedCode); err != nil || accountSchool != "" || accountDisplay != "" || blockedCode != "account_closed" {
		t.Fatal("学习账号展示信息未清理或未阻断")
	}
	var planName, planStatus string
	if err := service.pool.QueryRow(ctx, `SELECT name, status FROM public.learning_plans WHERE id=$1`, learning.PlanID).
		Scan(&planName, &planStatus); err != nil || planName != "已关闭安排" || planStatus != "archived" {
		t.Fatal("安排自定义文本未清理")
	}
	var courseTitle, teacher string
	if err := service.pool.QueryRow(ctx, `SELECT title, teacher FROM public.learning_courses WHERE user_id=$1`, userID).
		Scan(&courseTitle, &teacher); err != nil || courseTitle != "" || teacher != "" {
		t.Fatal("课程展示文本未清理")
	}
	var chapterTitle, attachmentTitle string
	if err := service.pool.QueryRow(ctx, `SELECT title FROM public.learning_chapters WHERE user_id=$1`, userID).Scan(&chapterTitle); err != nil || chapterTitle != "" {
		t.Fatal("章节标题未清理")
	}
	if err := service.pool.QueryRow(ctx, `SELECT title FROM public.learning_attachments WHERE user_id=$1`, userID).Scan(&attachmentTitle); err != nil || attachmentTitle != "" {
		t.Fatal("附件标题未清理")
	}
	var taskTitle, desired, observation, confirmation string
	if err := service.pool.QueryRow(ctx, `SELECT title, desired_state, observation::text, confirmation_reason
		FROM public.learning_tasks WHERE id=$1`, learning.TaskID).
		Scan(&taskTitle, &desired, &observation, &confirmation); err != nil || taskTitle != "" || desired != "cancel" || observation != "{}" || confirmation != "" {
		t.Fatal("任务自定义文本/证据未清理")
	}
	var attemptObservation, attemptError string
	if err := service.pool.QueryRow(ctx, `SELECT observation::text, error_code FROM public.learning_task_attempts WHERE user_id=$1`, userID).
		Scan(&attemptObservation, &attemptError); err != nil || attemptObservation != "{}" || attemptError != "" {
		t.Fatal("运行尝试证据未清理")
	}
	var commandReason string
	if err := service.pool.QueryRow(ctx, `SELECT reason FROM public.learning_task_commands WHERE user_id=$1`, userID).Scan(&commandReason); err != nil || commandReason != "" {
		t.Fatal("任务命令理由未清理")
	}
	var outboxCount int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.learning_outbox WHERE user_id=$1`, userID).Scan(&outboxCount); err != nil || outboxCount != 0 {
		t.Fatal("学习通知载荷未删除")
	}
	var questionTitle, questionAnswer string
	var contentExpired bool
	if err := service.pool.QueryRow(ctx, `SELECT question_title, COALESCE(answer,''), content_expired FROM public.question_queries WHERE id=$1`, queryID).
		Scan(&questionTitle, &questionAnswer, &contentExpired); err != nil || questionTitle != "" || questionAnswer != "" || !contentExpired {
		t.Fatal("题目内容未清理")
	}
	var exportStatus string
	var exportCipher []byte
	if err := service.pool.QueryRow(ctx, `SELECT status, result_ciphertext FROM public.data_requests
		WHERE user_id=$1 AND kind='export'`, userID).Scan(&exportStatus, &exportCipher); err != nil || exportStatus != "expired" || exportCipher != nil {
		t.Fatal("导出结果未清理")
	}
	var outboxPayload, streamPayload string
	if err := service.pool.QueryRow(ctx, `SELECT payload::text FROM public.outbox_events WHERE user_id=$1`, userID).Scan(&outboxPayload); err != nil || outboxPayload != "{}" {
		t.Fatal("Outbox 载荷未清理")
	}
	if err := service.pool.QueryRow(ctx, `SELECT payload::text FROM public.stream_entries WHERE stream_key=$1`, "user/"+userID).Scan(&streamPayload); err != nil || streamPayload != "{}" {
		t.Fatal("投递流载荷未清理")
	}

	// 不可变财务与审计记录必须保留。
	var orders, journals, postings, usages int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.recharge_orders WHERE user_id=$1 AND id=$2`, userID, orderID).Scan(&orders); err != nil || orders != 1 {
		t.Fatal("财务订单被删除")
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.journals WHERE user_id=$1`, userID).Scan(&journals); err != nil || journals != 1 {
		t.Fatal("账本分录被删除")
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.postings p JOIN public.journals j ON j.id=p.journal_id WHERE j.user_id=$1`, userID).Scan(&postings); err != nil || postings != 2 {
		t.Fatal("账本借贷项被删除")
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.usage_records WHERE user_id=$1 AND status='settled'`, userID).Scan(&usages); err != nil || usages != 1 {
		t.Fatal("已结算用量证据被删除")
	}
	var auditCount int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE action='privacy.deletion_completed' AND resource_id=$1`, requestID).Scan(&auditCount); err != nil || auditCount != 1 {
		t.Fatal("完成审计数量异常")
	}

	// 再次复核应因状态不允许而失败，不重复擦除。
	again := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"重复完成"}`)
	if again.Code != http.StatusConflict {
		t.Fatalf("重复完成未拒绝：%d %s", again.Code, again.Body.String())
	}
}

func TestPrivacyDeletionRequestRevokesAndStopsExecution(t *testing.T) {
	service, _ := closureFixture(t)
	ctx := context.Background()
	service.SetRevokeUser(func(context.Context, pgx.Tx, string) error { return nil })
	userID := seedUser(t, service, "active")
	identityID, _ := seedIdentity(t, service, userID, "active")
	seedAuthSecrets(t, service, userID, identityID)
	learning := seedLearning(t, service, userID, "running", "disabled", true)
	// 模拟执行前的正常状态：安排启用、任务允许运行、账号未被删除阻断。
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_plans SET status='active' WHERE id=$1`, learning.PlanID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_tasks SET state='running', desired_state='run' WHERE id=$1`, learning.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_accounts
		SET blocked_code='', blocked_reason='', blocked_at=NULL WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}

	userActor := web.Actor{ID: userID, Kind: "user", SessionID: web.NewID(), AuthenticatedAt: time.Now()}
	recorder := operationRequest(service, userActor, "POST", "/api/v1/me/deletion-requests", `{"confirm":true}`)
	if recorder.Code != 202 {
		t.Fatalf("删除申请失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var userStatus, planStatus, taskDesired, blocked string
	var stopRequested *time.Time
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "deletion_requested" {
		t.Fatalf("删除申请未冻结账号：%s", userStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.learning_plans WHERE id=$1`, learning.PlanID).Scan(&planStatus); err != nil || planStatus != "paused" {
		t.Fatalf("删除申请未暂停安排：%s", planStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT desired_state, stop_requested_at FROM public.learning_tasks WHERE id=$1`, learning.TaskID).
		Scan(&taskDesired, &stopRequested); err != nil || taskDesired != "pause" || stopRequested == nil {
		t.Fatal("删除申请未请求停止任务")
	}
	if err := service.pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&blocked); err != nil || blocked != "deletion_requested" {
		t.Fatalf("删除申请未阻断学习账号：%q", blocked)
	}
	// 申请阶段不清除原始主体密文（由完成阶段清除）。
	var ciphertext []byte
	if err := service.pool.QueryRow(ctx, `SELECT subject_ciphertext FROM public.external_identities WHERE user_id=$1`, userID).Scan(&ciphertext); err != nil || len(ciphertext) == 0 {
		t.Fatal("申请阶段过早清除了主体密文")
	}
}

func TestPrivacyDeletionRejectAllowsReentryWithoutResuming(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")
	identityID, _ := seedIdentity(t, service, userID, "active")
	learning := seedLearning(t, service, userID, "paused", "disabled", false)
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='deletion_requested', blocked_reason='用户申请删除' WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}

	recorder := reviewRequest(service, actor, requestID, `{"action":"reject","reason":"材料不足，驳回删除申请"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("驳回失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil || payload["status"] != "rejected" {
		t.Fatalf("驳回响应错误：%s", recorder.Body.String())
	}
	var userStatus, requestStatus, blocked string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "active" {
		t.Fatalf("驳回后用户未能重新验证进入：%s %v", userStatus, err)
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.data_requests WHERE id=$1`, requestID).Scan(&requestStatus); err != nil || requestStatus != "rejected" {
		t.Fatalf("申请状态非 rejected：%s", requestStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&blocked); err != nil || blocked != "" {
		t.Fatalf("删除专用阻断未解除：%q", blocked)
	}
	// 身份与会话：身份仍有效可重新验证，但没有自动恢复任何会话。
	var identityStatus string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.external_identities WHERE id=$1`, identityID).Scan(&identityStatus); err != nil || identityStatus != "active" {
		t.Fatalf("驳回后身份不可重新验证：%s", identityStatus)
	}
	var sessions int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.client_sessions WHERE user_id=$1`, userID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("驳回自动恢复了旧会话")
	}
	// 任务保持暂停，不自动恢复执行。
	var planStatus, taskDesired string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.learning_plans WHERE id=$1`, learning.PlanID).Scan(&planStatus); err != nil || planStatus != "paused" {
		t.Fatalf("驳回后安排被自动恢复：%s", planStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT desired_state FROM public.learning_tasks WHERE id=$1`, learning.TaskID).Scan(&taskDesired); err != nil || taskDesired != "pause" {
		t.Fatalf("驳回后任务被自动恢复：%s", taskDesired)
	}
	// 驳回后不能直接完成同一申请。
	complete := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"重复处理"}`)
	if complete.Code != http.StatusConflict {
		t.Fatalf("驳回后仍可完成：%d", complete.Code)
	}
}

func TestPrivacyDeletionLegacyReasonDefersAndDoesNotClaimCompletion(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")

	recorder := reviewRequest(service, actor, requestID, `{"reason":"等待财务与争议核对"}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("旧请求格式失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var payload map[string]any
	if json.Unmarshal(recorder.Body.Bytes(), &payload) != nil || payload["status"] != "deferred" || payload["closed"] != false {
		t.Fatalf("旧请求被误报为完成：%s", recorder.Body.String())
	}
	var status string
	var userStatus string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.data_requests WHERE id=$1`, requestID).Scan(&status); err != nil || status != "deferred" {
		t.Fatalf("旧请求未按 defer 处理：%s", status)
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "deletion_requested" {
		t.Fatalf("延后时错误恢复账号：%s", userStatus)
	}
	var closed int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.privacy_closures`).Scan(&closed); err != nil || closed != 0 {
		t.Fatal("延后时写入了关闭标记")
	}

	// 延后后仍可在条件满足时完成。
	done := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"资金已结清，完成关闭"}`)
	if done.Code != http.StatusOK {
		t.Fatalf("延后后完成失败：%d %s", done.Code, done.Body.String())
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "closed" {
		t.Fatalf("延后后完成未关闭账号：%s", userStatus)
	}
}

func TestPrivacyReviewRequiresRoleAndFreshAuthentication(t *testing.T) {
	service, actor := closureFixture(t)
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")

	noPermission := actor
	noPermission.Permissions = []string{}
	if recorder := reviewRequest(service, noPermission, requestID, `{"action":"defer","reason":"无权限测试"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("无 privacy.review 仍可复核：%d", recorder.Code)
	}
	stale := actor
	stale.AuthenticatedAt = time.Now().Add(-10 * time.Minute)
	if recorder := reviewRequest(service, stale, requestID, `{"action":"defer","reason":"过期验证"}`); recorder.Code != http.StatusForbidden {
		t.Fatalf("过期验证仍可复核：%d", recorder.Code)
	}
	if recorder := reviewRequest(service, actor, requestID, `{"action":"delete_now","reason":"非法动作"}`); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法动作未被拒绝：%d", recorder.Code)
	}
	if recorder := reviewRequest(service, actor, requestID, `{"action":"defer","reason":"短"}`); recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("过短理由未被拒绝：%d", recorder.Code)
	}
}

func TestPrivacyClosureReapplyAfterRestore(t *testing.T) {
	service, actor := closureFixture(t)
	ctx := context.Background()
	userID := seedUser(t, service, "deletion_requested")
	requestID := seedDeletionRequest(t, service, userID, "needs_review")
	identityID, subjectIndex := seedIdentity(t, service, userID, "active")
	learning := seedLearning(t, service, userID, "paused", "disabled", false)
	queryID := seedQuestion(t, service, userID, "answered", "settled")
	walletID := seedWallet(t, service, userID, 0, 0, 0, false)
	orderID := seedSettledOrder(t, service, userID, walletID)
	seedJournal(t, service, userID, walletID)

	if recorder := reviewRequest(service, actor, requestID, `{"action":"complete","reason":"完成关闭并记录恢复标记"}`); recorder.Code != http.StatusOK {
		t.Fatalf("完成失败：%d %s", recorder.Code, recorder.Body.String())
	}

	// 模拟从备份恢复到授权仍有效的旧状态。
	if _, err := service.pool.Exec(ctx, `UPDATE public.users SET status='active' WHERE id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.external_identities
		SET status='active', subject_ciphertext=$2, display_name='恢复学生', school='恢复学校', revoked_at=NULL
		WHERE id=$1`, identityID, bytes.Repeat([]byte{0x43}, 48)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.platform_sessions
		(id,user_id,external_identity_id,key_version,ciphertext,expires_at)
		VALUES($1,$2,$3,1,$4,now()+interval '2 hours')`,
		web.NewID(), userID, identityID, bytes.Repeat([]byte{0x45}, 48)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.client_sessions
		(id,user_id,channel,token_hash,csrf_hash,status,authenticated_at,created_at,updated_at,
		 last_seen_at,expires_at,absolute_expires_at)
		VALUES($1,$2,'web',$3,$4,'active',now(),now(),now(),now(),now()+interval '1 hour',now()+interval '1 hour')`,
		web.NewID(), userID, digest("restored-token:"+userID), digest("restored-csrf:"+userID)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_accounts
		SET blocked_code='', blocked_reason='', blocked_at=NULL, display_name='恢复学生', school='恢复学校'
		WHERE user_id=$1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_plans SET status='active', name='恢复安排' WHERE id=$1`, learning.PlanID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.learning_tasks SET title='恢复任务', desired_state='run' WHERE id=$1`, learning.TaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.question_queries SET question_title='恢复题目', answer='A', content_expired=false WHERE id=$1`, queryID); err != nil {
		t.Fatal(err)
	}

	var closed int
	if err := service.pool.QueryRow(ctx, `SELECT public.reapply_privacy_closures()`).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("恢复重放关闭用户数为 %d", closed)
	}
	var userStatus, identityStatus, accountBlocked, planStatus, taskDesired, questionTitle string
	var ciphertext []byte
	var sessions, platforms int
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, userID).Scan(&userStatus); err != nil || userStatus != "closed" {
		t.Fatalf("恢复后用户未重新关闭：%s", userStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT status, subject_ciphertext FROM public.external_identities WHERE id=$1`, identityID).
		Scan(&identityStatus, &ciphertext); err != nil || identityStatus != "revoked" || ciphertext != nil {
		t.Fatal("恢复后主体密文/状态未重新清理")
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.client_sessions WHERE user_id=$1`, userID).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("恢复后会话未重新撤销")
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.platform_sessions WHERE user_id=$1`, userID).Scan(&platforms); err != nil || platforms != 0 {
		t.Fatal("恢复后平台会话未重新清理")
	}
	if err := service.pool.QueryRow(ctx, `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&accountBlocked); err != nil || accountBlocked != "account_closed" {
		t.Fatal("恢复后学习账号未重新阻断")
	}
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.learning_plans WHERE id=$1`, learning.PlanID).Scan(&planStatus); err != nil || planStatus != "archived" {
		t.Fatal("恢复后安排未重新归档")
	}
	if err := service.pool.QueryRow(ctx, `SELECT desired_state FROM public.learning_tasks WHERE id=$1`, learning.TaskID).Scan(&taskDesired); err != nil || taskDesired != "cancel" {
		t.Fatal("恢复后任务未重新停止")
	}
	if err := service.pool.QueryRow(ctx, `SELECT question_title FROM public.question_queries WHERE id=$1`, queryID).Scan(&questionTitle); err != nil || questionTitle != "" {
		t.Fatal("恢复后题目内容未重新清理")
	}
	var orders int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.recharge_orders WHERE id=$1`, orderID).Scan(&orders); err != nil || orders != 1 {
		t.Fatal("恢复重放删除了财务订单")
	}
	var storedIndex string
	if err := service.pool.QueryRow(ctx, `SELECT subject_index FROM public.external_identities WHERE id=$1`, identityID).Scan(&storedIndex); err != nil || storedIndex != subjectIndex {
		t.Fatal("恢复重放修改了主体索引")
	}

	// 恢复点早于关闭标记时，可按外部清单补录用户编号。
	extraUser := seedUser(t, service, "active")
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.client_sessions
		(id,user_id,channel,token_hash,csrf_hash,status,authenticated_at,created_at,updated_at,
		 last_seen_at,expires_at,absolute_expires_at)
		VALUES($1,$2,'web',$3,$4,'active',now(),now(),now(),now(),now()+interval '1 hour',now()+interval '1 hour')`,
		web.NewID(), extraUser, digest("extra-token:"+extraUser), digest("extra-csrf:"+extraUser)); err != nil {
		t.Fatal(err)
	}
	if err := service.pool.QueryRow(ctx,
		fmt.Sprintf(`SELECT public.reapply_privacy_closures(ARRAY['%s']::uuid[])`, extraUser)).Scan(&closed); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("外部清单关闭用户数为 %d", closed)
	}
	var extraStatus string
	if err := service.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id=$1`, extraUser).Scan(&extraStatus); err != nil || extraStatus != "closed" {
		t.Fatalf("外部清单用户未关闭：%s", extraStatus)
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.client_sessions WHERE user_id=$1`, extraUser).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("外部清单恢复后会话未撤销")
	}
}
