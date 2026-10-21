package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// EraseUserSecrets 在调用方事务内清除认证模块持有的敏感材料。
//
// 前置条件：同一事务已把 users.status 置为 closed（本函数会锁定并再次校验）。
// 只处理 auth 拥有的表：客户端会话与刷新链、平台会话密文、登录挑战、偏好，
// 以及外部身份密文与展示信息。保留 users 行、subject_index、账本与审计记录。
// 可重复执行；不删除或改写财务/审计数据，不尝试恢复授权。
func EraseUserSecrets(ctx context.Context, tx pgx.Tx, userID string) error {
	if tx == nil {
		return fault.New("internal_error", "清理用户敏感数据要求显式事务")
	}
	if !validUUID(userID) {
		return fault.New("validation_failed", "用户编号无效")
	}
	var status string
	if err := tx.QueryRow(ctx, `SELECT status FROM public.users WHERE id = $1 FOR UPDATE`, userID).Scan(&status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.New("not_found", "用户不存在")
		}
		return fault.New("service_unavailable", "无法锁定用户状态")
	}
	if status != "closed" {
		return fault.New("conflict", "用户尚未关闭，拒绝清理认证敏感数据")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.client_sessions WHERE user_id = $1`, userID); err != nil {
		return fault.New("service_unavailable", "无法清除客户端会话")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.platform_sessions WHERE user_id = $1`, userID); err != nil {
		return fault.New("service_unavailable", "无法清除平台会话")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.login_challenges WHERE user_id = $1`, userID); err != nil {
		return fault.New("service_unavailable", "无法清除登录挑战")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM public.user_preferences WHERE user_id = $1`, userID); err != nil {
		return fault.New("service_unavailable", "无法清除偏好")
	}
	// 保留 subject_index（HMAC 索引）以阻止同一外部主体重新注册；清除可还原主体的密文与展示信息。
	if _, err := tx.Exec(ctx, `UPDATE public.external_identities
		SET subject_ciphertext = NULL, display_name = NULL, school = NULL,
		    status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now()
		WHERE user_id = $1`, userID); err != nil {
		return fault.New("service_unavailable", "无法清除外部身份材料")
	}
	return nil
}
