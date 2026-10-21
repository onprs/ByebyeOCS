package questions

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// recordPriceVersion 保留每个公开模型已发布的费率版本，重用版本只允许同一内容。
func recordPriceVersion(ctx context.Context, tx pgx.Tx, offeringID string, price *OfferingPricing, actor web.Actor) error {
	if price == nil {
		return nil
	}
	if err := price.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(price)
	if err != nil {
		return fault.New("validation_failed", "模型价格无法保存")
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.question_offering_price_versions(id,offering_id,version,pricing,created_by)VALUES($1,$2,$3,$4,$5) ON CONFLICT(offering_id,version)DO NOTHING`, web.NewID(), offeringID, price.Version, body, actor.ID)
	if err != nil {
		return fault.New("service_unavailable", "无法保存模型价格版本")
	}
	var same bool
	if err = tx.QueryRow(ctx, `SELECT pricing=$3::jsonb FROM public.question_offering_price_versions WHERE offering_id=$1 AND version=$2`, offeringID, price.Version, body).Scan(&same); err != nil {
		return fault.New("service_unavailable", "无法核对模型价格版本")
	}
	if !same {
		return fault.New("conflict", "此价格版本已有不同费率，请发布新版本")
	}
	return nil
}
