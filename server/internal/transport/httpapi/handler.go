// Package httpapi 提供双端隔离的 REST、SSE 与静态客户端入口。
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type Readiness interface{ Check(context.Context) error }
type Options struct {
	Business           http.Handler
	Callbacks          http.Handler
	AssetsDir          string
	Capabilities       func(context.Context, string, string) map[string]any
	LongRequestTimeout time.Duration
}

func UserHandler(logger *slog.Logger) http.Handler { return NewUserHandler(logger, Options{}) }
func AdminHandler(check Readiness, logger *slog.Logger) http.Handler {
	return NewAdminHandler(check, logger, Options{})
}
func NewUserHandler(logger *slog.Logger, options Options) http.Handler {
	return handler("user", nil, logger, options)
}
func NewAdminHandler(check Readiness, logger *slog.Logger, options Options) http.Handler {
	return handler("admin", check, logger, options)
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(data)
}
func (w *responseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func handler(audience string, check Readiness, logger *slog.Logger, options Options) http.Handler {
	assets := assetHandler(options.AssetsDir)
	return http.HandlerFunc(func(raw http.ResponseWriter, r *http.Request) {
		w := &responseWriter{ResponseWriter: raw}
		id := web.NewRequestID()
		r = r.WithContext(web.WithRequestID(r.Context(), id))
		start := time.Now()
		operation := "unknown"
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		defer func() {
			if recover() != nil {
				if w.status == 0 {
					web.Error(w, r, 500, "internal_error", "操作失败，请稍后重试")
				}
				logger.Error("请求处理异常", "code", "handler_panic", "request_id", id)
			}
			status := w.status
			if status == 0 {
				status = 200
			}
			logger.Info("HTTP 请求", "audience", audience, "operation", operation, "status", status, "duration_ms", time.Since(start).Milliseconds(), "request_id", id)
		}()
		path := r.URL.Path
		capabilityPath := "/api/v1/capabilities"
		prefix := "/api/v1/"
		if audience == "admin" {
			capabilityPath = "/admin/api/v1/capabilities"
			prefix = "/admin/api/v1/"
		}
		switch {
		case path == capabilityPath:
			operation = "capabilities"
		case audience == "admin" && path == "/health/live":
			operation = "health_live"
		case audience == "admin" && path == "/health/ready":
			operation = "health_ready"
		case audience == "admin" && path == "/payments/callbacks/zpay" && options.Callbacks != nil:
			operation = "payment_callback"
			options.Callbacks.ServeHTTP(w, r)
			return
		case strings.HasPrefix(path, prefix) && options.Business != nil:
			operation = audience + "_business"
			if r.Method == "POST" && options.LongRequestTimeout > 0 && (path == "/api/v1/question-queries" || (strings.HasPrefix(path, "/admin/api/v1/question-models/") && strings.HasSuffix(path, "/tests"))) {
				controller := http.NewResponseController(w)
				_ = controller.SetReadDeadline(time.Now().Add(45 * time.Second))
				_ = controller.SetWriteDeadline(time.Now().Add(options.LongRequestTimeout + 45*time.Second))
			}
			options.Business.ServeHTTP(w, r)
			return
		default:
			// 未命中本端的 API、健康和付款路径不可回落到另一端或 SPA。
			reserved := strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/admin/api/") || strings.HasPrefix(path, "/health/") || strings.HasPrefix(path, "/payments/")
			if !reserved && assets != nil {
				operation = "static_asset"
				assets.ServeHTTP(w, r)
				return
			}
			web.Error(w, r, 404, "not_found", "请求的接口不存在")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			web.Error(w, r, 405, "method_not_allowed", "此接口仅支持 GET 和 HEAD")
			return
		}
		if operation == "capabilities" {
			body := capabilities(audience, id)
			if options.Capabilities != nil {
				body = options.Capabilities(r.Context(), audience, id)
			}
			web.JSON(w, r, 200, body)
			return
		}
		if operation == "health_ready" {
			var err error
			if check == nil {
				err = fault.New("readiness_missing", "就绪检查尚未配置")
			} else {
				err = check.Check(r.Context())
			}
			if err != nil {
				code, _ := fault.Describe(err)
				logger.Warn("服务未就绪", "code", code, "request_id", id)
				web.Error(w, r, 503, "service_unavailable", "服务暂未就绪")
				return
			}
		}
		web.JSON(w, r, 200, map[string]string{"status": "ok", "request_id": id})
	})
}
func capabilities(audience, id string) map[string]any {
	return map[string]any{
		"api_version": "v1", "minimum_client_version": nil, "audience": audience, "request_id": id,
		"authentication": map[string]any{"enabled": false, "login_methods": []string{}},
		"features":       map[string]bool{"course_sync": false, "scheduling": false, "task_execution": false, "wallet": false, "payments": false, "tiku": false, "preferences": false},
		"appearance":     map[string]any{"default_color_scheme": "graphite", "color_schemes": []string{"graphite", "grove", "indigo"}, "default_theme_mode": "system", "theme_modes": []string{"system", "light", "dark"}},
		"pricing":        map[string]bool{"service_configured": false, "tiku_configured": false, "automatic_charging": false},
	}
}
func Capabilities(audience, id string) map[string]any { return capabilities(audience, id) }
