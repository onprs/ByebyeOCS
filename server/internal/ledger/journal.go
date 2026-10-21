package ledger

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type postingInput struct {
	AccountID   string
	AmountMinor int64
}

type journalInput struct {
	Kind           string
	BusinessKey    string
	RequestDigest  string
	UserID         string
	WalletID       string
	AmountMinor    int64
	Reason         string
	Actor          web.Actor
	ReferenceType  string
	ReferenceID    string
	OrderID        string
	HoldID         string
	PriceVersionID string
	UsageID        string
	Postings       []postingInput
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Service) advisoryLocks(ctx context.Context, tx pgx.Tx, keys ...string) error {
	unique := make([]string, 0, len(keys))
	seen := make(map[string]bool, len(keys))
	for _, key := range keys {
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, key)
	}
	sort.Strings(unique)
	for _, key := range unique {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
			"byebyeocs:ledger:"+key); err != nil {
			return fault.New("service_unavailable", "无法取得账本业务锁")
		}
	}
	return nil
}

// postJournal 在调用方事务内写入一条平衡分录。相同业务键与摘要复用，不同摘要返回冲突。
func (s *Service) postJournal(ctx context.Context, tx pgx.Tx, input journalInput) (string, bool, error) {
	if err := requireTx(tx); err != nil {
		return "", false, err
	}
	if len(input.Postings) < 2 || len(input.Postings) > 64 || input.AmountMinor <= 0 ||
		input.AmountMinor > maxAmountMinor {
		return "", false, fault.New("validation_failed", "分录必须包含 2 到 64 条非零金额且总额受限")
	}
	if len(input.RequestDigest) != 64 || input.BusinessKey == "" || input.Reason == "" {
		return "", false, fault.New("validation_failed", "分录业务键、摘要或原因无效")
	}
	if err := s.advisoryLocks(ctx, tx, input.BusinessKey); err != nil {
		return "", false, err
	}
	var existingID, existingDigest string
	err := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.journals WHERE business_key = $1`,
		input.BusinessKey).Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != input.RequestDigest {
			return "", false, fault.New("conflict", "相同业务键已用于不同请求")
		}
		return existingID, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", false, fault.New("service_unavailable", "无法检查账本业务键")
	}

	var sum, positive int64
	seenAccounts := make(map[string]bool, len(input.Postings))
	for _, posting := range input.Postings {
		if posting.AccountID == "" || !web.ValidID(posting.AccountID) || posting.AmountMinor == 0 ||
			posting.AmountMinor > maxAmountMinor || posting.AmountMinor < -maxAmountMinor {
			return "", false, fault.New("validation_failed", "分录账户或金额无效或超出上限")
		}
		if seenAccounts[posting.AccountID] {
			return "", false, fault.New("validation_failed", "同一分录不能重复使用同一账户")
		}
		seenAccounts[posting.AccountID] = true
		if posting.AmountMinor > 0 {
			if positive > maxAmountMinor-posting.AmountMinor {
				return "", false, fault.New("validation_failed", "分录金额累加溢出")
			}
			positive += posting.AmountMinor
		}
		if posting.AmountMinor > 0 && sum > maxAmountMinor-posting.AmountMinor {
			return "", false, fault.New("validation_failed", "分录金额累加溢出")
		}
		if posting.AmountMinor < 0 && sum < -maxAmountMinor-posting.AmountMinor {
			return "", false, fault.New("validation_failed", "分录金额累加溢出")
		}
		sum += posting.AmountMinor
	}
	if sum != 0 || positive != input.AmountMinor {
		return "", false, fault.New("validation_failed", "分录不平衡")
	}

	journalID := newUUID()
	actorKind := input.Actor.Kind
	if actorKind == "" {
		actorKind = "system"
	}
	var actorID any
	if input.Actor.ID != "" {
		actorID = input.Actor.ID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.journals (
		id, kind, business_key, request_digest, user_id, wallet_id, currency, amount_minor,
		reason, actor_kind, actor_id, reference_type, reference_id, order_id, hold_id,
		price_version_id, usage_id, created_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		journalID, input.Kind, input.BusinessKey, input.RequestDigest,
		nullableID(input.UserID), nullableID(input.WalletID), CurrencyCNY, input.AmountMinor,
		input.Reason, actorKind, actorID, nullableText(input.ReferenceType), nullableText(input.ReferenceID),
		nullableID(input.OrderID), nullableID(input.HoldID), nullableID(input.PriceVersionID),
		nullableID(input.UsageID), s.now()); err != nil {
		if isUniqueViolation(err) {
			// 并发冲突已由咨询锁串行化；这里仍按业务键复核一次。
			var existing, requestDigest string
			if queryErr := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.journals WHERE business_key = $1`,
				input.BusinessKey).Scan(&existing, &requestDigest); queryErr == nil {
				if requestDigest != input.RequestDigest {
					return "", false, fault.New("conflict", "相同业务键已用于不同请求")
				}
				return existing, true, nil
			}
		}
		return "", false, fault.New("service_unavailable", "无法写入账本分录")
	}
	for _, posting := range input.Postings {
		if _, err := tx.Exec(ctx, `INSERT INTO public.postings (id, journal_id, account_id, amount_minor, created_at)
			VALUES ($1,$2,$3,$4,$5)`, newUUID(), journalID, posting.AccountID, posting.AmountMinor, s.now()); err != nil {
			return "", false, fault.New("service_unavailable", "无法写入账本明细")
		}
	}
	return journalID, false, nil
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Service) findNoopKey(ctx context.Context, tx pgx.Tx, businessKey, requestDigest string) (bool, error) {
	var stored string
	err := tx.QueryRow(ctx, `SELECT request_digest FROM public.ledger_request_keys
		WHERE business_key = $1`, businessKey).Scan(&stored)
	if err == pgx.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fault.New("service_unavailable", "无法检查零金额结算幂等键")
	}
	if stored != requestDigest {
		return false, fault.New("conflict", "相同业务键已用于不同请求")
	}
	return true, nil
}

