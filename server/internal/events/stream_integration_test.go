//go:build integration

package events

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type streamFrame struct {
	id, event, data string
}

// streamClient 读取 SSE 帧；超时即关闭连接，避免测试挂死。
type streamClient struct {
	body   io.ReadCloser
	frames chan streamFrame
	errors chan error
}

func openStream(t *testing.T, server *httptest.Server, actor web.Actor, path, cursor string) (*streamClient, *http.Response) {
	t.Helper()
	if cursor != "" {
		path += "?cursor=" + cursor
	}
	request, err := http.NewRequest("GET", server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	client := &streamClient{body: response.Body, frames: make(chan streamFrame, 16), errors: make(chan error, 1)}
	go func() {
		reader := bufio.NewReader(response.Body)
		var current streamFrame
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				client.errors <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if current.event == "" && current.data == "" && current.id == "" {
					continue
				}
				select {
				case client.frames <- current:
				default:
				}
				current = streamFrame{}
			case strings.HasPrefix(line, "id: "):
				current.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "event: "):
				current.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return client, response
}

func (c *streamClient) next(t *testing.T) streamFrame {
	t.Helper()
	select {
	case frame := <-c.frames:
		return frame
	case err := <-c.errors:
		t.Fatalf("事件流提前结束：%v", err)
	case <-time.After(8 * time.Second):
		t.Fatal("等待事件帧超时")
	}
	return streamFrame{}
}

func (c *streamClient) close() { _ = c.body.Close() }

func testStreamServer(t *testing.T, service *Service, actor web.Actor) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	service.Register(mux)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(web.WithActor(r.Context(), actor)))
	}))
	t.Cleanup(server.Close)
	return server
}

// recordUserEvent 记录并投递用户事件，同时用显式事件时间固定顺序：
// 同一事务内记录的事件共享提交时间，按主键排序时无法稳定断言先后。
func recordUserEvent(t *testing.T, pool *pgxpool.Pool, user, kind string, offset time.Duration) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := Record(ctx, tx, Event{Audience: "user", UserID: user, Type: kind, ResourceID: web.NewID(), Version: 3, Data: map[string]any{"state": kind}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.outbox_events SET occurred_at=now()+$1::int*interval '1 millisecond' WHERE user_id=$2 AND event_type=$3`, int(offset/time.Millisecond), user, kind); err != nil {
		t.Fatal(err)
	}
}

func TestStreamResumesFromCursorAndRequiresResync(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	for index, kind := range []string{"task.claimed", "task.state_changed", "task.confirmed", "task.attempt_finished"} {
		recordUserEvent(t, pool, user, kind, time.Duration(index+1)*time.Second)
	}
	if err := service.Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: user, Kind: "user", SessionID: web.NewID()}
	server := testStreamServer(t, service, actor)

	// 续传：游标 2 只应收到 3、4，且载荷字段来自实际投递记录。
	client, response := openStream(t, server, actor, "/api/v1/events", "2")
	defer client.close()
	if response.StatusCode != 200 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("续传未建立事件流：%d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	connected := client.next(t)
	var handshake map[string]any
	if connected.event != "connected" || json.Unmarshal([]byte(connected.data), &handshake) != nil || handshake["watermark"] != "2" || handshake["resync_snapshot"] != false {
		t.Fatalf("续传握手指针错误：%+v", connected)
	}
	for _, expected := range []struct{ id, kind string }{{"3", "task.confirmed"}, {"4", "task.attempt_finished"}} {
		frame := client.next(t)
		var body struct {
			EventID         string         `json:"event_id"`
			ResourceVersion int64          `json:"resource_version"`
			Data            map[string]any `json:"data"`
		}
		if frame.id != expected.id || frame.event != expected.kind || json.Unmarshal([]byte(frame.data), &body) != nil {
			t.Fatalf("续传帧错误：%+v", frame)
		}
		if !web.ValidID(body.EventID) || body.ResourceVersion != 3 || body.Data["state"] != expected.kind {
			t.Fatalf("续传事件字段错误：%+v", body)
		}
	}

	// 保留期清理后旧游标必须要求重取快照，不能静默返回空流。
	if _, err := pool.Exec(ctx, `DELETE FROM public.stream_entries WHERE stream_key=$1 AND sequence<=3`, "user/"+user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.stream_heads SET earliest_sequence=4 WHERE stream_key=$1`, "user/"+user); err != nil {
		t.Fatal(err)
	}
	stale, err := http.Get(server.URL + "/api/v1/events?cursor=2")
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Body.Close()
	var failure struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if stale.StatusCode != 409 || json.NewDecoder(stale.Body).Decode(&failure) != nil || failure.Error.Code != "resync_required" {
		t.Fatalf("过期游标未被拒绝：%d %s", stale.StatusCode, failure.Error.Code)
	}

	// 边界：游标等于 earliest-1 时保留的事件仍可续传。
	edge, response := openStream(t, server, actor, "/api/v1/events", "3")
	defer edge.close()
	if response.StatusCode != 200 {
		t.Fatalf("边界游标被误判过期：%d", response.StatusCode)
	}
	if frame := edge.next(t); frame.event != "connected" {
		t.Fatalf("边界游标未建立事件流：%+v", frame)
	}
	if frame := edge.next(t); frame.id != "4" || frame.event != "task.attempt_finished" {
		t.Fatalf("边界游标丢失保留事件：%+v", frame)
	}

	// 首次连接从当前水位开始并要求重取快照。
	fresh, response := openStream(t, server, actor, "/api/v1/events", "")
	defer fresh.close()
	if response.StatusCode != 200 {
		t.Fatalf("首次连接失败：%d", response.StatusCode)
	}
	var first map[string]any
	if frame := fresh.next(t); json.Unmarshal([]byte(frame.data), &first) != nil || first["watermark"] != "4" || first["resync_snapshot"] != true {
		t.Fatalf("首次连接水位错误：%+v", frame)
	}
}

