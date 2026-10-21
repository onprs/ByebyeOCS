//go:build integration

package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestManualAdjustmentRequiresFreshAdminPermissionAndReason(t *testing.T) {
	service, pool, userID, adminID, _, clock := setupService(t)
	ctx := context.Background()
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	stale := adminActor(service, adminID)
	stale.AuthenticatedAt = clock.Now().Add(-10 * time.Minute)
	if _, err := service.AdjustWallet(ctx, stale, "stale", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "100", Reason: "测试人工调整",
	}); err == nil {
		t.Fatal("过期管理员验证仍可调整")
	}
	user := userActor(userID)
	if _, err := service.AdjustWallet(ctx, user, "user", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "100", Reason: "测试人工调整",
	}); err == nil {
		t.Fatal("普通用户可调整余额")
	}
	admin := adminActor(service, adminID)
	if _, err := service.AdjustWallet(ctx, admin, "short-reason", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "100", Reason: "短",
	}); err == nil {
		t.Fatal("过短理由被接受")
	}
	result, err := service.AdjustWallet(ctx, admin, "adjust-1", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "500", Reason: "客服核对后补记",
		Reference: "ticket-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Wallet.AvailableMinor != "500" {
		t.Fatalf("人工增加余额错误: %+v", result)
	}
	if _, err := service.AdjustWallet(ctx, admin, "adjust-1", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "600", Reason: "客服核对后补记",
	}); err == nil {
		t.Fatal("相同幂等键不同调整未冲突")
	} else if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("调整幂等冲突错误: %v", err)
	}
	if _, err := service.AdjustWallet(ctx, admin, "adjust-overdraft", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "debit", AmountMinor: "600", Reason: "测试透支扣减",
	}); err == nil {
		t.Fatal("人工扣减造成透支")
	}
	if _, err := service.AdjustWallet(ctx, admin, "adjust-2", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "debit", AmountMinor: "200", Reason: "客服核对后扣减",
	}); err != nil {
		t.Fatal(err)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "300" {
		t.Fatalf("人工调整后余额错误: %+v", wallet)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.audit_events WHERE action = 'wallet.adjusted'`).Scan(&audits); err != nil || audits != 2 {
		t.Fatalf("人工调整审计数量错误: %d,%v", audits, err)
	}
}

func TestReconciliationFreezesAndUnfreezes(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 1000)
	if _, err := pool.Exec(ctx, `UPDATE public.wallets SET available_minor = available_minor + 1 WHERE id = $1`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	record, err := service.CreateReconciliation(ctx, admin, "recon-1", ReconciliationInput{
		Kind: "wallet_rebuild", WalletID: wallet.ID, Reason: "人工核对缓存与账本",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != "mismatch" {
		t.Fatalf("不一致钱包未标记: %+v", record)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !wallet.Frozen {
		t.Fatalf("不一致钱包未冻结: %+v", wallet)
	}
	if _, err := service.AdjustWallet(ctx, admin, "frozen-adjust", WalletAdjustmentInput{
		WalletID: wallet.ID, Direction: "credit", AmountMinor: "100", Reason: "冻结时测试调整",
	}); err == nil {
		t.Fatal("冻结钱包仍可人工调整")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.wallets SET available_minor = available_minor - 1 WHERE id = $1`, wallet.ID); err != nil {
		t.Fatal(err)
	}
	unfrozen, err := service.CreateReconciliation(ctx, admin, "recon-2", ReconciliationInput{
		Kind: "wallet_unfreeze", WalletID: wallet.ID, Reason: "修正缓存后复核解除冻结",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unfrozen.Status != "matched" {
		t.Fatalf("复核未通过: %+v", unfrozen)
	}
	wallet, err = service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.Frozen {
		t.Fatalf("钱包未解除冻结: %+v", wallet)
	}
}

func TestHTTPBillingETagAndPermissions(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	mux := http.NewServeMux()
	service.Register(mux)
	admin := adminActor(service, adminID)
	getRequest := httptest.NewRequest(http.MethodGet, "/admin/api/v1/settings/billing", nil)
	getRequest = getRequest.WithContext(web.WithActor(getRequest.Context(), admin))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, getRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("读取计费设置失败: %d %s", recorder.Code, recorder.Body.String())
	}
	etag := recorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("缺少 ETag")
	}
	body := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	version := int64(body["version"].(float64))
	patchBody := []byte(`{"service_charge_enabled":true}`)
	patchRequest := httptest.NewRequest(http.MethodPatch, "/admin/api/v1/settings/billing", bytes.NewReader(patchBody))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest = patchRequest.WithContext(web.WithActor(patchRequest.Context(), admin))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, patchRequest)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("缺少 If-Match 未返回 428: %d", recorder.Code)
	}
	patchRequest = httptest.NewRequest(http.MethodPatch, "/admin/api/v1/settings/billing", bytes.NewReader(patchBody))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.Header.Set("If-Match", `"v`+formatAmount(version+1)+`"`)
	patchRequest = patchRequest.WithContext(web.WithActor(patchRequest.Context(), admin))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, patchRequest)
	if recorder.Code != http.StatusPreconditionFailed {
		t.Fatalf("过期版本未返回 412: %d %s", recorder.Code, recorder.Body.String())
	}
	patchRequest = httptest.NewRequest(http.MethodPatch, "/admin/api/v1/settings/billing", bytes.NewReader(patchBody))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.Header.Set("If-Match", etag)
	patchRequest = patchRequest.WithContext(web.WithActor(patchRequest.Context(), admin))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, patchRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("有效版本更新失败: %d %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("ETag") == etag {
		t.Fatal("更新后 ETag 未变化")
	}
	// 普通用户不能读取管理员计费设置；未认证用户不能读取钱包。
	user := userActor(userID)
	request := httptest.NewRequest(http.MethodGet, "/admin/api/v1/settings/billing", nil)
	request = request.WithContext(web.WithActor(request.Context(), user))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("普通用户读取管理员设置未返回 403: %d", recorder.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/wallet", nil)
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("未认证钱包请求未返回 401: %d", recorder.Code)
	}
}
