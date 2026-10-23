package events

import (
	"log/slog"
	"sync"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// failureLogInterval 是同一组件同一失败分类的最小记录间隔，避免后台循环刷屏。
const failureLogInterval = 30 * time.Second

// failureLog 按组件与安全分类码节流记录后台失败。
// 只记录 fault 分类码与压缩计数，不记录原始错误、请求内容或身份信息。
type failureLog struct {
	logger   *slog.Logger
	scope    string
	interval time.Duration

	mu         sync.Mutex
	last       map[string]time.Time
	suppressed map[string]int
}

// report 可在 nil 接收者上调用：未装配日志时静默。
func (f *failureLog) report(component string, err error) {
	if f == nil || f.logger == nil || err == nil {
		return
	}
	code, _ := fault.Describe(err)
	key := component + "/" + code
	now := time.Now()
	f.mu.Lock()
	if f.last == nil {
		f.last = map[string]time.Time{}
		f.suppressed = map[string]int{}
	}
	if previous, ok := f.last[key]; ok && now.Sub(previous) < f.interval {
		f.suppressed[key]++
		f.mu.Unlock()
		return
	}
	dropped := f.suppressed[key]
	delete(f.suppressed, key)
	f.last[key] = now
	f.mu.Unlock()
	attributes := []any{"scope", f.scope, "component", component, "code", code}
	if dropped > 0 {
		attributes = append(attributes, "suppressed", dropped)
	}
	f.logger.Warn("后台任务失败", attributes...)
}
