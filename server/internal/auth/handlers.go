package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// Register 注册认证、会话与偏好接口的完整路径。
func (s *Service) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/auth/chaoxing/challenges", s.handleChallengeCreate)
	mux.HandleFunc("GET /api/v1/auth/chaoxing/challenges/{id}", s.handleChallengeStatus)
	mux.HandleFunc("POST /api/v1/auth/chaoxing/challenges/{id}/verify", s.handleChallengeVerify)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/auth/session", s.handleSessionStatus)
	mux.HandleFunc("GET /api/v1/me", s.handleMe)
	mux.HandleFunc("GET /api/v1/me/sessions", s.handleListSessions)
	mux.HandleFunc("DELETE /api/v1/me/sessions/{id}", s.handleRevokeSession)
	mux.HandleFunc("GET /api/v1/me/preferences", s.handlePreferences)
	mux.HandleFunc("PATCH /api/v1/me/preferences", s.handlePreferences)

	mux.HandleFunc("POST /admin/api/v1/auth/login", s.handleAdminLogin)
	mux.HandleFunc("POST /admin/api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("POST /admin/api/v1/auth/step-up", s.handleAdminStepUp)
	mux.HandleFunc("GET /admin/api/v1/auth/session", s.handleSessionStatus)
	mux.HandleFunc("GET /admin/api/v1/me", s.handleMe)
	mux.HandleFunc("GET /admin/api/v1/me/sessions", s.handleListSessions)
	mux.HandleFunc("DELETE /admin/api/v1/me/sessions/{id}", s.handleRevokeSession)
	mux.HandleFunc("GET /admin/api/v1/me/preferences", s.handlePreferences)
	mux.HandleFunc("PATCH /admin/api/v1/me/preferences", s.handlePreferences)
	mux.HandleFunc("GET /admin/api/v1/admin-users", s.handleListAdminUsers)
	mux.HandleFunc("PATCH /admin/api/v1/admin-users/{id}/role", s.handleChangeAdminRole)
	mux.HandleFunc("POST /admin/api/v1/admin-users/{id}/revoke-sessions", s.handleRevokeAdminSessions)
}

// kindFor 依据入口路径确定端类型；两端由入口严格分离。
func kindFor(r *http.Request) string {
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		return KindAdmin
	}
	return KindUser
}

func decodeOptional(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.ContentLength == 0 {
		return true
	}
	return web.DecodeLimit(w, r, target, 64*1024)
}

