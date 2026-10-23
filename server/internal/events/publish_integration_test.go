//go:build integration

package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// TestPublishLocksStreamHeadsInKeyOrder 校验投递泵按 stream_key 升序锁定流头：
// 批次内首次出现顺序与 key 顺序相反时，不得先锁后面的流，否则并发投递会形成环路等待。
func TestPublishLocksStreamHeadsInKeyOrder(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	first, second := web.NewID(), web.NewID()
	if first > second {
		first, second = second, first
	}
	for _, id := range []string{first, second} {
		if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	// 让 key 排序靠后的流先提交，使批次的首次出现顺序与 key 顺序相反。
	for index, id := range []string{second, first} {
		if _, err := pool.Exec(ctx, `INSERT INTO public.outbox_events(id,audience,user_id,event_type,resource_version,payload,occurred_at)
			VALUES($1,'user',$2,'task.updated',1,'{}'::jsonb,now()+$3::int*interval '1 second')`, web.NewID(), id, index); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"user/" + first, "user/" + second} {
		if _, err := pool.Exec(ctx, `INSERT INTO public.stream_heads(stream_key) VALUES($1)`, key); err != nil {
			t.Fatal(err)
		}
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, `SELECT stream_key FROM public.stream_heads WHERE stream_key=$1 FOR UPDATE`, "user/"+second); err != nil {
		t.Fatal(err)
	}
	service := New(pool)
	done := make(chan error, 1)
	go func() { done <- service.Publish(ctx, 10) }()

	// 投递事务必须已经持有排序靠前的流头，才可能阻塞在排序靠后的流头上。
	locked := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		probe, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, probeErr := probe.Exec(ctx, `SELECT stream_key FROM public.stream_heads WHERE stream_key=$1 FOR UPDATE NOWAIT`, "user/"+first)
		_ = probe.Rollback(ctx)
		var databaseErr *pgconn.PgError
		switch {
		case probeErr == nil:
			time.Sleep(50 * time.Millisecond)
			continue
		case errors.As(probeErr, &databaseErr) && databaseErr.Code == "55P03":
			locked = true
		}
		break
	}
	if !locked {
		t.Fatal("投递未先锁定排序靠前的流头")
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("解锁后投递未完成")
	}
	// 两条事件仍按提交时间分配到各自流。
	var sequenced int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries WHERE sequence=1`).Scan(&sequenced); err != nil || sequenced != 2 {
		t.Fatalf("流序号分配不完整：%d", sequenced)
	}
}

// TestPublishConcurrentBatchesDoNotDeadlock 用多 Publisher 交叠不同流验证：
// 任意批次次序下投递都能完成且每流序号仍连续、不重复。
func TestPublishConcurrentBatchesDoNotDeadlock(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	users := []string{web.NewID(), web.NewID(), web.NewID(), web.NewID(), web.NewID(), web.NewID()}
	for _, id := range users {
		if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	const total = 180
	for index := range total {
		audience, user := "admin", any(nil)
		if index%4 != 0 {
			audience, user = "user", users[index%len(users)]
		}
		if _, err := pool.Exec(ctx, `INSERT INTO public.outbox_events(id,audience,user_id,event_type,resource_version,payload,occurred_at)
			VALUES($1,$2,$3,'task.updated',1,jsonb_build_object('index',$4::int),now()+$4::int*interval '1 millisecond')`,
			web.NewID(), audience, user, index); err != nil {
			t.Fatal(err)
		}
	}
	service := New(pool)
	var wait sync.WaitGroup
	errors := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 10 {
				if err := service.Publish(ctx, 7); err != nil {
					errors <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		t.Fatalf("并发投递失败：%v", err)
	}
	// 剩余事件由串行补水完成，随后校验每个流的序号连续且无重复。
	for range 3 {
		if err := service.Publish(ctx, 500); err != nil {
			t.Fatal(err)
		}
	}
	var unpublished int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events WHERE published_at IS NULL`).Scan(&unpublished); err != nil || unpublished != 0 {
		t.Fatalf("仍有未投递事件：%d", unpublished)
	}
	var entries, sequences int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(DISTINCT (stream_key,sequence)) FROM public.stream_entries`).Scan(&entries, &sequences); err != nil || entries != total || sequences != total {
		t.Fatalf("投递序号重复或丢失：%d/%d（应为 %d）", entries, sequences, total)
	}
	rows, err := pool.Query(ctx, `SELECT stream_key,max(sequence),count(*) FROM public.stream_entries GROUP BY stream_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		var maximum, count int64
		if err := rows.Scan(&key, &maximum, &count); err != nil {
			t.Fatal(err)
		}
		if maximum != count {
			t.Fatalf("流 %s 序号不连续：max=%d count=%d", key, maximum, count)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// TestPublishKeepsPerStreamOrderAcrossBatches 校验批量分配序号后每个流仍按 (occurred_at,id) 连续递增。
func TestPublishKeepsPerStreamOrderAcrossBatches(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	users := []string{web.NewID(), web.NewID(), web.NewID()}
	for _, id := range users {
		if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	// 交错写入用户流与管理员流，并让提交顺序与写入顺序不同，避免按主键顺序恰好相等。
	const total = 240
	order := make([]string, 0, total)
	for i := range total {
		audience, user := "admin", any(nil)
		if i%3 != 0 {
			username := users[i%len(users)]
			audience, user = "user", username
			order = append(order, "user/"+username)
		} else {
			order = append(order, "admin")
		}
		if _, err := pool.Exec(ctx, `INSERT INTO public.outbox_events(id,audience,user_id,event_type,resource_version,payload,occurred_at)
			VALUES($1,$2,$3,'task.updated',1,jsonb_build_object('index',$4::int),now()+$4::int*interval '1 millisecond')`,
			web.NewID(), audience, user, i); err != nil {
			t.Fatal(err)
		}
	}
	service := New(pool)
	for range 5 {
		if err := service.Publish(ctx, 70); err != nil {
			t.Fatal(err)
		}
	}
	var published int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events WHERE published_at IS NOT NULL`).Scan(&published); err != nil || published != total {
		t.Fatalf("投递未覆盖全部事件：%d", published)
	}
	// 每个流内的序号必须是 1..n 连续，且事件顺序与 occurred_at 一致。
	rows, err := pool.Query(ctx, `SELECT e.stream_key,e.sequence,e.payload->>'index' FROM public.stream_entries e ORDER BY e.stream_key,e.sequence`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expected := map[string]int64{}
	for _, key := range order {
		expected[key]++
	}
	lastSequence := map[string]int64{}
	lastIndex := map[string]int64{}
	seen := map[string]int64{}
	for rows.Next() {
		var key, index string
		var sequence int64
		if err := rows.Scan(&key, &sequence, &index); err != nil {
			t.Fatal(err)
		}
		if sequence != lastSequence[key]+1 {
			t.Fatalf("流 %s 序号不连续：%d", key, sequence)
		}
		var value int64
		if _, err := fmt.Sscanf(index, "%d", &value); err != nil {
			t.Fatalf("事件载荷无效：%v", err)
		}
		if previous, ok := lastIndex[key]; ok && value <= previous {
			t.Fatalf("流 %s 未按提交顺序投递：%d 在 %d 之后", key, value, previous)
		}
		lastSequence[key] = sequence
		lastIndex[key] = value
		seen[key] = sequence
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	for key, count := range expected {
		if seen[key] != count {
			t.Fatalf("流 %s 投递数量错误：%d/%d", key, seen[key], count)
		}
	}
	// 已投递事件不能被重复投递。
	if err := service.Publish(ctx, 500); err != nil {
		t.Fatal(err)
	}
	var entries int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.stream_entries`).Scan(&entries); err != nil || entries != total {
		t.Fatalf("重复投递产生了额外事件：%d", entries)
	}
}
