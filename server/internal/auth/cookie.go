package auth

import (
	"net/http"
	"strings"
	"time"
)

// Cookie 名称两端不同，避免用户端与管理员端会话互相串联。
const (
	userSessionCookie  = "bbo_user_session"
	adminSessionCookie = "bbo_admin_session"
	challengeCookie    = "bbo_login_binding"

	csrfHeader      = "X-CSRF-Token"
	challengeHeader = "X-Challenge-Binding"
)

func (s *Service) sessionCookieName(kind string) string {
	if kind == KindAdmin {
		return adminSessionCookie
	}
	return userSessionCookie
}

func (s *Service) newSessionCookie(name, value string, expires time.Time) *http.Cookie {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		// Lax 兼顾从外部返回的顶层导航；CSRF 令牌仍是写操作的必要条件。
		SameSite: http.SameSiteLaxMode,
		Secure:   s.options.CookieSecure,
	}
	if value != "" {
		cookie.Expires = expires.UTC()
		cookie.MaxAge = int(time.Until(expires).Seconds())
		if cookie.MaxAge < 1 {
			cookie.MaxAge = 1
		}
	}
	return cookie
}

func (s *Service) deletionCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.options.CookieSecure,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
	}
}

// setSessionCookie 下发 HttpOnly 会话 Cookie；令牌只出现在 Set-Cookie 响应头。
func (s *Service) setSessionCookie(w http.ResponseWriter, kind, token string, expires time.Time) {
	http.SetCookie(w, s.newSessionCookie(s.sessionCookieName(kind), token, expires))
}

func (s *Service) clearSessionCookie(w http.ResponseWriter, kind string) {
	http.SetCookie(w, s.deletionCookie(s.sessionCookieName(kind)))
}

// sessionTokenFromCookie 只读取本端 Cookie 名称。
func (s *Service) sessionTokenFromCookie(r *http.Request, kind string) (string, bool) {
	cookie, err := r.Cookie(s.sessionCookieName(kind))
	if err != nil || cookie.Value == "" {
		return "", false
	}
	return cookie.Value, true
}

func (s *Service) setChallengeCookie(w http.ResponseWriter, binding string, expires time.Time) {
	cookie := s.newSessionCookie(challengeCookie, binding, expires)
	cookie.Path = "/api/v1/auth/chaoxing"
	http.SetCookie(w, cookie)
}

func (s *Service) clearChallengeCookie(w http.ResponseWriter) {
	cookie := s.deletionCookie(challengeCookie)
	cookie.Path = "/api/v1/auth/chaoxing"
	http.SetCookie(w, cookie)
}

// challengeBinding 优先使用 HttpOnly Cookie；App 可通过请求头提交本次挑战绑定值。
func (s *Service) challengeBinding(r *http.Request) string {
	if cookie, err := r.Cookie(challengeCookie); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	return strings.TrimSpace(r.Header.Get(challengeHeader))
}
