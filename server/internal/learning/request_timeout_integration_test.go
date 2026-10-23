//go:build integration

package learning

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLearningRequestBoundsLockWaitWithoutPartialControl(t *testing.T) {
	pool := testPool(t)
	service, user, task := prepareTask(t, pool, defaultFakePlatform())
	service.opts.RequestTimeout = 100 * time.Millisecond
	server := newTestMux(t, service, map[string]Actor{"user": {ID: user, Kind: "user"}})
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rollbackLearningTx(tx)
	if _, err := tx.Exec(context.Background(), `SELECT id FROM public.learning_tasks WHERE id=$1 FOR UPDATE`, task); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/api/v1/tasks/"+task+"/controls", strings.NewReader(`{"action":"pause","reason":"锁等待测试"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Test-Actor", "user")
	request.Header.Set("Idempotency-Key", "lock-wait-control-0001")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal("学习接口未在内部时限返回", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("锁等待错误状态: %d", response.StatusCode)
	}
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, desired, _, _, _, _ := taskRow(t, pool, task)
	if state != StatePending || desired != DesiredRun {
		t.Fatalf("超时留下半完成控制: %s/%s", state, desired)
	}
}
