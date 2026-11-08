package questions

import (
	"net/http"
	"testing"
)

func TestInterruptedStoredQueryDoesNotBecomeRetryableServiceFailure(t *testing.T) {
	for _, code := range []string{"abandoned", "released_unknown_usage"} {
		t.Run(code, func(t *testing.T) {
			err := storedQueryError(code)
			if err.Code != "request_unknown" || err.Status != http.StatusConflict || err.Retryable {
				t.Fatalf("中断事实被错误地当作暂时故障: %+v", err)
			}
			if preCallFailure(&queryRecord{Status: "failed", ErrorCode: code, BillingState: "automatic_charging_disabled"}) {
				t.Fatal("未知模型用量不能自动重新调用")
			}
		})
	}
}
