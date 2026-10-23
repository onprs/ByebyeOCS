//go:build integration

package operations

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func publishUserEvents(t *testing.T, service *Service, userID string, count int) []string {
	t.Helper()
	ctx := context.Background()
	tx, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range count {
		if err := events.Record(ctx, tx, events.Event{Audience: "user", UserID: userID, Type: "task.updated", ResourceID: web.NewID(), Version: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err := service.pool.Query(ctx, `SELECT id::text FROM public.outbox_events WHERE user_id=$1 ORDER BY occurred_at,id`, userID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, count)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := events.New(service.pool).Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestCleanupTrimsStreamRetentionWithoutLosingResumeState 校验投递事件清理与最早可续传序号同批推进。
func TestCleanupTrimsStreamRetentionWithoutLosingResumeState(t *testing.T) {
	service, user, other := operationFixture(t)
	ctx := context.Background()
	expired := publishUserEvents(t, service, user.ID, 2)
	kept := publishUserEvents(t, service, other.ID, 1)

	// 两条事件已过 3 天投递保留期且已投递 8 天；另一流的事件仍然新鲜。
	if _, err := service.pool.Exec(ctx, `UPDATE public.stream_entries SET created_at=now()-interval '4 days' WHERE stream_key=$1`, "user/"+user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.pool.Exec(ctx, `UPDATE public.outbox_events SET published_at=now()-interval '8 days'`); err != nil {
		t.Fatal(err)
	}
	if err := service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE stream_key=$1`, "user/"+user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("过期投递事件未清理：%d", count)
	}
	// 已追平该流的游标仍可续传，更早的游标才要求重取快照：最早序号必须是 sequence+1。
	var sequence, earliest int64
	if err := service.pool.QueryRow(ctx, `SELECT sequence,earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, "user/"+user.ID).Scan(&sequence, &earliest); err != nil {
		t.Fatal(err)
	}
	if sequence != 2 || earliest != 3 {
		t.Fatalf("流保留范围与删除不一致：earliest=%d sequence=%d", earliest, sequence)
	}
	// 仍被投递记录引用的事件不能按年龄删除。
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events WHERE id = ANY($1::uuid[])`, []string{kept[0]}).Scan(&count); err != nil || count != 1 {
		t.Fatalf("仍在保留期内的事件被删除：%d", count)
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events WHERE id = ANY($1::uuid[])`, expired).Scan(&count); err != nil || count != 0 {
		t.Fatalf("无引用的已投递事件未清理：%d", count)
	}
	// 重复执行保持结果不变。
	if err := service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE stream_key=$1`, "user/"+other.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("新鲜投递事件被清理：%d", count)
	}
	var earliestAfter int64
	if err := service.pool.QueryRow(ctx, `SELECT earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, "user/"+other.ID).Scan(&earliestAfter); err != nil || earliestAfter != 1 {
		t.Fatalf("未清理的流保留范围被改动：%d", earliestAfter)
	}
}

// TestCleanupKeepsTrimAndResumeStateAtomic 用流头行锁构造“删除已提交、保留范围未推进”的中间态禁止场景。
// 删除与推进分离时，清理中断会留下已删除事件却保留旧游标的状态，客户端会静默漏读。
func TestCleanupKeepsTrimAndResumeStateAtomic(t *testing.T) {
	service, user, _ := operationFixture(t)
	ctx := context.Background()
	publishUserEvents(t, service, user.ID, 2)
	if _, err := service.pool.Exec(ctx, `UPDATE public.stream_entries SET created_at=now()-interval '4 days' WHERE stream_key=$1`, "user/"+user.ID); err != nil {
		t.Fatal(err)
	}
	blocker, err := service.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `SELECT stream_key FROM public.stream_heads WHERE stream_key=$1 FOR UPDATE`, "user/"+user.ID); err != nil {
		t.Fatal(err)
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	err = service.Cleanup(cleanupCtx)
	cancel()
	if err == nil {
		t.Fatal("流头被锁时清理未报告中断")
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE stream_key=$1`, "user/"+user.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("清理中断后仍删除了事件：%d", count)
	}
	var earliest int64
	if err := service.pool.QueryRow(ctx, `SELECT earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, "user/"+user.ID).Scan(&earliest); err != nil || earliest != 1 {
		t.Fatalf("清理中断后保留范围被误推进：%d", earliest)
	}
	// 重试可在锁释放后正常完成，且两者仍一致。
	if err := service.Cleanup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE stream_key=$1`, "user/"+user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("重试后仍未清理：%d", count)
	}
	if err := service.pool.QueryRow(ctx, `SELECT earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, "user/"+user.ID).Scan(&earliest); err != nil || earliest != 3 {
		t.Fatalf("重试后保留范围错误：%d", earliest)
	}
}

// TestRunPerformsRetentionOnStartup 校验服务启动即执行一次有界保留清理，短周期重启不会无限推迟清理。
func TestRunPerformsRetentionOnStartup(t *testing.T) {
	service, user, _ := operationFixture(t)
	ctx := context.Background()
	requestID := web.NewID()
	if _, err := service.pool.Exec(ctx, `INSERT INTO public.data_requests(id,user_id,kind,status,result_ciphertext,expires_at)
		VALUES($1,$2,'export','ready',$3,now()-interval '1 hour')`, requestID, user.ID, []byte("ciphertext")); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := service.Run(runCtx); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var status string
		var ciphertext []byte
		if err := service.pool.QueryRow(ctx, `SELECT status,result_ciphertext FROM public.data_requests WHERE id=$1`, requestID).Scan(&status, &ciphertext); err != nil {
			t.Fatal(err)
		}
		if status == "expired" && ciphertext == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("启动后未执行保留清理：status=%s", status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("维护循环未随上下文结束")
	}
}

// TestMaintenanceRequestTimeoutDoesNotHoldConnection 校验行锁等待不会让维护接口无限占用连接池。
func TestMaintenanceRequestTimeoutDoesNotHoldConnection(t *testing.T) {
	pool := testsupport.NewPool(t)
	service, err := New(pool, Options{DataDir: t.TempDir(), Key: bytes.Repeat([]byte{9}, 32), RequestTimeout: 300 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Prepare(); err != nil {
		t.Fatal(err)
	}
	adminID, userID := web.NewID(), web.NewID()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO public.admin_users(id,username,password_hash)
		VALUES($1,$2,'$argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHQ$aGFzaGhhc2g')`, adminID, "admin-"+adminID[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.users(id)VALUES($1)`, userID); err != nil {
		t.Fatal(err)
	}
	requestID := web.NewID()
	if _, err := pool.Exec(ctx, `INSERT INTO public.data_requests(id,user_id,kind,status)VALUES($1,$2,'deletion','needs_review')`, requestID, userID); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: adminID, Kind: "admin", SessionID: web.NewID(), Permissions: []string{"privacy.review"}, AuthenticatedAt: time.Now()}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `SELECT id FROM public.data_requests WHERE id=$1 FOR UPDATE`, requestID); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	w := operationRequest(service, actor, "POST", "/admin/api/v1/data-requests/"+requestID+"/review", `{"action":"complete","reason":"test lock wait"}`)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("行锁等待未被时限中断：%s", elapsed)
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("锁等待超时未返回可重试错误：%d %s", w.Code, w.Body.String())
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM public.data_requests WHERE id=$1`, requestID).Scan(&status); err != nil || status != "needs_review" {
		t.Fatalf("超时请求留下半完成状态：%s", status)
	}
}
