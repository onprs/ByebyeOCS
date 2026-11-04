// Package runtimeconfig 保存可在运行中热更新的执行与同步参数。
package runtimeconfig

import (
	"sync/atomic"
	"time"
)

// Learning 是并发与上游请求节奏的唯一可变来源；零值方法返回保守默认。
type Learning struct {
	global   atomic.Int64
	account  atomic.Int64
	chapter  atomic.Int64
	interval atomic.Int64
	floor    atomic.Int64
}

func NewLearning(global, account, chapter int, interval, floor time.Duration) *Learning {
	settings := &Learning{}
	settings.Set(global, account, chapter, interval, floor)
	return settings
}

func (s *Learning) Set(global, account, chapter int, interval, floor time.Duration) {
	if s == nil {
		return
	}
	if global > 0 {
		s.global.Store(int64(global))
	}
	if account > 0 {
		s.account.Store(int64(account))
	}
	if chapter > 0 {
		s.chapter.Store(int64(chapter))
	}
	if interval > 0 {
		s.interval.Store(int64(interval))
	}
	s.floor.Store(int64(floor))
}

func (s *Learning) GlobalConcurrency() int {
	if s == nil {
		return 0
	}
	return int(s.global.Load())
}

func (s *Learning) AccountConcurrency() int {
	if s == nil {
		return 0
	}
	return int(s.account.Load())
}

func (s *Learning) ChapterConcurrency() int {
	if s == nil {
		return 0
	}
	return int(s.chapter.Load())
}

func (s *Learning) RequestInterval() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(s.interval.Load())
}

// RequestIntervalFloor 返回自适应提速下限；0 表示按请求间隔的一半推导。
func (s *Learning) RequestIntervalFloor() time.Duration {
	if s == nil {
		return 0
	}
	return time.Duration(s.floor.Load())
}
