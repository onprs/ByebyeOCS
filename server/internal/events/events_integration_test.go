//go:build integration

package events

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestPublishFollowsCommitAndIsIdempotent(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	user := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal("创建测试用户失败")
	}
	service := New(pool)
	held, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if err := Record(ctx, held, Event{Audience: "user", UserID: user, Type: "task.updated", ResourceID: web.NewID(), Version: 1}); err != nil {
		t.Fatal(err)
	}
	committed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Record(ctx, committed, Event{Audience: "user", UserID: user, Type: "wallet.updated", ResourceID: web.NewID(), Version: 1}); err != nil {
		t.Fatal(err)
	}
	if err := committed.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.Publish(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := pool.QueryRow(ctx, "SELECT event_type FROM public.stream_entries WHERE stream_key=$1 AND sequence=1", "user/"+user).Scan(&kind); err != nil || kind != "wallet.updated" {
		t.Fatal("未提交事件占用了投递顺序")
	}
	if err := held.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 4)
	for range 4 {
		wait.Add(1)
		go func() { defer wait.Done(); results <- service.Publish(ctx, 100) }()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if pool.QueryRow(ctx, "SELECT count(*) FROM public.stream_entries WHERE stream_key=$1", "user/"+user).Scan(&count) != nil || count != 2 {
		t.Fatal("并发或重复投递产生重复事件")
	}
	if err := pool.QueryRow(ctx, "SELECT event_type FROM public.stream_entries WHERE stream_key=$1 AND sequence=2", "user/"+user).Scan(&kind); err != nil || kind != "task.updated" {
		t.Fatal("提交后的事件未正确排序")
	}
}
func TestRelayIsAtomicAndKeepsAudience(t *testing.T) {
	db := testsupport.NewDatabase(t)
	pool := db.Pool()
	ctx := context.Background()
	user := web.NewID()
	resource := web.NewID()
	if _, err := pool.Exec(ctx, "INSERT INTO public.users(id)VALUES($1)", user); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.learning_outbox(event_id,user_id,resource_type,resource_id,resource_version,topic,payload)VALUES($1,$2,'task',$3,2,'task.updated','{"state":"paused"}')`, web.NewID(), user, resource); err != nil {
		t.Fatal("创建测试事件失败")
	}
	service := New(pool)
	for range 2 {
		if err := service.RelayLearning(ctx, 100); err != nil {
			t.Fatal(err)
		}
		if err := service.Publish(ctx, 100); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if pool.QueryRow(ctx, "SELECT count(*) FROM public.stream_entries").Scan(&count) != nil || count != 2 {
		t.Fatal("桥接未保持单次用户/管理员投递")
	}
	var body []byte
	if pool.QueryRow(ctx, "SELECT payload FROM public.stream_entries WHERE stream_key='admin'").Scan(&body) != nil {
		t.Fatal("管理员事件不存在")
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil || value["state"] != nil || value["user_id"] != user {
		t.Fatal("管理员流未采用最小通知载荷")
	}
}
