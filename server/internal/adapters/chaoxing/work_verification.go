package chaoxing

import (
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

// workControlFailure 只识别题目区域外的验证控件与明确错误通知。
// 题干中讨论验证码、登录或权限的文字不构成平台验证要求。
func workControlFailure(root *htmlNode) *learning.Failure {
	var found *learning.Failure
	root.walk(func(node *htmlNode) bool {
		if found != nil || node.hasClass("singleQuesId") || node.tag == "script" || node.tag == "style" {
			return false
		}
		if _, hidden := node.attrs["hidden"]; hidden {
			return false
		}
		style := strings.ToLower(strings.Join(strings.Fields(node.attr("style")), ""))
		if strings.Contains(style, "display:none") || strings.Contains(style, "visibility:hidden") ||
			strings.EqualFold(node.attr("type"), "hidden") {
			return false
		}
		if node.attr("role") == "alert" {
			if failure := workFailureForMessage(htmlNodeText(node)); failure != nil {
				found = failure
				return false
			}
		}
		switch node.tag {
		case "input", "iframe", "div", "canvas", "img":
			marker := strings.ToLower(node.attr("id") + " " + node.attr("name") + " " + node.attr("class"))
			marker = strings.NewReplacer("_", "", "-", "").Replace(marker)
			challenge := strings.Contains(marker, "captcha") || strings.Contains(marker, "verifycode") || strings.Contains(marker, "validatecode")
			if node.tag == "iframe" && strings.Contains(strings.ToLower(node.attr("src")), "captcha") {
				challenge = true
			}
			if challenge {
				found = learning.Fail(learning.FailureVerificationNeeded, "平台要求验证，已停止测验操作")
				return false
			}
		}
		return true
	})
	return found
}
