package ledger

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// AdminUserWallet 是管理员用户详情页的钱包读取结果。
// WalletCreated 为 true 表示本次 GET 真实创建了零余额钱包，而不是返回模拟余额。
type AdminUserWallet struct {
	UserID        string         `json:"user_id"`
	DisplayName   string         `json:"display_name,omitempty"`
	Wallet        WalletSnapshot `json:"wallet"`
	WalletCreated bool           `json:"wallet_created"`
}

type AdminWalletItem struct {
	UserID      string         `json:"user_id"`
	DisplayName string         `json:"display_name,omitempty"`
	Wallet      WalletSnapshot `json:"wallet"`
	createdAt   time.Time
}

type AdminWalletPage struct {
	Items      []AdminWalletItem `json:"items"`
	NextCursor string            `json:"next_cursor"`
}

const adminWalletSelect = `SELECT w.id, w.user_id, coalesce(e.display_name, ''),
	w.available_minor, w.reserved_minor, w.tiku_accrued_micros, w.frozen,
	coalesce(w.frozen_reason, ''), w.version, w.updated_at, w.created_at
	FROM public.wallets w
	LEFT JOIN LATERAL (
		SELECT ei.display_name
		FROM public.external_identities ei
		WHERE ei.user_id = w.user_id AND ei.status = 'active' AND ei.display_name IS NOT NULL
		ORDER BY ei.verified_at DESC, ei.id
		LIMIT 1
	) e ON true`

func scanAdminWalletItem(row pgx.Row) (*AdminWalletItem, error) {
	var item AdminWalletItem
	var wallet walletRow
	if err := row.Scan(&wallet.ID, &wallet.UserID, &item.DisplayName,
		&wallet.AvailableMinor, &wallet.ReservedMinor, &wallet.TikuAccruedMicros,
		&wallet.Frozen, &wallet.FrozenReason, &wallet.Version, &wallet.UpdatedAt,
		&item.createdAt); err != nil {
		return nil, fault.New("service_unavailable", "无法读取管理员钱包列表")
	}
	item.UserID = wallet.UserID
	item.Wallet = snapshotWallet(&wallet)
	return &item, nil
}

func (s *Service) AdminUserWallet(ctx context.Context, actor web.Actor, userID string) (*AdminUserWallet, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return nil, err
	}
	if !web.ValidID(userID) {
		return nil, fault.New("validation_failed", "用户编号无效")
	}
	var result *AdminUserWallet
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var displayName string
		err := tx.QueryRow(ctx, `SELECT coalesce((
			SELECT ei.display_name FROM public.external_identities ei
			WHERE ei.user_id = u.id AND ei.status = 'active' AND ei.display_name IS NOT NULL
			ORDER BY ei.verified_at DESC, ei.id LIMIT 1
		), '') FROM public.users u WHERE u.id = $1`, userID).Scan(&displayName)
		if err != nil {
			if err == pgx.ErrNoRows {
				return fault.New("not_found", "用户不存在")
			}
			return fault.New("service_unavailable", "无法读取用户")
		}
		var existed bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM public.wallets WHERE user_id = $1)`,
			userID).Scan(&existed); err != nil {
			return fault.New("service_unavailable", "无法检查用户钱包")
		}
		wallet, err := s.ensureWallet(ctx, tx, userID)
		if err != nil {
			return err
		}
		result = &AdminUserWallet{UserID: userID, DisplayName: displayName,
			Wallet: snapshotWallet(wallet), WalletCreated: !existed}
		return nil
	})
	return result, err
}

// ListAdminWallets 只列真实存在的钱包；search 支持内部用户 ID、钱包 ID 精确匹配和显示名子串。
// 身份指纹、外部账号命名空间、会话和上游凭据不进入结果。
func (s *Service) ListAdminWallets(ctx context.Context, actor web.Actor, search, cursor string,
	limit int) (AdminWalletPage, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return AdminWalletPage{}, err
	}
	if limit < 1 || limit > 100 {
		return AdminWalletPage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	search = strings.TrimSpace(search)
	if len(search) > 128 {
		return AdminWalletPage{}, fault.New("validation_failed", "搜索词超出限制")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return AdminWalletPage{}, err
	}
	where := `($1 = '' OR w.user_id::text = $1 OR w.id::text = $1
		OR position(lower($1) in lower(coalesce(e.display_name, ''))) > 0)`
	args := []any{search}
	if decoded.id != "" {
		args = append(args, decoded.createdAt, decoded.id)
		where += " AND (w.created_at < $2 OR (w.created_at = $2 AND w.id < $3))"
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, adminWalletSelect+` WHERE `+where+`
		ORDER BY w.created_at DESC, w.id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return AdminWalletPage{}, fault.New("service_unavailable", "无法读取管理员钱包列表")
	}
	defer rows.Close()
	items := make([]AdminWalletItem, 0, limit+1)
	for rows.Next() {
		item, err := scanAdminWalletItem(rows)
		if err != nil {
			return AdminWalletPage{}, err
		}
		items = append(items, *item)
	}
	if err := rows.Err(); err != nil {
		return AdminWalletPage{}, fault.New("service_unavailable", "无法读取管理员钱包列表")
	}
	page := AdminWalletPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAt, id: last.Wallet.ID})
	}
	return page, nil
}

