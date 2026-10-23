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

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 新流程在预占提交后保持 'requested'，只有独立事务转为 'submitted' 后才调用渠道。
// 因此已提交的 'requested' 一定能证明渠道请求没有发出；相同幂等键的重试必须退回资金
// 并结束退款，不能永久占用用户余额与退款应付账户。
func TestInterruptedRefundReturnsFundsOnSameKeyRetry(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order := createOrder(t, service, userID, "refund-interrupt-order", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-refund-interrupt", 3000)); err != nil {
		t.Fatal(err)
	}
	service.testHook = func(stage string) error {
		if stage == "before_refund_submit" {
			return fault.New("service_unavailable", "模拟提交渠道前中断")
		}
		return nil
	}
	input := RefundInput{OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "3000",
		Reason: "中断恢复测试全额退回"}
	if _, err := service.CreateRefund(ctx, admin, "refund-interrupt", input); err == nil {
		t.Fatal("模拟中断未生效")
	}
	service.testHook = nil

	// 中断后：预占已提交但退款仍是 'requested'，渠道一次都没有被调用。
	interrupted := lookupRefund(t, pool, admin.ID, "refund-interrupt")
	if interrupted.Status != "requested" {
		t.Fatalf("中断后退款状态为 %s，期望 requested", interrupted.Status)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "0" {
		t.Fatalf("中断后钱包状态错误: %+v %v", wallet, err)
	}
	if payable := refundPayableTotal(t, pool); payable != 3000 {
		t.Fatalf("中断后退款应付账户金额为 %d，期望 3000", payable)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("中断前调用了渠道退款 %d 次", calls)
	}

	replayed, err := service.CreateRefund(ctx, admin, "refund-interrupt", input)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Status != "failed" || replayed.LastErrorCode != "refund_not_submitted" ||
		replayed.CompletedAt == "" {
		t.Fatalf("未提交的退款重试未安全结束: %+v", replayed)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("未提交的退款被自动重发 %d 次", calls)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("退款未提交但资金未退回: %+v %v", wallet, err)
	}
	if payable := refundPayableTotal(t, pool); payable != 0 {
		t.Fatalf("退款应付账户未清: %d", payable)
	}

	// 终态后再次重放保持终态，不重复退钱、不重发渠道请求。
	again, err := service.CreateRefund(ctx, admin, "refund-interrupt", input)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != replayed.ID || again.Status != "failed" {
		t.Fatalf("终态退款重放结果异常: %+v", again)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("重复重放重复退款: %+v %v", wallet, err)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("终态重放调用了渠道退款 %d 次", calls)
	}
}

// 旧版本在 'reserved' 状态就直接调用渠道，因此旧 'reserved' 不能推断“未发送”。
// 相同幂等键重放必须保持待核对，不能自动退回资金（可能已经汇出）。
func TestLegacyReservedRefundIsNotAutoReleased(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order, refund := prepareLegacyReservedRefund(t, service, pool, admin, userID, "refund-legacy", 1000)
	_ = order
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "2000" {
		t.Fatalf("旧 reserved 退款资金状态错误: %+v %v", wallet, err)
	}
	input := RefundInput{OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000",
		Reason: "旧状态兼容测试"}
	_, err := service.CreateRefund(ctx, admin, "refund-legacy", input)
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("旧 reserved 退款被自动处理: %v", err)
	}
	if refund.Status != "reserved" {
		t.Fatalf("旧 reserved 退款状态被改写: %+v", refund)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "2000" {
		t.Fatalf("旧 reserved 退款重放移动了资金: %+v %v", wallet, err)
	}
	if payable := refundPayableTotal(t, pool); payable != 1000 {
		t.Fatalf("旧 reserved 退款应付账户被冲销: %d", payable)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("旧 reserved 退款重放调用了渠道 %d 次", calls)
	}
}

// 管理员人工复核旧 reserved 退款为渠道已退款：只复用既有结算分录，不调用渠道，
// 不依赖退款开关，并记录审核事实。
func TestRefundReviewResolvesLegacyReservedWithoutProvider(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "succeeded"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order, refund := prepareLegacyReservedRefund(t, service, pool, admin, userID, "refund-review", 3000)
	// 复核入口不依赖退款开关。
	if _, err := pool.Exec(ctx, `UPDATE public.billing_settings SET zpay_refund_enabled = false`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveRefund(ctx, userActor(userID), "review-forbidden", refund.ID,
		RefundReviewInput{Outcome: "succeeded", Reason: "越权复核测试"}); err == nil {
		t.Fatal("普通用户可复核退款")
	}
	stale := adminActor(service, adminID)
	stale.AuthenticatedAt = stale.AuthenticatedAt.Add(-11 * time.Minute)
	if _, err := service.ResolveRefund(ctx, stale, "review-stale", refund.ID,
		RefundReviewInput{Outcome: "succeeded", Reason: "过期验证复核测试"}); err == nil {
		t.Fatal("过期验证的管理员可复核退款")
	}

	resolved, err := service.ResolveRefund(ctx, admin, "review-paid", refund.ID,
		RefundReviewInput{Outcome: "succeeded", Reason: "已核对渠道退款记录"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "succeeded" || resolved.CompletedAt == "" {
		t.Fatalf("人工复核未确认退款成功: %+v", resolved)
	}
	if calls := providerRefundCalls(provider); calls != 0 {
		t.Fatalf("人工复核调用了渠道退款 %d 次", calls)
	}
	if payable := refundPayableTotal(t, pool); payable != 0 {
		t.Fatalf("复核成功后退款应付账户未清: %d", payable)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "0" {
		t.Fatalf("复核成功后的钱包状态错误: %+v %v", wallet, err)
	}
	updated, err := service.GetRechargeOrder(ctx, userActor(userID), order.ID)
	if err != nil || updated.PaymentStatus != "refunded" {
		t.Fatalf("整笔复核退款后订单状态错误: %+v %v", updated, err)
	}
	if actions := auditCount(t, pool, "refund.reviewed"); actions != 1 {
		t.Fatalf("退款复核审计数量为 %d，期望 1", actions)
	}

	// 相同结果幂等返回，不重复记账、不追加审核事实。
	if _, err := service.ResolveRefund(ctx, admin, "review-paid", refund.ID,
		RefundReviewInput{Outcome: "succeeded", Reason: "已核对渠道退款记录"}); err != nil {
		t.Fatal(err)
	}
	if journals := countJournals(t, pool, "refund-payout:"+refund.ID); journals != 1 {
		t.Fatalf("退款成功分录数量为 %d，期望 1", journals)
	}
	if actions := auditCount(t, pool, "refund.reviewed"); actions != 1 {
		t.Fatalf("幂等重放追加了审核审计: %d", actions)
	}
	if _, err := service.ResolveRefund(ctx, admin, "review-other", refund.ID,
		RefundReviewInput{Outcome: "failed", Reason: "重复复核冲突测试"}); err == nil {
		t.Fatal("已成功退款被复核为失败")
	}
}

// 管理员核实渠道未退款时，复用既有释放事务把资金退回钱包。
func TestRefundReviewNotPaidReturnsFunds(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "unknown"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order := createOrder(t, service, userID, "refund-review-not-paid", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-review-not-paid", 3000)); err != nil {
		t.Fatal(err)
	}
	refund, err := service.CreateRefund(ctx, admin, "refund-review-not-paid", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道结果不明待核对",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "unknown" {
		t.Fatalf("退款状态错误: %+v", refund)
	}
	calls := providerRefundCalls(provider)
	resolved, err := service.ResolveRefund(ctx, admin, "review-not-paid", refund.ID,
		RefundReviewInput{Outcome: "failed", Reason: "渠道确认未退款，资金退回"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "failed" || resolved.CompletedAt == "" {
		t.Fatalf("人工复核未退回资金: %+v", resolved)
	}
	if providerRefundCalls(provider) != calls {
		t.Fatal("人工复核重发了渠道退款")
	}
	if payable := refundPayableTotal(t, pool); payable != 0 {
		t.Fatalf("复核未退款后应付账户未清: %d", payable)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("复核未退款后资金未退回: %+v %v", wallet, err)
	}
}

// 复核路由要求 Idempotency-Key，并在成功时返回退款记录。
func TestRefundReviewRouteRequiresIdempotencyKey(t *testing.T) {
	service, pool, _, adminID, provider, _ := setupService(t)
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	_, refund := prepareLegacyReservedRefund(t, service, pool, admin, newUser(t, pool), "refund-review-route", 1000)
	mux := http.NewServeMux()
	service.RegisterAdmin(mux)
	body := []byte(`{"outcome":"succeeded","reason":"已核对渠道退款记录"}`)
	request := httptest.NewRequest(http.MethodPost, "/admin/api/v1/refunds/"+refund.ID+"/resolve",
		bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(web.WithActor(request.Context(), admin))
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusPreconditionRequired {
		t.Fatalf("缺少 Idempotency-Key 返回 %d：%s", recorder.Code, recorder.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/admin/api/v1/refunds/"+refund.ID+"/resolve",
		bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "review-http")
	request = request.WithContext(web.WithActor(request.Context(), admin))
	recorder = httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("退款复核返回 %d：%s", recorder.Code, recorder.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["status"] != "succeeded" {
		t.Fatalf("退款复核响应错误: %v", decoded)
	}
}

// 结果不明且未经人工核实的退款不得自动结算或自动重发。
func TestUnknownRefundIsNotAutoResolved(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.refundStatus = "unknown"
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	order := createOrder(t, service, userID, "refund-unknown-order", 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-refund-unknown", 3000)); err != nil {
		t.Fatal(err)
	}
	refund, err := service.CreateRefund(ctx, admin, "refund-unknown", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道结果不明待核对",
	})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != "unknown" {
		t.Fatalf("结果不明退款状态错误: %+v", refund)
	}
	calls := providerRefundCalls(provider)

	// 已终态或结果不明都不能通过自动路径重新提交或确认打款；预占资金保留在退款应付账户等待核对。
	replayed, err := service.CreateRefund(ctx, admin, "refund-unknown", RefundInput{
		OrderID: order.ID, Kind: "abnormal_payment", AmountMinor: "1000", Reason: "渠道结果不明待核对",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != refund.ID || replayed.Status != "unknown" {
		t.Fatalf("结果不明退款重放结果异常: %+v", replayed)
	}
	if providerRefundCalls(provider) != calls {
		t.Fatal("结果不明退款被自动重发")
	}
	if payable := refundPayableTotal(t, pool); payable != 1000 {
		t.Fatalf("结果不明退款的应付金额被自动冲销: %d", payable)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "2000" {
		t.Fatalf("结果不明退款资金状态错误: %+v %v", wallet, err)
	}
}

// prepareLegacyReservedRefund 构造旧版本可能已调用渠道的 'reserved' 退款事实：
// 先制造新流程“预占已提交、请求未开始”的中断，再把状态改成旧版本的 'reserved'。
func prepareLegacyReservedRefund(t *testing.T, service *Service, pool *pgxpool.Pool,
	admin web.Actor, userID, key string, amount int64) (*RechargeOrder, *Refund) {
	t.Helper()
	ctx := context.Background()
	order := createOrder(t, service, userID, "order-"+key, 3000)
	if err := service.processPayment(ctx, paidFact(order, "trade-"+key, 3000)); err != nil {
		t.Fatal(err)
	}
	service.testHook = func(stage string) error {
		if stage == "before_refund_submit" {
			return fault.New("service_unavailable", "模拟提交渠道前中断")
		}
		return nil
	}
	_, err := service.CreateRefund(ctx, admin, key, RefundInput{OrderID: order.ID,
		Kind: "abnormal_payment", AmountMinor: formatAmount(amount), Reason: "旧状态兼容测试"})
	service.testHook = nil
	if err == nil {
		t.Fatal("模拟中断未生效")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.refunds SET status = 'reserved'
		WHERE requested_by = $1 AND idempotency_key = $2`, admin.ID, key); err != nil {
		t.Fatal(err)
	}
	return order, lookupRefund(t, pool, admin.ID, key)
}

func lookupRefund(t *testing.T, pool *pgxpool.Pool, adminID, key string) *Refund {
	t.Helper()
	refund, err := scanRefund(pool.QueryRow(context.Background(), refundSelect+
		` WHERE requested_by = $1 AND idempotency_key = $2`, adminID, key))
	if err != nil {
		t.Fatal(err)
	}
	return refund
}

func refundPayableTotal(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var payable int64
	if err := pool.QueryRow(context.Background(), `SELECT coalesce(sum(p.amount_minor)
		FILTER (WHERE a.kind = 'refund_payable'), 0)::bigint
		FROM public.postings p JOIN public.ledger_accounts a ON a.id = p.account_id`).Scan(&payable); err != nil {
		t.Fatal(err)
	}
	return payable
}

func providerRefundCalls(provider *fakeProvider) int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.refundCalls
}

func auditCount(t *testing.T, pool *pgxpool.Pool, action string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.audit_events WHERE action = $1`, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func countJournals(t *testing.T, pool *pgxpool.Pool, businessKey string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.journals WHERE business_key = $1`, businessKey).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// 'submitted' 可能仍处于渠道调用期限内，过早复核会与在途请求产生两种资金事实。
// 超过调用期限后，管理员可按核实结果结束退款。
func TestRefundReviewRejectsInFlightSubmission(t *testing.T) {
	service, pool, userID, adminID, provider, _ := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	enableRefund(t, pool)
	provider.mu.Lock()
	provider.refund = true
	provider.mu.Unlock()
	admin := adminActor(service, adminID)
	_, refund := prepareLegacyReservedRefund(t, service, pool, admin, userID, "refund-review-inflight", 1000)
	if _, err := pool.Exec(ctx, `UPDATE public.refunds SET status = 'submitted', updated_at = $2
		WHERE id = $1`, refund.ID, service.now()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ResolveRefund(ctx, admin, "review-inflight", refund.ID,
		RefundReviewInput{Outcome: "failed", Reason: "在途提交期内复核测试"}); err == nil {
		t.Fatal("在途提交期内允许人工复核")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.refunds SET updated_at = $2 WHERE id = $1`,
		refund.ID, service.now().Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.ResolveRefund(ctx, admin, "review-inflight", refund.ID,
		RefundReviewInput{Outcome: "failed", Reason: "渠道确认未退款，资金退回"})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "failed" {
		t.Fatalf("过期提交状态未被复核结束: %+v", resolved)
	}
	if wallet, err := service.GetWallet(ctx, userID); err != nil || wallet.AvailableMinor != "3000" {
		t.Fatalf("复核未退款后资金未退回: %+v %v", wallet, err)
	}
}
