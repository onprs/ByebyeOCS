//go:build integration && tiku_fork

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
	"golang.org/x/crypto/bcrypt"
)

const directForkToken = "isolated-fork-service-token-0123456789abcdef"
const directPanelPassword = "Isolated-Maintenance-2026!"

// 使用实际 fork 进程与独立数据库；只有上游模型和登录身份是受控夹具。
type directForkFixture struct {
	url       string
	db        *postgres.Database
	calls     atomic.Int64
	missing   atomic.Bool
	fail      atomic.Bool
	quiz      atomic.Bool
	mu        sync.Mutex
	models    []string
	before    func() bool
	badHold   atomic.Int64
	badSecret atomic.Int64
}

func newDirectForkFixture(t *testing.T, shared bool) *directForkFixture {
	t.Helper()
	f := &directForkFixture{db: testsupport.NewUnmigratedDatabase(t)}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/models" {
			_, _ = io.WriteString(w, `{"data":[]}`)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string          `json:"model"`
			Messages json.RawMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.models = append(f.models, body.Model)
		before := f.before
		f.mu.Unlock()
		f.calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer isolated-upstream-key-"+body.Model {
			f.badSecret.Add(1)
		}
		if before != nil && !before() {
			f.badHold.Add(1)
		}
		if f.fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		answer := "A"
		if f.quiz.Load() {
			for title, value := range map[string]string{"自动单选题": "B", "自动多选题": "C#A", "自动判断题": "对", "自动填空题": "甲#乙", "自动简答题": "完整简答内容"} {
				if bytes.Contains(body.Messages, []byte(title)) {
					answer = value
					break
				}
			}
		}
		response := map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "<answer>" + answer + "</answer>"}}}}
		if !f.missing.Load() {
			response["usage"] = map[string]int{"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150}
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(upstream.Close)
	directory := t.TempDir()
	writeFile := func(name string, data []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal("无法写入隔离题库配置")
		}
		return path
	}
	dsnPath := writeFile("fork.dsn", []byte(f.db.Pool().Config().ConnConfig.ConnString()))
	tokenPath := writeFile("fork.token", []byte(directForkToken))
	caPath := writeFile("upstream-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	f.url = "http://" + address
	models := make([]any, 0, 2)
	for _, id := range []string{"primary", "alternate"} {
		keyPath := writeFile(id+".key", []byte("isolated-upstream-key-"+id))
		models = append(models, map[string]any{
			"id": id, "display_name": "接口联调模型" + id, "protocol": "openai-chat", "model": id,
			"base_url": upstream.URL + "/v1", "api_key_file": keyPath, "context": 128000,
		})
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(directPanelPassword), 10)
	if err != nil {
		t.Fatal(err)
	}
	hashPath := writeFile("panel.hash", passwordHash)
	configuration, _ := json.Marshal(map[string]any{
		"listen": address, "database_dsn_file": dsnPath, "service_token_file": tokenPath,
		"upstream_ca_file": caPath, "allow_loopback_upstreams": true, "default_model_id": "primary",
		"cache": map[string]any{"ttl_days": 7, "max_ttl_days": 30, "shared_enabled": shared}, "models": models,
		"panel": map[string]any{"enabled": true, "origin": f.url, "users": []any{
			map[string]any{"username": "maintainer", "role": "maintainer", "password_hash_file": hashPath},
			map[string]any{"username": "viewer", "role": "viewer", "password_hash_file": hashPath},
		}},
	})
	configurationPath := writeFile("fork.json", configuration)
	binary := os.Getenv("BYEBYEOCS_TEST_TIKU_BINARY")
	if binary == "" {
		binary = filepath.Join(directory, "ocs-tiku-fork")
		build := exec.Command("go", "build", "-race", "-o", binary, "./cmd/ocs-tiku-fork")
		build.Dir = filepath.Clean("../../../services/tiku")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("无法构建实际题库 fork：%v\n%s", err, output)
		}
	}
	migrate := exec.Command(binary, "migrate", "-config", configurationPath)
	if output, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("题库独立数据库迁移失败：%v\n%s", err, output)
	}
	logFile, err := os.Create(filepath.Join(directory, "fork.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "serve", "-config", configurationPath)
	command.Stdout, command.Stderr = logFile, logFile
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal("无法启动实际题库 fork")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("题库进程退出异常：%v", err)
			}
		case <-time.After(10 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Error("题库进程未在期限内停止")
		}
		_ = logFile.Close()
	})
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			done <- err
			data, _ := os.ReadFile(logFile.Name())
			t.Fatalf("题库进程提前退出：%v\n%s", err, data)
		default:
		}
		response, err := client.Get(f.url + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return f
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("实际题库 fork 未在期限内就绪")
	return nil
}

