//go:build integration

package auth

import (
	"context"
	"testing"
	"time"
)

// PlatformSession 的 last_used_at 只用于运维观察，不参与鉴权。
// 每次读取都回写会产生无意义的 WAL 与行锁开销，因此同一窗口内重复读取不应新增写操作。
func TestIntegrationPlatformSessionUsageTouchIsThrottled(t *testing.T) {
	h := newHarness(t, nil)
	h.userSession()
	ctx := context.Background()
	var userID string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM public.users`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	readSession := func() {
		h.t.Helper()
		session, err := h.service.PlatformSession(ctx, userID)
		if err != nil {
			t.Fatal(err)
		}
		if string(session) != testPlatformBody {
			t.Fatalf("平台会话材料不一致：%q", string(session))
		}
	}
	touchValues := func() (lastUsed, updated time.Time) {
		h.t.Helper()
		if err := h.pool.QueryRow(ctx, `SELECT last_used_at, updated_at
			FROM public.platform_sessions WHERE user_id = $1`, userID).Scan(&lastUsed, &updated); err != nil {
			t.Fatal(err)
		}
		return lastUsed, updated
	}

	readSession() // 首次读取写入 last_used_at
	firstLastUsed, firstUpdated := touchValues()
	if firstLastUsed.IsZero() {
		t.Fatal("首次读取未记录平台会话使用时间")
	}

	h.clock.Advance(time.Minute)
	readSession()
	secondLastUsed, secondUpdated := touchValues()
	if !secondLastUsed.Equal(firstLastUsed) || !secondUpdated.Equal(firstUpdated) {
		t.Fatalf("窗口内重复读取回写了使用时间：%s → %s", firstLastUsed, secondLastUsed)
	}

	h.clock.Advance(platformSessionTouchInterval)
	readSession()
	thirdLastUsed, thirdUpdated := touchValues()
	if !thirdLastUsed.After(secondLastUsed) || !thirdUpdated.After(secondUpdated) {
		t.Fatalf("超过窗口后未更新使用时间：%s → %s", secondLastUsed, thirdLastUsed)
	}
}
