//go:build integration

package learning

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestActionableTasksExcludeClosedAndHistoricalBindings(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	platform := defaultFakePlatform()
	service, userID, _ := prepareTask(t, pool, platform)
	snapshot := platform.snapshots["101/202"]
	for index := 2; index <= 28; index++ {
		snapshot.Chapters[0].Attachments = append(snapshot.Chapters[0].Attachments, PlatformAttachment{ObjectID: fmt.Sprintf("media-%d", index), TaskPointID: fmt.Sprintf("%d", 9000+index), Kind: "video", Title: "异常任务", PassedKnown: true})
	}
	platform.snapshots["101/202"] = snapshot
	syncAll(t, service, userID)
	actor := Actor{ID: userID, Kind: "user"}
	courseID := courseIDForUser(t, pool, userID)
	ids := taskIDsForUser(t, pool, userID)
	plans, err := service.ListPlans(ctx, actor, PlanFilter{})
	if err != nil || len(plans.Items) != 1 {
		t.Fatal(err)
	}
	firstPlan := plans.Items[0]
	sharedPlan := createVideoPlan(t, service, userID, courseID)
	archivedPlan := createVideoPlan(t, service, userID, courseID)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE public.learning_tasks SET state='needs_attention',last_error_code='answer_unavailable' WHERE user_id=$1`, userID)
	exec(`UPDATE public.learning_plans SET status='archived' WHERE id=$1`, archivedPlan.ID)
	exec(`DELETE FROM public.learning_plan_task_bindings WHERE task_id=ANY($1::uuid[]) AND plan_id<>$2`, ids[4:], archivedPlan.ID)
	exec(`DELETE FROM public.learning_plan_task_bindings WHERE task_id=$1`, ids[4])
	exec(`UPDATE public.learning_tasks SET last_error_code='quiz_closed' WHERE id=$1`, ids[2])
	if err := service.withTx(ctx, func(tx pgx.Tx) error { return recordClosedTaskPoint(ctx, tx, ids[3], time.Now()) }); err != nil {
		t.Fatal(err)
	}
	check := func(expected int, filter TaskFilter) Page[TaskSummary] {
		t.Helper()
		filter.Actionable = true
		filter.Limit = 1
		page, err := service.ListTasks(ctx, actor, filter)
		if err != nil || page.TotalCount == nil || *page.TotalCount != expected {
			t.Fatalf("可处理总数不正确：%+v/%v，期望%d", page, err, expected)
		}
		if expected == 0 && len(page.Items) != 0 || expected > 0 && len(page.Items) != 1 {
			t.Fatalf("分页结果不正确：%+v", page)
		}
		return page
	}
	page := check(2, TaskFilter{})
	if page.NextCursor == "" {
		t.Fatal("分页游标缺失")
	}
	next := check(2, TaskFilter{Cursor: page.NextCursor})
	if len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
		t.Fatalf("游标改变了总数或重复任务：%+v", next)
	}
	check(0, TaskFilter{PlanID: archivedPlan.ID})
	check(0, TaskFilter{States: []string{StatePending}})
	exec(`UPDATE public.learning_courses SET platform_state='closed' WHERE id=$1`, courseID)
	check(0, TaskFilter{})
	exec(`UPDATE public.learning_courses SET platform_state='active' WHERE id=$1`, courseID)
	exec(`UPDATE public.learning_chapters SET availability='closed' WHERE course_id=$1`, courseID)
	check(0, TaskFilter{})
	exec(`UPDATE public.learning_chapters SET availability='open' WHERE course_id=$1`, courseID)
	check(2, TaskFilter{})
	// 其他用户拥有真实异常，也不能通过 user_id 查询参数越过当前用户范围。
	_, otherUser, _ := prepareTask(t, pool, defaultFakePlatform())
	exec(`UPDATE public.learning_tasks SET state='failed',last_error_code='permanent' WHERE user_id=$1`, otherUser)
	check(2, TaskFilter{UserID: otherUser})
	// 新版本排除视频后，历史绑定仍在，但不能继续作为当前安排异常。
	_, err = service.UpdatePlan(ctx, actor, sharedPlan.ID, sharedPlan.Version, PlanInput{Name: "只安排文档", CourseIDs: []string{courseID}, Scope: PlanScopeInput{Kind: "all"}, TaskTypes: []string{"document"}, Priority: 10})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE public.learning_plans SET status='archived' WHERE id=$1`, firstPlan.ID)
	check(0, TaskFilter{})
	var historical int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.learning_tasks WHERE user_id=$1 AND state='needs_attention'`, userID).Scan(&historical); err != nil || historical != 28 {
		t.Fatalf("历史任务被删除或改写：%d/%v", historical, err)
	}
}
