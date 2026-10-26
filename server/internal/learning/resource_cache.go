package learning

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CourseResourceCache 只共享章节卡片页码，个人状态始终通过当前用户会话读取。
// 缓存故障按未命中处理，不影响正常的平台读取。
type CourseResourceCache interface {
	LoadCardIndexes(context.Context, string) ([]int, time.Time, bool)
	StoreCardIndexes(context.Context, string, []int, time.Time)
}

// ResourceCachePlatform 在启动业务循环前接入共享缓存。
type ResourceCachePlatform interface {
	SetResourceCache(CourseResourceCache)
}

type postgresResourceCache struct{ pool *pgxpool.Pool }

func NewCourseResourceCache(pool *pgxpool.Pool) CourseResourceCache {
	return &postgresResourceCache{pool: pool}
}

func (c *postgresResourceCache) LoadCardIndexes(ctx context.Context, key string) ([]int, time.Time, bool) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	var indexes []int
	var expires time.Time
	err := c.pool.QueryRow(queryCtx, `SELECT card_indexes, expires_at
		FROM public.learning_resource_cache WHERE cache_key=$1 AND expires_at>now()`, key).Scan(&indexes, &expires)
	return indexes, expires, err == nil
}

func (c *postgresResourceCache) StoreCardIndexes(ctx context.Context, key string, indexes []int, expires time.Time) {
	queryCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = c.pool.Exec(queryCtx, `INSERT INTO public.learning_resource_cache (cache_key, card_indexes, expires_at)
		VALUES ($1,$2,$3) ON CONFLICT (cache_key) DO UPDATE
		SET card_indexes=EXCLUDED.card_indexes, expires_at=EXCLUDED.expires_at, updated_at=now()`, key, indexes, expires)
}
