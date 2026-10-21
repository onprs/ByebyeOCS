package chaoxing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

var (
	courseLinkPattern  = regexp.MustCompile(`stucoursemiddle\?courseid=(\d+)&clazzid=(\d+)&cpi=(\d+)`)
	courseNamePattern  = regexp.MustCompile(`class="course-name[^"]*"[^>]*title="([^"]*)"`)
	knowledgePattern   = regexp.MustCompile(`toOld\(\s*'(\d+)'\s*,\s*'(\d+)'\s*,\s*'(\d+)'`)
	cardsPattern       = regexp.MustCompile(`\bmArg\s*=\s*`)
	embedPattern       = regexp.MustCompile(`(?i)\bmodule\s*=\s*["']insertvideo["']`)
	attachmentIDFields = []string{"objectId", "objectid"}
)

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	}
	return ""
}

func number(value any) float64 {
	parsed, _ := strconv.ParseFloat(text(value), 64)
	return parsed
}

func mapValue(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return map[string]any{}
}

func (p *Platform) do(ctx context.Context, session Session, method, rawURL string, form url.Values, role string) ([]byte, error) {
	target, err := url.Parse(rawURL)
	if err != nil || !p.factory.allowedURL(target) {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "上游地址不在允许范围")
	}
	jar, err := sessionJar(session)
	if err != nil {
		return nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话无法建立 Cookie 容器")
	}
	client := p.factory.client(jar)
	if err := p.factory.limiter.wait(ctx, p.options.MinRequestInterval); err != nil {
		return nil, learning.Fail(learning.FailureTransient, "上游请求等待被取消")
	}
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(form.Encode())
	} else if len(form) > 0 {
		target.RawQuery = mergeQuery(target.Query(), form).Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, target.String(), body)
	if err != nil {
		return nil, learning.Fail(learning.FailurePermanent, "无法建立上游请求")
	}
	request.Header.Set("User-Agent", session.UserAgent)
	request.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	referer := "https://" + target.Hostname() + "/"
	request.Header.Set("Referer", referer)
	if role == "course_list" || role == "course_cards" {
		request.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if method == http.MethodPost {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		request.Header.Set("Origin", "https://"+target.Hostname())
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, learning.Fail(learning.FailureTransient, "上游请求未完成")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, learning.Fail(learning.FailureForbidden, "平台拒绝访问，可能需要重新验证身份")
	}
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, learning.Fail(learning.FailureRateLimited, "平台限流，已停止相关操作")
	}
	if response.StatusCode != http.StatusOK {
		return nil, learning.Fail(learning.FailureTransient, "上游响应未成功")
	}
	limited := io.LimitReader(response.Body, p.options.MaxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, learning.Fail(learning.FailureTransient, "上游响应读取失败")
	}
	if int64(len(payload)) > p.options.MaxResponseBytes {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "上游响应超出允许体积")
	}
	return payload, nil
}

func mergeQuery(base, extra url.Values) url.Values {
	result := url.Values{}
	for key, values := range base {
		result[key] = append([]string(nil), values...)
	}
	for key, values := range extra {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func (p *Platform) ListCourses(ctx context.Context, sessionBytes []byte) ([]learning.PlatformCourse, error) {
	if !p.options.ReadOnlyEnabled {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "学习通只读适配未启用")
	}
	session, err := decodeSession(sessionBytes)
	if err != nil {
		return nil, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	form := url.Values{"courseType": {"1"}, "courseFolderId": {"0"}, "query": {""}, "superstarClass": {"0"}}
	payload, err := p.do(ctx, session, http.MethodPost, p.options.endpoints.CourseList, form, "course_list")
	if err != nil {
		return nil, err
	}
	page := string(payload)
	links := courseLinkPattern.FindAllStringSubmatch(page, -1)
	if len(links) == 0 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "课程列表结构未识别")
	}
	names := courseNamePattern.FindAllStringSubmatch(page, -1)
	seen := map[string]bool{}
	courses := make([]learning.PlatformCourse, 0, len(links))
	for index, link := range links {
		key := link[1] + "/" + link[2] + "/" + link[3]
		if seen[key] {
			continue
		}
		seen[key] = true
		course := learning.PlatformCourse{
			ExternalCourseID: link[1], ExternalClassID: link[2], CPI: link[3],
		}
		if index < len(names) {
			course.Title = strings.TrimSpace(html.UnescapeString(names[index][1]))
		}
		courses = append(courses, course)
	}
	return courses, nil
}

