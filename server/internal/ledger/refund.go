package ledger

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type Refund struct {
	ID               string `json:"id"`
	OrderID          string `json:"order_id"`
	UserID           string `json:"user_id"`
	WalletID         string `json:"wallet_id"`
	Kind             string `json:"kind"`
	AmountMinor      string `json:"amount_minor"`
	Currency         string `json:"currency"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
	Provider         string `json:"provider"`
	ProviderRefundNo string `json:"provider_refund_no"`
	ProviderTradeNo  string `json:"provider_trade_no,omitempty"`
	LastErrorCode    string `json:"last_error_code,omitempty"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	CompletedAt      string `json:"completed_at,omitempty"`
	createdAtValue   time.Time
}

type RefundInput struct {
	OrderID     string
	Kind        string
	AmountMinor string
	Reason      string
}

const refundSelect = `SELECT id, order_id, user_id, wallet_id, kind, amount_minor, currency,
	status, reason, provider, provider_refund_no, coalesce(provider_trade_no, ''),
	coalesce(last_error_code, ''), created_at, updated_at, completed_at
	FROM public.refunds`

func scanRefund(row pgx.Row) (*Refund, error) {
	var refund Refund
	var amount int64
	var createdAt, updatedAt time.Time
	var completedAt pgtype.Timestamptz
	if err := row.Scan(&refund.ID, &refund.OrderID, &refund.UserID, &refund.WalletID,
		&refund.Kind, &amount, &refund.Currency, &refund.Status, &refund.Reason,
		&refund.Provider, &refund.ProviderRefundNo, &refund.ProviderTradeNo,
		&refund.LastErrorCode, &createdAt, &updatedAt, &completedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "退款记录不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取退款记录")
	}
	refund.AmountMinor = formatAmount(amount)
	refund.CreatedAt = utcTime(createdAt)
	refund.createdAtValue = createdAt
	refund.UpdatedAt = utcTime(updatedAt)
	if completedAt.Valid {
		refund.CompletedAt = utcTime(completedAt.Time)
	}
	return &refund, nil
}

