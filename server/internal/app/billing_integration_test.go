//go:build integration

package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type flowPlatform struct {
	fixturePlatform
	passed atomic.Bool
}

func (p *flowPlatform) CheckTask(context.Context, []byte, learning.PlatformTaskRef) (learning.PlatformCheck, error) {
	status := learning.CheckUnconfirmed
	if p.passed.Load() {
		status = learning.CheckConfirmed
	}
	return learning.PlatformCheck{Status: status, Passed: p.passed.Load(), CheckedAt: time.Now().UTC(), Source: "controlled-integration-fixture"}, nil
}

type flowExecutor struct {
	platform         *flowPlatform
	started, release chan struct{}
}

func (e *flowExecutor) Capabilities() learning.ExecutorCapabilities {
	return learning.ExecutorCapabilities{Name: "controlled-integration-fixture", Enabled: true, TaskKinds: []string{"video"}, SafeLeaseRecovery: true}
}
func (e *flowExecutor) Execute(ctx context.Context, request learning.ExecutionRequest) (learning.ExecutionResult, error) {
	select {
	case e.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return learning.ExecutionResult{}, learning.Fail(learning.FailureStopped, "测试执行停止")
	case <-e.release:
	}
	e.platform.passed.Store(true)
	return learning.ExecutionResult{Completed: true, Observation: learning.Observation{Source: "controlled-integration-fixture", ObservedAt: time.Now().UTC()}}, nil
}

func TestTaskLedgerUsesOneConfirmedPointAndFrozenPrice(t *testing.T) {
	for _, kind := range []string{"paid", "unpriced", "disabled", "zero"} {
		t.Run(kind, func(t *testing.T) {
			platform := &flowPlatform{}
			executor := &flowExecutor{platform: platform, started: make(chan struct{}, 1), release: make(chan struct{})}
			fixture := newRuntimeFixture(t, fixtureOptions{platform: platform, executor: executor, charging: true})
			ctx := context.Background()
			client := fixtureClient()
			defer client.CloseIdleConnections()
			csrf, userID := loginFixtureUser(t, fixture, client, "learner-a")
			var adminID string
			if fixture.runtime.db.Pool().QueryRow(ctx, "SELECT id::text FROM public.admin_users WHERE username=$1", fixtureAdminName).Scan(&adminID) != nil {
				t.Fatal("测试管理员不存在")
			}
			actor := web.Actor{ID: adminID, Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
			wallet, err := fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = fixture.runtime.Ledger.AdjustWallet(ctx, actor, "flow-funds", ledger.WalletAdjustmentInput{WalletID: wallet.ID, Direction: "credit", AmountMinor: "1000", Reason: "仅用于隔离集成测试的合成资金"}); err != nil {
				t.Fatal(err)
			}
			on := true
			off := false
			precision := 2
			patch := ledger.BillingPatch{ServiceChargeEnabled: &on}
			if kind == "disabled" {
				patch.ServiceChargeEnabled = &off
			}
			if kind != "unpriced" {
				rate := "125000"
				if kind == "zero" {
					rate = "0"
				}
				patch.ServicePrice = &ledger.ServicePricePatch{PriceMicros: rate, UnitPrecision: &precision, SettlementRule: "per_event", Rounding: "half_up", MinChargeMinor: "0", UnitLabel: "有效任务点"}
			}
			settings, err := fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, 1, patch)
			if err != nil {
				t.Fatal(err)
			}
			code, _, _ := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, nil)
			if code != 202 {
				t.Fatal("课程同步失败")
			}
			var courseID string
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				_, _, body := requestJSON(t, client, "GET", fixture.user.URL+"/api/v1/courses", "", nil, nil)
				if items, ok := body["items"].([]any); ok && len(items) > 0 {
					courseID = items[0].(map[string]any)["id"].(string)
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if courseID == "" {
				t.Fatal("课程未同步")
			}
			code, _, body := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/plans", csrf, map[string]any{"name": "计量组合测试", "course_ids": []string{courseID}, "scope": map[string]string{"kind": "all"}, "task_types": []string{"video"}, "priority": 1}, nil)
			if code != 201 {
				t.Fatal("安排未创建", code, body)
			}
			select {
			case <-executor.started:
			case <-time.After(5 * time.Second):
				t.Fatal("测试执行未开始")
			}
			// 执行中发布新价格；旧的未定价、已关闭、零价和付费尝试均不能被追溯改价。
			_, err = fixture.runtime.Ledger.UpdateBillingSettings(ctx, actor, settings.Version, ledger.BillingPatch{ServiceChargeEnabled: &on, ServicePrice: &ledger.ServicePricePatch{PriceMicros: "250000", UnitPrecision: &precision, SettlementRule: "per_event", Rounding: "half_up", MinChargeMinor: "0", UnitLabel: "有效任务点"}})
			if err != nil {
				t.Fatal(err)
			}
			close(executor.release)
			var taskID, state, billingState string
			deadline = time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				err = fixture.runtime.db.Pool().QueryRow(ctx, "SELECT id::text,state,billing_state FROM public.learning_tasks WHERE user_id=$1", userID).Scan(&taskID, &state, &billingState)
				if err == nil && state == "confirmed" {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			if state != "confirmed" {
				t.Fatal("任务没有获得平台确认", state, billingState)
			}
			var count int
			var charged, quantity int64
			var usageState string
			if err = fixture.runtime.db.Pool().QueryRow(ctx, "SELECT count(*),max(status),max(quantity_scaled),max(COALESCE(charged_minor,0)) FROM public.usage_records WHERE user_id=$1", userID).Scan(&count, &usageState, &quantity, &charged); err != nil || count != 1 {
				t.Fatal("平台确认没有唯一计量事实", err, count, billingState)
			}
			expected := int64(0)
			if kind == "paid" {
				expected = 13
				if quantity != 100 || billingState != "settled" {
					t.Fatal("一个完整任务点没有按原始精度结算", quantity, billingState)
				}
			}
			if charged != expected {
				t.Fatal("确认用量被错误追价或扣费", kind, charged, usageState, billingState)
			}
			if kind == "zero" && (usageState != "settled" || billingState != "settled") {
				t.Fatal("明确零价任务没有形成终态", usageState, billingState)
			}
			if kind == "unpriced" && usageState != "unpriced" {
				t.Fatal("未定价被误当免费或新价格")
			}
			wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
			if err != nil {
				t.Fatal(err)
			}
			want := "1000"
			if kind == "paid" {
				want = "987"
			}
			if wallet.AvailableMinor != want || wallet.ReservedMinor != "0" {
				t.Fatal("预占没有正确闭合", wallet.AvailableMinor, wallet.ReservedMinor, billingState)
			}
			tx, err := fixture.runtime.db.Pool().Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := (&taskBilling{book: fixture.runtime.Ledger}).RecordConfirmed(ctx, tx, userID, taskID); err != nil {
				tx.Rollback(ctx)
				t.Fatal("重复确认不幂等", err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
