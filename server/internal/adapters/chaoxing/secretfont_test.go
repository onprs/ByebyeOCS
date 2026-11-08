package chaoxing

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// 未作答页面含有“重做”“查看正确答案”“已完成”等模板文字，不能被判定为已提交。
func TestWorkPageConfirmedSubmittedMarkers(t *testing.T) {
	for _, page := range []string{
		"做作业 提交 重做 作业详情 剩余次数",
		"查看正确答案 重做 作业详情 本页已完成",
		"我的答案入口 剩余作答次数 已完成章节",
		"章节已完成 未提交",
	} {
		if workPageConfirmedSubmitted(page) {
			t.Fatalf("未作答页面被判定为已提交: %s", page)
		}
		if workPageAppearsSubmitted(page) {
			t.Fatalf("未作答页面被判定为已提交: %s", page)
		}
	}
	submitted := "查看已批阅作业 本次成绩 100 分 我的答案 A 正确答案 A 已完成"
	if !workPageConfirmedSubmitted(submitted) {
		t.Fatal("已提交页面未识别")
	}
	if workPageConfirmedSubmitted("本次成绩 85 分") {
		t.Fatal("只有成绩模板不能判定为已提交")
	}
	if !workPageConfirmedSubmitted("本次成绩 85 分 我的答案 A 正确答案 B") {
		t.Fatal("带答案的成绩页未识别")
	}
	if !workPageConfirmedSubmitted("我的答案 A 正确答案 A") {
		t.Fatal("批阅试卷未识别")
	}
	// 只有标签没有具体值时仍属于未作答模板，不能判为已提交。
	if workPageConfirmedSubmitted("我的答案 正确答案 查看已批阅作业") {
		t.Fatal("无结果值的标签被判定为已提交")
	}
}

// 平台不同课程使用过 cxSecretStyle 变体或没有 id 的样式块；
// 内联字体仍必须能解码，外部字体地址仍然拒绝。
func TestSecretFontStyleBlockVariants(t *testing.T) {
	base, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		old    string
		repl   string
		wantOK bool
	}{
		{"后缀变体", `id="cxSecretStyle"`, `id="cxSecretStyle2"`, true},
		{"无 id 样式块", `id="cxSecretStyle"`, ``, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := strings.Replace(string(base), tc.old, tc.repl, 1)
			decoder, err := newSecretFontDecoder([]byte(page))
			if tc.wantOK && err != nil {
				t.Fatalf("内联字体未被识别: %v", err)
			}
			if tc.wantOK {
				root := parseHTMLDocument([]byte(page))
				if err := rewriteSecretText(root, decoder); err != nil {
					t.Fatalf("变体样式块解码失败: %v", err)
				}
			}
		})
	}

	external := []byte(`<html><head><style type="text/css">@font-face{font-family:x;src:url('https://example.test/font.ttf')}</style></head><body><p class="font-cxsecret">x</p></body></html>`)
	if _, err := newSecretFontDecoder(external); err == nil {
		t.Fatal("外部字体地址不应被当作内联字体")
	}
}

// 字体位于外部样式表时，必须从同域样式与字体文件恢复解码，而不是直接转人工。
func TestSecretFontLoadsExternalStylesheetFont(t *testing.T) {
	page, err := os.ReadFile("testdata/secretfont-page.html")
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	start := strings.Index(strings.ToLower(text), "<style id=\"cxsecretstyle\"")
	if start < 0 {
		t.Fatal("夹具缺少内联字体样式")
	}
	end := strings.Index(text[start:], "</style>")
	if end < 0 {
		t.Fatal("夹具样式块未闭合")
	}
	style := text[start : start+end+len("</style>")]
	encoded := secretFontBase64.FindStringSubmatch(style)
	if len(encoded) != 2 {
		t.Fatal("夹具缺少内联字体数据")
	}
	font, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(string(encoded[1])), ""))
	if err != nil {
		t.Fatal(err)
	}
	css := `@font-face{font-family:cx;src:url('/font.ttf') format('truetype')}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/font.css":
			io.WriteString(w, css)
		case "/font.ttf":
			w.Write(font)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	platform := testAdapter(t, server)
	pageURL, err := url.Parse(server.URL + "/work")
	if err != nil {
		t.Fatal(err)
	}
	stripped := text[:start] + `<link rel="stylesheet" href="/font.css">` + text[start+end+len("</style>"):]
	restored, hasFont := platform.ensureSecretFont(context.Background(), []byte(stripped), pageURL)
	if !hasFont {
		t.Fatal("外部样式字体没有被识别")
	}
	if secretFontStyleBlock(restored) == nil {
		t.Fatal("外部样式字体没有被内联")
	}
	decoder, err := newSecretFontDecoder(restored)
	if err != nil {
		t.Fatalf("恢复后的字体无法解码: %v", err)
	}
	root := parseHTMLDocument(restored)
	if err := rewriteSecretText(root, decoder); err != nil {
		t.Fatalf("恢复后的页面无法解码: %v", err)
	}
	if !strings.Contains(htmlNodeText(root), "指的性目含") {
		t.Fatal("恢复后的页面题干不正确")
	}
}
