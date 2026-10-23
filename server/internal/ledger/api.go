package ledger

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// Register 注册用户、管理员与支付回调的完整路径。
// 正式入口应优先使用 RegisterUser/RegisterAdmin 将两端挂到不同监听。
func (s *Service) Register(mux *http.ServeMux) {
	s.RegisterUser(mux)
	s.RegisterAdmin(mux)
}

func (s *Service) RegisterUser(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/wallet", s.handleWallet)
	mux.HandleFunc("GET /api/v1/wallet/entries", s.handleUserWalletEntries)
	mux.HandleFunc("GET /api/v1/pricing", s.handlePricing)
	mux.HandleFunc("GET /api/v1/usage", s.handleUsage)
	mux.HandleFunc("GET /api/v1/recharge-orders", s.handleUserOrders)
	mux.HandleFunc("POST /api/v1/recharge-orders", s.handleCreateOrder)
	mux.HandleFunc("GET /api/v1/recharge-orders/{id}", s.handleGetOrder)
	mux.HandleFunc("/payments/callbacks/zpay", s.handleZPayCallback)
}

func (s *Service) RegisterAdmin(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/v1/orders", s.handleAdminOrders)
	mux.HandleFunc("GET /admin/api/v1/users/{id}/wallet", s.handleAdminUserWallet)
	mux.HandleFunc("GET /admin/api/v1/wallets", s.handleAdminWallets)
	mux.HandleFunc("GET /admin/api/v1/wallets/{id}/entries", s.handleAdminWalletEntries)
	mux.HandleFunc("GET /admin/api/v1/refunds", s.handleAdminRefunds)
	mux.HandleFunc("GET /admin/api/v1/finance/summary", s.handleFinanceSummary)
	mux.HandleFunc("POST /admin/api/v1/wallet-adjustments", s.handleWalletAdjustment)
	mux.HandleFunc("GET /admin/api/v1/reconciliations", s.handleListReconciliations)
	mux.HandleFunc("POST /admin/api/v1/reconciliations", s.handleCreateReconciliation)
	mux.HandleFunc("POST /admin/api/v1/refunds", s.handleCreateRefund)
	mux.HandleFunc("POST /admin/api/v1/refunds/{id}/resolve", s.handleResolveRefund)
	mux.HandleFunc("GET /admin/api/v1/settings/billing", s.handleGetBillingSettings)
	mux.HandleFunc("PATCH /admin/api/v1/settings/billing", s.handlePatchBillingSettings)
}

func requireUser(w http.ResponseWriter, r *http.Request) (web.Actor, bool) {
	return web.Require(w, r, "user", "")
}

func requireAdmin(w http.ResponseWriter, r *http.Request, permission string) (web.Actor, bool) {
	return web.Require(w, r, "admin", permission)
}

func (s *Service) pageLimit(r *http.Request) (int, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return s.pageSize, nil
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		return 0, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	return limit, nil
}

func idempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		web.Error(w, r, 428, "precondition_required", "此操作需要 Idempotency-Key")
		return "", false
	}
	if len(key) > 180 {
		web.Error(w, r, 422, "validation_failed", "Idempotency-Key 超出限制")
		return "", false
	}
	return key, true
}

func (s *Service) handleWallet(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	wallet, err := s.GetWallet(r.Context(), actor.ID)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, wallet)
}

