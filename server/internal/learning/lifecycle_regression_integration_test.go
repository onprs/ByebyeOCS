//go:build integration

package learning

import (
	"context"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestShutdownDuringSessionReadPreservesTaskForRecovery(t *testing.T) {
	pool := testPool(t)
	service := newVideoLearningService(t, pool, defaultFakePlatform())
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	createVideoPlan(t, service, user, courseIDForUser(t, pool, user))
	job := claimOne(t, service)
	entered := make(chan struct{})
	service.opts.PollInterval = time.Hour
	service.opts.VerificationInterval = time.Hour
	service.opts.Sessions = SessionResolverFunc(func(ctx context.Context, _ string) ([]byte, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()
	waitFor(t, time.Second, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return service.started })
	service.startExecution(ctx, job)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("未读取会话")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("读取取消后未退出")
	}
	state, desired, _, _, _, _ := taskRow(t, pool, job.TaskID)
	var blocked string
	if err := pool.QueryRow(context.Background(), `SELECT blocked_code FROM public.learning_accounts WHERE user_id=$1`, user).Scan(&blocked); err != nil {
		t.Fatal(err)
	}
	if state != StatePreparing || desired != DesiredRun || blocked != "" {
		t.Fatalf("停止误写用户意图或账号阻断: %s/%s/%s", state, desired, blocked)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_task_attempts SET lease_expires_at=now()-interval '1 second' WHERE id=$1`, job.AttemptID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.recoverOneExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _, _, _, _, _ = taskRow(t, pool, job.TaskID)
	if state != StateWaitingVerification {
		t.Fatalf("退出后未先恢复核验: %s", state)
	}
}

func TestLearningRunWaitsForExecutionCleanupAndBoundsShutdown(t *testing.T) {
	for _, timeout := range []bool{false, true} {
		t.Run(map[bool]string{false: "等待清理", true: "有界退出"}[timeout], func(t *testing.T) {
			pool := testPool(t)
			platform := defaultFakePlatform()
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			executor := &executorFunc{capabilities: ExecutorCapabilities{Enabled: true, TaskKinds: []string{"video"}},
				run: func(ctx context.Context, _ ExecutionRequest) (ExecutionResult, error) {
					close(entered)
					<-ctx.Done()
					<-release
					return ExecutionResult{}, Fail(FailureStopped, "夹具已清理")
				}}
			opts := fastOptions(platform, executor)
			opts.PollInterval, opts.VerificationInterval = time.Hour, time.Hour
			opts.ShutdownTimeout = 2 * time.Second
			if timeout {
				opts.ShutdownTimeout = 80 * time.Millisecond
			}
			service, err := New(pool, opts)
			if err != nil {
				t.Fatal(err)
			}
			user := seedUser(t, pool)
			bindTestAccount(t, service, user)
			syncAll(t, service, user)
			createVideoPlan(t, service, user, courseIDForUser(t, pool, user))
			job := claimOne(t, service)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan error, 1)
			go func() { returned <- service.Run(ctx) }()
			waitFor(t, time.Second, func() bool { service.mu.Lock(); defer service.mu.Unlock(); return service.started })
			service.startExecution(ctx, job)
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("执行未开始")
			}
			cancel()
			if timeout {
				select {
				case err := <-returned:
					code, _ := fault.Describe(err)
					if code != "shutdown_timeout" {
						t.Fatalf("未报告有界退出超时: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("执行器不退出导致停止永久阻塞")
				}
			} else {
				select {
				case err := <-returned:
					t.Fatalf("执行清理结束前Run已经返回: %v", err)
				case <-time.After(100 * time.Millisecond):
				}
			}
			close(release)
			if !timeout {
				select {
				case err := <-returned:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("清理结束后未退出")
				}
			}
			waitFor(t, time.Second, func() bool { return service.localRunning() == 0 })
		})
	}
}
