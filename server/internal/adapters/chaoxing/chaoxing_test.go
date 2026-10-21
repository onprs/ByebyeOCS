package chaoxing

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

func testSession() Session {
	return Session{
		Cookies:   []Cookie{{Name: "UID", Value: "opaque", Domain: "127.0.0.1", Path: "/", Secure: true}},
		UserAgent: "test-agent", Authorized: true,
	}
}

func encodeTestSession(t *testing.T) []byte {
	t.Helper()
	raw, err := encodeSession(testSession())
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseCardsAndTaskPointRules(t *testing.T) {
	page := []byte(`<html><script>var mArg = {"attachments":[
		{"objectId":"v1","jobid":"9001","type":"video","isPassed":false,"attDuration":100,"playTime":5000},
		{"objectId":"v2","jobid":"","type":"video","isPassed":false,"attDuration":60},
		{"objectId":"d1","jobid":"9002","type":"document","isPassed":true}
	]};</script></html>`)
	config, err := parseCards(page)
	if err != nil {
		t.Fatal(err)
	}
	attachments, ok := config["attachments"].([]any)
	if !ok || len(attachments) != 3 {
		t.Fatal("附件结构解析失败")
	}
	for _, raw := range attachments {
		item := mapValue(raw)
		taskPoint := text(item["jobid"]) != ""
		if text(item["objectId"]) == "v2" && taskPoint {
			t.Fatal("没有任务点编号的视频被识别为任务点")
		}
	}
}

func TestParseCardsStringPayload(t *testing.T) {
	page := []byte(`<script>window.mArg = "{\"attachments\":[{\"objectId\":\"v1\",\"jobid\":\"1\"}]}";</script>`)
	config, err := parseCards(page)
	if err != nil {
		t.Fatal(err)
	}
	if len(config["attachments"].([]any)) != 1 {
		t.Fatal("字符串形式 mArg 未解析")
	}
}

func TestPublicAddressValidation(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "::1", "fc00::1", "100.64.0.1", "0.0.0.0"} {
		address, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatal(err)
		}
		if isPublicAddress(address) {
			t.Fatalf("内网或保留地址未被拒绝: %s", raw)
		}
	}
	address, _ := netip.ParseAddr("47.96.1.1")
	if !isPublicAddress(address) {
		t.Fatal("公网地址被错误拒绝")
	}
}

func TestAllowedURLRestrictions(t *testing.T) {
	platform, err := NewPlatform(Options{})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"https://mooc1.chaoxing.com/mooc-ans/knowledge/cards": true,
		"https://chaoxing.com/":                               true,
		"http://mooc1.chaoxing.com/":                          false,
		"https://evil.example/":                               false,
		"https://chaoxing.com.evil.example/":                  false,
		"https://mooc1.chaoxing.com:8443/":                    false,
		"https://user:pass@mooc1.chaoxing.com/":               false,
	}
	for raw, want := range cases {
		target, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := platform.factory.allowedURL(target); got != want {
			t.Fatalf("%s 允许结果错误: %v", raw, got)
		}
	}
}

func testPlatformServer(t *testing.T) *httptest.Server {
	t.Helper()
	cards := `var mArg = {"attachments":[
		{"objectId":"v1","jobid":"9001","type":"video","isPassed":false,"attDuration":100,"playTime":5000},
		{"objectId":"v2","jobid":"","type":"video","isPassed":false,"attDuration":60},
		{"objectId":"d1","jobid":"","type":"document","isPassed":true},
		{"jobid":"9003","type":"video"}
	]};`
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		switch r.URL.Path {
		case "/courselist":
			if r.Method != http.MethodPost {
				t.Errorf("课程列表应使用 POST")
			}
			if err := r.ParseForm(); err != nil || r.Form.Get("courseType") != "1" {
				t.Errorf("课程列表表单错误")
			}
			io.WriteString(w, `<a href="https://mooc1.chaoxing.com/visit/stucoursemiddle?courseid=101&clazzid=202&cpi=303">x</a>
				<span class="course-name-x" title="测试课程"></span>`)
		case "/catalog":
			io.WriteString(w, `<script>toOld('101','5001','202')</script>
				<div id="cur5001" class="catalog_points_yi">1</div>`)
		case "/cards":
			if r.URL.Query().Get("knowledgeid") != "5001" {
				t.Errorf("任务卡片章节参数错误")
			}
			io.WriteString(w, cards)
		default:
			http.NotFound(w, r)
		}
	}))
}

