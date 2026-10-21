package auth

import (
	"testing"
	"time"
)

func TestRateLimitFailureWindow(t *testing.T) {
	options := RateLimitOptions{MaxFailures: 3, Window: 10 * time.Minute, BlockFor: 5 * time.Minute}
	start := time.Unix(1700000000, 0).UTC()
	state := rateLimitState{}

	state = rateLimitFailure(state, start, options)
	if state.Failures != 1 || !state.BlockedUntil.IsZero() {
		t.Fatalf("首次失败状态异常：%+v", state)
	}
	if blocked, _ := rateLimitBlocked(state, start); blocked {
		t.Fatal("首次失败即被封禁")
	}
	state = rateLimitFailure(state, start.Add(time.Minute), options)
	state = rateLimitFailure(state, start.Add(2*time.Minute), options)
	if state.Failures != 3 || !state.BlockedUntil.Equal(start.Add(2*time.Minute).Add(5*time.Minute)) {
		t.Fatalf("达到上限后未进入封禁：%+v", state)
	}
	blocked, retryAfter := rateLimitBlocked(state, start.Add(3*time.Minute))
	if !blocked || retryAfter != 4*time.Minute {
		t.Fatalf("封禁剩余时长错误：%v %v", blocked, retryAfter)
	}
	// 封禁期内继续尝试不应延长封禁或重置计数。
	state = rateLimitFailure(state, start.Add(4*time.Minute), options)
	if !state.BlockedUntil.Equal(start.Add(2 * time.Minute).Add(5 * time.Minute)) {
		t.Fatal("封禁期内失败延长了封禁时间")
	}
}

func TestRateLimitWindowReset(t *testing.T) {
	options := RateLimitOptions{MaxFailures: 2, Window: 5 * time.Minute, BlockFor: 5 * time.Minute}
	start := time.Unix(1700000000, 0).UTC()
	state := rateLimitFailure(rateLimitState{}, start, options)
	// 窗口过期后重新计数，允许一次失败而不封禁。
	state = rateLimitFailure(state, start.Add(6*time.Minute), options)
	if state.Failures != 1 {
		t.Fatalf("窗口过期后未重置失败计数：%d", state.Failures)
	}
	if blocked, _ := rateLimitBlocked(state, start.Add(6*time.Minute)); blocked {
		t.Fatal("窗口重置后仍处于封禁")
	}
}

func TestRateLimitSubjectIsolation(t *testing.T) {
	first := rateLimitSubject("admin_login", "Owner")
	second := rateLimitSubject("admin_login", "owner")
	if first != second {
		t.Fatal("账号大小写不同导致限流分桶，可能被绕过")
	}
	if first == rateLimitSubject("user_login", "owner") {
		t.Fatal("不同范围使用了相同限流键")
	}
	if len(first) != 64 {
		t.Fatalf("限流键长度异常：%d", len(first))
	}
}
