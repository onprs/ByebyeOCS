package chaoxing

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

var knowledgePattern = regexp.MustCompile(`toOld\(\s*['"](\d+)['"]\s*,\s*['"](\d+)['"]\s*,\s*['"](\d+)['"]`)

type courseCatalog struct {
	Chapters []learning.PlatformChapter
	Pending  map[string]int
}

func (p *Platform) catalogDetails(ctx context.Context, session Session, course learning.PlatformCourseRef) (courseCatalog, error) {
	query := url.Values{"courseid": {course.ExternalCourseID}, "clazzid": {course.ExternalClassID},
		"cpi": {course.CPI}, "ut": {"s"}}
	payload, err := p.do(ctx, session, http.MethodGet, p.options.endpoints.Catalog, query, "catalog")
	if err != nil {
		return courseCatalog{}, err
	}
	return parseCourseCatalog(payload, course, p.options.MaxChapters)
}

func (p *Platform) catalog(ctx context.Context, session Session, course learning.PlatformCourseRef) ([]string, map[string]int, error) {
	catalog, err := p.catalogDetails(ctx, session, course)
	if err != nil {
		return nil, nil, err
	}
	order := make([]string, 0, len(catalog.Chapters))
	for _, chapter := range catalog.Chapters {
		if !chapter.IsGroup {
			order = append(order, chapter.ExternalID)
		}
	}
	return order, catalog.Pending, nil
}

// parseCourseCatalog 保留平台 DOM 的前序顺序。章标题没有平台任务标识，单独建为目录分组；
// 小节的嵌套 UL 与所属 LI 决定父子关系，不能按标题、编号或外部 ID 重新排序。
func parseCourseCatalog(payload []byte, course learning.PlatformCourseRef, maxChapters int) (courseCatalog, error) {
	result := courseCatalog{Chapters: []learning.PlatformChapter{}, Pending: map[string]int{}}
	root := parseHTMLDocument(payload)
	var matches [][]string
	contextInvalid := false
	expected := map[string]string{"courseId": course.ExternalCourseID, "clazzId": course.ExternalClassID, "cpi": course.CPI}
	root.walk(func(node *htmlNode) bool {
		if value, exists := expected[node.attr("id")]; exists && node.tag == "input" && node.attr("value") != value {
			contextInvalid = true
		}
		// onclick 已由 HTML 解析器解码，兼容属性内的引号实体；旧布局保留文本/脚本入口。
		source := node.attr("onclick")
		if node.tag == "#text" {
			source = node.text
		}
		matches = append(matches, knowledgePattern.FindAllStringSubmatch(source, -1)...)
		return true
	})
	if contextInvalid {
		return courseCatalog{}, learning.Fail(learning.FailureCapabilityMissing, "课程目录身份上下文与请求不一致")
	}
	linked := make(map[string]bool, len(matches))
	for _, match := range matches {
		if match[1] != course.ExternalCourseID || match[3] != course.ExternalClassID {
			return courseCatalog{}, learning.Fail(learning.FailureCapabilityMissing, "课程目录上下文与请求不一致")
		}
		linked[match[2]] = true
	}
	seen := make(map[string]bool, len(linked))
	var parseErr error
	add := func(row *htmlNode, parent string, group bool) string {
		id := strings.TrimPrefix(row.attr("id"), "cur")
		number, title := catalogRowText(row)
		if group {
			if number == "" || title == "" {
				parseErr = learning.Fail(learning.FailureCapabilityMissing, "课程目录章标题不完整")
				return ""
			}
			// 平台分组没有 knowledgeid；该标识仅供目录关联，永远不用于卡片读取或任务执行。
			id = fmt.Sprintf("catalog-group:%x", sha256.Sum256([]byte(number+"\x00"+title)))
		} else if !strings.HasPrefix(row.attr("id"), "cur") || !isDigits(id) || !linked[id] {
			parseErr = learning.Fail(learning.FailureCapabilityMissing, "课程目录小节缺少可核对的平台标识")
			return ""
		}
		if seen[id] || len(id) > 128 || len(result.Chapters) >= maxChapters {
			parseErr = learning.Fail(learning.FailureCapabilityMissing, "课程章节标识重复或数量超出同步上限")
			return ""
		}
		if !group {
			for _, match := range knowledgePattern.FindAllStringSubmatch(row.attr("onclick"), -1) {
				if match[2] != id {
					parseErr = learning.Fail(learning.FailureCapabilityMissing, "课程目录节点与入口标识不一致")
					return ""
				}
			}
			if value, known := catalogPendingCount(row); known {
				result.Pending[id] = value
			}
		}
		seen[id] = true
		result.Chapters = append(result.Chapters, learning.PlatformChapter{
			ExternalID: id, ParentExternalID: parent, Number: number, Title: title,
			IsGroup: group, SortOrder: len(result.Chapters),
		})
		return id
	}
	var walk func(*htmlNode, string)
	walk = func(node *htmlNode, parent string) {
		if parseErr != nil || node.tag == "script" || node.tag == "style" || node.tag == "template" {
			return
		}
		// 平台把子目录放在 chapter_item 的同级 UL 中，必须在 LI/章容器这一层传递父标识。
		if node.tag == "li" || node.hasClass("chapter_unit") {
			var row *htmlNode
			for _, child := range node.children {
				if child.hasClass("chapter_item") {
					if row != nil {
						parseErr = learning.Fail(learning.FailureCapabilityMissing, "课程目录容器包含冲突节点")
						return
					}
					row = child
				}
			}
			if row != nil {
				parent = add(row, parent, node.hasClass("chapter_unit") && row.attr("id") == "")
				for _, child := range node.children {
					if child != row {
						walk(child, parent)
					}
				}
				return
			}
		}
		if strings.HasPrefix(node.attr("id"), "cur") && isDigits(strings.TrimPrefix(node.attr("id"), "cur")) {
			parent = add(node, parent, false)
		}
		for _, child := range node.children {
			walk(child, parent)
		}
	}
	walk(root, "")
	if parseErr != nil {
		return courseCatalog{}, parseErr
	}
	if len(result.Chapters) == 0 && len(linked) > 0 {
		// 兼容旧目录只在脚本中提供入口的布局；按入口顺序读取，不推断不存在的层级。
		for _, match := range matches {
			if seen[match[2]] {
				continue
			}
			if len(result.Chapters) >= maxChapters {
				return courseCatalog{}, learning.Fail(learning.FailureCapabilityMissing, "课程章节数量超出同步上限")
			}
			seen[match[2]] = true
			result.Chapters = append(result.Chapters, learning.PlatformChapter{ExternalID: match[2], SortOrder: len(result.Chapters)})
		}
	}
	for id := range linked {
		if !seen[id] {
			return courseCatalog{}, learning.Fail(learning.FailureCapabilityMissing, "课程目录结构不完整，未保存部分章节")
		}
	}
	if len(result.Chapters) == 0 && !emptyCourseCatalog(root, course) {
		return courseCatalog{}, learning.Fail(learning.FailureCapabilityMissing, "课程目录结构未识别")
	}
	return result, nil
}

