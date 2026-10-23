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
	if mode == "cache_first" {
		// 与查询入口保持同一判断：缓存关闭的模型不能用缓存优先模式，否则安排保存成功、
		// 每次自动作题在入口就被拒绝，任务只能停在人工处理。
		if !offering.CachePolicy.Allow {
			return fault.New("validation_failed", "该模型未启用答案缓存，请选择每次都使用模型")
		}
		return nil
	}
	if mode == "always_llm" {
		return nil
	}
	return fault.New("validation_failed", "题库模型不支持此查询模式")
}