type RefundPage struct {
	Items      []Refund `json:"items"`
	NextCursor string   `json:"next_cursor"`
}

func (s *Service) ListAdminRefunds(ctx context.Context, actor web.Actor, status, orderID, userID,
	cursor string, limit int) (RefundPage, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return RefundPage{}, err
	}
	if limit < 1 || limit > 100 {
		return RefundPage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return RefundPage{}, err
	}
	where := []string{"true"}
	args := []any{}
	if status != "" {
		switch status {
		case "requested", "reserved", "submitted", "succeeded", "failed", "unknown", "cancelled":
		default:
			return RefundPage{}, fault.New("validation_failed", "退款状态筛选无效")
		}
		args = append(args, status)
		where = append(where, "status = $"+itoa(len(args)))
	}
	if orderID != "" {
		if !web.ValidID(orderID) {
			return RefundPage{}, fault.New("validation_failed", "订单编号无效")
		}
		args = append(args, orderID)
		where = append(where, "order_id = $"+itoa(len(args)))
	}
	if userID != "" {
		if !web.ValidID(userID) {
			return RefundPage{}, fault.New("validation_failed", "用户编号无效")
		}
		args = append(args, userID)
		where = append(where, "user_id = $"+itoa(len(args)))
	}
	if decoded.id != "" {
		args = append(args, decoded.createdAt, decoded.id)
		where = append(where, "(created_at < $"+itoa(len(args)-1)+" OR (created_at = $"+
			itoa(len(args)-1)+" AND id < $"+itoa(len(args))+"))")
	}
	args = append(args, limit+1)
	rows, err := s.pool.Query(ctx, refundSelect+` WHERE `+joinAnd(where)+`
		ORDER BY created_at DESC, id DESC LIMIT $`+itoa(len(args)), args...)
	if err != nil {
		return RefundPage{}, fault.New("service_unavailable", "无法读取退款记录")
	}
	defer rows.Close()
	items := make([]Refund, 0, limit+1)
	for rows.Next() {
		refund, err := scanRefund(rows)
		if err != nil {
			return RefundPage{}, err
		}
		items = append(items, *refund)
	}
	if err := rows.Err(); err != nil {
		return RefundPage{}, fault.New("service_unavailable", "无法读取退款记录")
	}
	page := RefundPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAtValue, id: last.ID})
	}
	return page, nil
}

// Metric 是财务汇总中的真实整数计数与金额。
type Metric struct {
	Count       int64  `json:"count"`
	AmountMinor string `json:"amount_minor"`
}

type FinanceWalletSummary struct {
	Count             int64  `json:"count"`
	AvailableMinor    string `json:"available_minor"`
	ReservedMinor     string `json:"reserved_minor"`
	TikuAccruedMicros string `json:"tiku_accrued_micros"`
	FrozenCount       int64  `json:"frozen_count"`
}

type FinanceRangeView struct {
	From        string `json:"from"`
	To          string `json:"to"`
	AllTime     bool   `json:"all_time"`
	ToExclusive bool   `json:"to_exclusive"`
}

