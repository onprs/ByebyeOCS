package chaoxing

import (
	"html"
	"net/url"
	"strings"
)

type courseListMetadata struct {
	Title    string
	Teacher  string
	CoverURL string
}

// parseCourseMetadata 以单门课程容器中的稳定入口关联封面，避免重复链接或缺图造成位置错配。
func (p *Platform) parseCourseMetadata(root *htmlNode) map[string]courseListMetadata {
	metadata := map[string]courseListMetadata{}
	root.walk(func(row *htmlNode) bool {
		if !row.hasClass("course") {
			return true
		}
		keys := map[string]bool{}
		item := courseListMetadata{}
		row.walk(func(node *htmlNode) bool {
			if link := courseLinkPattern.FindString(node.attr("href")); link != "" {
				if target, err := url.Parse(html.UnescapeString(link)); err == nil {
					query := target.Query()
					if isDigits(query.Get("courseid")) && isDigits(query.Get("clazzid")) && isDigits(query.Get("cpi")) {
						keys[query.Get("courseid")+"/"+query.Get("clazzid")+"/"+query.Get("cpi")] = true
					}
				}
			}
			for _, class := range strings.Fields(node.attr("class")) {
				if strings.HasPrefix(class, "course-name") && item.Title == "" {
					item.Title = strings.TrimSpace(node.attr("title"))
					if item.Title == "" {
						item.Title = strings.TrimSpace(htmlNodeText(node))
					}
				}
				if node.tag == "p" && class == "color3" && item.Teacher == "" {
					item.Teacher = strings.TrimSpace(node.attr("title"))
					if item.Teacher == "" {
						item.Teacher = strings.TrimSpace(htmlNodeText(node))
					}
				}
			}
			if node.hasClass("course-cover") {
				node.walk(func(image *htmlNode) bool {
					if image.tag == "img" && item.CoverURL == "" {
						for _, attr := range []string{"data-original", "data-src", "src"} {
							if source := p.normalizeCoverURL(image.attr(attr)); source != "" {
								item.CoverURL = source
								break
							}
						}
					}
					return true
				})
			}
			return true
		})
		if len(keys) == 1 {
			for key := range keys {
				if previous, exists := metadata[key]; exists && previous != item {
					// 重复容器信息冲突时丢弃封面关联，名称仍由列表的严格匹配兜底。
					item.CoverURL = ""
				}
				metadata[key] = item
			}
		}
		return false
	})
	return metadata
}
