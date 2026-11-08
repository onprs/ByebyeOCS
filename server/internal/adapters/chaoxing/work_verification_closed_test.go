package chaoxing

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func TestSubmittedCheckPreservesClosedWorkFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cards":
			io.WriteString(w, `<script>var mArg={"defaults":{"knowledgeid":"301","cpi":"303","ktoken":"token"},"attachments":[{"type":"workid","jobid":"work-1"}]};</script>`)
		case "/work":
			io.WriteString(w, `<html><body><input type="hidden" name="ktoken" value="token"><div class="singleQuesId" data="1"><div class="Zy_TItle">只读问题</div><ul class="Zy_ulTop"><li>A. 内容</li></ul></div></body></html>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	platform.options.endpoints.Work = server.URL + "/work"
	ref := learning.PlatformTaskRef{Course: learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"}, ChapterExternalID: "301", ObjectID: "work-1", TaskPointID: "work-1"}
	submitted, _, err := platform.checkWorkSubmitted(context.Background(), testSession(), ref)
	var failure *learning.Failure
	if submitted || !errors.As(err, &failure) || failure.Class != learning.FailurePlatformClosed {
		t.Fatalf("关闭事实被吞掉: submitted=%t err=%v", submitted, err)
	}
}