func testAdapter(t *testing.T, server *httptest.Server) *Platform {
	t.Helper()
	options := Options{ReadOnlyEnabled: true, Endpoints: Endpoints{
		CourseList: server.URL + "/courselist", Catalog: server.URL + "/catalog", Cards: server.URL + "/cards",
	}}
	options.AllowedHosts = []string{"127.0.0.1"}
	options.allowPrivateNetworks = true
	options.MinRequestInterval = 50 * time.Millisecond
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	platform, err := NewPlatform(options)
	if err != nil {
		t.Fatal(err)
	}
	return platform
}

func TestDiscoverCourseAndCheckTask(t *testing.T) {
	server := testPlatformServer(t)
	defer server.Close()
	platform := testAdapter(t, server)
	session := encodeTestSession(t)
	courses, err := platform.ListCourses(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	if len(courses) != 1 || courses[0].Title != "测试课程" || courses[0].CPI != "303" {
		t.Fatalf("课程列表解析错误: %+v", courses)
	}
	snapshot, err := platform.DiscoverCourse(context.Background(), session, learning.PlatformCourseRef{
		ExternalCourseID: "101", ExternalClassID: "202", CPI: "303", CycleKey: "current"})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Chapters) != 1 || len(snapshot.Chapters[0].Attachments) != 3 {
		t.Fatalf("快照结构错误: %+v", snapshot)
	}
	if snapshot.SkippedAttachments != 1 {
		t.Fatal("缺少对象标识的附件未计数")
	}
	var resource, taskPoint *learning.PlatformAttachment
	for index := range snapshot.Chapters[0].Attachments {
		item := &snapshot.Chapters[0].Attachments[index]
		switch item.ObjectID {
		case "v2":
			resource = item
		case "v1":
			taskPoint = item
		}
	}
	if resource == nil || taskPoint == nil {
		t.Fatal("附件缺失")
	}
	if taskPoint.TaskPointID != "9001" || taskPoint.PlayedSeconds != 5 || !taskPoint.PassedKnown || taskPoint.Passed {
		t.Fatalf("任务点字段错误: %+v", taskPoint)
	}
	if resource.TaskPointID != "" {
		t.Fatal("普通视频资料被赋予任务点编号")
	}
	check, err := platform.CheckTask(context.Background(), session, learning.PlatformTaskRef{
		Course:            learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"},
		ChapterExternalID: "5001", ObjectID: "v1", TaskPointID: "9001"})
	if err != nil {
		t.Fatal(err)
	}
	if check.Status != learning.CheckUnconfirmed || check.Passed || check.PlayTimeSeconds == nil ||
		*check.PlayTimeSeconds != 5 {
		t.Fatalf("平台核对结果错误: %+v", check)
	}
	unknown, err := platform.CheckTask(context.Background(), session, learning.PlatformTaskRef{
		Course:            learning.PlatformCourseRef{ExternalCourseID: "101", ExternalClassID: "202", CPI: "303"},
		ChapterExternalID: "5001", ObjectID: "missing"})
	if err != nil || unknown.Status != learning.CheckUnknown {
		t.Fatalf("缺失任务应返回未知: %+v %v", unknown, err)
	}
}

