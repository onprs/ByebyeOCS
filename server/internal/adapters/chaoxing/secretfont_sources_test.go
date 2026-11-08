package chaoxing

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
)

func TestSecretFontLoadsImportedCSSAndRetriesFontSources(t *testing.T) {
	font := testSecretReferenceFont(t, map[rune]rune{0xe101: '龘'})
	var mu sync.Mutex
	requests := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("静态字体请求携带了会话")
		}
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/css/main.css":
			io.WriteString(w, `@import "./nested.css" screen;`)
		case "/css/nested.css":
			io.WriteString(w, `@import url("main.css");@font-face{font-family:cxsecret;src:url('../fonts/unsupported.woff2') format('woff2'),url('../fonts/quiz.ttf') format('truetype')}`)
		case "/fonts/unsupported.woff2":
			io.WriteString(w, "wOF2-unavailable-font")
		case "/fonts/quiz.ttf":
			w.Write(font)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	base, _ := url.Parse(server.URL + "/work")
	// 无效的早期内联样式不能遮住后来从 @import 恢复的可用字体。
	payload := []byte(`<html><head><style id="cxSecretStyle">@font-face{src:url('data:font/woff2;base64,d09GMgAAAAAAAAAAAA==')}</style><link rel="stylesheet" href="/css/main.css"></head><body><p class="font-cxsecret">&#xe101;</p></body></html>`)
	restored, hasFont := platform.ensureSecretFont(context.Background(), payload, base)
	if !hasFont {
		t.Fatal("未恢复导入样式中的字体")
	}
	decoder, err := newSecretFontDecoder(restored)
	if err != nil {
		t.Fatal(err)
	}
	root := parseHTMLDocument(restored)
	if err := rewriteSecretText(root, decoder); err != nil {
		t.Fatal(err)
	}
	if got := htmlNodeText(root); got != "龘" {
		t.Fatalf("未恢复题面: %q", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range []string{"/css/main.css", "/css/nested.css", "/fonts/unsupported.woff2", "/fonts/quiz.ttf"} {
		if requests[path] != 1 {
			t.Errorf("资源未读取或发生循环请求: %s=%d", path, requests[path])
		}
	}
}

func TestSecretFontRejectsUnrelatedInterfaceFont(t *testing.T) {
	interfaceFont := testSecretReferenceFont(t, map[rune]rune{'崤': '崤', ' ': ' '})
	questionFont := testSecretReferenceFont(t, map[rune]rune{'崤': '龘'})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fonts.css":
			fmt.Fprint(w, `@font-face{font-family:interface;src:url('/interface.ttf')}@font-face{font-family:question;src:url('/question.ttf')}`)
		case "/interface.ttf":
			w.Write(interfaceFont)
		case "/question.ttf":
			w.Write(questionFont)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	base, _ := url.Parse(server.URL + "/work")
	payload := []byte(`<html><head><link rel="stylesheet" href="/fonts.css"></head><body><p class="font-cxsecret"> 崤 </p></body></html>`)
	restored, hasFont := platform.ensureSecretFont(context.Background(), payload, base)
	if !hasFont {
		t.Fatal("未找到题面字体")
	}
	decoder, err := newSecretFontDecoder(restored)
	if err != nil {
		t.Fatal(err)
	}
	root := parseHTMLDocument(restored)
	if err := rewriteSecretText(root, decoder); err != nil {
		t.Fatal(err)
	}
	if got := htmlNodeText(root); got != "龘" {
		t.Fatalf("误用了界面字体: %q", got)
	}
}
