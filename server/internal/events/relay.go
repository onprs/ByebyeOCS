package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// RelayLearning 在事务内承接课程服务的 Outbox；崩溃不会留下半投递状态。
func (s *Service) RelayLearning(ctx context.Context, limit int) error {
	if limit < 1 || limit > 500 {
		return fault.New("validation_failed", "事件批次数量无效")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fault.New("service_unavailable", "事件桥接暂不可用")
	}
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT event_id::text,COALESCE(user_id::text,''),resource_type,resource_id::text,resource_version,topic,payload FROM public.learning_outbox WHERE published_at IS NULL ORDER BY created_at,event_id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return fault.New("service_unavailable", "无法读取课程事件")
	}
	type item struct {
		id, user, resourceType, resource, kind string
		version                                int64
		data                                   []byte
	}
	batch := []item{}
	for rows.Next() {
		var entry item
		if err := rows.Scan(&entry.id, &entry.user, &entry.resourceType, &entry.resource, &entry.version, &entry.kind, &entry.data); err != nil {
			rows.Close()
			return fault.New("service_unavailable", "课程事件格式无效")
		}
		batch = append(batch, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fault.New("service_unavailable", "课程事件暂不可读")
	}
	entries := make([]encodedEvent, 0, len(batch)*2)
	ids := make([]string, 0, len(batch))
	for _, entry := range batch {
		var payload map[string]any
		if json.Unmarshal(entry.data, &payload) != nil {
			return fault.New("service_unavailable", "课程事件载荷无效")
		}
		if entry.user != "" {
			item, err := encodeEvent(Event{Audience: "user", UserID: entry.user, Type: entry.kind, ResourceID: entry.resource, Version: entry.version, Data: payload})
			if err != nil {
				return err
			}
			entries = append(entries, item)
		}
		item, err := encodeEvent(Event{Audience: "admin", Type: entry.kind, ResourceID: entry.resource, Version: entry.version, Data: map[string]any{"user_id": entry.user, "resource_type": entry.resourceType}})
		if err != nil {
			return err
		}
		entries = append(entries, item)
		ids = append(ids, entry.id)
	}
	// 整批聚合写入并一次性确认已桥接，语句数不随事件数量增长。
	if err := insertEvents(ctx, tx, entries); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.learning_outbox SET published_at=now() WHERE event_id=ANY($1::uuid[])`, ids); err != nil {
		return fault.New("service_unavailable", "无法确认课程事件桥接")
	}
	if tx.Commit(ctx) != nil {
		return fault.New("service_unavailable", "课程事件桥接结果待核对")
	}
	return nil
}

func allowedAdminEvent(actor web.Actor, kind string) bool {
	if web.HasPermission(actor, "*") {
		return true
	}
	for _, prefix := range []string{"learning.", "task.", "plan.", "course.", "account.", "sync."} {
		if strings.HasPrefix(kind, prefix) {
			return web.HasPermission(actor, "learning.read")
		}
	}
	for _, prefix := range []string{"wallet.", "ledger.", "payment.", "billing.", "order."} {
		if strings.HasPrefix(kind, prefix) {
			return web.HasPermission(actor, "billing.read")
		}
	}
	if strings.HasPrefix(kind, "question.") || strings.HasPrefix(kind, "questions.") || strings.HasPrefix(kind, "tiku.") {
		return web.HasPermission(actor, "question:read")
	}
	if strings.HasPrefix(kind, "privacy.") {
		return web.HasPermission(actor, "privacy.review")
	}
	return false
}

type streamLimiter struct {
	mu              sync.Mutex
	maximum, active int
	actors          map[string]int
}

func (l *streamLimiter) acquire(actor web.Actor) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := actor.Kind + ":" + actor.ID
	if l.active >= l.maximum || l.actors[key] >= 3 {
		return false
	}
	l.active++
	l.actors[key]++
	return true
}
func (l *streamLimiter) release(actor web.Actor) {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := actor.Kind + ":" + actor.ID
	l.active--
	l.actors[key]--
	if l.actors[key] <= 0 {
		delete(l.actors, key)
	}
}
func (s *Service) SetMaxStreams(maximum int) error {
	if maximum < 1 || maximum > 1000 {
		return fault.New("validation_failed", "事件连接上限无效")
	}
	s.limits.mu.Lock()
	defer s.limits.mu.Unlock()
	s.limits.maximum = maximum
	return nil
}
