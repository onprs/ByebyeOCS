// Package events 将已提交 Outbox 转成有序且有界的投递流。
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type Event struct {
	Audience, UserID, Type, ResourceID string
	Version                            int64
	Data                               map[string]any
}

func Record(ctx context.Context, tx pgx.Tx, event Event) error {
	if tx == nil || !web.ValidResourceID(event.Type) || len(event.Type) > 128 || event.Version < 0 {
		return fault.New("validation_failed", "事件信息无效")
	}
	if event.Audience != "user" && event.Audience != "admin" {
		return fault.New("validation_failed", "事件接收方无效")
	}
	var owner, resource any
	if event.Audience == "user" {
		if !web.ValidID(event.UserID) {
			return fault.New("validation_failed", "事件用户编号无效")
		}
		owner = event.UserID
	} else if event.UserID != "" {
		return fault.New("validation_failed", "管理员事件不应携带用户流归属")
	}
	if event.ResourceID != "" {
		if !web.ValidResourceID(event.ResourceID) {
			return fault.New("validation_failed", "事件资源编号无效")
		}
		resource = event.ResourceID
	}
	data := event.Data
	if data == nil {
		data = map[string]any{}
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > 16384 {
		return fault.New("validation_failed", "事件载荷超出限制")
	}
	_, err = tx.Exec(ctx, `INSERT INTO public.outbox_events(id,audience,user_id,event_type,resource_id,resource_version,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, web.NewID(), event.Audience, owner, event.Type, resource, event.Version, encoded)
	if err != nil {
		return fault.New("event_write_failed", "无法记录业务事件")
	}
	return nil
}

type Service struct {
	pool         *pgxpool.Pool
	heartbeat    time.Duration
	sessionCheck func(context.Context, web.Actor) error
	limits       *streamLimiter
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, heartbeat: 15 * time.Second, limits: &streamLimiter{maximum: 64, actors: map[string]int{}}}
}
func (s *Service) SetSessionCheck(check func(context.Context, web.Actor) error) {
	s.sessionCheck = check
}
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", s.userStream)
	mux.HandleFunc("GET /admin/api/v1/events", s.adminStream)
}
func (s *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			batchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_ = s.RelayLearning(batchCtx, 100)
			_ = s.Publish(batchCtx, 100)
			cancel()
		}
	}
}
func (s *Service) Publish(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		return fault.New("validation_failed", "事件批次数量无效")
	}
	// 每批在事务里领取已提交的事件，再锁定各流头。序号不会先于业务提交分配。
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fault.New("service_unavailable", "事件投递暂不可用")
	}
	defer tx.Rollback(context.Background())
	rows, err := tx.Query(ctx, `SELECT id::text,audience,COALESCE(user_id::text,''),event_type,COALESCE(resource_id::text,''),resource_version,payload FROM public.outbox_events WHERE published_at IS NULL ORDER BY occurred_at,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return fault.New("service_unavailable", "无法读取待投递事件")
	}
	type pending struct {
		id, audience, user, kind, resource string
		version                            int64
		data                               []byte
	}
	items := []pending{}
	for rows.Next() {
		var item pending
		if err := rows.Scan(&item.id, &item.audience, &item.user, &item.kind, &item.resource, &item.version, &item.data); err != nil {
			rows.Close()
			return fault.New("service_unavailable", "事件记录格式无效")
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fault.New("service_unavailable", "无法读取待投递事件")
	}
	for _, item := range items {
		key := item.audience
		if item.audience == "user" {
			key += "/" + item.user
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.stream_heads(stream_key) VALUES($1) ON CONFLICT DO NOTHING`, key); err != nil {
			return fault.New("service_unavailable", "无法建立投递流")
		}
		var sequence int64
		if err := tx.QueryRow(ctx, `UPDATE public.stream_heads SET sequence=sequence+1,updated_at=now() WHERE stream_key=$1 RETURNING sequence`, key).Scan(&sequence); err != nil {
			return fault.New("service_unavailable", "无法分配投递序号")
		}
		var resource any
		if item.resource != "" {
			resource = item.resource
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.stream_entries(stream_key,sequence,event_id,event_type,resource_id,resource_version,payload) VALUES($1,$2,$3,$4,$5,$6,$7)`, key, sequence, item.id, item.kind, resource, item.version, item.data); err != nil {
			return fault.New("service_unavailable", "无法保存投递事件")
		}
		if _, err := tx.Exec(ctx, `UPDATE public.outbox_events SET published_at=now() WHERE id=$1`, item.id); err != nil {
			return fault.New("service_unavailable", "无法确认事件投递")
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fault.New("service_unavailable", "事件投递结果待重查")
	}
	return nil
}
func (s *Service) userStream(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	s.stream(w, r, actor, "user/"+actor.ID)
}
func (s *Service) adminStream(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "events.read")
	if !ok {
		return
	}
	s.stream(w, r, actor, "admin")
}
func (s *Service) stream(w http.ResponseWriter, r *http.Request, actor web.Actor, key string) {
	if !s.limits.acquire(actor) {
		w.Header().Set("Retry-After", "5")
		web.Error(w, r, 429, "rate_limited", "实时连接数量已达上限，请稍后重试")
		return
	}
	defer s.limits.release(actor)
	cursor := r.Header.Get("Last-Event-ID")
	if cursor == "" {
		cursor = r.URL.Query().Get("cursor")
	}
	var after int64
	var err error
	if cursor != "" {
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			web.Error(w, r, 422, "validation_failed", "事件游标无效")
			return
		}
	}
	var sequence, earliest int64
	err = s.pool.QueryRow(r.Context(), `SELECT sequence,earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, key).Scan(&sequence, &earliest)
	if err != nil && err != pgx.ErrNoRows {
		web.Error(w, r, 503, "service_unavailable", "事件流暂不可用")
		return
	}
	if earliest == 0 {
		earliest = 1
	}
	if cursor != "" && (after < earliest-1 || after > sequence) {
		web.Error(w, r, 409, "resync_required", "事件游标已过期，请重新读取最新状态")
		return
	}
	if cursor == "" {
		after = sequence
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	emit := func(kind, id string, value any) bool {
		body, err := json.Marshal(value)
		if err != nil {
			return false
		}
		if strings.ContainsAny(kind, "\r\n") {
			return false
		}
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if id != "" {
			if _, err := fmt.Fprintf(w, "id: %s\n", id); err != nil {
				return false
			}
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, body); err != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !emit("connected", "", map[string]any{"watermark": strconv.FormatInt(after, 10), "resync_snapshot": cursor == ""}) {
		return
	}
	poll := time.NewTicker(time.Second)
	defer poll.Stop()
	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if s.sessionCheck != nil && s.sessionCheck(r.Context(), actor) != nil {
				emit("reauth_required", "", map[string]string{"code": "session_expired"})
				return
			}
			if !emit("heartbeat", "", map[string]string{"time": time.Now().UTC().Format(time.RFC3339)}) {
				return
			}
		case <-poll.C:
			if s.sessionCheck != nil && s.sessionCheck(r.Context(), actor) != nil {
				emit("reauth_required", "", map[string]string{"code": "session_expired"})
				return
			}
			queryCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			rows, err := s.pool.Query(queryCtx, `SELECT sequence,event_id::text,event_type,COALESCE(resource_id::text,''),resource_version,payload FROM public.stream_entries WHERE stream_key=$1 AND sequence>$2 ORDER BY sequence LIMIT 100`, key, after)
			if err != nil {
				cancel()
				return
			}
			type entry struct {
				sequence           int64
				id, kind, resource string
				version            int64
				data               json.RawMessage
			}
			batch := []entry{}
			for rows.Next() {
				var item entry
				if err := rows.Scan(&item.sequence, &item.id, &item.kind, &item.resource, &item.version, &item.data); err != nil {
					rows.Close()
					cancel()
					return
				}
				batch = append(batch, item)
			}
			err = rows.Err()
			rows.Close()
			cancel()
			if err != nil {
				return
			}
			skipped := false
			for _, item := range batch {
				if actor.Kind == "admin" && !allowedAdminEvent(actor, item.kind) {
					after = item.sequence
					skipped = true
					continue
				}
				if !emit(item.kind, strconv.FormatInt(item.sequence, 10), map[string]any{"event_id": item.id, "resource_id": item.resource, "resource_version": item.version, "data": item.data}) {
					return
				}
				after = item.sequence
			}
			if skipped && !emit("watermark", strconv.FormatInt(after, 10), map[string]string{"watermark": strconv.FormatInt(after, 10)}) {
				return
			}
		}
	}
}
