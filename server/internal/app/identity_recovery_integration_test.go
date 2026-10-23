//go:build integration

package app

import (
	"context"
	"testing"
	"time"
)

func TestFreshLoginRecoversOnlyExpiredIdentityBlock(t *testing.T) {
	for _, item := range []struct {
		name, code, expected string
		cooldown             bool
		syncStatus           int
	}{
		{"失效身份", "identity_invalid", "", false, 202},
		{"保留冷却", "identity_invalid", "", true, 409},
		{"管理员限制", "admin_blocked", "admin_blocked", false, 409},
		{"平台交互验证", "verification_required", "verification_required", false, 409},
		{"权限拒绝", "forbidden", "forbidden", false, 409},
	} {
		t.Run(item.name, func(t *testing.T) {
			fixture := newRuntimeFixture(t)
			client := fixtureClient()
			defer client.CloseIdleConnections()
			_, userID := loginFixtureUser(t, fixture, client, "learner-a")
			var until *time.Time
			if item.cooldown {
				value := time.Now().UTC().Add(5 * time.Minute)
				until = &value
			}
			if _, err := fixture.runtime.db.Pool().Exec(context.Background(), `UPDATE public.learning_accounts
				SET blocked_code=$2, blocked_reason='受控身份恢复测试', blocked_at=now()-interval '1 minute', blocked_until=$3
				WHERE user_id=$1`, userID, item.code, until); err != nil {
				t.Fatal(err)
			}
			refreshed := fixtureClient()
			defer refreshed.CloseIdleConnections()
			csrf, sameUser := loginFixtureUser(t, fixture, refreshed, "learner-a")
			if sameUser != userID {
				t.Fatal("重新验证创建了第二个用户")
			}
			var code string
			var remaining *time.Time
			if err := fixture.runtime.db.Pool().QueryRow(context.Background(), `SELECT blocked_code,blocked_until FROM public.learning_accounts WHERE user_id=$1`, userID).Scan(&code, &remaining); err != nil {
				t.Fatal(err)
			}
			if code != item.expected || (until != nil && (remaining == nil || remaining.Before(until.Add(-time.Millisecond)))) {
				t.Fatalf("登录后账号限制错误：code=%q expected=%q cooldown=%v", code, item.expected, remaining)
			}
			status, _, _ := requestJSON(t, refreshed, "POST", fixture.user.URL+"/api/v1/course-syncs", csrf, map[string]any{}, nil)
			if status != item.syncStatus {
				t.Fatalf("正常重新验证后的同步入口不可用：HTTP %d，期望%d", status, item.syncStatus)
			}
		})
	}
}
