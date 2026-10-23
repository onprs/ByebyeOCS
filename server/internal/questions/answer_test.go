//go:build integration

package questions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func answeredForkResponse(request ForkAnswerRequest) ForkAnswerResponse {
	input, output, total := int64(100), int64(10), int64(110)
	return ForkAnswerResponse{
		RequestID: request.RequestID, Status: "answered", Question: request.Question.Title, Answer: "A",
		Model: ForkModel{OfferingID: request.ModelOfferingID, DisplayName: "标准", Capabilities: []string{"text"}},
		Mode:  request.Mode, Cache: ForkCache{Hit: false},
		Usage: Usage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Complete: true,
			Calls: []Call{{Slot: "answer", InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Status: "ok"}}},
		LatencyMS: 10,
	}
}

func mustAnswer(t *testing.T, env *testEnv, key string, input QueryInput) QueryView {
	t.Helper()
	view, err := env.service.Answer(context.Background(), env.userID, key, input)
	if err != nil {
		t.Fatalf("Answer 失败：%v", err)
	}
	return view
}

func requireQueryError(t *testing.T, err error, code string, status int) *QueryError {
	t.Helper()
	var queryErr *QueryError
	if !errors.As(err, &queryErr) {
		t.Fatalf("期望可分类的 QueryError，实际 %T：%v", err, err)
	}
	if queryErr.Code != code || queryErr.Status != status {
		t.Fatalf("错误分类不符：%s/%d（期望 %s/%d）", queryErr.Code, queryErr.Status, code, status)
	}
	return queryErr
}

func TestAnswerDefaultAndSpecifiedModel(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	defaultOffering := env.createOffering(t, "默认模型", "internal-default", nil, true)
	other := env.createOffering(t, "指定模型", "internal-other", nil, false)

	first := mustAnswer(t, env, "answer-default-0001", queryBody("默认模型题", "cache_first"))
	if first.Model.OfferingID != defaultOffering.ID || first.Model.DisplayName != "默认模型" {
		t.Fatalf("未选择默认模型：%#v", first.Model)
	}

	input := queryBody("指定模型题", "cache_first")
	input.ModelOfferingID = other.ID
	second := mustAnswer(t, env, "answer-specified-0001", input)
	if second.Model.OfferingID != other.ID || second.Model.DisplayName != "指定模型" {
		t.Fatalf("未使用指定模型：%#v", second.Model)
	}
	if env.fork.callCount() != 2 {
		t.Fatalf("两个不同查询应各调用一次 fork，实际 %d", env.fork.callCount())
	}
}

func TestAnswerCacheHitZeroTokensNoCharge(t *testing.T) {
	entryID := "entry-direct"
	created := time.Now().UTC()
	zero := int64(0)
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{RequestID: request.RequestID, Status: "cache_hit", Answer: "A",
			Model: ForkModel{OfferingID: request.ModelOfferingID}, Mode: request.Mode,
			Cache: ForkCache{Hit: true, EntryID: &entryID, CreatedAt: &created},
			Usage: Usage{InputTokens: &zero, OutputTokens: &zero, TotalTokens: &zero, Complete: true, Calls: []Call{}}}, nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "缓存模型", "internal-cache", testPricing(10_000_000, 20_000_000, 10000), true)

	view := mustAnswer(t, env, "answer-cache-0001", queryBody("缓存题", "cache_first"))
	if view.Status != "cache_hit" || !view.Cache.Hit || view.Cache.EntryID == nil || view.Cache.CreatedAt == nil {
		t.Fatalf("缓存命中事实不完整：%#v", view)
	}
	if view.Usage.TotalTokens == nil || *view.Usage.TotalTokens != 0 || len(view.Usage.Calls) != 0 {
		t.Fatalf("缓存命中必须零 token 空 calls：%#v", view.Usage)
	}
	if view.Pricing.State != "cache_hit" || view.Pricing.Charged {
		t.Fatalf("缓存命中不应扣费：%#v", view.Pricing)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("缓存命中应预占后全额释放：%d/%d/%d", reserves, settles, releases)
	}
}

func TestAnswerReplayChargesOnce(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "幂等模型", "internal-idem", testPricing(10_000_000, 20_000_000, 10000), true)

	input := queryBody("幂等题", "cache_first")
	first := mustAnswer(t, env, "answer-replay-0001", input)
	second := mustAnswer(t, env, "answer-replay-0001", input)
	if first.ID != second.ID {
		t.Fatalf("同键同题应返回同一查询：%s/%s", first.ID, second.ID)
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("幂等重放不应重复调用 fork，实际 %d", env.fork.callCount())
	}
	reserves, settles, releases, reserved, settled := env.billing.counts()
	if reserves != 1 || settles != 1 || releases != 1 || settled <= 0 || reserved <= settled {
		t.Fatalf("同键同题应只预占/结算一次：%d/%d/%d/%d/%d", reserves, settles, releases, reserved, settled)
	}
	var count int
	var charged bool
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*), bool_or((pricing->>'charged')::boolean)
		FROM public.question_queries WHERE user_id = $1 AND idempotency_key = 'answer-replay-0001'`,
		env.userID).Scan(&count, &charged); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !charged {
		t.Fatalf("数据库查询事实不符：count=%d charged=%v", count, charged)
	}
}

func TestAnswerSameKeyDifferentQuestionConflicts(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "冲突模型", "internal-conflict", testPricing(10_000_000, 20_000_000, 10000), true)

	first := mustAnswer(t, env, "answer-conflict-0001", queryBody("原题", "cache_first"))
	_, err := env.service.Answer(context.Background(), env.userID, "answer-conflict-0001",
		queryBody("不同题", "cache_first"))
	requireQueryError(t, err, "idempotency_conflict", 409)

	if env.fork.callCount() != 1 {
		t.Fatalf("冲突请求不得调用 fork，实际 %d", env.fork.callCount())
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 1 || releases != 1 {
		t.Fatalf("冲突请求不得再次计费：%d/%d/%d", reserves, settles, releases)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.question_queries
		WHERE id = $1`, first.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("查询行数不符：%d/%v", count, err)
	}
}

