//go:build integration

package questions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type fakeFork struct {
	mu           sync.Mutex
	calls        int
	requests     []ForkAnswerRequest
	invalidation string
	// invalidationMiss 模拟 fork 未实际失效（条目已失效或已过期）。
	invalidationMiss bool
	answer           func(ForkAnswerRequest) (ForkAnswerResponse, error)
	testResult       ForkTestResult
	catalog          *testCatalog
}

// testCatalog 是题库公开条目的内存夹具；目录内容由题库面板维护。
type testCatalog struct {
	mu        sync.Mutex
	offerings []ForkOffering
}

func (c *testCatalog) Offerings(context.Context) ([]ForkOffering, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := make([]ForkOffering, len(c.offerings))
	copy(result, c.offerings)
	return result, nil
}

func (c *testCatalog) addOffering(item ForkOffering) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !item.Enabled {
		item.Default = false
	}
	if item.Default {
		for i := range c.offerings {
			c.offerings[i].Default = false
		}
	}
	c.offerings = append(c.offerings, item)
}

// updateOffering 模拟题库面板修改公开条目；价格更新随后由主平台的目录读取生效。
func (c *testCatalog) updateOffering(publicID string, mutate func(*ForkOffering)) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.offerings {
		if c.offerings[i].PublicID != publicID {
			continue
		}
		mutate(&c.offerings[i])
		if !c.offerings[i].Enabled {
			c.offerings[i].Default = false
		}
		c.offerings[i].Version++
		c.offerings[i].UpdatedAt = time.Now().UTC()
		if c.offerings[i].Default {
			for j := range c.offerings {
				if j != i {
					c.offerings[j].Default = false
				}
			}
		}
		return true
	}
	return false
}

func (f *fakeFork) Offerings(ctx context.Context) ([]ForkOffering, error) {
	return f.catalog.Offerings(ctx)
}

func (f *fakeFork) Answer(_ context.Context, request ForkAnswerRequest, _ string) (ForkAnswerResponse, error) {
	f.mu.Lock()
	f.calls++
	f.requests = append(f.requests, request)
	fn := f.answer
	f.mu.Unlock()
	if fn != nil {
		return fn(request)
	}
	input, output, total := int64(100), int64(10), int64(110)
	return ForkAnswerResponse{
		RequestID: request.RequestID, Status: "answered", Question: request.Question.Title, Answer: "A",
		Model: ForkModel{OfferingID: request.ModelOfferingID, DisplayName: "标准", Capabilities: []string{"text"}},
		Mode:  request.Mode, Cache: ForkCache{Hit: false},
		Usage: Usage{InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Complete: true,
			Calls: []Call{{Slot: "answer", InputTokens: &input, OutputTokens: &output, TotalTokens: &total, Status: "ok"}}},
		LatencyMS: 10,
	}, nil
}

func (f *fakeFork) TestModel(_ context.Context, providerRef, kind string, _ bool) (ForkTestResult, error) {
	if f.testResult.Kind == "" {
		return ForkTestResult{Kind: kind, Status: "ok", Reachable: true, Functional: kind == "functional", LatencyMS: 7}, nil
	}
	return f.testResult, nil
}

func (f *fakeFork) InvalidateCache(_ context.Context, entryID, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.invalidation = entryID
	return !f.invalidationMiss, nil
}

func (f *fakeFork) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeBilling struct {
	mu            sync.Mutex
	reserves      int
	settles       int
	releases      int
	reservedMinor int64
	settledMinor  int64
	userID        string
	key           string
	reserveErr    error
	settleErr     error
	releaseErr    error
	// reserveFailures 让前 N 次预占按余额不足失败，用于验证充值后的重新调度。
	reserveFailures int
	// holds 按业务键保存预占，与真实账本一样同键重复预占返回同一笔预占。
	holds    map[string]string
	released map[string]bool
}

