//go:build integration

package events

import (
	"context"
	"sync"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// TestRelayHandlesBacklogConcurrentlyWithoutDuplicates 校验课程事件桥接批量化后：
// 并发领取不重复、每条事件仍产出用户与管理员两路、管理员载荷仍为最小通知、重复执行不追加。
func TestRelayHandlesBacklogConcurrentlyWithoutDuplicates(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	users := []string{web.NewID(), web.NewID(), web.NewID(), web.NewID()}
	for _, id := range users {
		if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	const total = 120
	for index := range total {
		if _, err := pool.Exec(ctx, `INSERT INTO public.learning_outbox(event_id,user_id,resource_type,resource_id,resource_version,topic,payload)
			VALUES($1,$2,'task',$3,2,'task.state_changed',jsonb_build_object('state','running','index',$4::int))`,
			web.NewID(), users[index%len(users)], web.NewID(), index); err != nil {
			t.Fatal(err)
		}
	}
	service := New(pool)
	var wait sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 6 {
				if err := service.RelayLearning(ctx, 20); err != nil {
					failures <- err
					return
				}
				if err := service.Publish(ctx, 40); err != nil {
					failures <- err
					return
				}
			}
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("并发桥接失败：%v", err)
	}
	for range 3 {
		if err := service.RelayLearning(ctx, 500); err != nil {
			t.Fatal(err)
		}
		if err := service.Publish(ctx, 500); err != nil {
			t.Fatal(err)
		}
	}
	var userEvents, adminEvents, unpublished, userWithState, adminWithState int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM public.outbox_events WHERE audience='user'),
		(SELECT count(*) FROM public.outbox_events WHERE audience='admin'),
		(SELECT count(*) FROM public.learning_outbox WHERE published_at IS NULL),
		(SELECT count(*) FROM public.outbox_events WHERE audience='user' AND payload ? 'state'),
		(SELECT count(*) FROM public.outbox_events WHERE audience='admin' AND payload ? 'state')`).Scan(
		&userEvents, &adminEvents, &unpublished, &userWithState, &adminWithState); err != nil {
		t.Fatal(err)
	}
	if userEvents != total || adminEvents != total || unpublished != 0 {
		t.Fatalf("桥接数量错误：用户=%d 管理员=%d 未确认=%d", userEvents, adminEvents, unpublished)
	}
	if userWithState != total || adminWithState != 0 {
		t.Fatalf("载荷分层错误：用户带状态=%d 管理员带状态=%d", userWithState, adminWithState)
	}
	// 重复执行不得追加桥接事件。
	if err := service.RelayLearning(ctx, 500); err != nil {
		t.Fatal(err)
	}
	var afterRepeat int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.outbox_events`).Scan(&afterRepeat); err != nil || afterRepeat != total*2 {
		t.Fatalf("重复桥接追加了事件：%d", afterRepeat)
	}
	// 每个流序号连续且无重复。
	rows, err := pool.Query(ctx, `SELECT stream_key,max(sequence),count(*) FROM public.stream_entries GROUP BY stream_key`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	streams := 0
	for rows.Next() {
		var key string
		var maximum, count int64
		if err := rows.Scan(&key, &maximum, &count); err != nil {
			t.Fatal(err)
		}
		if maximum != count {
			t.Fatalf("流 %s 序号不连续：max=%d count=%d", key, maximum, count)
		}
		streams++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if streams != len(users)+1 {
		t.Fatalf("投递流数量错误：%d", streams)
	}
}
