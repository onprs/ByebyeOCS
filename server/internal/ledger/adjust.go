package ledger

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type WalletAdjustmentInput struct {
	WalletID    string
	Direction   string
	AmountMinor string
	Reason      string
	Reference   string
}

type WalletAdjustmentResult struct {
	Wallet      WalletSnapshot `json:"wallet"`
	JournalID   string         `json:"journal_id"`
	Direction   string         `json:"direction"`
	AmountMinor string         `json:"amount_minor"`
}

func (s *Service) AdjustWallet(ctx context.Context, actor web.Actor, idempotencyKey string,
	input WalletAdjustmentInput) (*WalletAdjustmentResult, error) {
	if err := requireAgent(actor, "admin", PermWalletAdjust); err != nil {
		return nil, err
	}
	if err := requireFresh(actor, s.now()); err != nil {
		return nil, err
	}
	if err := validKey(idempotencyKey); err != nil {
		return nil, fault.New("validation_failed", "缺少有效的人工调整幂等键")
	}
	if !web.ValidID(input.WalletID) {
		return nil, fault.New("validation_failed", "钱包编号无效")
	}
	if input.Direction != "credit" && input.Direction != "debit" {
		return nil, fault.New("validation_failed", "人工调整方向必须为 credit 或 debit")
	}
	amount, err := parseAmount(input.AmountMinor)
	if err != nil {
		return nil, err
	}
	reason := trimSpace(input.Reason)
	if len(reason) < 5 || len(reason) > 500 {
		return nil, fault.New("validation_failed", "人工调整必须填写 5 到 500 字符的理由")
	}
	if input.Reference != "" && len(input.Reference) > 200 {
		return nil, fault.New("validation_failed", "关联单据编号超出限制")
	}
	businessKey := "adjust:" + actor.ID + ":" + digest(idempotencyKey)[:24]
	requestDigest := digest("adjust", actor.ID, input.WalletID, input.Direction,
		formatAmount(amount), reason, input.Reference)
	var result *WalletAdjustmentResult
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := s.advisoryLocks(ctx, tx, businessKey); err != nil {
			return err
		}
		var existingID, existingDigest string
		err := tx.QueryRow(ctx, `SELECT id, request_digest FROM public.journals WHERE business_key = $1`,
			businessKey).Scan(&existingID, &existingDigest)
		if err == nil {
			if existingDigest != requestDigest {
				return fault.New("conflict", "相同幂等键已用于不同人工调整")
			}
			wallet, err := s.lockWalletByID(ctx, tx, input.WalletID)
			if err != nil {
				return err
			}
			result = &WalletAdjustmentResult{Wallet: snapshotWallet(wallet), JournalID: existingID,
				Direction: input.Direction, AmountMinor: formatAmount(amount)}
			return nil
		}
		if err != pgx.ErrNoRows {
			return fault.New("service_unavailable", "无法检查人工调整幂等键")
		}
		wallet, err := s.lockWalletByID(ctx, tx, input.WalletID)
		if err != nil {
			return err
		}
		if wallet.Frozen {
			return fault.New("conflict", "钱包已冻结，请先完成对账")
		}
		var postings []postingInput
		kind := "manual_credit"
		if input.Direction == "credit" {
			postings = []postingInput{
				{AccountID: wallet.AvailableAccountID, AmountMinor: amount},
				{AccountID: systemPlatformAdjustment, AmountMinor: -amount},
			}
		} else {
			kind = "manual_debit"
			if wallet.Frozen {
				return fault.New("conflict", "钱包已冻结，请先完成对账")
			}
			if wallet.AvailableMinor < amount {
				return fault.New("insufficient_balance", "可用余额不足")
			}
			postings = []postingInput{
				{AccountID: systemPlatformAdjustment, AmountMinor: amount},
				{AccountID: wallet.AvailableAccountID, AmountMinor: -amount},
			}
		}
		journalID, reused, err := s.postJournal(ctx, tx, journalInput{
			Kind: kind, BusinessKey: businessKey, RequestDigest: requestDigest,
			UserID: wallet.UserID, WalletID: wallet.ID, AmountMinor: amount,
			Reason: reason, Actor: actor, ReferenceType: referenceType(input.Reference),
			ReferenceID: input.Reference, Postings: postings,
		})
		if err != nil {
			return err
		}
		if reused {
			return fault.New("conflict", "人工调整幂等键与既有分录不一致")
		}
		if input.Direction == "credit" {
			if err := s.creditFunds(ctx, tx, wallet, amount); err != nil {
				return err
			}
		} else {
			if err := s.debitFunds(ctx, tx, wallet, amount); err != nil {
				return err
			}
		}
		if err := s.audit(ctx, tx, actor, "wallet.adjusted", "wallet", wallet.ID,
			"人工"+mapDirection(input.Direction)+"："+reason); err != nil {
			return err
		}
		if err := s.recordWalletEvent(ctx, tx, wallet, "wallet.updated", map[string]any{
			"entry": "manual_adjustment", "journal_id": journalID, "direction": input.Direction,
		}); err != nil {
			return err
		}
		result = &WalletAdjustmentResult{Wallet: snapshotWallet(wallet), JournalID: journalID,
			Direction: input.Direction, AmountMinor: formatAmount(amount)}
		return nil
	})
	return result, err
}

func referenceType(value string) string {
	if value == "" {
		return ""
	}
	return "admin_reference"
}

func mapDirection(value string) string {
	if value == "credit" {
		return "增加"
	}
	return "扣减"
}

type AdminWalletEntries struct {
	Wallet     WalletSnapshot `json:"wallet"`
	Items      []WalletEntry  `json:"items"`
	NextCursor string         `json:"next_cursor"`
}

func (s *Service) AdminWalletEntries(ctx context.Context, actor web.Actor, walletID, cursor string,
	limit int) (*AdminWalletEntries, error) {
	if err := requireAgent(actor, "admin", PermBillingRead); err != nil {
		return nil, err
	}
	if !web.ValidID(walletID) {
		return nil, fault.New("validation_failed", "钱包编号无效")
	}
	wallet, err := scanWallet(s.pool.QueryRow(ctx, `SELECT id, user_id, available_minor, reserved_minor,
		tiku_accrued_micros, frozen, coalesce(frozen_reason, ''), version,
		coalesce(available_account_id::text, ''), coalesce(reserved_account_id::text, ''), updated_at
		FROM public.wallets WHERE id = $1`, walletID))
	if err != nil {
		return nil, err
	}
	page, err := s.ListWalletEntries(ctx, walletID, cursor, limit)
	if err != nil {
		return nil, err
	}
	return &AdminWalletEntries{Wallet: snapshotWallet(wallet), Items: page.Items,
		NextCursor: page.NextCursor}, nil
}
