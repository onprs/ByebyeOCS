package chaoxing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/learning"
)

const resourceCacheTTL = 30 * time.Minute
const resourceCacheLimit = 4096

type cardLayout struct {
	indexes []int
	expires time.Time
	usedAt  time.Time
}

type resourceCache struct {
	mu      sync.Mutex
	entries map[string]cardLayout
	flights map[string]chan struct{}
	store   learning.CourseResourceCache
	hits    uint64
	misses  uint64
}

// ResourceCacheStats 是运维统计，不包含课程标识或用户信息。
type ResourceCacheStats struct {
	Hits   uint64 `json:"hits"`
	Misses uint64 `json:"misses"`
}

func (p *Platform) SetResourceCache(store learning.CourseResourceCache) {
	p.resourceCache.mu.Lock()
	defer p.resourceCache.mu.Unlock()
	p.resourceCache.store = store
}

func (p *Platform) ResourceCacheStats() ResourceCacheStats {
	p.resourceCache.mu.Lock()
	defer p.resourceCache.mu.Unlock()
	return ResourceCacheStats{Hits: p.resourceCache.hits, Misses: p.resourceCache.misses}
}

// resourceKey 限定适配版本、上游、课程、班级和章节；不以个人CPI或Cookie分割共享目录。
func (p *Platform) resourceKey(course learning.PlatformCourseRef, knowledge string) string {
	raw := strings.Join([]string{adapterVersion, "card-layout-v1", p.options.endpoints.Study,
		p.options.endpoints.Cards, course.ExternalCourseID, course.ExternalClassID, course.CycleKey, knowledge}, "\x00")
	digest := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(digest[:])
}

func validCardIndexes(indexes []int) bool {
	if len(indexes) == 0 || len(indexes) > maxChapterCards {
		return false
	}
	previous := -1
	for _, index := range indexes {
		if index <= previous || index < 0 || index >= maxChapterCards {
			return false
		}
		previous = index
	}
	return true
}

// cachedCardIndexes 合并同目录的并发回源；失败仅返回发起该请求的用户，不向等待者传播身份错误。
// 调用前须已用当前会话读取课程目录，确认用户拥有该课程访问权。
func (p *Platform) cachedCardIndexes(ctx context.Context, key string, load func() ([]int, error)) ([]int, error) {
	cache := &p.resourceCache
	for {
		now := time.Now()
		cache.mu.Lock()
		if cache.entries == nil {
			cache.entries = map[string]cardLayout{}
			cache.flights = map[string]chan struct{}{}
		}
		if entry, ok := cache.entries[key]; ok && now.Before(entry.expires) {
			entry.usedAt = now
			cache.entries[key] = entry
			cache.hits++
			cache.mu.Unlock()
			return append([]int(nil), entry.indexes...), nil
		}
		if pending, ok := cache.flights[key]; ok {
			cache.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		cache.flights[key] = pending
		store := cache.store
		cache.mu.Unlock()
		indexes, expires, hit := []int(nil), time.Time{}, false
		if store != nil {
			indexes, expires, hit = store.LoadCardIndexes(ctx, key)
			hit = hit && validCardIndexes(indexes) && expires.After(now) && !expires.After(now.Add(resourceCacheTTL))
		}
		var err error
		if !hit {
			indexes, err = load()
			expires = time.Now().Add(resourceCacheTTL)
			if err == nil && validCardIndexes(indexes) && store != nil {
				store.StoreCardIndexes(ctx, key, indexes, expires)
			}
		}
		cache.mu.Lock()
		if hit {
			cache.hits++
		} else {
			cache.misses++
		}
		if err == nil && validCardIndexes(indexes) {
			if len(cache.entries) >= resourceCacheLimit {
				oldestKey, oldest := "", time.Now()
				for candidate, entry := range cache.entries {
					if !entry.expires.After(now) {
						oldestKey = candidate
						break
					}
					if entry.usedAt.Before(oldest) {
						oldestKey, oldest = candidate, entry.usedAt
					}
				}
				delete(cache.entries, oldestKey)
			}
			cache.entries[key] = cardLayout{indexes: append([]int(nil), indexes...), expires: expires, usedAt: now}
		}
		delete(cache.flights, key)
		close(pending)
		cache.mu.Unlock()
		return indexes, err
	}
}
