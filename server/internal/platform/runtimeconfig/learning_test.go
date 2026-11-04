package runtimeconfig

import (
	"testing"
	"time"
)

func TestLearningSettingsUpdateAtomically(t *testing.T) {
	settings := NewLearning(4, 3, 8, 150*time.Millisecond, 60*time.Millisecond)
	if settings.GlobalConcurrency() != 4 || settings.AccountConcurrency() != 3 || settings.ChapterConcurrency() != 8 || settings.RequestInterval() != 150*time.Millisecond || settings.RequestIntervalFloor() != 60*time.Millisecond {
		t.Fatal("初始运行参数错误")
	}
	settings.Set(8, 5, 12, 100*time.Millisecond, 40*time.Millisecond)
	if settings.GlobalConcurrency() != 8 || settings.AccountConcurrency() != 5 || settings.ChapterConcurrency() != 12 || settings.RequestInterval() != 100*time.Millisecond || settings.RequestIntervalFloor() != 40*time.Millisecond {
		t.Fatal("运行参数未实时更新")
	}
}
