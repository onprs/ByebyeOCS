//go:build integration

package questions

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestCachePolicySnapshotSurvivesPublishedChanges(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "新查询恢复保留原期限"
		if legacy {
			name = "旧查询恢复保留旧协议"
		}
		t.Run(name, func(t *testing.T) {
			fork := newIdempotentFork()
			env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
			model := env.createOffering(t, "缓存策略恢复模型", "policy-recovery", testPricing(10_000_000, 20_000_000, 10000), true)
			input := queryBody("恢复时缓存策略保持不变", "cache_first")
			input.ModelOfferingID = model.ID
			key := "cache-policy-recovery"
			offering, scopeKey, queryID := seedStalePending(t, env, model, &input, key)
			first := forkRequestFor(queryID, offering, input, scopeKey)
			if legacy {
				first.CachePolicy = nil
				if _, err := env.pool.Exec(context.Background(), `UPDATE public.question_queries SET fork_cache_policy=NULL WHERE id=$1`, queryID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fork.Answer(context.Background(), first, queryID); err != nil {
				t.Fatal(err)
			}
			if _, err := env.pool.Exec(context.Background(), `UPDATE public.question_offerings SET cache_policy='{"allow":true,"scope":"user","ttl_days":1}'::jsonb WHERE public_id=$1`, model.ID); err != nil {
				t.Fatal(err)
			}
			view, err := env.service.Answer(context.Background(), env.userID, key, input)
			if err != nil || view.Status != "answered" {
				t.Fatal("缓存策略变更后恢复失败", err, view.Status)
			}
			before, _ := json.Marshal(first)
			after, _ := json.Marshal(fork.lastRequest())
			if !bytes.Equal(before, after) {
				t.Fatal("恢复重建了不同的内部请求，实际 fork 会产生幂等冲突")
			}
			if httpCalls, modelCalls := fork.counts(); httpCalls != 2 || modelCalls != 1 {
				t.Fatal("恢复重复调用了模型", httpCalls, modelCalls)
			}
		})
	}
}