func (s *Service) handleUserWalletEntries(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	wallet, err := s.GetWallet(r.Context(), actor.ID)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	page, err := s.ListWalletEntries(r.Context(), wallet.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	for index := range page.Items {
		switch page.Items[index].Kind {
		case "manual_credit", "manual_debit":
			page.Items[index].Reason = "人工余额调整"
		case "refund_reserve", "refund_payout", "refund_return":
			page.Items[index].Reason = "异常资金退款处理"
		}
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handlePricing(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUser(w, r); !ok {
		return
	}
	view, err := s.GetPricing(r.Context())
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, view)
}

func (s *Service) handleUsage(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	page, err := s.ListUsage(r.Context(), actor.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

type createOrderRequest struct {
	AmountMinor string `json:"amount_minor"`
	Channel     string `json:"channel"`
}

func (s *Service) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var request createOrderRequest
	if !web.DecodeLimit(w, r, &request, 64*1024) {
		return
	}
	amount, err := parseAmount(request.AmountMinor)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	order, err := s.CreateRechargeOrder(r.Context(), actor, key, amount, request.Channel)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusCreated, order)
}

func (s *Service) handleUserOrders(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	page, err := s.ListUserRechargeOrders(r.Context(), actor.ID, r.URL.Query().Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireUser(w, r)
	if !ok {
		return
	}
	order, err := s.GetRechargeOrder(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, order)
}

func (s *Service) handleAdminOrders(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	query := r.URL.Query()
	page, err := s.ListAdminOrders(r.Context(), actor, query.Get("status"),
		query.Get("payment_status"), query.Get("credit_status"), query.Get("user_id"),
		query.Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleAdminWalletEntries(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	result, err := s.AdminWalletEntries(r.Context(), actor, r.PathValue("id"),
		r.URL.Query().Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, result)
}

type adjustmentRequest struct {
	WalletID    string `json:"wallet_id"`
	Direction   string `json:"direction"`
	AmountMinor string `json:"amount_minor"`
	Reason      string `json:"reason"`
	Reference   string `json:"reference"`
}

func (s *Service) handleWalletAdjustment(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermWalletAdjust)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var request adjustmentRequest
	if !web.DecodeLimit(w, r, &request, 64*1024) {
		return
	}
	result, err := s.AdjustWallet(r.Context(), actor, key, WalletAdjustmentInput{
		WalletID: request.WalletID, Direction: request.Direction, AmountMinor: request.AmountMinor,
		Reason: request.Reason, Reference: request.Reference,
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, result)
}

type reconciliationRequest struct {
	Kind     string `json:"kind"`
	WalletID string `json:"wallet_id"`
	UserID   string `json:"user_id"`
	OrderID  string `json:"order_id"`
	Reason   string `json:"reason"`
}

func (s *Service) handleCreateReconciliation(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermReconcile)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var request reconciliationRequest
	if !web.DecodeLimit(w, r, &request, 64*1024) {
		return
	}
	record, err := s.CreateReconciliation(r.Context(), actor, key, ReconciliationInput{
		Kind: request.Kind, WalletID: request.WalletID, UserID: request.UserID,
		OrderID: request.OrderID, Reason: request.Reason,
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusCreated, record)
}

func (s *Service) handleListReconciliations(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermReconcile)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	query := r.URL.Query()
	page, err := s.ListReconciliations(r.Context(), actor, query.Get("status"),
		query.Get("kind"), query.Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleAdminUserWallet(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	result, err := s.AdminUserWallet(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, result)
}

func (s *Service) handleAdminWallets(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	page, err := s.ListAdminWallets(r.Context(), actor, r.URL.Query().Get("search"),
		r.URL.Query().Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleAdminRefunds(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	limit, err := s.pageLimit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	query := r.URL.Query()
	page, err := s.ListAdminRefunds(r.Context(), actor, query.Get("status"),
		query.Get("order_id"), query.Get("user_id"), query.Get("cursor"), limit)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func parseOptionalTime(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, fault.New("validation_failed", "时间必须为 UTC RFC3339 格式")
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func (s *Service) handleFinanceSummary(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	from, err := parseOptionalTime(r.URL.Query().Get("from"))
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	to, err := parseOptionalTime(r.URL.Query().Get("to"))
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	summary, err := s.FinanceSummary(r.Context(), actor, from, to)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, summary)
}

type refundRequest struct {
	OrderID     string `json:"order_id"`
	Kind        string `json:"kind"`
	AmountMinor string `json:"amount_minor"`
	Reason      string `json:"reason"`
}

func (s *Service) handleCreateRefund(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermWalletRefund)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var request refundRequest
	if !web.DecodeLimit(w, r, &request, 64*1024) {
		return
	}
	refund, err := s.CreateRefund(r.Context(), actor, key, RefundInput{
		OrderID: request.OrderID, Kind: request.Kind,
		AmountMinor: request.AmountMinor, Reason: request.Reason,
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusCreated, refund)
}

type refundReviewRequest struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

// handleResolveRefund 记录管理员人工复核的退款结果；不调用渠道，不依赖退款开关。
func (s *Service) handleResolveRefund(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermWalletRefund)
	if !ok {
		return
	}
	key, ok := idempotencyKey(w, r)
	if !ok {
		return
	}
	var request refundReviewRequest
	if !web.DecodeLimit(w, r, &request, 64*1024) {
		return
	}
	refund, err := s.ResolveRefund(r.Context(), actor, key, r.PathValue("id"),
		RefundReviewInput{Outcome: request.Outcome, Reason: request.Reason})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, refund)
}

func (s *Service) handleGetBillingSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingRead)
	if !ok {
		return
	}
	view, err := s.GetBillingSettings(r.Context(), actor)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.ETag(w, view.Version)
	web.JSON(w, r, http.StatusOK, view)
}

func (s *Service) handlePatchBillingSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := requireAdmin(w, r, PermBillingManage)
	if !ok {
		return
	}
	version, err := web.MatchVersion(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	var patch BillingPatch
	if !web.DecodeLimit(w, r, &patch, 64*1024) {
		return
	}
	view, err := s.UpdateBillingSettings(r.Context(), actor, version, patch)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	web.ETag(w, view.Version)
	web.JSON(w, r, http.StatusOK, view)
}

func (s *Service) handleZPayCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(r.URL.RawQuery) > 4096 {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if err := s.ProcessZPayCallback(r.Context(), r.Form); err != nil {
		code, _ := fault.Describe(err)
		switch code {
		case "not_configured", "service_unavailable", "provider_unavailable", "external_disabled":
			http.Error(w, "retry", http.StatusServiceUnavailable)
		default:
			http.Error(w, "invalid", http.StatusBadRequest)
		}
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "success")
}
