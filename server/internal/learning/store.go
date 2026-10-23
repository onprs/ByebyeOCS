package learning

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// Service 是学习业务的唯一入口。构造后先迁移数据库，再调用 Register/Run。
type Service struct {
	pool *pgxpool.Pool
	opts Options

	mu      sync.Mutex
	running map[string]runningExecution
	closed  bool
	started bool
	runCtx  context.Context
	workers sync.WaitGroup
}

func New(pool *pgxpool.Pool, options *Options) (*Service, error) {
	if pool == nil {
		return nil, validation("数据库连接池不能为空")
	}
	opts := Options{}
	if options != nil {
		opts = *options
	}
	opts.withDefaults()
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &Service{pool: pool, opts: opts, running: make(map[string]runningExecution)}, nil
}

func (s *Service) now() time.Time { return s.opts.Now().UTC() }

func (s *Service) withTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return errServiceUnavailable
	}
	defer rollbackLearningTx(tx)
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errServiceUnavailable
	}
	return nil
}

// rollbackLearningTx 在请求已取消时仍清理事务，并限制失联数据库的清理等待。
func rollbackLearningTx(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

type txFunc func(pgx.Tx) error

// Page 是有界列表响应。
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

func clampLimit(value int) int {
	if value <= 0 {
		return 20
	}
	if value > 100 {
		return 100
	}
	return value
}

type cursor struct {
	Time time.Time
	ID   string
}

func encodeCursor(at time.Time, id string) string {
	raw := at.UTC().Format(time.RFC3339Nano) + "|" + id
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(value string) (cursor, error) {
	if value == "" {
		return cursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, validation("分页游标无效")
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return cursor{}, validation("分页游标无效")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil || len(parts[1]) != 36 {
		return cursor{}, validation("分页游标无效")
	}
	return cursor{Time: at.UTC(), ID: parts[1]}, nil
}

// Context helpers：HTTP 层写入请求编号，审计和事件复用同一个安全标识。
type contextKey int

const requestIDKey contextKey = 1

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok && validRequestID(value) {
		return value
	}
	return newRequestID()
}

func newRequestID() string { return web.NewRequestID() }

func validRequestID(value string) bool {
	return len(value) == 36 && strings.HasPrefix(value, "req_") && requestIDHex(value[4:])
}

func requestIDHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

// outbox 在同一业务事务写入事实；不假设数据库提交顺序。
type outboxEvent struct {
	EventID         string
	Topic           string
	ResourceType    string
	ResourceID      string
	UserID          string
	ResourceVersion int64
	Payload         map[string]any
	CreatedAt       time.Time
}

// OutboxRecord 供事件投递器读取；投递序号由投递器自行分配。
type OutboxRecord struct {
	EventID         string         `json:"event_id"`
	Topic           string         `json:"topic"`
	ResourceType    string         `json:"resource_type"`
	ResourceID      string         `json:"resource_id"`
	UserID          string         `json:"user_id,omitempty"`
	ResourceVersion int64          `json:"resource_version"`
	Payload         map[string]any `json:"payload"`
	CreatedAt       time.Time      `json:"created_at"`
	PublishedAt     *time.Time     `json:"published_at,omitempty"`
}

func writeOutbox(ctx context.Context, tx pgx.Tx, event outboxEvent) error {
	if event.EventID == "" {
		event.EventID = newID()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	var user any
	if event.UserID != "" {
		user = event.UserID
	}
	_, err := tx.Exec(ctx, `INSERT INTO public.learning_outbox
		(event_id, topic, resource_type, resource_id, user_id, resource_version, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		event.EventID, event.Topic, event.ResourceType, event.ResourceID, user, event.ResourceVersion, event.Payload, event.CreatedAt)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

// ListOutbox 返回尚未发布的事件，供主协调者统一投递。不返回投递序号。
func (s *Service) ListOutbox(ctx context.Context, limit int) ([]OutboxRecord, error) {
	limit = clampLimit(limit)
	rows, err := s.pool.Query(ctx, `SELECT event_id::text, topic, resource_type, resource_id::text,
		COALESCE(user_id::text, ''), resource_version, payload, created_at, published_at
		FROM public.learning_outbox WHERE published_at IS NULL
		ORDER BY created_at, event_id LIMIT $1`, limit)
	if err != nil {
		return nil, errServiceUnavailable
	}
	defer rows.Close()
	items := make([]OutboxRecord, 0, limit)
	for rows.Next() {
		var item OutboxRecord
		if err := rows.Scan(&item.EventID, &item.Topic, &item.ResourceType, &item.ResourceID,
			&item.UserID, &item.ResourceVersion, &item.Payload, &item.CreatedAt, &item.PublishedAt); err != nil {
			return nil, errServiceUnavailable
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errServiceUnavailable
	}
	return items, nil
}

// markOutboxPublished 仅由投递器在推送成功后调用；当前模块不自行推送 SSE。
func (s *Service) markOutboxPublished(ctx context.Context, eventID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE public.learning_outbox SET published_at = now()
		WHERE event_id = $1 AND published_at IS NULL`, eventID)
	if err != nil {
		return errServiceUnavailable
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, event AuditEvent) error {
	if event.RequestID == "" {
		event.RequestID = RequestID(ctx)
	}
	if err := audit.Record(ctx, tx, audit.Event{
		Actor: event.Actor, Action: event.Action, ResourceType: event.ResourceType,
		ResourceID: event.ResourceID, Reason: event.Reason, RequestID: event.RequestID,
	}); err != nil {
		return errServiceUnavailable
	}
	return nil
}

func nullableID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

// ensureAccount 读取并校验外部身份绑定。没有可信绑定就没有任务，服务不会用用户名兜底。
type accountRecord struct {
	UserID        string
	Provider      string
	Namespace     string
	Fingerprint   string
	School        string
	DisplayName   string
	BlockedCode   string
	BlockedReason string
	BlockedUntil  *time.Time
	LastClaimedAt *time.Time
	VerifiedAt    time.Time
}

func (s *Service) loadAccount(ctx context.Context, tx pgx.Tx, userID string) (accountRecord, error) {
	var item accountRecord
	err := tx.QueryRow(ctx, `SELECT user_id::text, provider, identity_namespace, identity_fingerprint,
		school, display_name, blocked_code, blocked_reason, blocked_until, last_claimed_at, verified_at
		FROM public.learning_accounts WHERE user_id = $1 FOR UPDATE`, userID).Scan(
		&item.UserID, &item.Provider, &item.Namespace, &item.Fingerprint, &item.School, &item.DisplayName,
		&item.BlockedCode, &item.BlockedReason, &item.BlockedUntil, &item.LastClaimedAt, &item.VerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return accountRecord{}, apiErr(409, "identity_not_linked", "账号尚未完成学习通身份绑定")
	}
	if err != nil {
		return accountRecord{}, errServiceUnavailable
	}
	return item, nil
}

func (s *Service) accountBlocked(item accountRecord, now time.Time) string {
	if item.BlockedCode != "" {
		return item.BlockedCode
	}
	if item.BlockedUntil != nil && item.BlockedUntil.After(now) {
		return "rate_limited"
	}
	return ""
}

// resolveSession 区分需要重新登录与本地服务暂时不可用，避免临时故障永久阻断账号。
func (s *Service) resolveSession(ctx context.Context, userID string) ([]byte, error) {
	if s.opts.Sessions == nil {
		return nil, Fail(FailureCapabilityMissing, "平台会话解析未配置")
	}
	session, err := s.opts.Sessions.PlatformSession(ctx, userID)
	if ctx.Err() == context.Canceled || errors.Is(err, context.Canceled) {
		return nil, Fail(FailureStopped, "平台会话读取已停止")
	}
	if err != nil {
		var classified *Failure
		if errors.As(err, &classified) {
			return nil, classified
		}
		code, _ := fault.Describe(err)
		if code == "platform_session_unavailable" {
			return nil, Fail(FailureIdentityInvalid, "平台会话不可用，需要重新验证")
		}
		return nil, Fail(FailureTransient, "平台会话暂不可读取，请稍后重试")
	}
	if len(session) == 0 {
		return nil, Fail(FailureIdentityInvalid, "平台会话不可用，需要重新验证")
	}
	return session, nil
}

// blockAccount 在账号范围停止新的外部操作，并给当前活跃尝试发出停止请求。
// 已发生的外部事实不会被抹掉。
func (s *Service) blockAccount(ctx context.Context, tx pgx.Tx, userID, code, reason string, until *time.Time) error {
	now := s.now()
	if _, err := tx.Exec(ctx, `UPDATE public.learning_accounts
		SET blocked_code = $2, blocked_reason = $3, blocked_at = $4, blocked_until = $5,
		    version = version + 1, updated_at = $4
		WHERE user_id = $1`, userID, code, truncate(reason, 300), now, until); err != nil {
		return errServiceUnavailable
	}
	if _, err := tx.Exec(ctx, `UPDATE public.learning_task_attempts a
		SET cancel_requested = true
		FROM public.learning_tasks t
		WHERE a.task_id = t.id AND a.user_id = $1 AND a.ended_at IS NULL
		  AND t.state IN ('preparing', 'running')`, userID); err != nil {
		return errServiceUnavailable
	}
	return nil
}

func truncate(value string, max int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, "\uFFFD"))
	count := 0
	for offset := range value {
		if count == max {
			return value[:offset]
		}
		count++
	}
	return value
}

// safeReason 防止把换行、控制字符写入审计或错误码。
func safeCode(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "unknown"
	}
	for _, r := range value {
		if !(r == '_' || r == '-' || r == '.' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return "unknown"
		}
	}
	return value
}

// describe 只记录安全错误码，不记录上游响应。
func describe(err error) string {
	var target *Error
	if errors.As(err, &target) {
		return target.Code
	}
	var failure *Failure
	if errors.As(err, &failure) {
		return string(failure.Class)
	}
	var safe *fault.Error
	if errors.As(err, &safe) {
		return safe.Code
	}
	return "internal_error"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ptrFloat(value float64) *float64 { return &value }

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func fmtDuration(seconds float64) string {
	return fmt.Sprintf("%.3f", seconds)
}