func (p *Platform) catalog(ctx context.Context, session Session, course learning.PlatformCourseRef) ([]string, map[string]int, error) {
	query := url.Values{"courseid": {course.ExternalCourseID}, "clazzid": {course.ExternalClassID},
		"cpi": {course.CPI}, "ut": {"s"}}
	payload, err := p.do(ctx, session, http.MethodGet, p.options.endpoints.Catalog, query, "catalog")
	if err != nil {
		return nil, nil, err
	}
	page := string(payload)
	matches := knowledgePattern.FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		return nil, nil, learning.Fail(learning.FailureCapabilityMissing, "课程目录结构未识别")
	}
	seen := map[string]bool{}
	order := make([]string, 0, len(matches))
	for _, match := range matches {
		if match[1] != course.ExternalCourseID || match[3] != course.ExternalClassID {
			return nil, nil, learning.Fail(learning.FailureCapabilityMissing, "课程目录上下文与请求不一致")
		}
		knowledge := match[2]
		if knowledge == "" || seen[knowledge] {
			continue
		}
		seen[knowledge] = true
		order = append(order, knowledge)
	}
	if len(order) > p.options.MaxChapters {
		order = order[:p.options.MaxChapters]
	}
	pending := map[string]int{}
	for _, knowledge := range order {
		pattern := regexp.MustCompile(`id="cur` + regexp.QuoteMeta(knowledge) + `"(?s).*?catalog_points_yi">(\d+)<`)
		if match := pattern.FindStringSubmatch(page); len(match) == 2 {
			if value, err := strconv.Atoi(match[1]); err == nil {
				pending[knowledge] = value
			}
		}
	}
	return order, pending, nil
}