func (b *fakeBilling) Reserve(_ context.Context, tx pgx.Tx, userID, key string, amountMinor int64) (string, error) {
	if tx == nil {
		return "", errors.New("missing tx")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reserveErr != nil {
		return "", b.reserveErr
	}
	if b.holds == nil {
		b.holds = make(map[string]string)
	}
	if holdID := b.holds[key]; holdID != "" {
		return holdID, nil
	}
	if b.reserveFailures > 0 {
		b.reserveFailures--
		return "", fault.New("insufficient_balance", "余额不足")
	}
	holdID := web.NewID()
	b.holds[key] = holdID
	b.reserves++
	b.reservedMinor, b.userID, b.key = amountMinor, userID, key
	return holdID, nil
}

func (b *fakeBilling) Settle(_ context.Context, tx pgx.Tx, holdID, key string, actualMinor int64) error {
	if tx == nil {
		return errors.New("missing tx")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.settleErr != nil {
		return b.settleErr
	}
	if b.holds[key] != holdID {
		return errors.New("预占不存在或已释放")
	}
	if b.released[key] {
		return errors.New("预占已释放")
	}
	b.settles++
	b.settledMinor = actualMinor
	return nil
}

func (b *fakeBilling) Release(_ context.Context, tx pgx.Tx, holdID, key string) error {
	if tx == nil {
		return errors.New("missing tx")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.releaseErr != nil {
		return b.releaseErr
	}
	if b.holds[key] != holdID {
		return errors.New("预占不存在")
	}
	// 与真实账本一致：同一业务键的重复释放是幂等的。
	if b.released[key] {
		return nil
	}
	if b.released == nil {
		b.released = make(map[string]bool)
	}
	b.released[key] = true
	b.releases++
	return nil
}

func (b *fakeBilling) counts() (int, int, int, int64, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reserves, b.settles, b.releases, b.reservedMinor, b.settledMinor
}

type testEnv struct {
	pool      *pgxpool.Pool
	service   *Service
	mux       *http.ServeMux
	fork      *fakeFork
	catalogue *testCatalog
	billing   *fakeBilling
	userID    string
	adminID   string
}

type envOptions struct {
	auto        bool
	autoAllowed func(context.Context) (bool, error)
	billing     bool
	shared      bool
	allowShared bool
	fork        ForkClient
	maxInput    int
}

func newTestEnv(t *testing.T, options envOptions) *testEnv {
	t.Helper()
	pool := testsupport.NewPool(t)
	userID := addUser(t, pool)
	adminID := addAdmin(t, pool)
	fork := options.fork
	catalogue := &testCatalog{}
	var fakeForkClient *fakeFork
	if fork == nil {
		fakeForkClient = &fakeFork{catalog: catalogue}
		fork = fakeForkClient
	} else if value, ok := fork.(*fakeFork); ok {
		fakeForkClient = value
		if value.catalog == nil {
			value.catalog = catalogue
		}
		catalogue = value.catalog
	} else if value, ok := fork.(*idempotentFork); ok {
		if value.catalog == nil {
			value.catalog = catalogue
		}
		catalogue = value.catalog
	}
	var billing Billing
	var fakeBill *fakeBilling
	if options.billing {
		fakeBill = &fakeBilling{}
		billing = fakeBill
	}
	maxInput := options.maxInput
	if maxInput == 0 {
		maxInput = 32768
	}
	service, err := New(pool, Options{
		Fork: fork, Billing: billing, AutomaticCharging: options.auto,
		AutomaticChargingAllowed: options.autoAllowed, MaxInputTokens: maxInput,
		CacheScopeSecret: []byte("0123456789abcdef0123456789abcdef"),
		AllowSharedCache: options.allowShared,
		MaxOutputTokens:  256, MaxImages: 4, MaxImageBytes: 1 << 20, MaxImagePixels: 1_000_000,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	service.Register(mux)
	return &testEnv{pool: pool, service: service, mux: mux, fork: fakeForkClient, catalogue: catalogue, billing: fakeBill, userID: userID, adminID: adminID}
}

// republish 模拟题库面板发布新价格版本；主平台在下一次目录读取时生效。
func (e *testEnv) republish(t *testing.T, publicID string, pricing *OfferingPricing) {
	t.Helper()
	if !e.catalogue.updateOffering(publicID, func(item *ForkOffering) {
		item.Pricing = pricing
		item.PriceVersion = pricing.Version
	}) {
		t.Fatal("无法发布新价格版本")
	}
}

func addUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.users (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func addAdmin(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.admin_users (id, username, password_hash)
		VALUES ($1, $2, 'test-hash')`, id, "admin_"+strings.ReplaceAll(id, "-", "")[:16]); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *testEnv) call(t *testing.T, method, path string, body any, actor web.Actor, headers map[string]string) *httptest.ResponseRecorder {
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
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	req = req.WithContext(web.WithActor(req.Context(), actor))
	recorder := httptest.NewRecorder()
	e.mux.ServeHTTP(recorder, req)
	return recorder
}

func (e *testEnv) user() web.Actor {
	return web.Actor{ID: e.userID, Kind: "user", Permissions: []string{"self"}}
}

func (e *testEnv) admin() web.Actor {
	return web.Actor{ID: e.adminID, Kind: "admin", Permissions: []string{"*"}}
}

func testPricing(input, output, microsPerMinor Micros) *OfferingPricing {
	return &OfferingPricing{Version: "price-v1", InputMicrosPerMillionTokens: input,
		OutputMicrosPerMillionTokens: output, MicrosPerMinor: microsPerMinor,
		Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
}

func (e *testEnv) createOffering(t *testing.T, displayName, providerRef string, pricing *OfferingPricing, isDefault bool) adminOfferingView {
	t.Helper()
	if e.catalogue == nil {
		t.Fatal("测试需要题库目录夹具")
	}
	now := time.Now().UTC()
	publicID := "qm_" + strings.ReplaceAll(web.NewID(), "-", "")
	priceVersion := ""
	if pricing != nil {
		priceVersion = pricing.Version
	}
	item := ForkOffering{
		ID: web.NewID(), PublicID: publicID, DisplayName: displayName, ProviderRef: providerRef,
		Capabilities: []string{"text"}, Pricing: pricing, PriceVersion: priceVersion,
		CachePolicy: CachePolicy{Allow: true, Scope: "user", TTLDays: 7},
		Enabled:     true, Default: isDefault, Health: "configured", Version: 1, CreatedAt: now, UpdatedAt: now,
	}
	e.catalogue.addOffering(item)
	return adminOfferingView{ID: publicID, OfferingID: publicID, DisplayName: displayName, ProviderRef: providerRef,
		Capabilities: []string{"text"}, Enabled: true, Default: isDefault, PriceVersion: priceVersion,
		Pricing: pricing, CachePolicy: item.CachePolicy, Health: "configured", Version: 1,
		CreatedAt: now, UpdatedAt: now}
}

func queryBody(title, mode string) QueryInput {
	var input QueryInput
	input.Mode = mode
	input.CacheScope = "user"
	input.Question.Title = title
	input.Question.Options = "A. 甲\nB. 乙"
	input.Question.Type = "single"
	input.Budget.MaxOutputTokens = 128
	return input
}

func decodeQuery(t *testing.T, recorder *httptest.ResponseRecorder) QueryView {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("期望 200，实际 %d：%s", recorder.Code, recorder.Body.String())
	}
	var view QueryView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatalf("查询响应无效：%v", err)
	}
	return view
}

func errorCode(recorder *httptest.ResponseRecorder) string {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &body)
	return body.Error.Code
}

func TestCreateQueryPricingOwnershipAndReplay(t *testing.T) {
	price := testPricing(10_000_000, 20_000_000, 10000)
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	// 覆盖模型条目的价格快照。
	offering := env.createOffering(t, "标准模型", "internal-standard", price, true)
	if offering.ProviderRef != "internal-standard" {
		t.Fatal("管理员响应应包含内部引用")
	}
	headers := map[string]string{"Idempotency-Key": "query-key-0001"}
	first := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("价格题", "cache_first"), env.user(), headers))
	if first.Status != "answered" || first.Answer != "A" {
		t.Fatalf("查询结果不符：%#v", first)
	}
	if first.Pricing.State != "quoted" || first.Pricing.CostMicros != "1200" || first.Pricing.AmountMinor != "1" || !first.Pricing.Charged {
		t.Fatalf("计费快照不符：%#v", first.Pricing)
	}
	rawFirst, _ := json.Marshal(first)
	if strings.Contains(string(rawFirst), "internal-standard") {
		t.Fatal("用户查询响应不得泄露 fork 内部模型名")
	}
	reserves, settles, releases, reserved, settled := env.billing.counts()
	if reserves != 1 || settles != 1 || releases != 1 || settled != 1 {
		t.Fatalf("账本预占/结算/释放不符：%d/%d/%d/%d/%d", reserves, settles, releases, reserved, settled)
	}
	if reserved <= settled {
		t.Fatalf("预估预占应覆盖实际费用：%d/%d", reserved, settled)
	}
	var pricingJSON []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT pricing FROM public.question_queries WHERE id = $1`, first.ID).Scan(&pricingJSON); err != nil {
		t.Fatal(err)
	}
	var storedSnapshot pricingSnapshot
	if err := json.Unmarshal(pricingJSON, &storedSnapshot); err != nil {
		t.Fatalf("价格快照 JSON 无效：%v", err)
	}
	if storedSnapshot.Pricing == nil || storedSnapshot.Pricing.InputMicrosPerMillionTokens != 10_000_000 ||
		storedSnapshot.EstimatedAmountMinor <= 0 || storedSnapshot.AmountMinor != 1 ||
		!storedSnapshot.Charged || storedSnapshot.HoldID == "" {
		t.Fatalf("价格快照未完整持久化：%#v", storedSnapshot)
	}
	replay := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("价格题", "cache_first"), env.user(), headers))
	if replay.ID != first.ID || env.fork.callCount() != 1 {
		t.Fatal("幂等重放不应重复调用 fork")
	}
	_detail := decodeQuery(t, env.call(t, http.MethodGet, "/api/v1/question-queries/"+first.ID, nil, env.user(), nil))
	if _detail.ID != first.ID {
		t.Fatal("查询详情不一致")
	}
	other := addUser(t, env.pool)
	otherRecorder := env.call(t, http.MethodGet, "/api/v1/question-queries/"+first.ID, nil,
		web.Actor{ID: other, Kind: "user", Permissions: []string{"*"}}, nil)
	if otherRecorder.Code != http.StatusNotFound {
		t.Fatalf("其他用户不得读取查询：%d", otherRecorder.Code)
	}
	list := env.call(t, http.MethodGet, "/api/v1/question-queries?limit=10", nil, env.user(), nil)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"items"`) {
		t.Fatalf("查询列表失败：%d %s", list.Code, list.Body.String())
	}
	adminList := env.call(t, http.MethodGet, "/admin/api/v1/question-queries?limit=10", nil, env.admin(), nil)
	if adminList.Code != http.StatusOK || strings.Contains(adminList.Body.String(), `"answer":`) {
		t.Fatalf("管理员查询列表应脱敏：%d %s", adminList.Code, adminList.Body.String())
	}
	usage := env.call(t, http.MethodGet, "/admin/api/v1/question-usage", nil, env.admin(), nil)
	if usage.Code != http.StatusOK || !strings.Contains(usage.Body.String(), `"queries":1`) {
		t.Fatalf("题库用量失败：%d %s", usage.Code, usage.Body.String())
	}
}

func TestSettlementFailureReleasesAndReviews(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "结算失败模型", "settle-fail", testPricing(10_000_000, 20_000_000, 10000), true)
	env.billing.settleErr = errors.New("ledger unavailable")
	recorder := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("结算失败题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "settle-fail-key-0001"})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("结算失败不得伪成功：%d %s", recorder.Code, recorder.Body.String())
	}
	reserves, settles, releases, _, _ := env.billing.counts()
	if reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("结算失败应释放预占：%d/%d/%d", reserves, settles, releases)
	}
	var status, billingState string
	if err := env.pool.QueryRow(context.Background(), `SELECT status, billing_state FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'settle-fail-key-0001'`, env.userID).Scan(&status, &billingState); err != nil {
		t.Fatal(err)
	}
	if status != "needs_review" || billingState != "billing_settlement_failed" {
		t.Fatalf("结算失败应进入待复核：%s/%s", status, billingState)
	}
}

func TestChargingCallbackFalseKeepsPriceButNoCharge(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true,
		autoAllowed: func(context.Context) (bool, error) { return false, nil }})
	env.createOffering(t, "开关关闭模型", "gate-off", testPricing(10_000_000, 20_000_000, 10000), true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("开关题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "gate-off-key-0001"}))
	if view.Status != "answered" || view.Pricing.State != "quoted" || view.Pricing.Charged ||
		view.Pricing.BillingState != "automatic_charging_disabled" || view.Pricing.AutomaticChargingAllowed {
		t.Fatalf("运营开关关闭时仍应可查询但不扣费：%#v", view)
	}
	if view.Pricing.CostMicros == "" || view.Pricing.CostMicros == "0" {
		t.Fatalf("收费关闭不能伪称价格为 0：%#v", view.Pricing)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatal("收费开关关闭时不得预占或结算")
	}
	if env.fork.callCount() != 1 {
		t.Fatal("查询能力应与收费开关分离")
	}
	configured, automatic, err := env.service.PublicPricing(context.Background())
	if err != nil || !configured || automatic {
		t.Fatalf("PublicPricing 不符：%v/%v/%v", configured, automatic, err)
	}
}

func TestChargingCallbackErrorFailsClosed(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true,
		autoAllowed: func(context.Context) (bool, error) { return false, errors.New("config store down") }})
	env.createOffering(t, "开关错误模型", "gate-error", testPricing(10_000_000, 20_000_000, 10000), true)
	recorder := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("开关错误题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "gate-error-key-0001"})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("收费开关读取失败应 fail closed：%d %s", recorder.Code, recorder.Body.String())
	}
	if env.fork.callCount() != 0 {
		t.Fatal("收费开关读取失败时不得调用付费 fork")
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatal("收费开关读取失败时不得触达账本")
	}
	var status, billingState, errorCode string
	if err := env.pool.QueryRow(context.Background(), `SELECT status, billing_state, error_code FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'gate-error-key-0001'`, env.userID).Scan(&status, &billingState, &errorCode); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || billingState != "charging_setting_unavailable" || errorCode != "charging_setting_unavailable" {
		t.Fatalf("失败事实不符：%s/%s/%s", status, billingState, errorCode)
	}
	if _, _, err := env.service.PublicPricing(context.Background()); err == nil {
		t.Fatal("PublicPricing 应向上传递开关读取错误")
	}
}

func TestChargingDecisionFrozenBeforeFork(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	env := newTestEnv(t, envOptions{auto: false, billing: true,
		autoAllowed: func(context.Context) (bool, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			return calls == 1, nil
		}})
	env.createOffering(t, "冻结模型", "gate-frozen", testPricing(10_000_000, 20_000_000, 10000), true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("冻结题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "gate-frozen-key-0001"}))
	if !view.Pricing.Charged || !view.Pricing.AutomaticChargingAllowed || view.Pricing.BillingState != "settled" {
		t.Fatalf("查询应使用调用前冻结的收费决策：%#v", view.Pricing)
	}
	mu.Lock()
	firstCalls := calls
	mu.Unlock()
	if firstCalls != 1 {
		t.Fatalf("查询流程只应读取一次收费开关，实际 %d", firstCalls)
	}
	_, automatic, err := env.service.PublicPricing(context.Background())
	if err != nil || automatic {
		t.Fatalf("后续开关变化不应改写旧查询：%v/%v", automatic, err)
	}
	if reserves, settles, _, _, _ := env.billing.counts(); reserves != 1 || settles != 1 {
		t.Fatal("冻结为允许后应在同一流程预占并结算")
	}
}

func TestPublicPricingUsesPublishedOfferings(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true})
	configured, automatic, err := env.service.PublicPricing(context.Background())
	if err != nil || configured || !automatic {
		t.Fatalf("无已发布价格时应为未配置：%v/%v/%v", configured, automatic, err)
	}
	env.createOffering(t, "未定价发布", "public-unpriced", nil, true)
	configured, _, err = env.service.PublicPricing(context.Background())
	if err != nil || configured {
		t.Fatalf("未定价发布不应报告 configured：%v/%v", configured, err)
	}
	env.createOffering(t, "定价发布", "public-priced", testPricing(1_000_000, 0, 10000), false)
	configured, automatic, err = env.service.PublicPricing(context.Background())
	if err != nil || !configured || !automatic {
		t.Fatalf("已发布模型价格应报告 configured：%v/%v/%v", configured, automatic, err)
	}
}

func TestPerOfferingRatesAndMillionPrecision(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	first := testPricing(1_234_567, 0, 10000)
	second := testPricing(3_000_000, 0, 10000)
	a := env.createOffering(t, "费率甲", "rate-a", first, true)
	b := env.createOffering(t, "费率乙", "rate-b", second, false)
	queryA := queryBody("费率题甲", "cache_first")
	viewA := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryA, env.user(),
		map[string]string{"Idempotency-Key": "rate-a-key-0001"}))
	// 100个输入token对应123.4567微元；向上取整124微元，人民币记账为1分。
	if viewA.Pricing.State != "quoted" || viewA.Pricing.CostMicros != "124" || viewA.Pricing.AmountMinor != "1" {
		t.Fatalf("百万 token 精度计算不符：%#v", viewA.Pricing)
	}
	if viewA.Pricing.Pricing == nil || viewA.Pricing.Pricing.InputMicrosPerMillionTokens != first.InputMicrosPerMillionTokens {
		t.Fatalf("公开价格快照不符：%#v", viewA.Pricing.Pricing)
	}
	queryB := queryBody("费率题乙", "cache_first")
	queryB.ModelOfferingID = b.ID
	viewB := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryB, env.user(),
		map[string]string{"Idempotency-Key": "rate-b-key-0001"}))
	if viewB.Pricing.CostMicros != "300" {
		t.Fatalf("第二个模型应使用自己的费率：%#v", viewB.Pricing)
	}
	if a.ID == b.ID {
		t.Fatal("条目 ID 必须唯一")
	}
	nextPricing := testPricing(4_000_000, 0, 10000)
	nextPricing.Version = "price-v2"
	if !env.catalogue.updateOffering(b.ID, func(item *ForkOffering) {
		item.Pricing = nextPricing
		item.PriceVersion = nextPricing.Version
	}) {
		t.Fatal("题库条目价格未能更新")
	}
	oldB := decodeQuery(t, env.call(t, http.MethodGet, "/api/v1/question-queries/"+viewB.ID, nil, env.user(), nil))
	if oldB.Pricing.CostMicros != "300" || oldB.Pricing.Pricing == nil || oldB.Pricing.Pricing.InputMicrosPerMillionTokens != second.InputMicrosPerMillionTokens {
		t.Fatalf("已查询结果应保留发布时的价格快照：%#v", oldB.Pricing)
	}
	public := env.call(t, http.MethodGet, "/api/v1/question-models", nil, env.user(), nil)
	if !strings.Contains(public.Body.String(), `"input_micros_per_million_tokens":"1234567"`) {
		t.Fatalf("公开条目应公布公开价格：%s", public.Body.String())
	}
	if strings.Contains(public.Body.String(), "micros_per_minor") {
		t.Fatal("公开条目不应泄露结算精度内部字段")
	}
}

func TestExplicitFreeVsUnconfigured(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "免费模型", "free-model", testPricing(0, 0, 10000), true)
	free := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("免费题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "free-key-0001"}))
	if free.Pricing.State != "free" || free.Pricing.Charged {
		t.Fatalf("显式免费必须与未定价区分：%#v", free.Pricing)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatal("显式免费不得调用账本")
	}
	unconfigured := newTestEnv(t, envOptions{auto: true, billing: true})
	unconfigured.createOffering(t, "未配置模型", "unconfigured-model", nil, true)
	view := decodeQuery(t, unconfigured.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("未配置题", "cache_first"), unconfigured.user(),
		map[string]string{"Idempotency-Key": "unconfigured-key-0001"}))
	if view.Pricing.State != "unpriced" || view.Pricing.Charged {
		t.Fatalf("未配置价格必须是 unpriced：%#v", view.Pricing)
	}
	if reserves, _, _, _, _ := unconfigured.billing.counts(); reserves != 0 {
		t.Fatal("未定价不得预占")
	}
}

func TestReserveBeforeForkAndUnknownReview(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "未知模型", "unknown-model", testPricing(10_000_000, 20_000_000, 10000), true)
	env.fork.answer = func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		if reserves, _, _, _, _ := env.billing.counts(); reserves == 0 {
			t.Error("fork 调用前必须完成预占")
		}
		return ForkAnswerResponse{}, &ForkError{Code: "upstream_timeout", Message: "超时", Status: 504}
	}
	headers := map[string]string{"Idempotency-Key": "unknown-key-0001"}
	first := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("未知题", "cache_first"), env.user(), headers)
	if first.Code != http.StatusGatewayTimeout {
		t.Fatalf("未知调用应返回 504：%d %s", first.Code, first.Body.String())
	}
	reserves, settles, releases, _, _ := env.billing.counts()
	if reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("超时应预占并释放、不结算：%d/%d/%d", reserves, settles, releases)
	}
	if env.fork.callCount() != 1 {
		t.Fatal("超时不得自动重试")
	}
	second := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("未知题", "cache_first"), env.user(), headers)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), `"status":"needs_review"`) {
		t.Fatalf("同键重试应返回待复核事实：%d %s", second.Code, second.Body.String())
	}
	if env.fork.callCount() != 1 {
		t.Fatal("同键重试不得再次调用 fork")
	}
}

func TestContentExpiredFlag(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "过期模型", "expired-model", nil, true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("过期题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "expired-key-0001"}))
	if _, err := env.pool.Exec(context.Background(), `UPDATE public.question_queries
		SET question_title = '', question_options = '', answer = NULL, content_expired = true WHERE id = $1`, view.ID); err != nil {
		t.Fatal(err)
	}
	detail := decodeQuery(t, env.call(t, http.MethodGet, "/api/v1/question-queries/"+view.ID, nil, env.user(), nil))
	if !detail.ContentExpired || detail.Question.Title != "" || detail.Answer != "" {
		t.Fatalf("内容过期应明确标记且不自动重查：%#v", detail)
	}
}

func TestUnpricedDoesNotCharge(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: true})
	env.createOffering(t, "未定价模型", "internal-unpriced", nil, true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("未定价题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "unpriced-key-0001"}))
	if view.Pricing.State != "unpriced" || view.Pricing.Charged {
		t.Fatalf("未配置价格必须显示 unpriced 且不扣费：%#v", view.Pricing)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 0 || settles != 0 || releases != 0 {
		t.Fatal("未定价查询不得调用账本")
	}
	if len(view.Usage.Calls) == 0 {
		t.Fatal("usage 事实应保留")
	}
}

func TestUsageIncompleteEntersReviewWithoutCharge(t *testing.T) {
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{RequestID: request.RequestID, Status: "answered", Answer: "A",
			Model: ForkModel{OfferingID: request.ModelOfferingID}, Mode: request.Mode,
			Usage: Usage{Complete: false, Calls: []Call{{Slot: "answer", Status: "ok"}}}}, nil
	}}
	env := newTestEnv(t, envOptions{auto: true, billing: true, fork: fork})
	env.createOffering(t, "用量模型", "internal-usage", testPricing(10_000_000, 20_000_000, 10000), true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("用量缺失题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "usage-key-0001"}))
	if view.Status != "needs_review" || view.Pricing.State != "usage_incomplete" || view.Pricing.Charged {
		t.Fatalf("usage_incomplete 必须待复核且不扣费：%#v", view)
	}
	if reserves, settles, releases, _, _ := env.billing.counts(); reserves != 1 || settles != 0 || releases != 1 {
		t.Fatalf("用量不完整应预占后释放：%d/%d/%d", reserves, settles, releases)
	}
	replay := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("用量缺失题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "usage-key-0001"}))
	if replay.ID != view.ID || env.fork.callCount() != 1 {
		t.Fatal("待复核查询重放不应重复调用")
	}
}

func TestCacheHitZeroTokensNoCharge(t *testing.T) {
	entryID := "entry-1"
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
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("缓存题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "cache-key-0001"}))
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

func TestOfferingDisabledAndForkFailure(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	offering := env.createOffering(t, "版本模型", "internal-version", nil, true)
	if !env.catalogue.updateOffering(offering.ID, func(item *ForkOffering) { item.Enabled = false }) {
		t.Fatal("题库条目未能下架")
	}
	disabledBody := queryBody("下架题", "cache_first")
	disabledBody.ModelOfferingID = offering.ID
	query := env.call(t, http.MethodPost, "/api/v1/question-queries", disabledBody, env.user(),
		map[string]string{"Idempotency-Key": "disabled-key-0001"})
	if query.Code != http.StatusNotFound {
		t.Fatalf("下架模型不可被用户查询：%d %s", query.Code, query.Body.String())
	}
	public := env.call(t, http.MethodGet, "/api/v1/question-models", nil, env.user(), nil)
	if strings.Contains(public.Body.String(), "internal-version") {
		t.Fatal("公开模型列表不得泄露 provider_ref")
	}
	forkFail := &fakeFork{answer: func(ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{}, &ForkError{Code: "model_disabled", Message: "模型已下架", Status: 409}
	}}
	failEnv := newTestEnv(t, envOptions{fork: forkFail})
	failEnv.createOffering(t, "失败模型", "internal-fail", nil, true)
	failed := failEnv.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("失败题", "cache_first"), failEnv.user(),
		map[string]string{"Idempotency-Key": "fork-fail-key-0001"})
	if failed.Code != http.StatusConflict || errorCode(failed) != "model_disabled" {
		t.Fatalf("fork 模型下架应 409：%d %s", failed.Code, failed.Body.String())
	}
}

func TestAdminReadOnlyCatalogueAndPermissions(t *testing.T) {
	env := newTestEnv(t, envOptions{billing: false})
	offering := env.createOffering(t, "测试模型", "internal-test", nil, true)
	create := env.call(t, http.MethodPost, "/admin/api/v1/question-models",
		map[string]any{"display_name": "管理员写入"}, env.admin(), nil)
	if create.Code != http.StatusMethodNotAllowed {
		t.Fatalf("主平台管理员不应能创建公开条目：%d %s", create.Code, create.Body.String())
	}
	test := env.call(t, http.MethodPost, "/admin/api/v1/question-models/"+offering.ID+"/tests",
		map[string]any{"kind": "reachability"}, env.admin(), nil)
	if test.Code != http.StatusNotFound {
		t.Fatalf("模型测试应只在题库面板提供：%d %s", test.Code, test.Body.String())
	}
	userAsAdmin := env.call(t, http.MethodGet, "/admin/api/v1/question-models", nil, env.user(), nil)
	if userAsAdmin.Code != http.StatusForbidden {
		t.Fatalf("用户不能访问管理员接口：%d", userAsAdmin.Code)
	}
	adminAsUser := env.call(t, http.MethodGet, "/api/v1/question-models", nil, env.admin(), nil)
	if adminAsUser.Code != http.StatusForbidden {
		t.Fatalf("管理员不能使用用户接口身份：%d", adminAsUser.Code)
	}
	anonymous := env.call(t, http.MethodGet, "/api/v1/question-models", nil, web.Actor{}, nil)
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("匿名请求应 401：%d", anonymous.Code)
	}
	invalidate := env.call(t, http.MethodPost, "/admin/api/v1/question-cache/invalidations",
		map[string]any{"cache_entry_id": "entry-x", "reason": "用户报告错误"}, env.admin(), nil)
	if invalidate.Code != http.StatusOK || env.fork.invalidation != "entry-x" {
		t.Fatalf("缓存失效失败：%d %s", invalidate.Code, invalidate.Body.String())
	}
}

func TestCacheReportOwnership(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "缓存报告模型", "internal-report", nil, true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("报告题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "report-key-0001"}))
	report := env.call(t, http.MethodPost, "/api/v1/question-cache-reports",
		map[string]any{"query_id": view.ID, "reason": "答案与教材不一致"}, env.user(), nil)
	if report.Code != http.StatusCreated || !strings.Contains(report.Body.String(), `"status":"pending"`) {
		t.Fatalf("缓存报告失败：%d %s", report.Code, report.Body.String())
	}
	other := addUser(t, env.pool)
	foreign := env.call(t, http.MethodPost, "/api/v1/question-cache-reports",
		map[string]any{"query_id": view.ID, "reason": "越权报告"},
		web.Actor{ID: other, Kind: "user", Permissions: []string{"*"}}, nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("其他用户报告应 404：%d", foreign.Code)
	}
}

func TestOfferingPaginationAndVisibility(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	first := env.createOffering(t, "模型一", "internal-one", nil, false)
	second := env.createOffering(t, "模型二", "internal-two", nil, false)
	page1 := env.call(t, http.MethodGet, "/admin/api/v1/question-models?limit=1", nil, env.admin(), nil)
	var listing struct {
		Items      []adminOfferingView `json:"items"`
		NextCursor *string             `json:"next_cursor"`
	}
	if err := json.Unmarshal(page1.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.Items) != 1 || listing.NextCursor == nil {
		t.Fatalf("第一页分页不符：%s", page1.Body.String())
	}
	page2 := env.call(t, http.MethodGet, "/admin/api/v1/question-models?limit=1&cursor="+*listing.NextCursor, nil, env.admin(), nil)
	var listing2 struct {
		Items []adminOfferingView `json:"items"`
	}
	if err := json.Unmarshal(page2.Body.Bytes(), &listing2); err != nil {
		t.Fatal(err)
	}
	if len(listing2.Items) != 1 || listing2.Items[0].ID == listing.Items[0].ID {
		t.Fatalf("第二页分页不符：%s", page2.Body.String())
	}
	if first.ID == second.ID {
		t.Fatal("条目 ID 必须唯一")
	}
	public := env.call(t, http.MethodGet, "/api/v1/question-models", nil, env.user(), nil)
	if strings.Contains(public.Body.String(), "internal-one") || strings.Contains(public.Body.String(), `"provider_ref"`) {
		t.Fatalf("公开列表泄露内部引用：%s", public.Body.String())
	}
}

func TestConfiguredPriceWithoutBillingIsNotCharged(t *testing.T) {
	price := testPricing(10_000_000, 20_000_000, 10000)
	env := newTestEnv(t, envOptions{auto: true, billing: false})
	env.createOffering(t, "无账本模型", "internal-no-billing", price, true)
	view := decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("无账本题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "no-billing-key-0001"}))
	if view.Pricing.State != "quoted" || view.Pricing.Charged || view.Pricing.BillingState != "billing_not_connected" {
		t.Fatalf("未连接账本不能声称已扣费：%#v", view.Pricing)
	}
}

func TestDefaultOfferingReplacement(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	first := env.createOffering(t, "默认一", "internal-default-one", nil, true)
	second := env.createOffering(t, "默认二", "internal-default-two", nil, true)
	page := env.call(t, http.MethodGet, "/admin/api/v1/question-models?limit=10", nil, env.admin(), nil)
	var listing struct {
		Items []adminOfferingView `json:"items"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	defaults := 0
	for _, item := range listing.Items {
		if item.Default {
			defaults++
			if item.ID != second.ID {
				t.Fatalf("默认模型应切换到最新条目：%#v", listing.Items)
			}
		}
	}
	if defaults != 1 {
		t.Fatalf("默认模型必须唯一，实际 %d", defaults)
	}
	_ = first
}

func TestHTTPForkUsesServiceTokenAndIdempotency(t *testing.T) {
	var path, authorization, idempotency string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, authorization, idempotency = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"request_id":"req-1","status":"answered","answer":"A","model":{"offering_id":"standard"},"mode":"cache_first","cache":{"hit":false},"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2,"complete":true,"calls":[]}}`))
	}))
	defer server.Close()
	client, err := NewHTTPFork(server.URL, "token-value", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Answer(context.Background(), ForkAnswerRequest{RequestID: "req-1", ModelOfferingID: "standard", Mode: "cache_first",
		Question: ForkQuestion{Title: "题", Type: "single"}}, "idem-1")
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v2/answers" || authorization != "Bearer token-value" || idempotency != "idem-1" {
		t.Fatalf("HTTPFork 请求不符：%q %q %q", path, authorization, idempotency)
	}
}

