package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// sessionState 是会话校验所需的最小事实集合，不含任何令牌明文。
type sessionState struct {
	ID                string
	Kind              string
	SubjectID         string
	Channel           string
	PlatformSessionID string
	CSRFHash          string
	Status            string
	Role              string
	Disabled          bool
	AuthenticatedAt   time.Time
	StepUpAt          time.Time
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
}

// sessionReader 允许单元测试替换数据库读取；生产实现直接查库。
type sessionReader interface {
	lookup(ctx context.Context, kind, tokenHash string) (sessionState, error)
	touch(ctx context.Context, kind, id string, at time.Time) error
	expire(ctx context.Context, kind, id string, at time.Time, reason string) error
}

type sqlSessionReader struct{ service *Service }

func (r sqlSessionReader) lookup(ctx context.Context, kind, tokenHash string) (sessionState, error) {
	state := sessionState{Kind: kind}
	var err error
	switch kind {
	case KindUser:
		state.Channel = ""
		err = r.service.pool.QueryRow(ctx, `SELECT id::text, user_id::text, coalesce(platform_session_id::text, ''),
				channel, csrf_hash, status, authenticated_at, authenticated_at,
				expires_at, absolute_expires_at, last_seen_at
			FROM public.client_sessions
			WHERE token_hash = $1`, tokenHash).Scan(
			&state.ID, &state.SubjectID, &state.PlatformSessionID, &state.Channel, &state.CSRFHash,
			&state.Status, &state.AuthenticatedAt, &state.StepUpAt, &state.ExpiresAt,
			&state.AbsoluteExpiresAt, &state.LastSeenAt)
	case KindAdmin:
		state.Channel = ChannelWeb
		err = r.service.pool.QueryRow(ctx, `SELECT s.id::text, s.admin_id::text, '', 'web', s.csrf_hash, s.status,
				s.authenticated_at, coalesce(s.step_up_at, s.authenticated_at), s.expires_at, s.absolute_expires_at,
				s.last_seen_at, a.role, a.disabled
			FROM public.admin_sessions s
			JOIN public.admin_users a ON a.id = s.admin_id
			WHERE s.token_hash = $1`, tokenHash).Scan(
			&state.ID, &state.SubjectID, &state.PlatformSessionID, &state.Channel, &state.CSRFHash,
			&state.Status, &state.AuthenticatedAt, &state.StepUpAt, &state.ExpiresAt,
			&state.AbsoluteExpiresAt, &state.LastSeenAt, &state.Role, &state.Disabled)
	default:
		return sessionState{}, fault.New("auth_config_invalid", "认证端类型无效")
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionState{}, fault.New("unauthenticated", "登录已失效")
	}
	if err != nil {
		return sessionState{}, fault.New("service_unavailable", "无法读取会话状态")
	}
	return state, nil
}

func (r sqlSessionReader) touch(ctx context.Context, kind, id string, at time.Time) error {
	query := `UPDATE public.client_sessions SET last_seen_at = $2 WHERE id = $1`
	if kind == KindAdmin {
		query = `UPDATE public.admin_sessions SET last_seen_at = $2 WHERE id = $1`
	}
	_, err := r.service.pool.Exec(ctx, query, id, at)
	return err
}

func (r sqlSessionReader) expire(ctx context.Context, kind, id string, at time.Time, reason string) error {
	query := `UPDATE public.client_sessions SET status = 'expired', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE id = $1 AND status = 'active'`
	if kind == KindAdmin {
		query = `UPDATE public.admin_sessions SET status = 'expired', revoked_at = $2, revoke_reason = $3, updated_at = $2
			WHERE id = $1 AND status = 'active'`
	}
	_, err := r.service.pool.Exec(ctx, query, id, at, reason)
	return err
}

// issuedCredential 是一次签发的凭据材料；明文只返回给调用方一次。
type issuedCredential struct {
	Token             string
	CSRF              string
	RefreshToken      string
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	SessionID         string
}

