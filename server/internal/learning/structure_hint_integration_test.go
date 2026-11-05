//go:build integration

package learning

import (
	"context"
	"testing"
)

// 第一次同步写入结构指纹；第二次同步把它作为 hint 传给适配器，便于跳过页码枚举。
func TestCourseSyncPassesStructureHintAfterFirstSnapshot(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)

	var modified int64
	if err := pool.QueryRow(context.Background(), `SELECT snapshot.platform_modified_at
		FROM public.learning_course_snapshots snapshot
		JOIN public.learning_courses c ON c.id = snapshot.course_id AND c.snapshot_version = snapshot.version
		WHERE c.user_id = $1`, user).Scan(&modified); err != nil {
		t.Fatal(err)
	}
	if modified != 1234 {
		t.Fatalf("结构指纹没有写入快照：%d", modified)
	}
	platform.mu.Lock()
	firstHint := platform.lastHint
	platform.mu.Unlock()
	if firstHint.ModifiedAt != 0 {
		t.Fatalf("首次同步不应带结构指纹：%+v", firstHint)
	}

	syncAll(t, service, user)
	platform.mu.Lock()
	second := platform.lastHint
	platform.mu.Unlock()
	if second.ModifiedAt != 1234 {
		t.Fatalf("第二次同步没有传入结构指纹：%+v", second)
	}
	indexes := second.Indexes["5001"]
	if len(indexes) != 1 || indexes[0] != 0 {
		t.Fatalf("结构指纹缺少章节页码：%+v", second.Indexes)
	}
}

// 结构指纹超过复用期限后不再传给适配器，按完整发现执行。
func TestCourseSyncIgnoresExpiredStructureHint(t *testing.T) {
	pool := testPool(t)
	platform := defaultFakePlatform()
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	// 把快照时间推回超过复用期限。
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_course_snapshots
		SET captured_at = now() - interval '30 days'`); err != nil {
		t.Fatal(err)
	}
	platform.mu.Lock()
	platform.lastHint = CourseStructureHint{}
	platform.mu.Unlock()
	syncAll(t, service, user)
	platform.mu.Lock()
	got := platform.lastHint
	platform.mu.Unlock()
	if got.ModifiedAt != 0 {
		t.Fatalf("过期结构指纹仍被复用：%+v", got)
	}
}
