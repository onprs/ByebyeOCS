package app

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

type taskBilling struct{ book *ledger.Service }

func (b *taskBilling) Quote(ctx context.Context, tx pgx.Tx, userID, taskID, taskKind string) (learning.BillingQuote, error) {
	var point string
	if err := tx.QueryRow(ctx, `SELECT task_point_id FROM public.learning_tasks WHERE id=$1 AND user_id=$2`, taskID, userID).Scan(&point); err != nil || point == "" {
		return learning.BillingQuote{}, fault.New("validation_failed", "普通附件不能计为有效任务点")
	}
	quote, err := b.book.QuoteInTransaction(ctx, tx, ledger.QuoteInput{Scope: ledger.ScopeServiceTaskPoint, QuantityScaled: 1})
	if err != nil {
		return learning.BillingQuote{}, err
	}
	if quote.PriceVersionID == "" || !quote.Chargeable {
		return learning.BillingQuote{Currency: "CNY", Configured: false, State: quote.State, PriceVersion: quote.PriceVersionID}, nil
	}
	quantity := int64(1)
	for range quote.UnitPrecision {
		quantity *= 10
	}
	if quantity != 1 {
		quote, err = b.book.QuoteInTransaction(ctx, tx, ledger.QuoteInput{Scope: ledger.ScopeServiceTaskPoint, PriceVersionID: quote.PriceVersionID, QuantityScaled: quantity})
		if err != nil {
			return learning.BillingQuote{}, err
		}
	}
	amount, err := strconv.ParseInt(quote.ChargeMinor, 10, 64)
	if err != nil || amount < 0 {
		return learning.BillingQuote{}, fault.New("validation_failed", "有效任务点报价无效")
	}
	return learning.BillingQuote{Configured: quote.Chargeable, AmountMinor: amount, Currency: "CNY", PriceVersion: quote.PriceVersionID, State: quote.State}, nil
}
func (b *taskBilling) Reserve(ctx context.Context, tx pgx.Tx, userID, taskID, key string, quote learning.BillingQuote) (string, error) {
	if !quote.Configured || quote.AmountMinor <= 0 {
		return "", fault.New("not_configured", "任务没有可预占的收费报价")
	}
	hold, err := b.book.Reserve(ctx, tx, userID, key, quote.AmountMinor)
	if err != nil {
		return "", err
	}
	tag, err := tx.Exec(ctx, `UPDATE public.holds SET source_type='service_task_point',source_id=$3,version=version+1,updated_at=now() WHERE id=$1 AND user_id=$2`, hold, userID, taskID)
	if err != nil || tag.RowsAffected() != 1 {
		return "", fault.New("service_unavailable", "无法绑定任务预占")
	}
	return hold, nil
}

type confirmedTask struct {
	quote                                              learning.BillingQuote
	noCharge                                           bool
	userID, taskID, point, priceID, holdID, holdStatus string
	quantity                                           int64
	confirmedAt                                        *time.Time
}

