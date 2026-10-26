package chaoxing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

var catalogTestRef = learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}

func catalogFixture(t testing.TB) []byte {
	t.Helper()
	payload, err := os.ReadFile("testdata/course_catalog.html")
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestCatalogPreservesPlatformHierarchyAndOrder(t *testing.T) {
	catalog, err := parseCourseCatalog(catalogFixture(t), catalogTestRef, 300)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Chapters) != 7 {
		t.Fatalf("目录节点缺失：%d", len(catalog.Chapters))
	}
	chapters := catalog.Chapters
	if !chapters[0].IsGroup || chapters[0].Title != "课程导学" || chapters[0].Number != "1" || !chapters[5].IsGroup || chapters[5].Title != "专题学习" {
		t.Fatalf("平台章标题没有保留：%+v", chapters)
	}
	wantIDs := []string{chapters[0].ExternalID, "900", "100", "700", "50", chapters[5].ExternalID, "800"}
	wantParents := []string{"", chapters[0].ExternalID, "900", "100", chapters[0].ExternalID, "", chapters[5].ExternalID}
	for index, chapter := range chapters {
		if chapter.ExternalID != wantIDs[index] || chapter.ParentExternalID != wantParents[index] || chapter.SortOrder != index {
			t.Fatalf("平台目录顺序或父子关系错位：%d / %+v", index, chapter)
		}
	}
	if chapters[2].Number != "1.1.1" || chapters[2].Title != "学习目标 & 要求" || catalog.Pending["900"] != 2 || catalog.Pending["100"] != 0 {
		t.Fatalf("编号、标题或个人进度串位：%+v / %v", chapters[2], catalog.Pending)
	}
}

func TestCatalogAcceptsEscapedCallbackQuotesAndChecksCPI(t *testing.T) {
	payload := `<input id="cpi" value="303"><div id="cur900" onclick="toOld(&quot;101&quot;,&quot;900&quot;,&quot;202&quot;)" title="学习小节"></div>`
	catalog, err := parseCourseCatalog([]byte(payload), catalogTestRef, 300)
	if err != nil || len(catalog.Chapters) != 1 || catalog.Chapters[0].ExternalID != "900" {
		t.Fatalf("HTML 属性引号没有被正确解码：%+v / %v", catalog, err)
	}
	if _, err := parseCourseCatalog([]byte(strings.Replace(payload, `value="303"`, `value="304"`, 1)), catalogTestRef, 300); err == nil {
		t.Fatal("目录中的个人身份上下文不匹配仍被读取")
	}
}

