//go:build integration

package learning

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type listQueryCounter struct{ count atomic.Int64 }

func (c *listQueryCounter) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	c.count.Add(1)
	return ctx
}
func (*listQueryCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestTaskAndPlanPagesUseBoundedDatabaseRoundTrips(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	attachments := make([]PlatformAttachment, 100)
	for i := range attachments {
		attachments[i] = videoAttachment(fmt.Sprintf("batch-video-%d", i), fmt.Sprintf("batch-job-%d", i))
	}
	platform.snapshots["101/202"] = snapshotWithChapters(videoChapter("5001", attachments...))
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	course := courseIDForUser(t, pool, user)
	for range 20 {
		createVideoPlan(t, service, user, course)
	}
	counter := &listQueryCounter{}
	cfg := pool.Config()
	cfg.MaxConns, cfg.MinConns = 1, 0
	cfg.ConnConfig.Tracer = counter
	counted, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer counted.Close()
	service.pool = counted
	ctx := context.Background()
	tasks, err := service.ListTasks(ctx, Actor{ID: user, Kind: "user"}, TaskFilter{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	taskQueries := counter.count.Swap(0)
	if len(tasks.Items) != 100 || len(tasks.Items[0].PlanRefs) != 20 {
		t.Fatalf("批量任务关联丢失: %d", len(tasks.Items))
	}
	if taskQueries != 2 {
		t.Fatalf("100个任务仍存在N+1查询: %d", taskQueries)
	}
	plans, err := service.ListPlans(ctx, Actor{ID: user, Kind: "user"}, PlanFilter{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	planQueries := counter.count.Swap(0)
	if len(plans.Items) != 10 || plans.NextCursor == "" {
		t.Fatal("安排分页缺失")
	}
	if planQueries != 1 {
		t.Fatalf("安排页仍逐条开启事务: %d", planQueries)
	}
	next, err := service.ListPlans(ctx, Actor{ID: user, Kind: "user"}, PlanFilter{Limit: 10, Cursor: plans.NextCursor})
	if err != nil || len(next.Items) != 10 || next.Items[0].ID == plans.Items[0].ID {
		t.Fatalf("安排下一页错误: %v", err)
	}
	t.Logf("100任务/20关联安排：任务页%d次SQL，10条安排页%d次SQL", taskQueries, planQueries)
}
