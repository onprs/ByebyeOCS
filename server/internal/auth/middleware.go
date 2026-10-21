package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type sessionContextKey struct{}

func withSession(ctx context.Context, state sessionState) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, state)
}

// sessionFrom 返回本次请求已验证的会话摘要。
func sessionFrom(ctx context.Context) (sessionState, bool) {
	state, ok := ctx.Value(sessionContextKey{}).(sessionState)
	return state, ok
}

func unsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// presentedCredential 描述请求携带的凭据来源，不包含任何明文日志字段。
type presentedCredential struct {
	Token       string
	Channel     string
	CookieBased bool
	Present     bool
}

func bearerToken(value string) (string, bool) {
	const prefix = "bearer "
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(value[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// presentedCredentials 只接受本端 Cookie；用户端额外接受 Bearer，管理员端不接受。
// 刷新接口以刷新令牌本身为凭据，允许客户端携带已过期的访问令牌；其他路径仍按失败关闭处理。
func (s *Service) presentedCredentials(r *http.Request, kind string) presentedCredential {
	if token, ok := s.sessionTokenFromCookie(r, kind); ok {
		return presentedCredential{Token: token, Channel: ChannelWeb, CookieBased: true, Present: true}
	}
	if header := r.Header.Get("Authorization"); header != "" {
		if kind != KindUser {
			return presentedCredential{Present: true}
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/auth/refresh" {
			// 刷新请求只提交刷新令牌，不把访问令牌作为认证依据。
			return presentedCredential{}
		}
		token, ok := bearerToken(header)
		if !ok {
			return presentedCredential{Present: true}
		}
		return presentedCredential{Token: token, Channel: ChannelApp, Present: true}
	}
	return presentedCredential{}
}

// resolveSession 校验过期、空闲与状态；失败时不回退为匿名。
func (s *Service) resolveSession(ctx context.Context, kind string, presented presentedCredential, now time.Time) (sessionState, error) {
	unauthenticated := fault.New("unauthenticated", "登录已失效")
	if presented.Token == "" || !tokenMatches(presented.Token, kind, presented.Channel) {
		return sessionState{}, unauthenticated
	}
	state, err := s.sessions.lookup(ctx, kind, tokenDigest(presented.Token))
	if err != nil {
		return sessionState{}, err
	}
	if state.Status != "active" || state.Channel != presented.Channel {
		return sessionState{}, unauthenticated
	}
	if !now.Before(state.ExpiresAt) || !now.Before(state.AbsoluteExpiresAt) {
		// Web 会话到期立即撤销；App 访问令牌到期可由刷新令牌续期，不撤销会话。
		if presented.Channel == ChannelWeb {
			_ = s.sessions.expire(ctx, kind, state.ID, now, "expired")
		}
		return sessionState{}, unauthenticated
	}
	if presented.Channel == ChannelWeb && s.options.SessionIdle > 0 && now.Sub(state.LastSeenAt) > s.options.SessionIdle {
		_ = s.sessions.expire(ctx, kind, state.ID, now, "idle")
		return sessionState{}, unauthenticated
	}
	if presented.Channel == ChannelWeb && now.Sub(state.LastSeenAt) >= sessionTouchInterval {
		_ = s.sessions.touch(ctx, kind, state.ID, now)
	}
	return state, nil
}

// publicPath 只放行确认为公共的认证入口；其余路径必须通过认证。
func (s *Service) publicPath(kind string, r *http.Request) bool {
	path := r.URL.Path
	if _, ok := s.extraPublic[path]; ok {
		return true
	}
	switch kind {
	case KindUser:
		switch {
		case path == "/api/v1/auth/chaoxing/challenges" && r.Method == http.MethodPost:
			return true
		case strings.HasPrefix(path, "/api/v1/auth/chaoxing/challenges/") &&
			(r.Method == http.MethodGet || r.Method == http.MethodPost):
			return true
		case path == "/api/v1/auth/refresh" && r.Method == http.MethodPost:
			// App 刷新只提交刷新令牌；Web 轮换在已认证时仍受 CSRF 与来源检查。
			return true
		case path == "/api/v1/auth/session" && r.Method == http.MethodGet:
			return true
		}
	case KindAdmin:
		switch {
		case path == "/admin/api/v1/auth/login" && r.Method == http.MethodPost:
			return true
		case path == "/admin/api/v1/auth/session" && r.Method == http.MethodGet:
			return true
		}
	}
	return false
}

// originAccepted 校验配置的来源。Cookie 认证的写请求必须带匹配的 Origin；
// Bearer 与 App 请求允许缺失 Origin，但出现时必须匹配。
func (s *Service) originAccepted(r *http.Request, kind string, required bool) bool {
	value := strings.TrimSpace(r.Header.Get("Origin"))
	if value == "" {
		return !required
	}
	return s.originFor(kind).matches(value)
}

// Middleware 校验会话、来源与 CSRF，并把 web.Actor 写入请求上下文。
//
// 规则：
//   - 无凭据且命中公共认证路径时按匿名放行；
//   - 携带非空但无效的凭据一律返回 401，不回退为匿名；
//   - Cookie 会话的写请求要求匹配 Origin 与 X-CSRF-Token；
//   - 用户端与管理员端只接受各自 Cookie，避免会话串用。
func (s *Service) Middleware(kind string, next http.Handler) http.Handler {
	if kind != KindUser && kind != KindAdmin {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.Error(w, r, http.StatusInternalServerError, "internal_error", "认证端类型无效")
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := s.clock()
		presented := s.presentedCredentials(r, kind)
		if !presented.Present {
			if s.publicPath(kind, r) {
				if unsafeMethod(r.Method) && !s.originAccepted(r, kind, false) {
					web.Error(w, r, http.StatusForbidden, "csrf_invalid", "来源校验未通过，请刷新页面后重试")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "请先登录")
			return
		}
		state, err := s.resolveSession(r.Context(), kind, presented, now)
		if err != nil {
			if presented.CookieBased {
				s.clearSessionCookie(w, kind)
			}
			if code, _ := fault.Describe(err); code == "unauthenticated" {
				web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "登录已失效，请重新验证身份")
				return
			}
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "认证服务暂不可用，请稍后重试")
			return
		}
		if unsafeMethod(r.Method) {
			if !s.originAccepted(r, kind, presented.CookieBased) {
				web.Error(w, r, http.StatusForbidden, "csrf_invalid", "来源校验未通过，请刷新页面后重试")
				return
			}
			if presented.CookieBased {
				provided := strings.TrimSpace(r.Header.Get(csrfHeader))
				if provided == "" || !constantTimeEqual(tokenDigest(provided), state.CSRFHash) {
					web.Error(w, r, http.StatusForbidden, "csrf_invalid", "请求校验失败，请刷新页面后重试")
					return
				}
			}
		}
		// 管理员角色每次请求从数据库重取；停用或未知角色失败关闭。
		permissions := permissionsFor(kind, state.Role)
		if kind == KindAdmin {
			if state.Disabled {
				web.Error(w, r, http.StatusForbidden, "forbidden", "管理员账号已停用")
				return
			}
			if len(permissions) == 0 {
				web.Error(w, r, http.StatusForbidden, "forbidden", "管理员角色配置无效")
				return
			}
		}
		actor := web.Actor{
			ID:              state.SubjectID,
			Kind:            kind,
			SessionID:       state.ID,
			Permissions:     permissions,
			AuthenticatedAt: state.AuthenticatedAt,
		}
		ctx := web.WithActor(r.Context(), actor)
		ctx = withSession(ctx, state)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
