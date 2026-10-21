package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// challengeState 是登录挑战的服务端事实，不含绑定明文。
type challengeState struct {
	ID            string
	Method        string
	ClientChannel string
	BindingHash   string
	Status        string
	Attempts      int
	MaxAttempts   int
	ExpiresAt     time.Time
	CreatedAt     time.Time
}

// loginOutcome 是一次成功登录的结果；令牌明文只在本次响应中返回。
type loginOutcome struct {
	UserID      string
	IdentityID  string
	DisplayName string
	School      string
	VerifiedAt  time.Time
	Channel     string
	Issued      issuedCredential
}

func newBindingToken() (string, string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", fault.New("internal_error", "无法生成登录绑定值")
	}
	binding := base64.RawURLEncoding.EncodeToString(raw[:])
	return binding, tokenDigest(binding), nil
}

// createChallenge 建立一次性登录挑战；当前只支持密码方式，扫码未实现。
// 返回值 retryAfter>0 表示触发持久化限流，调用方应设置 Retry-After。
func (s *Service) createChallenge(ctx context.Context, method, channel, clientKey string, now time.Time) (challengeState, string, time.Duration, error) {
	if s.options.Verifier == nil {
		// 没有外部验证能力时不写入任何挑战记录。
		return challengeState{}, "", 0, fault.New("external_disabled", "外部身份验证尚未配置")
	}
	if method != "password" {
		return challengeState{}, "", 0, fault.New("validation_failed", "当前只支持密码登录方式")
	}
	if channel != ChannelWeb && channel != ChannelApp {
		return challengeState{}, "", 0, fault.New("validation_failed", "客户端通道无效")
	}
	binding, bindingHash, err := newBindingToken()
	if err != nil {
		return challengeState{}, "", 0, err
	}
	state := challengeState{
		ID:            web.NewID(),
		Method:        method,
		ClientChannel: channel,
		BindingHash:   bindingHash,
		Status:        "pending",
		Attempts:      0,
		MaxAttempts:   s.options.ChallengeMaxAttempts,
		ExpiresAt:     now.Add(s.options.ChallengeTTL),
	}
	if !validUUID(state.ID) {
		return challengeState{}, "", 0, fault.New("internal_error", "内部编号生成失败")
	}
	// 客户端标识只以摘要形式保存，不保存明文 IP 或转发头。
	clientHash := tokenDigest("challenge-client\x00" + clientKey)
	limits := s.options.ChallengeRateLimit
	var retryAfter time.Duration
	limited := false
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// 单次有界清理过期挑战，避免无限增长；不删除仍可能被轮询的记录。
		if _, err := tx.Exec(ctx, `DELETE FROM public.login_challenges
			WHERE id IN (SELECT id FROM public.login_challenges WHERE expires_at < $1 LIMIT 500)`,
			now.Add(-limits.Retention)); err != nil {
			return fault.New("service_unavailable", "无法清理过期登录挑战")
		}
		var count int
		var oldest time.Time
		if err := tx.QueryRow(ctx, `SELECT count(*), coalesce(min(created_at), $2::timestamptz)
			FROM public.login_challenges
			WHERE client_key_hash = $1 AND created_at > $2`, clientHash, now.Add(-limits.Window)).
			Scan(&count, &oldest); err != nil {
			return fault.New("service_unavailable", "无法检查登录挑战限流")
		}
		if count >= limits.MaxPerClient {
			retryAfter = oldest.Add(limits.Window).Sub(now)
			if retryAfter < time.Second {
				retryAfter = time.Second
			}
			limited = true
			return nil
		}
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.login_challenges
			WHERE status = 'pending' AND expires_at > $1`, now).Scan(&pending); err != nil {
			return fault.New("service_unavailable", "无法检查未完成登录挑战")
		}
		if pending >= limits.MaxPending {
			retryAfter = time.Minute
			limited = true
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.login_challenges
			(id, method, client_channel, binding_hash, client_key_hash, status, attempts, max_attempts, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, 'pending', 0, $6, $7, $8)`,
			state.ID, method, channel, bindingHash, clientHash, state.MaxAttempts, now, state.ExpiresAt); err != nil {
			return fault.New("service_unavailable", "无法建立登录挑战，请稍后重试")
		}
		return nil
	})
	if err != nil {
		return challengeState{}, "", 0, err
	}
	if limited {
		return challengeState{}, "", retryAfter, fault.New("rate_limited", "登录请求过于频繁，请稍后再试")
	}
	return state, binding, 0, nil
}