type sessionView struct {
	Channel         string    `json:"channel"`
	ExpiresAt       time.Time `json:"expires_at"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
	Fresh           bool      `json:"fresh"`
}

type platformView struct {
	State      string     `json:"state"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type userView struct {
	ID          string       `json:"id"`
	Status      string       `json:"status"`
	DisplayName string       `json:"display_name,omitempty"`
	School      string       `json:"school,omitempty"`
	Provider    string       `json:"identity_provider,omitempty"`
	VerifiedAt  *time.Time   `json:"identity_verified_at,omitempty"`
	Session     sessionView  `json:"session"`
	Platform    platformView `json:"platform"`
	Permissions []string     `json:"permissions"`
}

type adminView struct {
	ID          string      `json:"id"`
	Username    string      `json:"username"`
	DisplayName string      `json:"display_name,omitempty"`
	Role        string      `json:"role"`
	TOTPEnabled bool        `json:"totp_enabled"`
	Session     sessionView `json:"session"`
	Permissions []string    `json:"permissions"`
}

type loginResponse struct {
	Mode         string     `json:"mode"`
	CSRF         string     `json:"csrf_token,omitempty"`
	AccessToken  string     `json:"access_token,omitempty"`
	RefreshToken string     `json:"refresh_token,omitempty"`
	TokenType    string     `json:"token_type,omitempty"`
	ExpiresAt    time.Time  `json:"expires_at"`
	User         *userView  `json:"user,omitempty"`
	Admin        *adminView `json:"admin,omitempty"`
	Permissions  []string   `json:"permissions"`
}

type sessionStatusResponse struct {
	Authenticated   bool           `json:"authenticated"`
	Kind            string         `json:"kind"`
	CSRF            string         `json:"csrf_token,omitempty"`
	Channel         string         `json:"channel,omitempty"`
	ExpiresAt       *time.Time     `json:"expires_at,omitempty"`
	AuthenticatedAt *time.Time     `json:"authenticated_at,omitempty"`
	Fresh           bool           `json:"fresh"`
	Permissions     []string       `json:"permissions"`
	Capabilities    map[string]any `json:"capabilities"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type logoutRequest struct {
	Platform bool `json:"platform"`
}

// handleSessionStatus 提供安全的 CSRF 令牌获取方式；不返回会话 Cookie 内容。
func (s *Service) handleSessionStatus(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	w.Header().Set("Cache-Control", "no-store")
	response := sessionStatusResponse{
		Authenticated: false,
		Kind:          kind,
		Permissions:   []string{},
		Capabilities:  s.capabilities(),
	}
	state, ok := sessionFrom(r.Context())
	if ok {
		actor, authorized := web.Require(w, r, kind, permissionFor(kind))
		if !authorized {
			return
		}
		expires := state.ExpiresAt
		authenticatedAt := state.AuthenticatedAt
		response.Authenticated = true
		response.CSRF = s.csrfToken(state.ID)
		response.Channel = state.Channel
		response.ExpiresAt = &expires
		response.AuthenticatedAt = &authenticatedAt
		response.Fresh = !actor.AuthenticatedAt.IsZero() && s.clock().Sub(actor.AuthenticatedAt) <= s.options.FreshWindow
		response.Permissions = actor.Permissions
	}
	web.JSON(w, r, http.StatusOK, response)
}

// handleRefresh 处理 Web 会话轮换与 App 刷新令牌轮换。
// 请求进入时已由 Middleware 校验存在的 Cookie 会话与 CSRF；App 仅提交刷新令牌。
func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	if kind != KindUser {
		web.Error(w, r, http.StatusNotFound, "not_found", "请求的接口不存在")
		return
	}
	var request refreshRequest
	if !decodeOptional(w, r, &request) {
		return
	}
	now := s.clock()
	if strings.TrimSpace(request.RefreshToken) != "" {
		if !strings.HasPrefix(request.RefreshToken, tokenPrefixRefresh) {
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "刷新令牌无效")
			return
		}
		issued, _, err := s.rotateRefreshToken(r.Context(), request.RefreshToken, now)
		if err != nil {
			if code, _ := fault.Describe(err); code == "unauthenticated" {
				web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "刷新令牌已失效，请重新登录")
				return
			}
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "刷新暂不可用，请稍后重试")
			return
		}
		web.JSON(w, r, http.StatusOK, loginResponse{
			Mode:         ChannelApp,
			AccessToken:  issued.Token,
			RefreshToken: issued.RefreshToken,
			TokenType:    "Bearer",
			ExpiresAt:    issued.ExpiresAt,
			Permissions:  permissionsFor(KindUser, ""),
		})
		return
	}
	state, ok := sessionFrom(r.Context())
	if !ok || state.Channel != ChannelWeb {
		web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	var issued issuedCredential
	err := s.inTx(r.Context(), func(tx pgx.Tx) error {
		rotated, err := s.rotateClientSession(r.Context(), tx, state, now)
		if err != nil {
			return err
		}
		issued = rotated
		return nil
	})
	if err != nil {
		code, message := fault.Describe(err)
		if code == "unauthenticated" {
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", message)
			return
		}
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", message)
		return
	}
	s.setSessionCookie(w, KindUser, issued.Token, issued.ExpiresAt)
	web.JSON(w, r, http.StatusOK, loginResponse{
		Mode:        ChannelWeb,
		CSRF:        issued.CSRF,
		ExpiresAt:   issued.ExpiresAt,
		Permissions: permissionsFor(KindUser, ""),
	})
}

// handleLogout 撤销当前端会话；可选择同时撤销平台会话。
func (s *Service) handleLogout(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return
	}
	state, ok := sessionFrom(r.Context())
	if !ok {
		web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	var request logoutRequest
	if !decodeOptional(w, r, &request) {
		return
	}
	now := s.clock()
	err := s.inTx(r.Context(), func(tx pgx.Tx) error {
		if kind == KindAdmin {
			if err := s.revokeAdminSession(r.Context(), tx, state.ID, "logout", now); err != nil {
				return err
			}
		} else {
			if err := s.revokeRefreshFamily(r.Context(), tx, state.ID, "logout", now); err != nil {
				return err
			}
			if err := s.revokeClientSession(r.Context(), tx, state.ID, "logout", now); err != nil {
				return err
			}
			if request.Platform {
				if err := s.revokePlatformSessions(r.Context(), tx, actor.ID, "logout", now); err != nil {
					return err
				}
			}
		}
		event := audit.Event{
			Actor:        actor,
			Action:       kind + ".logout",
			ResourceType: kind + "_session",
			ResourceID:   state.ID,
			RequestID:    requestID(r.Context()),
		}
		if err := audit.Record(r.Context(), tx, event); err != nil {
			return fault.New("service_unavailable", "无法记录登出审计")
		}
		return nil
	})
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "退出登录暂未完成，请稍后重试")
		return
	}
	if kind == KindAdmin {
		s.clearSessionCookie(w, KindAdmin)
	} else {
		s.clearSessionCookie(w, KindUser)
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"revoked": true, "platform_revoked": kind == KindUser && request.Platform})
}

// handleMe 返回安全展示身份；不返回平台会话材料、主体索引或口令摘要。
func (s *Service) handleMe(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return
	}
	state, _ := sessionFrom(r.Context())
	now := s.clock()
	fresh := !actor.AuthenticatedAt.IsZero() && now.Sub(actor.AuthenticatedAt) <= s.options.FreshWindow
	view := sessionView{Channel: state.Channel, ExpiresAt: state.ExpiresAt, AuthenticatedAt: state.AuthenticatedAt, Fresh: fresh}
	if kind == KindAdmin {
		var username, displayName, role string
		var totpEnabled bool
		err := s.pool.QueryRow(r.Context(), `SELECT username, coalesce(display_name, ''), role, totp_enabled
			FROM public.admin_users WHERE id = $1 AND disabled = false`, actor.ID).
			Scan(&username, &displayName, &role, &totpEnabled)
		if errors.Is(err, pgx.ErrNoRows) {
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "管理员账号不可用")
			return
		}
		if err != nil {
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取管理员资料")
			return
		}
		web.JSON(w, r, http.StatusOK, adminView{
			ID: actor.ID, Username: username, DisplayName: displayName, Role: role, TOTPEnabled: totpEnabled,
			Session: view, Permissions: actor.Permissions,
		})
		return
	}
	user, err := s.loadUserView(r.Context(), actor.ID, view, actor.Permissions, now)
	if err != nil {
		code, message := fault.Describe(err)
		if code == "not_found" {
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "用户资料不可用")
			return
		}
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", message)
		return
	}
	web.JSON(w, r, http.StatusOK, user)
}

func (s *Service) loadUserView(ctx context.Context, userID string, session sessionView, permissions []string, now time.Time) (userView, error) {
	view := userView{Session: session, Permissions: permissions, Platform: platformView{State: "reauthorize_required"}}
	var verifiedAt *time.Time
	err := s.pool.QueryRow(ctx, `SELECT u.id::text, u.status,
			coalesce(e.display_name, ''), coalesce(e.school, ''), coalesce(e.provider, ''), e.verified_at
		FROM public.users u
		LEFT JOIN public.external_identities e ON e.user_id = u.id AND e.status = 'active'
		WHERE u.id = $1`, userID).
		Scan(&view.ID, &view.Status, &view.DisplayName, &view.School, &view.Provider, &verifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return userView{}, fault.New("not_found", "用户不存在")
	}
	if err != nil {
		return userView{}, fault.New("service_unavailable", "无法读取用户资料")
	}
	view.VerifiedAt = verifiedAt
	view.Provider = strings.TrimSpace(view.Provider)
	var platformExpiry time.Time
	err = s.pool.QueryRow(ctx, `SELECT expires_at FROM public.platform_sessions
		WHERE user_id = $1 AND status = 'active' AND expires_at > $2
		ORDER BY created_at DESC LIMIT 1`, userID, now).Scan(&platformExpiry)
	if err == nil {
		expiry := platformExpiry.UTC()
		view.Platform = platformView{State: "active", VerifiedAt: verifiedAt, ExpiresAt: &expiry}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return userView{}, fault.New("service_unavailable", "无法读取平台会话状态")
	}
	return view, nil
}
