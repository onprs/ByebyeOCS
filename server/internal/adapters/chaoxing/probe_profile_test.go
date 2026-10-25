package chaoxing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newProfileOptions(account, school string) ProfileProbeOptions {
	return ProfileProbeOptions{
		Enabled: true, AccountURL: account, SchoolURL: school,
		Namespace: "chaoxing-uid-v1", EvidenceRef: "local-evidence-1",
		TTL: 24 * time.Hour, AllowedHosts: []string{"127.0.0.1"},
		MinRequestInterval: 50 * time.Millisecond, Timeout: 5 * time.Second,
		MaxResponseBytes: 64 * 1024, allowPrivateNetworks: true,
	}
}

func profileSession() Session {
	return Session{
		Cookies: []Cookie{
			{Name: "fid", Value: "42", Domain: "127.0.0.1", Path: "/", Secure: true},
			{Name: "UID", Value: "opaque", Domain: "127.0.0.1", Path: "/", Secure: true},
		},
		UserAgent: "test-agent", Authorized: true,
	}
}

func TestProfileProbeExtractsServerRenderedIdentity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			t.Errorf("资料查询未携带平台会话")
		}
		switch r.URL.Path {
		case "/account":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `<html><body><span id="messageName" value="学生甲">学生甲</span><p class="fl" id="uid">123456789</p></body></html>`)
		case "/org":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			io.WriteString(w, `{"site":[{"fid":7,"schoolname":"另一学校"},{"fid":42,"schoolname":"测试学校"}],"count":{"42":1}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	options := newProfileOptions(server.URL+"/account", server.URL+"/org")
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	probe, err := NewProfileSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := probe.Probe(context.Background(), profileSession())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Subject != "123456789" || evidence.DisplayName != "学生甲" || evidence.School != "测试学校" {
		t.Fatalf("资料探针结果错误: %+v", evidence)
	}
	if evidence.Namespace != "chaoxing-uid-v1" || !evidence.ExpiresAt.After(time.Now().Add(23*time.Hour)) {
		t.Fatalf("资料探针命名空间或到期错误: %+v", evidence)
	}
}

func TestProfileProbeKeepsLoginWhenSchoolUnavailable(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<p id="uid">123456789</p>`)
			return
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	options := newProfileOptions(server.URL+"/account", server.URL+"/org")
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	probe, err := NewProfileSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := probe.Probe(context.Background(), profileSession())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.School != "" || evidence.Subject != "123456789" {
		t.Fatalf("学校不可用时应保留主体: %+v", evidence)
	}
}

func TestProfileProbeRejectsMissingOrInvalidIdentity(t *testing.T) {
	cases := map[string]string{
		"缺少主体":  `<html><body>无资料</body></html>`,
		"主体非数字": `<p id="uid">abc</p>`,
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				io.WriteString(w, page)
			}))
			defer server.Close()
			options := newProfileOptions(server.URL+"/account", "")
			options.newClient = func(jar http.CookieJar) *http.Client {
				return &http.Client{Jar: jar, Transport: server.Client().Transport}
			}
			probe, err := NewProfileSubjectProbe(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := probe.Probe(context.Background(), profileSession()); err == nil {
				t.Fatal("缺少稳定主体时仍被接受")
			}
		})
	}
}

func TestProfileProbeRejectsUnsafeConfiguration(t *testing.T) {
	base := newProfileOptions("https://passport2.chaoxing.com/mooc/accountManage", "")
	base.TTL = 8 * 24 * time.Hour
	if _, err := NewProfileSubjectProbe(base); err == nil {
		t.Fatal("超出上限的 TTL 被接受")
	}
	httpURL := newProfileOptions("http://passport2.chaoxing.com/mooc/accountManage", "")
	if _, err := NewProfileSubjectProbe(httpURL); err == nil {
		t.Fatal("非 HTTPS 资料地址被接受")
	}
	missingNamespace := newProfileOptions("https://passport2.chaoxing.com/mooc/accountManage", "")
	missingNamespace.Namespace = ""
	if _, err := NewProfileSubjectProbe(missingNamespace); err == nil {
		t.Fatal("缺少命名空间被接受")
	}
	disabled, err := NewProfileSubjectProbe(ProfileProbeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Probe(context.Background(), profileSession()); err == nil || !strings.Contains(err.Error(), "未启用") {
		t.Fatal("默认关闭的资料探针仍可访问")
	}
}

func TestProfileProbeRetriesHTTPSAfterPlaintextDowngrade(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/account" {
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, `<p id="uid">123456789</p>`)
			return
		}
		attempts++
		if cookie, err := r.Cookie("ws"); err != nil || cookie.Value != "1" {
			http.SetCookie(w, &http.Cookie{Name: "ws", Value: "1", Path: "/", Secure: true})
			w.Header().Set("Location", "http://"+r.Host+r.URL.Path)
			w.WriteHeader(http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `{"site":[{"fid":42,"schoolname":"测试学校"}]}`)
	}))
	defer server.Close()
	options := newProfileOptions(server.URL+"/account", server.URL+"/org")
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	probe, err := NewProfileSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := probe.Probe(context.Background(), profileSession())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.School != "测试学校" || attempts < 2 {
		t.Fatalf("明文降级后未重试 HTTPS: %+v attempts=%d", evidence, attempts)
	}
}
