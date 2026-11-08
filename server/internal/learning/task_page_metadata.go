package learning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
)

const taskProgressTotalsSQL = `count(*),count(*) FILTER(WHERE t.state='confirmed'),
 count(*) FILTER(WHERE ` + taskCategorySQL + `='current'),
 count(*) FILTER(WHERE ` + taskCategorySQL + `='attention'),
 count(*) FILTER(WHERE ` + taskCategorySQL + `='closed'),
 count(*) FILTER(WHERE t.state='cancelled')`

func populateTaskPageMetadata(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, owner string, filter TaskFilter, conditions string, page *Page[TaskSummary]) error {
	meta := &TaskPageMetadata{Category: filter.Category}
	states := append([]string{}, filter.States...)
	query := `SELECT COALESCE((SELECT p.current_revision_id::text FROM public.learning_plans p
 WHERE p.id=NULLIF($4,'')::uuid AND ($1='' OR p.user_id=$1::uuid)),''),` + taskProgressTotalsSQL + `
 FROM public.learning_tasks t` + conditions
	if err := q.QueryRow(ctx, query, owner, states, filter.CourseID, filter.PlanID, filter.Actionable, filter.Category).Scan(&meta.PlanRevisionID,
		&meta.Progress.Total, &meta.Progress.Completed, &meta.Progress.Active, &meta.Progress.Attention, &meta.Progress.Closed, &meta.Progress.Cancelled); err != nil {
		return errServiceUnavailable
	}
	if filter.PlanID != "" && meta.PlanRevisionID == "" {
		return errNotFound
	}
	meta.Progress.Percent = planProgressPercent(meta.Progress)
	total := meta.Progress.Active
	switch filter.Category {
	case "attention":
		total = meta.Progress.Attention
	case "closed":
		total = meta.Progress.Closed
	case "history":
		total = meta.Progress.Completed + meta.Progress.Cancelled
	}
	page.TotalCount = &total
	page.TaskMeta = meta
	return nil
}

func taskCursorScope(owner string, filter TaskFilter, revision string) string {
	body, _ := json.Marshal([]any{owner, filter.PlanID, revision, filter.CourseID, filter.Category, filter.Actionable, filter.States})
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func splitTaskCursor(value, category string) (string, string, error) {
	if value == "" || category == "" {
		return value, "", nil
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[1]) != 64 {
		return "", "", validation("任务分页游标无效")
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", "", validation("任务分页游标无效")
	}
	return parts[0], parts[1], nil
}
