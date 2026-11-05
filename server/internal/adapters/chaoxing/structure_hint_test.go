package chaoxing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// 结构指纹一致时跳过逐章页码枚举；指纹不一致或出现新章节时退回完整发现。
func TestDiscoverCourseWithStructureHint(t *testing.T) {
	var studyCalls, catalogCalls, cardCalls atomic.Int32
	modified := atomic.Int64{}
	modified.Store(123)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog":
			catalogCalls.Add(1)
			fmt.Fprintf(w, `<div id="cur5001" title="第一章" onclick="toOld('101','5001','202')"></div>`)
			fmt.Fprintf(w, `<div id="cur5002" title="第二章" onclick="toOld('101','5002','202')"></div>`)
		case "/study":
			studyCalls.Add(1)
			io.WriteString(w, `<input id="cardcount" value="0">`)
		case "/cards":
			cardCalls.Add(1)
			fmt.Fprintf(w, `var mArg={"defaults":{"lastmodifytime":%d},"attachments":[{"objectId":"v1","jobid":"9001","type":"video","isPassed":false,"playTime":0}]};`, modified.Load())
		}
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	platform.options.endpoints.Study = server.URL + "/study"
	ref := learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", CycleKey: "current"}
	session := encodeTestSession(t)

	first, err := platform.DiscoverCourse(context.Background(), session, ref)
	if err != nil {
		t.Fatal(err)
	}
	if first.PlatformModifiedAt != 123 {
		t.Fatalf("首次发现没有记录结构指纹：%d", first.PlatformModifiedAt)
	}
	if studyCalls.Load() != 2 || cardCalls.Load() != 2 {
		t.Fatalf("首次发现请求数不符：study=%d cards=%d", studyCalls.Load(), cardCalls.Load())
	}

	hint := learning.CourseStructureHint{ModifiedAt: first.PlatformModifiedAt,
		Indexes: map[string][]int{"5001": {0}, "5002": {0}}}
	second, err := platform.DiscoverCourseWithHint(context.Background(), session, ref, hint)
	if err != nil {
		t.Fatal(err)
	}
	if second.PlatformModifiedAt != 123 || len(second.Chapters) != 2 {
		t.Fatalf("指纹命中结果不符：%+v", second)
	}
	if studyCalls.Load() != 2 {
		t.Fatalf("指纹命中仍然请求了页码目录：%d", studyCalls.Load())
	}
	if cardCalls.Load() != 4 {
		t.Fatalf("指纹命中没有按已知页码读取卡片：%d", cardCalls.Load())
	}

	// 平台时间戳变化：必须退回完整发现，重新枚举页码。
	modified.Store(456)
	third, err := platform.DiscoverCourseWithHint(context.Background(), session, ref, hint)
	if err != nil {
		t.Fatal(err)
	}
	if third.PlatformModifiedAt != 456 {
		t.Fatalf("指纹变化后没有更新修改时间：%d", third.PlatformModifiedAt)
	}
	if studyCalls.Load() != 4 {
		t.Fatalf("指纹变化后没有重新枚举页码：%d", studyCalls.Load())
	}

	// 出现新章节（指纹缺键）：退回完整发现。
	modified.Store(456)
	missing := learning.CourseStructureHint{ModifiedAt: 456, Indexes: map[string][]int{"5001": {0}}}
	if _, err := platform.DiscoverCourseWithHint(context.Background(), session, ref, missing); err != nil {
		t.Fatal(err)
	}
	if studyCalls.Load() != 6 {
		t.Fatalf("新章节没有触发完整发现：%d", studyCalls.Load())
	}

	// 已知但无附件的章节用空列表表示，不应被当成新章节。
	modified.Store(456)
	emptyChapter := learning.CourseStructureHint{ModifiedAt: 456,
		Indexes: map[string][]int{"5001": {0}, "5002": {}}}
	before := studyCalls.Load()
	if _, err := platform.DiscoverCourseWithHint(context.Background(), session, ref, emptyChapter); err != nil {
		t.Fatal(err)
	}
	if studyCalls.Load() != before {
		t.Fatalf("空章节被误判为新章节：%d", studyCalls.Load()-before)
	}
}
