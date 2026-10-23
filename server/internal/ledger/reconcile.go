package ledger

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onprs/ByebyeOCS/server/internal/payments"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type Reconciliation struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	Status          string         `json:"status"`
	TargetUserID    string         `json:"target_user_id,omitempty"`
	TargetWalletID  string         `json:"target_wallet_id,omitempty"`
	TargetOrderID   string         `json:"target_order_id,omitempty"`
	Findings        map[string]any `json:"findings"`
	ExpectedMinor   string         `json:"expected_minor,omitempty"`
	ActualMinor     string         `json:"actual_minor,omitempty"`
	DifferenceMinor string         `json:"difference_minor,omitempty"`
	ErrorCode       string         `json:"error_code,omitempty"`
	RequestedBy     string         `json:"requested_by"`
	CreatedAt       string         `json:"created_at"`
	createdAtValue  time.Time
	CompletedAt     string `json:"completed_at,omitempty"`
}

type ReconciliationPage struct {
	Items      []Reconciliation `json:"items"`
	NextCursor string           `json:"next_cursor"`
}

type ReconciliationInput struct {
	Kind     string
	WalletID string
	UserID   string
	OrderID  string
	Reason   string
}

const reconciliationSelect = `SELECT id, kind, status, coalesce(target_user_id::text, ''),
	coalesce(target_wallet_id::text, ''), coalesce(target_order_id::text, ''),
	findings::text, coalesce(expected_minor, 0), coalesce(actual_minor, 0),
	coalesce(difference_minor, 0), coalesce(error_code, ''), requested_by::text,
	created_at, completed_at
	FROM public.reconciliations`

func scanReconciliation(row pgx.Row) (*Reconciliation, error) {
	var record Reconciliation
	var findingsText string
	var expected, actual, difference int64
	var createdAt time.Time
	var completedAt pgtype.Timestamptz
	if err := row.Scan(&record.ID, &record.Kind, &record.Status, &record.TargetUserID,
		&record.TargetWalletID, &record.TargetOrderID, &findingsText, &expected, &actual,
		&difference, &record.ErrorCode, &record.RequestedBy, &createdAt, &completedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "对账记录不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取对账记录")
	}
	if err := json.Unmarshal([]byte(findingsText), &record.Findings); err != nil {
		record.Findings = map[string]any{}
	}
	if expected != 0 {
		record.ExpectedMinor = formatAmount(expected)
	}
	if actual != 0 {
		record.ActualMinor = formatAmount(actual)
	}
	if difference != 0 {
		record.DifferenceMinor = strconv.FormatInt(difference, 10)
	}
	record.CreatedAt = utcTime(createdAt)
	record.createdAtValue = createdAt
	if completedAt.Valid {
		record.CompletedAt = utcTime(completedAt.Time)
	}
	return &record, nil
}

