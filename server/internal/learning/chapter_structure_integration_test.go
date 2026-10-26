//go:build integration

package learning

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func hierarchyTestPlatform() *fakePlatform {
	platform := defaultFakePlatform()
	snapshot := platform.snapshots["101/202"]
	chapter := snapshot.Chapters[0]
	chapter.ParentExternalID, chapter.Number, chapter.Title = "group-intro", "1.1", "导学说明"
	chapter.Attachments[0].CardIndex = 1
	chapter.Attachments = append(chapter.Attachments, PlatformAttachment{ObjectID: "video-1", TaskPointID: "9002", Kind: "video", CardIndex: 2, PassedKnown: true, PlayedSeconds: 3})
	snapshot.Chapters = []PlatformChapter{
		{ExternalID: "group-intro", Number: "1", Title: "课程导学", IsGroup: true},
		chapter,
		{ExternalID: "5002", ParentExternalID: "5001", Number: "1.1.1", Title: "学习目标"},
		{ExternalID: "5003", ParentExternalID: "5002", Number: "1.1.1.1", Title: "学习资料", Attachments: []PlatformAttachment{{ObjectID: "document", Kind: "document"}}},
		{ExternalID: "group-topic", Number: "2", Title: "专题学习", IsGroup: true},
		{ExternalID: "6001", ParentExternalID: "group-topic", Number: "2.1", Title: "专题概述"},
	}
	platform.snapshots["101/202"] = snapshot
	return platform
}

func TestCourseHierarchySurvivesPersistenceAndResync(t *testing.T) {
	pool := testPool(t)
	platform := hierarchyTestPlatform()
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	actor, courseID := Actor{ID: user, Kind: "user"}, courseIDForUser(t, pool, user)
	detail, err := service.GetCourse(context.Background(), actor, courseID)
	if err != nil || detail.ChapterCount != 6 || len(detail.Chapters) != 6 || detail.TaskPointCount != 2 || detail.ResourceCount != 2 {
		t.Fatalf("目录或统计没有完整保存：%+v / %v", detail, err)
	}
	if !detail.Chapters[0].IsGroup || detail.Chapters[0].Number != "1" || detail.Chapters[3].ParentExternalID != "5002" || detail.Chapters[3].Number != "1.1.1.1" {
		t.Fatalf("目录层级或编号丢失：%+v", detail.Chapters)
	}
	attachments := detail.Chapters[1].Attachments
	if len(attachments) != 3 || attachments[0].TaskPointID != "9001" || attachments[2].TaskPointID != "9002" || attachments[2].PlayedSeconds != 3 {
		t.Fatalf("同一媒体的不同任务点被去重或进度串位：%+v", attachments)
	}
	chapterID := detail.Chapters[1].ID
	plan, err := service.CreatePlan(context.Background(), actor, PlanInput{Name: "导学安排", CourseIDs: []string{courseID},
		Scope: PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: chapterID}}}, TaskTypes: []string{"video"}})
	if err != nil {
		t.Fatal(err)
	}
	syncAll(t, service, user)
	updated, err := service.GetCourse(context.Background(), actor, courseID)
	if err != nil || updated.SnapshotVersion != 2 || updated.Chapters[1].ID != chapterID || currentRevisionBindings(t, pool, plan.CurrentRevisionID) != 2 {
		t.Fatalf("重新同步后稳定章节或安排关联失效：%+v / %v", updated, err)
	}
	_, err = service.CreatePlan(context.Background(), actor, PlanInput{Name: "无效分组安排", CourseIDs: []string{courseID},
		Scope: PlanScopeInput{Kind: "selected", Chapters: []PlanChapter{{CourseID: courseID, ChapterID: updated.Chapters[0].ID}}}, TaskTypes: []string{"video"}})
	if err == nil {
		t.Fatal("目录分组被保存为可执行章节范围")
	}
}

type courseReadQueryKey struct{}
type courseReadBarrier struct {
	read, resume chan struct{}
	once         sync.Once
}

func (b *courseReadBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT c.id::text") {
		return context.WithValue(ctx, courseReadQueryKey{}, true)
	}
	return ctx
}
func (b *courseReadBarrier) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if ctx.Value(courseReadQueryKey{}) == true {
		b.once.Do(func() {
			close(b.read)
			select {
			case <-b.resume:
			case <-ctx.Done():
			}
		})
	}
}