// createClientSession 建立 Web 或 App 会话，只写入令牌摘要。
// 登录签发前先按主体行锁执行会话配额（超过上限时撤销最旧会话及刷新链）。
func (s *Service) createClientSession(ctx context.Context, tx pgx.Tx, userID, platformSessionID, channel string, now time.Time) (issuedCredential, error) {
	if err := s.enforceUserSessionQuota(ctx, tx, userID, now); err != nil {
		return issuedCredential{}, err
	}
	sessionID := web.NewID()
	if !validUUID(sessionID) {
		return issuedCredential{}, fault.New("internal_error", "内部编号生成失败")
	}
	refreshToken, refreshDigest, err := issueToken(tokenPrefixRefresh)
	if err != nil {
		return issuedCredential{}, err
	}
	token, digest, err := issueToken(tokenPrefix(KindUser, channel))
	if err != nil {
		return issuedCredential{}, err
	}
	csrf := s.csrfToken(sessionID)
	absolute := now.Add(s.options.SessionTTL)
	if channel == ChannelApp {
		absolute = now.Add(s.options.RefreshTTL)
	}
	expires := absolute
	if channel == ChannelApp {
		expires = now.Add(s.options.AppAccessTTL)
	}
	var platform *string
	if platformSessionID != "" {
		platform = &platformSessionID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.client_sessions
		(id, user_id, platform_session_id, channel, token_hash, csrf_hash, status, authenticated_at,
		 created_at, updated_at, last_seen_at, expires_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, 'active', $7, $7, $7, $7, $8, $9)`,
		sessionID, userID, platform, channel, digest, s.csrfDigest(sessionID), now, expires, absolute); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法建立会话，请稍后重试")
	}
	if channel == ChannelApp {
		refreshExpiry := now.Add(s.options.RefreshTTL)
		if _, err := tx.Exec(ctx, `INSERT INTO public.refresh_tokens
			(id, session_id, family_id, token_hash, generation, created_at, expires_at)
			VALUES ($1, $2, $3, $4, 1, $5, $6)`,
			web.NewID(), sessionID, sessionID, refreshDigest, now, refreshExpiry); err != nil {
			return issuedCredential{}, fault.New("service_unavailable", "无法建立刷新链，请稍后重试")
		}
		return issuedCredential{Token: token, RefreshToken: refreshToken, ExpiresAt: expires,
			AbsoluteExpiresAt: absolute, SessionID: sessionID}, nil
	}
	return issuedCredential{Token: token, CSRF: csrf, ExpiresAt: expires,
		AbsoluteExpiresAt: absolute, SessionID: sessionID}, nil
}

// createAdminSession 建立独立管理员会话；登录含第二因素，视为新鲜验证。
// 与用户端一致，签发前按管理员行锁执行会话配额。
func (s *Service) createAdminSession(ctx context.Context, tx pgx.Tx, adminID string, now time.Time) (issuedCredential, error) {
	if err := s.enforceAdminSessionQuota(ctx, tx, adminID, now); err != nil {
		return issuedCredential{}, err
	}
	sessionID := web.NewID()
	if !validUUID(sessionID) {
		return issuedCredential{}, fault.New("internal_error", "内部编号生成失败")
	}
	token, digest, err := issueToken(tokenPrefixAdminWeb)
	if err != nil {
		return issuedCredential{}, err
	}
	absolute := now.Add(s.options.SessionTTL)
	if _, err := tx.Exec(ctx, `INSERT INTO public.admin_sessions
		(id, admin_id, token_hash, csrf_hash, status, authenticated_at, step_up_at,
		 created_at, updated_at, last_seen_at, expires_at, absolute_expires_at)
		VALUES ($1, $2, $3, $4, 'active', $5, $5, $5, $5, $5, $6, $6)`,
		sessionID, adminID, digest, s.csrfDigest(sessionID), now, absolute); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法建立管理员会话，请稍后重试")
	}
	return issuedCredential{Token: token, CSRF: s.csrfToken(sessionID), ExpiresAt: absolute,
		AbsoluteExpiresAt: absolute, SessionID: sessionID}, nil
}

// enforceUserSessionQuota 在登录事务内锁定用户行，超限时撤销最旧的在用会话。
// 会话配额是硬容量上限，不能只靠列表截断。App 会话被淘汰时同时吊销刷新链。
func (s *Service) enforceUserSessionQuota(ctx context.Context, tx pgx.Tx, userID string, now time.Time) error {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM public.users WHERE id = $1 FOR UPDATE`, userID).Scan(&locked); err != nil {
		return fault.New("service_unavailable", "无法锁定用户会话配额")
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.client_sessions
		WHERE user_id = $1 AND status = 'active' AND expires_at > $2`, userID, now).Scan(&count); err != nil {
		return fault.New("service_unavailable", "无法统计用户会话")
	}
	over := count - s.options.MaxSessionsPerSubject + 1
	if over <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text, channel FROM public.client_sessions
		WHERE user_id = $1 AND status = 'active' AND expires_at > $2
		ORDER BY last_seen_at ASC, created_at ASC, id
		LIMIT $3`, userID, now, over)
	if err != nil {
		return fault.New("service_unavailable", "无法读取待淘汰会话")
	}
	type victim struct{ id, channel string }
	victims := make([]victim, 0, over)
	for rows.Next() {
		var item victim
		if err := rows.Scan(&item.id, &item.channel); err != nil {
			rows.Close()
			return fault.New("service_unavailable", "无法读取待淘汰会话")
		}
		victims = append(victims, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fault.New("service_unavailable", "无法读取待淘汰会话")
	}
	rows.Close()
	for _, item := range victims {
		if item.channel == ChannelApp {
			if err := s.revokeRefreshFamily(ctx, tx, item.id, "quota_evicted", now); err != nil {
				return err
			}
		}
		if err := s.revokeClientSession(ctx, tx, item.id, "quota_evicted", now); err != nil {
			return err
		}
	}
	return nil
}

// enforceAdminSessionQuota 在登录事务内锁定管理员行，超限时撤销最旧的管理员会话。
func (s *Service) enforceAdminSessionQuota(ctx context.Context, tx pgx.Tx, adminID string, now time.Time) error {
	var locked string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM public.admin_users WHERE id = $1 FOR UPDATE`, adminID).Scan(&locked); err != nil {
		return fault.New("service_unavailable", "无法锁定管理员会话配额")
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM public.admin_sessions
		WHERE admin_id = $1 AND status = 'active' AND expires_at > $2`, adminID, now).Scan(&count); err != nil {
		return fault.New("service_unavailable", "无法统计管理员会话")
	}
	over := count - s.options.MaxSessionsPerSubject + 1
	if over <= 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `SELECT id::text FROM public.admin_sessions
		WHERE admin_id = $1 AND status = 'active' AND expires_at > $2
		ORDER BY last_seen_at ASC, created_at ASC, id
		LIMIT $3`, adminID, now, over)
	if err != nil {
		return fault.New("service_unavailable", "无法读取待淘汰会话")
	}
	victims := make([]string, 0, over)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fault.New("service_unavailable", "无法读取待淘汰会话")
		}
		victims = append(victims, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fault.New("service_unavailable", "无法读取待淘汰会话")
	}
	rows.Close()
	for _, id := range victims {
		if err := s.revokeAdminSession(ctx, tx, id, "quota_evicted", now); err != nil {
			return err
		}
	}
	return nil
}

