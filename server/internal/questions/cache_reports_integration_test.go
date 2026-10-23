//go:build integration

package questions

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type cacheReportPage struct {
	Items      []cacheReportItem `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

type cacheReportCreated struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// callCacheReportHandler 直接调用本任务的新处理器；路由注册由主线程在
// RegisterAdmin 中接入，测试不依赖或修改共享的 Register 实现。
func callCacheReportHandler(t *testing.T, handler func(http.ResponseWriter, *http.Request),
	method, target, reportID string, body any, actor web.Actor, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	request := httptest.NewRequest(method, target, reader)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if reportID != "" {
		request.SetPathValue("id", reportID)
	}
	request = request.WithContext(web.WithActor(request.Context(), actor))
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	return recorder
}

func cacheReportAdminReadOnly(env *testEnv) web.Actor {
	return web.Actor{ID: env.adminID, Kind: "admin", Permissions: []string{"question:read"}}
}

func cacheReportAdminManage(env *testEnv) web.Actor {
	return web.Actor{ID: env.adminID, Kind: "admin", Permissions: []string{"question:manage"}}
}

func createCacheReport(t *testing.T, env *testEnv, queryID, reason string) string {
	t.Helper()
	recorder := env.call(t, http.MethodPost, "/api/v1/question-cache-reports",
		map[string]any{"query_id": queryID, "reason": reason}, env.user(), nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("创建缓存报告失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var created cacheReportCreated
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" || created.Status != cacheReportPending {
		t.Fatalf("报告响应不符：%s", recorder.Body.String())
	}
	return created.ID
}

func listCacheReports(t *testing.T, env *testEnv, actor web.Actor, query string) cacheReportPage {
	t.Helper()
	recorder := callCacheReportHandler(t, env.service.adminListCacheReports, http.MethodGet,
		"/admin/api/v1/question-cache-reports"+query, "", nil, actor, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取缓存报告失败：%d %s", recorder.Code, recorder.Body.String())
	}
	var page cacheReportPage
	if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func reviewCacheReport(t *testing.T, env *testEnv, actor web.Actor, reportID, key, status, reason string) *httptest.ResponseRecorder {
	t.Helper()
	return callCacheReportHandler(t, env.service.adminReviewCacheReport, http.MethodPost,
		"/admin/api/v1/question-cache-reports/"+reportID+"/review", reportID,
		map[string]any{"status": status, "reason": reason}, actor,
		map[string]string{"Idempotency-Key": key})
}

func requireCacheReportReviewOK(t *testing.T, recorder *httptest.ResponseRecorder, reportID, status string) cacheReportReviewResult {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("复核应成功：%d %s", recorder.Code, recorder.Body.String())
	}
	var result cacheReportReviewResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != reportID || result.Status != status || result.ReviewID == "" || result.ResolvedAt.IsZero() {
		t.Fatalf("复核响应不符：%s", recorder.Body.String())
	}
	return result
}

func cacheReportStatusRow(t *testing.T, env *testEnv, reportID string) (string, *time.Time) {
	t.Helper()
	var status string
	var resolvedAt *time.Time
	if err := env.pool.QueryRow(context.Background(), `SELECT status, resolved_at
		FROM public.question_cache_reports WHERE id = $1::uuid`, reportID).Scan(&status, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	return status, resolvedAt
}

func cacheReportReviewRows(t *testing.T, env *testEnv, reportID string) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*)
		FROM public.question_cache_report_reviews WHERE report_id = $1::uuid`, reportID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func cacheReportAuditCount(t *testing.T, env *testEnv, reportID string) int {
	t.Helper()
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*)
		FROM public.audit_events WHERE action = 'question.cache_report.review'
		AND resource_type = 'question_cache_report' AND resource_id = $1::uuid`, reportID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAdminCacheReportListFiltersAndPaginates(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "缓存报告模型", "internal-report", nil, true)
	query := mustAnswer(t, env, "admin-report-query-0001", queryBody("敏感题干不会出现在管理列表", "cache_first"))
	reasons := []string{"第一次报告原因", "第二次报告原因", "第三次报告原因"}
	ids := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		ids = append(ids, createCacheReport(t, env, query.ID, reason))
	}

	first := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?limit=2")
	if len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("第一页分页不符：%+v", first)
	}
	for _, item := range first.Items {
		if item.Status != cacheReportPending || item.ResolvedAt != nil {
			t.Fatalf("默认列表不是 pending：%+v", item)
		}
		if item.Query.ID != query.ID || item.Query.Model.OfferingID == "" {
			t.Fatalf("查询/模型引用缺失：%+v", item)
		}
		if item.CacheEntryID != nil {
			t.Fatalf("非缓存报告的缓存引用应为空：%+v", item)
		}
	}
	second := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?limit=2&cursor="+*first.NextCursor)
	if len(second.Items) != 1 || second.NextCursor != nil {
		t.Fatalf("第二页分页不符：%+v", second)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		seen[item.ID] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Fatalf("分页遗漏报告：%s", id)
		}
	}

	recorder := callCacheReportHandler(t, env.service.adminListCacheReports, http.MethodGet,
		"/admin/api/v1/question-cache-reports", "", nil, cacheReportAdminReadOnly(env), nil)
	body := recorder.Body.String()
	if strings.Contains(body, query.Question.Title) || strings.Contains(body, `"answer"`) ||
		strings.Contains(body, `"usage"`) {
		t.Fatalf("列表返回了题干、答案或用量：%s", body)
	}

	reviewed := requireCacheReportReviewOK(t, reviewCacheReport(t, env, cacheReportAdminManage(env), ids[0],
		"admin-report-review-key-01", cacheReportResolved, "已核对并处理"), ids[0], cacheReportResolved)
	if reviewed.ReviewID == "" {
		t.Fatal("复核缺少编号")
	}
	if page := listCacheReports(t, env, cacheReportAdminReadOnly(env), ""); len(page.Items) != 2 {
		t.Fatalf("默认 pending 列表应排除已处理报告：%+v", page)
	}
	if page := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?status=resolved"); len(page.Items) != 1 ||
		page.Items[0].ID != ids[0] || page.Items[0].ResolvedAt == nil {
		t.Fatalf("resolved 筛选不符：%+v", page)
	}
	if page := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?status=rejected"); len(page.Items) != 0 {
		t.Fatalf("rejected 筛选应为空：%+v", page)
	}
	if page := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?status=pending"); len(page.Items) != 2 {
		t.Fatalf("pending 筛选不符：%+v", page)
	}

	for _, target := range []string{"?status=unknown", "?limit=0", "?limit=101", "?cursor=not-a-cursor"} {
		recorder := callCacheReportHandler(t, env.service.adminListCacheReports, http.MethodGet,
			"/admin/api/v1/question-cache-reports"+target, "", nil, cacheReportAdminReadOnly(env), nil)
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s 应返回 422：%d %s", target, recorder.Code, recorder.Body.String())
		}
	}
}

func TestAdminCacheReportReviewIdempotencyAndTerminal(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "缓存报告模型", "internal-report", nil, true)
	query := mustAnswer(t, env, "admin-report-query-0002", queryBody("复核幂等题", "cache_first"))
	firstID := createCacheReport(t, env, query.ID, "答案与教材不一致")
	secondID := createCacheReport(t, env, query.ID, "第二条报告")
	thirdID := createCacheReport(t, env, query.ID, "第三条报告")
	manage := cacheReportAdminManage(env)
	key := "admin-report-review-key-02"
	reason := "已核对答案并处理"

	created := requireCacheReportReviewOK(t, reviewCacheReport(t, env, manage, firstID, key,
		cacheReportResolved, reason), firstID, cacheReportResolved)
	status, resolvedAt := cacheReportStatusRow(t, env, firstID)
	if status != cacheReportResolved || resolvedAt == nil || !resolvedAt.Equal(created.ResolvedAt) {
		t.Fatalf("报告终态不符：%s/%v（响应 %v）", status, resolvedAt, created.ResolvedAt)
	}
	if cacheReportReviewRows(t, env, firstID) != 1 || cacheReportAuditCount(t, env, firstID) != 1 {
		t.Fatal("首次复核应只写一条复核事实和审计")
	}

	replay := requireCacheReportReviewOK(t, reviewCacheReport(t, env, manage, firstID, key,
		cacheReportResolved, reason), firstID, cacheReportResolved)
	if replay.ReviewID != created.ReviewID || !replay.ResolvedAt.Equal(created.ResolvedAt) {
		t.Fatalf("幂等重放未返回原复核：%+v", replay)
	}
	if cacheReportReviewRows(t, env, firstID) != 1 || cacheReportAuditCount(t, env, firstID) != 1 {
		t.Fatal("幂等重放重复写了复核事实或审计")
	}

	for _, conflict := range []struct {
		name, reportID, status, reason string
	}{
		{"同键不同理由", firstID, cacheReportResolved, "另一个复核理由"},
		{"同键不同状态", firstID, cacheReportRejected, reason},
		{"同键不同报告", secondID, cacheReportResolved, reason},
	} {
		recorder := reviewCacheReport(t, env, manage, conflict.reportID, key, conflict.status, conflict.reason)
		if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), "idempotency_conflict") {
			t.Fatalf("%s 应返回幂等冲突：%d %s", conflict.name, recorder.Code, recorder.Body.String())
		}
	}
	if status, _ := cacheReportStatusRow(t, env, secondID); status != cacheReportPending {
		t.Fatalf("幂等冲突改写了其他报告：%s", status)
	}

	// 终态报告使用新键也不能再次复核或改写。
	terminal := reviewCacheReport(t, env, manage, firstID, "admin-report-review-key-03",
		cacheReportRejected, "终态后尝试改写")
	if terminal.Code != http.StatusConflict || !strings.Contains(terminal.Body.String(), `"code":"conflict"`) {
		t.Fatalf("终态复核应冲突：%d %s", terminal.Code, terminal.Body.String())
	}
	var storedStatus, storedReason string
	if err := env.pool.QueryRow(context.Background(), `SELECT status, reason
		FROM public.question_cache_report_reviews WHERE report_id = $1::uuid`, firstID).
		Scan(&storedStatus, &storedReason); err != nil {
		t.Fatal(err)
	}
	if storedStatus != cacheReportResolved || storedReason != reason {
		t.Fatalf("终态复核被改写：%s/%s", storedStatus, storedReason)
	}

	// 非缓存报告同样可以结清；rejected 也记录终态时间。
	requireCacheReportReviewOK(t, reviewCacheReport(t, env, manage, secondID, "admin-report-review-key-04",
		cacheReportRejected, "证据不足驳回"), secondID, cacheReportRejected)
	if status, resolvedAt := cacheReportStatusRow(t, env, secondID); status != cacheReportRejected || resolvedAt == nil {
		t.Fatalf("rejected 报告未结清：%s/%v", status, resolvedAt)
	}
	if page := listCacheReports(t, env, cacheReportAdminReadOnly(env), "?status=rejected"); len(page.Items) != 1 ||
		page.Items[0].CacheEntryID != nil {
		t.Fatalf("非缓存报告 rejected 列表不符：%+v", page)
	}

	// 幂等键按管理员隔离：另一个管理员可使用相同键处理自己的待处理报告。
	otherAdminID := addAdmin(t, env.pool)
	other := web.Actor{ID: otherAdminID, Kind: "admin", Permissions: []string{"question:manage"}}
	requireCacheReportReviewOK(t, reviewCacheReport(t, env, other, thirdID, key,
		cacheReportResolved, reason), thirdID, cacheReportResolved)
	if cacheReportAuditCount(t, env, thirdID) != 1 {
		t.Fatal("第二管理员的复核审计缺失")
	}
}

func TestAdminCacheReportReviewPermissionsAndValidation(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "缓存报告模型", "internal-report", nil, true)
	query := mustAnswer(t, env, "admin-report-query-0003", queryBody("复核校验题", "cache_first"))
	pendingID := createCacheReport(t, env, query.ID, "用于校验的报告")
	secondID := createCacheReport(t, env, query.ID, "用于边界长度的报告")

	readOnly := cacheReportAdminReadOnly(env)
	manage := cacheReportAdminManage(env)
	if recorder := callCacheReportHandler(t, env.service.adminListCacheReports, http.MethodGet,
		"/admin/api/v1/question-cache-reports", "", nil,
		web.Actor{ID: env.adminID, Kind: "admin", Permissions: []string{}}, nil); recorder.Code != http.StatusForbidden {
		t.Fatalf("无读取权限应 403：%d", recorder.Code)
	}
	if recorder := reviewCacheReport(t, env, readOnly, pendingID, "admin-report-review-key-05",
		cacheReportResolved, "只有读取权限"); recorder.Code != http.StatusForbidden {
		t.Fatalf("只有读取权限不能复核：%d %s", recorder.Code, recorder.Body.String())
	}
	if status, _ := cacheReportStatusRow(t, env, pendingID); status != cacheReportPending {
		t.Fatalf("无权限请求改写了报告：%s", status)
	}

	missingKey := callCacheReportHandler(t, env.service.adminReviewCacheReport, http.MethodPost,
		"/admin/api/v1/question-cache-reports/"+pendingID+"/review", pendingID,
		map[string]any{"status": cacheReportResolved, "reason": "缺少幂等键"}, manage, nil)
	if missingKey.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺少幂等键应 422：%d", missingKey.Code)
	}
	for _, key := range []string{"short", strings.Repeat("k", 129)} {
		recorder := reviewCacheReport(t, env, manage, pendingID, key, cacheReportResolved, "幂等键长度校验")
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("幂等键长度 %d 应 422：%d", len(key), recorder.Code)
		}
	}
	for _, body := range []map[string]any{
		{"status": "unknown", "reason": "状态无效的请求"},
		{"status": cacheReportResolved, "reason": "四个字"},
		{"status": cacheReportResolved, "reason": strings.Repeat("理", 501)},
	} {
		recorder := callCacheReportHandler(t, env.service.adminReviewCacheReport, http.MethodPost,
			"/admin/api/v1/question-cache-reports/"+pendingID+"/review", pendingID, body, manage,
			map[string]string{"Idempotency-Key": "admin-report-review-key-06"})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Fatalf("非法复核输入应 422：%d %s", recorder.Code, recorder.Body.String())
		}
	}
	nonJSON := callCacheReportHandler(t, env.service.adminReviewCacheReport, http.MethodPost,
		"/admin/api/v1/question-cache-reports/"+pendingID+"/review", pendingID,
		map[string]any{"status": cacheReportResolved, "reason": "非 JSON 请求"}, manage,
		map[string]string{"Idempotency-Key": "admin-report-review-key-07", "Content-Type": "text/plain"})
	if nonJSON.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("非 JSON 应 415：%d", nonJSON.Code)
	}
	for _, reportID := range []string{web.NewID(), "not-a-uuid"} {
		recorder := reviewCacheReport(t, env, manage, reportID, "admin-report-review-key-08",
			cacheReportResolved, "报告不存在校验")
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("未知报告 %s 应 404：%d", reportID, recorder.Code)
		}
	}

	requireCacheReportReviewOK(t, reviewCacheReport(t, env, manage, pendingID, "admin-report-review-key-09",
		cacheReportResolved, "核对后通过"), pendingID, cacheReportResolved)
	requireCacheReportReviewOK(t, reviewCacheReport(t, env, manage, secondID, "admin-report-review-key-10",
		cacheReportResolved, strings.Repeat("核", 500)), secondID, cacheReportResolved)
}

func TestAdminCacheReportCacheReferenceAndMinimalPayload(t *testing.T) {
	entryID := "entry-admin-report-cache"
	created := time.Now().UTC()
	zero := int64(0)
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{RequestID: request.RequestID, Status: "cache_hit", Answer: "不应展示的答案",
			Model: ForkModel{OfferingID: request.ModelOfferingID, DisplayName: "缓存模型"},
			Mode:  request.Mode,
			Cache: ForkCache{Hit: true, EntryID: &entryID, CreatedAt: &created},
			Usage: Usage{InputTokens: &zero, OutputTokens: &zero, TotalTokens: &zero,
				Complete: true, Calls: []Call{}}}, nil
	}}
	env := newTestEnv(t, envOptions{fork: fork})
	env.createOffering(t, "缓存引用模型", "internal-cache-report", nil, true)
	query := mustAnswer(t, env, "admin-report-query-0004", queryBody("缓存引用敏感题干", "cache_first"))
	reportID := createCacheReport(t, env, query.ID, "缓存答案疑似错误")

	page := listCacheReports(t, env, cacheReportAdminReadOnly(env), "")
	if len(page.Items) != 1 || page.Items[0].ID != reportID {
		t.Fatalf("缓存引用列表不符：%+v", page)
	}
	item := page.Items[0]
	if item.CacheEntryID == nil || *item.CacheEntryID != entryID {
		t.Fatalf("缓存条目引用缺失：%+v", item)
	}
	if item.Query.Model.DisplayName != "缓存引用模型" {
		t.Fatalf("模型引用不符：%+v", item.Query.Model)
	}
	recorder := callCacheReportHandler(t, env.service.adminListCacheReports, http.MethodGet,
		"/admin/api/v1/question-cache-reports", "", nil, cacheReportAdminReadOnly(env), nil)
	body := recorder.Body.String()
	if strings.Contains(body, "缓存引用敏感题干") || strings.Contains(body, "不应展示的答案") ||
		strings.Contains(body, `"usage"`) || strings.Contains(body, `"answer"`) {
		t.Fatalf("列表泄露题干/答案/用量：%s", body)
	}
}

type cacheReportParallelResult struct {
	code     int
	reviewID string
	body     string
}

func parallelCacheReportReviews(t *testing.T, env *testEnv, actor web.Actor,
	requests []struct{ reportID, key, reason string }) []cacheReportParallelResult {
	t.Helper()
	results := make(chan cacheReportParallelResult, len(requests))
	var wait sync.WaitGroup
	for _, request := range requests {
		request := request
		wait.Add(1)
		go func() {
			defer wait.Done()
			recorder := reviewCacheReport(t, env, actor, request.reportID, request.key,
				cacheReportResolved, request.reason)
			var parsed cacheReportReviewResult
			_ = json.Unmarshal(recorder.Body.Bytes(), &parsed)
			results <- cacheReportParallelResult{code: recorder.Code, reviewID: parsed.ReviewID,
				body: recorder.Body.String()}
		}()
	}
	wait.Wait()
	close(results)
	collected := make([]cacheReportParallelResult, 0, len(requests))
	for result := range results {
		collected = append(collected, result)
	}
	return collected
}

func TestAdminCacheReportReviewConcurrentIdempotency(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "缓存报告模型", "internal-report", nil, true)
	query := mustAnswer(t, env, "admin-report-query-0005", queryBody("并发复核题", "cache_first"))
	manage := cacheReportAdminManage(env)

	// 同键同 payload：两个请求都应返回同一份复核结果，只写一次事实和审计。
	sameID := createCacheReport(t, env, query.ID, "并发同 payload 报告")
	key := "admin-report-concurrent-key-01"
	same := parallelCacheReportReviews(t, env, manage, []struct{ reportID, key, reason string }{
		{sameID, key, "并发相同幂等请求"},
		{sameID, key, "并发相同幂等请求"},
	})
	var reviewID string
	for _, result := range same {
		if result.code != http.StatusOK || result.reviewID == "" {
			t.Fatalf("同键同 payload 并发应成功：%+v", result)
		}
		if reviewID == "" {
			reviewID = result.reviewID
		} else if reviewID != result.reviewID {
			t.Fatalf("同键同 payload 返回了不同复核编号：%s/%s", reviewID, result.reviewID)
		}
	}
	if cacheReportReviewRows(t, env, sameID) != 1 || cacheReportAuditCount(t, env, sameID) != 1 {
		t.Fatal("同键同 payload 并发重复写事实或审计")
	}

	// 同键不同理由：只能有一方成功，另一方必须幂等冲突且不改写胜出事实。
	differentReasonID := createCacheReport(t, env, query.ID, "并发不同理由报告")
	differentReasonKey := "admin-report-concurrent-key-02"
	different := parallelCacheReportReviews(t, env, manage, []struct{ reportID, key, reason string }{
		{differentReasonID, differentReasonKey, "并发理由甲甲甲"},
		{differentReasonID, differentReasonKey, "并发理由乙乙乙"},
	})
	success, conflict := 0, 0
	for _, result := range different {
		switch result.code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			if !strings.Contains(result.body, "idempotency_conflict") {
				t.Fatalf("同键异 payload 应返回幂等冲突：%s", result.body)
			}
			conflict++
		default:
			t.Fatalf("同键异 payload 出现意外状态：%+v", result)
		}
	}
	if success != 1 || conflict != 1 || cacheReportReviewRows(t, env, differentReasonID) != 1 ||
		cacheReportAuditCount(t, env, differentReasonID) != 1 {
		t.Fatalf("同键异 payload 并发结果不符：成功 %d 冲突 %d", success, conflict)
	}

	// 同键不同报告并发：键只能绑定一个报告，另一个保持待处理。
	crossA := createCacheReport(t, env, query.ID, "并发跨报告甲")
	crossB := createCacheReport(t, env, query.ID, "并发跨报告乙")
	crossKey := "admin-report-concurrent-key-05"
	cross := parallelCacheReportReviews(t, env, manage, []struct{ reportID, key, reason string }{
		{crossA, crossKey, "并发跨报告相同理由"},
		{crossB, crossKey, "并发跨报告相同理由"},
	})
	success, conflict = 0, 0
	for _, result := range cross {
		switch result.code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			if !strings.Contains(result.body, "idempotency_conflict") {
				t.Fatalf("同键不同报告应返回幂等冲突：%s", result.body)
			}
			conflict++
		default:
			t.Fatalf("同键不同报告出现意外状态：%+v", result)
		}
	}
	statusA, _ := cacheReportStatusRow(t, env, crossA)
	statusB, _ := cacheReportStatusRow(t, env, crossB)
	if success != 1 || conflict != 1 ||
		(statusA == cacheReportPending) == (statusB == cacheReportPending) ||
		cacheReportReviewRows(t, env, crossA)+cacheReportReviewRows(t, env, crossB) != 1 {
		t.Fatalf("同键不同报告并发结果不符：成功 %d 冲突 %d 状态 %s/%s", success, conflict, statusA, statusB)
	}

	// 不同键同一报告：只有一个终态复核，另一个必须冲突且不改写。
	differentKeyID := createCacheReport(t, env, query.ID, "并发不同键报告")
	differentKeys := parallelCacheReportReviews(t, env, manage, []struct{ reportID, key, reason string }{
		{differentKeyID, "admin-report-concurrent-key-03", "并发不同键甲"},
		{differentKeyID, "admin-report-concurrent-key-04", "并发不同键乙"},
	})
	success, conflict = 0, 0
	for _, result := range differentKeys {
		switch result.code {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("不同键同一报告出现意外状态：%+v", result)
		}
	}
	if success != 1 || conflict != 1 || cacheReportReviewRows(t, env, differentKeyID) != 1 ||
		cacheReportAuditCount(t, env, differentKeyID) != 1 {
		t.Fatalf("不同键同一报告并发结果不符：成功 %d 冲突 %d", success, conflict)
	}
}
