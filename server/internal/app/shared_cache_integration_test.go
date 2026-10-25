//go:build integration

package app

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/questions"
)

type sharedScopeFork struct {
	accountedFork
	scope atomic.Value
}

func (f *sharedScopeFork) Answer(ctx context.Context, input questions.ForkAnswerRequest, key string) (questions.ForkAnswerResponse, error) {
	f.scope.Store(input.CacheScope)
	return f.accountedFork.Answer(ctx, input, key)
}

func TestConfiguredSharedCacheReachesQuestionService(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "默认关闭", true: "显式开启"}[enabled], func(t *testing.T) {
			fork := &sharedScopeFork{}
			fixture := newRuntimeFixture(t, fixtureOptions{fork: fork, sharedCache: enabled})
			client := fixtureClient()
			defer client.CloseIdleConnections()
			csrf, _ := loginFixtureUser(t, fixture, client, "learner-a")
			modelID := seedOffering(t, fork, "共享缓存联调模型", "shared-cache-fixture",
				questions.CachePolicy{Allow: true, Scope: "shared", TTLDays: 7}, nil, true)
			status, _, caps := requestJSON(t, client, "GET", fixture.user.URL+"/api/v1/capabilities", "", nil, nil)
			if status != 200 || caps["features"].(map[string]any)["shared_question_cache"] != enabled {
				t.Fatal("公开能力与显式配置不一致")
			}
			status, _, _ = requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, map[string]any{
				"model_offering_id": modelID, "mode": "cache_first", "cache_scope": "shared",
				"question": map[string]any{"title": "共享缓存受控题", "type": "single", "options": "A. 第一项\nB. 第二项", "images": []string{}},
				"budget":   map[string]int{"max_output_tokens": 100},
			}, map[string]string{"Idempotency-Key": "shared-cache-runtime"})
			if enabled && (status != 200 || fork.scope.Load() != "shared") || !enabled && (status != 422 || fork.calls.Load() != 0) {
				t.Fatalf("共享缓存配置未真正控制调用链：enabled=%v HTTP=%d scope=%v calls=%d", enabled, status, fork.scope.Load(), fork.calls.Load())
			}
		})
	}
}