func (s *Service) CreateReconciliation(ctx context.Context, actor web.Actor, idempotencyKey string,
	input ReconciliationInput) (*Reconciliation, error) {
	if err := requireAgent(actor, "admin", PermReconcile); err != nil {
		return nil, err
	}
	if err := requireFresh(actor, s.now()); err != nil {
		return nil, err
	}
	if err := validKey(idempotencyKey); err != nil {
		return nil, fault.New("validation_failed", "缺少有效对账幂等键")
	}
	if input.Kind != "wallet_rebuild" && input.Kind != "payment_orders" &&
		input.Kind != "payment_inbox" && input.Kind != "wallet_unfreeze" {
		return nil, fault.New("validation_failed", "对账类型无效")
	}
	if input.WalletID != "" && !web.ValidID(input.WalletID) {
		return nil, fault.New("validation_failed", "钱包编号无效")
	}
	if input.UserID != "" && !web.ValidID(input.UserID) {
		return nil, fault.New("validation_failed", "用户编号无效")
	}
	if input.OrderID != "" && !web.ValidID(input.OrderID) {
		return nil, fault.New("validation_failed", "订单编号无效")
	}
	if input.Kind == "wallet_unfreeze" && input.WalletID == "" {
		return nil, fault.New("validation_failed", "解除冻结必须指定钱包")
	}
	reason := trimSpace(input.Reason)
	if len(reason) < 5 || len(reason) > 500 {
		return nil, fault.New("validation_failed", "对账必须填写 5 到 500 字符的理由")
	}
	requestDigest := digest("reconciliation", actor.ID, input.Kind, input.WalletID, input.UserID,
		input.OrderID, reason)
	var existing *Reconciliation
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.advisoryLocks(ctx, tx, "reconcile:"+actor.ID+":"+idempotencyKey); err != nil {
			return err
		}
		var storedDigest string
		err := tx.QueryRow(ctx, `SELECT request_digest FROM public.reconciliations
			WHERE requested_by = $1 AND idempotency_key = $2`, actor.ID, idempotencyKey).Scan(&storedDigest)
		if err == nil {
			if storedDigest != requestDigest {
				return fault.New("conflict", "相同幂等键已用于不同对账请求")
			}
			record, err := scanReconciliation(tx.QueryRow(ctx, reconciliationSelect+
				` WHERE requested_by = $1 AND idempotency_key = $2`, actor.ID, idempotencyKey))
			if err != nil {
				return err
			}
			existing = record
			return nil
		}
		if err != pgx.ErrNoRows {
			return fault.New("service_unavailable", "无法检查对账幂等键")
		}
		for label, query := range map[string]string{
			"wallet": `SELECT 1 FROM public.wallets WHERE id = $1`,
			"user":   `SELECT 1 FROM public.users WHERE id = $1`,
			"order":  `SELECT 1 FROM public.recharge_orders WHERE id = $1`,
		} {
			var value string
			switch label {
			case "wallet":
				value = input.WalletID
			case "user":
				value = input.UserID
			case "order":
				value = input.OrderID
			}
			if value == "" {
				continue
			}
			var exists int
			if err := tx.QueryRow(ctx, query, value).Scan(&exists); err != nil {
				if err == pgx.ErrNoRows {
					return fault.New("not_found", "对账目标不存在")
				}
				return fault.New("service_unavailable", "无法核对对账目标")
			}
		}
		recordID := newUUID()
		if _, err := tx.Exec(ctx, `INSERT INTO public.reconciliations (
			id, kind, status, target_user_id, target_wallet_id, target_order_id, findings,
			requested_by, request_id, idempotency_key, request_digest, created_at, started_at
		) VALUES ($1,$2,'pending',$3,$4,$5,'{}'::jsonb,$6,$7,$8,$9,$10,$10)`,
			recordID, input.Kind, nullableID(input.UserID), nullableID(input.WalletID),
			nullableID(input.OrderID), actor.ID, s.requestID(ctx), idempotencyKey,
			requestDigest, s.now()); err != nil {
			return fault.New("service_unavailable", "无法创建对账记录")
		}
		record, err := scanReconciliation(tx.QueryRow(ctx, reconciliationSelect+` WHERE id = $1`, recordID))
		if err != nil {
			return err
		}
		if input.Kind == "wallet_rebuild" || input.Kind == "wallet_unfreeze" {
			if err := s.runWalletReconciliation(ctx, tx, record, actor, input, reason); err != nil {
				return err
			}
		}
		existing = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	if input.Kind == "payment_orders" || input.Kind == "payment_inbox" {
		if err := s.runPaymentReconciliation(ctx, existing.ID, actor, reason); err != nil {
			_ = s.inTx(ctx, func(tx pgx.Tx) error {
				_, updateErr := tx.Exec(ctx, `UPDATE public.reconciliations SET status = 'failed',
					error_code = 'internal_error', completed_at = $2 WHERE id = $1`, existing.ID, s.now())
				return updateErr
			})
			return nil, err
		}
		return s.getReconciliation(ctx, existing.ID)
	}
	return existing, nil
}

func (s *Service) getReconciliation(ctx context.Context, id string) (*Reconciliation, error) {
	return scanReconciliation(s.pool.QueryRow(ctx, reconciliationSelect+` WHERE id = $1`, id))
}