func (s *Service) CreateRefund(ctx context.Context, actor web.Actor, idempotencyKey string,
	input RefundInput) (*Refund, error) {
	if err := requireAgent(actor, "admin", PermWalletRefund); err != nil {
		return nil, err
	}
	if err := requireFresh(actor, s.now()); err != nil {
		return nil, err
	}
	if err := validKey(idempotencyKey); err != nil {
		return nil, fault.New("validation_failed", "缺少有效退款幂等键")
	}
	settings, err := s.loadBillingSettings(ctx, s.pool)
	if err != nil {
		return nil, err
	}
	if s.provider == nil || s.provider.Name() != "zpay" || !s.provider.Capabilities().Refund ||
		!settings.ZPayRefundEnabled {
		return nil, fault.New("not_configured", "ZPay 退款能力尚未完成核实，当前未启用")
	}
	if !web.ValidID(input.OrderID) {
		return nil, fault.New("validation_failed", "订单编号无效")
	}
	if input.Kind != "abnormal_payment" && input.Kind != "duplicate_payment" &&
		input.Kind != "chargeback" && input.Kind != "manual_correction" {
		return nil, fault.New("validation_failed", "退款类型无效")
	}
	amount, err := parseAmount(input.AmountMinor)
	if err != nil {
		return nil, err
	}
	reason := trimSpace(input.Reason)
	if len(reason) < 5 || len(reason) > 500 {
		return nil, fault.New("validation_failed", "退款必须填写 5 到 500 字符的理由")
	}
	requestDigest := digest("refund", actor.ID, input.OrderID, input.Kind,
		formatAmount(amount), reason)
	var refund *Refund
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.advisoryLocks(ctx, tx, "refund:"+actor.ID+":"+idempotencyKey); err != nil {
			return err
		}
		existing, err := scanRefund(tx.QueryRow(ctx, refundSelect+
			` WHERE requested_by = $1 AND idempotency_key = $2`, actor.ID, idempotencyKey))
		if err == nil {
			var storedDigest string
			if queryErr := tx.QueryRow(ctx, `SELECT request_digest FROM public.refunds
				WHERE id = $1`, existing.ID).Scan(&storedDigest); queryErr != nil {
				return fault.New("service_unavailable", "无法核对退款幂等键")
			}
			if storedDigest != requestDigest {
				return fault.New("conflict", "相同幂等键已用于不同退款请求")
			}
			if existing.Status == "reserved" || existing.Status == "requested" || existing.Status == "submitted" {
				return fault.New("conflict", "退款结果尚待核对，不能自动重放")
			}
			refund = existing
			return nil
		}
		if code, _ := fault.Describe(err); code != "not_found" {
			return err
		}
		order, err := scanOrder(tx.QueryRow(ctx, orderSelect+` WHERE id = $1 FOR UPDATE`, input.OrderID))
		if err != nil {
			return err
		}
		if order.PaymentStatus != "paid" || order.CreditStatus != "credited" {
			return fault.New("conflict", "订单尚未完成核验入账，不能发起异常退款")
		}
		if amount > mustAmount(order.AmountMinor) {
			return fault.New("validation_failed", "退款金额不能超过原订单金额")
		}
		var already int64
		if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount_minor), 0)::bigint FROM public.refunds
			WHERE order_id = $1 AND status IN ('requested','reserved','submitted','succeeded','unknown')`,
			order.ID).Scan(&already); err != nil {
			return fault.New("service_unavailable", "无法核对已有退款金额")
		}
		if already > mustAmount(order.AmountMinor)-amount {
			return fault.New("validation_failed", "退款金额超过原订单可退余额")
		}
		wallet, err := s.lockWalletByID(ctx, tx, order.WalletID)
		if err != nil {
			return err
		}
		if wallet.Frozen {
			return fault.New("conflict", "钱包已冻结，请先完成对账")
		}
		if wallet.AvailableMinor < amount {
			return fault.New("insufficient_balance", "可退款余额不足，正常已消费余额不支持退款")
		}
		refundID := newUUID()
		refundNo, err := newProviderRefundNo(s.now())
		if err != nil {
			return err
		}
		now := s.now()
		if _, err := tx.Exec(ctx, `INSERT INTO public.refunds (
			id, order_id, user_id, wallet_id, kind, amount_minor, currency, status, reason,
			provider, provider_refund_no, provider_trade_no, requested_by, approved_by,
			idempotency_key, request_digest, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,'CNY','requested',$7,'zpay',$8,$9,$10,$10,$11,$12,$13,$13)`,
			refundID, order.ID, order.UserID, wallet.ID, input.Kind, amount, reason,
			refundNo, order.ProviderTradeNo, actor.ID, idempotencyKey, requestDigest, now); err != nil {
			return fault.New("service_unavailable", "无法创建退款记录")
		}
		journalID, reused, err := s.postJournal(ctx, tx, journalInput{
			Kind: "refund_reserve", BusinessKey: "refund-reserve:" + refundID,
			RequestDigest: digest("refund_reserve", refundID, formatAmount(amount)),
			UserID:        order.UserID, WalletID: wallet.ID, AmountMinor: amount,
			Reason: "异常退款预占：" + reason, Actor: actor, OrderID: order.ID,
			Postings: []postingInput{
				{AccountID: wallet.AvailableAccountID, AmountMinor: -amount},
				{AccountID: systemRefundPayable, AmountMinor: amount},
			},
		})
		if err != nil {
			return err
		}
		if reused {
			return fault.New("conflict", "退款预占分录已存在")
		}
		if err := s.debitFunds(ctx, tx, wallet, amount); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE public.refunds SET status = 'reserved',
			reservation_journal_id = $2, updated_at = $3 WHERE id = $1`, refundID, journalID, now); err != nil {
			return fault.New("service_unavailable", "无法更新退款预占状态")
		}
		if err := s.audit(ctx, tx, actor, "refund.reserved", "refund", refundID,
			"异常退款预占："+reason); err != nil {
			return err
		}
		if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
			"entry": "refund_reserve", "refund_id": refundID, "journal_id": journalID,
		}); err != nil {
			return err
		}
		refund, err = scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1`, refundID))
		return err
	})
	if err != nil {
		return nil, err
	}
	if refund == nil || refund.Status != "reserved" {
		return refund, nil
	}
	return s.submitRefund(ctx, actor, refund)
}

func (s *Service) submitRefund(ctx context.Context, actor web.Actor, refund *Refund) (*Refund, error) {
	queryCtx, cancel := context.WithTimeout(ctx, s.queryTimeout)
	defer cancel()
	result, err := s.provider.Refund(queryCtx, payments.RefundRequest{
		MerchantOrderNo:  "",
		ProviderTradeNo:  refund.ProviderTradeNo,
		ProviderRefundNo: refund.ProviderRefundNo,
		AmountMinor:      mustAmount(refund.AmountMinor),
		Reason:           refund.Reason,
	})
	if err != nil {
		return s.finishRefund(ctx, actor, refund, "unknown", "provider_unavailable")
	}
	switch strings.ToLower(strings.TrimSpace(result.Status)) {
	case "succeeded", "success":
		return s.finishRefund(ctx, actor, refund, "succeeded", "")
	case "failed", "failure":
		return s.finishRefund(ctx, actor, refund, "failed", "")
	default:
		return s.finishRefund(ctx, actor, refund, "unknown", "provider_result_unknown")
	}
}

func (s *Service) finishRefund(ctx context.Context, actor web.Actor, refund *Refund,
	status, errorCode string) (*Refund, error) {
	var updated *Refund
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		current, err := scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1 FOR UPDATE`, refund.ID))
		if err != nil {
			return err
		}
		if current.Status == "succeeded" || current.Status == "failed" || current.Status == "cancelled" {
			updated = current
			return nil
		}
		amount := mustAmount(current.AmountMinor)
		now := s.now()
		if status == "succeeded" {
			journalID, reused, err := s.postJournal(ctx, tx, journalInput{
				Kind: "refund_payout", BusinessKey: "refund-payout:" + current.ID,
				RequestDigest: digest("refund_payout", current.ID, formatAmount(amount)),
				UserID:        current.UserID, WalletID: current.WalletID, AmountMinor: amount,
				Reason: "异常退款渠道成功", Actor: actor, OrderID: current.OrderID,
				Postings: []postingInput{
					{AccountID: systemRefundPayable, AmountMinor: -amount},
					{AccountID: systemChannelClearing, AmountMinor: amount},
				},
			})
			if err != nil {
				return err
			}
			if reused {
				return fault.New("conflict", "退款成功分录已存在")
			}
			if _, err := tx.Exec(ctx, `UPDATE public.refunds SET status = 'succeeded',
				payout_journal_id = $2, last_error_code = NULL, completed_at = $3, updated_at = $3
				WHERE id = $1`, current.ID, journalID, now); err != nil {
				return fault.New("service_unavailable", "无法更新退款成功状态")
			}
			var orderAmount int64
			if err := tx.QueryRow(ctx, `SELECT amount_minor FROM public.recharge_orders WHERE id = $1`,
				current.OrderID).Scan(&orderAmount); err != nil {
				return fault.New("service_unavailable", "无法读取原订单金额")
			}
			if amount == orderAmount {
				if _, err := tx.Exec(ctx, `UPDATE public.recharge_orders SET payment_status = 'refunded',
					updated_at = $2, version = version + 1 WHERE id = $1`, current.OrderID, now); err != nil {
					return fault.New("service_unavailable", "无法更新订单退款状态")
				}
			}
		} else if status == "failed" {
			wallet, err := s.lockWalletByID(ctx, tx, current.WalletID)
			if err != nil {
				return err
			}
			journalID, reused, err := s.postJournal(ctx, tx, journalInput{
				Kind: "refund_return", BusinessKey: "refund-return:" + current.ID,
				RequestDigest: digest("refund_return", current.ID, formatAmount(amount)),
				UserID:        current.UserID, WalletID: current.WalletID, AmountMinor: amount,
				Reason: "异常退款渠道失败，资金退回", Actor: actor, OrderID: current.OrderID,
				Postings: []postingInput{
					{AccountID: systemRefundPayable, AmountMinor: -amount},
					{AccountID: wallet.AvailableAccountID, AmountMinor: amount},
				},
			})
			if err != nil {
				return err
			}
			if reused {
				return fault.New("conflict", "退款退回分录已存在")
			}
			if err := s.creditFunds(ctx, tx, wallet, amount); err != nil {
				return err
			}
			if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
				"entry": "refund_return", "refund_id": current.ID, "journal_id": journalID,
			}); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE public.refunds SET status = 'failed',
				return_journal_id = $2, last_error_code = NULL, completed_at = $3, updated_at = $3
				WHERE id = $1`, current.ID, journalID, now); err != nil {
				return fault.New("service_unavailable", "无法更新退款失败状态")
			}
		} else {
			if _, err := tx.Exec(ctx, `UPDATE public.refunds SET status = 'unknown',
				last_error_code = $2, updated_at = $3 WHERE id = $1`,
				current.ID, nullableText(errorCode), now); err != nil {
				return fault.New("service_unavailable", "无法更新退款待查状态")
			}
		}
		action := "refund." + status
		if status == "unknown" {
			action = "refund.review_required"
		}
		if err := s.audit(ctx, tx, actor, action, "refund", current.ID,
			"异常退款处理结果："+status); err != nil {
			return err
		}
		updated, err = scanRefund(tx.QueryRow(ctx, refundSelect+` WHERE id = $1`, current.ID))
		return err
	})
	return updated, err
}
