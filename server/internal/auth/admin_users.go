package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// adminUserView 是管理员账号列表/角色变更响应的安全字段集合。
type adminUserView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name,omitempty"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled"`
	TOTPEnabled bool      `json:"totp_enabled"`
	Version     int64     `json:"version"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type adminUserRecord struct {
	ID          string
	Username    string
	DisplayName string
	Role        string
	Disabled    bool
	TOTPEnabled bool
	Version     int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (record adminUserRecord) view() adminUserView {
	return adminUserView{
		ID: record.ID, Username: record.Username, DisplayName: record.DisplayName,
		Role: record.Role, Disabled: record.Disabled, TOTPEnabled: record.TOTPEnabled,
		Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

const adminUserColumns = `id::text, username, coalesce(display_name, ''), role, disabled, totp_enabled, version, created_at, updated_at`

func scanAdminUserRow(row rowScanner) (adminUserRecord, error) {
	var record adminUserRecord
	err := row.Scan(&record.ID, &record.Username, &record.DisplayName, &record.Role,
		&record.Disabled, &record.TOTPEnabled, &record.Version, &record.CreatedAt, &record.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return adminUserRecord{}, fault.New("not_found", "管理员账号不存在")
	}
	if err != nil {
		return adminUserRecord{}, fault.New("service_unavailable", "无法读取管理员账号")
	}
	return record, nil
}

// requireOwnerFresh 要求 owner 权限且会话完成近期第二因素验证。
func (s *Service) requireOwnerFresh(w http.ResponseWriter, r *http.Request) (web.Actor, bool) {
	actor, ok := web.Require(w, r, KindAdmin, PermissionAdminUsers)
	if !ok {
		return web.Actor{}, false
	}
	if actor.AuthenticatedAt.IsZero() || s.clock().Sub(actor.AuthenticatedAt) > s.options.FreshWindow {
		// 管理端前端按 step_up_required 打开二次验证对话框；与隐私复核等
		// 其他管理员敏感操作保持同一错误码，不能只返回泛化的 reauth_required。
		web.Error(w, r, http.StatusForbidden, "step_up_required", "此操作需要重新完成管理员二次验证")
		return web.Actor{}, false
	}
	return actor, true
}

// handleListAdminUsers 供 owner 查看管理员、角色和角色权限；仅 owner 且新鲜验证可读。
func (s *Service) handleListAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireOwnerFresh(w, r); !ok {
		return
	}
	rows, err := s.pool.Query(r.Context(), `SELECT `+adminUserColumns+`
		FROM public.admin_users ORDER BY created_at, id LIMIT 200`)
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取管理员列表")
		return
	}
	defer rows.Close()
	users := make([]adminUserView, 0, 8)
	for rows.Next() {
		record, err := scanAdminUserRow(rows)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
		users = append(users, record.view())
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取管理员列表")
		return
	}
	permissions := make(map[string][]string, len(roleNames))
	for _, role := range roleNames {
		permissions[role] = permissionsForRole(role)
	}
	web.JSON(w, r, http.StatusOK, map[string]any{
		"admin_users":      users,
		"roles":            roleNames,
		"role_permissions": permissions,
	})
}

type adminRoleChangeRequest struct {
	Role   string `json:"role"`
	Reason string `json:"reason"`
}

type adminRoleChangeResponse struct {
	AdminUser             adminUserView `json:"admin_user"`
	SessionsRevoked       int           `json:"sessions_revoked"`
	CurrentSessionRevoked bool          `json:"current_session_revoked"`
}

// handleChangeAdminRole 变更管理员角色；只接受 role/reason 字段，权限数组由服务端角色表决定。
func (s *Service) handleChangeAdminRole(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireOwnerFresh(w, r)
	if !ok {
		return
	}
	var request adminRoleChangeRequest
	if !web.DecodeLimit(w, r, &request, 8*1024) {
		return
	}
	role := strings.TrimSpace(request.Role)
	reason, err := validateAdminReason(request.Reason)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if err := validateRole(role); err != nil {
		web.Fail(w, r, err)
		return
	}
	expected, err := web.MatchVersion(r)
	if err != nil {
		web.Error(w, r, http.StatusPreconditionRequired, "precondition_required", "修改角色需要 If-Match 版本条件")
		return
	}
	target := r.PathValue("id")
	if !validUUID(target) {
		web.Error(w, r, http.StatusNotFound, "not_found", "管理员账号不存在")
		return
	}
	now := s.clock()
	response := adminRoleChangeResponse{}
	err = s.inTx(r.Context(), func(tx pgx.Tx) error {
		// 串行化角色变更，避免并发降级同时通过“最后 owner”检查或产生行锁死锁。
		if _, err := tx.Exec(r.Context(), "SELECT pg_advisory_xact_lock(726239184002)"); err != nil {
			return fault.New("service_unavailable", "无法获取角色变更锁")
		}
		current, err := scanAdminUserRow(tx.QueryRow(r.Context(), `SELECT `+adminUserColumns+`
			FROM public.admin_users WHERE id = $1 FOR UPDATE`, target))
		if err != nil {
			return err
		}
		if current.Version != expected {
			return fault.New("version_conflict", "管理员信息已更新，请重新加载后再修改")
		}
		response.AdminUser = current.view()
		if current.Role == role {
			// 角色未变化时不撤销会话、不写审计，保持幂等。
			return nil
		}
		previousRole := current.Role
		if current.Role == RoleOwner && !current.Disabled && role != RoleOwner {
			owners, err := lockActiveOwners(r.Context(), tx)
			if err != nil {
				return err
			}
			if owners <= 1 {
				return fault.New("conflict", "不能降级最后一个有效 owner")
			}
		}
		var version int64
		if err := tx.QueryRow(r.Context(), `UPDATE public.admin_users
			SET role = $2, version = version + 1, updated_at = $3
			WHERE id = $1 AND version = $4
			RETURNING version`, target, role, now, expected).Scan(&version); err != nil {
			return fault.New("version_conflict", "管理员信息已更新，请重新加载后再修改")
		}
		current.Role = role
		current.Version = version
		current.UpdatedAt = now
		response.AdminUser = current.view()
		revoked, err := revokeAdminSessionsFor(r.Context(), tx, target, "role_changed", now)
		if err != nil {
			return err
		}
		response.SessionsRevoked = revoked
		response.CurrentSessionRevoked = target == actor.ID
		return audit.Record(r.Context(), tx, audit.Event{
			Actor:        actor,
			Action:       "admin.role_changed",
			ResourceType: "admin_user",
			ResourceID:   target,
			Reason:       fmt.Sprintf("角色 %s → %s：%s", previousRole, role, reason),
			RequestID:    requestID(r.Context()),
		})
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if response.CurrentSessionRevoked {
		s.clearSessionCookie(w, KindAdmin)
	}
	web.ETag(w, response.AdminUser.Version)
	web.JSON(w, r, http.StatusOK, response)
}

// lockActiveOwners 锁定所有有效 owner 行并返回数量，串行化并发降级。
func lockActiveOwners(ctx context.Context, tx pgx.Tx) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id::text FROM public.admin_users
		WHERE role = 'owner' AND disabled = false FOR UPDATE`)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法检查 owner 数量")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fault.New("service_unavailable", "无法检查 owner 数量")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fault.New("service_unavailable", "无法检查 owner 数量")
	}
	return count, nil
}

func revokeAdminSessionsFor(ctx context.Context, tx pgx.Tx, adminID, reason string, now time.Time) (int, error) {
	result, err := tx.Exec(ctx, `UPDATE public.admin_sessions
		SET status = 'revoked', revoked_at = $2, revoke_reason = $3, updated_at = $2
		WHERE admin_id = $1 AND status = 'active'`, adminID, now, reason)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法撤销管理员会话")
	}
	return int(result.RowsAffected()), nil
}

type adminSessionsRevokeRequest struct {
	Reason string `json:"reason"`
}

// handleRevokeAdminSessions 由 owner 撤销指定管理员的全部会话。
func (s *Service) handleRevokeAdminSessions(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requireOwnerFresh(w, r)
	if !ok {
		return
	}
	var request adminSessionsRevokeRequest
	if !web.DecodeLimit(w, r, &request, 8*1024) {
		return
	}
	reason, err := validateAdminReason(request.Reason)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	target := r.PathValue("id")
	if !validUUID(target) {
		web.Error(w, r, http.StatusNotFound, "not_found", "管理员账号不存在")
		return
	}
	now := s.clock()
	revoked := 0
	err = s.inTx(r.Context(), func(tx pgx.Tx) error {
		var exists string
		if err := tx.QueryRow(r.Context(), `SELECT id::text FROM public.admin_users WHERE id = $1 FOR UPDATE`, target).Scan(&exists); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fault.New("not_found", "管理员账号不存在")
			}
			return fault.New("service_unavailable", "无法读取管理员账号")
		}
		count, err := revokeAdminSessionsFor(r.Context(), tx, target, "admin_revoked", now)
		if err != nil {
			return err
		}
		revoked = count
		return audit.Record(r.Context(), tx, audit.Event{
			Actor:        actor,
			Action:       "admin.sessions_revoked",
			ResourceType: "admin_user",
			ResourceID:   target,
			Reason:       reason,
			RequestID:    requestID(r.Context()),
		})
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	current := target == actor.ID
	if current {
		s.clearSessionCookie(w, KindAdmin)
	}
	web.JSON(w, r, http.StatusOK, map[string]any{
		"revoked_sessions":        revoked,
		"current_session_revoked": current,
	})
}

// validateAdminReason 要求 4–500 个字符的操作理由，避免审计为空或超限。
func validateAdminReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return "", fault.New("validation_failed", "请填写操作理由")
	}
	count := utf8.RuneCountInString(trimmed)
	if count < 4 {
		return "", fault.New("validation_failed", "操作理由至少需要 4 个字符")
	}
	if count > 500 {
		return "", fault.New("validation_failed", "操作理由不能超过 500 个字符")
	}
	return trimmed, nil
}