type FinanceRangeMetrics struct {
	OrdersCreated      Metric `json:"orders_created"`
	PaymentSucceeded   Metric `json:"payment_succeeded"`
	PaymentCredited    Metric `json:"payment_credited"`
	RefundSucceeded    Metric `json:"refund_succeeded"`
	LedgerCaptured     Metric `json:"ledger_captured"`
	LedgerRefundPayout Metric `json:"ledger_refund_payout"`
	LedgerManualCredit Metric `json:"ledger_manual_credit"`
	LedgerManualDebit  Metric `json:"ledger_manual_debit"`
}

type FinanceCurrentMetrics struct {
	Wallets           FinanceWalletSummary `json:"wallets"`
	PaidNotCredited   Metric               `json:"paid_not_credited"`
	OrdersNeedsReview Metric               `json:"orders_needs_review"`
	RefundsOpen       Metric               `json:"refunds_open"`
}

type FinanceSummary struct {
	AsOf         string                `json:"as_of"`
	Range        FinanceRangeView      `json:"range"`
	RangeMetrics FinanceRangeMetrics   `json:"range_metrics"`
	Current      FinanceCurrentMetrics `json:"current"`
}

func metric(count, amount int64) Metric {
	return Metric{Count: count, AmountMinor: formatAmount(amount)}
}

