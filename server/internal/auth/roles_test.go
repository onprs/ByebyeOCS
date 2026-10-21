package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// businessAdminPermissions 是从 learning、ledger、questions、operations、events
// 实际 web.Require 字面量整理出的权限集合；新增业务权限时必须同步角色表。
var businessAdminPermissions = []string{
	"learning.read", "learning.tasks.operate", "learning.accounts.operate",
	"billing.read", "billing.manage", "wallet.adjust", "wallet.refund", "reconciliation.manage",
	"question:read", "question:manage",
	"operations.read", "audit.read", "privacy.review",
	"events.read",
}

func permissionSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func TestRolePermissionMapping(t *testing.T) {
	if !samePermissions(permissionsForRole(RoleOwner), []string{PermissionOwner}) {
		t.Fatalf("owner 权限应为唯一的 *：%v", permissionsForRole(RoleOwner))
	}
	known := permissionSet(businessAdminPermissions)
	known[PermissionAdminSelf] = struct{}{}
	known[PermissionAdminUsers] = struct{}{}
	for _, role := range []string{RoleOperator, RoleSupport, RoleFinance, RoleObserver} {
		permissions := permissionsForRole(role)
		if len(permissions) == 0 {
			t.Fatalf("角色 %s 没有任何权限", role)
		}
		set := permissionSet(permissions)
		if _, ok := set[PermissionOwner]; ok {
			t.Fatalf("非 owner 角色 %s 获得了通配权限", role)
		}
		if _, ok := set[PermissionAdminSelf]; !ok {
			t.Fatalf("角色 %s 缺少自助权限", role)
		}
		if _, ok := set[PermissionAdminUsers]; ok {
			t.Fatalf("角色 %s 不应拥有管理员管理权限", role)
		}
		for _, permission := range permissions {
			if _, ok := known[permission]; !ok {
				t.Fatalf("角色 %s 定义了业务服务未使用的权限 %q", role, permission)
			}
		}
	}

	operator := permissionSet(permissionsForRole(RoleOperator))
	if _, ok := operator["learning.tasks.operate"]; !ok {
		t.Fatal("operator 缺少学习任务操作权限")
	}
	if _, ok := operator["wallet.adjust"]; ok {
		t.Fatal("operator 不应拥有资金调整权限")
	}
	finance := permissionSet(permissionsForRole(RoleFinance))
	if _, ok := finance["wallet.adjust"]; !ok {
		t.Fatal("finance 缺少资金调整权限")
	}
	if _, ok := finance["learning.tasks.operate"]; ok {
		t.Fatal("finance 不应拥有学习任务操作权限")
	}
	support := permissionSet(permissionsForRole(RoleSupport))
	if _, ok := support["privacy.review"]; !ok {
		t.Fatal("support 缺少隐私复核权限")
	}
	observer := permissionSet(permissionsForRole(RoleObserver))
	for _, write := range []string{"learning.tasks.operate", "wallet.adjust", "wallet.refund", "question:manage", "billing.manage", "privacy.review"} {
		if _, ok := observer[write]; ok {
			t.Fatalf("observer 不应拥有写权限 %q", write)
		}
	}
	if _, ok := observer["audit.read"]; !ok {
		t.Fatal("observer 应能只读审计")
	}
	if permissions := permissionsForRole("unknown"); len(permissions) != 0 {
		t.Fatal("未知角色应返回空权限")
	}
}

func TestPermissionForSelfService(t *testing.T) {
	if permissionFor(KindAdmin) != PermissionAdminSelf || permissionFor(KindUser) != PermissionSelf {
		t.Fatal("自助权限字符串不符合预期")
	}
	observer := web.Actor{Kind: KindAdmin, ID: "x", Permissions: permissionsFor(KindAdmin, RoleObserver)}
	if !web.HasPermission(observer, PermissionAdminSelf) {
		t.Fatal("observer 无法访问管理端自助接口")
	}
	if web.HasPermission(observer, PermissionAdminUsers) {
		t.Fatal("observer 获得了管理员管理权限")
	}
	if !samePermissions(permissionsFor(KindUser, ""), []string{PermissionSelf}) {
		t.Fatal("用户端权限不应来自管理员角色")
	}
}

func TestValidateRole(t *testing.T) {
	for _, role := range roleNames {
		if err := validateRole(role); err != nil {
			t.Fatalf("标准角色 %s 被拒绝：%v", role, err)
		}
	}
	for _, role := range []string{"", "root", "OWNER", "superuser"} {
		err := validateRole(role)
		if err == nil {
			t.Fatalf("非法角色 %q 被接受", role)
		}
		if code, _ := fault.Describe(err); code != "validation_failed" {
			t.Fatalf("非法角色返回 %s", code)
		}
	}
}

