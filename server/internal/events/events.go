// Package events 将已提交 Outbox 转成有序且有界的投递流。
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// encodedEvent 是已校验、可直接写入 Outbox 的事件行；空串表示该列为 NULL。
type encodedEvent struct {
	audience, user, kind, resource, payload string
	version                                 int64
}

// encodeEvent 集中做事件校验与载荷编码，单条写入与批量桥接共用同一套规则。
func encodeEvent(event Event) (encodedEvent, error) {
	if !web.ValidResourceID(event.Type) || len(event.Type) > 128 || event.Version < 0 {
		return encodedEvent{}, fault.New("validation_failed", "事件信息无效")
	}
	if event.Audience != "user" && event.Audience != "admin" {
		return encodedEvent{}, fault.New("validation_failed", "事件接收方无效")
	}
	item := encodedEvent{audience: event.Audience, kind: event.Type, version: event.Version}
	if event.Audience == "user" {
		if !web.ValidID(event.UserID) {
			return encodedEvent{}, fault.New("validation_failed", "事件用户编号无效")
		}
		item.user = event.UserID
	} else if event.UserID != "" {
		return encodedEvent{}, fault.New("validation_failed", "管理员事件不应携带用户流归属")
	}
	if event.ResourceID != "" {
		if !web.ValidResourceID(event.ResourceID) {
			return encodedEvent{}, fault.New("validation_failed", "事件资源编号无效")
		}
		item.resource = event.ResourceID
	}
	data := event.Data
	if data == nil {
		data = map[string]any{}
	}
	encoded, err := json.Marshal(data)
	if err != nil || len(encoded) > 16384 {
		return encodedEvent{}, fault.New("validation_failed", "事件载荷超出限制")
	}
	item.payload = string(encoded)
	return item, nil
}

func Record(ctx context.Context, tx pgx.Tx, event Event) error {
	if tx == nil {
		return fault.New("validation_failed", "事件信息无效")
	}
	item, err := encodeEvent(event)
	if err != nil {
		return err
	}
	if err := insertEvents(ctx, tx, []encodedEvent{item}); err != nil {
		return err
	}
	return nil
}

