package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// ProviderChaoxing 是当前唯一开放的外部身份提供者。
const ProviderChaoxing = "chaoxing"

// 外部身份服务端确认结果。Session 为适配器取得的平台会话材料，只在服务内部加密保存。
type VerifiedSubject struct {
	Provider    string
	Namespace   string
	Subject     string
	DisplayName string
	School      string
	Session     []byte
	ExpiresAt   time.Time
}

// Credentials 是用户本次提交的一次性凭据；服务不持久化任何字段，也不写日志。
type Credentials struct {
	School       string
	Account      string
	Password     string
	Verification string
}

// Verifier 由外部平台适配器实现。适配器必须自行完成真实会话验证，
// 并返回服务端确认的稳定主体；不得把用户名当作 Subject。
type Verifier interface {
	Verify(ctx context.Context, credentials Credentials) (VerifiedSubject, error)
}

// ErrInvalidCredentials 由适配器返回，表示持有人提交的凭据被平台拒绝。
// 其他错误一律按外部验证暂不可用处理，不累计登录失败次数。
var ErrInvalidCredentials = errors.New("auth: 外部凭据无效")

// ErrIdentityRace 是并发首次登录的内部重试信号，不对外暴露。
var errIdentityRace = errors.New("auth: 外部身份并发写入")

// sanitizeDisplay 去除控制字符并限制长度，展示字段不作为身份键。
func sanitizeDisplay(value string, limit int) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == '\r' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > limit {
		cleaned = cleaned[:limit]
	}
	return cleaned
}

// validate 校验适配器返回值；任何缺失都按外部验证暂不可用处理，不把异常当作已确认身份。
func (subject VerifiedSubject) validate(now time.Time) error {
	if subject.Provider != ProviderChaoxing {
		return fault.New("service_unavailable", "外部身份提供者未获批准，无法完成登录")
	}
	if strings.TrimSpace(subject.Namespace) == "" || len(subject.Namespace) > 190 {
		return fault.New("service_unavailable", "外部身份命名空间无效，无法完成登录")
	}
	if strings.TrimSpace(subject.Subject) == "" || len(subject.Subject) > 512 {
		return fault.New("service_unavailable", "外部身份主体无效，无法完成登录")
	}
	if len(subject.Session) == 0 || len(subject.Session) > 32*1024 {
		return fault.New("service_unavailable", "外部平台会话材料无效，无法完成登录")
	}
	if subject.ExpiresAt.IsZero() || !subject.ExpiresAt.After(now) {
		return fault.New("service_unavailable", "外部平台会话已过期，请重新验证")
	}
	return nil
}

// subjectIndex 以主密钥派生的 HMAC 作为查询索引，避免直接保存明文主体或裸哈希。
func (s *Service) subjectIndex(provider, namespace, subject string) string {
	mac := hmac.New(sha256.New, s.indexKey)
	_, _ = mac.Write([]byte("bbo:subject-index:v1"))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(provider))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(namespace))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(subject))
	return hex.EncodeToString(mac.Sum(nil))
}

// identityBinding 是内部用户与外部身份的归属结果。
type identityBinding struct {
	UserID      string
	IdentityID  string
	DisplayName string
	School      string
}

