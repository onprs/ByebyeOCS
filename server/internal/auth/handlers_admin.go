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

var errAdminNotFound = errors.New("auth: 管理员账号不存在")

type adminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type adminStepUpRequest struct {
	Password string `json:"password"`
	TOTPCode string `json:"totp_code"`
}

type adminCredential struct {
	ID             string
	Username       string
	DisplayName    string
	Role           string
	PasswordHash   string
	Disabled       bool
	TOTPEnabled    bool
	TOTPCiphertext []byte
	TOTPKeyVersion int
}

func (s *Service) loadAdminByUsername(ctx context.Context, username string) (adminCredential, error) {
	return s.scanAdminCredential(s.pool.QueryRow(ctx, `SELECT id::text, username, coalesce(display_name, ''), role,
			password_hash, disabled, totp_enabled, totp_secret_ciphertext, coalesce(totp_secret_key_version, 0)
		FROM public.admin_users WHERE username = $1`, username))
}

func (s *Service) loadAdminByID(ctx context.Context, id string) (adminCredential, error) {
	return s.scanAdminCredential(s.pool.QueryRow(ctx, `SELECT id::text, username, coalesce(display_name, ''), role,
			password_hash, disabled, totp_enabled, totp_secret_ciphertext, coalesce(totp_secret_key_version, 0)
		FROM public.admin_users WHERE id = $1`, id))
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *Service) scanAdminCredential(row rowScanner) (adminCredential, error) {
	var admin adminCredential
	err := row.Scan(&admin.ID, &admin.Username, &admin.DisplayName, &admin.Role, &admin.PasswordHash,
		&admin.Disabled, &admin.TOTPEnabled, &admin.TOTPCiphertext, &admin.TOTPKeyVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminCredential{}, errAdminNotFound
	}
	if err != nil {
		return adminCredential{}, fault.New("service_unavailable", "无法读取管理员账号")
	}
	return admin, nil
}

// verifyAdminTOTP 校验 TOTP 并在同一事务内推进计数器，拒绝重放。
func (s *Service) verifyAdminTOTP(ctx context.Context, tx pgx.Tx, admin adminCredential, code string, now time.Time) error {
	if !admin.TOTPEnabled || len(admin.TOTPCiphertext) == 0 || admin.TOTPKeyVersion == 0 {
		// 管理员登录必须启用第二因素；未启用的账号被拒绝而不是降级为单因素。
		return fault.New("forbidden", "管理员账号未启用第二因素，登录已拒绝")
	}
	secret, err := s.open(purposeAdminTOTP, []string{admin.ID}, admin.TOTPKeyVersion, admin.TOTPCiphertext)
	if err != nil {
		return err
	}
	decoded, err := decodeTOTPSecret(string(secret))
	if err != nil {
		return fault.New("forbidden", "管理员第二因素配置无效，请联系维护者")
	}
	counter, matched := matchTOTP(decoded, code, now, s.options.TOTP)
	if !matched {
		return fault.New("unauthenticated", "动态验证码不正确")
	}
	result, err := tx.Exec(ctx, `UPDATE public.admin_users
		SET totp_last_counter = $2, updated_at = $3
		WHERE id = $1 AND totp_last_counter < $2`, admin.ID, int64(counter), now)
	if err != nil {
		return fault.New("service_unavailable", "无法记录动态验证码使用情况")
	}
	if result.RowsAffected() == 0 {
		return fault.New("unauthenticated", "动态验证码已使用，请等待下一个验证码")
	}
	return nil
}

