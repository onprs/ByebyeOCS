//go:build integration

package learning

import (
	"context"
	"testing"
	"time"
)

func TestCourseSyncPublishesTitlesBeforeDetailsAndKeepsCompleteSnapshot(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	platform.snapshots["101/202"] = func() PlatformCourseSnapshot {
		snapshot := platform.snapshots["101/202"]
		snapshot.Title = ""
		return snapshot
	}()
	service, err := New(pool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	page, err := service.ListCourses(context.Background(), actor, CourseFilter{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Title != "测试课程" || page.Items[0].SnapshotVersion != 1 || page.Items[0].ResourceCount != 1 {
		t.Fatalf("列表标题或快照统计丢失：%+v %v", page, err)
	}
	courseID := page.Items[0].ID
	platform.mu.Lock()
	delete(platform.snapshots, "101/202")
	platform.courses[0].Title = "更新后的课程名"
	platform.courses = append(platform.courses, PlatformCourse{ExternalCourseID: "104", ExternalClassID: "205", CPI: "306", Title: "待加载课程"})
	platform.mu.Unlock()
	syncAll(t, service, userID)
	page, err = service.ListCourses(context.Background(), actor, CourseFilter{})
	if err != nil || len(page.Items) != 2 {
		t.Fatalf("概要未先发布：%+v %v", page, err)
	}
	for _, course := range page.Items {
		if course.ID == courseID && (course.SnapshotVersion != 1 || course.Title != "更新后的课程名" || course.TaskPointCount != 1) {
			t.Fatalf("结构失败覆盖了完整快照：%+v", course)
		}
		if course.ID != courseID && (course.SnapshotVersion != 0 || !course.Stale || course.SyncedAt != nil) {
			t.Fatalf("课程概要被冒充为完整快照：%+v", course)
		}
	}
	latest, err := service.LatestCourseSync(context.Background(), actor)
	if err != nil || latest.Status != "needs_attention" || latest.ErrorCode != "course_structure_partial" || latest.CourseCount != 2 {
		t.Fatalf("部分完成状态错误：%+v %v", latest, err)
	}
	otherActor := Actor{ID: seedUser(t, pool), Kind: "user"}
	if _, err := service.GetCourseSync(context.Background(), otherActor, latest.ID); err == nil {
		t.Fatal("其他用户读取了同步状态")
	}
}

func TestCourseSyncResumeSkipsCommittedCoursesAndRetainsFailureProgress(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, err := New(pool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	_, _, err = service.RequestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal(err)
	}
	account, _ := service.accountForUser(context.Background(), userID)
	if _, err := service.applyCourseSnapshot(context.Background(), job, account, platform.snapshots["101/202"]); err != nil {
		t.Fatal(err)
	}
	platform.mu.Lock()
	delete(platform.snapshots, "101/202")
	platform.mu.Unlock()
	service.runCourseSync(context.Background(), *job)
	latest, err := service.LatestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if err != nil || latest.Status != "succeeded" || latest.CourseCount != 1 {
		t.Fatalf("恢复同步重新读取了已提交的课程：%+v %v", latest, err)
	}
	var version int64
	if err := pool.QueryRow(context.Background(), `SELECT snapshot_version FROM public.learning_courses WHERE user_id=$1`, userID).Scan(&version); err != nil || version != 1 {
		t.Fatal("恢复同步重复写快照", err)
	}
	_, _, _ = service.RequestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	job, _ = service.claimCourseSync(context.Background())
	if err := service.updateCourseSyncProgress(context.Background(), job, 5); err != nil {
		t.Fatal(err)
	}
	service.finishSyncFailure(context.Background(), *job, syncResult{status: "needs_attention", errorCode: "transient"})
	latest, _ = service.LatestCourseSync(context.Background(), Actor{ID: userID, Kind: "user"})
	if latest.CourseCount != 5 {
		t.Fatal("失败状态清空了已读取数量")
	}
}

func TestPostgresResourceCachePersistenceAndExpiry(t *testing.T) {
	pool := testPool(t)
	cache := NewCourseResourceCache(pool)
	key := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	cache.StoreCardIndexes(context.Background(), key, []int{0, 2}, time.Now().Add(time.Minute))
	indexes, _, found := NewCourseResourceCache(pool).LoadCardIndexes(context.Background(), key)
	if !found || len(indexes) != 2 || indexes[1] != 2 {
		t.Fatalf("跨实例缓存未命中：%v %v", indexes, found)
	}
	cache.StoreCardIndexes(context.Background(), key, []int{0}, time.Now().Add(-time.Second))
	if _, _, found := cache.LoadCardIndexes(context.Background(), key); found {
		t.Fatal("已过期缓存仍被返回")
	}
}