// bindIdentity 在事务中收敛内部用户归属。
//
// 唯一约束 (provider, namespace, subject_index) 负责收敛并发首次登录：
// 竞争失败的一侧整批回滚（不会留下孤立用户或第二个钱包），由调用方重试读取已提交身份。
func (s *Service) bindIdentity(ctx context.Context, tx pgx.Tx, subject VerifiedSubject, now time.Time) (identityBinding, error) {
	index := s.subjectIndex(subject.Provider, subject.Namespace, subject.Subject)
	displayName := sanitizeDisplay(subject.DisplayName, 128)
	school := sanitizeDisplay(subject.School, 190)

	var identityID, userID, status, userStatus string
	err := tx.QueryRow(ctx, `SELECT e.id::text, e.user_id::text, e.status, u.status
		FROM public.external_identities e
		JOIN public.users u ON u.id = e.user_id
		WHERE e.provider = $1 AND e.namespace = $2 AND e.subject_index = $3
		FOR UPDATE OF e`, subject.Provider, subject.Namespace, index).Scan(&identityID, &userID, &status, &userStatus)
	switch {
	case err == nil:
		if status != "active" {
			return identityBinding{}, fault.New("forbidden", "外部身份已冻结，请通过人工申诉处理")
		}
		if userStatus != "active" {
			return identityBinding{}, fault.New("forbidden", "账号状态受限，请通过人工申诉处理")
		}
		// 稳定主体未变时只刷新展示信息与验证时间，不改变归属。
		// 适配器没有返回展示字段时保留旧值，不用空值覆盖。
		if err := tx.QueryRow(ctx, `UPDATE public.external_identities
			SET display_name = coalesce(nullif($2, ''), display_name),
				school = coalesce(nullif($3, ''), school),
				verified_at = $4, updated_at = $4
			WHERE id = $1
			RETURNING coalesce(display_name, ''), coalesce(school, '')`,
			identityID, displayName, school, now).Scan(&displayName, &school); err != nil {
			return identityBinding{}, fault.New("service_unavailable", "无法更新外部身份，请稍后重试")
		}
		return identityBinding{UserID: userID, IdentityID: identityID, DisplayName: displayName, School: school}, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return identityBinding{}, fault.New("service_unavailable", "无法读取外部身份，请稍后重试")
	}

	userID = web.NewID()
	identityID = web.NewID()
	if !validUUID(userID) || !validUUID(identityID) {
		return identityBinding{}, fault.New("internal_error", "内部编号生成失败")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.users (id, status, created_at, updated_at)
		VALUES ($1, 'active', $2, $2)`, userID, now); err != nil {
		return identityBinding{}, fault.New("service_unavailable", "无法建立内部用户，请稍后重试")
	}
	ciphertext, version, err := s.seal(purposeSubject, []string{identityID, subject.Provider, subject.Namespace}, []byte(subject.Subject))
	if err != nil {
		return identityBinding{}, err
	}
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO public.external_identities
		(id, user_id, provider, namespace, subject_index, subject_ciphertext, subject_key_version,
		 display_name, school, status, verified_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, ''), nullif($9, ''), 'active', $10, $10, $10)
		ON CONFLICT (provider, namespace, subject_index) DO NOTHING
		RETURNING id::text`,
		identityID, userID, subject.Provider, subject.Namespace, index, ciphertext, version,
		displayName, school, now).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		// 另一事务先完成绑定；本次事务内创建的用户随回滚丢弃，调用方重试读取已提交身份。
		return identityBinding{}, errIdentityRace
	}
	if err != nil {
		return identityBinding{}, fault.New("service_unavailable", "无法建立外部身份，请稍后重试")
	}
	return identityBinding{UserID: userID, IdentityID: identityID, DisplayName: displayName, School: school}, nil
}

// savePlatformSession 在事务中保存加密的平台会话，并返回其编号。
func (s *Service) savePlatformSession(ctx context.Context, tx pgx.Tx, userID, identityID string, subject VerifiedSubject, now time.Time) (string, error) {
	sessionID := web.NewID()
	if !validUUID(sessionID) {
		return "", fault.New("internal_error", "内部编号生成失败")
	}
	ciphertext, version, err := s.seal(purposePlatformSession, []string{userID, identityID}, subject.Session)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.platform_sessions
		(id, user_id, external_identity_id, key_version, ciphertext, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)`,
		sessionID, userID, identityID, version, ciphertext, subject.ExpiresAt.UTC(), now); err != nil {
		return "", fault.New("service_unavailable", "无法保存平台会话，请稍后重试")
	}
	return sessionID, nil
}
