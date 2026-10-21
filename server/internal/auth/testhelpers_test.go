package auth

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// adjustableClock 让测试固定或推进服务端时间，不依赖真实等待。
type adjustableClock struct {
	mutex sync.Mutex
	at    time.Time
}

func newClock(at time.Time) *adjustableClock { return &adjustableClock{at: at.UTC()} }

func (c *adjustableClock) Now() time.Time {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.at
}

func (c *adjustableClock) Advance(delta time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.at = c.at.Add(delta)
}

// fakeSessionReader 替换数据库会话读取，用于中间件规则测试。
type fakeSessionReader struct {
	states  map[string]sessionState
	err     error
	expired []string
	touches int
}

func newFakeSessionReader() *fakeSessionReader {
	return &fakeSessionReader{states: map[string]sessionState{}}
}

func (reader *fakeSessionReader) key(kind, tokenHash string) string { return kind + "\x00" + tokenHash }

func (reader *fakeSessionReader) add(kind, token string, state sessionState) {
	state.Kind = kind
	reader.states[reader.key(kind, tokenDigest(token))] = state
}

func (reader *fakeSessionReader) lookup(_ context.Context, kind, tokenHash string) (sessionState, error) {
	if reader.err != nil {
		return sessionState{}, reader.err
	}
	state, ok := reader.states[reader.key(kind, tokenHash)]
	if !ok {
		return sessionState{}, fault.New("unauthenticated", "登录已失效")
	}
	return state, nil
}

func (reader *fakeSessionReader) touch(context.Context, string, string, time.Time) error {
	reader.touches++
	return nil
}

func (reader *fakeSessionReader) expire(_ context.Context, kind, id string, _ time.Time, reason string) error {
	reader.expired = append(reader.expired, kind+":"+id+":"+reason)
	return nil
}

func testEncryptionKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
	}
	return key
}

func baseTestOptions(now time.Time) Options {
	return Options{
		UserOrigin:           "http://user.test",
		AdminOrigin:          "http://admin.test",
		EncryptionKey:        testEncryptionKey(),
		SessionTTL:           2 * time.Hour,
		SessionIdle:          time.Hour,
		AppAccessTTL:         15 * time.Minute,
		RefreshTTL:           24 * time.Hour,
		ChallengeTTL:         2 * time.Minute,
		ChallengeMaxAttempts: 3,
		FreshWindow:          10 * time.Minute,
		Now:                  func() time.Time { return now },
	}
}

func newTestService(t *testing.T, options Options) (*Service, *fakeSessionReader) {
	t.Helper()
	service, err := newService(nil, options)
	if err != nil {
		t.Fatalf("构造测试认证服务失败：%v", err)
	}
	reader := newFakeSessionReader()
	service.sessions = reader
	return service, reader
}
