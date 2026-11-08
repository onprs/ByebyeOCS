package chaoxing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const closedChapterCatalogFixture = `<input id="courseId" value="101"><input id="clazzId" value="202"><input id="cpi" value="303">
<div class="chapter_item" id="cur701"><div class="catalog_name"><span class="catalog_sbar">1.1</span>开放小节</div><a onclick="toOld('101','701','202')">进入</a></div>
<div class="chapter_item" id="cur702"><div class="catalog_name"><span class="catalog_sbar">1.2</span>关闭小节</div><div class="catalog_state icon-bukaifang"><span>已关闭</span></div></div>`

func TestCatalogSupportsExplicitlyClosedChaptersWithoutLinks(t *testing.T) {
	catalog, err := parseCourseCatalog([]byte(closedChapterCatalogFixture), catalogTestRef, 300)
	if err != nil || len(catalog.Chapters) != 2 {
		t.Fatalf("关闭目录读取失败：%+v/%v", catalog, err)
	}
	closed := catalog.Chapters[1]
	if closed.ExternalID != "702" || !closed.Closed || closed.IsGroup || closed.Number != "1.2" || closed.Title != "关闭小节" {
		t.Fatalf("关闭小节身份或状态丢失：%+v", closed)
	}
	if _, known := catalog.Pending["702"]; known {
		t.Fatal("关闭被当成已完成")
	}
	for name, page := range map[string]string{
		"关闭节点入口冲突": strings.Replace(closedChapterCatalogFixture, `<div class="catalog_state icon-bukaifang">`, `<a onclick="toOld('101','701','202')">错误入口</a><div class="catalog_state icon-bukaifang">`, 1),
		"缺少关闭证据":   strings.Replace(closedChapterCatalogFixture, "icon-bukaifang", "", 1),
		"缺少关闭取值":   strings.Replace(closedChapterCatalogFixture, "已关闭", "状态未知", 1),
		"缺少课程上下文":  strings.Replace(closedChapterCatalogFixture, `<input id="courseId" value="101">`, "", 1),
		"课程上下文冲突":  strings.Replace(closedChapterCatalogFixture, `id="clazzId" value="202"`, `id="clazzId" value="999"`, 1),
		"身份上下文缺失":  strings.Replace(closedChapterCatalogFixture, `<input id="cpi" value="303">`, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCourseCatalog([]byte(page), catalogTestRef, 300); err == nil {
				t.Fatal("缺少可信依据仍接受无入口小节")
			}
		})
	}
}

func TestDiscoverClosedChaptersNeverRequestHiddenCards(t *testing.T) {
	var mu sync.Mutex
	requested := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			io.WriteString(w, closedChapterCatalogFixture)
			return
		}
		knowledge := r.URL.Query().Get("knowledgeid")
		mu.Lock()
		requested = append(requested, knowledge)
		mu.Unlock()
		io.WriteString(w, `mArg={"defaults":{"lastmodifytime":5},"attachments":[]};`)
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	for _, hinted := range []bool{false, true} {
		var snapshot learning.PlatformCourseSnapshot
		var err error
		if hinted {
			snapshot, err = platform.DiscoverCourseWithHint(context.Background(), encodeTestSession(t), catalogTestRef, learning.CourseStructureHint{ModifiedAt: 5, Indexes: map[string][]int{"701": {0}}})
		} else {
			snapshot, err = platform.DiscoverCourse(context.Background(), encodeTestSession(t), catalogTestRef)
		}
		if err != nil || len(snapshot.Chapters) != 2 || !snapshot.Chapters[1].Closed || len(snapshot.Chapters[1].Attachments) != 0 {
			t.Fatalf("关闭章节发现失败：%+v/%v", snapshot, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requested) != 2 || requested[0] != "701" || requested[1] != "701" {
		t.Fatalf("读取了隐藏卡片：%v", requested)
	}
}