func TestSamePermissions(t *testing.T) {
	if !samePermissions([]string{"a", "b"}, []string{"b", "a"}) {
		t.Fatal("同集合不同顺序被判为不同")
	}
	if samePermissions([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("不同集合被判为相同")
	}
	if samePermissions(nil, []string{"a"}) {
		t.Fatal("空集合与单元素集合被判为相同")
	}
}

func TestDefaultClientKey(t *testing.T) {
	cases := []struct {
		remote string
		want   string
	}{
		{"203.0.113.9:54321", "203.0.113.9"},
		{"203.0.113.9", "203.0.113.9"},
		{"[2001:db8::1]:443", "2001:db8::1"},
		{"[2001:db8::1]", "2001:db8::1"},
		{"  198.51.100.7:1234  ", "198.51.100.7"},
		{"", ""},
	}
	for _, item := range cases {
		request := httptest.NewRequest("POST", "/api/v1/auth/chaoxing/challenges", strings.NewReader("{}"))
		request.RemoteAddr = item.remote
		if got := defaultClientKey(request); got != item.want {
			t.Fatalf("RemoteAddr %q 解析为 %q，期望 %q", item.remote, got, item.want)
		}
	}
}

func TestClientKeyIgnoresForwardedHeaders(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	service, _ := newTestService(t, baseTestOptions(now))
	request := httptest.NewRequest("POST", "/api/v1/auth/chaoxing/challenges", strings.NewReader("{}"))
	request.RemoteAddr = "203.0.113.9:5000"
	request.Header.Set("X-Forwarded-For", "10.0.0.1")
	request.Header.Set("X-Real-IP", "10.0.0.2")
	if key := service.clientKey(request); key != "203.0.113.9" {
		t.Fatalf("默认客户端键信任了转发头：%q", key)
	}

	custom := baseTestOptions(now)
	custom.ClientKey = func(r *http.Request) string { return strings.TrimSpace(r.Header.Get("X-Test-Client")) }
	customService, err := newService(nil, custom)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Test-Client", "client-a")
	if key := customService.clientKey(request); key != "client-a" {
		t.Fatalf("注入的 ClientKey 未生效：%q", key)
	}
	request.Header.Del("X-Test-Client")
	if key := customService.clientKey(request); key != "unknown" {
		t.Fatalf("空客户端标识未回退为 unknown：%q", key)
	}
}

func TestValidateAdminReason(t *testing.T) {
	if _, err := validateAdminReason("   "); err == nil {
		t.Fatal("空理由被接受")
	}
	if _, err := validateAdminReason("短"); err == nil {
		t.Fatal("过短理由被接受")
	}
	if _, err := validateAdminReason(strings.Repeat("长", 501)); err == nil {
		t.Fatal("超长理由被接受")
	}
	trimmed, err := validateAdminReason("  调整角色职责  ")
	if err != nil || trimmed != "调整角色职责" {
		t.Fatalf("有效理由未正确裁剪：%q %v", trimmed, err)
	}
}

func TestChallengeRateLimitDefaults(t *testing.T) {
	options := baseTestOptions(time.Unix(1700000000, 0).UTC())
	service, err := newService(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	limits := service.options.ChallengeRateLimit
	if limits.MaxPerClient != defaultChallengeMaxPerClient || limits.MaxPending != defaultChallengeMaxPending ||
		limits.Window != defaultChallengeWindow || limits.Retention != defaultChallengeRetention {
		t.Fatalf("挑战限流默认值异常：%+v", limits)
	}
	invalid := baseTestOptions(time.Unix(1700000000, 0).UTC())
	invalid.ChallengeRateLimit = ChallengeRateLimit{MaxPerClient: 1, Window: 2 * time.Second}
	if _, err := newService(nil, invalid); err == nil {
		t.Fatal("过短挑战限流窗口被接受")
	}
}

func TestSessionQuotaDefaults(t *testing.T) {
	options := baseTestOptions(time.Unix(1700000000, 0).UTC())
	service, err := newService(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if service.options.MaxSessionsPerSubject != defaultMaxSessionsPerSubject {
		t.Fatalf("默认单主体会话上限为 %d", service.options.MaxSessionsPerSubject)
	}
	if defaultMaxSessionsPerSubject != 16 {
		t.Fatalf("设计默认会话上限应为 16，实际 %d", defaultMaxSessionsPerSubject)
	}
	for _, invalid := range []int{-1, maxSessionsPerSubjectLimit + 1} {
		candidate := baseTestOptions(time.Unix(1700000000, 0).UTC())
		candidate.MaxSessionsPerSubject = invalid
		if _, err := newService(nil, candidate); err == nil {
			t.Fatalf("非法会话上限 %d 被接受", invalid)
		}
	}
}
