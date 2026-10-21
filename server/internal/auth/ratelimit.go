package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 限流范围；与 auth_rate_limits.scope 约束一致。
const (
	scopeAdminLogin = "admin_login"
	scopeUserLogin  = "user_login"
)

type rateLimitState struct {
	WindowStartedAt time.Time
	Failures        int
	BlockedUntil    time.Time
}

// rateLimitBlocked 判断当前是否处于封禁期并返回剩余时长。
func rateLimitBlocked(state rateLimitState, now time.Time) (bool, time.Duration) {
	if state.BlockedUntil.After(now) {
		return true, state.BlockedUntil.Sub(now)
	}
	return false, 0
}

// rateLimitFailure 记录一次失败；窗口过期后重新计数，达到上限则进入封禁。
func rateLimitFailure(state rateLimitState, now time.Time, options RateLimitOptions) rateLimitState {
	if state.BlockedUntil.After(now) {
		// 封禁期内不累计、不延长封禁，避免攻击者持续刷失败导致永久锁定。
		return state
	}
	next := state
	if next.WindowStartedAt.IsZero() || !now.Before(next.WindowStartedAt.Add(options.Window)) {
		next.WindowStartedAt = now
		next.Failures = 0
	}
	next.Failures++
	if next.Failures >= options.MaxFailures {
		next.BlockedUntil = now.Add(options.BlockFor)
	}
	return next
}

func rateLimitSubject(scope, identifier string) string {
	return tokenDigest(scope + "\x00" + strings.ToLower(strings.TrimSpace(identifier)))
}

// rateLimitStatus 读取持久化限流状态；失败时按服务不可用处理，不放行。
func (s *Service) rateLimitStatus(ctx context.Context, scope, identifier string, now time.Time) (bool, time.Duration, error) {
	hash := rateLimitSubject(scope, identifier)
	var state rateLimitState
	var blockedUntil *time.Time
	err := s.pool.QueryRow(ctx, `SELECT window_started_at, failures, blocked_until
		FROM public.auth_rate_limits WHERE scope = $1 AND subject_hash = $2`, scope, hash).
		Scan(&state.WindowStartedAt, &state.Failures, &blockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, fault.New("service_unavailable", "无法读取登录限流状态，请稍后重试")
	}
	if blockedUntil != nil {
		state.BlockedUntil = *blockedUntil
	}
	blocked, retryAfter := rateLimitBlocked(state, now)
	return blocked, retryAfter, nil
}

// rateLimitFailure 在事务中累计失败次数并返回最新状态。
func (s *Service) addRateLimitFailure(ctx context.Context, scope, identifier string, now time.Time) (time.Duration, error) {
	hash := rateLimitSubject(scope, identifier)
	var retryAfter time.Duration
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO public.auth_rate_limits
			(id, scope, subject_hash, window_started_at, failures, blocked_until, updated_at)
			VALUES ($1, $2, $3, $4, 0, NULL, $4)
			ON CONFLICT (scope, subject_hash) DO NOTHING`, web.NewID(), scope, hash, now); err != nil {
			return fault.New("service_unavailable", "无法记录登录失败")
		}
		var state rateLimitState
		var blockedUntil *time.Time
		if err := tx.QueryRow(ctx, `SELECT window_started_at, failures, blocked_until
			FROM public.auth_rate_limits WHERE scope = $1 AND subject_hash = $2
			FOR UPDATE`, scope, hash).Scan(&state.WindowStartedAt, &state.Failures, &blockedUntil); err != nil {
			return fault.New("service_unavailable", "无法读取登录失败记录")
		}
		if blockedUntil != nil {
			state.BlockedUntil = *blockedUntil
		}
		next := rateLimitFailure(state, now, s.options.RateLimit)
		var nextBlocked *time.Time
		if next.BlockedUntil.After(now) {
			nextBlocked = &next.BlockedUntil
		}
		if _, err := tx.Exec(ctx, `UPDATE public.auth_rate_limits
			SET window_started_at = $3, failures = $4, blocked_until = $5, updated_at = $2
			WHERE scope = $1 AND subject_hash = $6`,
			scope, now, next.WindowStartedAt, next.Failures, nextBlocked, hash); err != nil {
			return fault.New("service_unavailable", "无法更新登录失败记录")
		}
		if blocked, remaining := rateLimitBlocked(next, now); blocked {
			retryAfter = remaining
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return retryAfter, nil
}

// resetRateLimit 在成功登录后清除失败计数。
func (s *Service) resetRateLimit(ctx context.Context, scope, identifier string) {
	hash := rateLimitSubject(scope, identifier)
	_, _ = s.pool.Exec(ctx, `DELETE FROM public.auth_rate_limits WHERE scope = $1 AND subject_hash = $2`, scope, hash)
}
