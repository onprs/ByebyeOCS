package ledger

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type rowQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type walletRow struct {
	ID                 string
	UserID             string
	AvailableMinor     int64
	ReservedMinor      int64
	TikuAccruedMicros  int64
	Frozen             bool
	FrozenReason       string
	Version            int64
	AvailableAccountID string
	ReservedAccountID  string
	UpdatedAt          time.Time
}

type WalletSnapshot struct {
	ID                string `json:"id"`
	Currency          string `json:"currency"`
	AvailableMinor    string `json:"available_minor"`
	ReservedMinor     string `json:"reserved_minor"`
	TikuAccruedMicros string `json:"tiku_accrued_micros"`
	Frozen            bool   `json:"frozen"`
	FrozenReason      string `json:"frozen_reason,omitempty"`
	Version           int64  `json:"version"`
	UpdatedAt         string `json:"updated_at"`
}

func snapshotWallet(wallet *walletRow) WalletSnapshot {
	return WalletSnapshot{
		ID: wallet.ID, Currency: CurrencyCNY,
		AvailableMinor:    formatAmount(wallet.AvailableMinor),
		ReservedMinor:     formatAmount(wallet.ReservedMinor),
		TikuAccruedMicros: formatAmount(wallet.TikuAccruedMicros),
		Frozen:            wallet.Frozen, FrozenReason: wallet.FrozenReason,
		Version: wallet.Version, UpdatedAt: wallet.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (s *Service) ensureWallet(ctx context.Context, tx pgx.Tx, userID string) (*walletRow, error) {
	if !web.ValidID(userID) {
		return nil, fault.New("validation_failed", "用户编号无效")
	}
	_, err := tx.Exec(ctx, `INSERT INTO public.wallets (id, user_id)
		VALUES ($1, $2) ON CONFLICT (user_id) DO NOTHING`, newUUID(), userID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, fault.New("not_found", "用户不存在")
		}
		return nil, fault.New("service_unavailable", "无法创建钱包")
	}
	wallet, err := scanWallet(tx.QueryRow(ctx, `SELECT id, user_id, available_minor, reserved_minor,
		tiku_accrued_micros, frozen, coalesce(frozen_reason, ''), version,
		coalesce(available_account_id::text, ''), coalesce(reserved_account_id::text, ''), updated_at
		FROM public.wallets WHERE user_id = $1 FOR UPDATE`, userID))
	if err != nil {
		return nil, err
	}
	if wallet.AvailableAccountID == "" || wallet.ReservedAccountID == "" {
		availableID, reservedID := newUUID(), newUUID()
		if _, err := tx.Exec(ctx, `INSERT INTO public.ledger_accounts (id, code, kind, wallet_id, user_id)
			VALUES ($1, $2, 'user_available', $3, $4), ($5, $6, 'user_reserved', $3, $4)`,
			availableID, "wallet:"+wallet.ID+":available", wallet.ID, userID,
			reservedID, "wallet:"+wallet.ID+":reserved"); err != nil {
			return nil, fault.New("service_unavailable", "无法创建钱包账户")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.wallets
			SET available_account_id = $2, reserved_account_id = $3, version = version + 1, updated_at = $4
			WHERE id = $1`, wallet.ID, availableID, reservedID, s.now()); err != nil {
			return nil, fault.New("service_unavailable", "无法绑定钱包账户")
		}
		wallet.AvailableAccountID, wallet.ReservedAccountID = availableID, reservedID
		wallet.Version++
	}
	return wallet, nil
}

func scanWallet(row pgx.Row) (*walletRow, error) {
	var wallet walletRow
	if err := row.Scan(&wallet.ID, &wallet.UserID, &wallet.AvailableMinor, &wallet.ReservedMinor,
		&wallet.TikuAccruedMicros, &wallet.Frozen, &wallet.FrozenReason, &wallet.Version,
		&wallet.AvailableAccountID, &wallet.ReservedAccountID, &wallet.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "钱包不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取钱包")
	}
	return &wallet, nil
}

func (s *Service) lockWalletByID(ctx context.Context, tx pgx.Tx, walletID string) (*walletRow, error) {
	if !web.ValidID(walletID) {
		return nil, fault.New("validation_failed", "钱包编号无效")
	}
	wallet, err := scanWallet(tx.QueryRow(ctx, `SELECT id, user_id, available_minor, reserved_minor,
		tiku_accrued_micros, frozen, coalesce(frozen_reason, ''), version,
		coalesce(available_account_id::text, ''), coalesce(reserved_account_id::text, ''), updated_at
		FROM public.wallets WHERE id = $1 FOR UPDATE`, walletID))
	if err != nil {
		return nil, err
	}
	return wallet, nil
}

func (s *Service) GetWallet(ctx context.Context, userID string) (WalletSnapshot, error) {
	var snapshot WalletSnapshot
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		wallet, err := s.ensureWallet(ctx, tx, userID)
		if err != nil {
			return err
		}
		snapshot = snapshotWallet(wallet)
		return nil
	})
	return snapshot, err
}

func (s *Service) reserveFunds(ctx context.Context, tx pgx.Tx, wallet *walletRow, amount int64) error {
	if wallet.Frozen {
		return fault.New("conflict", "钱包已冻结，请先完成对账")
	}
	if amount <= 0 || wallet.AvailableMinor < amount {
		return fault.New("insufficient_balance", "可用余额不足")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET available_minor = available_minor - $2, reserved_minor = reserved_minor + $2,
		    version = version + 1, updated_at = $3
		WHERE id = $1 AND frozen = false AND available_minor >= $2`,
		wallet.ID, amount, s.now())
	if err != nil {
		return fault.New("service_unavailable", "无法更新钱包预占")
	}
	if tag.RowsAffected() != 1 {
		return fault.New("insufficient_balance", "可用余额不足或钱包已冻结")
	}
	wallet.AvailableMinor -= amount
	wallet.ReservedMinor += amount
	wallet.Version++
	return nil
}

func (s *Service) captureFunds(ctx context.Context, tx pgx.Tx, wallet *walletRow, amount int64) error {
	if amount <= 0 || wallet.ReservedMinor < amount {
		return fault.New("conflict", "预占余额不足，不能结算")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET reserved_minor = reserved_minor - $2, version = version + 1, updated_at = $3
		WHERE id = $1 AND reserved_minor >= $2`, wallet.ID, amount, s.now())
	if err != nil {
		return fault.New("service_unavailable", "无法更新钱包结算")
	}
	if tag.RowsAffected() != 1 {
		return fault.New("conflict", "预占余额不足，不能结算")
	}
	wallet.ReservedMinor -= amount
	wallet.Version++
	return nil
}

func (s *Service) releaseFunds(ctx context.Context, tx pgx.Tx, wallet *walletRow, amount int64) error {
	if amount <= 0 || wallet.ReservedMinor < amount {
		return fault.New("conflict", "预占余额不足，不能释放")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET reserved_minor = reserved_minor - $2, available_minor = available_minor + $2,
		    version = version + 1, updated_at = $3
		WHERE id = $1 AND reserved_minor >= $2`, wallet.ID, amount, s.now())
	if err != nil {
		return fault.New("service_unavailable", "无法更新钱包释放")
	}
	if tag.RowsAffected() != 1 {
		return fault.New("conflict", "预占余额不足，不能释放")
	}
	wallet.ReservedMinor -= amount
	wallet.AvailableMinor += amount
	wallet.Version++
	return nil
}

func (s *Service) creditFunds(ctx context.Context, tx pgx.Tx, wallet *walletRow, amount int64) error {
	if amount <= 0 {
		return fault.New("validation_failed", "入账金额必须大于零")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET available_minor = available_minor + $2, version = version + 1, updated_at = $3
		WHERE id = $1`, wallet.ID, amount, s.now())
	if err != nil || tag.RowsAffected() != 1 {
		return fault.New("service_unavailable", "无法更新钱包入账")
	}
	wallet.AvailableMinor += amount
	wallet.Version++
	return nil
}

func (s *Service) debitFunds(ctx context.Context, tx pgx.Tx, wallet *walletRow, amount int64) error {
	if wallet.Frozen {
		return fault.New("conflict", "钱包已冻结，请先完成对账")
	}
	if amount <= 0 || wallet.AvailableMinor < amount {
		return fault.New("insufficient_balance", "可用余额不足")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET available_minor = available_minor - $2, version = version + 1, updated_at = $3
		WHERE id = $1 AND frozen = false AND available_minor >= $2`, wallet.ID, amount, s.now())
	if err != nil || tag.RowsAffected() != 1 {
		return fault.New("insufficient_balance", "可用余额不足或钱包已冻结")
	}
	wallet.AvailableMinor -= amount
	wallet.Version++
	return nil
}

func (s *Service) setTikuAccrual(ctx context.Context, tx pgx.Tx, wallet *walletRow, micros int64) error {
	if micros < 0 {
		return fault.New("validation_failed", "题库尾差不能为负")
	}
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET tiku_accrued_micros = $2, version = version + 1, updated_at = $3
		WHERE id = $1`, wallet.ID, micros, s.now())
	if err != nil || tag.RowsAffected() != 1 {
		return fault.New("service_unavailable", "无法更新题库计费尾差")
	}
	wallet.TikuAccruedMicros = micros
	wallet.Version++
	return nil
}

func (s *Service) freezeWallet(ctx context.Context, tx pgx.Tx, walletID, reason string) error {
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET frozen = true, frozen_reason = $2, version = version + 1, updated_at = $3
		WHERE id = $1`, walletID, reason, s.now())
	if err != nil || tag.RowsAffected() != 1 {
		return fault.New("service_unavailable", "无法冻结钱包")
	}
	return nil
}

func (s *Service) unfreezeWallet(ctx context.Context, tx pgx.Tx, walletID string) error {
	tag, err := tx.Exec(ctx, `UPDATE public.wallets
		SET frozen = false, frozen_reason = NULL, version = version + 1, updated_at = $2
		WHERE id = $1`, walletID, s.now())
	if err != nil || tag.RowsAffected() != 1 {
		return fault.New("service_unavailable", "无法解除钱包冻结")
	}
	return nil
}

// WalletEntry 以 journal 为单位汇总该钱包的可用/预占变化，避免暴露系统账户。
type WalletEntry struct {
	JournalID           string `json:"journal_id"`
	Kind                string `json:"kind"`
	Reason              string `json:"reason"`
	AvailableDeltaMinor string `json:"available_delta_minor"`
	ReservedDeltaMinor  string `json:"reserved_delta_minor"`
	PriceVersionID      string `json:"price_version_id,omitempty"`
	UsageID             string `json:"usage_id,omitempty"`
	OrderID             string `json:"order_id,omitempty"`
	CreatedAt           string `json:"created_at"`
	createdAtValue      time.Time
}

type WalletEntryPage struct {
	Items      []WalletEntry `json:"items"`
	NextCursor string        `json:"next_cursor"`
}

type cursorValue struct {
	createdAt time.Time
	id        string
}

func encodeCursor(value cursorValue) string {
	raw := value.createdAt.UTC().Format(time.RFC3339Nano) + "|" + value.id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (cursorValue, error) {
	if value == "" {
		return cursorValue{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursorValue{}, fault.New("validation_failed", "分页游标无效")
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 || !web.ValidID(parts[1]) {
		return cursorValue{}, fault.New("validation_failed", "分页游标无效")
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return cursorValue{}, fault.New("validation_failed", "分页游标无效")
	}
	return cursorValue{createdAt: createdAt, id: parts[1]}, nil
}

func (s *Service) ListWalletEntries(ctx context.Context, walletID, cursor string, limit int) (WalletEntryPage, error) {
	if limit < 1 || limit > 100 {
		return WalletEntryPage{}, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	decoded, err := decodeCursor(cursor)
	if err != nil {
		return WalletEntryPage{}, err
	}
	where := "j.wallet_id = $1"
	args := []any{walletID}
	if decoded.id != "" {
		where += " AND (j.created_at < $2 OR (j.created_at = $2 AND j.id < $3))"
		args = append(args, decoded.createdAt, decoded.id)
	}
	args = append(args, limit+1)
	query := `SELECT j.id, j.kind, j.reason,
		coalesce(sum(p.amount_minor) FILTER (WHERE a.kind = 'user_available'), 0)::bigint,
		coalesce(sum(p.amount_minor) FILTER (WHERE a.kind = 'user_reserved'), 0)::bigint,
		coalesce(j.price_version_id::text, ''), coalesce(j.usage_id::text, ''),
		coalesce(j.order_id::text, ''), j.created_at
		FROM public.journals j
		JOIN public.postings p ON p.journal_id = j.id
		JOIN public.ledger_accounts a ON a.id = p.account_id AND a.wallet_id = j.wallet_id
		WHERE ` + where + `
		GROUP BY j.id
		ORDER BY j.created_at DESC, j.id DESC
		LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return WalletEntryPage{}, fault.New("service_unavailable", "无法读取钱包流水")
	}
	defer rows.Close()
	items := make([]WalletEntry, 0, limit+1)
	for rows.Next() {
		var item WalletEntry
		var available, reserved int64
		var createdAt time.Time
		if err := rows.Scan(&item.JournalID, &item.Kind, &item.Reason, &available, &reserved,
			&item.PriceVersionID, &item.UsageID, &item.OrderID, &createdAt); err != nil {
			return WalletEntryPage{}, fault.New("service_unavailable", "无法读取钱包流水")
		}
		item.AvailableDeltaMinor = formatSigned(available)
		item.ReservedDeltaMinor = formatSigned(reserved)
		item.CreatedAt = createdAt.UTC().Format(time.RFC3339)
		item.createdAtValue = createdAt
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return WalletEntryPage{}, fault.New("service_unavailable", "无法读取钱包流水")
	}
	page := WalletEntryPage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		last := items[limit-1]
		page.NextCursor = encodeCursor(cursorValue{createdAt: last.createdAtValue, id: last.JournalID})
	}
	return page, nil
}

type walletRebuild struct {
	WalletID        string
	AvailableCached int64
	AvailableLedger int64
	ReservedCached  int64
	ReservedLedger  int64
	ReservedHolds   int64
	Matched         bool
}

func (s *Service) rebuildWallet(ctx context.Context, tx pgx.Tx, walletID string) (walletRebuild, error) {
	wallet, err := scanWallet(tx.QueryRow(ctx, `SELECT id, user_id, available_minor, reserved_minor,
		tiku_accrued_micros, frozen, coalesce(frozen_reason, ''), version,
		coalesce(available_account_id::text, ''), coalesce(reserved_account_id::text, ''), updated_at
		FROM public.wallets WHERE id = $1 FOR UPDATE`, walletID))
	if err != nil {
		return walletRebuild{}, err
	}
	var available, reserved int64
	if err := tx.QueryRow(ctx, `SELECT
		coalesce(sum(p.amount_minor) FILTER (WHERE a.kind = 'user_available'), 0)::bigint,
		coalesce(sum(p.amount_minor) FILTER (WHERE a.kind = 'user_reserved'), 0)::bigint
		FROM public.postings p
		JOIN public.ledger_accounts a ON a.id = p.account_id
		WHERE a.wallet_id = $1`, walletID).Scan(&available, &reserved); err != nil {
		return walletRebuild{}, fault.New("service_unavailable", "无法从账本重建余额")
	}
	var holds int64
	if err := tx.QueryRow(ctx, `SELECT coalesce(sum(amount_minor - settled_minor - released_minor), 0)::bigint
		FROM public.holds WHERE wallet_id = $1 AND status = 'open'`, walletID).Scan(&holds); err != nil {
		return walletRebuild{}, fault.New("service_unavailable", "无法核对预占余额")
	}
	result := walletRebuild{
		WalletID: walletID, AvailableCached: wallet.AvailableMinor, AvailableLedger: available,
		ReservedCached: wallet.ReservedMinor, ReservedLedger: reserved, ReservedHolds: holds,
	}
	result.Matched = available == wallet.AvailableMinor && reserved == wallet.ReservedMinor && holds == reserved
	return result, nil
}
