package chaoxing

import (
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"strings"
)

func workClosedMessage(value string) bool {
	text := strings.NewReplacer(" ", "", "\n", "", "，", "", ",", "", "（", "", "）", "", "(", "", ")", "").Replace(cleanWorkText(value))
	for _, marker := range []string{"已截止不能作答", "已截止不可作答", "不允许作答", "当前不可作答", "作业已截止", "测验已截止", "作业已关闭", "测验已关闭", "作业未开放", "测验未开放"} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// workClosedControlFailure 只读取题目区域之外可见的状态文字；截止后保留表单也不能继续取答案。
func workClosedControlFailure(root *htmlNode) *learning.Failure {
	var found *learning.Failure
	root.walk(func(node *htmlNode) bool {
		if found != nil || node.hasClass("singleQuesId") || node.tag == "script" || node.tag == "style" || node.tag == "template" {
			return false
		}
		if _, hidden := node.attrs["hidden"]; hidden {
			return false
		}
		style := strings.ToLower(strings.Join(strings.Fields(node.attr("style")), ""))
		if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") || strings.EqualFold(node.attr("type"), "hidden") {
			return false
		}
		if node.tag == "span" || node.tag == "p" || node.tag == "div" || node.attr("role") == "alert" {
			leaf := true
			for _, child := range node.children {
				if child.tag != "#text" && child.tag != "#comment" && child.tag != "br" {
					leaf = false
					break
				}
			}
			// 只检查文字叶节点，隐藏模板和题目祖先均不参与判定。
			if leaf && workClosedMessage(htmlNodeText(node)) {
				found = learning.Fail(learning.FailurePlatformClosed, "测验已截止或当前不可作答")
				return false
			}
		}
		return true
	})
	return found
}