// seedOffering 直接写入真实 fork 数据库，模拟题库面板已完成发布。
func (f *directForkFixture) seedOffering(t *testing.T, displayName, providerRef, scope string, ttlDays int,
	pricing *questions.OfferingPricing, isDefault bool) string {
	t.Helper()
	publicID := "qm_" + strings.ReplaceAll(web.NewID(), "-", "")
	offeringID := web.NewID()
	capabilities, err := json.Marshal([]string{"text"})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := json.Marshal(map[string]any{"allow": true, "scope": scope, "ttl_days": ttlDays})
	if err != nil {
		t.Fatal(err)
	}
	var pricingJSON []byte
	priceVersion := ""
	if pricing != nil {
		if pricingJSON, err = json.Marshal(pricing); err != nil {
			t.Fatal(err)
		}
		priceVersion = pricing.Version
	}
	if _, err := f.db.Pool().Exec(context.Background(), `INSERT INTO tiku.offerings
		(id, public_id, display_name, description, provider_ref, capabilities, pricing, price_version,
		 cache_policy, enabled, is_default, sort_order)
		VALUES ($1,$2,$3,'',$4,$5,$6,$7,$8,true,$9,0)`,
		offeringID, publicID, displayName, providerRef, capabilities, pricingJSON, priceVersion, policy, isDefault); err != nil {
		t.Fatal("无法写入题库公开条目", err)
	}
	if pricing != nil {
		if _, err := f.db.Pool().Exec(context.Background(), `INSERT INTO tiku.offering_price_versions
			(id, offering_id, version, pricing, created_by) VALUES ($1,$2,$3,$4,'integration-fixture')`,
			web.NewID(), offeringID, pricing.Version, pricingJSON); err != nil {
			t.Fatal("无法写入题库价格版本", err)
		}
	}
	return publicID
}

func (f *directForkFixture) setBefore(before func() bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.before = before
}