func (s *Service) runWalletReconciliation(ctx context.Context, tx pgx.Tx, record *Reconciliation,
	actor web.Actor, input ReconciliationInput, reason string) error {
	findings := map[string]any{}
	status := "matched"
	expected, actual, difference := int64(0), int64(0), int64(0)
	if input.WalletID == "" && input.UserID != "" {
		var walletID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM public.wallets WHERE user_id = $1`,
			input.UserID).Scan(&walletID); err != nil {
			if err == pgx.ErrNoRows {
				return fault.New("not_found", "该用户尚未创建钱包")
			}
			return fault.New("service_unavailable", "无法读取用户钱包")
		}
		input.WalletID = walletID
	}
	if input.WalletID != "" {
		result, err := s.rebuildWallet(ctx, tx, input.WalletID)
		if err != nil {
			return err
		}
		findings["wallet_id"] = result.WalletID
		findings["cached_available_minor"] = formatAmount(result.AvailableCached)
		findings["ledger_available_minor"] = formatAmount(result.AvailableLedger)
		findings["cached_reserved_minor"] = formatAmount(result.ReservedCached)
		findings["ledger_reserved_minor"] = formatAmount(result.ReservedLedger)
		findings["hold_reserved_minor"] = formatAmount(result.ReservedHolds)
		expected, actual = result.AvailableCached, result.AvailableLedger
		difference = actual - expected
		if !result.Matched {
			status = "mismatch"
			if err := s.freezeWallet(ctx, tx, input.WalletID, "reconciliation_mismatch"); err != nil {
				return err
			}
		} else if input.Kind == "wallet_unfreeze" {
			if err := s.unfreezeWallet(ctx, tx, input.WalletID); err != nil {
				return err
			}
			findings["unfrozen"] = true
		}
	} else {
		ids := []string{}
		var totalWallets int
		if err := tx.QueryRow(ctx, `SELECT count(*)::integer FROM public.wallets`).Scan(&totalWallets); err != nil {
			return fault.New("service_unavailable", "无法统计钱包数量")
		}
		rows, err := tx.Query(ctx, `SELECT id FROM public.wallets ORDER BY created_at LIMIT $1`, s.reconcileLimit)
		if err != nil {
			return fault.New("service_unavailable", "无法读取待对账钱包")
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return fault.New("service_unavailable", "无法读取待对账钱包")
			}
			ids = append(ids, id)
		}
		rows.Close()
		mismatches := []string{}
		for _, id := range ids {
			result, err := s.rebuildWallet(ctx, tx, id)
			if err != nil {
				return err
			}
			if !result.Matched {
				mismatches = append(mismatches, id)
				if err := s.freezeWallet(ctx, tx, id, "reconciliation_mismatch"); err != nil {
					return err
				}
			}
		}
		findings["wallets_checked"] = strconv.Itoa(len(ids))
		findings["wallets_total"] = strconv.Itoa(totalWallets)
		findings["wallets_mismatched"] = strconv.Itoa(len(mismatches))
		if len(mismatches) > 0 {
			status = "mismatch"
			findings["mismatched_wallet_ids"] = mismatches
		} else if totalWallets > len(ids) {
			status = "incomplete"
		}
	}
	findingsJSON, _ := json.Marshal(findings)
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE public.reconciliations SET status = $2, findings = $3::jsonb,
		expected_minor = $4, actual_minor = $5, difference_minor = $6, completed_at = $7
		WHERE id = $1`, record.ID, status, string(findingsJSON),
		nullableZero(expected), nullableZero(actual), difference, now); err != nil {
		return fault.New("service_unavailable", "无法更新对账结果")
	}
	record.Status = status
	record.Findings = findings
	record.CompletedAt = utcTime(now)
	action := "reconciliation.completed"
	if status == "mismatch" {
		action = "reconciliation.mismatch"
	}
	return s.audit(ctx, tx, actor, action, "reconciliation", record.ID, reason)
}

