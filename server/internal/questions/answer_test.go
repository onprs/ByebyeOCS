//go:build integration

package questions

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/ledger"
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

func (*idempotentFork) InvalidateCache(context.Context, string, string) error { return nil }

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
	_, holdID, _, err := env.service.prepareBilling(ctx, queryID, env.userID, offering, input.Budget.MaxOutputTokens, images.Count)
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
