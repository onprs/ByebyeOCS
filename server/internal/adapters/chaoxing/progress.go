package chaoxing

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

var playerFrame = regexp.MustCompile(`(?is)<iframe\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)
var insertedVideo = regexp.MustCompile(`(?i)\bmodule\s*=\s*["']insertvideo["']`)
var insertedAudio = regexp.MustCompile(`(?i)\bmodule\s*=\s*["']insertaudio["']`)

func (r *httpExecution) prepare(ctx context.Context) error {
	ref := r.request.Task
	target, _ := url.Parse(r.executor.platform.cardsURLAtIndex(ref.Course, ref.ChapterExternalID, ref.CardIndex))
	r.cardsURL = target.String()
	body, err := r.executor.platform.do(ctx, r.session, http.MethodGet, r.cardsURL, nil, "course_cards")
	if err != nil {
		return err
	}
	cards, err := parseCards(body)
	if err != nil {
		return learning.Fail(learning.FailureCapabilityMissing, "任务卡片结构未确认")
	}
	r.defaults = mapValue(cards["defaults"])
	items, ok := cards["attachments"].([]any)
	if !ok {
		return learning.Fail(learning.FailureCapabilityMissing, "任务卡片缺少附件列表")
	}
	matches := 0
	for _, raw := range items {
		item := mapValue(raw)
		if firstText(item["objectId"], item["objectid"], mapValue(item["property"])["objectid"]) == ref.ObjectID && text(item["jobid"]) == ref.TaskPointID {
			r.task = item
			matches++
		}
	}
	kind := attachmentKind(r.task)
	if matches != 1 || (kind != "video" && kind != "audio") || r.request.TaskKind != "" && kind != r.request.TaskKind {
		return learning.Fail(learning.FailureInvalidTask, "任务卡片中没有唯一匹配的音视频任务点")
	}
	r.mediaType = "Video"
	module := "video"
	if kind == "audio" {
		r.mediaType, module = "Audio", "audio"
	}
	for key, expected := range map[string]string{"courseid": ref.Course.ExternalCourseID, "clazzId": ref.Course.ExternalClassID, "cpi": ref.Course.CPI} {
		if text(r.defaults[key]) != expected {
			return learning.Fail(learning.FailureInvalidTask, "任务卡片课程归属与已领取任务不一致")
		}
	}
	if !protocolID(text(r.defaults["userid"])) || !protocolID(text(r.defaults["fid"])) {
		return learning.Fail(learning.FailureCapabilityMissing, "任务缺少平台下发的用户或机构上下文")
	}
	// 播放器抓拍、归档和跳转配置不参与 HTTP 进度协议，不构成服务端验证拒绝。
	// 实际验证码、身份失效与进度响应中的强制校验仍按真实响应处理。
	// 平台会在卡片里下发 randomFaceCaptureTimeList（抓拍时间点），2026-11 实测该字段
	// 只驱动播放器前端弹窗，进度上报与完成判定都不校验 videoFaceCaptureEnc，
	// 因此不因该字段停止任务；真正要求本人验证时由平台响应转人工。
	for _, frame := range playerFrame.FindAllSubmatch(body, -1) {
		relative, err := url.Parse(html.UnescapeString(string(frame[1])))
		if err != nil {
			continue
		}
		resolved := target.ResolveReference(relative)
		if r.executor.platform.factory.allowedURL(resolved) && strings.HasPrefix(resolved.Path, "/ananas/modules/"+module+"/") && strings.HasSuffix(resolved.Path, "/index.html") {
			r.player = resolved.String()
		}
	}
	if r.player == "" && (module == "video" && insertedVideo.Match(body) || module == "audio" && insertedAudio.Match(body)) {
		r.player = target.Scheme + "://" + target.Host + "/ananas/modules/" + module + "/index.html"
	}
	if r.player == "" {
		return learning.Fail(learning.FailureCapabilityMissing, "任务卡片未提供可核对的播放器文档")
	}
	if _, err := r.get(ctx, r.player, "player"); err != nil {
		return err
	}
	status := &url.URL{Scheme: target.Scheme, Host: target.Host, Path: "/ananas/status/" + ref.ObjectID}
	status.RawQuery = url.Values{"k": {text(r.defaults["fid"])}, "flag": {"normal"}}.Encode()
	body, err = r.get(ctx, status.String(), "metadata")
	if err != nil {
		return err
	}
	if json.Unmarshal(body, &r.metadata) != nil || text(r.metadata["status"]) != "success" {
		return learning.Fail(learning.FailureCapabilityMissing, "平台视频元数据不可用")
	}
	duration, durationKnown := protocolNumber(r.metadata["duration"])
	interval, intervalKnown := protocolNumber(r.defaults["reportTimeInterval"])
	// 未开始过的任务卡片不下发 playTime；此时平台保存位置就是 0，
	// 按缺省 0 起步而不是判定为不支持。
	saved, positionKnown := 0.0, true
	if value, exists := r.task["playTime"]; exists && value != nil {
		saved, positionKnown = protocolNumber(value)
		saved /= 1000
	}
	if !durationKnown || !intervalKnown || !positionKnown || duration < 1 || duration != math.Floor(duration) || duration > r.executor.options.MaxDuration.Seconds() || interval < 30 || interval > 300 || saved < 0 || saved > duration {
		return learning.Fail(learning.FailureCapabilityMissing, "平台时长、保存位置或请求间隔超出已支持范围")
	}
	if r.request.MediaDurationSeconds > 0 && math.Abs(r.request.MediaDurationSeconds-duration) > 2 {
		return learning.Fail(learning.FailureInvalidTask, "视频元数据时长与课程快照不一致，需要重新同步")
	}
	if !protocolID(text(r.metadata["dtoken"])) {
		return learning.Fail(learning.FailureCapabilityMissing, "平台未提供有效的任务请求令牌")
	}
	r.duration, r.position = int(duration), int(math.Floor(saved))
	r.interval = time.Duration(interval * float64(time.Second))
	base, err := url.Parse(text(r.defaults["reportUrl"]))
	if err != nil || !r.executor.platform.factory.allowedURL(base) || base.RawQuery != "" || base.Fragment != "" || !(strings.HasPrefix(base.Path, "/multimedia/log/a/") || strings.HasPrefix(base.Path, "/mooc-ans/multimedia/log/a/")) || !strings.HasSuffix(strings.TrimRight(base.Path, "/"), "/"+ref.Course.CPI) {
		return learning.Fail(learning.FailureCapabilityMissing, "平台任务请求入口未通过校验")
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + text(r.metadata["dtoken"])
	r.reportURL = base.String()
	r.other = text(r.task["otherInfo"])
	if strings.Contains(r.other, "&") {
		parts, err := url.ParseQuery("otherInfo=" + r.other)
		if err != nil || len(parts) != 2 || len(parts["otherInfo"]) != 1 || len(parts["courseId"]) != 1 || parts.Get("courseId") != ref.Course.ExternalCourseID {
			return learning.Fail(learning.FailureInvalidTask, "任务内嵌课程上下文不一致或含未知参数")
		}
		r.other = parts.Get("otherInfo")
	}
	match := regexp.MustCompile(`(?:^|-)rt_([^&-]+)`).FindStringSubmatch(r.other)
	if len(match) != 2 {
		return learning.Fail(learning.FailureCapabilityMissing, "平台未提供已确认的执行比例编码")
	}
	switch match[1] {
	case "d":
		r.rt = "0.9"
	case "1", "0.9":
		r.rt = match[1]
	default:
		return learning.Fail(learning.FailureCapabilityMissing, "平台执行比例编码尚未支持")
	}
	if rawEngine, exists := r.defaults["courseEngineInfo"]; exists {
		engine, ok := rawEngine.(bool)
		if !ok {
			return learning.Fail(learning.FailureCapabilityMissing, "课程引擎上下文结构未确认")
		}
		r.engine = &engine
	}
	// 交互时间轴只服务播放器，不属于原生进度协议的前置条件。
	return nil
}

func protocolNumber(value any) (float64, bool) {
	var parsed float64
	switch typed := value.(type) {
	case float64:
		parsed = typed
	case string:
		var err error
		parsed, err = strconv.ParseFloat(typed, 64)
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return parsed, !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func (r *httpExecution) report(ctx context.Context, position, event int) (bool, error) {
	if position < 0 || position > r.duration {
		return false, learning.Fail(learning.FailureInvalidTask, "任务位置超出平台时长")
	}
	clip := "0_" + strconv.Itoa(r.duration)
	// 平台协议公开使用的固定签名常量，不能替代登录身份或用户授权。
	signature := fmt.Sprintf("[%s][%s][%s][%s][%d][d_yHJ!$pdA~5][%d][%s]", r.request.Task.Course.ExternalClassID, text(r.defaults["userid"]), r.request.Task.TaskPointID, r.request.Task.ObjectID, position*1000, r.duration*1000, clip)
	digest := md5.Sum([]byte(signature))
	values := url.Values{"clazzId": {r.request.Task.Course.ExternalClassID}, "userid": {text(r.defaults["userid"])}, "jobid": {r.request.Task.TaskPointID}, "objectId": {r.request.Task.ObjectID}, "courseId": {r.request.Task.Course.ExternalCourseID}, "playingTime": {strconv.Itoa(position)}, "duration": {strconv.Itoa(r.duration)}, "clipTime": {clip}, "otherInfo": {r.other}, "rt": {r.rt}, "dtype": {r.mediaType}, "view": {"pc"}, "_t": {strconv.FormatInt(r.executor.options.now().UnixMilli(), 10)}, "attDuration": {text(r.task["attDuration"])}, "attDurationEnc": {text(r.task["attDurationEnc"])}, "videoFaceCaptureEnc": {text(r.task["videoFaceCaptureEnc"])}, "enc": {hex.EncodeToString(digest[:])}, "isdrag": {strconv.Itoa(event)}}
	if r.engine != nil {
		values.Set("courseEngineInfo", strconv.FormatBool(*r.engine))
	}
	target, _ := url.Parse(r.reportURL)
	target.RawQuery = values.Encode()
	body, err := r.get(ctx, target.String(), "progress")
	if err != nil {
		return false, err
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return false, learning.Fail(learning.FailureCapabilityMissing, "任务请求响应结构未确认")
	}
	for _, key := range []string{"success", "status"} {
		if value, ok := payload[key].(bool); ok && !value {
			return false, learning.Fail(learning.FailureCapabilityMissing, "平台拒绝本次任务请求")
		}
	}
	passed, ok := payload["isPassed"].(bool)
	if !ok {
		return false, learning.Fail(learning.FailureCapabilityMissing, "任务请求响应缺少完成状态")
	}
	return passed, nil
}
