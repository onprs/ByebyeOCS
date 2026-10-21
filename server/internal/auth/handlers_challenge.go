package auth

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type challengeCreateRequest struct {
	Method string `json:"method"`
	Client string `json:"client"`
}

type challengeVerifyRequest struct {
	School       string `json:"school"`
	Account      string `json:"account"`
	Password     string `json:"password"`
	Verification string `json:"verification"`
}

func writeRetryAfter(w http.ResponseWriter, r *http.Request, retryAfter time.Duration, message string) {
	seconds := int(math.Ceil(retryAfter.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	web.Error(w, r, http.StatusTooManyRequests, "rate_limited", message)
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	writeRetryAfter(w, r, retryAfter, "登录失败次数过多，请稍后再试")
}

// handleChallengeCreate 创建登录挑战；扫码方式未实现，不在能力中公布。
func (s *Service) handleChallengeCreate(w http.ResponseWriter, r *http.Request) {
	if s.options.Verifier == nil {
		web.Error(w, r, http.StatusServiceUnavailable, "external_disabled", "外部身份验证尚未配置")
		return
	}
	var request challengeCreateRequest
	if !web.DecodeLimit(w, r, &request, 8*1024) {
		return
	}
	method := strings.TrimSpace(request.Method)
	if method == "" {
		method = "password"
	}
	channel := strings.TrimSpace(request.Client)
	if channel == "" {
		channel = ChannelWeb
	}
	now := s.clock()
	state, binding, retryAfter, err := s.createChallenge(r.Context(), method, channel, s.clientKey(r), now)
	if err != nil {
		if retryAfter > 0 {
			writeRetryAfter(w, r, retryAfter, "登录请求过于频繁，请稍后再试")
			return
		}
		web.Fail(w, r, err)
		return
	}
	response := map[string]any{
		"id":                 state.ID,
		"method":             state.Method,
		"client":             state.ClientChannel,
		"status":             state.Status,
		"expires_at":         state.ExpiresAt,
		"attempts_remaining": state.MaxAttempts,
		"capabilities":       s.capabilities(),
	}
	if channel == ChannelWeb {
		s.setChallengeCookie(w, binding, state.ExpiresAt)
	} else {
		// App 无法使用 Cookie；绑定值只返回给发起挑战的客户端一次。
		response["binding_token"] = binding
	}
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, r, http.StatusCreated, response)
}

// handleChallengeStatus 返回挑战状态；必须提供同一客户端绑定值。
func (s *Service) handleChallengeStatus(w http.ResponseWriter, r *http.Request) {
	now := s.clock()
	state, err := s.loadChallenge(r.Context(), r.PathValue("id"), s.challengeBinding(r), now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	remaining := state.MaxAttempts - state.Attempts
	if remaining < 0 {
		remaining = 0
	}
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, r, http.StatusOK, map[string]any{
		"id":                 state.ID,
		"method":             state.Method,
		"client":             state.ClientChannel,
		"status":             state.Status,
		"expires_at":         state.ExpiresAt,
		"attempts_remaining": remaining,
	})
}

// handleChallengeVerify 提交持有人完成的验证，成功后在事务中建立内部身份与会话。
func (s *Service) handleChallengeVerify(w http.ResponseWriter, r *http.Request) {
	if s.options.Verifier == nil {
		web.Error(w, r, http.StatusServiceUnavailable, "external_disabled", "外部身份验证尚未配置")
		return
	}
	var request challengeVerifyRequest
	if !web.DecodeLimit(w, r, &request, 16*1024) {
		return
	}
	credentials := Credentials{
		School:       strings.TrimSpace(request.School),
		Account:      strings.TrimSpace(request.Account),
		Password:     request.Password,
		Verification: strings.TrimSpace(request.Verification),
	}
	if credentials.Account == "" || credentials.Password == "" {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "请填写学习通账号和密码")
		return
	}
	now := s.clock()
	identifier := credentials.School + "\x00" + credentials.Account
	blocked, retryAfter, err := s.rateLimitStatus(r.Context(), scopeUserLogin, identifier, now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if blocked {
		writeRateLimited(w, r, retryAfter)
		return
	}
	attempt, err := s.reserveChallengeAttempt(r.Context(), r.PathValue("id"), s.challengeBinding(r), now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	subject, err := s.options.Verifier.Verify(r.Context(), credentials)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			retryAfter, limitErr := s.addRateLimitFailure(r.Context(), scopeUserLogin, identifier, now)
			if limitErr != nil {
				web.Fail(w, r, limitErr)
				return
			}
			s.failChallenge(r.Context(), attempt.ID, attempt.Attempts, attempt.MaxAttempts)
			if retryAfter > 0 {
				writeRateLimited(w, r, retryAfter)
				return
			}
			web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "学习通身份验证未通过，请检查账号、密码或验证方式")
			return
		}
		// 已知平台失败映射为可读、不可无限重试的错误；未知错误统一 503，不泄露上游细节。
		if writeProviderFailure(w, r, err) {
			return
		}
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "外部身份验证暂不可用，请稍后重试")
		return
	}
	if err := subject.validate(now); err != nil {
		web.Fail(w, r, err)
		return
	}
	outcome, err := s.completeLogin(r.Context(), attempt, subject, now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	s.resetRateLimit(r.Context(), scopeUserLogin, identifier)
	s.clearChallengeCookie(w)
	verifiedAt := outcome.VerifiedAt.UTC()
	platformExpiry := subject.ExpiresAt.UTC()
	session := sessionView{
		Channel: outcome.Channel, ExpiresAt: outcome.Issued.ExpiresAt,
		AuthenticatedAt: now, Fresh: true,
	}
	view := &userView{
		ID: outcome.UserID, Status: "active", DisplayName: outcome.DisplayName, School: outcome.School,
		Provider: ProviderChaoxing, VerifiedAt: &verifiedAt, Session: session,
		Platform:    platformView{State: "active", VerifiedAt: &verifiedAt, ExpiresAt: &platformExpiry},
		Permissions: permissionsFor(KindUser, ""),
	}
	response := loginResponse{
		Mode:        outcome.Channel,
		ExpiresAt:   outcome.Issued.ExpiresAt,
		User:        view,
		Permissions: permissionsFor(KindUser, ""),
	}
	if outcome.Channel == ChannelApp {
		response.AccessToken = outcome.Issued.Token
		response.RefreshToken = outcome.Issued.RefreshToken
		response.TokenType = "Bearer"
	} else {
		response.CSRF = outcome.Issued.CSRF
		s.setSessionCookie(w, KindUser, outcome.Issued.Token, outcome.Issued.ExpiresAt)
	}
	w.Header().Set("Cache-Control", "no-store")
	web.JSON(w, r, http.StatusOK, response)
}
