//go:build integration

package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 管理端首次配置价格时默认提交 ceil；真实 PATCH 接口必须能发布该价格，
// 并以向上取整计算最小货币单位，而不是只在直接调用服务方法时成功。
func TestHTTPFirstPublishServicePriceWithCeil(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	mux := http.NewServeMux()
	service.Register(mux)
	admin := adminActor(service, adminID)

	getRequest := httptest.NewRequest(http.MethodGet, "/admin/api/v1/settings/billing", nil)
	getRequest = getRequest.WithContext(web.WithActor(getRequest.Context(), admin))
	getRecorder := httptest.NewRecorder()
	mux.ServeHTTP(getRecorder, getRequest)
	if getRecorder.Code != http.StatusOK {
		t.Fatalf("读取计费设置失败: %d %s", getRecorder.Code, getRecorder.Body.String())
	}
	etag := getRecorder.Header().Get("ETag")
	if etag == "" {
		t.Fatal("读取计费设置缺少 ETag")
	}

	patchBody, err := json.Marshal(map[string]any{
		"service_charge_enabled": true,
		"service_price": map[string]any{
			"price_micros":     "15000",
			"unit_precision":   0,
			"settlement_rule":  "per_event",
			"rounding":         "ceil",
			"min_charge_minor": "0",
			"unit_label":       "有效任务点",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	patchRequest := httptest.NewRequest(http.MethodPatch, "/admin/api/v1/settings/billing", bytes.NewReader(patchBody))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.Header.Set("If-Match", etag)
	patchRequest = patchRequest.WithContext(web.WithActor(patchRequest.Context(), admin))
	patchRecorder := httptest.NewRecorder()
	mux.ServeHTTP(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("首次发布 ceil 价格失败: %d %s", patchRecorder.Code, patchRecorder.Body.String())
	}
	var view BillingSettingsView
	if err := json.Unmarshal(patchRecorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Service.Configured || view.Service.Rounding != "ceil" ||
		view.Service.PriceMicros != "15000" || view.Service.State != "active" {
		t.Fatalf("ceil 价格响应错误: %+v", view.Service)
	}
	var stored string
	if err := pool.QueryRow(context.Background(), `SELECT rounding FROM public.price_versions
		WHERE scope = 'service_task_point' AND status = 'published'`).Scan(&stored); err != nil || stored != "ceil" {
		t.Fatalf("数据库未保存 ceil 价格: %s %v", stored, err)
	}

	quote, err := service.QuoteServiceTaskPoint(context.Background(), userID, "ceil-first-publish", 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if quote.State != "quoted" || !quote.Chargeable || quote.ChargeMinor != "2" {
		t.Fatalf("ceil 价格报价错误: %+v", quote)
	}
}
