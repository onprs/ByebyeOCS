package chaoxing

import (
	"os"
	"strings"
	"testing"
)

// 夹具保留真实平台字体子集，文本只由已收录字形组成，用于校验解码与属性还原。
func TestSecretFontDecodesTextAndAriaLabels(t *testing.T) {
	payload, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	decoder, err := newSecretFontDecoder(payload)
	if err != nil {
		t.Fatal(err)
	}
	root := parseHTMLDocument(payload)
	if err := rewriteSecretText(root, decoder); err != nil {
		t.Fatal(err)
	}
	visible := htmlNodeText(root)
	if !strings.Contains(visible, "指的性目含") {
		t.Fatalf("题干没有解码: %s", visible)
	}
	var aria string
	root.walk(func(node *htmlNode) bool {
		if node.hasClass("font-cxsecret") && node.attr("aria-label") != "" {
			aria = node.attr("aria-label")
			return false
		}
		return true
	})
	if aria != "A 指的性目含" {
		t.Fatalf("选项属性没有解码: %q", aria)
	}
}

// 索引里没有的字形必须整体失败，不能生成可疑题干。
func TestSecretFontRejectsUnknownGlyph(t *testing.T) {
	payload := []byte(`<html><head><style id="cxSecretStyle">@font-face{src:url('data:application/font-ttf;base64,AAEAAAAMAIAAAwBAQkFTRRuOGNgAAGVEAAAA5E9TLzKUGwCtAAABSAAAAGBWT1JHUavDeAAAZigAAAN0Y21hcKhDeaMAAAKEAAAB+GdseWZkJWTVAAAGMAAATrhoZWFkBmbCYQAAAMwAAAA2aGhlYQzu/tUAAAEEAAAAJGhtdHgNsQq2AAABqAAAANpsb2NhAA80uAAABHwAAAG0bWF4cACWASsAAAEoAAAAIG5hbWWAs0JrAABU6AAAEDlwb3N0/4YAMgAAZSQAAAAgAAEAAAABAAACDa/ZXw889QADA+gAAAAAz87zegAAAADP+IsO/Bj76AtwBxAAAAADAAIAAQAAAAAAAQAAA3D/iAH0A+j8GPtjC3AAAQAAAAAAAAAAAAAAAAAAAAEAAQAAAGwBKwApAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEA9wBXgAFAAgCigJYAAAASwKKAlgAAAFeADIBRQAAAgsEAAAAAAAAACAAAAMq3zwQAAAAFgAAAABBREJFAYAAAf//A3D/iAH0BDEBLmAGAQcAAAAAAh8C3QAAACAABgPoAGQAOwA+ACkAZQA6ADIAOgBsABYAJQAeABoAJQAiAB0ANAA3ACsAQwAwACsAMQAkAFwAHgBOACUAJgAnADkAJwAjACsAOQBIACQAKAApACIARgAkACwAUgA4ACUAKQA6ACUAKQAqACUAIAArAC0AVQApACcALAAoACUAHAAjACMAJQAnAC0AKAAsACsAOgAlAFwAowAyACIAKQAbACkAJwA0ACgAKgArAF0APwAYACkAJgAwADcAMAArAC0AMAApACwAMgAsACgAMgAnACsAUgAiACQAOAAlAAAAAAABAAMAAQAAAAwABAHsAAAATABAAAUADFA9UNBVRVVJVVJVVFVWVV1VYlVrVXdVe1WBVYNViFWXVZlVm1WmVapVrVW2VbpVvFXHVg1WFlbAXVRmdW4nb8Z8x4PIivSSnJYjAAEAAFBQUVBVRFVHVUtVVFVWVVhVYFVoVW5VeVV9VYNVhVWLVZlVm1WeVahVrFWvVbhVvFXHVg1WFlbAXVRmdW4nb8Z8x4PIivSSnJYj//8AAFA9UNBVRFVHVUtVVFVWVVhVYFVoVW5VeVV9VYNVhVWLVZlVm1WeVahVrFWvVbhVvFXHVg1WFlbAX'));}</style></head><body><p>\ue000</p></body></html>`)
	if _, err := newSecretFontDecoder(payload); err == nil {
		t.Fatal("无效字体验证失败")
	}
}

// 未作答页面含有“重做”等模板文字，不能被判定为已提交。
func TestWorkPageConfirmedSubmittedMarkers(t *testing.T) {
	unsubmitted := "做作业 提交 重做 作业详情 剩余次数"
	if workPageConfirmedSubmitted(unsubmitted) {
		t.Fatal("未作答页面被判定为已提交")
	}
	submitted := "查看已批阅作业 本次成绩 我的答案 正确答案 已完成"
	if !workPageConfirmedSubmitted(submitted) {
		t.Fatal("已提交页面未识别")
	}
}
