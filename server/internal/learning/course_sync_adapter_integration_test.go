//go:build integration

package learning

import (
	"context"
	"testing"
)

func TestCourseSyncResumeRefreshesSnapshotsFromOlderAdapter(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, err := New(pool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	actor := Actor{ID: user, Kind: "user"}
	if _, _, err := service.RequestCourseSync(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal("无法领取同步", err)
	}
	account, err := service.accountForUser(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Adapter = "older-catalog-adapter"
	if _, err := service.applyCourseSnapshot(context.Background(), job, account, snapshot); err != nil {
		t.Fatal(err)
	}
	completed, err := service.completedSyncCourses(context.Background(), *job)
	if err != nil || len(completed) != 0 {
		t.Fatalf("旧解析器保存的目录被恢复逻辑跳过：%v / %v", completed, err)
	}
	snapshot.Adapter = platform.Capabilities().Adapter
	if _, err := service.applyCourseSnapshot(context.Background(), job, account, snapshot); err != nil {
		t.Fatal(err)
	}
	completed, err = service.completedSyncCourses(context.Background(), *job)
	if err != nil || completed["101/202"] != 2 {
		t.Fatalf("当前解析器的完整快照未被复用：%v / %v", completed, err)
	}
}