func (s *Service) rememberNoopKey(ctx context.Context, tx pgx.Tx, businessKey, requestDigest, kind string) error {
	var inserted int
	err := tx.QueryRow(ctx, `INSERT INTO public.ledger_request_keys (business_key, request_digest, kind, created_at)
		VALUES ($1,$2,$3,$4) ON CONFLICT (business_key) DO NOTHING RETURNING 1`,
		businessKey, requestDigest, kind, s.now()).Scan(&inserted)
	if err == pgx.ErrNoRows {
		ok, checkErr := s.findNoopKey(ctx, tx, businessKey, requestDigest)
		if checkErr != nil {
			return checkErr
		}
		if !ok {
			return fault.New("conflict", "相同业务键已用于不同请求")
		}
		return nil
	}
	if err != nil {
		return fault.New("service_unavailable", "无法保存零金额结算幂等键")
	}
	return nil
}

type holdRow struct {
	ID            string
	UserID        string
	WalletID      string
	BusinessKey   string
	RequestDigest string
	AmountMinor   int64
	SettledMinor  int64
	ReleasedMinor int64
	Status        string
	SourceType    string
	SourceID      string
}

func scanHold(row pgx.Row) (*holdRow, error) {
	var hold holdRow
	if err := row.Scan(&hold.ID, &hold.UserID, &hold.WalletID, &hold.BusinessKey, &hold.RequestDigest,
		&hold.AmountMinor, &hold.SettledMinor, &hold.ReleasedMinor, &hold.Status,
		&hold.SourceType, &hold.SourceID); err != nil {
		if err == pgx.ErrNoRows {
			return nil, fault.New("not_found", "预占不存在")
		}
		return nil, fault.New("service_unavailable", "无法读取预占")
	}
	return &hold, nil
}

func (s *Service) holdRemaining(hold *holdRow) int64 {
	return hold.AmountMinor - hold.SettledMinor - hold.ReleasedMinor
}

// Reserve 在调用方事务内预占金额。相同 key 与摘要返回同一 hold；不同摘要返回冲突。
func (s *Service) Reserve(ctx context.Context, tx pgx.Tx, userID, key string, amountMinor int64) (string, error) {
	if err := requireTx(tx); err != nil {
		return "", err
	}
	if !web.ValidID(userID) {
		return "", fault.New("validation_failed", "用户编号无效")
	}
	if err := validKey(key); err != nil {
		return "", err
	}
	if amountMinor <= 0 || amountMinor > maxAmountMinor {
		return "", fault.New("validation_failed", "预占金额必须为正且不超过上限")
	}
	requestDigest := digest("reserve", userID, key, formatAmount(amountMinor))
	if err := s.advisoryLocks(ctx, tx, "hold:"+key); err != nil {
		return "", err
	}
	var existingID, existingDigest string
	err := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.holds WHERE business_key = $1`, key).
		Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != requestDigest {
			return "", fault.New("conflict", "相同预占业务键已用于不同金额或用户")
		}
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return "", fault.New("service_unavailable", "无法检查预占业务键")
	}
	if err := s.checkChargingGate(ctx); err != nil {
		return "", err
	}
	wallet, err := s.ensureWallet(ctx, tx, userID)
	if err != nil {
		return "", err
	}
	if err := s.reserveFunds(ctx, tx, wallet, amountMinor); err != nil {
		return "", err
	}
	holdID := newUUID()
	if _, err := tx.Exec(ctx, `INSERT INTO public.holds (
		id, user_id, wallet_id, business_key, request_digest, amount_minor,
		settled_minor, released_minor, status, source_type, created_at, updated_at, version
	) VALUES ($1,$2,$3,$4,$5,$6,0,0,'open','other',$7,$7,1)`,
		holdID, userID, wallet.ID, key, requestDigest, amountMinor, s.now()); err != nil {
		if isUniqueViolation(err) {
			var id, storedDigest string
			if queryErr := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.holds WHERE business_key = $1`, key).
				Scan(&id, &storedDigest); queryErr == nil {
				if storedDigest != requestDigest {
					return "", fault.New("conflict", "相同预占业务键已用于不同金额或用户")
				}
				return id, nil
			}
		}
		return "", fault.New("service_unavailable", "无法创建预占")
	}
	_, reused, err := s.postJournal(ctx, tx, journalInput{
		Kind: "hold", BusinessKey: "hold:" + key, RequestDigest: requestDigest,
		UserID: userID, WalletID: wallet.ID, AmountMinor: amountMinor, Reason: "预占余额",
		Actor: actorFromID("system", ""), HoldID: holdID,
		Postings: []postingInput{
			{AccountID: wallet.AvailableAccountID, AmountMinor: -amountMinor},
			{AccountID: wallet.ReservedAccountID, AmountMinor: amountMinor},
		},
	})
	if err != nil {
		return "", err
	}
	if reused {
		return "", fault.New("conflict", "预占业务键与既有分录状态不一致，请人工核对")
	}
	if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
		"hold_id": holdID, "entry": "hold",
	}); err != nil {
		return "", err
	}
	return holdID, nil
}

