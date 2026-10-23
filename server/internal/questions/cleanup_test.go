//go:build integration

package questions

import (
	"context"
	"strings"
	"testing"
)

// 旧时间戳夹具：按 completed_at 清理终态且账务已落定的查询内容，保留幂等、用量、
// 价格与账本关联；pending/needs_review、账务异常和未到期查询原样保留。
func TestRunCleanupSanitizesExpiredContentOnly(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "清理模型", "internal-cleanup", nil, true)
	keys := []string{
		"cleanup-answered", "cleanup-cache", "cleanup-failed",
		"cleanup-pending", "cleanup-review", "cleanup-billing-review", "cleanup-recent",
	}
	statuses := []string{"answered", "cache_hit", "failed", "pending", "needs_review", "answered", "answered"}
	ids := make([]string, len(keys))
	for i, key := range keys {
		view := mustAnswer(t, env, key, queryBody("清理题"+key, "cache_first"))
		ids[i] = view.ID
		if statuses[i] != "answered" {
			if _, err := env.pool.Exec(context.Background(), `UPDATE public.question_queries
				SET status = $2 WHERE id = $1`, view.ID, statuses[i]); err != nil {
				t.Fatal(err)
			}
		}
	}

	// 除 cleanup-answered 外 created_at 都是旧的，证明清理依据 completed_at；
	// cleanup-recent 的 completed_at 是新的，必须保留。
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := env.pool.Exec(context.Background(), sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE public.question_queries SET created_at = now() - interval '30 days',
		updated_at = now() - interval '30 days', completed_at = now() - interval '30 days'
		WHERE user_id = $1 AND idempotency_key LIKE 'cleanup-%' AND idempotency_key <> 'cleanup-answered'`, env.userID)
	exec(`UPDATE public.question_queries SET completed_at = now() - interval '30 days',
		updated_at = now() - interval '30 days'
		WHERE user_id = $1 AND idempotency_key = 'cleanup-answered'`, env.userID)
	exec(`UPDATE public.question_queries SET completed_at = now()
		WHERE user_id = $1 AND idempotency_key = 'cleanup-recent'`, env.userID)
	exec(`UPDATE public.question_queries SET billing_state = 'needs_review'
		WHERE user_id = $1 AND idempotency_key = 'cleanup-billing-review'`, env.userID)

	type fact struct {
		usage, pricing, billing string
		reserved, version       int64
	}
	before := make(map[string]fact, len(ids))
	for _, id := range ids {
		var item fact
		if err := env.pool.QueryRow(context.Background(), `SELECT usage::text, pricing::text, billing_state,
			reserved_minor, version FROM public.question_queries WHERE id = $1`, id).
			Scan(&item.usage, &item.pricing, &item.billing, &item.reserved, &item.version); err != nil {
			t.Fatal(err)
		}
		before[id] = item
	}

	result, err := env.service.RunCleanup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.QuestionsSanitized != 3 {
		t.Fatalf("应只清理 answered/cache_hit/failed 且账务已落定的到期查询，实际 %d", result.QuestionsSanitized)
	}

	for i, id := range ids {
		var title, options, answer, status, usage, pricing, billing string
		var expired bool
		var reserved, version int64
		if err := env.pool.QueryRow(context.Background(), `SELECT COALESCE(question_title, ''), COALESCE(question_options, ''),
			COALESCE(answer, ''), status, content_expired, usage::text, pricing::text, billing_state,
			reserved_minor, version FROM public.question_queries WHERE id = $1`, id).
			Scan(&title, &options, &answer, &status, &expired, &usage, &pricing, &billing, &reserved, &version); err != nil {
			t.Fatal(err)
		}
		item := before[id]
		if status != statuses[i] {
			t.Fatalf("清理不得改变状态：%s/%s", status, statuses[i])
		}
		sanitized := i < 3
		if sanitized {
			if title != "" || options != "" || answer != "" || !expired {
				t.Fatalf("终态到期查询内容应已脱敏：%s %q/%q/%q/%v", status, title, options, answer, expired)
			}
			if usage != item.usage || pricing != item.pricing || billing != item.billing || reserved != item.reserved ||
				version != item.version {
				t.Fatalf("清理不得改变用量/价格/账本关联/版本：%s %s %s %s %d/%d %d/%d",
					usage, item.usage, pricing, item.pricing, reserved, item.reserved, version, item.version)
			}
			continue
		}
		if !strings.HasPrefix(title, "清理题") || options == "" || answer == "" || expired {
			t.Fatalf("受保护查询必须保留原内容：%s %q/%v", status, title, expired)
		}
		if usage != item.usage || billing != item.billing || reserved != item.reserved || version != item.version {
			t.Fatalf("受保护查询不得被清理：%s/%s %d/%d", usage, item.usage, version, item.version)
		}
	}

	again, err := env.service.RunCleanup(context.Background())
	if err != nil || again.QuestionsSanitized != 0 {
		t.Fatalf("重复清理应无改动：%d/%v", again.QuestionsSanitized, err)
	}
}
