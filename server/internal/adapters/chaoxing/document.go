package chaoxing

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// executeDocument 使用平台文档/阅读任务接口。请求结束后仍由调度器独立核验任务点。
func (r *httpExecution) executeDocument(ctx context.Context) (learning.ExecutionResult, error) {
	ref := r.request.Task
	r.cardsURL = r.executor.platform.cardsURLAtIndex(ref.Course, ref.ChapterExternalID, ref.CardIndex)
	payload, err := r.get(ctx, r.cardsURL, "cards")
	if err != nil {
		return learning.ExecutionResult{}, err
	}
	config, err := parseCards(payload)
	if err != nil {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing, "文档任务卡片无法读取")
	}
	defaults := mapValue(config["defaults"])
	for key, expected := range map[string]string{"courseid": ref.Course.ExternalCourseID, "clazzId": ref.Course.ExternalClassID, "cpi": ref.Course.CPI, "knowledgeid": ref.ChapterExternalID} {
		if actual := text(defaults[key]); actual != "" && actual != expected {
			return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "文档任务归属与已领取任务不一致")
		}
	}
	attachments, ok := config["attachments"].([]any)
	if !ok {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing, "文档任务卡片缺少附件列表")
	}
	var task map[string]any
	for _, raw := range attachments {
		item := mapValue(raw)
		if text(item["jobid"]) != ref.TaskPointID || attachmentObjectID(item, attachmentKind(item), text(item["jobid"])) != ref.ObjectID {
			continue
		}
		if task != nil || attachmentKind(item) != "document" {
			return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "文档任务点不唯一或类型不符")
		}
		task = item
	}
	if task == nil {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "未找到对应文档任务点")
	}
	property := mapValue(task["property"])
	jtoken := firstText(task["jtoken"], property["jtoken"])
	if !safeWorkToken(jtoken) {
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing, "文档任务缺少有效请求令牌")
	}
	if info := firstText(task["otherInfo"], task["otherinfo"]); info != "" {
		// 文档协议的nodeId必须属于当前章节，不能从任意字符串猜测章节。
		if start := strings.Index(info, "nodeId_"); start >= 0 {
			node := strings.SplitN(info[start+len("nodeId_"):], "-", 2)[0]
			if node != ref.ChapterExternalID {
				return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "文档任务章节上下文不一致")
			}
		}
	}
	target, _ := url.Parse(r.cardsURL)
	target.RawQuery = ""
	switch strings.ToLower(text(task["type"])) {
	case "read":
		target.Path = "/ananas/job/readv2"
	case "document", "ppt", "pdf", "pptx":
		target.Path = "/ananas/job/document"
	default:
		return learning.ExecutionResult{}, learning.Fail(learning.FailureCapabilityMissing, "当前文档任务协议尚未支持")
	}
	query := url.Values{"jobid": {ref.TaskPointID}, "knowledgeid": {ref.ChapterExternalID}, "courseid": {ref.Course.ExternalCourseID}, "clazzid": {ref.Course.ExternalClassID}, "jtoken": {jtoken}}
	if target.Path == "/ananas/job/document" {
		query.Set("_dc", strconv.FormatInt(r.executor.options.now().UnixMilli(), 10))
	}
	target.RawQuery = query.Encode()
	for attempt := 0; attempt < 2; attempt++ {
		payload, err = r.get(ctx, target.String(), "document")
		if err != nil {
			return learning.ExecutionResult{}, err
		}
		var response map[string]any
		accepted := false
		if json.Unmarshal(payload, &response) == nil {
			for _, key := range []string{"status", "success", "result"} {
				if value, ok := response[key].(bool); ok {
					if !value {
						return learning.ExecutionResult{}, learning.Fail(learning.FailureInvalidTask, "平台未接受本次文档请求")
					}
					accepted = true
				}
			}
			message := firstText(response["msg"], response["message"])
			completed := false
			for _, marker := range []string{"考核点已经完成", "考核点已完成", "任务点已完成", "阅读已完成"} {
				if strings.Contains(message, marker) {
					completed = true
				}
			}
			if strings.Contains(message, "未完成") || strings.Contains(message, "无法完成") {
				completed = false
			}
			if accepted && completed {
				check := &learning.PlatformCheck{Status: learning.CheckConfirmed, Passed: true, CheckedAt: r.executor.options.now().UTC(), Source: "chaoxing.document", Note: message}
				return learning.ExecutionResult{Completed: true, PlatformCompletion: check, Observation: learning.Observation{Source: "chaoxing-http", ObservedAt: r.executor.options.now().UTC(), Note: "文档考核点已完成"}}, nil
			}
		}
		// 文档卡片可能永远不提供完成字段；接受请求后仅做一次幂等复查，保留平台的明确反馈。
		if !accepted || attempt > 0 {
			break
		}
		if err := r.executor.options.wait(ctx, time.Second); err != nil {
			return learning.ExecutionResult{}, requestFailure(err, "文档复查已停止")
		}
		query.Set("_dc", strconv.FormatInt(r.executor.options.now().UnixMilli(), 10))
		target.RawQuery = query.Encode()
	}
	return learning.ExecutionResult{Completed: true, Observation: learning.Observation{Source: "chaoxing-http", ObservedAt: r.executor.options.now().UTC(), Note: "文档请求已接受，等待平台确认"}}, nil
}
