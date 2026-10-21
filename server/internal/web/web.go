// Package web 提供身份上下文、统一错误和有界 JSON 编解码。
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

type Actor struct {
	ID              string
	Kind            string
	SessionID       string
	Permissions     []string
	AuthenticatedAt time.Time
}
type contextKey uint8

const (
	actorKey contextKey = iota
	requestKey
)

func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}
func ActorFrom(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorKey).(Actor)
	return actor, ok && actor.ID != ""
}
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestKey, id)
}
func RequestID(ctx context.Context) string { id, _ := ctx.Value(requestKey).(string); return id }
func NewRequestID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	return "req_" + hex.EncodeToString(value[:])
}
func NewID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[:4], value[4:6], value[6:8], value[8:10], value[10:])
}
func ValidID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, c := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
func HasPermission(actor Actor, permission string) bool {
	if permission == "" {
		return true
	}
	for _, p := range actor.Permissions {
		if p == "*" || p == permission {
			return true
		}
	}
	return false
}
func Require(w http.ResponseWriter, r *http.Request, kind, permission string) (Actor, bool) {
	actor, ok := ActorFrom(r.Context())
	if !ok {
		Error(w, r, 401, "unauthenticated", "请先登录")
		return Actor{}, false
	}
	if actor.Kind != kind || !HasPermission(actor, permission) {
		Error(w, r, 403, "forbidden", "当前身份无权执行此操作")
		return Actor{}, false
	}
	return actor, true
}
func Fresh(actor Actor, now time.Time) bool {
	return !actor.AuthenticatedAt.IsZero() && now.Sub(actor.AuthenticatedAt) >= 0 && now.Sub(actor.AuthenticatedAt) <= 5*time.Minute
}

func JSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if id := RequestID(r.Context()); id != "" {
		w.Header().Set("X-Request-ID", id)
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	id := RequestID(r.Context())
	if id == "" {
		id = NewRequestID()
		r = r.WithContext(WithRequestID(r.Context(), id))
	}
	scope := "request"
	if status == 503 {
		scope = "service"
	}
	JSON(w, r, status, map[string]any{"error": map[string]any{"code": code, "message": message, "request_id": id, "retryable": status == 503 || status == 429, "scope": scope}})
}
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	code, message := fault.Describe(err)
	status := http.StatusInternalServerError
	switch code {
	case "unauthenticated", "session_expired", "invalid_credentials":
		status = 401
	case "forbidden", "csrf_invalid", "origin_invalid", "step_up_required":
		status = 403
	case "not_found":
		status = 404
	case "conflict", "insufficient_balance", "idempotency_conflict":
		status = 409
	case "version_conflict":
		status = 412
	case "validation_failed", "invalid_amount":
		status = 422
	case "precondition_required":
		status = 428
	case "rate_limited":
		status = 429
	case "not_configured", "external_disabled", "service_unavailable", "provider_unavailable", "usage_incomplete", "backup_unavailable", "storage_pressure", "recovery_required":
		status = 503
	}
	Error(w, r, status, code, message)
}
func Decode(w http.ResponseWriter, r *http.Request, target any) bool {
	return DecodeLimit(w, r, target, 1<<20)
}
func DecodeLimit(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	contentType := strings.Split(r.Header.Get("Content-Type"), ";")[0]
	if contentType != "application/json" {
		Error(w, r, 415, "unsupported_media_type", "请使用 JSON 请求")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		Error(w, r, 422, "validation_failed", "请求格式无效、字段未知或内容过大")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		Error(w, r, 422, "validation_failed", "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
func ETag(w http.ResponseWriter, version int64) {
	w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, version))
}
func MatchVersion(r *http.Request) (int64, error) {
	value := r.Header.Get("If-Match")
	if value == "" {
		return 0, fault.New("precondition_required", "请先读取最新版本再保存")
	}
	if len(value) < 4 || !strings.HasPrefix(value, `"v`) || !strings.HasSuffix(value, `"`) {
		return 0, fault.New("validation_failed", "版本条件无效")
	}
	version, err := strconv.ParseInt(value[2:len(value)-1], 10, 64)
	if err != nil || version < 1 {
		return 0, fault.New("validation_failed", "版本条件无效")
	}
	return version, nil
}
func Limit(r *http.Request) (int, error) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 50, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > 100 {
		return 0, fault.New("validation_failed", "每页数量必须为 1 到 100")
	}
	return n, nil
}