// rotateClientSession 轮换 Web 会话：新记录继承绝对到期时间，旧记录标记为已轮换。
func (s *Service) rotateClientSession(ctx context.Context, tx pgx.Tx, previous sessionState, now time.Time) (issuedCredential, error) {
	if previous.Channel != ChannelWeb {
		return issuedCredential{}, fault.New("forbidden", "该会话不支持 Web 轮换")
	}
	if !previous.AbsoluteExpiresAt.After(now) {
		return issuedCredential{}, fault.New("unauthenticated", "登录已过期，请重新验证身份")
	}
	newID := web.NewID()
	if !validUUID(newID) {
		return issuedCredential{}, fault.New("internal_error", "内部编号生成失败")
	}
	token, digest, err := issueToken(tokenPrefixUserWeb)
	if err != nil {
		return issuedCredential{}, err
	}
	csrf := s.csrfToken(newID)
	var platform *string
	if previous.PlatformSessionID != "" {
		platform = &previous.PlatformSessionID
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.client_sessions
		(id, user_id, platform_session_id, channel, token_hash, csrf_hash, status, authenticated_at,
		 created_at, updated_at, last_seen_at, expires_at, absolute_expires_at, rotated_from)
		VALUES ($1, $2, $3, 'web', $4, $5, 'active', $6, $7, $7, $7, $8, $8, $9)`,
		newID, previous.SubjectID, platform, digest, s.csrfDigest(newID), previous.AuthenticatedAt,
		now, previous.AbsoluteExpiresAt, previous.ID); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法轮换会话，请稍后重试")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.client_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = 'rotated', updated_at = $2
		WHERE id = $1 AND status = 'active'`, previous.ID, now); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法撤销旧会话，请稍后重试")
	}
	return issuedCredential{Token: token, CSRF: csrf, ExpiresAt: previous.AbsoluteExpiresAt,
		AbsoluteExpiresAt: previous.AbsoluteExpiresAt, SessionID: newID}, nil
}

