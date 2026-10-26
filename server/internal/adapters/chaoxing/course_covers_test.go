package chaoxing

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func coverPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: 40, G: 140, B: 100, A: 180})
		}
	}
	var output bytes.Buffer
	if err := png.Encode(&output, img); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestCourseCoverMetadataUsesCourseContainer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<div class="course-list">
<div class="course"><div class="course-cover"><a href="/stucoursemiddle?courseid=101&amp;clazzid=201&amp;cpi=301"><img src="/covers/first.png"></a></div>
<a href="/stucoursemiddle?courseid=101&amp;clazzid=201&amp;cpi=301"><span class="course-name" title="第一门课"></span></a></div>
<div class="course"><div class="course-cover"><a href="/stucoursemiddle?courseid=102&amp;clazzid=202&amp;cpi=302"></a></div>
<a href="/stucoursemiddle?courseid=102&amp;clazzid=202&amp;cpi=302"><span class="course-name" title="第二门课"></span></a></div>
<div class="course"><div class="course-cover"><a href="/stucoursemiddle?courseid=103&amp;clazzid=203&amp;cpi=303"><img src="/placeholder.png" data-original="/covers/third.png"></a></div>
<a href="/stucoursemiddle?courseid=103&amp;clazzid=203&amp;cpi=303"><span class="course-name" title="第三门课"></span></a></div></div>`)
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	courses, err := platform.ListCourses(context.Background(), encodeTestSession(t))
	if err != nil || len(courses) != 3 {
		t.Fatalf("课程读取失败：%+v %v", courses, err)
	}
	for _, course := range courses {
		if !course.CoverKnown {
			t.Fatal("已识别课程容器没有记录封面读取事实")
		}
		switch course.ExternalCourseID {
		case "101":
			if course.CoverURL != server.URL+"/covers/first.png" || course.Title != "第一门课" {
				t.Fatal("第一门课程封面关联错误")
			}
		case "102":
			if course.CoverURL != "" || course.Title != "第二门课" {
				t.Fatal("缺图课程错误复用了其他课程封面")
			}
		case "103":
			if course.CoverURL != server.URL+"/covers/third.png" || course.Title != "第三门课" {
				t.Fatal("延迟加载封面没有优先读取真实地址")
			}
		}
	}
}

func TestCourseCoverNormalizesTrustedURLs(t *testing.T) {
	platform, _ := NewPlatform(Options{ReadOnlyEnabled: true})
	for _, source := range []string{"http://p.ananas.chaoxing.com/cover.jpg", "//p.ananas.chaoxing.com/cover.jpg"} {
		if platform.normalizeCoverURL(source) != "https://p.ananas.chaoxing.com/cover.jpg" {
			t.Fatal("没有将可信封面升级为HTTPS")
		}
	}
	for _, source := range []string{
		"https://chaoxing.com.evil.test/a.jpg", "https://127.0.0.1/a.jpg", "http://p.ananas.chaoxing.com:80/a.jpg",
		"data:image/png;base64,AAAA", "https://user:pass@p.ananas.chaoxing.com/a.jpg", "https://p.ananas.chaoxing.com/a.jpg#fragment",
	} {
		if platform.normalizeCoverURL(source) != "" {
			t.Fatalf("不可信封面未拒绝：%s", source)
		}
	}
}

func TestCoverFetchDoesNotForwardSessionAndReturnsSmallJPEG(t *testing.T) {
	payload := coverPNG(t, 1400, 700)
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("封面请求携带了学习或业务会话")
		}
		if !strings.Contains(r.Header.Get("User-Agent"), "Mozilla/5.0") || !strings.Contains(r.Header.Get("User-Agent"), "Chrome/") {
			t.Error("图片CDN会拒绝非浏览器标识，封面请求必须使用标准浏览器的User-Agent")
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	platform := testAdapter(t, server)
	ctx, release, err := platform.BeginSession(context.Background(), encodeTestSession(t))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cover, err := platform.FetchCourseCover(ctx, server.URL+"/cover.png")
	if err != nil {
		t.Fatal(err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(cover.Bytes))
	if err != nil || config.Width != 768 || config.Height != 384 || len(cover.Bytes) > maxCoverBytes || requests.Load() != 1 {
		t.Fatalf("封面压缩或缩图失败：%+v %d %v", config, len(cover.Bytes), err)
	}
}

func TestCoverRejectsMalformedAndOversizedImages(t *testing.T) {
	for _, payload := range [][]byte{
		[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		[]byte(`<html>平台登录页</html>`), []byte("broken"), bytes.Repeat([]byte{'x'}, maxCoverSourceBytes+1),
		coverPNG(t, 4100, 1), coverPNG(t, 2100, 2100),
	} {
		if _, err := normalizeCourseCover(payload); err == nil {
			t.Fatal("不支持或超限图片被当作有效封面")
		}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, strings.Repeat("x", 100))
	}))
	defer server.Close()
	if _, err := testAdapter(t, server).FetchCourseCover(context.Background(), server.URL+"/denied"); err == nil {
		t.Fatal("被拒绝的图片请求未失败")
	}
}
