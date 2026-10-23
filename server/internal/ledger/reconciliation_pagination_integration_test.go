//go:build integration

package ledger

import (
	"context"
	"testing"
)

// 对账列表的游标必须指向本页最后一条已返回记录。
// 若按多取的探测行生成游标，下一页会跳过一条真实记录，管理员看不到该对账结果。
func TestReconciliationListPaginationDoesNotSkipRows(t *testing.T) {
	service, _, userID, adminID, _, _ := setupService(t)
	ctx := context.Background()
	admin := adminActor(service, adminID)
	wallet := fundedWallet(t, service, admin, userID, 0)
	created := map[string]bool{}
	for index := 0; index < 3; index++ {
		record, err := service.CreateReconciliation(ctx, admin, "reconcile-page-"+itoa(index),
			ReconciliationInput{Kind: "wallet_rebuild", WalletID: wallet.ID, Reason: "分页核对测试"})
		if err != nil {
			t.Fatal(err)
		}
		created[record.ID] = true
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < len(created)+1; page++ {
		result, err := service.ListReconciliations(ctx, admin, "", "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatalf("对账分页重复返回记录 %s", item.ID)
			}
			if !created[item.ID] {
				t.Fatalf("对账分页返回未知记录 %s", item.ID)
			}
			seen[item.ID] = true
		}
		if result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(seen) != len(created) {
		t.Fatalf("对账分页遗漏 %d 条记录（共 %d 条）", len(created)-len(seen), len(created))
	}
}