// handleAdminLogin 完成管理员独立认证：密码 + 必须启用的 TOTP。
func (s *Service) handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	var request adminLoginRequest
	if !web.DecodeLimit(w, r, &request, 8*1024) {
		return
	}
	username := strings.TrimSpace(request.Username)
	code := strings.TrimSpace(request.TOTPCode)
	now := s.clock()
	if username == "" || request.Password == "" || code == "" {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "请输入管理员账号、密码和动态验证码")
		return
	}
	blocked, retryAfter, err := s.rateLimitStatus(r.Context(), scopeAdminLogin, username, now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if blocked {
		writeRateLimited(w, r, retryAfter)
		return
	}
	admin, err := s.loadAdminByUsername(r.Context(), username)
	if errors.Is(err, errAdminNotFound) {
		if !s.verifyUnknownAccountWithSlot(w, r, request.Password) {
			return
		}
		s.rejectAdminLogin(w, r, username, now)
		return
	}
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if admin.Disabled {
		if !s.verifyUnknownAccountWithSlot(w, r, request.Password) {
			return
		}
		s.rejectAdminLogin(w, r, username, now)
		return
	}
	if !roleExists(admin.Role) {
		// 角色被写成未知值时拒绝登录，不静默降级或当作 owner。
		web.Error(w, r, http.StatusForbidden, "forbidden", "管理员角色配置无效，请联系维护者")
		return
	}
	permissions := permissionsFor(KindAdmin, admin.Role)
	matched, ok := s.verifyAdminPasswordWithSlot(w, r, admin.PasswordHash, request.Password)
	if !ok {
		return
	}
	if !matched {
		s.rejectAdminLogin(w, r, username, now)
		return
	}
	var issued issuedCredential
	err = s.inTx(r.Context(), func(tx pgx.Tx) error {
		if err := s.verifyAdminTOTP(r.Context(), tx, admin, code, now); err != nil {
			return err
		}
		created, err := s.createAdminSession(r.Context(), tx, admin.ID, now)
		if err != nil {
			return err
		}
		issued = created
		event := audit.Event{
			Actor:        web.Actor{ID: admin.ID, Kind: KindAdmin, SessionID: created.SessionID, Permissions: permissions},
			Action:       "admin.login",
			ResourceType: "admin_session",
			ResourceID:   created.SessionID,
			RequestID:    requestID(r.Context()),
		}
		if err := audit.Record(r.Context(), tx, event); err != nil {
			return fault.New("service_unavailable", "无法记录登录审计")
		}
		return nil
	})
	if err != nil {
		codeName, message := fault.Describe(err)
		switch codeName {
		case "unauthenticated":
			s.rejectAdminLoginWithMessage(w, r, username, now, message)
		case "forbidden":
			web.Error(w, r, http.StatusForbidden, "forbidden", message)
		default:
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", message)
		}
		return
	}
	s.resetRateLimit(r.Context(), scopeAdminLogin, username)
	s.setSessionCookie(w, KindAdmin, issued.Token, issued.ExpiresAt)
	web.JSON(w, r, http.StatusOK, loginResponse{
		Mode:        ChannelWeb,
		CSRF:        issued.CSRF,
		ExpiresAt:   issued.ExpiresAt,
		Permissions: permissions,
		Admin: &adminView{
			ID: admin.ID, Username: admin.Username, DisplayName: admin.DisplayName, Role: admin.Role,
			TOTPEnabled: admin.TOTPEnabled,
			Session:     sessionView{Channel: ChannelWeb, ExpiresAt: issued.ExpiresAt, AuthenticatedAt: now, Fresh: true},
			Permissions: permissions,
		},
	})
}

// verifyAdminPasswordWithSlot 在全局有界并发内执行 Argon2id 校验。
// 名额饱和时直接返回 429/Retry-After，不排队；校验参数错误按服务异常处理。
func (s *Service) verifyAdminPasswordWithSlot(w http.ResponseWriter, r *http.Request, hash, password string) (matched, ok bool) {
	release, acquired := s.acquirePasswordSlot()
	if !acquired {
		writeRetryAfter(w, r, passwordBusyRetryAfter, "登录验证繁忙，请稍后再试")
		return false, false
	}
	defer release()
	result, err := verifyPassword(hash, password)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "管理员账号配置异常，请联系维护者")
		return false, false
	}
	return result, true
}

