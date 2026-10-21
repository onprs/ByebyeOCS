package auth

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// ReencryptResult 汇总一次密钥重封装实际更新行数。
type ReencryptResult struct {
	ExternalIdentities int `json:"external_identities"`
	PlatformSessions   int `json:"platform_sessions"`
	AdminTOTP          int `json:"admin_totp"`
	Batches            int `json:"batches"`
}

// ReencryptSecrets 把外部主体、平台会话与管理员 TOTP 密文重封装到当前密钥版本。
//
// 约束：
//   - 每批最多 Options.ReencryptBatchSize 行，独立事务提交，可通过 ctx 取消；
//   - 只更新 ciphertext 与 key_version，不修改 subject_index、用户、钱包或学习身份指纹；
//   - 全部旧密钥仍在 EncryptionKeys 中时可安全重复执行，已迁移行会被跳过；
//   - 遇到未知版本密钥立即失败关闭，已提交批次保留；中断不会留下半写的密文/版本组合。
func (s *Service) ReencryptSecrets(ctx context.Context) (ReencryptResult, error) {
	result := ReencryptResult{}
	current := s.options.EncryptionKeyVersion
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var batch ReencryptResult
		err := s.inTx(ctx, func(tx pgx.Tx) error {
			count, err := s.reencryptSubjects(ctx, tx, current)
			if err != nil {
				return err
			}
			batch.ExternalIdentities = count
			count, err = s.reencryptPlatformSessions(ctx, tx, current)
			if err != nil {
				return err
			}
			batch.PlatformSessions = count
			count, err = s.reencryptAdminTOTP(ctx, tx, current)
			if err != nil {
				return err
			}
			batch.AdminTOTP = count
			return nil
		})
		if err != nil {
			return result, err
		}
		processed := batch.ExternalIdentities + batch.PlatformSessions + batch.AdminTOTP
		if processed == 0 {
			return result, nil
		}
		result.ExternalIdentities += batch.ExternalIdentities
		result.PlatformSessions += batch.PlatformSessions
		result.AdminTOTP += batch.AdminTOTP
		result.Batches++
	}
}

func (s *Service) reencryptSubjects(ctx context.Context, tx pgx.Tx, current int) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, provider, namespace, subject_key_version, subject_ciphertext
		FROM public.external_identities
		WHERE subject_key_version <> $1 AND subject_ciphertext IS NOT NULL
		ORDER BY id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, current, s.options.ReencryptBatchSize)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法读取待重封装的外部身份")
	}
	type item struct {
		id, provider, namespace string
		version                 int
		ciphertext              []byte
	}
	items := make([]item, 0, 8)
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.provider, &value.namespace, &value.version, &value.ciphertext); err != nil {
			rows.Close()
			return 0, fault.New("service_unavailable", "无法读取待重封装的外部身份")
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fault.New("service_unavailable", "无法读取待重封装的外部身份")
	}
	rows.Close()

	updated := 0
	for _, value := range items {
		parts := []string{value.id, value.provider, value.namespace}
		plaintext, err := s.open(purposeSubject, parts, value.version, value.ciphertext)
		if err != nil {
			return updated, err
		}
		sealed, version, err := s.seal(purposeSubject, parts, plaintext)
		if err != nil {
			return updated, err
		}
		tag, err := tx.Exec(ctx, `UPDATE public.external_identities
			SET subject_ciphertext = $2, subject_key_version = $3
			WHERE id = $1 AND subject_key_version = $4`, value.id, sealed, version, value.version)
		if err != nil {
			return updated, fault.New("service_unavailable", "无法更新外部身份密文")
		}
		updated += int(tag.RowsAffected())
	}
	return updated, nil
}

func (s *Service) reencryptPlatformSessions(ctx context.Context, tx pgx.Tx, current int) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, user_id::text, external_identity_id::text, key_version, ciphertext
		FROM public.platform_sessions
		WHERE key_version <> $1
		ORDER BY id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, current, s.options.ReencryptBatchSize)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法读取待重封装的平台会话")
	}
	type item struct {
		id, userID, identityID string
		version                int
		ciphertext             []byte
	}
	items := make([]item, 0, 8)
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.userID, &value.identityID, &value.version, &value.ciphertext); err != nil {
			rows.Close()
			return 0, fault.New("service_unavailable", "无法读取待重封装的平台会话")
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fault.New("service_unavailable", "无法读取待重封装的平台会话")
	}
	rows.Close()

	updated := 0
	for _, value := range items {
		parts := []string{value.userID, value.identityID}
		plaintext, err := s.open(purposePlatformSession, parts, value.version, value.ciphertext)
		if err != nil {
			return updated, err
		}
		sealed, version, err := s.seal(purposePlatformSession, parts, plaintext)
		if err != nil {
			return updated, err
		}
		tag, err := tx.Exec(ctx, `UPDATE public.platform_sessions
			SET ciphertext = $2, key_version = $3
			WHERE id = $1 AND key_version = $4`, value.id, sealed, version, value.version)
		if err != nil {
			return updated, fault.New("service_unavailable", "无法更新平台会话密文")
		}
		updated += int(tag.RowsAffected())
	}
	return updated, nil
}

func (s *Service) reencryptAdminTOTP(ctx context.Context, tx pgx.Tx, current int) (int, error) {
	rows, err := tx.Query(ctx, `SELECT id::text, totp_secret_key_version, totp_secret_ciphertext
		FROM public.admin_users
		WHERE totp_enabled = true AND totp_secret_ciphertext IS NOT NULL
			AND totp_secret_key_version IS NOT NULL AND totp_secret_key_version <> $1
		ORDER BY id
		LIMIT $2
		FOR UPDATE SKIP LOCKED`, current, s.options.ReencryptBatchSize)
	if err != nil {
		return 0, fault.New("service_unavailable", "无法读取待重封装的管理员密钥")
	}
	type item struct {
		id         string
		version    int
		ciphertext []byte
	}
	items := make([]item, 0, 8)
	for rows.Next() {
		var value item
		if err := rows.Scan(&value.id, &value.version, &value.ciphertext); err != nil {
			rows.Close()
			return 0, fault.New("service_unavailable", "无法读取待重封装的管理员密钥")
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fault.New("service_unavailable", "无法读取待重封装的管理员密钥")
	}
	rows.Close()

	updated := 0
	for _, value := range items {
		parts := []string{value.id}
		plaintext, err := s.open(purposeAdminTOTP, parts, value.version, value.ciphertext)
		if err != nil {
			return updated, err
		}
		sealed, version, err := s.seal(purposeAdminTOTP, parts, plaintext)
		if err != nil {
			return updated, err
		}
		tag, err := tx.Exec(ctx, `UPDATE public.admin_users
			SET totp_secret_ciphertext = $2, totp_secret_key_version = $3
			WHERE id = $1 AND totp_secret_key_version = $4`, value.id, sealed, version, value.version)
		if err != nil {
			return updated, fault.New("service_unavailable", "无法更新管理员密钥密文")
		}
		updated += int(tag.RowsAffected())
	}
	return updated, nil
}