func (p *Platform) cardsURL(course learning.PlatformCourseRef, knowledge string) string {
	parsed, _ := url.Parse(p.options.endpoints.Cards)
	query := parsed.Query()
	query.Set("clazzid", course.ExternalClassID)
	query.Set("courseid", course.ExternalCourseID)
	query.Set("knowledgeid", knowledge)
	query.Set("num", "0")
	query.Set("ut", "s")
	query.Set("cpi", course.CPI)
	query.Set("v", "20190915")
	query.Set("mooc2", "1")
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func (p *Platform) readCards(ctx context.Context, session Session, course learning.PlatformCourseRef, knowledge string) ([]learning.PlatformAttachment, int, error) {
	payload, err := p.do(ctx, session, http.MethodGet, p.cardsURL(course, knowledge), nil, "course_cards")
	if err != nil {
		return nil, 0, err
	}
	config, err := parseCards(payload)
	if err != nil {
		return nil, 0, learning.Fail(learning.FailureCapabilityMissing, "任务卡片结构未识别")
	}
	rawAttachments, ok := config["attachments"].([]any)
	if !ok {
		return nil, 0, learning.Fail(learning.FailureCapabilityMissing, "任务卡片缺少附件列表")
	}
	skipped := 0
	attachments := make([]learning.PlatformAttachment, 0, len(rawAttachments))
	for index, raw := range rawAttachments {
		item := mapValue(raw)
		objectID := ""
		for _, field := range attachmentIDFields {
			if objectID = text(item[field]); objectID != "" {
				break
			}
		}
		if objectID == "" {
			skipped++
			continue
		}
		taskPoint := text(item["jobid"])
		kind := strings.ToLower(strings.TrimSpace(text(item["type"])))
		if kind == "" {
			kind = "other"
		}
		if len(kind) > 32 {
			kind = "other"
		}
		passed := false
		passedKnown := false
		if value, exists := item["isPassed"]; exists {
			if boolean, ok := value.(bool); ok {
				passed, passedKnown = boolean, true
			}
		}
		title := firstText(item["name"], item["title"])
		if title == "" {
			title = firstText(mapValue(item["property"])["name"], mapValue(item["property"])["title"])
		}
		attachments = append(attachments, learning.PlatformAttachment{
			ObjectID: objectID, TaskPointID: taskPoint, Kind: kind, Title: strings.TrimSpace(html.UnescapeString(title)),
			DurationSeconds: nonNegative(number(item["attDuration"])),
			PlayedSeconds:   nonNegative(number(item["playTime"]) / 1000),
			Passed:          passed, PassedKnown: passedKnown, SortOrder: index,
		})
	}
	if len(attachments) == 0 {
		return nil, skipped, learning.Fail(learning.FailureCapabilityMissing, "任务卡片附件结构变化")
	}
	if embedPattern.Match(payload) {
		// 嵌入视频由页面脚本生成；只记录来源上下文，不据此推断完成。
	}
	return attachments, skipped, nil
}

// readAttachments 读取章节附件，并用目录页待完成任务点数补足缺少 isPassed 的卡片。
// 该结果只是平台读取事实，不改变平台状态。
func (p *Platform) readAttachments(ctx context.Context, session Session, course learning.PlatformCourseRef, knowledge string) ([]learning.PlatformAttachment, map[string]int, error) {
	attachments, _, err := p.readCards(ctx, session, course, knowledge)
	if err != nil {
		return nil, nil, err
	}
	pending := map[string]int{}
	missing := false
	for _, attachment := range attachments {
		if !attachment.PassedKnown {
			missing = true
			break
		}
	}
	if missing {
		if value, known, err := p.chapterPending(ctx, session, course, knowledge); err == nil && known {
			pending[knowledge] = value
		}
	}
	return attachments, pending, nil
}

func nonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func firstText(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(textValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func textValue(value any) string {
	if value == nil {
		return ""
	}
	return text(value)
}

func parseCards(payload []byte) (map[string]any, error) {
	for _, index := range cardsPattern.FindAllIndex(payload, -1) {
		decoder := json.NewDecoder(bytes.NewReader(payload[index[1]:]))
		var value any
		if decoder.Decode(&value) != nil {
			continue
		}
		if encoded, ok := value.(string); ok {
			if json.Unmarshal([]byte(encoded), &value) != nil {
				continue
			}
		}
		config := mapValue(value)
		if _, ok := config["attachments"].([]any); ok {
			return config, nil
		}
	}
	return nil, errors.New("任务卡片无法识别")
}

func (p *Platform) DiscoverCourse(ctx context.Context, sessionBytes []byte, course learning.PlatformCourseRef) (learning.PlatformCourseSnapshot, error) {
	if !p.options.ReadOnlyEnabled {
		return learning.PlatformCourseSnapshot{}, learning.Fail(learning.FailureCapabilityMissing, "学习通只读适配未启用")
	}
	session, err := decodeSession(sessionBytes)
	if err != nil {
		return learning.PlatformCourseSnapshot{}, learning.Fail(learning.FailureIdentityInvalid, "平台会话格式无效")
	}
	order, pending, err := p.catalog(ctx, session, course)
	if err != nil {
		return learning.PlatformCourseSnapshot{}, err
	}
	snapshot := learning.PlatformCourseSnapshot{
		Course: course, Adapter: adapterVersion,
		Chapters: make([]learning.PlatformChapter, 0, len(order)),
	}
	skipped := 0
	for index, knowledge := range order {
		attachments, skippedCards, err := p.readCards(ctx, session, course, knowledge)
		if err != nil {
			return learning.PlatformCourseSnapshot{}, err
		}
		skipped += skippedCards
		chapter := learning.PlatformChapter{ExternalID: knowledge, SortOrder: index,
			Attachments: make([]learning.PlatformAttachment, 0, len(attachments))}
		taskPointCount := 0
		for _, attachment := range attachments {
			if attachment.TaskPointID != "" {
				taskPointCount++
			}
		}
		for _, attachment := range attachments {
			// 目录待完成计数只在这个章节恰好一个任务点时可用于补足单卡缺失字段，
			// 多任务点章节不能把整章计数分摊给每个附件。
			if !attachment.PassedKnown && taskPointCount == 1 {
				if count, ok := pending[knowledge]; ok {
					attachment.PassedKnown = true
					attachment.Passed = count == 0
				}
			}
			chapter.Attachments = append(chapter.Attachments, attachment)
		}
		snapshot.Chapters = append(snapshot.Chapters, chapter)
	}
	snapshot.SkippedAttachments = skipped
	return snapshot, nil
}

func (p *Platform) chapterPending(ctx context.Context, session Session, course learning.PlatformCourseRef, knowledge string) (int, bool, error) {
	order, pending, err := p.catalog(ctx, session, course)
	if err != nil {
		return 0, false, err
	}
	for _, item := range order {
		if item == knowledge {
			value, ok := pending[knowledge]
			return value, ok, nil
		}
	}
	return 0, false, nil
}
