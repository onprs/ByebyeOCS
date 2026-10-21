package ledger

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type RechargeOrder struct {
	ID              string `json:"id"`
	UserID          string `json:"user_id,omitempty"`
	WalletID        string `json:"wallet_id,omitempty"`
	AmountMinor     string `json:"amount_minor"`
	Currency        string `json:"currency"`
	Channel         string `json:"channel"`
	Status          string `json:"status"`
	PaymentStatus   string `json:"payment_status"`
	CreditStatus    string `json:"credit_status"`
	Provider        string `json:"provider"`
	MerchantOrderNo string `json:"merchant_order_no"`
	ProviderTradeNo string `json:"provider_trade_no,omitempty"`
	LatePayment     bool   `json:"late_payment"`
	CreatedAt       string `json:"created_at"`
	createdAtValue  time.Time
	UpdatedAt       string `json:"updated_at"`
	ExpiresAt       string `json:"expires_at"`
	expiresAtValue  time.Time
	PaidAt          string `json:"paid_at,omitempty"`
	CreditedAt      string `json:"credited_at,omitempty"`
	ClosedAt        string `json:"closed_at,omitempty"`
	Version         int64  `json:"version"`
	CheckoutURL     string `json:"checkout_url,omitempty"`
	CheckoutState   string `json:"checkout_state"`
}

type RechargeOrderPage struct {
	Items      []RechargeOrder `json:"items"`
	NextCursor string          `json:"next_cursor"`
}

const orderSelect = `SELECT id, user_id, wallet_id, amount_minor, currency, channel, status,
	payment_status, credit_status, provider, merchant_order_no, coalesce(provider_trade_no, ''),
	late_payment, created_at, updated_at, expires_at, paid_at, credited_at, closed_at, version
	FROM public.recharge_orders`

func scanOrder(row pgx.Row) (*RechargeOrder, error) {
	var order RechargeOrder
	var amount int64
	var paidAt, creditedAt, closedAt pgtype.Timestamptz
	var createdAt, updatedAt, expiresAt time.Time
	if err := row.Scan(&order.ID, &order.UserID, &order.WalletID, &amount, &order.Currency,
		&order.Channel, &order.Status, &order.PaymentStatus, &order.CreditStatus, &order.Provider,
		&order.MerchantOrderNo, &order.ProviderTradeNo, &order.LatePayment,
		&createdAt, &updatedAt, &expiresAt, &paidAt, &creditedAt, &closedAt, &order.Version); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "充值订单不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取充值订单")
	}
	order.AmountMinor = formatAmount(amount)
	order.CreatedAt = utcTime(createdAt)
	order.createdAtValue = createdAt
	order.UpdatedAt = utcTime(updatedAt)
	order.ExpiresAt = utcTime(expiresAt)
	order.expiresAtValue = expiresAt
	if paidAt.Valid {
		order.PaidAt = utcTime(paidAt.Time)
	}
	if creditedAt.Valid {
		order.CreditedAt = utcTime(creditedAt.Time)
	}
	if closedAt.Valid {
		order.ClosedAt = utcTime(closedAt.Time)
	}
	order.CheckoutState = "not_available"
	return &order, nil
}

func (s *Service) checkoutReady(settings billingSettings) bool {
	return s.chargingAuthorized() && s.provider != nil && s.provider.Name() == "zpay" &&
		s.provider.Capabilities().Checkout && settings.ZPayCheckoutEnabled
}

func (s *Service) checkoutURL(ctx context.Context, order *RechargeOrder, settings billingSettings) string {
	if order.Status != "pending_payment" || order.PaymentStatus != "unpaid" {
		order.CheckoutState = "not_available"
		return ""
	}
	if !s.checkoutReady(settings) {
		order.CheckoutState = "not_configured"
		return ""
	}
	endpoint, err := s.provider.CheckoutURL(payments.CheckoutRequest{
		MerchantOrderNo: order.MerchantOrderNo, AmountMinor: mustAmount(order.AmountMinor),
		Channel: order.Channel,
	})
	if err != nil {
		order.CheckoutState = "not_configured"
		return ""
	}
	order.CheckoutState = "available"
	return endpoint
}