func TestAnswerConcurrentSameKeyChargesOnce(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "并发模型", "internal-concurrent", testPricing(10_000_000, 20_000_000, 10000), true)

	const workers = 6
	type outcome struct {
		view QueryView
		err  error
	}
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, err := env.service.Answer(context.Background(), env.userID, "answer-concurrent-0001",
				queryBody("并发题", "cache_first"))
			results <- outcome{view: view, err: err}
		}()
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("并发请求未进入模型调用")
	}
	close(release)
	wg.Wait()
	close(results)

	var firstID string
	for result := range results {
		if result.err != nil {
			t.Fatalf("并发同键请求失败：%v", result.err)
		}
		if firstID == "" {
			firstID = result.view.ID
		}
		if result.view.ID != firstID {
			t.Fatalf("并发同键请求返回了不同查询：%s/%s", firstID, result.view.ID)
		}
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("并发同键只应调用一次 fork，实际 %d", env.fork.callCount())
	}
	reserves, settles, releases, _, _ := env.billing.counts()
	if reserves != 1 || settles != 1 || releases != 1 {
		t.Fatalf("并发同键不得双扣：%d/%d/%d", reserves, settles, releases)
	}
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'answer-concurrent-0001'`, env.userID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("并发同键应只有一条查询：%d/%v", count, err)
	}
}

func TestAnswerIncompleteUsageReleasesHold(t *testing.T) {
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{RequestID: request.RequestID, Status: "answered", Answer: "A",
			Model: ForkModel{OfferingID: request.ModelOfferingID}, Mode: request.Mode,
			Usage: Usage{Complete: false, Calls: []Call{{Slot: "answer", Status: "ok"}}}}, nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "用量模型", "internal-usage", testPricing(10_000_000, 20_000_000, 10000), true)

	view := mustAnswer(t, env, "answer-usage-0001", queryBody("用量缺失题", "cache_first"))
	if view.Status != "needs_review" || view.Pricing.State != "usage_incomplete" || view.Pricing.Charged {
		t.Fatalf("usage_incomplete 必须待复核且不扣费：%#v", view)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("用量不完整应预占后释放：%d/%d/%d", reserves, settles, releases)
	}

	replay := mustAnswer(t, env, "answer-usage-0001", queryBody("用量缺失题", "cache_first"))
	if replay.ID != view.ID || env.fork.callCount() != 1 {
		t.Fatal("待复核查询重放不应重复调用")
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("待复核查询重放不应再次触达账本：%d/%d/%d", reserves, settles, releases)
	}
}

func TestAnswerUpstreamUnknownReleasesHoldWithoutLeakingMessage(t *testing.T) {
	fork := &fakeFork{answer: func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{}, &ForkError{Code: "upstream_timeout", Status: 504,
			Message: "上游原始响应 secret-sk-123：连接重置"}
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "未知模型", "internal-unknown", testPricing(10_000_000, 20_000_000, 10000), true)

	_, err := env.service.Answer(context.Background(), env.userID, "answer-unknown-0001",
		queryBody("未知题", "cache_first"))
	queryErr := requireQueryError(t, err, "upstream_timeout", 504)
	if strings.Contains(queryErr.Message, "secret") || strings.Contains(queryErr.Message, "原始") {
		t.Fatalf("错误不得泄露上游原始响应：%s", queryErr.Message)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("未知结果应预占后释放、不结算：%d/%d/%d", reserves, settles, releases)
	}

	replay, err := env.service.Answer(context.Background(), env.userID, "answer-unknown-0001",
		queryBody("未知题", "cache_first"))
	if err != nil {
		t.Fatalf("待复核结果重放应返回已保存事实：%v", err)
	}
	if replay.Status != "needs_review" || replay.ReviewReason != "upstream_timeout" {
		t.Fatalf("待复核结果重放事实不符：%#v", replay)
	}
	if env.fork.callCount() != 1 {
		t.Fatal("未知结果重放不得再次调用 fork")
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("未知结果重放不得再次触达账本：%d/%d/%d", reserves, settles, releases)
	}
}

func TestAnswerValidatesUserKeyAndInputBeforeReplay(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	env.createOffering(t, "校验模型", "internal-validation", testPricing(10_000_000, 20_000_000, 10000), true)
	valid := queryBody("有效题", "cache_first")

	_, err := env.service.Answer(context.Background(), "not-a-user-id", "answer-validation-0001", valid)
	requireQueryError(t, err, "invalid_user", 400)
	_, err = env.service.Answer(context.Background(), env.userID, "  ", valid)
	requireQueryError(t, err, "idempotency_required", 400)

	first := mustAnswer(t, env, "answer-validation-0001", valid)
	_, err = env.service.Answer(context.Background(), env.userID, "answer-validation-0001",
		queryBody("", "cache_first"))
	requireQueryError(t, err, "validation_failed", 422)

	if env.fork.callCount() != 1 {
		t.Fatalf("校验失败不得调用 fork，实际 %d", env.fork.callCount())
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatalf("校验失败不得触达账本：%d/%d/%d", reserves, settles, releases)
	}
	detail, err := env.service.Answer(context.Background(), env.userID, "answer-validation-0002", valid)
	if err != nil || detail.ID == first.ID {
		t.Fatalf("新幂等键应创建独立查询：%v/%#v", err, detail)
	}
}

// attachedLedger 在题库服务上挂真实账本，验证 Answer 的预占/结算确实作用于钱包。
func attachedLedger(t *testing.T, env *testEnv) *ledger.Service {
	t.Helper()
	book, err := ledger.New(env.pool, ledger.Options{})
	if err != nil {
		t.Fatal(err)
	}
	env.service.billing = book
	return book
}

func fundWallet(t *testing.T, book *ledger.Service, env *testEnv, amount string) {
	t.Helper()
	ctx := context.Background()
	wallet, err := book.GetWallet(ctx, env.userID)
	if err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: env.adminID, Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
	if _, err := book.AdjustWallet(ctx, actor, "question-auto-fund", ledger.WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: amount, Reason: "题库自动测试资金",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerRealLedgerChargesConcurrentReplayOnce(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		startOnce.Do(func() { close(started) })
		<-release
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, fork: fork})
	book := attachedLedger(t, env)
	fundWallet(t, book, env, "1000")
	env.createOffering(t, "真实账本模型", "internal-ledger", testPricing(10_000_000, 20_000_000, 10000), true)

	const workers = 4
	type outcome struct {
		view QueryView
		err  error
	}
	results := make(chan outcome, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, err := env.service.Answer(context.Background(), env.userID, "answer-real-ledger-0001",
				queryBody("真实账本题", "cache_first"))
			results <- outcome{view: view, err: err}
		}()
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("真实账本并发请求未进入模型调用")
	}
	close(release)
	wg.Wait()
	close(results)
	var firstID string
	for result := range results {
		if result.err != nil {
			t.Fatalf("真实账本并发请求失败：%v", result.err)
		}
		if firstID == "" {
			firstID = result.view.ID
		}
		if result.view.ID != firstID {
			t.Fatalf("真实账本并发返回不同查询：%s/%s", firstID, result.view.ID)
		}
	}
	wallet, err := book.GetWallet(context.Background(), env.userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" {
		t.Fatalf("并发幂等应只扣 1 分且无残留预占：可用 %s 预占 %s", wallet.AvailableMinor, wallet.ReservedMinor)
	}
	replay := mustAnswer(t, env, "answer-real-ledger-0001", queryBody("真实账本题", "cache_first"))
	if replay.ID != firstID {
		t.Fatalf("真实账本重放返回不同查询：%s/%s", firstID, replay.ID)
	}
	wallet, err = book.GetWallet(context.Background(), env.userID)
	if err != nil || wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" {
		t.Fatalf("重放不应再次扣费：%s/%s/%v", wallet.AvailableMinor, wallet.ReservedMinor, err)
	}
}

// idempotentFork 模拟 fork 的 request_id 幂等：重复 HTTP 调用不增加模型调用数。
type idempotentFork struct {
	mu         sync.Mutex
	responses  map[string]ForkAnswerResponse
	requests   []ForkAnswerRequest
	httpCalls  int
	modelCalls int
	gate       chan struct{}
	started    chan struct{}
	startOnce  sync.Once
}

func newIdempotentFork() *idempotentFork {
	return &idempotentFork{responses: make(map[string]ForkAnswerResponse)}
}

func (f *idempotentFork) Answer(_ context.Context, request ForkAnswerRequest, _ string) (ForkAnswerResponse, error) {
	f.mu.Lock()
	f.httpCalls++
	f.requests = append(f.requests, request)
	response, ok := f.responses[request.RequestID]
	if !ok {
		f.modelCalls++
		response = answeredForkResponse(request)
		f.responses[request.RequestID] = response
	}
	gate, started := f.gate, f.started
	f.mu.Unlock()
	if gate != nil {
		if started != nil {
			f.startOnce.Do(func() { close(started) })
		}
		<-gate
	}
	return response, nil
}

func (*idempotentFork) TestModel(context.Context, string, string, bool) (ForkTestResult, error) {
	return ForkTestResult{}, nil
}

func (*idempotentFork) InvalidateCache(context.Context, string, string) (bool, error) {
	return true, nil
}

func (f *idempotentFork) counts() (httpCalls, modelCalls int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.httpCalls, f.modelCalls
}

func (f *idempotentFork) lastRequest() ForkAnswerRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func (f *idempotentFork) blockNextCalls() (started chan struct{}, release func()) {
	f.mu.Lock()
	f.gate = make(chan struct{})
	f.started = make(chan struct{})
	gate, started := f.gate, f.started
	f.mu.Unlock()
	return started, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.gate == gate {
			close(gate)
			f.gate = nil
		}
	}
}

func buildRecoveryRequest(queryID, scopeKey string, offering *Offering, input QueryInput) ForkAnswerRequest {
	return ForkAnswerRequest{
		RequestID: queryID, ModelOfferingID: offering.ProviderRef, Mode: input.Mode,
		CacheScope: input.CacheScope, CacheScopeKey: scopeKey,
		Question: ForkQuestion{Title: input.Question.Title, Options: input.Question.Options, Type: input.Question.Type, Images: input.Question.Images},
		Budget:   ForkBudget{MaxOutputTokens: input.Budget.MaxOutputTokens},
	}
}

// seedStalePending 直接构造“fork 已返回但主平台未 finalize”的陈旧 pending。
func seedStalePending(t *testing.T, env *testEnv, offeringView adminOfferingView, input *QueryInput, key string) (*Offering, string, string) {
	t.Helper()
	ctx := context.Background()
	offering, err := env.service.offeringByPublicID(ctx, offeringView.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	images, err := env.service.validateQuery(input)
	if err != nil {
		t.Fatal(err)
	}
	scopeKey := env.service.scopeKeyFor(env.userID)
	queryID := newPrefixedID("qq_")
	fp := fingerprint(env.userID, *input)
	created, q, err := env.service.insertPendingQuery(ctx, queryID, env.userID, key, fp, offering, *input, hashHex(scopeKey), images)
	if err != nil || !created || q.ID != queryID {
		t.Fatalf("构造 pending 失败：%v/%v/%v", err, created, q)
	}
	_, holdID, _, err := env.service.prepareBilling(ctx, queryID, env.userID, offering, input.Budget.MaxOutputTokens, images.Count,
		env.service.billingKey(queryID))
	if err != nil || holdID == "" {
		t.Fatalf("构造预占失败：%v/%s", err, holdID)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE public.question_queries
		SET updated_at = now() - interval '5 minutes' WHERE id = $1`, queryID); err != nil {
		t.Fatal(err)
	}
	return offering, scopeKey, queryID
}

