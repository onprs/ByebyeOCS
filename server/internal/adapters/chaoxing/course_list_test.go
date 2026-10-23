package chaoxing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCourseListAcceptsHTMLLinksAndStableNames(t *testing.T) {
	for _, item := range []struct{ name, page string }{
		{"HTML转义和参数顺序", `<section>
			<span title='课程甲' class='course-name'></span><a href='/visit/stucoursemiddle?cpi=303&amp;courseid=101&amp;clazzid=202'>进入</a>
			<span class='course-name' title='课程乙'></span><a href='/visit/stucoursemiddle?clazzid=205&amp;courseid=104&amp;cpi=306&amp;v=2'>进入</a>
		</section>`},
		{"封面和名称重复链接", `<section>
			<a href='/visit/stucoursemiddle?courseid=101&clazzid=202&cpi=303'>封面</a>
			<a href='/visit/stucoursemiddle?courseid=101&clazzid=202&cpi=303'>进入</a><span class="course-name" title="课程甲"></span>
			<a href='/visit/stucoursemiddle?courseid=104&clazzid=205&cpi=306'>进入</a><span class="course-name" title="课程乙"></span>
		</section>`},
	} {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, item.page) }))
			defer server.Close()
			courses, err := testAdapter(t, server).ListCourses(context.Background(), encodeTestSession(t))
			if err != nil || len(courses) != 2 {
				t.Fatalf("合法课程页未识别完整列表：%#v/%v", courses, err)
			}
			if courses[0].ExternalCourseID != "101" || courses[0].ExternalClassID != "202" || courses[0].CPI != "303" || courses[0].Title != "课程甲" || courses[1].ExternalCourseID != "104" || courses[1].Title != "课程乙" {
				t.Fatalf("课程身份或名称串位：%#v", courses)
			}
		})
	}
}
