package questions

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 释放接口对不存在的查询返回 404，不能被当成服务故障。
func TestReleaseNotFoundMapsTo404(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/admin/api/v1/question-queries/qq_00000000000000000000000000000000/releases", nil)
	writeQueryError(recorder, request, errQueryNotFound)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("不存在应返回 404，实际 %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), `"not_found"`) {
		t.Fatalf("错误码不符：%s", recorder.Body.String())
	}
}