// verifyUnknownAccountWithSlot 对不存在的管理员执行同等成本的占位校验，同样受全局并发限制。
func (s *Service) verifyUnknownAccountWithSlot(w http.ResponseWriter, r *http.Request, password string) bool {
	release, acquired := s.acquirePasswordSlot()
	if !acquired {
		writeRetryAfter(w, r, passwordBusyRetryAfter, "登录验证繁忙，请稍后再试")
		return false
	}
	defer release()
	verifyUnknownAccount(password)
	return true
}

// rejectAdminLogin 记录失败并按限流状态返回统一错误，避免区分账号是否存在。
func (s *Service) rejectAdminLogin(w http.ResponseWriter, r *http.Request, username string, now time.Time) {
	s.rejectAdminLoginWithMessage(w, r, username, now, "管理员账号或凭据不正确")
}

func (s *Service) rejectAdminLoginWithMessage(w http.ResponseWriter, r *http.Request, username string, now time.Time, message string) {
	retryAfter, err := s.addRateLimitFailure(r.Context(), scopeAdminLogin, username, now)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "登录暂时不可用，请稍后重试")
		return
	}
	if retryAfter > 0 {
		writeRateLimited(w, r, retryAfter)
		return
	}
	web.Error(w, r, http.StatusUnauthorized, "unauthenticated", message)
}

// handleAdminStepUp 在现有会话上提升新鲜验证，供敏感操作使用。
func (s *Service) handleAdminStepUp(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, KindAdmin, PermissionAdminSelf)
	if !ok {
		return
	}
	state, ok := sessionFrom(r.Context())
	if !ok {
		web.Error(w, r, http.StatusUnauthorized, "unauthenticated", "请先登录")
		return
	}
	var request adminStepUpRequest
	if !web.DecodeLimit(w, r, &request, 8*1024) {
		return
	}
	code := strings.TrimSpace(request.TOTPCode)
	if request.Password == "" || code == "" {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "请输入密码和动态验证码")
		return
	}
	now := s.clock()
	identifier := "step-up:" + actor.ID
	blocked, retryAfter, err := s.rateLimitStatus(r.Context(), scopeAdminLogin, identifier, now)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if blocked {
		writeRateLimited(w, r, retryAfter)
		return
	}
	admin, err := s.loadAdminByID(r.Context(), actor.ID)
	if err != nil {
		s.rejectAdminLoginWithMessage(w, r, identifier, now, "密码或动态验证码不正确")
		return
	}
	matched, ok := s.verifyAdminPasswordWithSlot(w, r, admin.PasswordHash, request.Password)
	if !ok {
		return
	}
	if !matched {
		s.rejectAdminLoginWithMessage(w, r, identifier, now, "密码或动态验证码不正确")
		return
	}
	err = s.inTx(r.Context(), func(tx pgx.Tx) error {
		if err := s.verifyAdminTOTP(r.Context(), tx, admin, code, now); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `UPDATE public.admin_sessions
			SET authenticated_at = $2, step_up_at = $2, updated_at = $2
			WHERE id = $1 AND status = 'active'`, state.ID, now); err != nil {
			return fault.New("service_unavailable", "无法更新会话验证时间")
		}
		event := audit.Event{
			Actor:        actor,
			Action:       "admin.step_up",
			ResourceType: "admin_session",
			ResourceID:   state.ID,
			RequestID:    requestID(r.Context()),
		}
		if err := audit.Record(r.Context(), tx, event); err != nil {
			return fault.New("service_unavailable", "无法记录验证审计")
		}
		return nil
	})
	if err != nil {
		codeName, message := fault.Describe(err)
		switch codeName {
		case "unauthenticated":
			s.rejectAdminLoginWithMessage(w, r, identifier, now, message)
		case "forbidden":
			web.Error(w, r, http.StatusForbidden, "forbidden", message)
		default:
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", message)
		}
		return
	}
	s.resetRateLimit(r.Context(), scopeAdminLogin, identifier)
	web.JSON(w, r, http.StatusOK, map[string]any{
		"authenticated_at": now,
		"fresh_until":      now.Add(s.options.FreshWindow),
		"csrf_token":       s.csrfToken(state.ID),
	})
}
