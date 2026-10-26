//go:build integration

package learning

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type coverFakePlatform struct {
	*fakePlatform
	calls atomic.Int32
	fail  atomic.Bool
	image []byte
	gate  chan struct{}
}

func (f *coverFakePlatform) FetchCourseCover(ctx context.Context, source string) (CourseCoverImage, error) {
	f.calls.Add(1)
	if f.gate != nil {
		select {
		case <-ctx.Done():
			return CourseCoverImage{}, ctx.Err()
		case <-f.gate:
		}
	}
	if f.fail.Load() {
		return CourseCoverImage{}, Fail(FailureTransient, "测试封面不可用")
	}
	return CourseCoverImage{Bytes: f.image}, nil
}

func coverTestPlatform(t *testing.T) *coverFakePlatform {
	t.Helper()
	var output bytes.Buffer
	if err := jpeg.Encode(&output, image.NewRGBA(image.Rect(0, 0, 4, 3)), nil); err != nil {
		t.Fatal(err)
	}
	platform := &coverFakePlatform{fakePlatform: defaultFakePlatform(), image: output.Bytes()}
	platform.courses[0].CoverURL = "https://p.ananas.chaoxing.com/course-cover.jpg"
	platform.courses[0].CoverKnown = true
	return platform
}

func TestCourseCoversShareBytesWithoutSharingCourseAuthorization(t *testing.T) {
	pool := testPool(t)
	platform := coverTestPlatform(t)
	service, err := New(pool, fastOptions(platform, nil))
	if err != nil {
		t.Fatal(err)
	}
	first, second := seedUser(t, pool), seedUser(t, pool)
	for _, userID := range []string{first, second} {
		bindTestAccount(t, service, userID)
		syncAll(t, service, userID)
	}
	pageA, _ := service.ListCourses(context.Background(), Actor{ID: first, Kind: "user"}, CourseFilter{})
	pageB, _ := service.ListCourses(context.Background(), Actor{ID: second, Kind: "user"}, CourseFilter{})
	if len(pageA.Items) != 1 || len(pageB.Items) != 1 || pageA.Items[0].CoverURL == "" || pageB.Items[0].CoverURL == "" {
		t.Fatal("课程没有发布封面路径")
	}
	raw, _ := json.Marshal(pageA)
	if bytes.Contains(raw, []byte("p.ananas.chaoxing.com")) {
		t.Fatal("接口泄露了上游图片来源")
	}
	detail, err := service.GetCourse(context.Background(), Actor{ID: first, Kind: "user"}, pageA.Items[0].ID)
	if err != nil || detail.CoverURL != pageA.Items[0].CoverURL {
		t.Fatal("详情与列表的封面不一致", err)
	}
	server := newTestMux(t, service, map[string]Actor{"a": {ID: first, Kind: "user"}, "b": {ID: second, Kind: "user"}})
	targetA, targetB := server.URL+"/api/v1/"+pageA.Items[0].CoverURL, server.URL+"/api/v1/"+pageB.Items[0].CoverURL
	response, payload := requestJSON(t, "GET", targetA, nil, map[string]string{"X-Test-Actor": "a"})
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal(payload, platform.image) {
		t.Fatal("封面接口未返回真实JPEG内容", response.StatusCode)
	}
	etag := response.Header.Get("ETag")
	if etag == "" || !strings.Contains(response.Header.Get("Cache-Control"), "private") || !strings.Contains(response.Header.Get("Vary"), "Authorization") {
		t.Fatal("图片响应缺少私有缓存约束")
	}
	response, _ = requestJSON(t, "GET", targetB, nil, map[string]string{"X-Test-Actor": "b"})
	if response.StatusCode != http.StatusOK || platform.calls.Load() != 1 {
		t.Fatalf("第二个用户未复用持久图片缓存：%d %d", response.StatusCode, platform.calls.Load())
	}
	response, payload = requestJSON(t, "GET", targetA, nil, map[string]string{"X-Test-Actor": "a", "If-None-Match": etag})
	if response.StatusCode != http.StatusNotModified || len(payload) != 0 {
		t.Fatal("封面条件请求未生效")
	}
	response, _ = requestJSON(t, "GET", targetA, nil, map[string]string{"X-Test-Actor": "b", "If-None-Match": etag})
	if response.StatusCode != http.StatusNotFound {
		t.Fatal("共享缓存绕过了课程归属检查")
	}
	response, _ = requestJSON(t, "GET", targetA, nil, nil)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("匿名用户读取了课程封面")
	}
	restarted, _ := New(pool, fastOptions(platform, nil))
	if _, err := restarted.GetCourseCover(context.Background(), Actor{ID: second, Kind: "user"}, pageB.Items[0].ID, ""); err != nil || platform.calls.Load() != 1 {
		t.Fatal("服务重启后没有复用缓存", err)
	}
}

