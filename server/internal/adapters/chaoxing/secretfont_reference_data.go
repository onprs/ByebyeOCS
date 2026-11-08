package chaoxing

import (
	_ "embed"
	"sync"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/sync/singleflight"
)

// 参考数据由原版字体自动生成，不含课程内容或人工识字标签。
// 来源、校验值与重建方式见 docs/development/secretfont-index.md。
//
//go:embed secretfont_reference.bin.gz
var secretReferenceData []byte

var secretReferenceOnce sync.Once
var secretReferenceTable *secretFontReference

func secretReference() *secretFontReference {
	secretReferenceOnce.Do(func() {
		secretReferenceTable, _ = parseSecretFontReference(secretReferenceData)
	})
	return secretReferenceTable
}

const maxSecretReferenceCache = 4096

type secretReferenceCache struct {
	mu     sync.Mutex
	values map[string]secretReferenceMatch
	keys   []string
	next   int
	group  singleflight.Group
}

func (t *secretFontReference) matchCached(font *sfnt.Font, gid uint16, signature string) secretReferenceMatch {
	if font == nil {
		return secretReferenceMatch{}
	}
	cache := &t.cache
	cache.mu.Lock()
	cached, ok := cache.values[signature]
	cache.mu.Unlock()
	if ok {
		return cached
	}
	value, _, _ := cache.group.Do(signature, func() (any, error) {
		cache.mu.Lock()
		cached, ok := cache.values[signature]
		cache.mu.Unlock()
		if ok {
			return cached, nil
		}
		matched := t.match(font, gid)
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.values == nil {
			cache.values = make(map[string]secretReferenceMatch)
		}
		if len(cache.keys) < maxSecretReferenceCache {
			cache.keys = append(cache.keys, signature)
		} else {
			delete(cache.values, cache.keys[cache.next])
			cache.keys[cache.next] = signature
			cache.next = (cache.next + 1) % maxSecretReferenceCache
		}
		cache.values[signature] = matched
		return matched, nil
	})
	return value.(secretReferenceMatch)
}