func TestReadOnlyDisabled(t *testing.T) {
	platform, err := NewPlatform(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.ListCourses(context.Background(), encodeTestSession(t)); err == nil {
		t.Fatal("默认关闭的只读适配仍可访问")
	}
	if platform.Capabilities().CourseRead {
		t.Fatal("能力开关与配置不一致")
	}
}

type probeFunc func(ctx context.Context, session Session) (SubjectEvidence, error)

func (f probeFunc) Probe(ctx context.Context, session Session) (SubjectEvidence, error) {
	return f(ctx, session)
}

func TestVerifierRequiresEvidenceGate(t *testing.T) {
	_, err := NewVerifier(VerifierOptions{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	verifier, _ := NewVerifier(VerifierOptions{Enabled: true})
	_, err = verifier.Verify(context.Background(), Credentials{School: "1", Account: "user", Password: "pass"})
	var failure *learning.Failure
	if !errors.As(err, &failure) || failure.Class != learning.FailureCapabilityMissing {
		t.Fatalf("缺少命名空间证据时未拒绝: %v", err)
	}
}

func TestVerifierReturnsProbedSubject(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("登录应使用 POST")
		}
		http.SetCookie(w, &http.Cookie{Name: "UID", Value: "cookie-opaque", Path: "/", Secure: true, HttpOnly: true})
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":true}`)
	}))
	defer server.Close()
	options := VerifierOptions{
		Enabled: true, LoginEnabled: true, ReadOnlyAuthorized: true,
		Namespace: "verified-namespace", EvidenceRef: "local-evidence-1",
		Endpoints: Endpoints{Login: server.URL + "/login"},
		SubjectProbe: probeFunc(func(ctx context.Context, session Session) (SubjectEvidence, error) {
			if len(session.Cookies) == 0 || session.Cookies[0].Value != "cookie-opaque" {
				t.Errorf("可信探针未取得平台会话")
			}
			return SubjectEvidence{Namespace: "verified-namespace", Subject: "stable-subject-1",
				DisplayName: "学生", School: "学校", ExpiresAt: time.Now().Add(time.Hour)}, nil
		}),
	}
	options.AllowedHosts = []string{"127.0.0.1"}
	options.allowPrivateNetworks = true
	options.MinRequestInterval = 50 * time.Millisecond
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	verifier, err := NewVerifier(options)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := verifier.Verify(context.Background(), Credentials{School: "1", Account: "user-name", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if subject.Subject != "stable-subject-1" || subject.Subject == "user-name" {
		t.Fatal("验证主体不应来自用户名")
	}
	if _, err := decodeSession(subject.Session); err != nil {
		t.Fatal("返回的会话材料不可解码", err)
	}
	options.SubjectProbe = probeFunc(func(ctx context.Context, session Session) (SubjectEvidence, error) {
		return SubjectEvidence{Namespace: "other", Subject: "x", ExpiresAt: time.Now().Add(time.Hour)}, nil
	})
	other, _ := NewVerifier(options)
	if _, err := other.Verify(context.Background(), Credentials{School: "1", Account: "user", Password: "pass"}); err == nil {
		t.Fatal("命名空间不一致仍通过验证")
	}
}

func TestSessionRejectsUnauthorized(t *testing.T) {
	raw, _ := encodeSession(Session{Cookies: []Cookie{{Name: "UID", Value: "x", Domain: "example.com"}},
		UserAgent: "ua", Authorized: false})
	if _, err := decodeSession(raw); err == nil {
		t.Fatal("未授权会话被接受")
	}
	if _, err := decodeSession([]byte(strings.Repeat("x", 100))); err == nil {
		t.Fatal("损坏的会话被接受")
	}
}

func TestRateLimiterSerializesConcurrentCallers(t *testing.T) {
	var mu sync.Mutex
	arrivals := make([]time.Time, 0, 6)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<a href="stucoursemiddle?courseid=101&clazzid=202&cpi=303">x</a>
			<span class="course-name-x" title="测试课程"></span>`)
	}))
	defer server.Close()
	options := Options{ReadOnlyEnabled: true, Endpoints: Endpoints{CourseList: server.URL + "/courselist"}}
	options.AllowedHosts = []string{"127.0.0.1"}
	options.allowPrivateNetworks = true
	options.MinRequestInterval = 60 * time.Millisecond
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	platform, err := NewPlatform(options)
	if err != nil {
		t.Fatal(err)
	}
	session := encodeTestSession(t)
	start := make(chan struct{})
	errs := make(chan error, 6)
	var wait sync.WaitGroup
	for range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := platform.ListCourses(context.Background(), session)
			errs <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) != 6 {
		t.Fatalf("并发请求数量错误: %d", len(arrivals))
	}
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
	for index := 1; index < len(arrivals); index++ {
		if gap := arrivals[index].Sub(arrivals[index-1]); gap < 45*time.Millisecond {
			t.Fatalf("并发请求未被限速串行: 第 %d 个间隔 %v", index, gap)
		}
	}
}

func newProbeOptions(endpoint string) JSONProbeOptions {
	options := JSONProbeOptions{
		Enabled: true, Endpoint: endpoint, Method: http.MethodGet,
		Namespace: "verified-namespace", EvidenceRef: "local-evidence-1",
		SubjectPath: "data.uid", ExpiresAtPath: "data.expires_at",
		DisplayNamePath: "data.name", SchoolPath: "data.school",
		AllowedHosts: []string{"127.0.0.1"}, MinRequestInterval: 50 * time.Millisecond,
		Timeout: 5 * time.Second, MaxResponseBytes: 64 * 1024,
		allowPrivateNetworks: true,
	}
	return options
}

func TestJSONProbeExtractsConfiguredPaths(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") == "" {
			t.Errorf("可信会话查询未携带平台会话")
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, `{"data":{"uid":"stable-uid-1","name":"学生","school":"学校",
			"expires_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}}`)
	}))
	defer server.Close()
	options := newProbeOptions(server.URL + "/profile")
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	probe, err := NewJSONSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := probe.Probe(context.Background(), testSession())
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Subject != "stable-uid-1" || evidence.Namespace != "verified-namespace" ||
		evidence.DisplayName != "学生" || evidence.School != "学校" || !evidence.ExpiresAt.After(time.Now()) {
		t.Fatalf("探针结果错误: %+v", evidence)
	}
}