func mustAmount(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func (s *Service) CreateRechargeOrder(ctx context.Context, actor web.Actor, idempotencyKey string,
	amountMinor int64, channel string) (*RechargeOrder, error) {
	if err := requireAgent(actor, "user", ""); err != nil {
		return nil, err
	}
	if err := validKey(idempotencyKey); err != nil {
		return nil, fault.New("validation_failed", "缺少有效的幂等键")
	}
	if amountMinor <= 0 || amountMinor > maxAmountMinor {
		return nil, fault.New("validation_failed", "充值金额必须为正且不超过上限")
	}
	if channel != ChannelAlipay && channel != ChannelWxpay {
		return nil, fault.New("validation_failed", "不支持的支付方式")
	}
	requestDigest := digest("recharge_order", actor.ID, formatAmount(amountMinor), channel)
	var result *RechargeOrder
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		settings, err := s.loadBillingSettings(ctx, tx)
		if err != nil {
			return err
		}
		if err := s.advisoryLocks(ctx, tx, "order:"+actor.ID+":"+idempotencyKey); err != nil {
			return err
		}
		existing, err := scanOrder(tx.QueryRow(ctx, orderSelect+
			` WHERE user_id = $1 AND idempotency_key = $2`, actor.ID, idempotencyKey))
		if err == nil {
			var storedDigest string
			if queryErr := tx.QueryRow(ctx, `SELECT request_digest FROM public.recharge_orders
				WHERE id = $1`, existing.ID).Scan(&storedDigest); queryErr != nil {
				return fault.New("service_unavailable", "无法核对订单幂等键")
			}
			if storedDigest != requestDigest {
				return fault.New("conflict", "相同幂等键已用于不同充值请求")
			}
			if existing.Status == "pending_payment" && existing.PaymentStatus == "unpaid" &&
				!s.now().Before(existing.expiresAtValue) {
				now := s.now()
				tag, err := tx.Exec(ctx, `UPDATE public.recharge_orders SET status = 'closed',
					closed_at = $2, updated_at = $2, version = version + 1
					WHERE id = $1 AND status = 'pending_payment' AND payment_status = 'unpaid'`,
					existing.ID, now)
				if err != nil {
					return fault.New("service_unavailable", "无法关闭超时订单")
				}
				if tag.RowsAffected() == 1 {
					existing.Status = "closed"
					existing.ClosedAt = utcTime(now)
					existing.Version++
				} else {
					refreshed, readErr := scanOrder(tx.QueryRow(ctx, orderSelect+` WHERE id = $1`, existing.ID))
					if readErr != nil {
						return readErr
					}
					existing = refreshed
				}
			}
			existing.CheckoutURL = s.checkoutURL(ctx, existing, settings)
			result = existing
			return nil
		}
		if code, _ := fault.Describe(err); code != "not_found" {
			return err
		}
		if !s.checkoutReady(settings) {
			return fault.New("not_configured", "充值支付尚未配置或运营未启用")
		}
		if amountMinor < settings.MinRechargeMinor || amountMinor > settings.MaxRechargeMinor {
			return fault.New("validation_failed", "充值金额超出当前允许范围")
		}
		if err := s.checkChargingGate(ctx); err != nil {
			return err
		}
		wallet, err := s.ensureWallet(ctx, tx, actor.ID)
		if err != nil {
			return err
		}
		if wallet.Frozen {
			return fault.New("conflict", "钱包已冻结，请先完成对账")
		}
		now := s.now()
		var order *RechargeOrder
		for attempt := 0; attempt < 3; attempt++ {
			merchantOrderNo, err := newMerchantOrderNo(now)
			if err != nil {
				return err
			}
			orderID := newUUID()
			expiresAt := now.Add(30 * time.Minute)
			_, insertErr := tx.Exec(ctx, `INSERT INTO public.recharge_orders (
				id, user_id, wallet_id, amount_minor, currency, channel, status, payment_status,
				credit_status, provider, merchant_order_no, idempotency_key, request_digest,
				created_at, updated_at, expires_at, version
			) VALUES ($1,$2,$3,$4,'CNY',$5,'pending_payment','unpaid','not_credited','zpay',$6,$7,$8,$9,$9,$10,1)`,
				orderID, actor.ID, wallet.ID, amountMinor, channel, merchantOrderNo,
				idempotencyKey, requestDigest, now, expiresAt)
			if insertErr != nil {
				if isUniqueViolation(insertErr) {
					continue
				}
				return fault.New("service_unavailable", "无法创建充值订单")
			}
			if _, err := tx.Exec(ctx, `INSERT INTO public.payment_attempts (
				id, order_id, provider, amount_minor, channel, status, created_at, updated_at
			) VALUES ($1,$2,'zpay',$3,$4,'pending',$5,$5)`,
				newUUID(), orderID, amountMinor, channel, now); err != nil {
				return fault.New("service_unavailable", "无法创建支付尝试")
			}
			order = &RechargeOrder{
				ID: orderID, UserID: actor.ID, WalletID: wallet.ID,
				AmountMinor: formatAmount(amountMinor), Currency: CurrencyCNY, Channel: channel,
				Status: "pending_payment", PaymentStatus: "unpaid", CreditStatus: "not_credited",
				Provider: "zpay", MerchantOrderNo: merchantOrderNo,
				CreatedAt: utcTime(now), UpdatedAt: utcTime(now), ExpiresAt: utcTime(expiresAt),
				expiresAtValue: expiresAt, Version: 1,
			}
			order.CheckoutURL = s.checkoutURL(ctx, order, settings)
			if order.CheckoutURL == "" {
				return fault.New("not_configured", "无法生成 ZPay 收银入口")
			}
			result = order
			return nil
		}
		return fault.New("service_unavailable", "无法生成唯一支付订单号")
	})
	return result, err
}

