//go:build integration

package learning

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestTaskCategoryPagesShareCountsAndRejectOldScopes(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	platform := defaultFakePlatform()
	snapshot := platform.snapshots["101/202"]
	snapshot.Chapters[0].Attachments = nil
	for i := 0; i < 52; i++ {
		snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments, PlatformAttachment{ObjectID: fmt.Sprintf("page-media-%d", i), TaskPointID: fmt.Sprintf("page-job-%d", i), Kind: "video", Title: "分页任务", PassedKnown: true})
	}
	platform.snapshots["101/202"] = snapshot
	service := newVideoLearningService(t, pool, platform)
	user := seedUser(t, pool)
	bindTestAccount(t, service, user)
	syncAll(t, service, user)
	course := courseIDForUser(t, pool, user)
	plan := createVideoPlan(t, service, user, course)
	actor := Actor{ID: user, Kind: "user"}
	ids := taskIDsForUser(t, pool, user)
	for i, id := range ids {
		state, code := "pending", ""
		if i >= 15 && i < 32 {
			state = "needs_attention"
			code = "answer_unavailable"
		}
		if i >= 32 && i < 43 {
			state = "needs_attention"
			code = "quiz_closed"
		}
		if i >= 43 {
			state = "confirmed"
		}
		if _, err := pool.Exec(ctx, `UPDATE public.learning_tasks SET state=$2,last_error_code=$3,created_at=$4 WHERE id=$1`, id, state, code, time.Now().Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	wanted := map[string]int{"current": 15, "attention": 17, "closed": 11, "history": 9}
	var attentionCursor string
	for category, count := range wanted {
		seen := map[string]bool{}
		cursor := ""
		for {
			page, err := service.ListTasks(ctx, actor, TaskFilter{PlanID: plan.ID, Category: category, Limit: 10, Cursor: cursor})
			if err != nil || page.TotalCount == nil || *page.TotalCount != count || page.TaskMeta == nil {
				t.Fatalf("分类、计数未原子返回：%s/%+v/%v", category, page, err)
			}
			p := page.TaskMeta.Progress
			if p.Total != 52 || p.Active != 15 || p.Attention != 17 || p.Closed != 11 || p.Completed != 9 {
				t.Fatalf("分类汇总不一致：%+v", p)
			}
			for _, task := range page.Items {
				if task.Category != category || seen[task.ID] {
					t.Fatalf("错误分类或重复行：%+v", task)
				}
				seen[task.ID] = true
			}
			if category == "attention" && cursor == "" {
				attentionCursor = page.NextCursor
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor == cursor {
				t.Fatal("游标未推进")
			}
			cursor = page.NextCursor
		}
		if len(seen) != count {
			t.Fatalf("实际行数与统计不同：%s/%d/%d", category, len(seen), count)
		}
	}
	plans, err := service.ListPlans(ctx, actor, PlanFilter{})
	if err != nil || plans.Items[0].Progress.Attention != 17 {
		t.Fatalf("安排汇总与分类不同：%+v/%v", plans, err)
	}
	if _, err := service.ListTasks(ctx, actor, TaskFilter{PlanID: plan.ID, Category: "closed", Cursor: attentionCursor}); err == nil {
		t.Fatal("游标跨分类仍被接受")
	}
	other := createVideoPlan(t, service, user, course)
	if _, err := service.ListTasks(ctx, actor, TaskFilter{PlanID: other.ID, Category: "attention", Cursor: attentionCursor}); err == nil {
		t.Fatal("游标跨安排仍被接受")
	}
	_, err = service.UpdatePlan(ctx, actor, plan.ID, plan.Version, PlanInput{Name: "更新后的安排", CourseIDs: []string{course}, Scope: PlanScopeInput{Kind: "all"}, TaskTypes: []string{"video"}, Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ListTasks(ctx, actor, TaskFilter{PlanID: plan.ID, Category: "attention", Cursor: attentionCursor}); err == nil {
		t.Fatal("旧安排版本游标仍被接受")
	}
}
