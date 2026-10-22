package app

import (
	"context"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
)

// workAnswerer 将安排冻结的公开模型与平台题目交给同一题库业务入口。
// HTTP 查询和后台做题共享资源名额、幂等记录、预占及实际 token 结算。
type workAnswerer struct {
	questions *questions.Service
}

func (a *workAnswerer) AnswerWorkQuestion(ctx context.Context, userID, key string, policy learning.QuestionPolicy, question learning.WorkQuestion) (learning.WorkQueryResult, error) {
	input := questions.QueryInput{ModelOfferingID: policy.ModelOfferingID, Mode: policy.Mode, CacheScope: "user"}
	input.Question.Title, input.Question.Type = question.Title, question.Type
	options := make([]string, 0, len(question.Options))
	for _, option := range question.Options {
		options = append(options, option.Label+". "+option.Text)
	}
	input.Question.Options = strings.Join(options, "\n")
	input.Question.Images = make([]string, 0, len(question.Images))
	for _, item := range question.Images {
		input.Question.Images = append(input.Question.Images, "data:"+item.MIMEType+";base64,"+item.Base64)
	}
	input.Budget.MaxOutputTokens = 2048
	view, err := a.questions.Answer(ctx, userID, key, input)
	if err != nil {
		if queryErr, ok := questions.AsQueryError(err); ok {
			switch queryErr.Code {
			case "request_in_progress", "rate_limited", "upstream_rate_limited":
				delay := 3 * time.Second
				if queryErr.Code == "request_in_progress" {
					delay = 2*time.Minute + time.Second
				}
				return learning.WorkQueryResult{}, &learning.Failure{Class: learning.FailureRateLimited,
					RetryAfter: delay, Detail: "题库查询正在等待恢复，稍后继续"}
			case "insufficient_balance":
				return learning.WorkQueryResult{}, learning.Fail(learning.FailureAnswerUnavailable, "余额不足，本次尚未取得全部答案")
			case "service_unavailable", "charging_setting_unavailable":
				return learning.WorkQueryResult{}, learning.Fail(learning.FailureTransient, "题库服务暂不可用")
			default:
				return learning.WorkQueryResult{}, learning.Fail(learning.FailureAnswerUnavailable, "题库未取得可用答案，请查看查询记录")
			}
		}
		return learning.WorkQueryResult{}, learning.Fail(learning.FailureAnswerUnavailable, "题库请求未完成")
	}
	// token/计费异常与答案可用性分开；用量不完整时由题库模块保留事实并停止扣费。
	if view.ContentExpired || strings.TrimSpace(view.Answer) == "" ||
		(view.Status != "answered" && view.Status != "cache_hit" && view.Status != "needs_review") {
		return learning.WorkQueryResult{}, learning.Fail(learning.FailureAnswerUnavailable, "题库结果没有可填写的答案")
	}
	return learning.WorkQueryResult{QueryID: view.ID, Answer: view.Answer}, nil
}
