package questions

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// WorkOutputBudget 保留已有幂等查询的冻结预算；新查询使用题库面板的有效模型上限。
func (s *Service) WorkOutputBudget(ctx context.Context, userID, key, offeringID string) (int, error) {
	var frozen int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(budget_output_tokens,2048) FROM public.question_queries WHERE user_id=$1 AND idempotency_key=$2`, userID, key).Scan(&frozen)
	if err == nil {
		return frozen, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if s.fork == nil {
		return 0, errors.New("题库服务不可用")
	}
	items, err := s.fork.Offerings(ctx)
	if err != nil {
		return 0, err
	}
	for _, item := range items {
		if (item.PublicID == offeringID || item.PublicID == "" && item.ID == offeringID) && item.Enabled {
			budget := item.MaxOutputTokens
			if budget <= 0 {
				budget = 2048
			}
			if budget > s.maxOut {
				budget = s.maxOut
			}
			return budget, nil
		}
	}
	return 0, errors.New("题库模型不可用")
}
