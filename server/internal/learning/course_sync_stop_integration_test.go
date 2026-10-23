//go:build integration

package learning

import (
	"context"
	"testing"
	"time"
)

type blockedCourseDiscovery struct {
	*fakePlatform
	entered chan struct{}
}

func (p blockedCourseDiscovery) DiscoverCourse(ctx context.Context, _ []byte, _ PlatformCourseRef) (PlatformCourseSnapshot, error) {
	close(p.entered)
	<-ctx.Done()
	return PlatformCourseSnapshot{}, ctx.Err()
}

func TestRunningCourseSyncStopsWhenAccountIsBlocked(t *testing.T) {
	pool := testPool(t)
	platform := blockedCourseDiscovery{fakePlatform: defaultFakePlatform(), entered: make(chan struct{})}
	opts := fastOptions(platform, nil)
	opts.LeaseTTL, opts.HeartbeatInterval = time.Second, 50*time.Millisecond
	service, err := New(pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, _, err := service.RequestCourseSync(ctx, Actor{Kind: "user", ID: user}); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); service.runCourseSync(ctx, *job) }()
	defer func() { cancel(); <-done }()
	select {
	case <-platform.entered:
	case <-time.After(time.Second):
		t.Fatal("目录读取未开始")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.learning_accounts SET blocked_code='admin_blocked' WHERE user_id=$1`, user); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("账号限制后仍持续读取外部课程")
	}
}
