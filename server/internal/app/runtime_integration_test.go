//go:build integration

package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/learning"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/questions"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/transport/httpapi"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 所有验证器与课程均只存在于测试二进制，不进入正式服务。
const fixtureAdminName = "review-admin"
const fixtureAdminPassword = "Fixture-Admin-2026!"
const fixtureUserPassword = "Fixture-Learner-2026!"
const fixtureTOTPSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

type fixtureVerifier struct{}

func (fixtureVerifier) Verify(ctx context.Context, input auth.Credentials) (auth.VerifiedSubject, error) {
	if input.Password != fixtureUserPassword || (input.Account != "learner-a" && input.Account != "learner-b") {
		return auth.VerifiedSubject{}, auth.ErrInvalidCredentials
	}
	return auth.VerifiedSubject{Provider: "chaoxing", Namespace: "fixture-platform-v1", Subject: "fixture-subject-" + input.Account, DisplayName: map[string]string{"learner-a": "联调用户甲", "learner-b": "联调用户乙"}[input.Account], School: "本地测试学校", Session: []byte("fixture-session:" + input.Account), ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type fixturePlatform struct{}

func (fixturePlatform) Capabilities() learning.PlatformCapabilities {
	return learning.PlatformCapabilities{Adapter: "local-fixture", CourseRead: true, TaskCheck: true, ReadRequiresSession: true}
}
func (fixturePlatform) ListCourses(ctx context.Context, session []byte) ([]learning.PlatformCourse, error) {
	if !bytes.HasPrefix(session, []byte("fixture-session:")) {
		return nil, learning.Fail(learning.FailureIdentityInvalid, "测试会话无效")
	}
	return []learning.PlatformCourse{{ExternalCourseID: "fixture-course", ExternalClassID: "fixture-class", CPI: "fixture-cpi", Title: "课程与任务联调", Teacher: "测试教师", Term: "本地测试"}}, nil
}
func (fixturePlatform) DiscoverCourse(ctx context.Context, session []byte, course learning.PlatformCourseRef) (learning.PlatformCourseSnapshot, error) {
	return learning.PlatformCourseSnapshot{Course: course, Title: "课程与任务联调", Teacher: "测试教师", Term: "本地测试", Adapter: "local-fixture", Chapters: []learning.PlatformChapter{{ExternalID: "fixture-chapter", Title: "课程章节", Attachments: []learning.PlatformAttachment{{ObjectID: "fixture-video", TaskPointID: "fixture-task-point", Kind: "video", Title: "视频任务", DurationSeconds: 120, PassedKnown: true}, {ObjectID: "fixture-material", Kind: "document", Title: "普通学习资料", PassedKnown: true}}}}}, nil
}
func (fixturePlatform) CheckTask(context.Context, []byte, learning.PlatformTaskRef) (learning.PlatformCheck, error) {
	return learning.PlatformCheck{Status: learning.CheckUnconfirmed, CheckedAt: time.Now().UTC(), Source: "local-fixture"}, nil
}

type runtimeFixture struct {
	runtime     *Runtime
	user, admin *httptest.Server
}

type fixtureOptions struct {
	fork        questions.ForkClient
	platform    learning.Platform
	executor    learning.Executor
	charging    bool
	sharedCache bool
}

func newRuntimeFixture(t *testing.T, options ...fixtureOptions) *runtimeFixture {
	t.Helper()
	db := testsupport.NewDatabase(t)
	user := httptest.NewUnstartedServer(nil)
	admin := httptest.NewUnstartedServer(nil)
	cfg := config.Defaults()
	cfg.Mode = "development"
	cfg.Auth.Enabled = true
	cfg.Auth.CookieSecure = false
	cfg.Auth.EncryptionKey = bytes.Repeat([]byte{9}, 32)
	cfg.Auth.IdentityKey = bytes.Repeat([]byte{8}, 32)
	cfg.Auth.UserOrigin = "http://" + user.Listener.Addr().String()
	cfg.Auth.AdminOrigin = "http://" + admin.Listener.Addr().String()
	cfg.Storage.DataDir = t.TempDir()
	cfg.Storage.AdminAssetsDir = os.Getenv("BYEBYEOCS_UI_ADMIN_ASSETS")
	cfg.Storage.ClientAssetsDir = os.Getenv("BYEBYEOCS_UI_CLIENT_ASSETS")
	cfg.Learning.Enabled = true
	cfg.Learning.AuthorizationReference = "local-fixture"
	cfg.Learning.SubjectNamespace = "fixture-platform-v1"
	cfg.Operations.RequireFreshBackup = false
	cfg.Operations.DiskProtection = false
	deps := Dependencies{Verifier: fixtureVerifier{}, Platform: fixturePlatform{}}
	if len(options) > 0 {
		option := options[0]
		if option.platform != nil {
			deps.Platform = option.platform
		}
		deps.Executor = option.executor
		cfg.Learning.ExecutionEnabled = option.executor != nil
		cfg.Payments.ChargingEnabled = option.charging
		cfg.Questions.AllowSharedCache = option.sharedCache
		if option.fork != nil {
			deps.Fork = option.fork
			cfg.Questions.Enabled = true
		}
	}
	runtime, err := Assemble(cfg, db, slog.New(slog.NewJSONHandler(io.Discard, nil)), deps)
	if err != nil {
		user.Close()
		admin.Close()
		t.Fatal(err)
	}
	if err := runtime.Auth.CreateAdmin(context.Background(), fixtureAdminName, fixtureAdminPassword, fixtureTOTPSecret); err != nil {
		t.Fatal(err)
	}
	user.Config.Handler = httpapi.NewUserHandler(runtime.logger, runtime.HTTP.User)
	admin.Config.Handler = httpapi.NewAdminHandler(db, runtime.logger, runtime.HTTP.Admin)
	user.Start()
	admin.Start()
	ctx, cancel := context.WithCancel(context.Background())
	var wait sync.WaitGroup
	for _, run := range []func(context.Context) error{runtime.Learning.Run, runtime.Events.Run, runtime.Operations.Run} {
		wait.Add(1)
		go func() { defer wait.Done(); _ = run(ctx) }()
	}
	t.Cleanup(func() { cancel(); user.Close(); admin.Close(); wait.Wait() })
	return &runtimeFixture{runtime: runtime, user: user, admin: admin}
}
func fixtureClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}
func requestJSON(t *testing.T, client *http.Client, method, target, csrf string, value any, headers map[string]string) (int, http.Header, map[string]any) {
	t.Helper()
	var body io.Reader
	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal(err)
	}
	if value != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	parsed, _ := url.Parse(target)
	req.Header.Set("Origin", parsed.Scheme+"://"+parsed.Host)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal("本地接口请求失败")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if len(data) > 0 {
		if json.Unmarshal(data, &result) != nil {
			t.Fatalf("响应未遵守 JSON 契约，HTTP %d", res.StatusCode)
		}
	}
	return res.StatusCode, res.Header, result
}
func loginFixtureUser(t *testing.T, fixture *runtimeFixture, client *http.Client, account string) (string, string) {
	t.Helper()
	code, _, challenge := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/auth/chaoxing/challenges", "", map[string]string{"method": "password", "client": "web"}, nil)
	if code != 201 {
		t.Fatal("无法建立本地登录挑战", code, challenge)
	}
	id, _ := challenge["id"].(string)
	code, _, session := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/auth/chaoxing/challenges/"+id+"/verify", "", map[string]string{"school": "本地测试学校", "account": account, "password": fixtureUserPassword}, nil)
	if code != 200 {
		t.Fatal("本地登录验证失败", code, session)
	}
	profile, ok := session["user"].(map[string]any)
	if !ok {
		t.Fatal("登录资料结构无效")
	}
	return session["csrf_token"].(string), profile["id"].(string)
}
func fixtureTOTP() string {
	key, _ := base32.StdEncoding.DecodeString(fixtureTOTPSecret)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(time.Now().Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	number := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", number%1000000)
}

func TestRuntimeUserAndAdminFlow(t *testing.T) {
	fixture := newRuntimeFixture(t)
	userClient := fixtureClient()
	otherClient := fixtureClient()
	defer userClient.CloseIdleConnections()
	defer otherClient.CloseIdleConnections()
	csrf, userID := loginFixtureUser(t, fixture, userClient, "learner-a")
	var fingerprint string
	if err := fixture.runtime.db.Pool().QueryRow(context.Background(), "SELECT identity_fingerprint FROM public.learning_accounts WHERE user_id=$1", userID).Scan(&fingerprint); err != nil || fingerprint == "fixture-subject-learner-a" || len(fingerprint) < 32 {
		t.Fatal("认证与课程身份没有同事务绑定为HMAC指纹")
	}
	code, _, body := requestJSON(t, userClient, "GET", fixture.user.URL+"/api/v1/wallet", "", nil, nil)
	if code != 200 {
		t.Fatal("钱包不可读", code, body)
	}
	if _, ok := body["available_minor"].(string); !ok {
		t.Fatal("钱包金额没有使用字符串")
	}
	code, _, body = requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, map[string]string{"Idempotency-Key": web.NewID()})
	if code != 202 {
		t.Fatal("课程同步未受理", code, body)
	}
	var courseID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		code, _, body = requestJSON(t, userClient, "GET", fixture.user.URL+"/api/v1/courses", "", nil, nil)
		if code == 200 {
			if items, ok := body["items"].([]any); ok && len(items) > 0 {
				courseID = items[0].(map[string]any)["id"].(string)
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if courseID == "" {
		t.Fatal("课程同步未生成快照")
	}
	loginFixtureUser(t, fixture, otherClient, "learner-b")
	if code, _, _ := requestJSON(t, otherClient, "GET", fixture.user.URL+"/api/v1/courses/"+courseID, "", nil, nil); code != 404 {
		t.Fatal("其他用户读取到课程详情")
	}
	if code, _, _ := requestJSON(t, userClient, "GET", fixture.admin.URL+"/admin/api/v1/users", "", nil, nil); code != 401 {
		t.Fatal("用户会话被管理员端接受")
	}
	code, headers, _ := requestJSON(t, userClient, "GET", fixture.user.URL+"/api/v1/me/preferences", "", nil, nil)
	if code != 200 {
		t.Fatal("无法读取偏好")
	}
	version := headers.Get("ETag")
	if code, _, body := requestJSON(t, userClient, "PATCH", fixture.user.URL+"/api/v1/me/preferences", csrf, map[string]string{"color_scheme": "grove"}, map[string]string{"If-Match": version}); code != 200 {
		t.Fatal("无法保存偏好", code, body)
	}
	if code, _, _ := requestJSON(t, userClient, "PATCH", fixture.user.URL+"/api/v1/me/preferences", csrf, map[string]string{"color_scheme": "indigo"}, map[string]string{"If-Match": version}); code != 412 {
		t.Fatal("旧版本覆盖偏好")
	}
	code, _, body = requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/plans", csrf, map[string]any{"name": "本地联调安排", "course_ids": []string{courseID}, "scope": map[string]string{"kind": "all"}, "task_types": []string{"video"}, "priority": 20}, nil)
	if code != 201 {
		t.Fatal("安排创建失败", code, body)
	}
	if code, _, _ := requestJSON(t, userClient, "POST", fixture.user.URL+"/api/v1/plans", "", map[string]any{}, nil); code != 403 {
		t.Fatal("缺少CSRF仍可写入")
	}
	adminClient := fixtureClient()
	defer adminClient.CloseIdleConnections()
	code, _, adminSession := requestJSON(t, adminClient, "POST", fixture.admin.URL+"/admin/api/v1/auth/login", "", map[string]string{"username": fixtureAdminName, "password": fixtureAdminPassword, "totp_code": fixtureTOTP()}, nil)
	if code != 200 {
		t.Fatal("管理员实际登录失败", code, adminSession)
	}
	if code, _, _ := requestJSON(t, adminClient, "GET", fixture.admin.URL+"/admin/api/v1/dashboard", "", nil, nil); code != 200 {
		t.Fatal("总览接口不可用")
	}
	if code, _, _ := requestJSON(t, adminClient, "GET", fixture.user.URL+"/api/v1/wallet", "", nil, nil); code != 401 {
		t.Fatal("管理员会话被用户端接受")
	}
}

// TestBrowserFixtureServer 仅供明确启用的本地 UI 联调；正式二进制没有此入口。
func TestBrowserFixtureServer(t *testing.T) {
	if os.Getenv("BYEBYEOCS_BROWSER_FIXTURE") != "1" {
		t.Skip("仅在明确启用浏览器联调时运行")
	}
	fixture := newRuntimeFixture(t)
	client := fixtureClient()
	csrf, userID := loginFixtureUser(t, fixture, client, "learner-a")
	code, _, body := requestJSON(t, client, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, nil)
	if code != 202 {
		t.Fatal("联调数据同步失败", body)
	}
	path := os.Getenv("BYEBYEOCS_BROWSER_FIXTURE_FILE")
	if path == "" {
		path = filepath.Join(t.TempDir(), "fixture.json")
	}
	value := map[string]any{"user_url": fixture.user.URL, "admin_url": fixture.admin.URL, "admin_username": fixtureAdminName, "admin_password": fixtureAdminPassword, "admin_totp_secret": fixtureTOTPSecret, "user_account": "learner-a", "user_password": fixtureUserPassword, "user_school": "本地测试学校", "user_id": userID, "data_kind": "synthetic-local-fixture"}
	encoded, _ := json.MarshalIndent(value, "", "  ")
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal("无法保存本地联调入口")
	}
	t.Log("本地合成数据联调服务已启动")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	client.CloseIdleConnections()
	if path != "" {
		_ = os.Remove(path)
	}
}
