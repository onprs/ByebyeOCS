// Package operations 提供受权的维护、审计、隐私请求与磁盘保护。
package operations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type Options struct {
	DataDir             string
	Key                 []byte
	DecryptionKeys      [][]byte
	ExportMaxBytes      int64
	RetentionInterval   time.Duration
	Runtime             map[string]any
	DiskProtection      bool
	RequireFreshBackup  bool
	BackupStatusFile    string
	BackupMaxAge        time.Duration
	QuestionConcurrency int
	Export              func(context.Context, string) (map[string]any, error)
	RevokeUser          func(context.Context, pgx.Tx, string) error
}
type Service struct {
	pool          *pgxpool.Pool
	options       Options
	questionSlots chan struct{}
}

func New(pool *pgxpool.Pool, options Options) (*Service, error) {
	if pool == nil || len(options.Key) != 32 || options.DataDir == "" {
		return nil, fault.New("config_invalid", "维护服务配置不完整")
	}
	if options.ExportMaxBytes == 0 {
		options.ExportMaxBytes = 8 << 20
	}
	if options.BackupMaxAge == 0 {
		options.BackupMaxAge = 15 * time.Minute
	}
	if options.RetentionInterval == 0 {
		options.RetentionInterval = 24 * time.Hour
	}
	if options.QuestionConcurrency == 0 {
		options.QuestionConcurrency = 2
	}
	if options.QuestionConcurrency < 1 || options.QuestionConcurrency > 16 {
		return nil, fault.New("config_invalid", "题库并发上限无效")
	}
	if len(options.DecryptionKeys) > 7 {
		return nil, fault.New("config_invalid", "导出解密密钥环最多8个版本")
	}
	keys := make([][]byte, 0, len(options.DecryptionKeys))
	for _, key := range options.DecryptionKeys {
		if len(key) != 32 {
			return nil, fault.New("config_invalid", "导出解密密钥必须为32字节")
		}
		keys = append(keys, append([]byte(nil), key...))
	}
	options.Key = append([]byte(nil), options.Key...)
	options.DecryptionKeys = keys
	service := &Service{pool: pool, options: options, questionSlots: make(chan struct{}, options.QuestionConcurrency)}
	if service.options.Export == nil {
		service.options.Export = service.ExportUserData
	}
	return service, nil
}
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/api/v1/audit-events", s.auditEvents)
	mux.HandleFunc("GET /admin/api/v1/settings/runtime", s.runtimeSettings)
	mux.HandleFunc("GET /admin/api/v1/maintenance", s.runtimeSettings)
	mux.HandleFunc("POST /api/v1/me/data-exports", s.createExport)
	mux.HandleFunc("GET /api/v1/me/data-exports", s.listExports)
	mux.HandleFunc("GET /api/v1/me/data-exports/{id}", s.getExport)
	mux.HandleFunc("GET /api/v1/me/data-exports/{id}/download", s.downloadExport)
	mux.HandleFunc("POST /api/v1/me/deletion-requests", s.createDeletion)
	mux.HandleFunc("GET /api/v1/me/deletion-requests", s.listDeletions)
	mux.HandleFunc("GET /admin/api/v1/data-requests", s.adminRequests)
	mux.HandleFunc("POST /admin/api/v1/data-requests/{id}/review", s.reviewDeletion)
}

type Disk struct {
	Status         string  `json:"status"`
	UsedPercent    float64 `json:"used_percent"`
	AvailableBytes string  `json:"available_bytes"`
}