// rotateAdminSession 轮换管理员 Web 会话，保持原有新鲜验证时间。
func (s *Service) rotateAdminSession(ctx context.Context, tx pgx.Tx, previous sessionState, now time.Time) (issuedCredential, error) {
	if !previous.AbsoluteExpiresAt.After(now) {
		return issuedCredential{}, fault.New("unauthenticated", "登录已过期，请重新验证身份")
	}
	newID := web.NewID()
	if !validUUID(newID) {
		return issuedCredential{}, fault.New("internal_error", "内部编号生成失败")
	}
	token, digest, err := issueToken(tokenPrefixAdminWeb)
	if err != nil {
		return issuedCredential{}, err
	}
	csrf := s.csrfToken(newID)
	if _, err := tx.Exec(ctx, `INSERT INTO public.admin_sessions
		(id, admin_id, token_hash, csrf_hash, status, authenticated_at, step_up_at,
		 created_at, updated_at, last_seen_at, expires_at, absolute_expires_at, rotated_from)
		VALUES ($1, $2, $3, $4, 'active', $5, $6, $7, $7, $7, $8, $8, $9)`,
		newID, previous.SubjectID, digest, s.csrfDigest(newID), previous.AuthenticatedAt,
		previous.StepUpAt, now, previous.AbsoluteExpiresAt, previous.ID); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法轮换管理员会话，请稍后重试")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.admin_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = 'rotated', updated_at = $2
		WHERE id = $1 AND status = 'active'`, previous.ID, now); err != nil {
		return issuedCredential{}, fault.New("service_unavailable", "无法撤销旧管理员会话，请稍后重试")
	}
	return issuedCredential{Token: token, CSRF: csrf, ExpiresAt: previous.AbsoluteExpiresAt,
		AbsoluteExpiresAt: previous.AbsoluteExpiresAt, SessionID: newID}, nil
}

func (s *Service) revokeClientSession(ctx context.Context, tx pgx.Tx, id, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE public.client_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE id = $1 AND status = 'active'`, id, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销会话，请稍后重试")
	}
	return nil
}

func (s *Service) revokeAdminSession(ctx context.Context, tx pgx.Tx, id, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE public.admin_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE id = $1 AND status = 'active'`, id, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销管理员会话，请稍后重试")
	}
	return nil
}

// revokeRefreshFamily 撤销整个刷新链，用于复用检测与登出。
func (s *Service) revokeRefreshFamily(ctx context.Context, tx pgx.Tx, sessionID, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE public.refresh_tokens
		SET revoked_at = coalesce(revoked_at, $2), revoke_reason = coalesce(revoke_reason, $3)
		WHERE session_id = $1`, sessionID, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销刷新链，请稍后重试")
	}
	return nil
}

// revokePlatformSessions 撤销该用户全部平台会话，用于显式登出与重新验证。
func (s *Service) revokePlatformSessions(ctx context.Context, tx pgx.Tx, userID, reason string, now time.Time) error {
	if _, err := tx.Exec(ctx, `UPDATE public.platform_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE user_id = $1 AND status = 'active'`, userID, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销平台会话，请稍后重试")
	}
	return nil
}