func TestJSONProbeRejectsInvalidRecords(t *testing.T) {
	cases := map[string]string{
		"缺失主体":   `{"data":{"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`,
		"会话已过期":  `{"data":{"uid":"x","expires_at":"2020-01-01T00:00:00Z"}}`,
		"主体类型错误": `{"data":{"uid":true,"expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, payload)
			}))
			defer server.Close()
			options := newProbeOptions(server.URL + "/profile")
			options.newClient = func(jar http.CookieJar) *http.Client {
				return &http.Client{Jar: jar, Transport: server.Client().Transport}
			}
			probe, err := NewJSONSubjectProbe(options)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := probe.Probe(context.Background(), testSession()); err == nil {
				t.Fatal("无效可信会话记录被接受")
			}
		})
	}
}

func TestJSONProbeRejectsCrossOriginRedirect(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"uid":"x","expires_at":"`+time.Now().Add(time.Hour).UTC().Format(time.RFC3339)+`"}}`)
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/profile", http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	options := newProbeOptions(source.URL + "/profile")
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: source.Client().Transport}
	}
	probe, err := NewJSONSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), testSession()); err == nil {
		t.Fatal("跨域重定向未被拒绝")
	}
}

func TestJSONProbeDisabledAndNumericExpiry(t *testing.T) {
	disabled, err := NewJSONSubjectProbe(JSONProbeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disabled.Probe(context.Background(), testSession()); err == nil {
		t.Fatal("默认关闭的探针仍可访问")
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"data":{"uid":"numeric-subject","expires_at":`+
			strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)+`}}`)
	}))
	defer server.Close()
	options := newProbeOptions(server.URL + "/profile")
	options.ExpiresAtFormat = "unix"
	options.newClient = func(jar http.CookieJar) *http.Client {
		return &http.Client{Jar: jar, Transport: server.Client().Transport}
	}
	probe, err := NewJSONSubjectProbe(options)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := probe.Probe(context.Background(), testSession())
	if err != nil || evidence.Subject != "numeric-subject" {
		t.Fatalf("数字到期时间解析失败: %+v %v", evidence, err)
	}
}

func TestJSONPathLookup(t *testing.T) {
	var document any
	_ = json.Unmarshal([]byte(`{"data":{"items":[{"uid":"a"}],"name":"n"}}`), &document)
	if value, ok := lookupJSONPath(document, "data.items[0].uid"); !ok || value != "a" {
		t.Fatal("嵌套数组路径解析失败")
	}
	if _, ok := lookupJSONPath(document, "data.items[1].uid"); ok {
		t.Fatal("越界数组被接受")
	}
	if _, ok := lookupJSONPath(document, "data..name"); ok {
		t.Fatal("空段路径被接受")
	}
}

func TestHTTPStatusClassification(t *testing.T) {
	cases := map[int]learning.FailureClass{
		http.StatusForbidden:       learning.FailureForbidden,
		http.StatusTooManyRequests: learning.FailureRateLimited,
		http.StatusBadGateway:      learning.FailureTransient,
	}
	for status, want := range cases {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			options := Options{ReadOnlyEnabled: true,
				Endpoints: Endpoints{CourseList: server.URL + "/courselist", Catalog: server.URL + "/catalog",
					Cards: server.URL + "/cards"}}
			options.AllowedHosts = []string{"127.0.0.1"}
			options.allowPrivateNetworks = true
			options.MinRequestInterval = 50 * time.Millisecond
			options.newClient = func(jar http.CookieJar) *http.Client {
				return &http.Client{Jar: jar, Transport: server.Client().Transport}
			}
			platform, err := NewPlatform(options)
			if err != nil {
				t.Fatal(err)
			}
			_, err = platform.ListCourses(context.Background(), encodeTestSession(t))
			var failure *learning.Failure
			if !errors.As(err, &failure) || failure.Class != want {
				t.Fatalf("HTTP %d 分类错误: %v", status, err)
			}
		})
	}
}