func TestCatalogRejectsPartialOrConflictingStructure(t *testing.T) {
	payload := string(catalogFixture(t))
	for name, page := range map[string]string{
		"课程归属不一致":  strings.Replace(payload, "toOld('101', '900'", "toOld('102', '900'", 1),
		"节点和入口不一致": strings.Replace(payload, `id="cur900"`, `id="cur901"`, 1),
		"重复小节":     payload + `<div id="cur900" title="重复"></div>`,
		"目录漏项":     payload + `<script>toOld('101','999','202')</script>`,
		"分组缺少标题":   strings.Replace(payload, `<span title="课程导学">课程导学</span>`, `<span></span>`, 1),
		"不明响应":     `<html><p>请重新登录</p></html>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCourseCatalog([]byte(page), catalogTestRef, 300); err == nil {
				t.Fatal("异常目录被保存为完整课程")
			}
		})
	}
	if _, err := parseCourseCatalog([]byte(payload), catalogTestRef, 6); err == nil {
		t.Fatal("超过上限后截断了完整目录")
	}
}

func TestDiscoverCatalogGroupsNeverRequestCards(t *testing.T) {
	page := catalogFixture(t)
	var mu sync.Mutex
	requested := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/catalog" {
			w.Write(page)
			return
		}
		mu.Lock()
		requested[r.URL.Query().Get("knowledgeid")]++
		mu.Unlock()
		io.WriteString(w, `mArg={"attachments":[]};`)
	}))
	defer server.Close()
	snapshot, err := testAdapter(t, server).DiscoverCourse(context.Background(), encodeTestSession(t), catalogTestRef)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"900": 1, "100": 1, "700": 1, "50": 1, "800": 1}
	mu.Lock()
	defer mu.Unlock()
	if len(snapshot.Chapters) != 7 || !reflect.DeepEqual(requested, want) || snapshot.Chapters[3].ParentExternalID != "100" {
		t.Fatalf("分组被读取为任务或并发读取改变层级：%v / %+v", requested, snapshot.Chapters)
	}
}

func TestCatalogPendingRejectsConflictingOrNestedCounts(t *testing.T) {
	row := parseHTMLDocument([]byte(`<div id="cur1"><span class="catalog_points_yi">2</span><div id="cur2"><span class="catalog_points_yi">0</span></div></div>`)).first(func(node *htmlNode) bool { return node.attr("id") == "cur1" })
	if count, known := catalogPendingCount(row); !known || count != 2 {
		t.Fatal("子章节的计数污染了当前章节")
	}
	row = parseHTMLDocument([]byte(`<div><span class="catalog_points_yi">0</span><span class="catalog_points_yi">1</span></div>`))
	if _, known := catalogPendingCount(row); known {
		t.Fatal("冲突计数被当成平台完成事实")
	}
}

// TestSavedCatalogPages 用显式提供的本地页面检查真实布局，不读取账号或发起平台请求。
func TestSavedCatalogPages(t *testing.T) {
	directory := os.Getenv("BYEBYEOCS_CATALOG_FIXTURE_DIR")
	if directory == "" {
		t.Skip("未提供本地目录页面")
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.html"))
	if err != nil || len(paths) == 0 {
		t.Fatal("本地目录页面不存在")
	}
	total, groups, maxDepth := 0, 0, 0
	for index, path := range paths {
		payload, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("本地目录页面无法读取")
		}
		root := parseHTMLDocument(payload)
		values := map[string]string{}
		root.walk(func(node *htmlNode) bool {
			if node.tag == "input" {
				values[node.attr("id")] = node.attr("value")
			}
			return true
		})
		ref := learning.PlatformCourseRef{ExternalCourseID: values["courseId"], ExternalClassID: values["clazzId"], CPI: values["cpi"]}
		catalog, err := parseCourseCatalog(payload, ref, 300)
		if err != nil {
			t.Fatalf("页面 %d 目录读取失败：%v", index, err)
		}
		depths := map[string]int{"": 0}
		for _, chapter := range catalog.Chapters {
			parentDepth, exists := depths[chapter.ParentExternalID]
			if !exists {
				t.Fatalf("页面 %d 目录父节点未先出现", index)
			}
			depths[chapter.ExternalID] = parentDepth + 1
			maxDepth = max(maxDepth, parentDepth+1)
			if chapter.IsGroup {
				groups++
			}
		}
		total += len(catalog.Chapters)
	}
	t.Logf("已读取 %d 个本地页面，%d 个目录节点，%d 个分组，最大 %d 层", len(paths), total, groups, maxDepth)
}

func BenchmarkParseCourseCatalog(b *testing.B) {
	var page strings.Builder
	for group := range 16 {
		fmt.Fprintf(&page, `<div class="chapter_unit"><div class="chapter_item"><div class="catalog_num">%d</div><div class="catalog_name">专题</div></div><div class="catalog_level"><ul>`, group+1)
		for chapter := range 16 {
			id := 1000 + group*16 + chapter
			fmt.Fprintf(&page, `<li><div class="chapter_item" id="cur%d" onclick="toOld('101','%d','202')" title="学习小节"><div class="catalog_name"><span class="catalog_sbar">%d.%d</span>学习小节</div><span class="catalog_points_yi">1</span></div></li>`, id, id, group+1, chapter+1)
		}
		page.WriteString(`</ul></div></div>`)
	}
	payload := []byte(page.String())
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for range b.N {
		if _, err := parseCourseCatalog(payload, catalogTestRef, 300); err != nil {
			b.Fatal(err)
		}
	}
}