// insertEvents 整批写入 Outbox，语句数不随事件数量增长。
func insertEvents(ctx context.Context, tx pgx.Tx, items []encodedEvent) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	audiences := make([]string, len(items))
	users := make([]string, len(items))
	kinds := make([]string, len(items))
	resources := make([]string, len(items))
	versions := make([]int64, len(items))
	payloads := make([]string, len(items))
	for i, item := range items {
		ids[i] = web.NewID()
		audiences[i] = item.audience
		users[i] = item.user
		kinds[i] = item.kind
		resources[i] = item.resource
		versions[i] = item.version
		payloads[i] = item.payload
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.outbox_events(id,audience,user_id,event_type,resource_id,resource_version,payload)
		SELECT v.id::uuid,v.audience,NULLIF(v.user_id,'')::uuid,v.event_type,NULLIF(v.resource_id,''),v.version,v.payload::jsonb
		FROM unnest($1::text[],$2::text[],$3::text[],$4::text[],$5::text[],$6::bigint[],$7::text[])
			AS v(id,audience,user_id,event_type,resource_id,version,payload)`,
		ids, audiences, users, kinds, resources, versions, payloads); err != nil {
		return fault.New("event_write_failed", "无法记录业务事件")
	}
	return nil
}

type Service struct {
	pool         *pgxpool.Pool
	heartbeat    time.Duration
	sessionCheck func(context.Context, web.Actor) error
	limits       *streamLimiter
	failures     *failureLog
	stop         chan struct{}
	stopOnce     sync.Once
}

// streamQueryTimeout 限制长连接上单次读库与会话校验的等待，避免停滞查询长期占用连接池。
const streamQueryTimeout = 3 * time.Second

// maxIdlePollDelay 限制空闲流的事件轮询间隔。
// 满连接数时每流每秒固定查询会形成与业务无关的持续库压力；无事件时逐级退避，
// 有新事件后立即回到 basePollDelay，持续有事件时频率与顺序均与原先一致。
const (
	basePollDelay    = time.Second
	maxIdlePollDelay = 3 * time.Second
)

// rollbackTimeout 限定未提交事务的回滚等待；连接异常时不能无限阻塞。
const rollbackTimeout = 3 * time.Second

func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_ = tx.Rollback(ctx)
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, heartbeat: 15 * time.Second, limits: &streamLimiter{maximum: 64, actors: map[string]int{}}, stop: make(chan struct{})}
}

// SetLogger 装配后台任务失败日志；应在 Run 开始前调用。
func (s *Service) SetLogger(logger *slog.Logger) {
	s.failures = &failureLog{logger: logger, scope: "events", interval: failureLogInterval}
}

// Stop 结束所有已建立的实时流，供服务停止时先断开长连接；可重复调用。
func (s *Service) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
}

func (s *Service) stopping() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}
func (s *Service) SetSessionCheck(check func(context.Context, web.Actor) error) {
	s.sessionCheck = check
}

// sessionValid 在有限时限内复核会话与权限；没有装配校验器时视为不变。
func (s *Service) sessionValid(ctx context.Context, actor web.Actor) bool {
	if s.sessionCheck == nil {
		return true
	}
	checkCtx, cancel := context.WithTimeout(ctx, streamQueryTimeout)
	defer cancel()
	return s.sessionCheck(checkCtx, actor) == nil
}
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", s.userStream)
	mux.HandleFunc("GET /admin/api/v1/events", s.adminStream)
}
func (s *Service) Run(ctx context.Context) error {
	// 退出时断开已有实时流，避免后台停止后长连接继续占用。
	defer s.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			batchCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			if err := s.RelayLearning(batchCtx, 100); err != nil {
				s.failures.report("relay_learning", err)
			}
			if err := s.Publish(batchCtx, 100); err != nil {
				s.failures.report("publish", err)
			}
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
	defer rollback(tx)
	rows, err := tx.Query(ctx, `SELECT id::text,audience,COALESCE(user_id::text,'') FROM public.outbox_events WHERE published_at IS NULL ORDER BY occurred_at,id LIMIT $1 FOR UPDATE SKIP LOCKED`, limit)
	if err != nil {
		return fault.New("service_unavailable", "无法读取待投递事件")
	}
	ids := make([]string, 0, limit)
	totals := make(map[string]int64, 8)
	for rows.Next() {
		var id, audience, user string
		if err := rows.Scan(&id, &audience, &user); err != nil {
			rows.Close()
			return fault.New("service_unavailable", "事件记录格式无效")
		}
		ids = append(ids, id)
		key := audience
		if audience == "user" {
			key = "user/" + user
		}
		totals[key]++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fault.New("service_unavailable", "无法读取待投递事件")
	}
	if len(ids) == 0 {
		return nil
	}
	// 流头按 stream_key 排序后统一加锁：并发 Publisher 的批次次序不同也不会形成环路等待。
	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	counts := make([]int64, len(keys))
	for i, key := range keys {
		counts[i] = totals[key]
	}
	// 整批按流聚合写入，语句数不随事件数量增长：逐个事件往返会在批次变大时超出投递时限并长期占用连接。
	if _, err := tx.Exec(ctx, `INSERT INTO public.stream_heads(stream_key) SELECT DISTINCT unnest($1::text[]) ORDER BY 1 ON CONFLICT DO NOTHING`, keys); err != nil {
		return fault.New("service_unavailable", "无法建立投递流")
	}
	if _, err := tx.Exec(ctx, `SELECT stream_key FROM public.stream_heads WHERE stream_key=ANY($1::text[]) ORDER BY stream_key FOR UPDATE`, keys); err != nil {
		return fault.New("service_unavailable", "无法锁定投递流")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.stream_heads h SET sequence=h.sequence+v.count,updated_at=now() FROM unnest($1::text[],$2::bigint[]) AS v(key,count) WHERE h.stream_key=v.key`, keys, counts); err != nil {
		return fault.New("service_unavailable", "无法分配投递序号")
	}
	// 序号沿用批次顺序：(occurred_at,id) 在每个流内连续递增，与逐条分配等价。
	if _, err := tx.Exec(ctx, `WITH batch AS (
			SELECT o.id,o.event_type,o.resource_id,o.resource_version,o.payload,o.occurred_at,
			       CASE WHEN o.audience='user' THEN 'user/'||o.user_id::text ELSE 'admin' END AS stream_key
			FROM public.outbox_events o WHERE o.id=ANY($1::uuid[])
		), positioned AS (
			SELECT b.*,row_number() OVER (PARTITION BY b.stream_key ORDER BY b.occurred_at,b.id) AS position,
			       count(*) OVER (PARTITION BY b.stream_key) AS total
			FROM batch b
		), inserted AS (
			INSERT INTO public.stream_entries(stream_key,sequence,event_id,event_type,resource_id,resource_version,payload)
			SELECT p.stream_key,h.sequence-p.total+p.position,p.id,p.event_type,p.resource_id,p.resource_version,p.payload
			FROM positioned p JOIN public.stream_heads h ON h.stream_key=p.stream_key
			RETURNING event_id
		)
		UPDATE public.outbox_events o SET published_at=now() FROM inserted i WHERE o.id=i.event_id`, ids); err != nil {
		return fault.New("service_unavailable", "无法保存投递事件")
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
	if s.stopping() {
		w.Header().Set("Retry-After", "5")
		web.Error(w, r, 503, "service_unavailable", "事件流正在停止，请稍后重新连接")
		return
	}
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
	// 读流头与校验会话都限时：库端停滞时不能把连接和协程无限留在长连接上。
	headCtx, cancelHead := context.WithTimeout(r.Context(), streamQueryTimeout)
	err = s.pool.QueryRow(headCtx, `SELECT sequence,earliest_sequence FROM public.stream_heads WHERE stream_key=$1`, key).Scan(&sequence, &earliest)
	cancelHead()
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
	// 空载节拍每秒一次会话与权限复核；事件查询与投递也在同一节拍内完成，
	// 保证每批业务事件在发出前都经过一次位于本次查询之后的复核。
	tick := time.NewTicker(basePollDelay)
	defer tick.Stop()
	heartbeat := time.NewTicker(s.heartbeat)
	defer heartbeat.Stop()
	type entry struct {
		sequence           int64
		id, kind, resource string
		version            int64
		data               json.RawMessage
	}
	// deliver 查询一批待投递事件，并在发出前用最新会话与权限复核；
	// 查询自身最长等待 streamQueryTimeout，不能沿用在查询开始前的复核结果。
	deliver := func() (delivered bool, alive bool) {
		queryCtx, cancel := context.WithTimeout(r.Context(), streamQueryTimeout)
		defer cancel()
		rows, err := s.pool.Query(queryCtx, `SELECT sequence,event_id::text,event_type,COALESCE(resource_id::text,''),resource_version,payload FROM public.stream_entries WHERE stream_key=$1 AND sequence>$2 ORDER BY sequence LIMIT 100`, key, after)
		if err != nil {
			return false, false
		}
		batch := []entry{}
		for rows.Next() {
			var item entry
			if err := rows.Scan(&item.sequence, &item.id, &item.kind, &item.resource, &item.version, &item.data); err != nil {
				rows.Close()
				return false, false
			}
			batch = append(batch, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, false
		}
		// 本节拍的会话与权限复核：查询可能已占用到 streamQueryTimeout，
		// 必须在本批发出前用当前状态重新校验，不能沿用查询开始前的结论。
		if !s.sessionValid(r.Context(), actor) {
			emit("reauth_required", "", map[string]string{"code": "session_expired"})
			return false, false
		}
		if len(batch) == 0 {
			// 空批次也已复核本节拍，下一节拍仍按空闲节奏继续。
			return false, true
		}
		skipped := false
		for _, item := range batch {
			if actor.Kind == "admin" && !allowedAdminEvent(actor, item.kind) {
				after = item.sequence
				skipped = true
				continue
			}
			if !emit(item.kind, strconv.FormatInt(item.sequence, 10), map[string]any{"event_id": item.id, "resource_id": item.resource, "resource_version": item.version, "data": item.data}) {
				return false, false
			}
			after = item.sequence
		}
		if skipped && !emit("watermark", strconv.FormatInt(after, 10), map[string]string{"watermark": strconv.FormatInt(after, 10)}) {
			return false, false
		}
		return true, true
	}
	pollDelay := basePollDelay
	nextPoll := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			// 服务停止时先断开长连接，正常请求由各自的处理流程结束。
			return
		case <-heartbeat.C:
			// 心跳只保持连接活跃，不重复查询会话。
			if !emit("heartbeat", "", map[string]string{"time": time.Now().UTC().Format(time.RFC3339)}) {
				return
			}
		case <-tick.C:
			if time.Now().Before(nextPoll) {
				// 未到轮询时刻：本节拍只复核会话与权限，撤销与降权仍在一秒内断开。
				if !s.sessionValid(r.Context(), actor) {
					emit("reauth_required", "", map[string]string{"code": "session_expired"})
					return
				}
				continue
			}
			delivered, alive := deliver()
			if !alive {
				return
			}
			// 无新事件时退避轮询，有事件后立即恢复每秒查询；事件顺序仍由流水号决定。
			if delivered {
				pollDelay = basePollDelay
			} else if pollDelay < maxIdlePollDelay {
				pollDelay = min(pollDelay+basePollDelay, maxIdlePollDelay)
			}
			nextPoll = time.Now().Add(pollDelay)
		}
	}
}
