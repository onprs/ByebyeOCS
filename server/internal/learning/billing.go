package learning

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// withSavepoint 在父事务内执行可能失败的业务调用。
// 失败时先回滚保存点，让父事务仍可记录失败状态；成功时释放保存点。
// 这保证账本实现即使已经执行部分 SQL 后返回错误，也不会留下部分扣费或
// 让父事务进入 aborted 状态。
func withSavepoint(ctx context.Context, parent pgx.Tx, fn func(pgx.Tx) error) error {
	nested, err := parent.Begin(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			rollbackLearningTx(nested)
		}
	}()
	if err := fn(nested); err != nil {
		return err
	}
	if err := nested.Commit(ctx); err != nil {
		return err
	}
	committed = true
	return nil
}