func TestAnswerRecoversStalePendingWithFrozenFacts(t *testing.T) {
	fork := newIdempotentFork()
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	offering := env.createOffering(t, "恢复模型", "internal-original", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("恢复题", "cache_first")
	input.ModelOfferingID = offering.ID
	frozenOffering, scopeKey, queryID := seedStalePending(t, env, offering, &input, "recovery-key-0001")

	// 模拟 fork 已成功返回，但主平台在 finalize 前崩溃。
	if _, err := fork.Answer(context.Background(), buildRecoveryRequest(queryID, scopeKey, frozenOffering, input), queryID); err != nil {
		t.Fatal(err)
	}
	if httpCalls, modelCalls := fork.counts(); httpCalls != 1 || modelCalls != 1 {
		t.Fatalf("模拟首次调用计数不符：%d/%d", httpCalls, modelCalls)
	}

	// 模型引用和价格在崩溃后发生变化，恢复必须使用查询行冻结的事实。
	changed := testPricing(1_000_000, 2_000_000, 10000)
	changed.Version = "price-v2"
	changedJSON, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(context.Background(), `UPDATE public.question_offerings
		SET provider_ref = 'changed-model', pricing = $2, price_version = 'price-v2',
		version = version + 1 WHERE public_id = $1`, offering.ID, changedJSON); err != nil {
		t.Fatal(err)
	}

	started, release := fork.blockNextCalls()
	type result struct {
		view QueryView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		view, err := env.service.Answer(context.Background(), env.userID, "recovery-key-0001", input)
		done <- result{view: view, err: err}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("恢复未进入 fork 调用")
	}
	release()
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("恢复失败：%v", outcome.err)
	}
	if outcome.view.Status != "answered" || outcome.view.Answer != "A" {
		t.Fatalf("恢复结果不符：%#v", outcome.view)
	}
	if outcome.view.Model.OfferingID != offering.ID || outcome.view.Model.DisplayName != "恢复模型" {
		t.Fatalf("恢复不应切换公开模型：%#v", outcome.view.Model)
	}
	if outcome.view.Pricing.CostMicros != "1200" || outcome.view.Pricing.AmountMinor != "1" ||
		!outcome.view.Pricing.Charged || outcome.view.Pricing.PriceVersion != "price-v1" {
		t.Fatalf("恢复应使用冻结价格结算：%#v", outcome.view.Pricing)
	}
	if httpCalls, modelCalls := fork.counts(); httpCalls != 2 || modelCalls != 1 {
		t.Fatalf("恢复只应复用同一 fork 结果：HTTP %d，模型 %d", httpCalls, modelCalls)
	}
	if last := fork.lastRequest(); last.ModelOfferingID != "internal-original" {
		t.Fatalf("恢复使用了新模型引用：%s", last.ModelOfferingID)
	}
	if reserves, settles, releases, _, settled := env.billing.counts(); reserves != 1 || settles != 1 || releases != 1 || settled != 1 {
		t.Fatalf("恢复不应二次预占或扣费：%d/%d/%d/%d", reserves, settles, releases, settled)
	}
	var providerRef string
	var status string
	if err := env.pool.QueryRow(context.Background(), `SELECT COALESCE(offering_provider_ref, ''), status
		FROM public.question_queries WHERE id = $1`, queryID).Scan(&providerRef, &status); err != nil {
		t.Fatal(err)
	}
	if providerRef != "" || status != "answered" {
		t.Fatalf("完成后应清空冻结引用：%q/%s", providerRef, status)
	}
}

