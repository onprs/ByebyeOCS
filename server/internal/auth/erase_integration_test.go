//go:build integration

package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestIntegrationEraseUserSecrets(t *testing.T) {
	harness := newHarness(t, nil)
	ctx := context.Background()
	cookie, _ := harness.userSession()
	// 建立偏好记录，确认清理范围覆盖 auth 自有非必要数据。
	requireStatus(t, harness.do(harness.user, http.MethodGet, "/api/v1/me/preferences", nil,
		withCookie(userSessionCookie, cookie)), http.StatusOK)

	var userID, identityID, subjectIndex string
	if err := harness.pool.QueryRow(ctx, `SELECT cs.user_id::text, e.id::text, e.subject_index
		FROM public.client_sessions cs
		JOIN public.external_identities e ON e.user_id = cs.user_id
		WHERE cs.token_hash = $1`, tokenDigest(cookie)).Scan(&userID, &identityID, &subjectIndex); err != nil {
		t.Fatal(err)
	}

	// 未关闭的账号拒绝清理，避免误删。
	err := harness.service.inTx(ctx, func(tx pgx.Tx) error {
		return EraseUserSecrets(ctx, tx, userID)
	})
	if err == nil {
		t.Fatal("未关闭账号仍允许清理认证敏感数据")
	}
	if code, _ := fault.Describe(err); code != "conflict" {
		t.Fatalf("未关闭账号返回 %s", code)
	}

	if err := harness.service.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE public.users SET status = 'closed' WHERE id = $1`, userID); err != nil {
			return err
		}
		if err := EraseUserSecrets(ctx, tx, userID); err != nil {
			return err
		}
		// 可重复执行，第二次不报错。
		return EraseUserSecrets(ctx, tx, userID)
	}); err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"client_sessions", "platform_sessions", "user_preferences", "login_challenges"} {
		if got := harness.count(`SELECT count(*) FROM public.`+table+` WHERE user_id = $1`, userID); got != 0 {
			t.Fatalf("%s 未清除：%d", table, got)
		}
	}
	var status, storedIndex string
	var ciphertext []byte
	var display, school *string
	if err := harness.pool.QueryRow(ctx, `SELECT status, subject_index, subject_ciphertext, display_name, school
		FROM public.external_identities WHERE id = $1`, identityID).
		Scan(&status, &storedIndex, &ciphertext, &display, &school); err != nil {
		t.Fatal(err)
	}
	if status != "revoked" || ciphertext != nil || display != nil || school != nil {
		t.Fatal("外部身份敏感材料未清除")
	}
	if storedIndex != subjectIndex {
		t.Fatal("清理修改了防复活的 subject_index")
	}
	var userStatus string
	if err := harness.pool.QueryRow(ctx, `SELECT status FROM public.users WHERE id = $1`, userID).Scan(&userStatus); err != nil {
		t.Fatal(err)
	}
	if userStatus != "closed" {
		t.Fatalf("用户行未保留关闭状态：%s", userStatus)
	}
	// 主体密文为 NULL 时重封装不得失败。
	if _, err := harness.service.ReencryptSecrets(ctx); err != nil {
		t.Fatalf("清理后重封装失败：%v", err)
	}

	// 关闭后同一外部主体不能重新登录。
	challengeID, binding := harness.createChallenge(ChannelWeb)
	forbidden := harness.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
	requireStatus(t, forbidden, http.StatusForbidden)
}