func TestAdminStreamSkipsEventsOutsidePermissions(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for index, kind := range []string{"task.updated", "wallet.updated"} {
		if err := Record(ctx, tx, Event{Audience: "admin", Type: kind, ResourceID: web.NewID(), Version: 1, Data: map[string]any{"user_id": user}}); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, `UPDATE public.outbox_events SET occurred_at=now()+$1::int*interval '1 millisecond' WHERE event_type=$2`, (index+1)*1000, kind); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: web.NewID(), Kind: "admin", SessionID: web.NewID(), Permissions: []string{"events.read", "learning.read"}}
	server := testStreamServer(t, service, actor)
	client, response := openStream(t, server, actor, "/admin/api/v1/events", "0")
	defer client.close()
	if response.StatusCode != 200 {
		t.Fatalf("管理员事件流未建立：%d", response.StatusCode)
	}
	if frame := client.next(t); frame.event != "connected" {
		t.Fatalf("缺少握手帧：%+v", frame)
	}
	if frame := client.next(t); frame.event != "task.updated" || frame.id != "1" {
		t.Fatalf("越权过滤错误：%+v", frame)
	}
	// 被过滤的事件只推进水位，不泄露载荷。
	if frame := client.next(t); frame.event != "watermark" || frame.id != "2" || frame.data != `{"watermark":"2"}` {
		t.Fatalf("过滤后未推进水位：%+v", frame)
	}
}

func TestStreamClosesWhenSessionCheckStalls(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	stalled := make(chan time.Duration, 4)
	service.SetSessionCheck(func(checkCtx context.Context, _ web.Actor) error {
		start := time.Now()
		<-checkCtx.Done()
		stalled <- time.Since(start)
		return checkCtx.Err()
	})
	actor := web.Actor{ID: user, Kind: "user", SessionID: web.NewID()}
	server := testStreamServer(t, service, actor)
	client, response := openStream(t, server, actor, "/api/v1/events", "")
	defer client.close()
	if response.StatusCode != 200 {
		t.Fatalf("事件流未建立：%d", response.StatusCode)
	}
	if frame := client.next(t); frame.event != "connected" {
		t.Fatalf("缺少握手帧：%+v", frame)
	}
	started := time.Now()
	if frame := client.next(t); frame.event != "reauth_required" {
		t.Fatalf("会话校验超时未结束事件流：%+v", frame)
	}
	if elapsed := time.Since(started); elapsed > 6*time.Second {
		t.Fatalf("会话校验超时后事件流仍占用连接 %s", elapsed)
	}
	select {
	case waited := <-stalled:
		if waited > streamQueryTimeout+2*time.Second {
			t.Fatalf("会话校验未按时限中断：%s", waited)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("会话校验未按时限中断")
	}
	deadline := time.Now().Add(2 * time.Second)
	for activeStreams(service) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active, actors := activeStreams(service), activeActors(service); active != 0 || actors != 0 {
		t.Fatalf("事件流结束后未释放连接名额：%d/%d", active, actors)
	}
}

func activeStreams(service *Service) int {
	service.limits.mu.Lock()
	defer service.limits.mu.Unlock()
	return service.limits.active
}

func activeActors(service *Service) int {
	service.limits.mu.Lock()
	defer service.limits.mu.Unlock()
	return len(service.limits.actors)
}

// streamEntryIndexScans 读取投递表索引扫描次数，用作空闲轮询频度的受控计数。
func streamEntryIndexScans(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	var scans int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(idx_scan,0) FROM pg_stat_user_tables WHERE relname='stream_entries'`).Scan(&scans); err != nil {
		t.Fatal(err)
	}
	return scans
}

// TestStreamRechecksSessionBeforeDeliveringBatch 校验每批业务事件发出前都重新复核会话与权限，
// 不依赖节拍分支的先后：查询本身可占用到 streamQueryTimeout，期间的撤销必须拦住本批投递。
func TestStreamRechecksSessionBeforeDeliveringBatch(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	recordUserEvent(t, pool, user, "task.claimed", time.Second)
	if err := service.Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	service.SetSessionCheck(func(context.Context, web.Actor) error {
		if calls.Add(1) == 1 {
			return nil
		}
		return errors.New("会话已撤销")
	})
	actor := web.Actor{ID: user, Kind: "user", SessionID: web.NewID()}
	server := testStreamServer(t, service, actor)
	client, response := openStream(t, server, actor, "/api/v1/events", "0")
	defer client.close()
	if response.StatusCode != 200 {
		t.Fatalf("事件流未建立：%d", response.StatusCode)
	}
	if frame := client.next(t); frame.event != "connected" {
		t.Fatalf("缺少握手帧：%+v", frame)
	}
	// 首个批次在复核通过后正常投递。
	if frame := client.next(t); frame.event != "task.claimed" || frame.id != "1" {
		t.Fatalf("首个批次未投递：%+v", frame)
	}
	// 之后新增一批事件；下一节拍复核已失败，本批不得发出。
	recordUserEvent(t, pool, user, "task.confirmed", 2*time.Second)
	if err := service.Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if frame := client.next(t); frame.event != "reauth_required" {
		t.Fatalf("撤销后仍投递了本批事件：%+v", frame)
	}
	if calls.Load() < 2 {
		t.Fatalf("批次投递前未复核会话：仅 %d 次", calls.Load())
	}
	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE stream_key=$1`, "user/"+user).Scan(&entries); err != nil || entries != 2 {
		t.Fatalf("待投递事件被重复分配：%d", entries)
	}
}

