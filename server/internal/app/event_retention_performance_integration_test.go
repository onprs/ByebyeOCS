//go:build integration

package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
)

func TestEventRetentionLookupDoesNotScanEntireStream(t *testing.T) {
	pool := testsupport.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO public.stream_heads(stream_key,sequence) VALUES('admin',5000);
		INSERT INTO public.outbox_events(id,audience,event_type,resource_version,payload)
		SELECT md5('fixture-event-'||i)::uuid,'admin','task.updated',1,jsonb_build_object('value',repeat('x',512)) FROM generate_series(1,5000) i;
		INSERT INTO public.stream_entries(stream_key,sequence,event_id,event_type,resource_version,payload)
		SELECT 'admin',row_number() OVER (ORDER BY id),id,event_type,resource_version,payload FROM public.outbox_events;
		ANALYZE public.stream_entries`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	blocks := func() int {
		t.Helper()
		var raw []byte
		if err := tx.QueryRow(ctx, `EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT 1 FROM public.stream_entries
			WHERE event_id='00000000-0000-4000-8000-000000000001' LIMIT 1`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var result []struct {
			Plan struct {
				Hits  int `json:"Shared Hit Blocks"`
				Reads int `json:"Shared Read Blocks"`
			} `json:"Plan"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result[0].Plan.Hits + result[0].Plan.Reads
	}
	indexed := blocks()
	if _, err := tx.Exec(ctx, `DROP INDEX public.stream_entries_event_id_idx`); err != nil {
		t.Fatal(err)
	}
	unindexed := blocks()
	if unindexed < 20 || indexed >= unindexed {
		t.Fatalf("事件外键查询仍扫描整流: indexed=%d baseline=%d", indexed, unindexed)
	}
	t.Logf("5000条投递记录、缺失事件查找：有索引%d个缓冲块，旧结构%d个缓冲块", indexed, unindexed)
}
