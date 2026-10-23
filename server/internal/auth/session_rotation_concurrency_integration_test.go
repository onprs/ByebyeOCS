//go:build integration

package auth

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// Web 会话轮换必须一次性占用旧会话：同一 Cookie 的并发轮换只能成功一次，
// 否则每个并发请求都会派生出新的在用会话，绕过单主体会话上限。
func TestIntegrationWebSessionRotationCreatesSingleSession(t *testing.T) {
	h := newHarness(t, nil)
	cookie, _ := h.userSession()
	ctx := context.Background()
	reader := sqlSessionReader{service: h.service}
	state, err := reader.lookup(ctx, KindUser, tokenDigest(cookie))
	if err != nil {
		t.Fatal(err)
	}
	const parallel = 8
	results := make(chan error, parallel)
	var wait sync.WaitGroup
	now := h.clock.Now()
	for index := 0; index < parallel; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- h.service.inTx(ctx, func(tx pgx.Tx) error {
				_, err := h.service.rotateClientSession(ctx, tx, state, now)
				return err
			})
		}()
	}
	wait.Wait()
	close(results)
	success, rejected := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		if code, _ := fault.Describe(err); code != "unauthenticated" {
			t.Fatalf("并发轮换返回意外错误: %v", err)
		}
		rejected++
	}
	if success != 1 || rejected != parallel-1 {
		t.Fatalf("并发轮换成功 %d 次、拒绝 %d 次，期望 1/%d", success, rejected, parallel-1)
	}
	if active := h.count(`SELECT count(*) FROM public.client_sessions WHERE status = 'active'`); active != 1 {
		t.Fatalf("并发轮换后在用会话为 %d，期望 1", active)
	}
	if rotated := h.count(`SELECT count(*) FROM public.client_sessions WHERE revoke_reason = 'rotated'`); rotated != 1 {
		t.Fatalf("并发轮换标记数量为 %d，期望 1", rotated)
	}
}
