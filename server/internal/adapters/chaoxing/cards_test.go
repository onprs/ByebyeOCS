package chaoxing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func TestChapterCardDirectoryAndBounds(t *testing.T) {
	course := learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}
	for _, test := range []struct {
		name string
		page string
		want []int
	}{
		{"标签列出全部卡片", `<ul id="prev_tab"><li cardid="first"></li><li cardid="second"></li><li cardid="third"></li></ul><iframe src="/knowledge/cards?num=0&knowledgeid=5001"></iframe>`, []int{0, 1, 2}},
		{"末页编号", `<input id="cardcount" value="2"><iframe src="/knowledge/cards?num=0"></iframe>`, []int{0, 1, 2}},
		{"单卡片", `<input id="cardcount" value="0">`, []int{0}},
		{"未知目录", `<html>内容未加载</html>`, nil},
		{"超出上限", `<input id="cardcount" value="40">`, nil},
		{"目录归属不符", `<iframe src="/knowledge/cards?num=1&knowledgeid=9999"></iframe>`, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := chapterCardIndexes([]byte(test.page), course, "5001")
			if test.want == nil {
				if err == nil {
					t.Fatal("未知或冲突目录被当成完整目录")
				}
			} else if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("页码不符: %v / %v", got, err)
			}
		})
	}
}

func TestDiscoverAllCardsAndVerifyTheMatchingCard(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/study":
			fmt.Fprint(w, `<ul id="prev_tab"><li cardid="one"></li><li cardid="two"></li><li cardid="three"></li></ul>`)
		case "/catalog":
			fmt.Fprint(w, `<script>toOld('101','5001','202')</script><div id="cur5001"><span class="catalog_points_yi">3</span></div>`)
		case "/cards":
			var item map[string]any
			switch r.URL.Query().Get("num") {
			case "0":
				item = map[string]any{"type": "video", "objectId": "video", "jobid": "job-video", "isPassed": false}
			case "1":
				item = map[string]any{"type": "read", "jobid": "job-read", "isPassed": false}
			case "2":
				item = map[string]any{"type": "workid", "jobid": "work-quiz", "isPassed": true}
			default:
				t.Error("请求了目录以外的卡片")
			}
			data, _ := json.Marshal(map[string]any{"attachments": []any{item}})
			fmt.Fprintf(w, "<script>mArg=%s;</script>", data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	platform.options.endpoints.Study = server.URL + "/study"
	course := learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}
	snapshot, err := platform.DiscoverCourse(context.Background(), encodeTestSession(t), course)
	if err != nil {
		t.Fatal(err)
	}
	items := snapshot.Chapters[0].Attachments
	if len(items) != 3 || items[1].Kind != "document" || items[1].ObjectID != "job-read" || items[2].CardIndex != 2 {
		t.Fatalf("后续卡片或无objectId的阅读任务遗漏: %+v", items)
	}
	check, err := platform.CheckTask(context.Background(), encodeTestSession(t), learning.PlatformTaskRef{Course: course, ChapterExternalID: "5001", CardIndex: 2, ObjectID: "work-quiz", TaskPointID: "work-quiz"})
	if err != nil || !check.Passed || check.Status != learning.CheckConfirmed {
		t.Fatalf("独立核验没有读取目标卡片: %+v / %v", check, err)
	}
}

func TestEmptyCardsAndUnknownTaskPointsStayVisible(t *testing.T) {
	items, skipped, err := parseAttachments([]byte(`mArg={"attachments":[]};`))
	if err != nil || skipped != 0 || len(items) != 0 {
		t.Fatalf("空卡片阻止同步: %v / %d", err, skipped)
	}
	items, skipped, err = parseAttachments([]byte(`mArg={"attachments":[{"type":"live","jobid":"live-job"},{"type":"video","objectId":"file","property":{"type":".mp3"},"jobid":"audio-job"},{"type":"document","objectId":"ordinary"}]};`))
	if err != nil || skipped != 0 || len(items) != 3 || items[0].Kind != "other" || items[0].ObjectID != "live-job" || items[1].Kind != "audio" || items[2].TaskPointID != "" {
		t.Fatalf("类型或任务点识别错误: %+v / %v", items, err)
	}
}

func TestChapterPendingNeverUsesTheNextChapterCount(t *testing.T) {
	page := []byte(`<div id="cur5001"><span>本章没有完成字段</span></div><div id="cur5002"><span class="catalog_points_yi">0</span></div>`)
	pending := chapterPendingCounts(page, []string{"5001", "5002"})
	if _, exists := pending["5001"]; exists || pending["5002"] != 0 {
		t.Fatalf("把下一章节的0分配给当前章节: %v", pending)
	}
}

func TestCatalogLimitDoesNotSilentlyDropChapters(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<script>toOld('101','5001','202');toOld('101','5002','202');</script>`)
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	platform.options.MaxChapters = 1
	_, _, err := platform.catalog(context.Background(), testSession(), learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"})
	if err == nil {
		t.Fatal("超出上限时保存了被截断的完整目录")
	}
}

func TestEmptyChapterCardsAreNotCapabilityFailure(t *testing.T) {
	attachments, skipped, err := parseAttachments([]byte(`<script>mArg = "";</script>`))
	if err != nil || len(attachments) != 0 || skipped != 0 {
		t.Fatalf("空章节卡片应视为无任务点: %v %d %d", err, len(attachments), skipped)
	}
	if _, _, err := parseAttachments([]byte(`<html>登录已失效</html>`)); err == nil {
		t.Fatal("没有卡片配置的页面仍应失败")
	}
}