// loadChallenge 读取挑战并校验客户端绑定；不匹配一律按不存在处理。
func (s *Service) loadChallenge(ctx context.Context, id, binding string, now time.Time) (challengeState, error) {
	if !validUUID(id) || binding == "" {
		return challengeState{}, fault.New("not_found", "登录挑战不存在或已失效")
	}
	state := challengeState{ID: id}
	var bindingHash string
	err := s.pool.QueryRow(ctx, `SELECT method, client_channel, binding_hash, status, attempts, max_attempts, created_at, expires_at
		FROM public.login_challenges WHERE id = $1`, id).
		Scan(&state.Method, &state.ClientChannel, &bindingHash, &state.Status, &state.Attempts,
			&state.MaxAttempts, &state.CreatedAt, &state.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return challengeState{}, fault.New("not_found", "登录挑战不存在或已失效")
	}
	if err != nil {
		return challengeState{}, fault.New("service_unavailable", "无法读取登录挑战，请稍后重试")
	}
	if !constantTimeEqual(tokenDigest(binding), bindingHash) {
		return challengeState{}, fault.New("not_found", "登录挑战不存在或已失效")
	}
	if !now.Before(state.ExpiresAt) {
		_, _ = s.pool.Exec(ctx, `UPDATE public.login_challenges SET status = 'expired'
			WHERE id = $1 AND status = 'pending'`, id)
		state.Status = "expired"
	}
	state.BindingHash = bindingHash
	return state, nil
}

