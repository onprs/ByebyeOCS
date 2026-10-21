package auth

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// CreateAdmin 建立管理员账号，供维护 CLI 显式调用；服务启动不会创建默认账号。
// 默认初始角色为 owner，保持既有维护命令兼容。
func (s *Service) CreateAdmin(ctx context.Context, username, password, totpSecret string) error {
	return s.CreateAdminWithRole(ctx, username, password, totpSecret, RoleOwner)
}

// CreateAdminWithRole 以指定标准角色建立管理员账号。
//
// totpSecret 为空时使用 Options.Enrollment 一次性交付新生成的密钥；
// 未配置交付通道则返回错误，而不是把密钥写入日志或数据库明文。
func (s *Service) CreateAdminWithRole(ctx context.Context, username, password, totpSecret, role string) error {
	if err := validateRole(role); err != nil {
		return err
	}
	name := strings.TrimSpace(username)
	if err := validateAdminUsername(name); err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	secret := strings.TrimSpace(totpSecret)
	generated := false
	if secret == "" {
		if s.options.Enrollment == nil {
			return fault.New("totp_secret_required", "创建管理员需要提供 TOTP 密钥，或配置一次性安全交付通道")
		}
		generatedSecret, err := GenerateTOTPSecret()
		if err != nil {
			return err
		}
		secret = generatedSecret
		generated = true
	}
	normalized, err := normalizeTOTPSecret(secret)
	if err != nil {
		return err
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return err
	}
	adminID := web.NewID()
	if !validUUID(adminID) {
		return fault.New("internal_error", "内部编号生成失败")
	}
	ciphertext, version, err := s.seal(purposeAdminTOTP, []string{adminID}, []byte(normalized))
	if err != nil {
		return err
	}
	now := s.clock()
	// 维护命令没有 HTTP 请求上下文；审计记录需要合法请求编号。
	if web.RequestID(ctx) == "" {
		ctx = web.WithRequestID(ctx, web.NewRequestID())
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO public.admin_users
			(id, username, password_hash, role, totp_secret_ciphertext, totp_secret_key_version,
			 totp_enabled, totp_confirmed_at, password_updated_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, true, $7, $7, $7, $7)`,
			adminID, name, passwordHash, role, ciphertext, version, now); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return fault.New("conflict", "管理员账号已存在")
			}
			return fault.New("service_unavailable", "无法创建管理员账号")
		}
		return audit.Record(ctx, tx, audit.Event{
			Actor:        web.Actor{Kind: "system"},
			Action:       "admin.create",
			ResourceType: "admin_user",
			ResourceID:   adminID,
			Reason:       "初始角色 " + role,
			RequestID:    web.RequestID(ctx),
		})
	})
	if err != nil {
		return err
	}
	if generated {
		if deliverErr := s.options.Enrollment(name, normalized); deliverErr != nil {
			// 唯一一次密钥交付失败时删除刚建立的账号，避免留下无法登录的管理员。
			_, _ = s.pool.Exec(context.WithoutCancel(ctx), `DELETE FROM public.admin_users WHERE id = $1`, adminID)
			return fault.New("totp_delivery_failed", "TOTP 密钥交付失败，管理员账号未保留")
		}
	}
	return nil
}

func validateAdminUsername(name string) error {
	if name == "" || len(name) > 128 {
		return fault.New("validation_failed", "管理员账号长度必须在 1 到 128 个字符之间")
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fault.New("validation_failed", "管理员账号不能包含空白或控制字符")
		}
	}
	return nil
}
