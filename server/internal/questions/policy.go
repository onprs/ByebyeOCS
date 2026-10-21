package questions

import (
	"context"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// ValidateQuestionPolicy 只校验管理员公开模型条目与模式，不执行查询或提交。
func (s *Service) ValidateQuestionPolicy(ctx context.Context, modelID, mode string) error {
	offering, err := s.offeringByPublicID(ctx, modelID, false)
	if err != nil {
		return err
	}
	if !offering.Enabled || offering.Health == "unavailable" {
		return fault.New("validation_failed", "题库模型条目尚未发布或已停用")
	}
	if mode == "cache_first" || mode == "always_llm" {
		return nil
	}
	return fault.New("validation_failed", "题库模型不支持此查询模式")
}
