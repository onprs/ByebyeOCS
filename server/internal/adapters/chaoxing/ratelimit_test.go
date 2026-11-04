package chaoxing

import (
	"context"
	"testing"
	"time"
)

// 自适应节奏：初始等于配置值，连续成功后逼近下限，被限流后按倍数退让。
func TestRateLimiterAdaptsWithinBounds(t *testing.T) {
	limiter := &rateLimiter{}
	configured := 200 * time.Millisecond
	floor, ceiling := intervalBounds(configured, 0)
	if floor != 100*time.Millisecond || ceiling != 800*time.Millisecond {
		t.Fatalf("边界不符：%s/%s", floor, ceiling)
	}
	if got := limiter.currentInterval(configured, 0); got != configured {
		t.Fatalf("初始间隔应等于配置值：%s", got)
	}
	for index := 0; index < 50; index++ {
		limiter.onSuccess(configured, 0)
	}
	accelerated := limiter.currentInterval(configured, 0)
	if accelerated >= configured || accelerated < floor {
		t.Fatalf("连续成功后应加速且不低于下限：%s", accelerated)
	}
	for index := 0; index < 2000; index++ {
		limiter.onSuccess(configured, 0)
	}
	if slowest := limiter.currentInterval(configured, 0); slowest != floor {
		t.Fatalf("持续成功应停在下限：%s", slowest)
	}
	limiter.onThrottle(configured, 0)
	retreated := limiter.currentInterval(configured, 0)
	if retreated <= accelerated {
		t.Fatalf("限流后应退让：%s", retreated)
	}
	for index := 0; index < 20; index++ {
		limiter.onThrottle(configured, 0)
	}
	if capped := limiter.currentInterval(configured, 0); capped != ceiling {
		t.Fatalf("退让应停在上限：%s", capped)
	}
}

// 配置变化后重置为新的配置值，避免沿用旧的自适应结果。
func TestRateLimiterResetsOnConfigChange(t *testing.T) {
	limiter := &rateLimiter{}
	limiter.currentInterval(300*time.Millisecond, 0)
	for index := 0; index < 50; index++ {
		limiter.onSuccess(300*time.Millisecond, 0)
	}
	if got := limiter.currentInterval(150*time.Millisecond, 0); got != 150*time.Millisecond {
		t.Fatalf("配置变化未重置：%s", got)
	}
}

// 等待保持顺序与可取消。
func TestRateLimiterWaitHonorsContext(t *testing.T) {
	limiter := &rateLimiter{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := limiter.wait(ctx, time.Second, 0); err == nil {
		t.Fatal("取消的上下文应返回错误")
	}
	start := time.Now()
	if err := limiter.wait(context.Background(), 20*time.Millisecond, 0); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("首次等待不应阻塞过久")
	}
}

// 显式下限允许降到更低，但不得低于 20 毫秒；未配置时保持历史下限。
func TestRateLimiterExplicitFloor(t *testing.T) {
	limiter := &rateLimiter{}
	configured := 150 * time.Millisecond
	for index := 0; index < 5000; index++ {
		limiter.onSuccess(configured, 30*time.Millisecond)
	}
	if got := limiter.currentInterval(configured, 30*time.Millisecond); got != 30*time.Millisecond {
		t.Fatalf("显式下限未生效：%s", got)
	}
	for index := 0; index < 5000; index++ {
		limiter.onSuccess(configured, 0)
	}
	if got := limiter.currentInterval(configured, 0); got != 75*time.Millisecond {
		t.Fatalf("未配置下限时应为间隔一半：%s", got)
	}
	floor, _ := intervalBounds(150*time.Millisecond, 5*time.Millisecond)
	if floor != 10*time.Millisecond {
		t.Fatalf("下限不能低于 10 毫秒：%s", floor)
	}
}