func nullableZero(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func (s *Service) runPaymentReconciliation(ctx context.Context, id string, actor web.Actor, reason string) error {
	record, err := s.getReconciliation(ctx, id)
	if err != nil {
		return err
	}
	findings := map[string]any{}
	status := "matched"
	errorCode := ""
	switch record.Kind {
	case "payment_inbox":
		var total, needsReview, received int
		if err := s.pool.QueryRow(ctx, `SELECT count(*),
			count(*) FILTER (WHERE status = 'needs_review'),
			count(*) FILTER (WHERE status = 'received')
			FROM public.payment_inbox`).Scan(&total, &needsReview, &received); err != nil {
			return fault.New("service_unavailable", "无法核对支付通知")
		}
		findings["total"] = strconv.Itoa(total)
		findings["needs_review"] = strconv.Itoa(needsReview)
		findings["unprocessed"] = strconv.Itoa(received)
		if needsReview > 0 {
			status = "mismatch"
		} else if received > 0 {
			status = "incomplete"
		}
	case "payment_orders":
		settings, err := s.loadBillingSettings(ctx, s.pool)
		if err != nil {
			return err
		}
		if s.provider == nil || s.provider.Name() != "zpay" || !s.provider.Capabilities().Query ||
			!settings.ZPayCheckoutEnabled {
			status = "incomplete"
			errorCode = "not_configured"
			findings["reason"] = "ZPay 主动查单未配置或运营未启用"
			break
		}
		orderQuery := orderSelect + ` WHERE status = 'pending_payment'
			AND payment_status = 'unpaid'`
		orderArgs := []any{}
		if record.TargetOrderID != "" {
			orderQuery += ` AND id = $1`
			orderArgs = append(orderArgs, record.TargetOrderID)
		}
		orderQuery += ` ORDER BY created_at LIMIT $` + itoa(len(orderArgs)+1)
		orderArgs = append(orderArgs, s.reconcileLimit)
		var totalPending int
		countQuery := `SELECT count(*)::integer FROM public.recharge_orders
			WHERE status = 'pending_payment' AND payment_status = 'unpaid'`
		countArgs := []any{}
		if record.TargetOrderID != "" {
			countQuery += ` AND id = $1`
			countArgs = append(countArgs, record.TargetOrderID)
		}
		if err := s.pool.QueryRow(ctx, countQuery, countArgs...).Scan(&totalPending); err != nil {
			return fault.New("service_unavailable", "无法统计待查订单")
		}
		rows, err := s.pool.Query(ctx, orderQuery, orderArgs...)
		if err != nil {
			return fault.New("service_unavailable", "无法读取待查订单")
		}
		orders := []*RechargeOrder{}
		for rows.Next() {
			order, err := scanOrder(rows)
			if err != nil {
				rows.Close()
				return err
			}
			orders = append(orders, order)
		}
		rows.Close()
		queried, paid, mismatched, failed := 0, 0, 0, 0
		batchCtx, cancelBatch := context.WithTimeout(ctx, 10*time.Second)
		defer cancelBatch()
		for _, order := range orders {
			if batchCtx.Err() != nil {
				failed++
				errorCode = "query_timeout"
				break
			}
			queryCtx, cancel := context.WithTimeout(batchCtx, s.queryTimeout)
			result, queryErr := s.provider.QueryOrder(queryCtx, payments.QueryRequest{MerchantOrderNo: order.MerchantOrderNo})
			cancel()
			if queryErr != nil {
				failed++
				continue
			}
			queried++
			if !result.Paid {
				continue
			}
			if result.AmountMinor != mustAmount(order.AmountMinor) || result.Channel != order.Channel {
				mismatched++
				if err := s.recordQueryMismatch(ctx, actor, order, result); err != nil {
					failed++
				}
				continue
			}
			if err := s.processPayment(ctx, paymentFact{MerchantOrderNo: order.MerchantOrderNo,
				ProviderTradeNo: result.ProviderTradeNo, AmountMinor: result.AmountMinor,
				Channel: result.Channel, ProviderStatus: result.ProviderStatus, Source: "query"}); err != nil {
				failed++
				continue
			}
			paid++
		}
		findings["orders_checked"] = strconv.Itoa(len(orders))
		findings["orders_total"] = strconv.Itoa(totalPending)
		findings["queried"] = strconv.Itoa(queried)
		findings["paid"] = strconv.Itoa(paid)
		findings["mismatched"] = strconv.Itoa(mismatched)
		findings["failed"] = strconv.Itoa(failed)
		if mismatched > 0 {
			status = "mismatch"
		} else if failed > 0 {
			status = "incomplete"
			if errorCode == "" {
				errorCode = "query_failed"
			}
		} else if totalPending > len(orders) {
			status = "incomplete"
			if errorCode == "" {
				errorCode = "batch_limit"
			}
		}
	default:
		return fault.New("validation_failed", "对账类型无效")
	}
	findingsJSON, _ := json.Marshal(findings)
	now := s.now()
	if err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE public.reconciliations SET status = $2, findings = $3::jsonb,
			error_code = NULLIF($4, ''), completed_at = $5 WHERE id = $1`,
			id, status, string(findingsJSON), errorCode, now); err != nil {
			return fault.New("service_unavailable", "无法更新对账结果")
		}
		action := "reconciliation.completed"
		if status == "mismatch" {
			action = "reconciliation.mismatch"
		}
		return s.audit(ctx, tx, actor, action, "reconciliation", id, reason)
	}); err != nil {
		return err
	}
	return nil
}

func (s *Service) recordQueryMismatch(ctx context.Context, actor web.Actor, order *RechargeOrder,
	result payments.QueryResult) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO public.payment_inbox (
			id, provider, source, dedupe_key, payload_digest, order_id, merchant_order_no,
			provider_trade_no, amount_minor, channel, provider_status, status, verified,
			reason_code, received_at, processed_at
		) VALUES ($1,'zpay','query',$2,$3,$4,$5,$6,$7,$8,$9,'needs_review',true,
			'query_mismatch',$10,$10) ON CONFLICT (dedupe_key) DO NOTHING`,
			newUUID(), digest("zpay", order.MerchantOrderNo, result.ProviderTradeNo,
				formatAmount(result.AmountMinor), result.ProviderStatus, result.Channel),
			digest(order.MerchantOrderNo, result.ProviderTradeNo, formatAmount(result.AmountMinor),
				result.ProviderStatus, result.Channel), order.ID, order.MerchantOrderNo,
			result.ProviderTradeNo, result.AmountMinor, result.Channel, result.ProviderStatus,
			s.now()); err != nil {
			return fault.New("service_unavailable", "无法记录查单差异")
		}
		if err := s.markOrderReview(ctx, tx, order.ID); err != nil {
			return err
		}
		return s.audit(ctx, tx, actor, "payment.review_required", "recharge_order",
			order.ID, "主动查单金额或支付方式与本地订单不符")
	})
}