func TestCoverNegativeCacheExpiresAndSourceChangesInvalidateURLs(t *testing.T) {
	pool := testPool(t)
	platform := coverTestPlatform(t)
	platform.fail.Store(true)
	service, _ := New(pool, fastOptions(platform, nil))
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	page, _ := service.ListCourses(context.Background(), actor, CourseFilter{})
	courseID := page.Items[0].ID
	for range 2 {
		if _, err := service.GetCourseCover(context.Background(), actor, courseID, ""); err == nil {
			t.Fatal("错误图片不应被返回")
		}
	}
	if platform.calls.Load() != 1 {
		t.Fatal("封面失败后反复回源")
	}
	if _, err := pool.Exec(context.Background(), `UPDATE public.learning_course_cover_cache SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	platform.fail.Store(false)
	if _, err := service.GetCourseCover(context.Background(), actor, courseID, ""); err != nil || platform.calls.Load() != 2 {
		t.Fatal("过期后的封面没有重试", err)
	}
	oldHash := coverSourceHash(platform.courses[0].CoverURL)
	platform.mu.Lock()
	platform.courses[0].CoverURL = "https://p.ananas.chaoxing.com/updated-course-cover.jpg"
	platform.mu.Unlock()
	syncAll(t, service, userID)
	if _, err := service.GetCourseCover(context.Background(), actor, courseID, oldHash); err == nil {
		t.Fatal("旧封面版本仍被返回")
	}
	if _, err := service.GetCourseCover(context.Background(), actor, courseID, ""); err != nil || platform.calls.Load() != 3 {
		t.Fatal("新来源错误复用了旧图片", err)
	}
	platform.mu.Lock()
	platform.courses[0].CoverURL = ""
	platform.mu.Unlock()
	syncAll(t, service, userID)
	page, _ = service.ListCourses(context.Background(), actor, CourseFilter{})
	if page.Items[0].CoverURL != "" {
		t.Fatal("明确无封面时保留了旧地址")
	}
}

func TestConcurrentCoverRequestsCoalesceAndCacheRemainsBounded(t *testing.T) {
	pool := testPool(t)
	platform := coverTestPlatform(t)
	platform.gate = make(chan struct{})
	service, _ := New(pool, fastOptions(platform, nil))
	userID := seedUser(t, pool)
	bindTestAccount(t, service, userID)
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	page, _ := service.ListCourses(context.Background(), actor, CourseFilter{})
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := service.GetCourseCover(context.Background(), actor, page.Items[0].ID, ""); err != nil {
				t.Error("并发封面读取失败", err)
			}
		}()
	}
	waitFor(t, time.Second, func() bool { return platform.calls.Load() == 1 })
	close(platform.gate)
	wait.Wait()
	if platform.calls.Load() != 1 {
		t.Fatal("相同封面并发未命中时重复回源")
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.learning_course_cover_cache (source_hash,expires_at,updated_at)
		SELECT md5(s::text)||md5(s::text),now()+interval '1 day',now()-interval '1 hour'
		FROM generate_series(1,1025) s`); err != nil {
		t.Fatal(err)
	}
	if err := service.storeCourseCover(context.Background(), coverSourceHash("new-source"), courseCover{}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM public.learning_course_cover_cache`).Scan(&count); err != nil || count != courseCoverCacheLimit {
		t.Fatalf("图片缓存无界增长：%d %v", count, err)
	}
}