func (s *Service) GetRechargeOrder(ctx context.Context, actor web.Actor, orderID string) (*RechargeOrder, error) {
	if err := requireAgent(actor, "user", ""); err != nil {
		return nil, err
	}
	if !web.ValidID(orderID) {
		return nil, fault.New("validation_failed", "订单编号无效")
	}
	order, err := scanOrder(s.pool.QueryRow(ctx, orderSelect+
		` WHERE id = $1 AND user_id = $2`, orderID, actor.ID))
	if err != nil {
		return nil, err
	}
	if order.Status == "pending_payment" && order.PaymentStatus == "unpaid" &&
		!s.now().Before(order.expiresAtValue) {
		now := s.now()
		if err := s.inTx(ctx, func(tx pgx.Tx) error {
			tag, err := tx.Exec(ctx, `UPDATE public.recharge_orders SET status = 'closed',
				closed_at = $2, updated_at = $2, version = version + 1
				WHERE id = $1 AND status = 'pending_payment' AND payment_status = 'unpaid'`,
				order.ID, now)
			if err != nil {
				return fault.New("service_unavailable", "无法关闭超时订单")
			}
			if tag.RowsAffected() == 1 {
				order.Status = "closed"
				order.ClosedAt = utcTime(now)
				order.Version++
			} else {
				refreshed, readErr := scanOrder(tx.QueryRow(ctx, orderSelect+` WHERE id = $1`, order.ID))
				if readErr != nil {
					return readErr
				}
				*order = *refreshed
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	settings, err := s.loadBillingSettings(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	order.CheckoutURL = s.checkoutURL(ctx, order, settings)
	return order, nil
}

func (s *Service) ListUserRechargeOrders(ctx context.Context, userID, cursor string, limit int) (RechargeOrderPage, error) {
	if !web.ValidID(userID) {
		return RechargeOrderPage{}, fault.New("validation_failed", "用户编号无效")
	}
	return s.listOrders(ctx, "user_id = $1", []any{userID}, cursor, limit)
}

func (s *Service) ListAdminOrders(ctx context.Context, actor web.Actor, status, paymentStatus, creditStatus,
	userID, cursor string, limit int) (RechargeOrderPage, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return RechargeOrderPage{}, err
	}
	where := []string{"true"}
	args := []any{}
	if status != "" {
		if status != "pending_payment" && status != "completed" && status != "closed" && status != "needs_review" {
			return RechargeOrderPage{}, fault.New("validation_failed", "订单状态筛选无效")
		}
		args = append(args, status)
		where = append(where, "status = $"+itoa(len(args)))
	}
	if paymentStatus != "" {
		if paymentStatus != "unpaid" && paymentStatus != "paid" && paymentStatus != "failed" &&
			paymentStatus != "unknown" && paymentStatus != "refunded" {
			return RechargeOrderPage{}, fault.New("validation_failed", "支付状态筛选无效")
		}
		args = append(args, paymentStatus)
		where = append(where, "payment_status = $"+itoa(len(args)))
	}
	if creditStatus != "" {
		if creditStatus != "not_credited" && creditStatus != "credited" && creditStatus != "needs_review" {
			return RechargeOrderPage{}, fault.New("validation_failed", "到账状态筛选无效")
		}
		args = append(args, creditStatus)
		where = append(where, "credit_status = $"+itoa(len(args)))
	}
	if userID != "" {
		if !web.ValidID(userID) {
			return RechargeOrderPage{}, fault.New("validation_failed", "用户编号无效")
		}
		args = append(args, userID)
		where = append(where, "user_id = $"+itoa(len(args)))
	}
	return s.listOrders(ctx, joinAnd(where), args, cursor, limit)
}

func joinAnd(parts []string) string {
	result := ""
	for i, part := range parts {
		if i > 0 {
			result += " AND "
		}
		result += part
	}
	return result
}

func (s *Service) listOrders(ctx context.Context, where string, args []any, cursor string, limit int) (RechargeOrderPage, error) {
	if limit < 1 || limit > 100 {
		return RechargeOrderPage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	if _, err := s.pool.Exec(ctx, `UPDATE public.recharge_orders SET status = 'closed',
		closed_at = $1, updated_at = $1, version = version + 1
		WHERE status = 'pending_payment' AND payment_status = 'unpaid' AND expires_at < $1`, s.now()); err != nil {
		return RechargeOrderPage{}, fault.New("service_unavailable", "无法关闭超时订单")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return RechargeOrderPage{}, err
	}
	if decoded.id != "" {
		args = append(args, decoded.createdAt, decoded.id)
		where += " AND (created_at < $" + itoa(len(args)-1) + " OR (created_at = $" +
			itoa(len(args)-1) + " AND id < $" + itoa(len(args)) + "))"
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, orderSelect+` WHERE `+where+`
		ORDER BY created_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return RechargeOrderPage{}, fault.New("service_unavailable", "无法读取充值订单")
	}
	defer rows.Close()
	items := make([]RechargeOrder, 0, limit+1)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return RechargeOrderPage{}, err
		}
		items = append(items, *order)
	}
	if err := rows.Err(); err != nil {
		return RechargeOrderPage{}, fault.New("service_unavailable", "无法读取充值订单")
	}
	page := RechargeOrderPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAtValue, id: last.ID})
	}
	return page, nil
}

func mustTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339, value)
	return parsed
}

type paymentFact struct {
	MerchantOrderNo string
	ProviderTradeNo string
	AmountMinor     int64
	Channel         string
	ProviderStatus  string
	Source          string
}

func (s *Service) ProcessZPayCallback(ctx context.Context, values url.Values) error {
	if s.provider == nil || s.provider.Name() != "zpay" {
		return fault.New("not_configured", "ZPay 支付尚未配置")
	}
	notification, err := s.provider.VerifyNotification(values)
	if err != nil {
		return err
	}
	fact := paymentFact{
		MerchantOrderNo: notification.MerchantOrderNo, ProviderTradeNo: notification.ProviderTradeNo,
		AmountMinor: notification.AmountMinor, Channel: notification.Channel,
		ProviderStatus: notification.ProviderStatus, Source: "callback",
	}
	if !notification.Paid {
		return s.recordUnpaidNotification(ctx, fact)
	}
	return s.processPayment(ctx, fact)
}

func inboxKeys(fact paymentFact) (string, string) {
	dedupe := digest("zpay", fact.MerchantOrderNo, fact.ProviderTradeNo,
		formatAmount(fact.AmountMinor), fact.ProviderStatus, fact.Channel)
	payload := digest(fact.MerchantOrderNo, fact.ProviderTradeNo,
		formatAmount(fact.AmountMinor), fact.ProviderStatus, fact.Channel)
	return dedupe, payload
}

func (s *Service) recordUnpaidNotification(ctx context.Context, fact paymentFact) error {
	status := "ignored"
	reason := ""
	if fact.ProviderStatus != "WAIT_BUYER_PAY" && fact.ProviderStatus != "TRADE_CLOSED" {
		status = "needs_review"
		reason = "unrecognized_trade_status"
	}
	dedupeKey, payloadDigest := inboxKeys(fact)
	return s.inTx(ctx, func(tx pgx.Tx) error {
		inboxID := newUUID()
		var orderID string
		_ = tx.QueryRow(ctx, `SELECT id::text FROM public.recharge_orders WHERE merchant_order_no = $1`,
			fact.MerchantOrderNo).Scan(&orderID)
		var inserted int
		err := tx.QueryRow(ctx, `INSERT INTO public.payment_inbox (
			id, provider, source, dedupe_key, payload_digest, order_id, merchant_order_no,
			provider_trade_no, amount_minor, channel, provider_status, status, verified,
			reason_code, received_at, processed_at
		) VALUES ($1,'zpay',$2,$3,$4,$5,$6,$7,$8,$9,$10,true,$11,$12,$12)
		ON CONFLICT (dedupe_key) DO NOTHING RETURNING 1`, inboxID, fact.Source, dedupeKey,
			payloadDigest, nullableID(orderID), fact.MerchantOrderNo, fact.ProviderTradeNo,
			fact.AmountMinor, fact.Channel, fact.ProviderStatus, status, nullableText(reason), s.now()).Scan(&inserted)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return fault.New("service_unavailable", "无法持久化支付通知")
		}
		if status == "needs_review" {
			if orderID != "" {
				if err := s.markOrderReview(ctx, tx, orderID); err != nil {
					return err
				}
			}
			return s.audit(ctx, tx, actorFromID("system", ""), "payment.review_required",
				"payment_inbox", inboxID, "支付通知状态无法识别，已隔离待核对")
		}
		return nil
	})
}

func (s *Service) processPayment(ctx context.Context, fact paymentFact) error {
	dedupeKey, payloadDigest := inboxKeys(fact)
	return s.inTx(ctx, func(tx pgx.Tx) error {
		inboxID := newUUID()
		var inserted int
		err := tx.QueryRow(ctx, `INSERT INTO public.payment_inbox (
			id, provider, source, dedupe_key, payload_digest, merchant_order_no, provider_trade_no,
			amount_minor, channel, provider_status, status, verified, received_at
		) VALUES ($1,'zpay',$2,$3,$4,$5,$6,$7,$8,$9,'received',true,$10)
		ON CONFLICT (dedupe_key) DO NOTHING RETURNING 1`,
			inboxID, fact.Source, dedupeKey, payloadDigest, fact.MerchantOrderNo,
			fact.ProviderTradeNo, fact.AmountMinor, fact.Channel, fact.ProviderStatus, s.now()).Scan(&inserted)
		if err == pgx.ErrNoRows {
			var status string
			if queryErr := tx.QueryRow(ctx, `SELECT status FROM public.payment_inbox WHERE dedupe_key = $1`,
				dedupeKey).Scan(&status); queryErr != nil {
				return fault.New("service_unavailable", "无法读取支付通知状态")
			}
			if status == "processed" || status == "ignored" || status == "needs_review" {
				return nil
			}
			if queryErr := tx.QueryRow(ctx, `SELECT id FROM public.payment_inbox WHERE dedupe_key = $1`,
				dedupeKey).Scan(&inboxID); queryErr != nil {
				return fault.New("service_unavailable", "无法读取支付通知")
			}
		} else if err != nil {
			return fault.New("service_unavailable", "无法持久化支付通知")
		}
		if err := s.runHook("after_inbox"); err != nil {
			return err
		}
		order, err := scanOrder(tx.QueryRow(ctx, orderSelect+
			` WHERE merchant_order_no = $1 FOR UPDATE`, fact.MerchantOrderNo))
		if err != nil {
			if code, _ := fault.Describe(err); code == "not_found" {
				return s.markInboxReview(ctx, tx, inboxID, "", "order_not_found", "支付通知找不到本地订单")
			}
			return err
		}
		markReview := func(reason string, message string) error {
			return s.markInboxReview(ctx, tx, inboxID, order.ID, reason, message)
		}
		if order.Provider != "zpay" {
			return markReview("provider_mismatch", "支付通知提供商与本地订单不符")
		}
		if order.AmountMinor != formatAmount(fact.AmountMinor) {
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return markReview("amount_mismatch", "支付通知金额与本地订单不符")
		}
		if order.Channel != fact.Channel {
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return markReview("channel_mismatch", "支付通知支付方式与本地订单不符")
		}
		if order.CreditStatus == "credited" {
			if order.ProviderTradeNo == fact.ProviderTradeNo {
				if _, err := tx.Exec(ctx, `UPDATE public.payment_inbox SET status = 'processed',
					order_id = $2, processed_at = $3 WHERE id = $1`, inboxID, order.ID, s.now()); err != nil {
					return fault.New("service_unavailable", "无法确认重复支付通知")
				}
				return nil
			}
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return markReview("duplicate_payment", "同一本地订单出现第二笔已支付交易")
		}
		if order.PaymentStatus == "paid" && order.ProviderTradeNo != "" &&
			order.ProviderTradeNo != fact.ProviderTradeNo {
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return markReview("duplicate_payment", "本地订单已有不同的渠道交易号")
		}
		var otherOrder string
		err = tx.QueryRow(ctx, `SELECT id FROM public.recharge_orders
			WHERE provider = 'zpay' AND provider_trade_no = $1 AND id <> $2 LIMIT 1`,
			fact.ProviderTradeNo, order.ID).Scan(&otherOrder)
		if err == nil {
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return markReview("duplicate_trade_no", "渠道交易号已被其他订单使用")
		}
		if err != pgx.ErrNoRows {
			return fault.New("service_unavailable", "无法核对渠道交易号")
		}
		if err := s.runHook("before_credit"); err != nil {
			return err
		}
		wallet, err := s.lockWalletByID(ctx, tx, order.WalletID)
		if err != nil {
			return err
		}
		if err := s.creditFunds(ctx, tx, wallet, fact.AmountMinor); err != nil {
			return err
		}
		journalID, reused, err := s.postJournal(ctx, tx, journalInput{
			Kind: "recharge", BusinessKey: "recharge:" + order.ID,
			RequestDigest: digest("recharge", order.ID, fact.ProviderTradeNo, formatAmount(fact.AmountMinor)),
			UserID:        order.UserID, WalletID: order.WalletID, AmountMinor: fact.AmountMinor,
			Reason: "ZPay 充值到账", Actor: actorFromID("system", ""), OrderID: order.ID,
			Postings: []postingInput{
				{AccountID: wallet.AvailableAccountID, AmountMinor: fact.AmountMinor},
				{AccountID: systemChannelClearing, AmountMinor: -fact.AmountMinor},
			},
		})
		if err != nil {
			return err
		}
		if reused {
			return fault.New("conflict", "充值入账分录已存在但与当前通知不一致")
		}
		now := s.now()
		late := order.Status == "closed"
		status := "completed"
		if late {
			status = "completed"
		}
		if _, err := tx.Exec(ctx, `UPDATE public.recharge_orders SET payment_status = 'paid',
			credit_status = 'credited', status = $2, provider_trade_no = $3, late_payment = $4,
			paid_at = coalesce(paid_at, $5), credited_at = $5, updated_at = $5,
			version = version + 1 WHERE id = $1`,
			order.ID, status, fact.ProviderTradeNo, late, now); err != nil {
			return fault.New("service_unavailable", "无法更新充值订单到账状态")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.payment_attempts SET provider_trade_no = $2,
			status = 'paid', updated_at = $3 WHERE order_id = $1`,
			order.ID, fact.ProviderTradeNo, now); err != nil {
			return fault.New("service_unavailable", "无法更新支付尝试")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.payment_inbox SET status = 'processed',
			order_id = $2, processed_at = $3 WHERE id = $1`, inboxID, order.ID, now); err != nil {
			return fault.New("service_unavailable", "无法确认支付通知")
		}
		if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
			"entry": "recharge", "order_id": order.ID, "journal_id": journalID,
		}); err != nil {
			return err
		}
		reason := "ZPay 支付已核验并原子入账"
		if late {
			reason = "ZPay 迟到支付已核验并在关闭订单后入账"
		}
		if err := s.audit(ctx, tx, actorFromID("system", ""), "payment.credited",
			"recharge_order", order.ID, reason); err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) markInboxReview(ctx context.Context, tx pgx.Tx, inboxID, orderID, reason, message string) error {
	if _, err := tx.Exec(ctx, `UPDATE public.payment_inbox SET status = 'needs_review',
		order_id = $2, reason_code = $3, processed_at = $4 WHERE id = $1`,
		inboxID, nullableID(orderID), reason, s.now()); err != nil {
		return fault.New("service_unavailable", "无法记录支付异常")
	}
	if err := s.audit(ctx, tx, actorFromID("system", ""), "payment.review_required",
		"payment_inbox", inboxID, message); err != nil {
		return err
	}
	return nil
}

func (s *Service) markOrderReview(ctx context.Context, tx pgx.Tx, orderID string) error {
	if _, err := tx.Exec(ctx, `UPDATE public.recharge_orders SET status = 'needs_review',
		credit_status = CASE WHEN credit_status = 'credited' THEN credit_status ELSE 'needs_review' END,
		updated_at = $2, version = version + 1 WHERE id = $1`, orderID, s.now()); err != nil {
		return fault.New("service_unavailable", "无法标记订单异常")
	}
	return nil
}

// QueryRechargeOrder 主动查单并仅在提供商明确返回已支付时走同一入账路径。
func (s *Service) QueryRechargeOrder(ctx context.Context, actor web.Actor, orderID string) (*QueryResultView, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return nil, err
	}
	if !web.ValidID(orderID) {
		return nil, fault.New("validation_failed", "订单编号无效")
	}
	if s.provider == nil || s.provider.Name() != "zpay" || !s.provider.Capabilities().Query {
		return nil, fault.New("not_configured", "ZPay 主动查单尚未配置")
	}
	settings, err := s.loadBillingSettings(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	if !settings.ZPayCheckoutEnabled {
		return nil, fault.New("not_configured", "运营未启用 ZPay 网络访问")
	}
	order, err := scanOrder(s.pool.QueryRow(ctx, orderSelect+` WHERE id = $1`, orderID))
	if err != nil {
		return nil, err
	}
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	result, err := s.provider.QueryOrder(queryCtx, payments.QueryRequest{MerchantOrderNo: order.MerchantOrderNo})
	if err != nil {
		return nil, err
	}
	view := &QueryResultView{OrderID: order.ID, MerchantOrderNo: order.MerchantOrderNo,
		ProviderTradeNo: result.ProviderTradeNo, AmountMinor: formatAmount(result.AmountMinor),
		Channel: result.Channel, ProviderStatus: result.ProviderStatus, Paid: result.Paid,
		LocalAmountMinor: order.AmountMinor, Matched: result.AmountMinor == mustAmount(order.AmountMinor) &&
			result.Channel == order.Channel}
	if !result.Paid {
		return view, nil
	}
	if !view.Matched {
		dedupeKey, payloadDigest := inboxKeys(paymentFact{
			MerchantOrderNo: order.MerchantOrderNo, ProviderTradeNo: result.ProviderTradeNo,
			AmountMinor: result.AmountMinor, Channel: result.Channel,
			ProviderStatus: result.ProviderStatus, Source: "query",
		})
		return view, s.inTx(ctx, func(tx pgx.Tx) error {
			inboxID := newUUID()
			if _, err := tx.Exec(ctx, `INSERT INTO public.payment_inbox (
				id, provider, source, dedupe_key, payload_digest, order_id, merchant_order_no,
				provider_trade_no, amount_minor, channel, provider_status, status, verified,
				reason_code, received_at, processed_at
			) VALUES ($1,'zpay','query',$2,$3,$4,$5,$6,$7,$8,$9,'needs_review',true,
				'query_mismatch',$10,$10) ON CONFLICT (dedupe_key) DO NOTHING`,
				inboxID, dedupeKey, payloadDigest, order.ID, order.MerchantOrderNo,
				result.ProviderTradeNo, result.AmountMinor, result.Channel, result.ProviderStatus, s.now()); err != nil {
				return fault.New("service_unavailable", "无法记录查单差异")
			}
			if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
				return err
			}
			return s.audit(ctx, tx, actor, "payment.review_required", "recharge_order",
				order.ID, "主动查单金额或支付方式与本地订单不符")
		})
	}
	if err := s.processPayment(ctx, paymentFact{
		MerchantOrderNo: order.MerchantOrderNo, ProviderTradeNo: result.ProviderTradeNo,
		AmountMinor: result.AmountMinor, Channel: result.Channel,
		ProviderStatus: result.ProviderStatus, Source: "query",
	}); err != nil {
		return nil, err
	}
	return view, nil
}

type QueryResultView struct {
	OrderID          string `json:"order_id"`
	MerchantOrderNo  string `json:"merchant_order_no"`
	ProviderTradeNo  string `json:"provider_trade_no,omitempty"`
	AmountMinor      string `json:"amount_minor"`
	LocalAmountMinor string `json:"local_amount_minor"`
	Channel          string `json:"channel"`
	ProviderStatus   string `json:"provider_status"`
	Paid             bool   `json:"paid"`
	Matched          bool   `json:"matched"`
}