// FinanceSummary 在 from（含）到 to（不含）范围内聚合全部真实记录；未提供范围时 all_time=true。
// current 部分是按 as_of 时刻的全量状态，不受 from/to 过滤，避免用最近若干条冒充全量。
func (s *Service) FinanceSummary(ctx context.Context, actor web.Actor, from, to *time.Time) (*FinanceSummary, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return nil, err
	}
	now := s.now()
	fromTime := time.Unix(0, 0).UTC()
	toTime := now
	allTime := from == nil && to == nil
	if allTime {
		// 全量语义不设上限，避免把时间戳略晚于当前时钟的真实记录漏掉。
		toTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	}
	fromLabel, toLabel := "", ""
	if from != nil {
		fromTime = from.UTC()
		fromLabel = utcTime(*from)
	}
	if to != nil {
		toTime = to.UTC()
		toLabel = utcTime(*to)
	}
	if !fromTime.Before(toTime) {
		return nil, fault.New("validation_failed", "时间范围必须满足 from < to")
	}

	var createdCount, createdAmount int64
	var paidCount, paidAmount int64
	var creditedCount, creditedAmount int64
	var paidNotCreditedCount, paidNotCreditedAmount int64
	var needsReviewCount, needsReviewAmount int64
	if err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE created_at >= $1 AND created_at < $2),
		coalesce(sum(amount_minor) FILTER (WHERE created_at >= $1 AND created_at < $2), 0)::bigint,
		count(*) FILTER (WHERE payment_status IN ('paid', 'refunded') AND paid_at >= $1 AND paid_at < $2),
		coalesce(sum(amount_minor) FILTER (WHERE payment_status IN ('paid', 'refunded')
			AND paid_at >= $1 AND paid_at < $2), 0)::bigint,
		count(*) FILTER (WHERE credit_status = 'credited' AND credited_at >= $1 AND credited_at < $2),
		coalesce(sum(amount_minor) FILTER (WHERE credit_status = 'credited'
			AND credited_at >= $1 AND credited_at < $2), 0)::bigint,
		count(*) FILTER (WHERE payment_status = 'paid' AND credit_status <> 'credited'),
		coalesce(sum(amount_minor) FILTER (WHERE payment_status = 'paid'
			AND credit_status <> 'credited'), 0)::bigint,
		count(*) FILTER (WHERE status = 'needs_review'),
		coalesce(sum(amount_minor) FILTER (WHERE status = 'needs_review'), 0)::bigint
		FROM public.recharge_orders`, fromTime, toTime).Scan(&createdCount, &createdAmount,
		&paidCount, &paidAmount, &creditedCount, &creditedAmount,
		&paidNotCreditedCount, &paidNotCreditedAmount, &needsReviewCount, &needsReviewAmount); err != nil {
		return nil, fault.New("service_unavailable", "无法聚合订单财务汇总")
	}

	var refundSucceededCount, refundSucceededAmount int64
	var refundsOpenCount, refundsOpenAmount int64
	if err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE status = 'succeeded' AND completed_at >= $1 AND completed_at < $2),
		coalesce(sum(amount_minor) FILTER (WHERE status = 'succeeded'
			AND completed_at >= $1 AND completed_at < $2), 0)::bigint,
		count(*) FILTER (WHERE status IN ('requested', 'reserved', 'submitted', 'unknown')),
		coalesce(sum(amount_minor) FILTER (WHERE status IN
			('requested', 'reserved', 'submitted', 'unknown')), 0)::bigint
		FROM public.refunds`, fromTime, toTime).Scan(&refundSucceededCount, &refundSucceededAmount,
		&refundsOpenCount, &refundsOpenAmount); err != nil {
		return nil, fault.New("service_unavailable", "无法聚合退款财务汇总")
	}

	var walletCount, available, reserved, tikuAccrued, frozenCount int64
	if err := s.pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(available_minor), 0)::bigint,
		coalesce(sum(reserved_minor), 0)::bigint, coalesce(sum(tiku_accrued_micros), 0)::bigint,
		count(*) FILTER (WHERE frozen) FROM public.wallets`).Scan(&walletCount, &available,
		&reserved, &tikuAccrued, &frozenCount); err != nil {
		return nil, fault.New("service_unavailable", "无法聚合钱包财务汇总")
	}

	var capturedCount, capturedAmount int64
	var payoutCount, payoutAmount int64
	var manualCreditCount, manualCreditAmount int64
	var manualDebitCount, manualDebitAmount int64
	// WHERE 只取这四项统计涉及的 kind 与时间范围；其余 kind（hold、release、充值等）
	// 在任何 FILTER 中都不计入。该条件与 FILTER 等价，同时使 journals(kind, created_at) 索引可用，
	// 避免仍按整表 journals 扫描后再过滤。
	if err := s.pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE kind = 'capture'),
		coalesce(sum(amount_minor) FILTER (WHERE kind = 'capture'), 0)::bigint,
		count(*) FILTER (WHERE kind = 'refund_payout'),
		coalesce(sum(amount_minor) FILTER (WHERE kind = 'refund_payout'), 0)::bigint,
		count(*) FILTER (WHERE kind = 'manual_credit'),
		coalesce(sum(amount_minor) FILTER (WHERE kind = 'manual_credit'), 0)::bigint,
		count(*) FILTER (WHERE kind = 'manual_debit'),
		coalesce(sum(amount_minor) FILTER (WHERE kind = 'manual_debit'), 0)::bigint
		FROM public.journals
		WHERE kind IN ('capture', 'refund_payout', 'manual_credit', 'manual_debit')
		  AND created_at >= $1 AND created_at < $2`, fromTime, toTime).Scan(&capturedCount, &capturedAmount,
		&payoutCount, &payoutAmount, &manualCreditCount, &manualCreditAmount,
		&manualDebitCount, &manualDebitAmount); err != nil {
		return nil, fault.New("service_unavailable", "无法聚合账本财务汇总")
	}

	return &FinanceSummary{
		AsOf:  utcTime(now),
		Range: FinanceRangeView{From: fromLabel, To: toLabel, AllTime: allTime, ToExclusive: true},
		RangeMetrics: FinanceRangeMetrics{
			OrdersCreated:      metric(createdCount, createdAmount),
			PaymentSucceeded:   metric(paidCount, paidAmount),
			PaymentCredited:    metric(creditedCount, creditedAmount),
			RefundSucceeded:    metric(refundSucceededCount, refundSucceededAmount),
			LedgerCaptured:     metric(capturedCount, capturedAmount),
			LedgerRefundPayout: metric(payoutCount, payoutAmount),
			LedgerManualCredit: metric(manualCreditCount, manualCreditAmount),
			LedgerManualDebit:  metric(manualDebitCount, manualDebitAmount),
		},
		Current: FinanceCurrentMetrics{
			Wallets: FinanceWalletSummary{Count: walletCount, AvailableMinor: formatAmount(available),
				ReservedMinor: formatAmount(reserved), TikuAccruedMicros: formatAmount(tikuAccrued),
				FrozenCount: frozenCount},
			PaidNotCredited:   metric(paidNotCreditedCount, paidNotCreditedAmount),
			OrdersNeedsReview: metric(needsReviewCount, needsReviewAmount),
			RefundsOpen:       metric(refundsOpenCount, refundsOpenAmount),
		},
	}, nil
}