func TestAnswerStaleRecoveryIsAtomic(t *testing.T) {
	fork := newIdempotentFork()
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	offering := env.createOffering(t, "并发恢复模型", "internal-recover", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("并发恢复题", "cache_first")
	input.ModelOfferingID = offering.ID
	frozenOffering, scopeKey, queryID := seedStalePending(t, env, offering, &input, "recovery-concurrent-0001")
	if _, err := fork.Answer(context.Background(), buildRecoveryRequest(queryID, scopeKey, frozenOffering, input), queryID); err != nil {
		t.Fatal(err)
	}
	started, release := fork.blockNextCalls()

	const workers = 5
	type result struct {
		view QueryView
		err  error
	}
	results := make(chan result, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			view, err := env.service.Answer(context.Background(), env.userID, "recovery-concurrent-0001", input)
			results <- result{view: view, err: err}
		}()
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("并发恢复未进入 fork 调用")
	}
	release()
	wg.Wait()
	close(results)

	answered := 0
	for outcome := range results {
		if outcome.err == nil {
			if outcome.view.Status != "answered" || outcome.view.ID != queryID {
				t.Fatalf("并发恢复结果不符：%#v", outcome.view)
			}
			answered++
			continue
		}
		queryErr := requireQueryError(t, outcome.err, "request_in_progress", 409)
		if !queryErr.Retryable {
			t.Fatal("request_in_progress 应可安全重试")
		}
	}
	if answered == 0 {
		t.Fatal("并发恢复没有成功结果")
	}
	if httpCalls, modelCalls := fork.counts(); httpCalls != 2 || modelCalls != 1 {
		t.Fatalf("并发恢复只应有一个恢复者调用 fork：HTTP %d，模型 %d", httpCalls, modelCalls)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 1 || releases != 1 {
		t.Fatalf("并发恢复不应二次预占或扣费：%d/%d/%d", reserves, settles, releases)
	}
}

func TestAnswerStaleRecoveryUnknownStopsCharge(t *testing.T) {
	fork := &fakeFork{answer: func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{}, &ForkError{Code: "request_unknown", Message: "结果未知", Status: 409}
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	offering := env.createOffering(t, "未知恢复模型", "internal-recover-unknown", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("未知恢复题", "cache_first")
	input.ModelOfferingID = offering.ID
	seedStalePending(t, env, offering, &input, "recovery-unknown-0001")

	view, err := env.service.Answer(context.Background(), env.userID, "recovery-unknown-0001", input)
	if err != nil {
		t.Fatalf("恢复未知结果应返回待复核事实：%v", err)
	}
	if view.Status != "needs_review" || view.ReviewReason != "request_unknown" || view.Pricing.Charged {
		t.Fatalf("恢复未知结果必须停止收费：%#v", view)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("恢复未知结果应释放预占：%d/%d/%d", reserves, settles, releases)
	}
}

func TestQueryErrorHTTPContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/question-queries", nil)
	writeQueryError(recorder, request, requestInProgressError())
	if recorder.Code != http.StatusConflict {
		t.Fatalf("request_in_progress 应返回 409：%d", recorder.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Retryable bool   `json:"retryable"`
			Scope     string `json:"scope"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "request_in_progress" || !body.Error.Retryable ||
		body.Error.Scope != "request" || body.Error.RequestID == "" {
		t.Fatalf("request_in_progress HTTP 契约不符：%#v", body.Error)
	}
}

func TestAnswerStaleRecoveryRebuildsImagesFromInput(t *testing.T) {
	fork := newIdempotentFork()
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	offering := env.createOffering(t, "图片恢复模型", "internal-image-recover", testPricing(10_000_000, 20_000_000, 10000), true)
	image := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	input := queryBody("图片恢复题[图片1]", "cache_first")
	input.ModelOfferingID = offering.ID
	input.Question.Images = []string{image}
	frozenOffering, scopeKey, queryID := seedStalePending(t, env, offering, &input, "recovery-image-0001")
	if _, err := fork.Answer(context.Background(), buildRecoveryRequest(queryID, scopeKey, frozenOffering, input), queryID); err != nil {
		t.Fatal(err)
	}
	started, release := fork.blockNextCalls()
	type result struct {
		view QueryView
		err  error
	}
	done := make(chan result, 1)
	go func() {
		view, err := env.service.Answer(context.Background(), env.userID, "recovery-image-0001", input)
		done <- result{view: view, err: err}
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("图片恢复未进入 fork 调用")
	}
	release()
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("图片恢复失败：%v", outcome.err)
	}
	if outcome.view.Status != "answered" || outcome.view.Question.ImageCount != 1 {
		t.Fatalf("图片恢复结果不符：%#v", outcome.view)
	}
	last := fork.lastRequest()
	if len(last.Question.Images) != 1 || last.Question.Images[0] != image {
		t.Fatalf("图片恢复必须使用调用方再次提供的同一图片：%#v", last.Question.Images)
	}
	if httpCalls, modelCalls := fork.counts(); httpCalls != 2 || modelCalls != 1 {
		t.Fatalf("图片恢复不应产生第二次模型调用：HTTP %d，模型 %d", httpCalls, modelCalls)
	}
}

func TestAnswerAcceptsShortAnswerType(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "简答模型", "internal-short", nil, true)
	input := queryBody("简答题", "cache_first")
	input.Question.Type = "shortanswer"
	view := mustAnswer(t, env, "shortanswer-key-0001", input)
	if view.Status != "answered" || view.Question.Type != "shortanswer" {
		t.Fatalf("简答题未通过校验：%#v", view)
	}
	env.fork.mu.Lock()
	requests := append([]ForkAnswerRequest(nil), env.fork.requests...)
	env.fork.mu.Unlock()
	if len(requests) == 0 || requests[0].Question.Type != "shortanswer" {
		t.Fatalf("fork 请求未保留简答题类型：%#v", requests)
	}
}

// 多图题在 fork 侧按图片逐张转述，再产生一次回答调用；这类完整用量仍应正常结算，
// 不能因调用条数超过单图假设而降级为待复核。
func TestAnswerMultiImageCaptionUsageSettles(t *testing.T) {
	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls := make([]Call, 0, len(request.Question.Images)+1)
		var input, output, total int64
		for range request.Question.Images {
			in, out, sum := int64(100), int64(10), int64(110)
			calls = append(calls, Call{Slot: "caption", InputTokens: &in, OutputTokens: &out, TotalTokens: &sum, Status: "ok"})
			input, output, total = input+in, output+out, total+sum
		}
		in, out, sum := int64(200), int64(20), int64(220)
		calls = append(calls, Call{Slot: "answer", InputTokens: &in, OutputTokens: &out, TotalTokens: &sum, Status: "ok"})
		input, output, total = input+in, output+out, total+sum
		return ForkAnswerResponse{
			RequestID: request.RequestID, Status: "answered", Question: request.Question.Title, Answer: "A",
			Model: ForkModel{OfferingID: request.ModelOfferingID, DisplayName: "多图模型"},
			Mode:  request.Mode, Cache: ForkCache{Hit: false},
			Usage: Usage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Complete: true, Calls: calls},
		}, nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "多图模型", "internal-multi-image", testPricing(10_000_000, 20_000_000, 10000), true)

	input := queryBody("四图题[图片1][图片2][图片3][图片4]", "cache_first")
	input.Question.Images = []string{tinyPNG, tinyPNG, tinyPNG, tinyPNG}
	view := mustAnswer(t, env, "answer-multi-image-0001", input)
	if view.Status != "answered" || view.Answer != "A" {
		t.Fatalf("多图完整用量不应降级为待复核：%#v", view)
	}
	if view.Pricing.State != "quoted" || !view.Pricing.Charged || view.Pricing.AmountMinor != "1" {
		t.Fatalf("多图完整用量应按实际 token 结算：%#v", view.Pricing)
	}
	if reserves, settles, _, _, _ := env.billing.counts(); reserves != 1 || settles != 1 {
		t.Fatalf("多图查询应只预占并结算一次：%d/%d", reserves, settles)
	}
}

// 调用方取消时上游可能已产生费用：应保留待复核事实并释放预占，
// 不能因为请求上下文已取消而让清理与事实提交一起丢失。
func TestAnswerCancellationFinalizesAndReleasesHold(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v2/answers" {
			http.NotFound(w, r)
			return
		}
		// 请求体读完后服务端才会检测客户端断开；夹具先读空 body，避免等待空间挂住。
		_, _ = io.Copy(io.Discard, r.Body)
		startOnce.Do(func() { close(started) })
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		server.Close()
	})
	fork, err := NewHTTPFork(server.URL, "fork-test-token-0123456789", 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "取消模型", "internal-cancel", testPricing(10_000_000, 20_000_000, 10000), true)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := env.service.Answer(ctx, env.userID, "answer-cancel-0001", queryBody("取消题", "cache_first"))
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("未进入 fork 调用")
	}
	cancel()
	select {
	case <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("取消后 Answer 未结束")
	}

	var status, errorCode, billingState string
	if err := env.pool.QueryRow(context.Background(), `SELECT status, COALESCE(error_code, ''), billing_state
		FROM public.question_queries WHERE user_id = $1 AND idempotency_key = 'answer-cancel-0001'`,
		env.userID).Scan(&status, &errorCode, &billingState); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" || errorCode != "request_cancelled" {
		t.Fatalf("取消应保留待复核事实：%s/%s", status, errorCode)
	}
	if billingState != "released" {
		t.Fatalf("取消后应释放预占：%s", billingState)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("取消后账本应为预占1/结算0/释放1，实际：%d/%d/%d", reserves, settles, releases)
	}
}

// 结果事实写入失败时不能把预占留在账户上：补偿释放必须在回滚之后完成。
// 用只拦截题库事件的触发器制造确定性失败，并验证真实账本余额。
func TestAnswerEventFailureReleasesReservation(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true})
	book := attachedLedger(t, env)
	fundWallet(t, book, env, "1000")
	env.createOffering(t, "事件失败模型", "internal-event-fail", testPricing(10_000_000, 20_000_000, 10000), true)
	ctx := context.Background()
	if _, err := env.pool.Exec(ctx, `CREATE FUNCTION public.block_question_events() RETURNS trigger LANGUAGE plpgsql AS $body$
		BEGIN
			IF NEW.event_type = 'question.query.updated' THEN
				RAISE EXCEPTION 'blocked question event';
			END IF;
			RETURN NEW;
		END $body$`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.pool.Exec(ctx, `CREATE TRIGGER block_question_events BEFORE INSERT ON public.outbox_events
		FOR EACH ROW EXECUTE FUNCTION public.block_question_events()`); err != nil {
		t.Fatal(err)
	}

	_, err := env.service.Answer(ctx, env.userID, "answer-event-fail-0001", queryBody("事件失败题", "cache_first"))
	requireQueryError(t, err, "service_unavailable", 503)
	if env.fork.callCount() != 1 {
		t.Fatalf("事件失败发生在调用之后，实际 fork 调用 %d 次", env.fork.callCount())
	}

	var status, billingState string
	if err := env.pool.QueryRow(ctx, `SELECT status, billing_state FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'answer-event-fail-0001'`, env.userID).Scan(&status, &billingState); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" || billingState != "finalize_failed" {
		t.Fatalf("事实写入失败应待复核：%s/%s", status, billingState)
	}
	wallet, err := book.GetWallet(ctx, env.userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "1000" || wallet.ReservedMinor != "0" {
		t.Fatalf("预占必须回滚后释放，余额应为 1000/0，实际 %s/%s", wallet.AvailableMinor, wallet.ReservedMinor)
	}
	var holdStatus string
	var releasedMinor int64
	if err := env.pool.QueryRow(ctx, `SELECT h.status, h.released_minor FROM public.holds h
		JOIN public.question_queries q ON h.business_key = 'question-query:' || q.id
		WHERE q.user_id = $1 AND q.idempotency_key = 'answer-event-fail-0001'`, env.userID).
		Scan(&holdStatus, &releasedMinor); err != nil {
		t.Fatal(err)
	}
	if holdStatus != "released" || releasedMinor == 0 {
		t.Fatalf("预占应已释放并保留释放事实：%s/%d", holdStatus, releasedMinor)
	}
}

// fork 确定性错误的重放边界：入口阶段、可证未调用模型的错误在释放旧预占后可用
// 新一代预占重驱动；请求内容错误、冲突或可能已调用的错误同键只返回原失败。
func TestPermanentForkErrorDoesNotRetryOrCharge(t *testing.T) {
	cases := []struct {
		forkCode   string
		forkStatus int
		wantCode   string
		wantStatus int
		retryable  bool
	}{
		{"invalid_request", 400, "validation_failed", 422, false},
		{"shared_cache_disabled", 409, "shared_cache_not_allowed", 422, true},
		{"idempotency_conflict", 409, "idempotency_conflict", 409, false},
		{"not_found", 404, "not_found", 404, false},
		{"unauthenticated", 401, "provider_unavailable", 503, true},
		{"forbidden", 403, "provider_unavailable", 503, true},
		{"model_not_configured", 503, "not_configured", 503, true},
		{"model_disabled", 409, "model_disabled", 409, true},
		{"invalid_question", 422, "invalid_question", 422, true},
		{"budget_exceeded", 422, "budget_exceeded", 422, true},
	}
	for _, item := range cases {
		t.Run(item.forkCode, func(t *testing.T) {
			fork := &fakeFork{answer: func(ForkAnswerRequest) (ForkAnswerResponse, error) {
				return ForkAnswerResponse{}, &ForkError{Code: item.forkCode, Message: "上游原始细节 secret", Status: item.forkStatus}
			}}
			env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
			env.createOffering(t, "永久错误模型", "internal-permanent", testPricing(10_000_000, 20_000_000, 10000), true)
			key := "permanent-" + item.forkCode
			_, err := env.service.Answer(context.Background(), env.userID, key, queryBody("永久错误题", "cache_first"))
			queryErr := requireQueryError(t, err, item.wantCode, item.wantStatus)
			if queryErr.Retryable {
				t.Fatalf("永久错误不应标记可重试：%#v", queryErr)
			}
			if strings.Contains(queryErr.Message, "secret") {
				t.Fatalf("错误泄露 fork 原始响应：%s", queryErr.Message)
			}

			// 同键重放：不可重驱动的错误不再次调用 fork 或账本；入口阶段错误会用新一代预占重驱动。
			_, err = env.service.Answer(context.Background(), env.userID, key, queryBody("永久错误题", "cache_first"))
			replayErr := requireQueryError(t, err, item.wantCode, item.wantStatus)
			if replayErr.Retryable {
				t.Fatalf("重放分类仍应为不可重试：%#v", replayErr)
			}
			calls := env.fork.callCount()
			reserves, settles, releases, _, _ := env.billing.counts()
			if !item.retryable {
				if calls != 1 || reserves != 1 || settles != 0 || releases != 1 {
					t.Fatalf("不可重驱动的错误不得重试：fork=%d 预占/结算/释放=%d/%d/%d", calls, reserves, settles, releases)
				}
			} else {
				if calls != 2 || reserves != 2 || settles != 0 || releases != 2 {
					t.Fatalf("入口阶段错误应在新一代预占上重试：fork=%d 预占/结算/释放=%d/%d/%d", calls, reserves, settles, releases)
				}
			}
			var status string
			if err := env.pool.QueryRow(context.Background(), `SELECT status FROM public.question_queries
				WHERE user_id = $1 AND idempotency_key = $2`, env.userID, key).Scan(&status); err != nil || status != "failed" {
				t.Fatalf("错误应保存失败事实：%s/%v", status, err)
			}
		})
	}
}

// 不可重试的 503 必须如实回传 retryable=false，而不是按状态码推断。
func TestQueryErrorHTTPRetryableContract(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/question-queries", nil)
	writeQueryError(recorder, request, newQueryError("provider_unavailable", "题库服务凭据无效，请人工核对", http.StatusServiceUnavailable, false))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("应返回 503：%d", recorder.Code)
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
			Scope     string `json:"scope"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "provider_unavailable" || body.Error.Retryable || body.Error.Scope != "service" || body.Error.RequestID == "" {
		t.Fatalf("不可重试 503 封套不符：%#v", body.Error)
	}
}

// 首次余额不足后充值并重新调度：预占阶段失败没有调用模型，可用同一幂等键重新驱动。
func TestAnswerRecoversReservationFailureAfterBalanceRestored(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "余额恢复模型", "internal-balance", testPricing(10_000_000, 20_000_000, 10000), true)
	env.billing.reserveFailures = 1
	input := queryBody("余额恢复题", "cache_first")

	_, err := env.service.Answer(context.Background(), env.userID, "answer-balance-0001", input)
	requireQueryError(t, err, "insufficient_balance", http.StatusConflict)
	if env.fork.callCount() != 0 {
		t.Fatalf("余额不足不得调用 fork，实际 %d", env.fork.callCount())
	}

	view, err := env.service.Answer(context.Background(), env.userID, "answer-balance-0001", input)
	if err != nil {
		t.Fatalf("充值后同一幂等键应可重新驱动：%v", err)
	}
	if view.Status != "answered" || view.Answer != "A" {
		t.Fatalf("重驱动未取得答案：%#v", view)
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("重驱动只应调用一次 fork，实际 %d", env.fork.callCount())
	}
	// 真实账本按业务键幂等：首次预占虽因提交回执不明未写入查询行，重驱动会复用同一笔预占，
	// 而不是再占一次新额度；结算后按实际用量释放剩余额度。
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves < 1 || settles != 1 || releases > 1 {
		t.Fatalf("重驱动账本事实不符：预占/结算/释放 %d/%d/%d", reserves, settles, releases)
	}
	var reservedCount int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'answer-balance-0001' AND reserved_minor > 0`, env.userID).Scan(&reservedCount); err != nil {
		t.Fatal(err)
	}
	if reservedCount != 1 {
		t.Fatalf("重驱动后的查询必须保留预占事实：%d", reservedCount)
	}
}

// fork 入口阶段拒绝（配置、令牌、预算、停用）没有写入请求记录，也没有模型调用：
// 修复配置后同一幂等键重驱动必须成功。
func TestAnswerRecoversProvablePreCallForkFailure(t *testing.T) {
	for _, code := range []string{
		"model_not_configured", "model_disabled", "unauthenticated", "forbidden",
		"invalid_question", "budget_exceeded", "shared_cache_disabled",
	} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
				calls++
				if calls == 1 {
					return ForkAnswerResponse{}, &ForkError{Code: code, Message: "入口拒绝", Status: http.StatusServiceUnavailable}
				}
				return answeredForkResponse(request), nil
			}}
			env := newTestEnv(t, envOptions{fork: fork})
			env.createOffering(t, "入口校验模型", "internal-pre-call", nil, true)
			input := queryBody("入口校验题", "cache_first")
			key := "pre-call-" + code

			_, err := env.service.Answer(context.Background(), env.userID, key, input)
			if err == nil {
				t.Fatal("首次调用应保留入口失败")
			}
			view, err := env.service.Answer(context.Background(), env.userID, key, input)
			if err != nil {
				t.Fatalf("可证明未调用模型的失败应可重驱动：%v", err)
			}
			if view.Status != "answered" || calls != 2 {
				t.Fatalf("重驱动事实不符：status=%s calls=%d", view.Status, calls)
			}
			var billingState string
			if err := env.pool.QueryRow(context.Background(), `SELECT billing_state FROM public.question_queries
				WHERE user_id = $1 AND idempotency_key = $2`, env.userID, key).Scan(&billingState); err != nil {
				t.Fatal(err)
			}
			if billingState == "pending" {
				t.Fatalf("重驱动后仍停在 pending：%s", billingState)
			}
		})
	}
}

// 通用 503、上游错误与超时可能已经产生模型费用：同键重放必须保留原事实，不重新调用模型。
func TestAnswerDoesNotRetryAmbiguousForkFailure(t *testing.T) {
	cases := []struct {
		code        string
		wantCode    string
		wantStatus  int
		needsReview bool
	}{
		{"model_unavailable", "service_unavailable", http.StatusServiceUnavailable, false},
		{"service_unavailable", "service_unavailable", http.StatusServiceUnavailable, false},
		{"internal_error", "service_unavailable", http.StatusServiceUnavailable, false},
		{"upstream_rate_limited", "upstream_rate_limited", http.StatusTooManyRequests, false},
		{"upstream_timeout", "", 0, true},
		{"request_cancelled", "", 0, true},
	}
	for _, item := range cases {
		t.Run(item.code, func(t *testing.T) {
			fork := &fakeFork{answer: func(ForkAnswerRequest) (ForkAnswerResponse, error) {
				return ForkAnswerResponse{}, &ForkError{Code: item.code, Message: "结果未知", Status: http.StatusServiceUnavailable}
			}}
			env := newTestEnv(t, envOptions{fork: fork})
			env.createOffering(t, "不确定模型", "internal-uncertain", nil, true)
			input := queryBody("不确定题", "cache_first")
			key := "uncertain-" + item.code

			if _, err := env.service.Answer(context.Background(), env.userID, key, input); err == nil {
				t.Fatal("首次调用应返回错误")
			}
			view, err := env.service.Answer(context.Background(), env.userID, key, input)
			if item.needsReview {
				if err != nil {
					t.Fatalf("待复核结果应按原事实返回：%v", err)
				}
				if view.Status != "needs_review" {
					t.Fatalf("状态应为 needs_review：%s", view.Status)
				}
			} else {
				queryErr := requireQueryError(t, err, item.wantCode, item.wantStatus)
				if strings.Contains(queryErr.Message, "结果未知") {
					t.Fatalf("错误不得转发 fork 自由文本：%s", queryErr.Message)
				}
			}
			if fork.callCount() != 1 {
				t.Fatalf("可能已付费的失败不得重驱动，实际调用 %d", fork.callCount())
			}
		})
	}
}

// 可能已调用模型的失败即使预占已释放也不重驱动：未知结果保留原失败事实。
func TestAnswerDoesNotRetryFailureWithReleasedReservation(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_unavailable", Message: "上游结果未知", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "已预占模型", "internal-reserved", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("已预占题", "cache_first")

	_, err := env.service.Answer(context.Background(), env.userID, "answer-released-0001", input)
	requireQueryError(t, err, "service_unavailable", http.StatusServiceUnavailable)
	_, err = env.service.Answer(context.Background(), env.userID, "answer-released-0001", input)
	requireQueryError(t, err, "service_unavailable", http.StatusServiceUnavailable)
	if calls != 1 {
		t.Fatalf("已释放预占的未知失败不得重驱动，实际调用 %d", calls)
	}
	if reserves, _, releases, _, _ := env.billing.counts(); reserves != 1 || releases != 1 {
		t.Fatalf("账本事实不符：%d/%d", reserves, releases)
	}
	var holdID *string
	if err := env.pool.QueryRow(context.Background(), `SELECT billing_hold_id FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'answer-released-0001'`, env.userID).Scan(&holdID); err != nil {
		t.Fatal(err)
	}
	if holdID == nil || *holdID == "" {
		t.Fatal("已释放预占必须保留预占编号作为核对事实")
	}
}

// 主平台未配置 fork 时保存的失败没有外部调用：补齐配置后同一幂等键可重驱动。
func TestAnswerRecoversUnconfiguredFork(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "未接线模型", "internal-unwired", nil, true)
	env.service.fork = nil
	input := queryBody("未接线题", "cache_first")

	_, err := env.service.Answer(context.Background(), env.userID, "answer-unwired-0001", input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	env.service.fork = env.fork
	view, err := env.service.Answer(context.Background(), env.userID, "answer-unwired-0001", input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("补齐配置后应可重驱动：%v %#v", err, view)
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("应只调用一次 fork，实际 %d", env.fork.callCount())
	}
}

// 崩溃遗留、尚未写入任何收费决策的在途行，可证明没有调用模型：应在恢复时重新驱动，
// 而不是永久停在人工复核（后台做题会因此再也拿不到这个任务的答案）。
func TestAnswerRecoversInterruptedPendingWithoutExternalCall(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	offering := env.createOffering(t, "恢复在途模型", "internal-interrupted", nil, true)
	input := queryBody("中断题", "cache_first")
	images, err := env.service.validateQuery(&input)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	resolved, err := env.service.offeringByPublicID(ctx, offering.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	queryID := newPrefixedID("qq_")
	created, _, err := env.service.insertPendingQuery(ctx, queryID, env.userID, "interrupted-key-0001",
		fingerprint(env.userID, input), resolved, input, hashHex(env.service.scopeKeyFor(env.userID)), images)
	if err != nil || !created {
		t.Fatalf("无法建立在途夹具：%v/%v", created, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE public.question_queries SET updated_at = now() - interval '10 minutes'
		WHERE id = $1`, queryID); err != nil {
		t.Fatal(err)
	}
	view, err := env.service.Answer(ctx, env.userID, "interrupted-key-0001", input)
	if err != nil {
		t.Fatalf("无外部调用的在途行应可恢复：%v", err)
	}
	if view.Status != "answered" || view.ID != queryID {
		t.Fatalf("恢复应沿用原查询编号：%#v", view)
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("恢复只应调用一次 fork，实际 %d", env.fork.callCount())
	}
}

// 自动做题的题干来自平台 HTML，图片按 DOM 顺序附加，不包含 [图片N] 占位标记；
// 合法的平台图片题必须能进入模型调用，不能因缺少文本标记被拒绝。
func TestAnswerAcceptsImagesWithoutTextMarkers(t *testing.T) {
	const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "平台图片模型", "internal-platform-image", nil, true)
	input := queryBody("平台题干不含占位标记", "cache_first")
	input.Question.Images = []string{tinyPNG}
	view := mustAnswer(t, env, "platform-image-0001", input)
	if view.Status != "answered" || view.Question.ImageCount != 1 {
		t.Fatalf("图片题未按原样通过：%#v", view)
	}
	if env.fork.callCount() != 1 {
		t.Fatalf("图片题应调用一次 fork，实际 %d", env.fork.callCount())
	}
	env.fork.mu.Lock()
	requests := append([]ForkAnswerRequest(nil), env.fork.requests...)
	env.fork.mu.Unlock()
	if len(requests) != 1 || len(requests[0].Question.Images) != 1 {
		t.Fatalf("图片必须原样传给 fork：%#v", requests)
	}
}

// 主平台尚未发布模型时，失败发生在写入查询之前：发布后同一幂等键直接可用。
func TestAnswerQueriesAfterDefaultModelPublished(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	input := queryBody("待发布模型题", "cache_first")
	_, err := env.service.Answer(context.Background(), env.userID, "answer-publish-0001", input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	var count int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'answer-publish-0001'`, env.userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("模型未发布时不应留下查询行：%d", count)
	}
	env.createOffering(t, "后发布模型", "internal-published", nil, true)
	view := mustAnswer(t, env, "answer-publish-0001", input)
	if view.Status != "answered" || env.fork.callCount() != 1 {
		t.Fatalf("发布后应正常作答：%#v/%d", view, env.fork.callCount())
	}
}

// 缓存关闭的模型不能用于「缓存优先」安排：保存时就应拒绝，而不是让每次自动作题
// 在入口被 422 cache_not_allowed 拒绝后停在人工处理。
func TestValidateQuestionPolicyRejectsCacheFirstWithoutCache(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	recorder := env.call(t, http.MethodPost, "/admin/api/v1/question-models", map[string]any{
		"display_name": "无缓存模型", "provider_ref": "internal-no-cache", "capabilities": []string{"text"},
		"cache_policy": map[string]any{"allow": false, "scope": "user", "ttl_days": 7},
	}, env.admin(), nil)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("无法创建无缓存模型：%d %s", recorder.Code, recorder.Body.String())
	}
	var view adminOfferingView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	err := env.service.ValidateQuestionPolicy(context.Background(), view.ID, "cache_first")
	var safe *fault.Error
	if !errors.As(err, &safe) || safe.Code != "validation_failed" {
		t.Fatalf("缓存优先必须被拒绝：%v", err)
	}
	if err := env.service.ValidateQuestionPolicy(context.Background(), view.ID, "always_llm"); err != nil {
		t.Fatalf("每次调用模型应允许：%v", err)
	}
}

// 已释放预占、可证明未调用模型的入口拒绝：先确认旧预占释放，再用新一代业务键
// 按冻结价格重新预占；旧预占与分录保留，结算使用新的代次键。
func TestAnswerRetriesReleasedPreCallFailureWithNewGeneration(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	model := env.createOffering(t, "代次模型", "internal-generation", testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("代次题", "cache_first")
	const key = "answer-generation-0001"

	_, err := env.service.Answer(context.Background(), env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(context.Background(), env.userID, key)
	if err != nil || before == nil || before.BillingHoldID == "" || before.BillingState != "released" {
		t.Fatalf("首次失败事实不完整：%#v/%v", before, err)
	}
	oldSnapshot := parsePricingSnapshot(before.Pricing)
	if oldSnapshot.BillingKey == "" || oldSnapshot.Pricing == nil || before.ReservedMinor <= 0 {
		t.Fatalf("首次失败未保留冻结收费事实：%#v", oldSnapshot)
	}

	// 两次尝试之间发布新价格版本：重驱动必须沿用原冻结价格，不按新价格多占。
	repriced := *testPricing(20_000_000, 40_000_000, 10000)
	repriced.Version = "price-v2"
	changed := env.call(t, http.MethodPatch, "/admin/api/v1/question-models/"+model.ID,
		map[string]any{"price_version": repriced.Version, "pricing": repriced}, env.admin(),
		map[string]string{"If-Match": fmt.Sprintf(`"v%d"`, model.Version)})
	if changed.Code != http.StatusOK {
		t.Fatalf("无法发布新价格版本：%d %s", changed.Code, changed.Body.String())
	}

	view, err := env.service.Answer(context.Background(), env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("入口修复后应重驱动成功：%v %#v", err, view)
	}
	after, err := env.service.loadQueryByUserKey(context.Background(), env.userID, key)
	if err != nil || after == nil {
		t.Fatalf("无法读取重驱动结果：%v", err)
	}
	if after.BillingHoldID == "" || after.BillingHoldID == before.BillingHoldID {
		t.Fatalf("重驱动必须使用新一代预占：old=%s new=%s", before.BillingHoldID, after.BillingHoldID)
	}
	newSnapshot := parsePricingSnapshot(after.Pricing)
	if newSnapshot.BillingKey == oldSnapshot.BillingKey || !strings.Contains(newSnapshot.BillingKey, ":g") {
		t.Fatalf("新代次必须使用独立业务键：old=%s new=%s", oldSnapshot.BillingKey, newSnapshot.BillingKey)
	}
	if newSnapshot.PriceVersion != "price-v1" || newSnapshot.Pricing == nil || newSnapshot.Pricing.Version != "price-v1" {
		t.Fatalf("重驱动丢失原冻结价格版本：%#v", newSnapshot)
	}
	if after.ReservedMinor != before.ReservedMinor {
		t.Fatalf("重驱动金额必须沿用冻结报价：old=%d new=%d", before.ReservedMinor, after.ReservedMinor)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 2 || settles != 1 || releases != 2 {
		t.Fatalf("新一代预占账本事实不符：预占/结算/释放 %d/%d/%d", reserves, settles, releases)
	}
	if calls != 2 {
		t.Fatalf("只应发生一次入口拒绝与一次作答：%d", calls)
	}
}

// 真实账本回归：先预占 → 模型入口拒绝 → 全额释放 → 修复后重新预占（新代次）→
// 一次模型调用、一次结算，金额与价格沿用原冻结报价，旧预占保留在账本。
func TestAnswerRealLedgerNewGenerationAfterReleasedPreCallFailure(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, fork: fork})
	book := attachedLedger(t, env)
	fundWallet(t, book, env, "1000")
	model := env.createOffering(t, "真实账本代次模型", "internal-ledger-generation",
		testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("真实账本代次题", "cache_first")
	const key = "answer-ledger-generation-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.Status != "failed" ||
		before.BillingState != "released" || before.BillingHoldID == "" || before.ReservedMinor <= 0 {
		t.Fatalf("首次入口拒绝应释放并保留预占事实：%#v/%v", before, err)
	}
	oldHold, oldAmount := before.BillingHoldID, before.ReservedMinor
	oldSnapshot := parsePricingSnapshot(before.Pricing)
	if oldSnapshot.BillingKey == "" || oldSnapshot.Pricing == nil {
		t.Fatalf("首次失败未保留冻结收费事实：%#v", oldSnapshot)
	}

	repriced := *testPricing(20_000_000, 40_000_000, 10000)
	repriced.Version = "price-v2"
	changed := env.call(t, http.MethodPatch, "/admin/api/v1/question-models/"+model.ID,
		map[string]any{"price_version": repriced.Version, "pricing": repriced}, env.admin(),
		map[string]string{"If-Match": fmt.Sprintf(`"v%d"`, model.Version)})
	if changed.Code != http.StatusOK {
		t.Fatalf("无法发布新价格版本：%d %s", changed.Code, changed.Body.String())
	}

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("真实账本重驱动失败：%v %#v", err, view)
	}
	after, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || after == nil || after.BillingHoldID == "" || after.BillingHoldID == oldHold {
		t.Fatalf("重驱动必须形成独立的新预占：%#v/%v", after, err)
	}
	newSnapshot := parsePricingSnapshot(after.Pricing)
	if newSnapshot.BillingKey == oldSnapshot.BillingKey || !strings.Contains(newSnapshot.BillingKey, ":g") {
		t.Fatalf("新代次业务键未区分旧预占：old=%s new=%s", oldSnapshot.BillingKey, newSnapshot.BillingKey)
	}
	if newSnapshot.PriceVersion != "price-v1" || newSnapshot.Pricing == nil || newSnapshot.Pricing.Version != "price-v1" {
		t.Fatalf("重驱动没有沿用冻结价格：%#v", newSnapshot)
	}

	wallet, err := book.GetWallet(ctx, env.userID)
	if err != nil || wallet.AvailableMinor != "999" || wallet.ReservedMinor != "0" {
		t.Fatalf("钱包应只扣一次实际费用并释放全部预占：%#v/%v", wallet, err)
	}

	var oldStatus, oldKey string
	var oldHoldAmount int64
	if err := env.pool.QueryRow(ctx, `SELECT status, amount_minor, business_key FROM public.holds WHERE id = $1`,
		oldHold).Scan(&oldStatus, &oldHoldAmount, &oldKey); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "released" || oldHoldAmount != oldAmount {
		t.Fatalf("旧预占应保留为已释放事实：%s/%d（期望 %d）", oldStatus, oldHoldAmount, oldAmount)
	}
	var newStatus, newKey string
	var newAmount, newSettled, newReleased int64
	if err := env.pool.QueryRow(ctx, `SELECT status, amount_minor, settled_minor, released_minor, business_key
		FROM public.holds WHERE id = $1`, after.BillingHoldID).
		Scan(&newStatus, &newAmount, &newSettled, &newReleased, &newKey); err != nil {
		t.Fatal(err)
	}
	if newStatus != "released" || newAmount != oldAmount || newSettled != 1 || newReleased != oldAmount-1 {
		t.Fatalf("新预占结算事实不符：%s/%d/%d/%d（期望 released/%d/1/%d）",
			newStatus, newAmount, newSettled, newReleased, oldAmount, oldAmount-1)
	}
	if newKey == oldKey || !strings.Contains(newKey, ":g") {
		t.Fatalf("新旧预占业务键必须区分：old=%s new=%s", oldKey, newKey)
	}

	for _, item := range []struct {
		name string
		hold string
		kind string
		want int
	}{
		{"旧预占释放", oldHold, "release", 1},
		{"新预占结算", after.BillingHoldID, "capture", 1},
		{"新预占剩余释放", after.BillingHoldID, "release", 1},
	} {
		var count int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM public.journals WHERE kind = $2 AND hold_id = $1`,
			item.hold, item.kind).Scan(&count); err != nil || count != item.want {
			t.Fatalf("%s 分录不符：%d（期望 %d）/%v", item.name, count, item.want, err)
		}
	}
	if calls != 2 {
		t.Fatalf("只应发生一次入口拒绝与一次模型作答：%d", calls)
	}
}