func loadConfirmedTask(ctx context.Context, tx pgx.Tx, userID, taskID string) (confirmedTask, error) {
	record := confirmedTask{userID: userID, taskID: taskID, quantity: 1}
	var state, platformStatus string
	var quoteJSON []byte
	err := tx.QueryRow(ctx, `SELECT state,platform_status,task_point_id,COALESCE(billing_hold_id::text,''),COALESCE(price_version_id::text,''),confirmed_at,pre_completed_no_charge,billing_quote FROM public.learning_tasks WHERE id=$1 AND user_id=$2 FOR UPDATE`, taskID, userID).Scan(&state, &platformStatus, &record.point, &record.holdID, &record.priceID, &record.confirmedAt, &record.noCharge, &quoteJSON)
	if err != nil || record.point == "" || state != "confirmed" || platformStatus != "confirmed" || record.confirmedAt == nil {
		return record, fault.New("validation_failed", "用量缺少可信的平台确认或归属不匹配")
	}
	if json.Unmarshal(quoteJSON, &record.quote) != nil {
		return record, fault.New("conflict", "任务原始报价快照不可读，需要复核")
	}
	if record.priceID != "" {
		var precision int
		if tx.QueryRow(ctx, `SELECT unit_precision FROM public.price_versions WHERE id=$1 AND scope='service_task_point'`, record.priceID).Scan(&precision) != nil {
			return record, fault.New("not_configured", "原任务价格快照不可用")
		}
		for range precision {
			record.quantity *= 10
		}
	}
	if record.holdID != "" {
		if tx.QueryRow(ctx, `SELECT status FROM public.holds WHERE id=$1 AND user_id=$2 AND source_type='service_task_point' AND source_id=$3`, record.holdID, userID, taskID).Scan(&record.holdStatus) != nil {
			return record, fault.New("forbidden", "任务预占归属不匹配")
		}
	}
	return record, nil
}
func (b *taskBilling) recordTaskUsage(ctx context.Context, tx pgx.Tx, record confirmedTask) (string, error) {
	key := "learning-task:" + record.taskID
	var usageID, priceID, sourceType, sourceID string
	var quantity int64
	var confirmedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT id::text,COALESCE(price_version_id::text,''),quantity_scaled,platform_confirmed_at,COALESCE(source_type,''),COALESCE(source_id,'') FROM public.usage_records WHERE user_id=$1 AND scope='service_task_point' AND stable_key=$2 FOR UPDATE`, record.userID, key).Scan(&usageID, &priceID, &quantity, &confirmedAt, &sourceType, &sourceID)
	if err == nil {
		if priceID != record.priceID || quantity != record.quantity || confirmedAt == nil || !confirmedAt.Equal(*record.confirmedAt) || sourceType != "learning_task" || sourceID != record.taskID {
			return "", fault.New("conflict", "既有任务计量与原始确认不一致，需要复核")
		}
		return usageID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", fault.New("service_unavailable", "无法读取任务计量")
	}
	uncharged := record.noCharge || record.holdID == "" || record.holdStatus == "released"
	usage, err := b.book.RecordUsage(ctx, tx, ledger.RecordUsageInput{UserID: record.userID, Scope: ledger.ScopeServiceTaskPoint, StableKey: key, SourceType: "learning_task", SourceID: record.taskID, PriceVersionID: record.priceID, QuantityScaled: record.quantity, UsageComplete: true, Uncharged: uncharged, PlatformConfirmedAt: record.confirmedAt, PlatformEvidenceRef: "learning_task:" + record.taskID + ":platform_confirmation"})
	if err != nil {
		return "", err
	}
	if !uncharged {
		tag, err := tx.Exec(ctx, `UPDATE public.usage_records SET hold_id=$2,status=CASE WHEN status='quoted' THEN 'reserved' ELSE status END,updated_at=now() WHERE id=$1 AND user_id=$3`, usage.ID, record.holdID, record.userID)
		if err != nil || tag.RowsAffected() != 1 {
			return "", fault.New("service_unavailable", "任务预占与计量绑定失败")
		}
	}
	return usage.ID, nil
}

// RecordConfirmed 在独立保存点内持久化确认事实；结算失败不会抹掉已确认用量。
func (b *taskBilling) RecordConfirmed(ctx context.Context, tx pgx.Tx, userID, taskID string) error {
	record, err := loadConfirmedTask(ctx, tx, userID, taskID)
	if err != nil {
		return err
	}
	usageID, err := b.recordTaskUsage(ctx, tx, record)
	if err != nil {
		return err
	}
	if record.holdID == "" && !record.noCharge && record.priceID != "" && record.quote.Configured && record.quote.AmountMinor == 0 {
		var amount int64
		if tx.QueryRow(ctx, `SELECT quote_minor FROM public.usage_records WHERE id=$1`, usageID).Scan(&amount) != nil || amount != 0 {
			return fault.New("conflict", "零收费确认与冻结报价不一致")
		}
		if _, err = tx.Exec(ctx, `UPDATE public.usage_records SET status='settled',cost_micros=quote_micros,charged_minor=0,settled_at=COALESCE(settled_at,now()),updated_at=now() WHERE id=$1 AND status<>'settled'`, usageID); err != nil {
			return fault.New("service_unavailable", "无法记录已确认零收费")
		}
		if _, err = tx.Exec(ctx, `UPDATE public.learning_tasks SET billing_state='settled',billing_error='' WHERE id=$1 AND user_id=$2 AND billing_state<>'settled'`, taskID, userID); err != nil {
			return fault.New("service_unavailable", "无法闭合零收费任务状态")
		}
	}
	return nil
}
func (b *taskBilling) Settle(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) error {
	_, err := b.SettleState(ctx, tx, userID, taskID, holdID, key, amountMinor)
	return err
}
func (b *taskBilling) SettleState(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key string, amountMinor int64) (string, error) {
	record, err := loadConfirmedTask(ctx, tx, userID, taskID)
	if err != nil {
		return "", err
	}
	if record.holdID != holdID || holdID == "" {
		return "", fault.New("validation_failed", "任务结算预占不匹配")
	}
	usageID, err := b.recordTaskUsage(ctx, tx, record)
	if err != nil {
		return "", err
	}
	var quoted int64
	if tx.QueryRow(ctx, `SELECT quote_minor FROM public.usage_records WHERE id=$1`, usageID).Scan(&quoted) != nil {
		return "", fault.New("service_unavailable", "无法核对任务原始报价")
	}
	if quoted != amountMinor {
		return "", fault.New("conflict", "结算与任务原始报价不一致，需要复核")
	}
	outcome, err := b.book.SettleUsage(ctx, tx, usageID)
	if err != nil {
		return "", err
	}
	if outcome.State != "settled" && outcome.State != "released" {
		return "", fault.New("billing_review_required", "任务用量需要人工复核")
	}
	return outcome.State, nil
}
func (b *taskBilling) Release(ctx context.Context, tx pgx.Tx, userID, taskID, holdID, key, reason string) error {
	var valid bool
	if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM public.holds WHERE id=$1 AND user_id=$2 AND source_type='service_task_point' AND source_id=$3)`, holdID, userID, taskID).Scan(&valid) != nil || !valid {
		return fault.New("forbidden", "任务预占归属不匹配")
	}
	return b.book.Release(ctx, tx, holdID, key)
}