func (s *Service) Disk() Disk {
	var info syscall.Statfs_t
	if syscall.Statfs(s.options.DataDir, &info) != nil || info.Blocks == 0 {
		return Disk{Status: "unknown"}
	}
	percent := float64(info.Blocks-info.Bavail) * 100 / float64(info.Blocks)
	state := "ok"
	if percent >= 90 {
		state = "maintenance"
	} else if percent >= 80 {
		state = "protected"
	} else if percent >= 70 {
		state = "warning"
	}
	return Disk{Status: state, UsedPercent: percent, AvailableBytes: strconv.FormatUint(info.Bavail*uint64(info.Bsize), 10)}
}
func (s *Service) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		questionRequest := r.Method == "POST" && (r.URL.Path == "/api/v1/question-queries" || (strings.HasPrefix(r.URL.Path, "/admin/api/v1/question-models/") && strings.HasSuffix(r.URL.Path, "/tests")))
		if questionRequest {
			release, err := s.AcquireQuestion(r.Context())
			if err != nil {
				code, _ := fault.Describe(err)
				if code == "rate_limited" {
					w.Header().Set("Retry-After", "3")
					web.Error(w, r, 429, code, "题库正在处理其他查询，请稍后重试")
				} else {
					web.Fail(w, r, err)
				}
				return
			}
			defer release()
		}
		if s.options.DiskProtection && r.Method == "POST" {
			path := r.URL.Path
			large := path == "/api/v1/course-syncs" || path == "/api/v1/plans" || path == "/api/v1/question-queries" || path == "/api/v1/recharge-orders" || path == "/api/v1/me/data-exports"
			if large {
				disk := s.Disk()
				if disk.Status == "protected" || disk.Status == "maintenance" || disk.Status == "unknown" {
					web.Error(w, r, 503, "storage_pressure", "当前存储受限，请稍后再发起新操作")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Service) runtimeSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "operations.read"); !ok {
		return
	}
	web.JSON(w, r, 200, map[string]any{"runtime": s.options.Runtime, "storage": s.Disk(), "backup": s.Backup(), "retention": map[string]any{"stream_days": 3, "outbox_days": 7, "question_content_days": 7, "export_hours": 24, "financial_records": "retained"}, "generated_at": time.Now().UTC()})
}

type auditCursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func (s *Service) auditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "audit.read"); !ok {
		return
	}
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	cursor := auditCursor{Time: time.Now().UTC().Add(time.Second), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}
	if encoded := r.URL.Query().Get("cursor"); encoded != "" {
		data, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || json.Unmarshal(data, &cursor) != nil || !web.ValidID(cursor.ID) {
			web.Error(w, r, 422, "validation_failed", "分页游标无效")
			return
		}
	}
	rows, err := s.pool.Query(r.Context(), `SELECT id::text,actor_kind,COALESCE(actor_user_id::text,actor_admin_id::text,''),action,resource_type,COALESCE(resource_key,resource_id::text,''),request_id,COALESCE(reason,''),occurred_at FROM public.audit_events WHERE (occurred_at,id)<($1,$2::uuid) ORDER BY occurred_at DESC,id DESC LIMIT $3`, cursor.Time, cursor.ID, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法读取审计记录")
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	var last auditCursor
	more := false
	for rows.Next() {
		var id, kind, actor, action, resourceType, resource, requestID, reason string
		var occurred time.Time
		if rows.Scan(&id, &kind, &actor, &action, &resourceType, &resource, &requestID, &reason, &occurred) != nil {
			web.Error(w, r, 503, "service_unavailable", "审计记录暂不可读")
			return
		}
		if len(items) == limit {
			more = true
			break
		}
		items = append(items, map[string]any{"id": id, "actor_kind": kind, "actor_id": actor, "action": action, "resource_type": resourceType, "resource_id": resource, "request_id": requestID, "reason": reason, "occurred_at": occurred})
		last = auditCursor{Time: occurred, ID: id}
	}
	if rows.Err() != nil {
		web.Error(w, r, 503, "service_unavailable", "读取审计记录失败")
		return
	}
	var next any
	if more {
		data, _ := json.Marshal(last)
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	web.JSON(w, r, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *Service) Run(ctx context.Context) error {
	work := time.NewTicker(time.Second)
	defer work.Stop()
	retention := time.NewTicker(s.options.RetentionInterval)
	defer retention.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-work.C:
			job, cancel := context.WithTimeout(ctx, 15*time.Second)
			_ = s.processExport(job)
			cancel()
		case <-retention.C:
			job, cancel := context.WithTimeout(ctx, 30*time.Second)
			_ = s.Cleanup(job)
			cancel()
		}
	}
}
func (s *Service) Prepare() error {
	if err := os.MkdirAll(s.options.DataDir, 0700); err != nil {
		return fault.New("data_directory_invalid", "无法创建数据目录")
	}
	info, err := os.Stat(s.options.DataDir)
	if err != nil || !info.IsDir() {
		return fault.New("data_directory_invalid", "数据目录无效")
	}
	return nil
}
func (s *Service) record(ctx context.Context, tx pgx.Tx, actor web.Actor, action, resourceID, reason string) error {
	return audit.Record(ctx, tx, audit.Event{Actor: actor, Action: action, ResourceType: "data_request", ResourceID: resourceID, Reason: strings.TrimSpace(reason), RequestID: web.RequestID(ctx)})
}