// TestStopClosesActiveStreams 校验服务停止时已有长连接立即结束、重复调用幂等、停止后新连接被拒绝。
func TestStopClosesActiveStreams(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	actor := web.Actor{ID: user, Kind: "user", SessionID: web.NewID()}
	server := testStreamServer(t, service, actor)
	client, response := openStream(t, server, actor, "/api/v1/events", "")
	defer client.close()
	if response.StatusCode != 200 {
		t.Fatalf("事件流未建立：%d", response.StatusCode)
	}
	if frame := client.next(t); frame.event != "connected" {
		t.Fatalf("缺少握手帧：%+v", frame)
	}
	service.Stop()
	service.Stop()
	select {
	case <-client.errors:
	case frame := <-client.frames:
		t.Fatalf("停止后仍收到帧：%+v", frame)
	case <-time.After(5 * time.Second):
		t.Fatal("停止后长连接未结束")
	}
	deadline := time.Now().Add(2 * time.Second)
	for activeStreams(service) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if active := activeStreams(service); active != 0 {
		t.Fatalf("停止后未释放连接名额：%d", active)
	}
	rejected, err := http.Get(server.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	defer rejected.Body.Close()
	if rejected.StatusCode != 503 || rejected.Header.Get("Retry-After") == "" {
		t.Fatalf("停止后新连接未被拒绝：%d", rejected.StatusCode)
	}
}

// TestStreamIdlePollingIsBounded 确认无事件时投递轮询逐级退避，且会话复核仍保持每秒一次。
func TestStreamIdlePollingIsBounded(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	checks := make(chan time.Time, 32)
	service.SetSessionCheck(func(context.Context, web.Actor) error {
		select {
		case checks <- time.Now():
		default:
		}
		return nil
	})
	actor := web.Actor{ID: user, Kind: "user", SessionID: web.NewID()}
	server := testStreamServer(t, service, actor)
	before := streamEntryIndexScans(t, pool)
	client, response := openStream(t, server, actor, "/api/v1/events", "")
	if response.StatusCode != 200 {
		t.Fatalf("事件流未建立：%d", response.StatusCode)
	}
	if frame := client.next(t); frame.event != "connected" {
		t.Fatalf("缺少握手帧：%+v", frame)
	}
	// 3.5 秒空闲：固定每秒轮询会查 3 次，退避后只应查 1 秒与 3 秒两次。
	time.Sleep(3500 * time.Millisecond)
	client.close()
	deadline := time.Now().Add(3 * time.Second)
	for activeStreams(service) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	// 等 PostgreSQL 累计统计刷新。
	time.Sleep(2 * time.Second)
	if delta := streamEntryIndexScans(t, pool) - before; delta > 2 {
		t.Fatalf("空闲投递轮询未退避：3.5 秒内查询 %d 次", delta)
	}
	if len(checks) < 3 {
		t.Fatalf("会话复核频率被削弱：3.5 秒内仅 %d 次", len(checks))
	}
}