// Settle 在调用方事务内按实际金额结算预占；actualMinor 为 0 时不产生分录。
func (s *Service) Settle(ctx context.Context, tx pgx.Tx, holdID, key string, actualMinor int64) error {
	_, err := s.settleHold(ctx, tx, holdID, key, actualMinor)
	return err
}

func (s *Service) settleHold(ctx context.Context, tx pgx.Tx, holdID, key string, actualMinor int64) (string, error) {
	return s.settleHoldMeta(ctx, tx, holdID, key, actualMinor, "", "")
}

func (s *Service) settleHoldMeta(ctx context.Context, tx pgx.Tx, holdID, key string, actualMinor int64,
	usageID, priceVersionID string) (string, error) {
	if err := requireTx(tx); err != nil {
		return "", err
	}
	if !web.ValidID(holdID) {
		return "", fault.New("validation_failed", "预占编号无效")
	}
	if err := validKey(key); err != nil {
		return "", err
	}
	if actualMinor < 0 || actualMinor > maxAmountMinor {
		return "", fault.New("validation_failed", "结算金额必须为非负整数且不超过上限")
	}
	requestDigest := digest("settle", holdID, key, formatAmount(actualMinor))
	businessKey := "capture:" + key
	if err := s.advisoryLocks(ctx, tx, businessKey); err != nil {
		return "", err
	}
	var existingID, existingDigest string
	err := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.journals WHERE business_key = $1`, businessKey).
		Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != requestDigest {
			return "", fault.New("conflict", "相同结算业务键已用于不同金额或预占")
		}
		return existingID, nil
	}
	if err != pgx.ErrNoRows {
		return "", fault.New("service_unavailable", "无法检查结算业务键")
	}
	if reusedNoop, err := s.findNoopKey(ctx, tx, businessKey, requestDigest); err != nil {
		return "", err
	} else if reusedNoop {
		return "", nil
	}
	var walletID string
	if err := tx.QueryRow(ctx, `SELECT wallet_id FROM public.holds WHERE id = $1`, holdID).Scan(&walletID); err != nil {
		if err == pgx.ErrNoRows {
			return "", fault.New("not_found", "预占不存在")
		}
		return "", fault.New("service_unavailable", "无法读取预占")
	}
	if actualMinor > 0 {
		if err := s.checkChargingGate(ctx); err != nil {
			return "", err
		}
	}
	wallet, err := s.lockWalletByID(ctx, tx, walletID)
	if err != nil {
		return "", err
	}
	hold, err := scanHold(tx.QueryRow(ctx, `SELECT id, user_id, wallet_id, business_key, request_digest,
		amount_minor, settled_minor, released_minor, status, source_type, coalesce(source_id, '')
		FROM public.holds WHERE id = $1 FOR UPDATE`, holdID))
	if err != nil {
		return "", err
	}
	if hold.WalletID != wallet.ID {
		return "", fault.New("conflict", "预占归属与钱包不一致")
	}
	if actualMinor == 0 {
		if err := s.rememberNoopKey(ctx, tx, businessKey, requestDigest, "capture_zero"); err != nil {
			return "", err
		}
		return "", nil
	}
	remaining := s.holdRemaining(hold)
	if actualMinor > remaining {
		return "", fault.New("conflict", "结算金额超过预占剩余金额")
	}
	journalID, reused, err := s.postJournal(ctx, tx, journalInput{
		Kind: "capture", BusinessKey: businessKey, RequestDigest: requestDigest,
		UserID: hold.UserID, WalletID: wallet.ID, AmountMinor: actualMinor, Reason: "预占结算",
		Actor: actorFromID("system", ""), HoldID: hold.ID, UsageID: usageID,
		PriceVersionID: priceVersionID,
		Postings: []postingInput{
			{AccountID: wallet.ReservedAccountID, AmountMinor: -actualMinor},
			{AccountID: systemServiceRevenue, AmountMinor: actualMinor},
		},
	})
	if err != nil {
		return "", err
	}
	if reused {
		return journalID, nil
	}
	if err := s.captureFunds(ctx, tx, wallet, actualMinor); err != nil {
		return "", err
	}
	settled := hold.SettledMinor + actualMinor
	nextRemaining := hold.AmountMinor - settled - hold.ReleasedMinor
	status := "open"
	if nextRemaining == 0 {
		status = "settled"
	}
	if _, err := tx.Exec(ctx, `UPDATE public.holds SET settled_minor = $2, status = $3,
		version = version + 1, updated_at = $4 WHERE id = $1`, hold.ID, settled, status, s.now()); err != nil {
		return "", fault.New("service_unavailable", "无法更新预占结算状态")
	}
	if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
		"hold_id": hold.ID, "entry": "capture", "journal_id": journalID,
	}); err != nil {
		return "", err
	}
	return journalID, nil
}

// Release 在调用方事务内释放预占剩余金额；已经无剩余时幂等返回。
func (s *Service) Release(ctx context.Context, tx pgx.Tx, holdID, key string) error {
	if err := requireTx(tx); err != nil {
		return err
	}
	if !web.ValidID(holdID) {
		return fault.New("validation_failed", "预占编号无效")
	}
	if err := validKey(key); err != nil {
		return err
	}
	requestDigest := digest("release", holdID, key)
	businessKey := "release:" + key
	if err := s.advisoryLocks(ctx, tx, businessKey); err != nil {
		return err
	}
	var existingID, existingDigest string
	err := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.journals WHERE business_key = $1`, businessKey).
		Scan(&existingID, &existingDigest)
	if err == nil {
		if existingDigest != requestDigest {
			return fault.New("conflict", "相同释放业务键已用于不同预占")
		}
		return nil
	}
	if err != pgx.ErrNoRows {
		return fault.New("service_unavailable", "无法检查释放业务键")
	}
	if reusedNoop, err := s.findNoopKey(ctx, tx, businessKey, requestDigest); err != nil {
		return err
	} else if reusedNoop {
		return nil
	}
	var walletID string
	if err := tx.QueryRow(ctx, `SELECT wallet_id FROM public.holds WHERE id = $1`, holdID).Scan(&walletID); err != nil {
		if err == pgx.ErrNoRows {
			return fault.New("not_found", "预占不存在")
		}
		return fault.New("service_unavailable", "无法读取预占")
	}
	wallet, err := s.lockWalletByID(ctx, tx, walletID)
	if err != nil {
		return err
	}
	hold, err := scanHold(tx.QueryRow(ctx, `SELECT id, user_id, wallet_id, business_key, request_digest,
		amount_minor, settled_minor, released_minor, status, source_type, coalesce(source_id, '')
		FROM public.holds WHERE id = $1 FOR UPDATE`, holdID))
	if err != nil {
		return err
	}
	remaining := s.holdRemaining(hold)
	if remaining == 0 {
		return s.rememberNoopKey(ctx, tx, businessKey, requestDigest, "release_zero")
	}
	_, reused, err := s.postJournal(ctx, tx, journalInput{
		Kind: "release", BusinessKey: businessKey, RequestDigest: requestDigest,
		UserID: hold.UserID, WalletID: wallet.ID, AmountMinor: remaining, Reason: "释放预占",
		Actor: actorFromID("system", ""), HoldID: hold.ID,
		Postings: []postingInput{
			{AccountID: wallet.ReservedAccountID, AmountMinor: -remaining},
			{AccountID: wallet.AvailableAccountID, AmountMinor: remaining},
		},
	})
	if err != nil {
		return err
	}
	if reused {
		return nil
	}
	if err := s.releaseFunds(ctx, tx, wallet, remaining); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.holds SET released_minor = released_minor + $2,
		status = 'released', version = version + 1, updated_at = $3 WHERE id = $1`,
		hold.ID, remaining, s.now()); err != nil {
		return fault.New("service_unavailable", "无法更新预占释放状态")
	}
	if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
		"hold_id": hold.ID, "entry": "release",
	}); err != nil {
		return err
	}
	return nil
}
