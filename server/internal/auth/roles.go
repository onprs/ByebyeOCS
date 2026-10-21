package auth

import (
	"sort"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

// 固定标准管理员角色。owner 拥有全部权限；其他角色按业务服务实际
// web.Require 权限字符串最小化授予，不定义业务服务从未检查的权限。
const (
	RoleOwner    = "owner"
	RoleOperator = "operator"
	RoleSupport  = "support"
	RoleFinance  = "finance"
	RoleObserver = "observer"
)

// 管理端自助权限：读取/修改自己的资料与偏好、步进验证、登出、会话列表。
// 所有标准角色都具备，避免只有 owner 能维护自己的账号。
const PermissionAdminSelf = "admin.self"

// 管理员与角色管理权限：仅 owner（通过 *）具备，供后续细分角色复用。
const PermissionAdminUsers = "admin.users.manage"

// roleNames 是允许写入数据库的角色集合，顺序用于展示。
var roleNames = []string{RoleOwner, RoleOperator, RoleSupport, RoleFinance, RoleObserver}

// rolePermissions 与业务模块实际检查的权限字符串一一对应：
//
//	learning: learning.read / learning.tasks.operate / learning.accounts.operate
//	ledger:   billing.read / billing.manage / wallet.adjust / wallet.refund / reconciliation.manage
//	questions: question:read / question:manage
//	operations: operations.read / audit.read / privacy.review
//	events:    events.read
var rolePermissions = map[string][]string{
	RoleOwner: {PermissionOwner},
	RoleOperator: {
		PermissionAdminSelf,
		"learning.read", "learning.tasks.operate", "learning.accounts.operate",
		"operations.read", "events.read", "question:read",
	},
	RoleSupport: {
		PermissionAdminSelf,
		"learning.read", "question:read", "privacy.review",
	},
	RoleFinance: {
		PermissionAdminSelf,
		"billing.read", "billing.manage", "wallet.adjust", "wallet.refund", "reconciliation.manage",
	},
	RoleObserver: {
		PermissionAdminSelf,
		"learning.read", "billing.read", "question:read",
		"operations.read", "audit.read", "events.read",
	},
}

// permissionsForRole 返回角色权限副本；未知角色返回空集合（失败关闭）。
func permissionsForRole(role string) []string {
	values, ok := rolePermissions[role]
	if !ok {
		return nil
	}
	return append([]string(nil), values...)
}

func roleExists(role string) bool {
	_, ok := rolePermissions[role]
	return ok
}

func validateRole(role string) error {
	if !roleExists(role) {
		return fault.New("validation_failed", "角色只支持 owner、operator、support、finance 或 observer")
	}
	return nil
}

// samePermissions 按集合比较权限，避免顺序差异造成误判。
func samePermissions(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

// permissionsFor 返回 actor 使用的权限集合。
func permissionsFor(kind, role string) []string {
	if kind == KindAdmin {
		return permissionsForRole(role)
	}
	return []string{PermissionSelf}
}

// permissionFor 返回该端自助操作的最低权限。
func permissionFor(kind string) string {
	if kind == KindAdmin {
		return PermissionAdminSelf
	}
	return PermissionSelf
}
