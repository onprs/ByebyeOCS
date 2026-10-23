//go:build integration

package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// 长中文展示字段必须按字符边界截断后写入：按字节截断会产生无效 UTF-8，
// PostgreSQL 拒绝写入，导致使用长姓名/学校名的用户无法登录。
func TestIntegrationLongChineseDisplayFieldsStayValidUTF8(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	longSchool := strings.Repeat("示例实验学校", 60) // 360 字符、1080 字节，超过 190 字符上限
	id, binding := h.createChallenge(ChannelWeb)
	response := h.do(h.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges/"+id+"/verify",
		map[string]any{"school": longSchool, "account": testAccount, "password": testUserPassword},
		withOrigin("http://user.test"), withHeader(challengeHeader, binding),
		withCookie(challengeCookie, binding))
	requireStatus(t, response, http.StatusOK)

	var storedSchool string
	if err := h.pool.QueryRow(ctx, `SELECT coalesce(school, '') FROM public.external_identities`).
		Scan(&storedSchool); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(storedSchool) {
		t.Fatalf("学校名包含无效 UTF-8: %q", storedSchool)
	}
	if runes := utf8.RuneCountInString(storedSchool); runes != 190 {
		t.Fatalf("学校名截断为 %d 字符，期望 190", runes)
	}
	if !strings.HasPrefix(longSchool, storedSchool) {
		t.Fatalf("学校名截断改变了前缀内容: %q", storedSchool)
	}
	view, _ := response.json(t)["user"].(map[string]any)
	if view["school"] != storedSchool {
		t.Fatalf("登录响应返回了未截断的学校名: %v", view["school"])
	}
}

// 身份归属只由 provider/namespace/subject 决定；展示字段按字符上限截断，
// 同一稳定主体重复登录仍归属同一用户。
func TestIntegrationBindIdentityTruncatesDisplayFieldsOnRuneBoundary(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	longName := strings.Repeat("欧阳娜娜", 80)   // 320 字符
	longSchool := strings.Repeat("实验学校", 80) // 320 字符
	subject := VerifiedSubject{
		Provider:    ProviderChaoxing,
		Namespace:   testNamespace,
		Subject:     "uid-long-display",
		DisplayName: longName,
		School:      longSchool,
		Session:     []byte(testPlatformBody),
		ExpiresAt:   h.clock.Now().Add(time.Hour),
	}
	var first identityBinding
	if err := h.service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		first, err = h.service.bindIdentity(ctx, tx, subject, h.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(first.DisplayName) || !utf8.ValidString(first.School) {
		t.Fatal("绑定结果包含无效 UTF-8")
	}
	if runes := utf8.RuneCountInString(first.DisplayName); runes != 128 {
		t.Fatalf("显示名截断为 %d 字符，期望 128", runes)
	}
	if runes := utf8.RuneCountInString(first.School); runes != 190 {
		t.Fatalf("学校名截断为 %d 字符，期望 190", runes)
	}
	if !strings.HasPrefix(longName, first.DisplayName) || !strings.HasPrefix(longSchool, first.School) {
		t.Fatal("展示字段截断改变了前缀内容")
	}

	h.clock.Advance(time.Minute)
	longerName := strings.Repeat("欧阳娜娜", 100)
	subject.DisplayName = longerName
	var second identityBinding
	if err := h.service.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		second, err = h.service.bindIdentity(ctx, tx, subject, h.clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if second.UserID != first.UserID || second.IdentityID != first.IdentityID {
		t.Fatalf("展示字段变化改变了稳定主体归属: %+v / %+v", first, second)
	}
	if runes := utf8.RuneCountInString(second.DisplayName); runes != 128 {
		t.Fatalf("更新后显示名截断为 %d 字符，期望 128", runes)
	}
}
