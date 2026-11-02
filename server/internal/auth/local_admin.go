package auth

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// localAdminAutoLoginAllowed 只判断当前请求是否属于开发回环自动登录范围。
func (s *Service) localAdminAutoLoginAllowed(r *http.Request) bool {
	return s.options.LocalAdminAutoLogin != nil && s.options.LocalAdminAutoLogin(r)
}

// localAdminAutoLogin 只处理 development 回环入口的会话引导；调用方负责配置与地址校验。
func (s *Service) localAdminAutoLogin(w http.ResponseWriter, r *http.Request) bool {
	if !s.localAdminAutoLoginAllowed(r) {
		return false
	}
	ctx := r.Context()
	now := s.clock()
	var id, username, displayName, role string
	var totpEnabled bool
	err := s.pool.QueryRow(ctx, `SELECT id::text, username, coalesce(display_name, ''), role, totp_enabled
		FROM public.admin_users WHERE disabled = false
		ORDER BY CASE role WHEN 'owner' THEN 0 ELSE 1 END, created_at, id LIMIT 1`).Scan(&id, &username, &displayName, &role, &totpEnabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil || !roleExists(role) {
		return false
	}
	permissions := permissionsFor(KindAdmin, role)
	var issued issuedCredential
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		created, err := s.createAdminSession(ctx, tx, id, now)
		if err != nil {
			return err
		}
		issued = created
		return audit.Record(ctx, tx, audit.Event{
			Actor:        web.Actor{ID: id, Kind: KindAdmin, SessionID: created.SessionID, Permissions: permissions},
			Action:       "admin.local_login",
			ResourceType: "admin_session",
			ResourceID:   created.SessionID,
			RequestID:    requestID(ctx),
		})
	})
	if err != nil {
		return false
	}
	s.setSessionCookie(w, KindAdmin, issued.Token, issued.ExpiresAt)
	web.JSON(w, r, http.StatusOK, sessionStatusResponse{
		Authenticated: true, Kind: KindAdmin, CSRF: issued.CSRF, Channel: ChannelWeb,
		ExpiresAt: &issued.ExpiresAt, AuthenticatedAt: &now, Fresh: true,
		Permissions: permissions, Capabilities: s.capabilities(),
	})
	return true
}