// rotateRefreshToken 校验并轮换 App 刷新令牌；复用旧令牌时撤销整条链。
func (s *Service) rotateRefreshToken(ctx context.Context, presented string, now time.Time) (issuedCredential, string, error) {
	digest := tokenDigest(presented)
	var result issuedCredential
	var userID string
	var outcome error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var refreshID, sessionID, familyID string
		var generation int
		var refreshExpiry, sessionAbsolute time.Time
		var usedAt, revokedAt *time.Time
		var sessionStatus string
		err := tx.QueryRow(ctx, `SELECT rt.id::text, rt.session_id::text, rt.family_id::text, rt.generation,
				rt.expires_at, rt.used_at, rt.revoked_at, cs.user_id::text, cs.status, cs.absolute_expires_at
			FROM public.refresh_tokens rt
			JOIN public.client_sessions cs ON cs.id = rt.session_id
			WHERE rt.token_hash = $1
			FOR UPDATE OF rt, cs`, digest).Scan(&refreshID, &sessionID, &familyID, &generation,
			&refreshExpiry, &usedAt, &revokedAt, &userID, &sessionStatus, &sessionAbsolute)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.New("unauthenticated", "刷新令牌无效")
		}
		if err != nil {
			return fault.New("service_unavailable", "无法读取刷新令牌")
		}
		if usedAt != nil || revokedAt != nil {
			// 复用已使用或已撤销的令牌：撤销整条链与该会话。状态变更必须先提交。
			if err := s.revokeRefreshFamily(ctx, tx, sessionID, "reuse", now); err != nil {
				return err
			}
			if err := s.revokeClientSession(ctx, tx, sessionID, "refresh_reuse", now); err != nil {
				return err
			}
			outcome = fault.New("unauthenticated", "刷新令牌已失效，请重新登录")
			return nil
		}
		if !refreshExpiry.After(now) || sessionStatus != "active" || !sessionAbsolute.After(now) {
			if err := s.revokeRefreshFamily(ctx, tx, sessionID, "expired", now); err != nil {
				return err
			}
			outcome = fault.New("unauthenticated", "刷新令牌已过期，请重新登录")
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE public.refresh_tokens
			SET used_at = $2, revoked_at = $2, revoke_reason = 'rotated'
			WHERE id = $1`, refreshID, now); err != nil {
			return fault.New("service_unavailable", "无法轮换刷新令牌")
		}
		newRefresh, newRefreshDigest, err := issueToken(tokenPrefixRefresh)
		if err != nil {
			return err
		}
		refreshExpiry = now.Add(s.options.RefreshTTL)
		if refreshExpiry.After(sessionAbsolute) {
			refreshExpiry = sessionAbsolute
		}
		if _, err := tx.Exec(ctx, `INSERT INTO public.refresh_tokens
			(id, session_id, family_id, token_hash, generation, created_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			web.NewID(), sessionID, familyID, newRefreshDigest, generation+1, now, refreshExpiry); err != nil {
			return fault.New("service_unavailable", "无法建立新的刷新令牌")
		}
		accessToken, accessDigest, err := issueToken(tokenPrefixUserApp)
		if err != nil {
			return err
		}
		accessExpiry := now.Add(s.options.AppAccessTTL)
		if accessExpiry.After(sessionAbsolute) {
			accessExpiry = sessionAbsolute
		}
		if _, err := tx.Exec(ctx, `UPDATE public.client_sessions
			SET token_hash = $2, expires_at = $3, last_seen_at = $4, updated_at = $4
			WHERE id = $1 AND status = 'active'`, sessionID, accessDigest, accessExpiry, now); err != nil {
			return fault.New("service_unavailable", "无法更新访问令牌")
		}
		result = issuedCredential{Token: accessToken, RefreshToken: newRefresh,
			ExpiresAt: accessExpiry, AbsoluteExpiresAt: sessionAbsolute, SessionID: sessionID}
		return nil
	})
	if err != nil {
		return issuedCredential{}, "", err
	}
	if outcome != nil {
		return issuedCredential{}, "", outcome
	}
	return result, userID, nil
}