func TestImageAndBudgetValidation(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "图片模型", "internal-image", nil, true)
	bad := queryBody("看图[图片1]", "cache_first")
	bad.Question.Images = []string{"data:image/png;base64,not-valid"}
	badRecorder := env.call(t, http.MethodPost, "/api/v1/question-queries", bad, env.user(),
		map[string]string{"Idempotency-Key": "image-key-0001"})
	if badRecorder.Code != http.StatusUnprocessableEntity || errorCode(badRecorder) != "validation_failed" {
		t.Fatalf("非法图片应 422：%d %s", badRecorder.Code, badRecorder.Body.String())
	}
	over := queryBody("预算题", "cache_first")
	over.Budget.MaxOutputTokens = 9999
	overRecorder := env.call(t, http.MethodPost, "/api/v1/question-queries", over, env.user(),
		map[string]string{"Idempotency-Key": "budget-key-0001"})
	if overRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("超预算应 422：%d %s", overRecorder.Code, overRecorder.Body.String())
	}
	if env.fork.callCount() != 0 {
		t.Fatal("输入校验失败不得调用 fork")
	}
}

func TestInsufficientBalanceBlocksFork(t *testing.T) {
	env := newTestEnv(t, envOptions{auto: true, billing: true})
	env.createOffering(t, "失败账本模型", "internal-billing-fail", testPricing(10_000_000, 20_000_000, 10000), true)
	env.billing.reserveErr = fault.New("insufficient_balance", "余额不足")
	recorder := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("账本题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "billing-fail-key-0001"})
	if recorder.Code != http.StatusConflict || errorCode(recorder) != "insufficient_balance" {
		t.Fatalf("余额不足不能伪成功：%d %s", recorder.Code, recorder.Body.String())
	}
	if env.fork.callCount() != 0 {
		t.Fatal("余额不足时不得调用 fork")
	}
	var status, billingState string
	if err := env.pool.QueryRow(context.Background(), `SELECT status, billing_state FROM public.question_queries
		WHERE user_id = $1 AND idempotency_key = 'billing-fail-key-0001'`, env.userID).Scan(&status, &billingState); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || billingState != "reserve_failed" {
		t.Fatalf("预占失败应保留失败事实：%s/%s", status, billingState)
	}
}

// 默认条目不可用时不会静默改选其他模型：查询在入口停止，等待管理员处理。
func TestUnavailableDefaultDoesNotFallBack(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "备用模型", "internal-backup", nil, false)
	offering := env.createOffering(t, "可用默认", "internal-available", nil, true)
	if !env.catalogue.updateOffering(offering.ID, func(item *ForkOffering) { item.Health = "unavailable" }) {
		t.Fatal("默认条目不可用状态未能更新")
	}
	query := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("默认不可用题", "cache_first"), env.user(),
		map[string]string{"Idempotency-Key": "dead-default-key-0001"})
	if query.Code != http.StatusServiceUnavailable || errorCode(query) != "not_configured" {
		t.Fatalf("不可用默认应停止查询：%d %s", query.Code, query.Body.String())
	}
	if env.fork.callCount() != 0 {
		t.Fatal("默认条目不可用时不得调用模型")
	}
}