// 新一代预占尚未完成时崩溃：pending 行保存的冻结收费事实必须在恢复时继续生效，
// 不能按当前条目重算或再次进人工复核。
func TestAnswerRecoversInterruptedFrozenGeneration(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	model := env.createOffering(t, "冻结中断模型", "internal-frozen-interrupted",
		testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("冻结中断题", "cache_first")
	const key = "answer-frozen-interrupted-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.BillingHoldID == "" {
		t.Fatalf("首次失败事实不完整：%#v/%v", before, err)
	}
	resolved, err := env.service.offeringByPublicID(ctx, model.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟已释放旧预占、抢占成功但新预占尚未写入时进程退出。
	claimed, err := env.service.claimRetryableFailure(ctx, before.ID, env.userID, resolved,
		hashHex(env.service.scopeKeyFor(env.userID)), input)
	if err != nil || claimed == nil || claimed.BillingHoldID != "" || claimed.Status != "pending" {
		t.Fatalf("无法构造新一代预占中断现场：%#v/%v", claimed, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE public.question_queries
		SET updated_at = now() - interval '10 minutes' WHERE id = $1`, before.ID); err != nil {
		t.Fatal(err)
	}

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("冻结代次中断应可恢复：%v %#v", err, view)
	}
	after, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || after == nil || after.BillingHoldID == "" {
		t.Fatalf("恢复后缺少新代次预占：%#v/%v", after, err)
	}
	snapshot := parsePricingSnapshot(after.Pricing)
	if snapshot.PriceVersion != "price-v1" || snapshot.Pricing == nil || snapshot.Pricing.Version != "price-v1" {
		t.Fatalf("中断恢复丢失冻结价格：%#v", snapshot)
	}
	if !strings.Contains(snapshot.BillingKey, ":g") {
		t.Fatalf("中断恢复应使用新一代业务键：%s", snapshot.BillingKey)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 2 || settles != 1 || releases != 2 {
		t.Fatalf("中断恢复账本事实不符：%d/%d/%d", reserves, settles, releases)
	}
	if calls != 2 {
		t.Fatalf("只应有一次入口拒绝与一次作答：%d", calls)
	}
}

// 首次明确前置失败时收费关闭：重试即使管理员已开启收费、发布新价格，
// 也必须沿用原“不收费”决定，不新开预占、不按新价格扣费。
func TestAnswerFreezesNoChargeDecisionAcrossRepricing(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: false, billing: true, fork: fork})
	model := env.createOffering(t, "免费决定模型", "internal-free-decision",
		testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("免费决定题", "cache_first")
	const key = "answer-free-decision-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.BillingHoldID != "" || before.BillingState != "automatic_charging_disabled" {
		t.Fatalf("收费关闭时不应形成预占：%#v/%v", before, err)
	}

	repriced := *testPricing(20_000_000, 40_000_000, 10000)
	repriced.Version = "price-v2"
	changed := env.call(t, http.MethodPatch, "/admin/api/v1/question-models/"+model.ID,
		map[string]any{"price_version": repriced.Version, "pricing": repriced}, env.admin(),
		map[string]string{"If-Match": fmt.Sprintf(`"v%d"`, model.Version)})
	if changed.Code != http.StatusOK {
		t.Fatalf("无法发布新价格版本：%d %s", changed.Code, changed.Body.String())
	}
	env.service.auto = true

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("重试应作答：%v %#v", err, view)
	}
	if view.Pricing.Charged || view.Pricing.BillingState != "automatic_charging_disabled" {
		t.Fatalf("必须沿用原不收费决定：%#v", view.Pricing)
	}
	if view.Pricing.PriceVersion != "price-v1" {
		t.Fatalf("必须沿用冻结价格版本：%s", view.Pricing.PriceVersion)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatalf("不收费决定不得触发账本操作：预占/结算/释放 %d/%d/%d", reserves, settles, releases)
	}
	if calls != 2 {
		t.Fatalf("只应有一次入口拒绝与一次作答：%d", calls)
	}
}

// 首次前置失败时模型未定价：管理员后来发布价格，重试仍按原未定价决定处理。
func TestAnswerFreezesUnpricedDecisionAcrossPricing(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	model := env.createOffering(t, "未定价决定模型", "internal-unpriced-decision", nil, true)
	input := queryBody("未定价决定题", "cache_first")
	const key = "answer-unpriced-decision-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.BillingHoldID != "" {
		t.Fatalf("未定价时不应形成预占：%#v/%v", before, err)
	}
	repriced := *testPricing(20_000_000, 40_000_000, 10000)
	repriced.Version = "price-v2"
	changed := env.call(t, http.MethodPatch, "/admin/api/v1/question-models/"+model.ID,
		map[string]any{"price_version": repriced.Version, "pricing": repriced}, env.admin(),
		map[string]string{"If-Match": fmt.Sprintf(`"v%d"`, model.Version)})
	if changed.Code != http.StatusOK {
		t.Fatalf("无法发布新价格版本：%d %s", changed.Code, changed.Body.String())
	}

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("重试应作答：%v %#v", err, view)
	}
	if view.Pricing.State != "unpriced" || view.Pricing.Charged {
		t.Fatalf("必须沿用原未定价决定：%#v", view.Pricing)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatalf("未定价决定不得触发账本操作：预占/结算/释放 %d/%d/%d", reserves, settles, releases)
	}
}

// 抢占成功但新预占尚未写入时进程退出：无预占的冻结决定也必须能按原决定恢复，
// 不能转人工复核，也不能在管理员后来开启收费后变成收费查询。
func TestAnswerRecoversInterruptedFrozenNoChargeGeneration(t *testing.T) {
	calls := 0
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		calls++
		if calls == 1 {
			return ForkAnswerResponse{}, &ForkError{Code: "model_not_configured", Message: "入口拒绝", Status: http.StatusServiceUnavailable}
		}
		return answeredForkResponse(request), nil
	}}
	env := newTestEnv(t, envOptions{auto: false, billing: true, fork: fork})
	model := env.createOffering(t, "冻结无预占模型", "internal-frozen-no-hold",
		testPricing(10_000_000, 20_000_000, 10000), true)
	input := queryBody("冻结无预占题", "cache_first")
	const key = "answer-frozen-no-hold-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "not_configured", http.StatusServiceUnavailable)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.BillingHoldID != "" {
		t.Fatalf("首次失败不应形成预占：%#v/%v", before, err)
	}
	resolved, err := env.service.offeringByPublicID(ctx, model.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := env.service.claimRetryableFailure(ctx, before.ID, env.userID, resolved,
		hashHex(env.service.scopeKeyFor(env.userID)), input)
	if err != nil || claimed == nil || claimed.Status != "pending" || claimed.BillingHoldID != "" {
		t.Fatalf("无法构造无预占冻结中断现场：%#v/%v", claimed, err)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE public.question_queries
		SET updated_at = now() - interval '10 minutes' WHERE id = $1`, before.ID); err != nil {
		t.Fatal(err)
	}
	env.service.auto = true

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("无预占冻结决定应可恢复：%v %#v", err, view)
	}
	if view.Pricing.Charged || view.Pricing.BillingState != "automatic_charging_disabled" {
		t.Fatalf("中断恢复必须沿用原不收费决定：%#v", view.Pricing)
	}
	if view.Pricing.PriceVersion != "price-v1" {
		t.Fatalf("中断恢复必须沿用冻结价格版本：%s", view.Pricing.PriceVersion)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatalf("中断恢复不得触发账本操作：%d/%d/%d", reserves, settles, releases)
	}
	if calls != 2 {
		t.Fatalf("只应有一次入口拒绝与一次作答：%d", calls)
	}
}

// 预占阶段失败从未保存收费快照：抢占后崩溃无 frozen 标记，恢复按当前报价重新驱动，
// 不因缺少冻结事实转人工复核。
func TestAnswerRecoversInterruptedClaimWithoutFrozenSnapshot(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	model := env.createOffering(t, "无快照恢复模型", "internal-no-snapshot",
		testPricing(10_000_000, 20_000_000, 10000), true)
	env.billing.reserveFailures = 1
	input := queryBody("无快照恢复题", "cache_first")
	const key = "answer-no-snapshot-0001"
	ctx := context.Background()

	_, err := env.service.Answer(ctx, env.userID, key, input)
	requireQueryError(t, err, "insufficient_balance", http.StatusConflict)
	before, err := env.service.loadQueryByUserKey(ctx, env.userID, key)
	if err != nil || before == nil || before.BillingHoldID != "" || string(before.Pricing) != "{}" {
		t.Fatalf("预占阶段失败不应保存收费快照：%#v/%v", before, err)
	}
	resolved, err := env.service.offeringByPublicID(ctx, model.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := env.service.claimRetryableFailure(ctx, before.ID, env.userID, resolved,
		hashHex(env.service.scopeKeyFor(env.userID)), input)
	if err != nil || claimed == nil || claimed.Status != "pending" {
		t.Fatalf("无法构造无快照中断现场：%#v/%v", claimed, err)
	}
	if string(claimed.Pricing) != "{}" {
		t.Fatalf("无快照不应生成 frozen 标记：%s", claimed.Pricing)
	}
	if _, err := env.pool.Exec(ctx, `UPDATE public.question_queries
		SET updated_at = now() - interval '10 minutes' WHERE id = $1`, before.ID); err != nil {
		t.Fatal(err)
	}

	view, err := env.service.Answer(ctx, env.userID, key, input)
	if err != nil || view.Status != "answered" {
		t.Fatalf("无快照中断应按当前报价恢复：%v %#v", err, view)
	}
	if reserves, settles, _, _, _ := env.billing.counts(); reserves != 1 || settles != 1 {
		t.Fatalf("恢复账本事实不符：%d/%d", reserves, settles)
	}
}
