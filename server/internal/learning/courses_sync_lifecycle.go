package learning

import (
	"context"
	"time"
)

// courseSyncLeaseContext 在整个目录读取期间续租，避免单课程读取较慢时失去所有权。
// 续租失败立即取消上游读取；停止后等待心跳退出，避免泄漏协程或继续写入旧租约。
func (s *Service) courseSyncLeaseContext(parent context.Context, job courseSyncJob) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(s.opts.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			renewCtx, stopRenew := context.WithTimeout(ctx, s.opts.LeaseTTL/2)
			err := s.assertCourseSyncLease(renewCtx, &job)
			stopRenew()
			if err != nil {
				if ctx.Err() == nil {
					s.opts.Logger.Warn("课程同步续租结束", "code", describe(err))
				}
				cancel()
				return
			}
		}
	}()
	return ctx, func() { cancel(); <-done }
}