// 缓存失效返回真实结果：fork 未实际失效（已失效/已过期）时不能记为已失效。
func TestInvalidateCacheReportsActualForkResult(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.fork.invalidationMiss = true
	miss := env.call(t, http.MethodPost, "/admin/api/v1/question-cache/invalidations",
		map[string]any{"cache_entry_id": "entry-gone", "reason": "用户报告错误"}, env.admin(), nil)
	if miss.Code != http.StatusOK || !strings.Contains(miss.Body.String(), `"invalidated":false`) {
		t.Fatalf("fork 未失效时应返回真实结果：%d %s", miss.Code, miss.Body.String())
	}
	var forkInvalidated bool
	if err := env.pool.QueryRow(context.Background(), `SELECT fork_invalidated FROM public.question_cache_invalidations
		WHERE cache_entry_id = 'entry-gone'`).Scan(&forkInvalidated); err != nil || forkInvalidated {
		t.Fatalf("失效记录必须与 fork 结果一致：%v/%v", forkInvalidated, err)
	}

	env.fork.mu.Lock()
	env.fork.invalidationMiss = false
	env.fork.mu.Unlock()
	hit := env.call(t, http.MethodPost, "/admin/api/v1/question-cache/invalidations",
		map[string]any{"cache_entry_id": "entry-live", "reason": "用户报告错误"}, env.admin(), nil)
	if hit.Code != http.StatusOK || !strings.Contains(hit.Body.String(), `"invalidated":true`) {
		t.Fatalf("fork 已失效时应返回 true：%d %s", hit.Code, hit.Body.String())
	}
	if err := env.pool.QueryRow(context.Background(), `SELECT fork_invalidated FROM public.question_cache_invalidations
		WHERE cache_entry_id = 'entry-live'`).Scan(&forkInvalidated); err != nil || !forkInvalidated {
		t.Fatalf("已失效记录应为 true：%v/%v", forkInvalidated, err)
	}
}

