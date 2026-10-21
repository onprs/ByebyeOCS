package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// EffectiveActor 按数据库中的会话、主体状态与角色重新构造 Actor。
//
// SSE 等长连接每次拉取都应使用返回值中的权限过滤事件，不能继续使用连接建立时的
// 旧 Permissions 读取更高权限的事件。
func (s *Service) EffectiveActor(ctx context.Context, actor web.Actor) (web.Actor, error) {
	if !validUUID(actor.ID) || !validUUID(actor.SessionID) {
		return web.Actor{}, fault.New("unauthenticated", "登录已失效")
	}
	now := s.clock()
	switch actor.Kind {
	case KindUser:
		var status, channel, subjectID, userStatus string
		var authenticatedAt, expiresAt, absoluteExpiresAt, lastSeenAt time.Time
		err := s.pool.QueryRow(ctx, `SELECT cs.status, cs.channel, cs.authenticated_at, cs.expires_at,
				cs.absolute_expires_at, cs.last_seen_at, cs.user_id::text, u.status
			FROM public.client_sessions cs
			JOIN public.users u ON u.id = cs.user_id
			WHERE cs.id = $1`, actor.SessionID).Scan(&status, &channel, &authenticatedAt,
			&expiresAt, &absoluteExpiresAt, &lastSeenAt, &subjectID, &userStatus)
		if errors.Is(err, pgx.ErrNoRows) {
			return web.Actor{}, fault.New("unauthenticated", "登录已失效")
		}
		if err != nil {
			return web.Actor{}, fault.New("service_unavailable", "无法校验会话状态")
		}
		if status != "active" || subjectID != actor.ID || actor.Kind != KindUser {
			return web.Actor{}, fault.New("unauthenticated", "登录已失效")
		}
		if !now.Before(expiresAt) || !now.Before(absoluteExpiresAt) {
			_ = s.sessions.expire(ctx, KindUser, actor.SessionID, now, "expired")
			return web.Actor{}, fault.New("unauthenticated", "登录已失效，请重新验证身份")
		}
		if channel == ChannelWeb && s.options.SessionIdle > 0 && now.Sub(lastSeenAt) > s.options.SessionIdle {
			_ = s.sessions.expire(ctx, KindUser, actor.SessionID, now, "idle")
			return web.Actor{}, fault.New("unauthenticated", "登录已因长时间未使用而失效")
		}
		if userStatus != "active" {
			return web.Actor{}, fault.New("forbidden", "账号状态受限，请通过人工申诉处理")
		}
		return web.Actor{
			ID: subjectID, Kind: KindUser, SessionID: actor.SessionID,
			Permissions: permissionsFor(KindUser, ""), AuthenticatedAt: authenticatedAt,
		}, nil
	case KindAdmin:
		var status, subjectID, role string
		var disabled bool
		var authenticatedAt, expiresAt, absoluteExpiresAt, lastSeenAt time.Time
		err := s.pool.QueryRow(ctx, `SELECT s.status, s.authenticated_at, s.expires_at, s.absolute_expires_at,
				s.last_seen_at, s.admin_id::text, a.disabled, a.role
			FROM public.admin_sessions s
			JOIN public.admin_users a ON a.id = s.admin_id
			WHERE s.id = $1`, actor.SessionID).Scan(&status, &authenticatedAt,
			&expiresAt, &absoluteExpiresAt, &lastSeenAt, &subjectID, &disabled, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return web.Actor{}, fault.New("unauthenticated", "登录已失效")
		}
		if err != nil {
			return web.Actor{}, fault.New("service_unavailable", "无法校验会话状态")
		}
		if status != "active" || subjectID != actor.ID || actor.Kind != KindAdmin {
			return web.Actor{}, fault.New("unauthenticated", "登录已失效")
		}
		if !now.Before(expiresAt) || !now.Before(absoluteExpiresAt) {
			_ = s.sessions.expire(ctx, KindAdmin, actor.SessionID, now, "expired")
			return web.Actor{}, fault.New("unauthenticated", "登录已失效，请重新验证身份")
		}
		if s.options.SessionIdle > 0 && now.Sub(lastSeenAt) > s.options.SessionIdle {
			_ = s.sessions.expire(ctx, KindAdmin, actor.SessionID, now, "idle")
			return web.Actor{}, fault.New("unauthenticated", "登录已因长时间未使用而失效")
		}
		if disabled {
			return web.Actor{}, fault.New("forbidden", "管理员账号已停用")
		}
		permissions := permissionsFor(KindAdmin, role)
		if len(permissions) == 0 {
			return web.Actor{}, fault.New("forbidden", "管理员角色配置无效")
		}
		return web.Actor{
			ID: subjectID, Kind: KindAdmin, SessionID: actor.SessionID,
			Permissions: permissions, AuthenticatedAt: authenticatedAt,
		}, nil
	default:
		return web.Actor{}, fault.New("unauthenticated", "身份类型无效")
	}
}

// ValidateActor 校验已认证主体并确认权限未发生变化。
//
// 角色变更、停用或撤销会话都会让长连接失败关闭；调用方应关闭流，让客户端重新认证
// 并以 EffectiveActor 返回的新权限重连。
func (s *Service) ValidateActor(ctx context.Context, actor web.Actor) error {
	current, err := s.EffectiveActor(ctx, actor)
	if err != nil {
		return err
	}
	if !samePermissions(actor.Permissions, current.Permissions) {
		return fault.New("unauthenticated", "权限已变更，请重新连接事件流")
	}
	return nil
}

// RevokeUser 在同一事务内撤销该用户的全部客户端会话、刷新链与平台会话。
//
// 隐私删除流程由调用方负责用户状态、资料与业务数据；本方法只处理认证模块拥有的表。
func (s *Service) RevokeUser(ctx context.Context, tx pgx.Tx, userID string) error {
	if tx == nil {
		return fault.New("internal_error", "撤销用户会话要求显式事务")
	}
	if !validUUID(userID) {
		return fault.New("validation_failed", "用户编号无效")
	}
	now := s.clock()
	const reason = "user_revoked"
	if _, err := tx.Exec(ctx, `UPDATE public.refresh_tokens
		SET revoked_at = coalesce(revoked_at, $2), revoke_reason = coalesce(revoke_reason, $3)
		WHERE session_id IN (SELECT id FROM public.client_sessions WHERE user_id = $1)`,
		userID, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销刷新令牌链")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.client_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE user_id = $1 AND status = 'active'`, userID, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销客户端会话")
	}
	if _, err := tx.Exec(ctx, `UPDATE public.platform_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE user_id = $1 AND status = 'active'`, userID, now, reason); err != nil {
		return fault.New("service_unavailable", "无法撤销平台会话")
	}
	return nil
}