func catalogRowText(row *htmlNode) (number, title string) {
	if item := row.firstByClass("catalog_sbar", "catalog_num"); item != nil {
		number = htmlNodeText(item)
	}
	title = cleanWorkText(row.attr("title"))
	if title != "" {
		return number, title
	}
	name := row.firstByClass("catalog_name")
	var content strings.Builder
	var collect func(*htmlNode)
	collect = func(node *htmlNode) {
		if node.hasClass("catalog_sbar") || node.tag == "script" || node.tag == "style" {
			return
		}
		content.WriteString(node.text)
		for _, child := range node.children {
			collect(child)
		}
	}
	if name != nil {
		collect(name)
	}
	return number, cleanWorkText(content.String())
}

func emptyCourseCatalog(root *htmlNode, course learning.PlatformCourseRef) bool {
	expected := map[string]string{"courseId": course.ExternalCourseID, "clazzId": course.ExternalClassID, "cpi": course.CPI}
	seen, empty, invalid := map[string]bool{}, false, false
	root.walk(func(node *htmlNode) bool {
		if value, ok := expected[node.attr("id")]; ok && node.tag == "input" {
			if value == "" || value != node.attr("value") || seen[node.attr("id")] {
				invalid = true
			}
			seen[node.attr("id")] = true
		}
		if node.hasClass("nodata") && htmlNodeText(node) == "暂无章节内容" {
			empty = true
		}
		return true
	})
	return empty && !invalid && len(seen) == len(expected)
}
