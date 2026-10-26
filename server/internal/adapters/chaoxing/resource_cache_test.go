package chaoxing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

type testResourceStore struct {
	mu      sync.Mutex
	entries map[string]cardLayout
}

func (s *testResourceStore) LoadCardIndexes(_ context.Context, key string) ([]int, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	return append([]int(nil), entry.indexes...), entry.expires, ok
}
func (s *testResourceStore) StoreCardIndexes(_ context.Context, key string, indexes []int, expires time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries == nil {
		s.entries = map[string]cardLayout{}
	}
	s.entries[key] = cardLayout{indexes: append([]int(nil), indexes...), expires: expires}
}

func TestSharedCardLayoutKeepsUserProgressFresh(t *testing.T) {
	var studyCalls, catalogCalls, cardCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			catalogCalls.Add(1)
			if r.URL.Query().Get("cpi") == "999" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprintf(w, `<div id="cur5001" title="章节标题" onclick="toOld('101','5001','%s')"></div>`, r.URL.Query().Get("clazzid"))
		case "/study":
			studyCalls.Add(1)
			io.WriteString(w, `<input id="cardcount" value="0">`)
		case "/cards":
			cardCalls.Add(1)
			passed := r.URL.Query().Get("cpi") == "303"
			played := 0
			if passed {
				played = 30000
			}
			fmt.Fprintf(w, `var mArg={"attachments":[{"objectId":"v1","jobid":"9001","type":"video","isPassed":%t,"playTime":%d}]};`, passed, played)
		}
	}))
	defer server.Close()
	store := &testResourceStore{}
	newPlatform := func() *Platform {
		platform := testAdapter(t, server)
		platform.options.endpoints.Study = server.URL + "/study"
		platform.SetResourceCache(store)
		return platform
	}
	platform := newPlatform()
	ref := learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", CycleKey: "current"}
	read := func(p *Platform, ref learning.PlatformCourseRef) learning.PlatformAttachment {
		t.Helper()
		snapshot, err := p.DiscoverCourse(context.Background(), encodeTestSession(t), ref)
		if err != nil || len(snapshot.Chapters) != 1 || len(snapshot.Chapters[0].Attachments) != 1 {
			t.Fatalf("读取失败：%#v %v", snapshot, err)
		}
		if snapshot.Chapters[0].Title != "章节标题" {
			t.Fatal("章节标题丢失")
		}
		return snapshot.Chapters[0].Attachments[0]
	}
	first := read(platform, ref)
	ref.CPI = "304"
	second := read(platform, ref)
	// 重建适配器代表进程重启，仍通过持久缓存省去章节目录请求。
	third := read(newPlatform(), ref)
	if !first.Passed || first.PlayedSeconds != 30 || second.Passed || third.Passed || second.PlayedSeconds != 0 {
		t.Fatalf("个人进度被共享：%+v %+v %+v", first, second, third)
	}
	if studyCalls.Load() != 1 || catalogCalls.Load() != 3 || cardCalls.Load() != 3 {
		t.Fatalf("共享缓存未减少结构请求或跳过了个人读取：study=%d catalog=%d cards=%d", studyCalls.Load(), catalogCalls.Load(), cardCalls.Load())
	}
	ref.ExternalClassID = "205"
	read(platform, ref)
	if studyCalls.Load() != 2 {
		t.Fatal("不同班级误用目录缓存")
	}
	ref.CPI = "999"
	if _, err := platform.DiscoverCourse(context.Background(), encodeTestSession(t), ref); err == nil {
		t.Fatal("缓存绕过了当前用户的课程访问校验")
	}
}

func TestResourceCacheExpiryAndConcurrentMiss(t *testing.T) {
	platform, _ := NewPlatform(Options{})
	var calls atomic.Int32
	gate := make(chan struct{})
	var wait sync.WaitGroup
	for range 12 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			indexes, err := platform.cachedCardIndexes(context.Background(), "key", func() ([]int, error) {
				calls.Add(1)
				<-gate
				return []int{0, 2}, nil
			})
			if err != nil || len(indexes) != 2 {
				t.Errorf("合并读取失败：%v %v", indexes, err)
			}
		}()
	}
	close(gate)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("并发回源未合并：%d", calls.Load())
	}
	platform.resourceCache.mu.Lock()
	entry := platform.resourceCache.entries["key"]
	entry.expires = time.Now().Add(-time.Second)
	platform.resourceCache.entries["key"] = entry
	platform.resourceCache.mu.Unlock()
	indexes, err := platform.cachedCardIndexes(context.Background(), "key", func() ([]int, error) {
		calls.Add(1)
		return []int{0, 1, 2}, nil
	})
	if err != nil || calls.Load() != 2 || len(indexes) != 3 {
		t.Fatal("到期后没有刷新目录")
	}
	// 返回数组不能修改缓存内存。
	indexes[0] = 31
	indexes, _ = platform.cachedCardIndexes(context.Background(), "key", nil)
	if indexes[0] != 0 {
		t.Fatal("调用者修改了共享缓存")
	}
}

func TestDiscoveryBoundsConcurrencyAndKeepsOrder(t *testing.T) {
	var active, peak atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			for index := range 10 {
				fmt.Fprintf(w, `toOld('101','%d','202');`, 5001+index)
			}
			return
		}
		current := active.Add(1)
		defer active.Add(-1)
		for {
			previous := peak.Load()
			if current <= previous || peak.CompareAndSwap(previous, current) {
				break
			}
		}
		time.Sleep(180 * time.Millisecond)
		io.WriteString(w, `var mArg={"attachments":[]};`)
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	snapshot, err := platform.DiscoverCourse(context.Background(), encodeTestSession(t), learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"})
	if err != nil || len(snapshot.Chapters) != 10 {
		t.Fatalf("并发读取失败：%+v %v", snapshot, err)
	}
	if peak.Load() <= 1 || peak.Load() > 4 {
		t.Fatalf("并发超出边界或仍为串行：%d", peak.Load())
	}
	for index, chapter := range snapshot.Chapters {
		if chapter.ExternalID != strconv.Itoa(5001+index) || chapter.SortOrder != index {
			t.Fatal("并发读取改变了章节顺序")
		}
	}
}

func TestEmptyCatalogRequiresMatchingContext(t *testing.T) {
	ref := learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}
	page := `<input id="courseId" value="101"><input id="clazzId" value="202"><input id="cpi" value="303"><div class="nodata"><p>暂无章节内容</p></div>`
	if !isEmptyCourseCatalog([]byte(page), ref) {
		t.Fatal("已核对课程的空目录没有识别")
	}
	ref.CPI = "304"
	if isEmptyCourseCatalog([]byte(page), ref) || isEmptyCourseCatalog([]byte(`<div class="nodata">暂无章节内容</div>`), ref) {
		t.Fatal("缺少或不匹配的课程上下文被当成空目录")
	}
	for _, payload := range []string{`mArg={broken}`, `mArg="";mArg={broken}`, `mArg={"unexpected":true};`, `<html>mArg 读取失败</html>`} {
		if _, _, err := parseAttachments([]byte(payload)); err == nil {
			t.Fatalf("损坏的配置被当成空章节：%s", payload)
		}
	}
}
