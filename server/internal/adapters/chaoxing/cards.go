package chaoxing

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"golang.org/x/net/html"
)

const maxChapterCards = 32

// chapterCardIndexes 读取章节页面中的实际卡片页码；固定测试入口可仅提供单卡片。
func (p *Platform) chapterCardIndexes(ctx context.Context, session Session, course learning.PlatformCourseRef, knowledge string) ([]int, error) {
	if p.options.endpoints.Study == "" {
		return []int{0}, nil
	}
	return p.cachedCardIndexes(ctx, p.resourceKey(course, knowledge), func() ([]int, error) {
		query := url.Values{"courseId": {course.ExternalCourseID}, "clazzid": {course.ExternalClassID},
			"chapterId": {knowledge}, "cpi": {course.CPI}, "verificationcode": {""}, "mooc2": {"1"}}
		payload, err := p.do(ctx, session, http.MethodGet, p.options.endpoints.Study, query, "chapter_study")
		if err != nil {
			return nil, err
		}
		return chapterCardIndexes(payload, course, knowledge)
	})
}

func cardAttr(node *html.Node, name string) string {
	for _, attr := range node.Attr {
		if attr.Key == name {
			return attr.Val
		}
	}
	return ""
}

func chapterCardIndexes(payload []byte, course learning.PlatformCourseRef, knowledge string) ([]int, error) {
	root, err := html.Parse(strings.NewReader(string(payload)))
	if err != nil {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "章节卡片目录无法读取")
	}
	indexes := map[int]bool{}
	count, countKnown, invalid := 0, false, false
	tabCount := 0
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, inTabs bool) {
		if node.Type == html.ElementNode {
			inTabs = inTabs || cardAttr(node, "id") == "prev_tab" || strings.Contains(" "+cardAttr(node, "class")+" ", " prev_ul ")
			if inTabs && node.Data == "li" && cardAttr(node, "cardid") != "" {
				// 卡片标签的DOM顺序就是平台切换函数使用的从0开始的页码。
				indexes[tabCount] = true
				tabCount++
			}
			if node.Data == "input" && cardAttr(node, "id") == "cardcount" {
				value, err := strconv.Atoi(cardAttr(node, "value"))
				if err != nil || value < 0 || value >= maxChapterCards || countKnown && count != value {
					invalid = true
				} else {
					count, countKnown = value, true
				}
			}
			if node.Data == "iframe" {
				source, err := url.Parse(cardAttr(node, "src"))
				if err == nil && strings.HasSuffix(source.Path, "/knowledge/cards") {
					query := source.Query()
					for key, expected := range map[string]string{"knowledgeid": knowledge, "courseid": course.ExternalCourseID, "clazzid": course.ExternalClassID, "cpi": course.CPI} {
						if value := query.Get(key); value != "" && value != expected {
							invalid = true
						}
					}
					index, err := strconv.Atoi(query.Get("num"))
					if err != nil || index < 0 || index >= maxChapterCards {
						invalid = true
					} else {
						indexes[index] = true
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, inTabs)
		}
	}
	walk(root, false)
	if invalid || tabCount > maxChapterCards {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "章节卡片数量或归属不符合支持范围")
	}
	if tabCount == 0 && countKnown {
		// cardcount为章节页面中的末页索引，0仍包含第一张卡片。
		for index := 0; index <= count; index++ {
			indexes[index] = true
		}
	}
	if len(indexes) == 0 {
		return nil, learning.Fail(learning.FailureCapabilityMissing, "章节页面缺少可核对的任务卡片目录")
	}
	result := make([]int, 0, len(indexes))
	for index := range indexes {
		result = append(result, index)
	}
	sort.Ints(result)
	return result, nil
}

func (p *Platform) readCardsAtIndex(ctx context.Context, session Session, course learning.PlatformCourseRef, knowledge string, index int) ([]learning.PlatformAttachment, int, error) {
	if index < 0 || index >= maxChapterCards {
		return nil, 0, learning.Fail(learning.FailureInvalidTask, "任务卡片页码无效")
	}
	payload, err := p.do(ctx, session, http.MethodGet, p.cardsURLAtIndex(course, knowledge, index), nil, "course_cards")
	if err != nil {
		return nil, 0, err
	}
	attachments, skipped, err := parseAttachments(payload)
	for offset := range attachments {
		attachments[offset].CardIndex = index
	}
	return attachments, skipped, err
}

func (p *Platform) readTaskAttachments(ctx context.Context, session Session, ref learning.PlatformTaskRef) ([]learning.PlatformAttachment, map[string]int, error) {
	attachments, _, err := p.readCardsAtIndex(ctx, session, ref.Course, ref.ChapterExternalID, ref.CardIndex)
	if err != nil {
		return nil, nil, err
	}
	pending := map[string]int{}
	for _, attachment := range attachments {
		if attachment.ObjectID == ref.ObjectID && attachment.TaskPointID == ref.TaskPointID && !attachment.PassedKnown {
			value, known, err := p.chapterPending(ctx, session, ref.Course, ref.ChapterExternalID)
			if err != nil {
				return nil, nil, err
			}
			if known {
				pending[ref.ChapterExternalID] = value
			}
			break
		}
	}
	return attachments, pending, nil
}

// chapterPendingCounts 限定在对应章节节点内查找计数，防止匹配到下一章节的0。
func chapterPendingCounts(payload []byte, order []string) map[string]int {
	root, err := html.Parse(strings.NewReader(string(payload)))
	if err != nil {
		return map[string]int{}
	}
	wanted := map[string]bool{}
	for _, id := range order {
		wanted["cur"+id] = true
	}
	result, conflicted := map[string]int{}, map[string]bool{}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		id := cardAttr(node, "id")
		if wanted[id] {
			var find func(*html.Node)
			find = func(item *html.Node) {
				if item != node && wanted[cardAttr(item, "id")] {
					return
				}
				if strings.Contains(" "+cardAttr(item, "class")+" ", " catalog_points_yi ") {
					var content strings.Builder
					var collect func(*html.Node)
					collect = func(part *html.Node) {
						if part.Type == html.TextNode {
							content.WriteString(part.Data)
						}
						for child := part.FirstChild; child != nil; child = child.NextSibling {
							collect(child)
						}
					}
					collect(item)
					if value, err := strconv.Atoi(strings.TrimSpace(content.String())); err == nil && value >= 0 {
						key := strings.TrimPrefix(id, "cur")
						if previous, exists := result[key]; exists && previous != value {
							conflicted[key] = true
						}
						result[key] = value
					}
				}
				for child := item.FirstChild; child != nil; child = child.NextSibling {
					find(child)
				}
			}
			find(node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	for key := range conflicted {
		delete(result, key)
	}
	return result
}

// attachmentKind 将平台文件类型和任务种类统一为可安排的公开分类。
func attachmentKind(item map[string]any) string {
	kind := normalizeAttachmentKind(text(item["type"]))
	if kind == "video" {
		switch strings.ToLower(text(mapValue(item["property"])["type"])) {
		case ".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac", ".wma", ".amr":
			return "audio"
		}
	}
	return kind
}