// reserveChallengeAttempt 在事务中占用一次尝试额度，防止并行猜测。
// 过期或失败标记需要提交，因此先提交状态变更，再返回业务错误。
func (s *Service) reserveChallengeAttempt(ctx context.Context, id, binding string, now time.Time) (challengeState, error) {
	if !validUUID(id) || binding == "" {
		return challengeState{}, fault.New("not_found", "登录挑战不存在或已失效")
	}
	state := challengeState{ID: id}
	var outcome error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var bindingHash string
		err := tx.QueryRow(ctx, `SELECT method, client_channel, binding_hash, status, attempts, max_attempts, created_at, expires_at
			FROM public.login_challenges WHERE id = $1 FOR UPDATE`, id).
			Scan(&state.Method, &state.ClientChannel, &bindingHash, &state.Status, &state.Attempts,
				&state.MaxAttempts, &state.CreatedAt, &state.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.New("not_found", "登录挑战不存在或已失效")
		}
		if err != nil {
			return fault.New("service_unavailable", "无法读取登录挑战，请稍后重试")
		}
		if !constantTimeEqual(tokenDigest(binding), bindingHash) {
			return fault.New("not_found", "登录挑战不存在或已失效")
		}
		if !now.Before(state.ExpiresAt) {
			if _, err := tx.Exec(ctx, `UPDATE public.login_challenges SET status = 'expired'
				WHERE id = $1 AND status = 'pending'`, id); err != nil {
				return fault.New("service_unavailable", "无法更新登录挑战")
			}
			outcome = fault.New("not_found", "登录挑战已过期，请重新发起")
			return nil
		}
		if state.Status != "pending" {
			outcome = fault.New("conflict", "登录挑战已使用或已失效，请重新发起")
			return nil
		}
		if state.Attempts >= state.MaxAttempts {
			if _, err := tx.Exec(ctx, `UPDATE public.login_challenges SET status = 'failed'
				WHERE id = $1 AND status = 'pending'`, id); err != nil {
				return fault.New("service_unavailable", "无法更新登录挑战")
			}
			outcome = fault.New("conflict", "登录挑战尝试次数已用尽，请重新发起")
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE public.login_challenges SET attempts = attempts + 1
			WHERE id = $1`, id); err != nil {
			return fault.New("service_unavailable", "无法记录登录尝试")
		}
		state.Attempts++
		return nil
	})
	if err != nil {
		return challengeState{}, err
	}
	if outcome != nil {
		return challengeState{}, outcome
	}
	return state, nil
}

// failChallenge 在凭据被拒绝且额度用尽时把挑战标记为失败。
func (s *Service) failChallenge(ctx context.Context, id string, attempts, maxAttempts int) {
	if attempts < maxAttempts {
		return
	}
	_, _ = s.pool.Exec(ctx, `UPDATE public.login_challenges SET status = 'failed'
		WHERE id = $1 AND status = 'pending'`, id)
}

// completeLogin 在事务中收敛身份归属、保存平台会话并签发客户端会话。
// 并发首次登录通过唯一约束收敛；竞争失败时整批回滚后重试。
func (s *Service) completeLogin(ctx context.Context, attempt challengeState, subject VerifiedSubject, now time.Time) (loginOutcome, error) {
	var outcome loginOutcome
	for range 4 {
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			var status string
			var attempts, maxAttempts int
			var expires time.Time
			err := tx.QueryRow(ctx, `SELECT status, attempts, max_attempts, expires_at
				FROM public.login_challenges WHERE id = $1 FOR UPDATE`, attempt.ID).
				Scan(&status, &attempts, &maxAttempts, &expires)
			if errors.Is(err, pgx.ErrNoRows) {
				return fault.New("not_found", "登录挑战不存在或已失效")
			}
			if err != nil {
				return fault.New("service_unavailable", "无法读取登录挑战")
			}
			if !now.Before(expires) {
				if _, err := tx.Exec(ctx, `UPDATE public.login_challenges SET status = 'expired'
					WHERE id = $1 AND status = 'pending'`, attempt.ID); err != nil {
					return fault.New("service_unavailable", "无法更新登录挑战")
				}
				return fault.New("not_found", "登录挑战已过期，请重新发起")
			}
			if status != "pending" {
				return fault.New("conflict", "登录挑战已使用或已失效，请重新发起")
			}
			binding, err := s.bindIdentity(ctx, tx, subject, now)
			if err != nil {
				return err
			}
			platformSessionID, err := s.savePlatformSession(ctx, tx, binding.UserID, binding.IdentityID, subject, now)
			if err != nil {
				return err
			}
			// 同一事务内通知其他模块绑定内部身份；失败则整批回滚，不留下半个账号。
			if err := s.notifyIdentityBound(ctx, tx, binding.UserID, subject); err != nil {
				return err
			}
			issued, err := s.createClientSession(ctx, tx, binding.UserID, platformSessionID, attempt.ClientChannel, now)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE public.login_challenges
				SET status = 'consumed', consumed_at = $2, verified_at = $2, user_id = $3, external_identity_id = $4
				WHERE id = $1 AND status = 'pending'`,
				attempt.ID, now, binding.UserID, binding.IdentityID); err != nil {
				return fault.New("service_unavailable", "无法完成登录挑战")
			}
			event := audit.Event{
				Actor:        web.Actor{ID: binding.UserID, Kind: KindUser, Permissions: permissionsFor(KindUser, "")},
				Action:       "user.login",
				ResourceType: "external_identity",
				ResourceID:   binding.IdentityID,
				RequestID:    requestID(ctx),
			}
			if err := audit.Record(ctx, tx, event); err != nil {
				return fault.New("service_unavailable", "无法记录登录审计")
			}
			outcome = loginOutcome{
				UserID:      binding.UserID,
				IdentityID:  binding.IdentityID,
				DisplayName: binding.DisplayName,
				School:      binding.School,
				VerifiedAt:  now,
				Channel:     attempt.ClientChannel,
				Issued:      issued,
			}
			return nil
		})
		if errors.Is(err, errIdentityRace) {
			continue
		}
		if err != nil {
			return loginOutcome{}, err
		}
		return outcome, nil
	}
	return loginOutcome{}, fault.New("service_unavailable", "身份关联并发冲突，请稍后重试")
}

// notifyIdentityBound 调用可选的身份绑定钩子；回调错误会回滚登录事务。
func (s *Service) notifyIdentityBound(ctx context.Context, tx pgx.Tx, userID string, subject VerifiedSubject) error {
	if s.options.OnIdentityBound == nil {
		return nil
	}
	if err := s.options.OnIdentityBound(ctx, tx, userID, subject); err != nil {
		// 已带安全错误码的回调错误直接传递；未知错误不泄露内部细节。
		if code, _ := fault.Describe(err); code != "internal_error" {
			return err
		}
		return fault.New("service_unavailable", "账号绑定未完成，请稍后重试")
	}
	return nil
}
