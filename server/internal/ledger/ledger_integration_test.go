//go:build integration

package ledger

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestReserveSettleReleaseAndRebuild(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 10000)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		id, err := service.Reserve(ctx, tx, userID, "hold-a", 1000)
		holdID = id
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "settle-a", 600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "release-a")
	}); err != nil {
		t.Fatal(err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "9400" || wallet.ReservedMinor != "0" {
		t.Fatalf("钱包余额错误: %+v", wallet)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		result, err := service.rebuildWallet(ctx, tx, wallet.ID)
		if err != nil {
			return err
		}
		if !result.Matched {
			t.Fatalf("账本重建不一致: %+v", result)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