type questionBilling struct{ book *ledger.Service }

func (b *questionBilling) Reserve(ctx context.Context, tx pgx.Tx, userID, key string, amountMinor int64) (string, error) {
	hold, err := b.book.Reserve(ctx, tx, userID, key, amountMinor)
	if err != nil {
		return "", err
	}
	source := key
	if i := strings.Index(key, "qq_"); i >= 0 && len(key) >= i+35 {
		source = key[i : i+35]
	}
	tag, err := tx.Exec(ctx, `UPDATE public.holds SET source_type='tiku_query',source_id=$3 WHERE id=$1 AND user_id=$2`, hold, userID, source)
	if err != nil || tag.RowsAffected() != 1 {
		return "", fault.New("service_unavailable", "无法绑定题库预占")
	}
	return hold, nil
}
func (b *questionBilling) Settle(ctx context.Context, tx pgx.Tx, hold, key string, actual int64) error {
	return b.book.Settle(ctx, tx, hold, key, actual)
}
func (b *questionBilling) Release(ctx context.Context, tx pgx.Tx, hold, key string) error {
	return b.book.Release(ctx, tx, hold, key)
}

var _ learning.Billing = (*taskBilling)(nil)
var _ learning.BillingConfirmationRecorder = (*taskBilling)(nil)