// fork 返回的失效条目与请求不一致时不能当作成功。
func TestHTTPForkInvalidateCacheResult(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		status  int
		want    bool
		wantErr bool
	}{
		{"已失效", `{"invalidated":true,"entry_id":"entry-1"}`, http.StatusOK, true, false},
		{"未失效", `{"invalidated":false,"entry_id":""}`, http.StatusOK, false, false},
		{"条目不一致", `{"invalidated":true,"entry_id":"entry-2"}`, http.StatusOK, false, true},
		{"上游错误", `{"error":{"code":"internal_error","message":"raw detail"}}`, http.StatusInternalServerError, false, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/internal/v1/cache/invalidations" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(item.status)
				_, _ = w.Write([]byte(item.body))
			}))
			defer server.Close()
			fork, err := NewHTTPFork(server.URL, "fork-test-token-0123456789", 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			invalidated, err := fork.InvalidateCache(context.Background(), "entry-1", "原因")
			if (err != nil) != item.wantErr || invalidated != item.want {
				t.Fatalf("失效结果不符：invalidated=%v err=%v", invalidated, err)
			}
			if err != nil && strings.Contains(err.Error(), "raw detail") {
				t.Fatalf("错误不得转发 fork 原始响应：%v", err)
			}
		})
	}
}

