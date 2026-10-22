//go:build integration

package app

import (
	"testing"
)

func TestRuntimeTaskTypesReflectTheConfiguredExecutor(t *testing.T) {
	fixture := newRuntimeFixture(t, fixtureOptions{executor: automaticVideoFallback{}})
	code, _, body := requestJSON(t, fixtureClient(), "GET", fixture.user.URL+"/api/v1/capabilities", "", nil, nil)
	if code != 200 {
		t.Fatalf("能力读取失败: %d", code)
	}
	types, ok := body["task_types"].([]any)
	if !ok || len(types) != 5 {
		t.Fatalf("没有返回任务类型支持范围: %v", body["task_types"])
	}
	for _, raw := range types {
		item, ok := raw.(map[string]any)
		if !ok {
			t.Fatal("任务类型结构无效")
		}
		if item["available"] != (item["id"] == "video") {
			t.Fatalf("把未配置的任务执行器声明为支持: %v", item)
		}
	}
}
