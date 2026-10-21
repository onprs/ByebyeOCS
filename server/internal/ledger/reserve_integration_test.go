//go:build integration

package ledger

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestConcurrentReserveDoesNotOverdraft(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 1000)
	var wait sync.WaitGroup
	results := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			results <- service.inTx(ctx, func(tx pgx.Tx) error {
				_, err := service.Reserve(ctx, tx, userID, "concurrent-"+itoa(index), 100)
				return err
			})
		}(i)
	}
	wait.Wait()
	close(results)
	success, insufficient := 0, 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		code, _ := fault.Describe(err)
		if code == "insufficient_balance" {
			insufficient++
			continue
		}
		t.Fatalf("意外错误: %v", err)
	}
	if success != 10 || insufficient != 10 {
		t.Fatalf("并发预占结果 success=%d insufficient=%d", success, insufficient)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "0" || wallet.ReservedMinor != "1000" {
		t.Fatalf("并发预占后钱包错误: %+v", wallet)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.holds WHERE user_id = $1`, userID).Scan(&count); err != nil || count != 10 {
		t.Fatalf("预占数量错误: %d,%v", count, err)
	}
}

func TestIdempotentBusinessKeys(t *testing.T) {
	service, pool, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 5000)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		id, err := service.Reserve(ctx, tx, userID, "same-key", 1000)
		holdID = id
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var reused string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		id, err := service.Reserve(ctx, tx, userID, "same-key", 1000)
		reused = id
		return err
	}); err != nil || reused != holdID {
		t.Fatalf("相同预占未复用: %q %q %v", reused, holdID, err)
	}
	var journalCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.journals WHERE business_key = 'hold:same-key'`).Scan(&journalCount); err != nil || journalCount != 1 {
		t.Fatalf("重复预占生成了多条分录: %d,%v", journalCount, err)
	}
	err := service.inTx(ctx, func(tx pgx.Tx) error {
		_, err := service.Reserve(ctx, tx, userID, "same-key", 900)
		return err
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("相同键不同金额未冲突: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "settle-key", 600)
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "settle-key", 600)
	}); err != nil {
		t.Fatalf("重复结算应幂等: %v", err)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "settle-key", 500)
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("相同结算键不同金额未冲突: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "release-key")
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "release-key")
	}); err != nil {
		t.Fatalf("重复释放应幂等: %v", err)
	}
	var captureCount, releaseCount int
	if err := pool.QueryRow(ctx, `SELECT
		count(*) FILTER (WHERE business_key = 'capture:settle-key'),
		count(*) FILTER (WHERE business_key = 'release:release-key')
		FROM public.journals`).Scan(&captureCount, &releaseCount); err != nil || captureCount != 1 || releaseCount != 1 {
		t.Fatalf("结算/释放分录数量错误: %d,%d,%v", captureCount, releaseCount, err)
	}
	wallet, err := service.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if wallet.AvailableMinor != "4400" || wallet.ReservedMinor != "0" {
		t.Fatalf("部分结算释放后余额错误: %+v", wallet)
	}
}

func TestNilTransactionRejected(t *testing.T) {
	service, _, userID, _, _, _ := setupService(t)
	if _, err := service.Reserve(context.Background(), nil, userID, "nil", 100); err == nil {
		t.Fatal("nil 事务未被拒绝")
	}
	if err := service.Settle(context.Background(), nil, "00000000-0000-4000-8000-000000000000", "nil", 1); err == nil {
		t.Fatal("nil 事务结算未被拒绝")
	}
	if err := service.Release(context.Background(), nil, "00000000-0000-4000-8000-000000000000", "nil"); err == nil {
		t.Fatal("nil 事务释放未被拒绝")
	}
}

func TestZeroSettlementAndReleaseKeysAreRemembered(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	fundedWallet(t, service, adminActor(service, adminID), userID, 1000)
	var holdID string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		holdID, err = service.Reserve(ctx, tx, userID, "zero-hold", 500)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "zero-settle", 0)
	}); err != nil {
		t.Fatal(err)
	}
	err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, holdID, "zero-settle", 50)
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("零结算后同键不同金额未冲突: %v", err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, holdID, "zero-release")
	}); err != nil {
		t.Fatal(err)
	}
	var second string
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		second, err = service.Reserve(ctx, tx, userID, "zero-hold-2", 100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Settle(ctx, tx, second, "zero-hold-2-settle", 100)
	}); err != nil {
		t.Fatal(err)
	}
	err = service.inTx(ctx, func(tx pgx.Tx) error {
		return service.Release(ctx, tx, second, "zero-release")
	})
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("零释放后同键不同预占未冲突: %v", err)
	}
}
