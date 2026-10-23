//go:build integration

package ledger

import (
	"context"
	"testing"
	"time"
)

// 订单列表只关闭当前页到期的未支付订单：任意用户读页不再扫描并更新全库订单。
// 未出现在本页或属于其他用户的过期订单保持原状态，由各自的查询/读取路径处理。
func TestListOrdersClosesOnlyPageRows(t *testing.T) {
	service, pool, userA, _, _, clock := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	userB := newUser(t, pool)
	orderA := createOrder(t, service, userA, "expiry-page-a", 3000)
	clock.Advance(time.Second)
	orderB := createOrder(t, service, userB, "expiry-page-b", 3000)
	clock.Advance(time.Second)
	latestA := createOrder(t, service, userA, "expiry-page-a2", 3000)
	clock.Advance(31 * time.Minute)

	page, err := service.ListUserRechargeOrders(ctx, userA, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != latestA.ID {
		t.Fatalf("订单列表首项错误: %+v", page.Items)
	}
	if page.Items[0].Status != "closed" || page.Items[0].ClosedAt == "" {
		t.Fatalf("当前页的过期订单未关闭: %+v", page.Items[0])
	}
	status := func(id string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT status FROM public.recharge_orders WHERE id = $1`, id).
			Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	if status(latestA.ID) != "closed" {
		t.Fatal("当前页过期订单未在数据库关闭")
	}
	if status(orderA.ID) != "pending_payment" {
		t.Fatalf("未出现在本页的过期订单被读请求关闭: %s", status(orderA.ID))
	}
	if status(orderB.ID) != "pending_payment" {
		t.Fatalf("其他用户的过期订单被读请求关闭: %s", status(orderB.ID))
	}
}

// 读取列表不再顺手关闭全库订单后，迟到付款仍按供应商真实付款事实入账，并标记为迟到。
func TestLatePaymentCreditsWithoutReadSweep(t *testing.T) {
	service, pool, userID, _, _, clock := setupService(t)
	ctx := context.Background()
	enableCheckout(t, pool)
	createOrder(t, service, userID, "expiry-late-direct", 3000)
	clock.Advance(31 * time.Minute)
	if _, err := service.ListUserRechargeOrders(ctx, userID, "", 10); err != nil {
		t.Fatal(err)
	}
	// 上一步只检查当前页（包含该订单）会关闭它；这里用另一个未读取的订单验证迟到入账。
	pending := createOrder(t, service, userID, "expiry-late-unread", 4000)
	clock.Advance(31 * time.Minute)
	if err := service.processPayment(ctx, paidFact(pending, "trade-late-unread", 4000)); err != nil {
		t.Fatal(err)
	}
	var status, creditStatus string
	var late bool
	if err := pool.QueryRow(ctx, `SELECT status, credit_status, late_payment
		FROM public.recharge_orders WHERE id = $1`, pending.ID).
		Scan(&status, &creditStatus, &late); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || creditStatus != "credited" || !late {
		t.Fatalf("迟到付款未按供应商事实入账: status=%s credit=%s late=%v", status, creditStatus, late)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "4000" {
		t.Fatalf("迟到付款未到账: %+v %v", wallet, err)
	}
}
