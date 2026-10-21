//go:build integration

package ledger

import (
	"context"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *testClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

type fakeProvider struct {
	mu           sync.Mutex
	checkout     bool
	refund       bool
	refundStatus string
	refundErr    error
	queryPaid    bool
	queryAmount  int64
	queryChannel string
	queryTradeNo string
	queryErr     error
	refundCalls  int
	notification *payments.Notification
	verifyErr    error
	checkoutURL  string
}

func (f *fakeProvider) Name() string { return "zpay" }
func (f *fakeProvider) Capabilities() payments.Capabilities {
	f.mu.Lock()
	defer f.mu.Unlock()
	return payments.Capabilities{Checkout: f.checkout, Query: true, Refund: f.refund}
}
func (f *fakeProvider) CheckoutURL(payments.CheckoutRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkoutURL != "" {
		return f.checkoutURL, nil
	}
	return "https://zpayz.cn/submit.php?test=1", nil
}
func (f *fakeProvider) QueryOrder(context.Context, payments.QueryRequest) (payments.QueryResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return payments.QueryResult{Paid: f.queryPaid, AmountMinor: f.queryAmount,
		Channel: f.queryChannel, ProviderTradeNo: f.queryTradeNo,
		ProviderStatus: "TRADE_SUCCESS"}, f.queryErr
}
func (f *fakeProvider) Refund(context.Context, payments.RefundRequest) (payments.RefundResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refundCalls++
	return payments.RefundResult{Status: f.refundStatus}, f.refundErr
}
func (f *fakeProvider) VerifyNotification(url.Values) (payments.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.verifyErr != nil {
		return payments.Notification{}, f.verifyErr
	}
	if f.notification == nil {
		return payments.Notification{}, nil
	}
	return *f.notification, nil
}

func setupService(t *testing.T) (*Service, *pgxpool.Pool, string, string, *fakeProvider, *testClock) {
	t.Helper()
	pool := testsupport.NewPool(t)
	clock := &testClock{at: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	userID := newUser(t, pool)
	adminID := newAdmin(t, pool)
	provider := &fakeProvider{checkout: true}
	service, err := New(pool, Options{Provider: provider, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	return service, pool, userID, adminID, provider, clock
}

func newUser(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.users (id) VALUES ($1)`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func newAdmin(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	id := web.NewID()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.admin_users (id, username, password_hash)
		VALUES ($1, $2, 'synthetic')`, id, "admin-"+id[:8]); err != nil {
		t.Fatal(err)
	}
	return id
}

func adminActor(service *Service, adminID string) web.Actor {
	return web.Actor{Kind: "admin", ID: adminID, Permissions: []string{"*"}, AuthenticatedAt: service.now()}
}

func userActor(id string) web.Actor {
	return web.Actor{Kind: "user", ID: id, AuthenticatedAt: time.Now()}
}

func fundedWallet(t *testing.T, service *Service, actor web.Actor, userID string, amount int64) WalletSnapshot {
	t.Helper()
	wallet, err := service.GetWallet(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if amount > 0 {
		if _, err := service.AdjustWallet(context.Background(), actor, "fund-"+web.NewID()[:8],
			WalletAdjustmentInput{WalletID: wallet.ID, Direction: "credit",
				AmountMinor: formatAmount(amount), Reason: "测试资金准备"}); err != nil {
			t.Fatal(err)
		}
		wallet, err = service.GetWallet(context.Background(), userID)
		if err != nil {
			t.Fatal(err)
		}
	}
	return wallet
}

func createOrder(t *testing.T, service *Service, userID string, key string, amount int64) *RechargeOrder {
	t.Helper()
	order, err := service.CreateRechargeOrder(context.Background(), userActor(userID), key, amount, "alipay")
	if err != nil {
		t.Fatal(err)
	}
	return order
}

func enableCheckout(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE public.billing_settings
		SET zpay_checkout_enabled = true`); err != nil {
		t.Fatal(err)
	}
}

func enableRefund(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE public.billing_settings
		SET zpay_refund_enabled = true`); err != nil {
		t.Fatal(err)
	}
}

func publishServicePrice(t *testing.T, service *Service, admin web.Actor, priceMicros int64) {
	t.Helper()
	precision := 0
	rounding := "floor"
	patch := BillingPatch{
		ServiceChargeEnabled: boolPtr(true),
		ServicePrice: &ServicePricePatch{
			PriceMicros: formatAmount(priceMicros), UnitPrecision: &precision,
			SettlementRule: "per_event_min_charge", Rounding: rounding, MinChargeMinor: "0",
		},
	}
	current, err := service.GetBillingSettings(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBillingSettings(context.Background(), admin, current.Version, patch); err != nil {
		t.Fatal(err)
	}
}

func publishTikuPrice(t *testing.T, service *Service, admin web.Actor, input, output, threshold int64) {
	t.Helper()
	patch := BillingPatch{
		TikuChargeEnabled: boolPtr(true),
		TikuPrice: &TikuPricePatch{
			InputMicrosPerMillion: formatAmount(input), OutputMicrosPerMillion: formatAmount(output),
			SettlementRule: "accumulate_threshold", SettlementThresholdMicros: formatAmount(threshold),
			Rounding: "floor",
		},
	}
	current, err := service.GetBillingSettings(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateBillingSettings(context.Background(), admin, current.Version, patch); err != nil {
		t.Fatal(err)
	}
}

func boolPtr(value bool) *bool { return &value }

func addDisplayIdentity(t *testing.T, pool *pgxpool.Pool, userID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO public.external_identities (
		id, user_id, provider, namespace, subject_index, subject_ciphertext,
		subject_key_version, display_name, status, verified_at
	) VALUES ($1,$2,'chaoxing','test',$3,$4,1,$5,'active',now())`,
		web.NewID(), userID, digest("identity", userID), make([]byte, 32), name); err != nil {
		t.Fatal(err)
	}
}
