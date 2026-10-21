package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
)

type checkFunc func(context.Context) error

func (f checkFunc) Check(ctx context.Context) error { return f(ctx) }

func quietLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

func TestAudienceIsolationAndMethods(t *testing.T) {
	for _, test := range []struct {
		name         string
		handler      http.Handler
		path, method string
		status       int
	}{
		{"用户能力", UserHandler(quietLogger()), "/api/v1/capabilities", "GET", 200},
		{"管理员能力", AdminHandler(nil, quietLogger()), "/admin/api/v1/capabilities", "GET", 200},
		{"用户不能访问管理", UserHandler(quietLogger()), "/admin/api/v1/capabilities", "GET", 404},
		{"管理不复用用户路由", AdminHandler(nil, quietLogger()), "/api/v1/capabilities", "GET", 404},
		{"用户不能访问健康检查", UserHandler(quietLogger()), "/health/live", "GET", 404},
		{"用户不能访问就绪检查", UserHandler(quietLogger()), "/health/ready", "GET", 404},
		{"未开放业务接口", UserHandler(quietLogger()), "/api/v1/wallet", "GET", 404},
		{"不接受写入", UserHandler(quietLogger()), "/api/v1/capabilities", "POST", 405},
		{"预检不隐式开放跨域", UserHandler(quietLogger()), "/api/v1/capabilities", "OPTIONS", 405},
		{"无重定向", UserHandler(quietLogger()), "/api/v1/capabilities/", "GET", 404},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			r.Header.Set("X-Request-ID", "private-test-value")
			r.Header.Set("Origin", "https://untrusted.example")
			w := httptest.NewRecorder()
			test.handler.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("状态码 %d，期望 %d", w.Code, test.status)
			}
			if !regexp.MustCompile(`^req_[0-9a-f]{32}$`).MatchString(w.Header().Get("X-Request-ID")) {
				t.Fatal("请求编号未由服务端生成")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("缓存或跨域策略错误")
			}
			if test.status == 405 && w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("缺少允许方法")
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			requestID := body["request_id"]
			if test.status >= 400 {
				requestID = body["error"].(map[string]any)["request_id"]
			}
			if requestID != w.Header().Get("X-Request-ID") {
				t.Fatal("响应体与响应头请求编号不同")
			}
		})
	}
}

func TestCapabilitiesNeverEnableUnimplementedFeatures(t *testing.T) {
	for audience, handler := range map[string]http.Handler{
		"user": UserHandler(quietLogger()), "admin": AdminHandler(nil, quietLogger()),
	} {
		w := httptest.NewRecorder()
		path := "/api/v1/capabilities"
		if audience == "admin" {
			path = "/admin/api/v1/capabilities"
		}
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body["minimum_client_version"] != nil {
			t.Fatal("未发布正式客户端前不能声称已有兼容版本")
		}
		if body["audience"] != audience {
			t.Fatal("双端能力主体错误")
		}
		for _, section := range []string{"features", "pricing"} {
			for _, enabled := range body[section].(map[string]any) {
				if enabled != false {
					t.Fatal("误开放尚未实现的业务能力")
				}
			}
		}
		auth := body["authentication"].(map[string]any)
		if auth["enabled"] != false || len(auth["login_methods"].([]any)) != 0 {
			t.Fatal("误展示可用登录方式")
		}
		appearance := body["appearance"].(map[string]any)
		if appearance["default_color_scheme"] != "graphite" || len(appearance["color_schemes"].([]any)) != 3 {
			t.Fatal("外观与设计基准不一致")
		}
	}
}

func TestHealthAndSensitiveFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		check  checkFunc
		path   string
		status int
	}{
		{"存活不访问数据库", func(context.Context) error { t.Fatal("存活接口调用了数据库"); return nil }, "/health/live", 200},
		{"数据库就绪", func(context.Context) error { return nil }, "/health/ready", 200},
		{"数据库不可用", func(context.Context) error { return errors.New("private-test-value") }, "/health/ready", 503},
		{"异常恢复", func(context.Context) error { panic("private-test-value") }, "/health/ready", 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := AdminHandler(test.check, slog.New(slog.NewJSONHandler(&logs, nil)))
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", test.path+"?password=private-test-value", strings.NewReader("private-test-value"))
			r.Header.Set("Authorization", "Bearer private-test-value")
			r.Header.Set("Cookie", "session=private-test-value")
			handler.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("状态码 %d，期望 %d", w.Code, test.status)
			}
			if strings.Contains(logs.String()+w.Body.String(), "private-test-value") {
				t.Fatal("响应或日志泄露原始请求与异常")
			}
		})
	}
}

func TestImplementedResponsesConformToOpenAPI(t *testing.T) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("../../../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, method       string
		unavailable, panic bool
	}{
		{"/api/v1/capabilities", "GET", false, false},
		{"/api/v1/capabilities", "HEAD", false, false},
		{"/admin/api/v1/capabilities", "GET", false, false},
		{"/admin/api/v1/capabilities", "HEAD", false, false},
		{"/health/live", "GET", false, false}, {"/health/live", "HEAD", false, false},
		{"/health/ready", "GET", false, false}, {"/health/ready", "HEAD", false, false},
		{"/health/ready", "GET", true, false}, {"/health/ready", "HEAD", true, false},
		{"/health/ready", "GET", false, true}, {"/health/ready", "HEAD", false, true},
	} {
		t.Run(test.path+test.method, func(t *testing.T) {
			check := checkFunc(func(context.Context) error {
				if test.panic {
					panic("synthetic failure")
				}
				if test.unavailable {
					return errors.New("synthetic failure")
				}
				return nil
			})
			var handler http.Handler = AdminHandler(check, quietLogger())
			if strings.HasPrefix(test.path, "/api/") {
				handler = UserHandler(quietLogger())
			}
			r := httptest.NewRequest(test.method, test.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			route, params, err := router.FindRoute(r)
			if err != nil {
				t.Fatal(err)
			}
			input := &openapi3filter.ResponseValidationInput{
				RequestValidationInput: &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route},
				Status:                 w.Code, Header: w.Header(),
			}
			input.SetBodyBytes(w.Body.Bytes())
			if err := openapi3filter.ValidateResponse(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if test.method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD 返回了响应体")
			}
		})
	}
}