func TestIdempotencyConflict(t *testing.T) {
	env := newTestEnv(t, envOptions{})
	env.createOffering(t, "幂等模型", "internal-idem", nil, true)
	headers := map[string]string{"Idempotency-Key": "conflict-key-0001"}
	_ = decodeQuery(t, env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("幂等题", "cache_first"), env.user(), headers))
	conflict := env.call(t, http.MethodPost, "/api/v1/question-queries", queryBody("不同题", "cache_first"), env.user(), headers)
	if conflict.Code != http.StatusConflict || errorCode(conflict) != "idempotency_conflict" {
		t.Fatalf("相同键不同摘要应 409：%d %s", conflict.Code, conflict.Body.String())
	}
	if env.fork.callCount() != 1 {
		t.Fatal("冲突请求不得产生新的 fork 调用")
	}
}

// 缓存失效后，同一缓存条目的待处理报告应结清；失效原因按公开契约允许最长 1000 字。
func TestInvalidateCacheResolvesPendingReports(t *testing.T) {
	entryID := "entry-report"
	created := time.Now().UTC()
	zero := int64(0)
	fork := &fakeFork{answer: func(request ForkAnswerRequest) (ForkAnswerResponse, error) {
		return ForkAnswerResponse{RequestID: request.RequestID, Status: "cache_hit", Answer: "A",
			Model: ForkModel{OfferingID: request.ModelOfferingID}, Mode: request.Mode,
			Cache: ForkCache{Hit: true, EntryID: &entryID, CreatedAt: &created},
			Usage: Usage{InputTokens: &zero, OutputTokens: &zero, TotalTokens: &zero, Complete: true, Calls: []Call{}}}, nil
	}}
	env := newTestEnv(t, envOptions{fork: fork})
	env.createOffering(t, "报告模型", "internal-report", nil, true)
	view := mustAnswer(t, env, "report-query-0001", queryBody("报告题", "cache_first"))

	report := env.call(t, http.MethodPost, "/api/v1/question-cache-reports",
		map[string]any{"query_id": view.ID, "reason": "答案与题目不符"}, env.user(), nil)
	if report.Code != http.StatusCreated {
		t.Fatalf("报告应被记录：%d %s", report.Code, report.Body.String())
	}
	invalidated := env.call(t, http.MethodPost, "/admin/api/v1/question-cache/invalidations",
		map[string]any{"query_id": view.ID, "reason": strings.Repeat("核对失败原因", 100)}, env.admin(), nil)
	if invalidated.Code != http.StatusOK {
		t.Fatalf("失效应成功：%d %s", invalidated.Code, invalidated.Body.String())
	}
	var status string
	var resolvedAt *time.Time
	if err := env.pool.QueryRow(context.Background(), `SELECT status, resolved_at FROM public.question_cache_reports
		WHERE query_id = $1`, view.ID).Scan(&status, &resolvedAt); err != nil {
		t.Fatal(err)
	}
	if status != "resolved" || resolvedAt == nil {
		t.Fatalf("失效后报告应结清：%s/%v", status, resolvedAt)
	}
}