func TestCourseReadRemainsConsistentDuringSnapshotReplacement(t *testing.T) {
	pool := testPool(t)
	platform := hierarchyTestPlatform()
	service, err := New(pool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	courseID := courseIDForUser(t, pool, user)
	barrier := &courseReadBarrier{read: make(chan struct{}), resume: make(chan struct{})}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = barrier
	readerPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer readerPool.Close()
	var release sync.Once
	defer release.Do(func() { close(barrier.resume) })
	reader, err := New(readerPool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type result struct {
		detail CourseDetail
		err    error
	}
	completed := make(chan result, 1)
	go func() {
		detail, err := reader.GetCourse(ctx, Actor{ID: user, Kind: "user"}, courseID)
		completed <- result{detail, err}
	}()
	select {
	case <-barrier.read:
	case <-ctx.Done():
		t.Fatal("课程详情没有进入概要读取阶段")
	}
	platform.mu.Lock()
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[1].Title = "更新后的导学说明"
	snapshot.Chapters[1].Attachments = append(snapshot.Chapters[1].Attachments, PlatformAttachment{ObjectID: "new-video", TaskPointID: "new-task", Kind: "video"})
	platform.snapshots["101/202"] = snapshot
	platform.mu.Unlock()
	syncAll(t, service, user)
	release.Do(func() { close(barrier.resume) })
	select {
	case got := <-completed:
		if got.err != nil || got.detail.SnapshotVersion != 1 || len(got.detail.Chapters) != 6 || got.detail.Chapters[1].Title != "导学说明" || len(got.detail.Chapters[1].Attachments) != 3 {
			t.Fatalf("概要与章节来自不同快照：%+v / %v", got.detail, got.err)
		}
	case <-ctx.Done():
		t.Fatal("课程详情读取未完成")
	}
	latest, err := service.GetCourse(ctx, Actor{ID: user, Kind: "user"}, courseID)
	if err != nil || latest.SnapshotVersion != 2 || latest.Chapters[1].Title != "更新后的导学说明" {
		t.Fatalf("并发同步未实际提交：%+v / %v", latest, err)
	}
}

func TestCourseSnapshotBatchFailureRollsBackAllEarlierBatches(t *testing.T) {
	pool := testPool(t)
	stats := &snapshotBatchStats{}
	cfg := pool.Config()
	cfg.ConnConfig.Tracer = stats
	writerPool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer writerPool.Close()
	platform := defaultFakePlatform()
	service, err := New(writerPool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	actor := Actor{ID: user, Kind: "user"}
	if _, _, err := service.RequestCourseSync(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	job, err := service.claimCourseSync(context.Background())
	if err != nil || job == nil {
		t.Fatal("无法领取测试同步", err)
	}
	account, err := service.accountForUser(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters = nil
	for index := range 150 {
		snapshot.Chapters = append(snapshot.Chapters, PlatformChapter{ExternalID: fmt.Sprintf("chapter-%d", index),
			Attachments: []PlatformAttachment{{ObjectID: fmt.Sprintf("video-%d", index), TaskPointID: fmt.Sprintf("task-%d", index), Kind: "video"}}})
	}
	snapshot.Chapters[149].Attachments[0].CardIndex = -1
	if _, err := service.applyCourseSnapshot(context.Background(), job, account, snapshot); err == nil {
		t.Fatal("无效后续批次没有中止快照")
	}
	detail, err := service.GetCourse(context.Background(), actor, courseIDForUser(t, pool, user))
	if err != nil || detail.SnapshotVersion != 1 || len(detail.Chapters) != 1 || detail.Chapters[0].ExternalID != "5001" || len(taskIDsForUser(t, pool, user)) != 1 {
		t.Fatalf("失败批次部分覆盖了旧快照：%+v / %v", detail, err)
	}
	var leaked int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_chapters WHERE user_id=$1 AND external_id LIKE 'chapter-%'`, user).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("已写入的早期批次没有回滚：%d / %v", leaked, err)
	}
	// 相同数据修正异常后应能跨多个批次完整提交，不能只覆盖首批章节。
	snapshot.Chapters[149].Attachments[0].CardIndex = 0
	stats.batches.Store(0)
	stats.statements.Store(0)
	started := time.Now()
	version, err := service.applyCourseSnapshot(context.Background(), job, account, snapshot)
	elapsed := time.Since(started)
	if err != nil || version != 2 {
		t.Fatalf("大目录没有完整提交：%d / %v", version, err)
	}
	detail, err = service.GetCourse(context.Background(), actor, detail.ID)
	if err != nil || len(detail.Chapters) != 150 || detail.TaskPointCount != 150 || detail.Chapters[149].ExternalID != "chapter-149" {
		t.Fatalf("后续批次的章节或任务丢失：%d / %v", len(detail.Chapters), err)
	}
	t.Logf("150 个章节及 150 个任务点：内容写入 %d 个批次、%d 条语句，完整事务 %s", stats.batches.Load(), stats.statements.Load(), elapsed)
}

type snapshotBatchStats struct {
	batches, statements atomic.Int32
}

func (s *snapshotBatchStats) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	return ctx
}
func (s *snapshotBatchStats) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
func (s *snapshotBatchStats) TraceBatchStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceBatchStartData) context.Context {
	s.batches.Add(1)
	return ctx
}
func (s *snapshotBatchStats) TraceBatchQuery(context.Context, *pgx.Conn, pgx.TraceBatchQueryData) {
	s.statements.Add(1)
}
func (s *snapshotBatchStats) TraceBatchEnd(context.Context, *pgx.Conn, pgx.TraceBatchEndData) {}