func (s *Service) ListReconciliations(ctx context.Context, actor web.Actor, status, kind, cursor string,
	limit int) (ReconciliationPage, error) {
	if err := requireAgent(actor, "admin", PermReconcile); err != nil {
		return ReconciliationPage{}, err
	}
	if limit < 1 || limit > 100 {
		return ReconciliationPage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return ReconciliationPage{}, err
	}
	where := []string{"true"}
	args := []any{}
	if status != "" {
		if status != "pending" && status != "running" && status != "matched" &&
			status != "mismatch" && status != "incomplete" && status != "failed" {
			return ReconciliationPage{}, fault.New("validation_failed", "对账状态筛选无效")
		}
		args = append(args, status)
		where = append(where, "status = $"+itoa(len(args)))
	}
	if kind != "" {
		if kind != "wallet_rebuild" && kind != "wallet_unfreeze" && kind != "payment_orders" &&
			kind != "payment_inbox" {
			return ReconciliationPage{}, fault.New("validation_failed", "对账类型筛选无效")
		}
		args = append(args, kind)
		where = append(where, "kind = $"+itoa(len(args)))
	}
	if decoded.id != "" {
		args = append(args, decoded.createdAt, decoded.id)
		where = append(where, "(created_at < $"+itoa(len(args)-1)+" OR (created_at = $"+
			itoa(len(args)-1)+" AND id < $"+itoa(len(args))+"))")
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, reconciliationSelect+` WHERE `+joinAnd(where)+`
		ORDER BY created_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return ReconciliationPage{}, fault.New("service_unavailable", "无法读取对账记录")
	}
	defer rows.Close()
	items := make([]Reconciliation, 0, limit+1)
	for rows.Next() {
		record, err := scanReconciliation(rows)
		if err != nil {
			return ReconciliationPage{}, err
		}
		items = append(items, *record)
	}
	if err := rows.Err(); err != nil {
		return ReconciliationPage{}, fault.New("service_unavailable", "无法读取对账记录")
	}
	page := ReconciliationPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		// 游标必须指向本页最后一条已返回记录；指向多取的探测行会让下一页跳过一条。
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAtValue, id: last.ID})
	}
	return page, nil
}
