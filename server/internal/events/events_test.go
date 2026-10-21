package events

import (
	"github.com/onprs/ByebyeOCS/server/internal/web"
	"testing"
)

func TestStreamLimitAndPermissions(t *testing.T) {
	limiter := &streamLimiter{maximum: 4, actors: map[string]int{}}
	actor := web.Actor{Kind: "user", ID: web.NewID()}
	for range 3 {
		if !limiter.acquire(actor) {
			t.Fatal("未达到单用户上限却被拒绝")
		}
	}
	if limiter.acquire(actor) {
		t.Fatal("单用户连接数没有上限")
	}
	other := web.Actor{Kind: "user", ID: web.NewID()}
	if !limiter.acquire(other) || limiter.acquire(web.Actor{Kind: "user", ID: web.NewID()}) {
		t.Fatal("全局连接上限无效")
	}
	for range 3 {
		limiter.release(actor)
	}
	limiter.release(other)
	if len(limiter.actors) != 0 || limiter.active != 0 {
		t.Fatal("连接结束后未释放计数")
	}
	observer := web.Actor{Kind: "admin", ID: web.NewID(), Permissions: []string{"events.read", "learning.read"}}
	if !allowedAdminEvent(observer, "task.updated") || allowedAdminEvent(observer, "wallet.updated") || allowedAdminEvent(observer, "privacy.deletion_requested") || allowedAdminEvent(observer, "unknown.private") {
		t.Fatal("管理员流越过角色权限")
	}
}