func TestTikuForkDirectAdaptation(t *testing.T) {
	fork := newDirectForkFixture(t, true)
	client, err := questions.NewHTTPFork(fork.url, directForkToken, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fixture := newRuntimeFixture(t, fixtureOptions{fork: client, charging: true, sharedCache: true})
	ctx := context.Background()
	if fixture.runtime.db.Pool().Config().ConnConfig.Database == fork.db.Pool().Config().ConnConfig.Database {
		t.Fatal("主服务与 fork 没有隔离数据库")
	}
	user, admin := fixtureClient(), fixtureClient()
	defer user.CloseIdleConnections()
	defer admin.CloseIdleConnections()
	csrf, userID := loginFixtureUser(t, fixture, user, "learner-a")
	code, _, login := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{
		"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP(),
	}, nil)
	if code != http.StatusOK {
		t.Fatal("无法登录联调管理员", code)
	}
	adminCSRF := login["csrf_token"].(string)
	actor := web.Actor{ID: login["admin"].(map[string]any)["id"].(string), Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
	fund := func(t *testing.T, id string) {
		t.Helper()
		wallet, err := fixture.runtime.Ledger.GetWallet(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = fixture.runtime.Ledger.AdjustWallet(ctx, actor, web.NewID(), ledger.WalletAdjustmentInput{
			WalletID: wallet.ID, Direction: "credit", AmountMinor: "1000", Reason: "独立题库适配测试合成资金",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fund(t, userID)
	enabled := true
	if _, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, 1, ledger.BillingPatch{TikuChargeEnabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	modelIDs := make(map[string]string)
	adaptationPricing := &questions.OfferingPricing{Version: "direct-adaptation-v1", InputMicrosPerMillionTokens: 1000000,
		OutputMicrosPerMillionTokens: 2000000, MicrosPerMinor: 10000, Rounding: "ceil", Rule: "tokens_x_micros_per_million_v1"}
	for _, provider := range []string{"primary", "alternate", "shared"} {
		providerRef, scope := provider, "user"
		if provider == "shared" {
			providerRef, scope = "primary", "shared"
		}
		modelIDs[provider] = fork.seedOffering(t, "题库适配模型"+provider, providerRef, scope, 1, adaptationPricing, false)
	}
	fork.setBefore(func() bool {
		var held int64
		err := fixture.runtime.db.Pool().QueryRow(ctx, "SELECT COALESCE(sum(reserved_minor), 0) FROM public.wallets").Scan(&held)
		return err == nil && held > 0
	})
	queryBody := func(model, mode, title, options string) map[string]any {
		return map[string]any{"model_offering_id": model, "mode": mode,
			"question": map[string]any{"title": title, "type": "single", "options": options, "images": []string{}},
			"budget":   map[string]int{"max_output_tokens": 100}}
	}
	query := func(t *testing.T, httpClient *http.Client, token, key string, body map[string]any) map[string]any {
		t.Helper()
		code, _, result := requestJSON(t, httpClient, "POST", fixture.user.URL+"/api/v1/question-queries", token, body, map[string]string{"Idempotency-Key": key})
		if code != http.StatusOK {
			t.Fatal("跨服务查询失败", code, result)
		}
		return result
	}
	assertQuery := func(t *testing.T, result map[string]any, status string, tokens int, charged bool) {
		t.Helper()
		usage := result["usage"].(map[string]any)
		pricing := result["pricing"].(map[string]any)
		if result["status"] != status || usage["total_tokens"] != float64(tokens) || pricing["charged"] != charged || result["answer"] != "A" {
			t.Fatal("跨服务答案、token 或收费结果不符", result)
		}
	}
	assertWallet := func(t *testing.T, id, available string) {
		t.Helper()
		wallet, err := fixture.runtime.Ledger.GetWallet(ctx, id)
		if err != nil || wallet.AvailableMinor != available || wallet.ReservedMinor != "0" {
			t.Fatalf("钱包结算或预占释放不符：%+v，错误：%v", wallet, err)
		}
	}
	title, options := "主项目与独立题库接口联调", "A. 第一项\nB. 第二项"
	body := queryBody(modelIDs["primary"], "cache_first", title, options)
	key := web.NewID()
	var first, cached map[string]any
	if !t.Run("真实HTTP查询与预占结算", func(t *testing.T) {
		first = query(t, user, csrf, key, body)
		assertQuery(t, first, "answered", 150, true)
		if first["model"].(map[string]any)["offering_id"] != modelIDs["primary"] || fork.calls.Load() != 1 {
			t.Fatal("公开模型与 fork 引用映射错误")
		}
		assertWallet(t, userID, "999")
	}) {
		return
	}
	t.Run("查询重试不重复调用或收费", func(t *testing.T) {
		replayed := query(t, user, csrf, key, body)
		if replayed["id"] != first["id"] || fork.calls.Load() != 1 {
			t.Fatal("主平台幂等重试改变了查询或重复请求模型")
		}
		assertWallet(t, userID, "999")
	})
	if !t.Run("精确缓存命中为零token且免费", func(t *testing.T) {
		cached = query(t, user, csrf, web.NewID(), body)
		assertQuery(t, cached, "cache_hit", 0, false)
		if fork.calls.Load() != 1 {
			t.Fatal("缓存命中仍请求上游模型")
		}
		assertWallet(t, userID, "999")
	}) {
		return
	}
	t.Run("每次使用模型跳过缓存", func(t *testing.T) {
		result := query(t, user, csrf, web.NewID(), queryBody(modelIDs["primary"], "always_llm", title, options))
		assertQuery(t, result, "answered", 150, true)
		if fork.calls.Load() != 2 {
			t.Fatal("always_llm 没有调用上游模型")
		}
		assertWallet(t, userID, "998")
	})
	t.Run("选项变化不复用旧答案缓存", func(t *testing.T) {
		result := query(t, user, csrf, web.NewID(), queryBody(modelIDs["primary"], "cache_first", title, "A. 第一项\nB. 修改后的选项"))
		assertQuery(t, result, "answered", 150, true)
		assertWallet(t, userID, "997")
	})
	t.Run("请求级模型选择隔离缓存与密钥", func(t *testing.T) {
		result := query(t, user, csrf, web.NewID(), queryBody(modelIDs["alternate"], "cache_first", title, options))
		assertQuery(t, result, "answered", 150, true)
		fork.mu.Lock()
		lastModel := fork.models[len(fork.models)-1]
		fork.mu.Unlock()
		if lastModel != "alternate" || result["model"].(map[string]any)["offering_id"] != modelIDs["alternate"] || fork.badSecret.Load() != 0 {
			t.Fatal("模型选择、缓存分区或独立上游密钥不符")
		}
		assertWallet(t, userID, "996")
	})
	t.Run("不同用户的默认缓存隔离", func(t *testing.T) {
		other := fixtureClient()
		defer other.CloseIdleConnections()
		otherCSRF, otherID := loginFixtureUser(t, fixture, other, "learner-b")
		fund(t, otherID)
		before := fork.calls.Load()
		result := query(t, other, otherCSRF, web.NewID(), body)
		assertQuery(t, result, "answered", 150, true)
		if fork.calls.Load() != before+1 {
			t.Fatal("不同用户复用了私有答案缓存")
		}
		assertWallet(t, otherID, "999")
	})
	t.Run("管理员使错误缓存实际失效", func(t *testing.T) {
		current := query(t, user, csrf, web.NewID(), body)
		entryID := current["cache"].(map[string]any)["entry_id"]
		code, _, result := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/question-cache/invalidations", adminCSRF,
			map[string]any{"cache_entry_id": entryID, "reason": strings.Repeat("答案核对", 150)}, nil)
		if code != http.StatusOK || result["invalidated"] != true {
			t.Fatal("缓存失效没有贯通主服务与 fork", code, result)
		}
		before := fork.calls.Load()
		assertQuery(t, query(t, user, csrf, web.NewID(), body), "answered", 150, true)
		if fork.calls.Load() != before+1 {
			t.Fatal("已失效缓存仍被使用")
		}
		assertWallet(t, userID, "995")
	})
	t.Run("服务端token缺失进入复核且不扣费", func(t *testing.T) {
		fork.missing.Store(true)
		defer fork.missing.Store(false)
		before := fork.calls.Load()
		input := queryBody(modelIDs["primary"], "always_llm", "上游未返回用量", options)
		idem := web.NewID()
		result := query(t, user, csrf, idem, input)
		if result["status"] != "needs_review" || result["usage"].(map[string]any)["complete"] != false || result["pricing"].(map[string]any)["charged"] != false {
			t.Fatal("缺失 token 被当作正常或可扣费答案", result)
		}
		query(t, user, csrf, idem, input)
		if fork.calls.Load() != before+1 {
			t.Fatal("用量待复核的重试再次请求了付费模型")
		}
		assertWallet(t, userID, "995")
	})
	t.Run("上游失败结果不明进入复核且不重复调用", func(t *testing.T) {
		fork.fail.Store(true)
		defer fork.fail.Store(false)
		before := fork.calls.Load()
		idem := web.NewID()
		input := queryBody(modelIDs["primary"], "always_llm", "上游响应异常", options)
		code, _, result := requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, input, map[string]string{"Idempotency-Key": idem})
		if code != http.StatusConflict || result["error"].(map[string]any)["code"] != "request_unknown" {
			t.Fatal("不明用量没有进入复核", code, result)
		}
		var status, reason string
		if err := fixture.runtime.db.Pool().QueryRow(ctx, `SELECT status, COALESCE(error_code,'') FROM public.question_queries WHERE user_id=$1 AND idempotency_key=$2`, userID, idem).Scan(&status, &reason); err != nil || status != "needs_review" || reason != "request_unknown" {
			t.Fatal("主平台没有保存待复核事实", status, reason, err)
		}
		_, _, _ = requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/question-queries", csrf, input, map[string]string{"Idempotency-Key": idem})
		if fork.calls.Load() != before+1 {
			t.Fatal("结果未知后重复调用了上游")
		}
		assertWallet(t, userID, "995")
	})
	if fork.badHold.Load() != 0 {
		t.Fatal("真实上游模型请求发生在钱包预占之前")
	}
	fork.setBefore(nil)
	t.Run("主平台不再写入公开目录", func(t *testing.T) {
		code, _, _ := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/question-models", adminCSRF,
			map[string]any{"display_name": "不应创建"}, nil)
		if code != http.StatusMethodNotAllowed {
			t.Fatal("主平台管理员仍能写入公开目录", code)
		}
		assertWallet(t, userID, "995")
	})
	t.Run("fork幂等响应与冲突保护", func(t *testing.T) {
		request := questions.ForkAnswerRequest{RequestID: web.NewID(), ModelOfferingID: "primary", Mode: "always_llm", CacheScope: "user", CacheScopeKey: "direct-contract-cache-scope",
			Question: questions.ForkQuestion{Title: "独立接口幂等性", Type: "single", Options: options}, Budget: questions.ForkBudget{MaxOutputTokens: 100}}
		key := web.NewID()
		before := fork.calls.Load()
		one, err := client.Answer(ctx, request, key)
		if err != nil {
			t.Fatal(err)
		}
		two, err := client.Answer(ctx, request, key)
		if err != nil || one.RequestID != two.RequestID || one.Answer != two.Answer || fork.calls.Load() != before+1 {
			t.Fatal("fork 幂等重试重复执行或返回不一致", err)
		}
		request.Question.Title = "修改后的请求"
		_, err = client.Answer(ctx, request, key)
		if err == nil || fork.calls.Load() != before+1 {
			t.Fatal("fork 未拒绝相同幂等键的不同请求")
		}
	})
	t.Run("内部接口拒绝缺少服务令牌", func(t *testing.T) {
		response, err := http.Get(fork.url + "/internal/v1/models")
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatal("内部模型目录没有校验服务令牌", response.StatusCode)
		}
	})

	t.Run("双方显式启用后共享精确缓存", func(t *testing.T) {
		other := fixtureClient()
		defer other.CloseIdleConnections()
		otherCSRF, otherID := loginFixtureUser(t, fixture, other, "learner-b")
		input := queryBody(modelIDs["shared"], "cache_first", "明确允许共享的缓存题", options)
		input["cache_scope"] = "shared"
		before := fork.calls.Load()
		assertQuery(t, query(t, user, csrf, web.NewID(), input), "answered", 150, true)
		assertQuery(t, query(t, other, otherCSRF, web.NewID(), input), "cache_hit", 0, false)
		if fork.calls.Load() != before+1 {
			t.Fatal("已授权共享缓存仍按用户重复请求模型")
		}
		assertWallet(t, userID, "994")
		assertWallet(t, otherID, "999")
	})

	// 面板路由和真实缓存有效期也作为回归断言，防止接口通过而适配退回。
	report := map[string]any{"environment": "开发维护隔离环境", "upstream": "本地TLS模型夹具", "panel_routes": map[string]int{}}
	for _, path := range []string{"/panel/", "/panel/app.js", "/panel/styles.css"} {
		request, _ := http.NewRequest(http.MethodGet, fork.url+path, nil)
		request.Header.Set("Authorization", "Bearer "+directForkToken)
		response, err := user.Do(request)
		if err != nil {
			t.Fatal("面板实际路由探测失败", err)
		}
		_ = response.Body.Close()
		report["panel_routes"].(map[string]int)[path] = response.StatusCode
		t.Logf("正式 fork 面板路由：GET %s -> %d", path, response.StatusCode)
		if response.StatusCode != http.StatusOK {
			t.Fatal("正式维护面板不可用", path, response.StatusCode)
		}
	}
	current := query(t, user, csrf, web.NewID(), body)
	entryID := current["cache"].(map[string]any)["entry_id"]
	var cacheHours float64
	if err := fork.db.Pool().QueryRow(ctx, `SELECT EXTRACT(EPOCH FROM (expires_at-created_at))/3600 FROM tiku.cache_entries WHERE id=$1`, entryID).Scan(&cacheHours); err != nil {
		t.Fatal("无法读取真实 fork 缓存有效期", err)
	}
	if cacheHours < 23.99 || cacheHours > 24.01 {
		t.Fatal("模型公开缓存期限与 fork 实际期限不符", cacheHours)
	}
	report["cache_policy"] = map[string]any{"published_ttl_days": 1, "fork_actual_ttl_hours": cacheHours, "matches": true}
	t.Logf("模型公开缓存有效期为1天，真实 fork 缓存有效期为 %.2f 小时", cacheHours)
	if script := os.Getenv("BYEBYEOCS_TIKU_PANEL_PROBE_SCRIPT"); script != "" {
		probe := exec.Command("node", script, fork.url)
		if output, err := probe.CombinedOutput(); err != nil {
			t.Fatalf("真实题库面板浏览器探测失败：%v\n%s", err, output)
		} else {
			t.Logf("真实题库面板浏览器探测：%s", output)
		}
	}
	report["upstream_calls"] = fork.calls.Load()
	report["model_credentials_isolated"] = fork.badSecret.Load() == 0
	if outputPath := os.Getenv("BYEBYEOCS_TIKU_ADAPTATION_REPORT_FILE"); outputPath != "" {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil || os.WriteFile(outputPath, data, 0600) != nil {
			t.Fatal("无法保存题库适配审计证据")
		}
	}
}

func TestTikuForkAutomaticWorkAdaptation(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		name := "五种题型自动提交"
		if unknown {
			name = "提交回执丢失后独立复核"
		}
		t.Run(name, func(t *testing.T) {
			fork := newDirectForkFixture(t, false)
			fork.quiz.Store(true)
			forkClient, err := questions.NewHTTPFork(fork.url, directForkToken, 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			platform := &automaticWorkPlatform{unknown: unknown}
			fixture := newRuntimeFixture(t, fixtureOptions{fork: forkClient, platform: platform, executor: automaticVideoFallback{}, charging: true})
			user, admin := fixtureClient(), fixtureClient()
			defer user.CloseIdleConnections()
			defer admin.CloseIdleConnections()
			csrf, userID := loginFixtureUser(t, fixture, user, "learner-a")
			code, _, login := requestJSON(t, admin, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{
				"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP(),
			}, nil)
			if code != http.StatusOK {
				t.Fatal("无法登录自动测验联调管理员", code)
			}
			actor := web.Actor{ID: login["admin"].(map[string]any)["id"].(string), Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
			modelID := fork.seedOffering(t, "独立题库自动测验模型", "primary", "user", 7,
				&questions.OfferingPricing{Version: "automatic-fork-v1", InputMicrosPerMillionTokens: 1000000,
					OutputMicrosPerMillionTokens: 2000000, MicrosPerMinor: 10000, Rounding: "ceil",
					Rule: "tokens_x_micros_per_million_v1"}, false)
			ctx := context.Background()
			wallet, err := fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.runtime.Ledger.AdjustWallet(ctx, actor, web.NewID(), ledger.WalletAdjustmentInput{
				WalletID: wallet.ID, Direction: "credit", AmountMinor: "1000", Reason: "独立题库自动测验合成资金",
			}); err != nil {
				t.Fatal(err)
			}
			enabled := true
			if _, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, 1, ledger.BillingPatch{TikuChargeEnabled: &enabled}); err != nil {
				t.Fatal(err)
			}
			fork.setBefore(func() bool {
				var held int64
				return fixture.runtime.db.Pool().QueryRow(ctx, "SELECT reserved_minor FROM public.wallets WHERE user_id=$1", userID).Scan(&held) == nil && held > 0
			})
			code, _, _ = requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, map[string]string{"Idempotency-Key": web.NewID()})
			if code != http.StatusAccepted {
				t.Fatal("自动测验课程同步未受理", code)
			}
			var courseID string
			waitAutomatic(t, 8*time.Second, func() bool {
				return fixture.runtime.db.Pool().QueryRow(ctx, "SELECT id::text FROM public.learning_courses WHERE user_id=$1", userID).Scan(&courseID) == nil
			})
			code, _, plan := requestJSON(t, user, "POST", fixture.user.URL+"/api/v1/plans", csrf, map[string]any{
				"name": "独立题库自动测验安排", "course_ids": []string{courseID}, "scope": map[string]string{"kind": "all"}, "task_types": []string{"quiz"},
				"question_policy": map[string]any{"enabled": true, "model_offering_id": modelID, "answer_mode": "always_llm"},
			}, nil)
			if code != http.StatusCreated || plan["question_policy"].(map[string]any)["require_manual_confirmation"] != false {
				t.Fatal("自动测验安排未就绪或出现了人工确认", code, plan)
			}
			var state string
			waitAutomatic(t, 14*time.Second, func() bool {
				return fixture.runtime.db.Pool().QueryRow(ctx, "SELECT state FROM public.learning_tasks WHERE user_id=$1", userID).Scan(&state) == nil && state == learning.StateConfirmed
			})
			wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil || wallet.AvailableMinor != "995" || wallet.ReservedMinor != "0" || fork.calls.Load() != 5 || platform.posts.Load() != 1 || platform.invalid.Load() || fork.badHold.Load() != 0 {
				t.Fatalf("真实 fork 自动测验不符：state=%s wallet=%+v 模型请求=%d 最终提交=%d 错误提交=%t 缺少预占=%d err=%v",
					state, wallet, fork.calls.Load(), platform.posts.Load(), platform.invalid.Load(), fork.badHold.Load(), err)
			}
			var queryCount, totalTokens int64
			if err := fixture.runtime.db.Pool().QueryRow(ctx, "SELECT count(*), COALESCE(sum((usage->>'total_tokens')::bigint),0) FROM public.question_queries WHERE user_id=$1", userID).Scan(&queryCount, &totalTokens); err != nil || queryCount != 5 || totalTokens != 750 {
				t.Fatal("自动测验题库用量事实不完整", queryCount, totalTokens, err)
			}
		})
	}
}
