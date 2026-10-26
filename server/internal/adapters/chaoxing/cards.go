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

// chapterPendingCounts 为独立卡片核对保留按章节隔离的读取入口。
func chapterPendingCounts(payload []byte, order []string) map[string]int {
	wanted := make(map[string]bool, len(order))
	for _, id := range order {
		wanted["cur"+id] = true
	}
	result, conflicted := map[string]int{}, map[string]bool{}
	parseHTMLDocument(payload).walk(func(node *htmlNode) bool {
		if wanted[node.attr("id")] {
			id := strings.TrimPrefix(node.attr("id"), "cur")
			if value, known := catalogPendingCount(node); known {
				if previous, exists := result[id]; exists && previous != value {
					conflicted[id] = true
				}
				result[id] = value
			}
		}
		return true
	})
	for id := range conflicted {
		delete(result, id)
	}
	return result
}

// catalogPendingCount 只读取当前行，子章节和冲突计数不能用作当前章节的完成事实。
func catalogPendingCount(row *htmlNode) (int, bool) {
	value, known, invalid := 0, false, false
	row.walk(func(node *htmlNode) bool {
		if node != row && strings.HasPrefix(node.attr("id"), "cur") && isDigits(strings.TrimPrefix(node.attr("id"), "cur")) {
			return false
		}
		if node.hasClass("catalog_points_yi") {
			count, err := strconv.Atoi(htmlNodeText(node))
			if err != nil || count < 0 || known && value != count {
				invalid = true
			} else {
				value, known = count, true
			}
			return false
		}
		return true
	})
	return value, known && !invalid
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
