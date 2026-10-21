//go:build integration

package auth

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func (harness *harness) createRoleAdmin(username, role string) string {
	harness.t.Helper()
	secret, err := GenerateTOTPSecret()
	if err != nil {
		harness.t.Fatal(err)
	}
	if err := harness.service.CreateAdminWithRole(context.Background(), username, testAdminPassword, secret, role); err != nil {
		harness.t.Fatalf("创建角色 %s 的管理员失败：%v", role, err)
	}
	return secret
}

func (harness *harness) adminLoginAs(username, secret string) (string, string) {
	harness.t.Helper()
	// 同一管理员的 TOTP 计数器有时间窗口重放保护，测试中先推进一个周期。
	harness.clock.Advance(30 * time.Second)
	response := harness.adminLogin(username, testAdminPassword, harness.totpCode(secret))
	requireStatus(harness.t, response, http.StatusOK)
	cookie := response.cookie(adminSessionCookie)
	csrf, _ := response.json(harness.t)["csrf_token"].(string)
	if cookie == "" || csrf == "" {
		harness.t.Fatal("管理员登录未返回会话或 CSRF")
	}
	return cookie, csrf
}

func (harness *harness) stepUpAdmin(cookie, csrf, secret string) apiResponse {
	harness.t.Helper()
	harness.clock.Advance(30 * time.Second)
	return harness.adminRequest(http.MethodPost, "/admin/api/v1/auth/step-up", map[string]any{
		"password": testAdminPassword, "totp_code": harness.totpCode(secret),
	}, cookie, csrf)
}

func (harness *harness) adminRequest(method, path string, body any, cookie, csrf string, options ...requestOption) apiResponse {
	harness.t.Helper()
	base := []requestOption{withCookie(adminSessionCookie, cookie)}
	if method != http.MethodGet {
		base = append(base, withOrigin("http://admin.test"), withCSRF(csrf))
	}
	return harness.do(harness.admin, method, path, body, append(base, options...)...)
}

func (harness *harness) changeRole(cookie, csrf, id, role, reason, ifMatch string, options ...requestOption) apiResponse {
	harness.t.Helper()
	items := []requestOption{withIfMatch(ifMatch)}
	items = append(items, options...)
	return harness.adminRequest(http.MethodPatch, "/admin/api/v1/admin-users/"+id+"/role",
		map[string]any{"role": role, "reason": reason}, cookie, csrf, items...)
}

func findAdmin(t *testing.T, response apiResponse, username string) map[string]any {
	t.Helper()
	items, _ := response.json(t)["admin_users"].([]any)
	for _, raw := range items {
		record, _ := raw.(map[string]any)
		if record["username"] == username {
			return record
		}
	}
	t.Fatalf("管理员列表缺少 %s：%s", username, string(response.body))
	return nil
}

func adminVersion(record map[string]any) string {
	value, _ := record["version"].(float64)
	return fmt.Sprintf(`"v%d"`, int64(value))
}

func permissionList(t *testing.T, value any) []string {
	t.Helper()
	items, ok := value.([]any)
	if !ok {
		t.Fatalf("权限字段不是数组：%v", value)
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, _ := item.(string)
		result = append(result, text)
	}
	return result
}

func TestIntegrationAdminRoleManagement(t *testing.T) {
	harness := newHarness(t, nil)
	ownerSecret := harness.createRoleAdmin("owner", RoleOwner)
	operatorSecret := harness.createRoleAdmin("operator1", RoleOperator)
	ownerCookie, ownerCSRF := harness.adminLoginAs("owner", ownerSecret)

	list := harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, ownerCookie, ownerCSRF)
	requireStatus(t, list, http.StatusOK)
	decoded := list.json(t)
	if roles, _ := decoded["roles"].([]any); len(roles) != len(roleNames) {
		t.Fatalf("角色列表不完整：%v", decoded["roles"])
	}
	rolePermissions, _ := decoded["role_permissions"].(map[string]any)
	ownerPermissions := permissionList(t, rolePermissions[RoleOwner])
	if len(ownerPermissions) != 1 || ownerPermissions[0] != PermissionOwner {
		t.Fatalf("owner 权限应为 *：%v", ownerPermissions)
	}
	operator := findAdmin(t, list, "operator1")
	operatorID, _ := operator["id"].(string)
	operatorVersion := adminVersion(operator)

	missingPrecondition := harness.changeRole(ownerCookie, ownerCSRF, operatorID, RoleFinance, "调整财务职责", "")
	requireStatus(t, missingPrecondition, http.StatusPreconditionRequired)

	// 权限数组不能作为权威输入，未知字段直接 422。
	smuggled := harness.adminRequest(http.MethodPatch, "/admin/api/v1/admin-users/"+operatorID+"/role",
		map[string]any{"role": RoleFinance, "reason": "调整财务职责", "permissions": []string{"*"}},
		ownerCookie, ownerCSRF, withIfMatch(operatorVersion))
	requireStatus(t, smuggled, http.StatusUnprocessableEntity)

	stale := harness.changeRole(ownerCookie, ownerCSRF, operatorID, RoleFinance, "调整财务职责", `"v99"`)
	requireStatus(t, stale, http.StatusPreconditionFailed)
	if harness.errorCode(stale) != "version_conflict" {
		t.Fatalf("过期版本返回 %s", harness.errorCode(stale))
	}
	unknownRole := harness.changeRole(ownerCookie, ownerCSRF, operatorID, "root", "非法角色", operatorVersion)
	requireStatus(t, unknownRole, http.StatusUnprocessableEntity)
	shortReason := harness.changeRole(ownerCookie, ownerCSRF, operatorID, RoleFinance, "短", operatorVersion)
	requireStatus(t, shortReason, http.StatusUnprocessableEntity)

	// 非 owner 不能管理管理员，也不能自我提权。
	operatorCookie, operatorCSRF := harness.adminLoginAs("operator1", operatorSecret)
	escalation := harness.changeRole(operatorCookie, operatorCSRF, operatorID, RoleOwner, "自我提权", operatorVersion)
	requireStatus(t, escalation, http.StatusForbidden)
	requireStatus(t, harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, operatorCookie, operatorCSRF), http.StatusForbidden)
	me := harness.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, operatorCookie, operatorCSRF)
	requireStatus(t, me, http.StatusOK)
	if me.json(t)["role"] != RoleOperator {
		t.Fatalf("operator 自助接口返回角色错误：%v", me.json(t))
	}

	changed := harness.changeRole(ownerCookie, ownerCSRF, operatorID, RoleFinance, "调整为财务职责", operatorVersion)
	requireStatus(t, changed, http.StatusOK)
	changeBody := changed.json(t)
	if revoked, _ := changeBody["sessions_revoked"].(float64); revoked < 1 {
		t.Fatalf("角色变更未撤销旧会话：%v", changeBody)
	}
	requireStatus(t, harness.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, operatorCookie, operatorCSRF), http.StatusUnauthorized)

	financeCookie, financeCSRF := harness.adminLoginAs("operator1", operatorSecret)
	financeMe := harness.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, financeCookie, financeCSRF)
	requireStatus(t, financeMe, http.StatusOK)
	financeBody := financeMe.json(t)
	if financeBody["role"] != RoleFinance {
		t.Fatalf("角色变更后登录角色为 %v", financeBody["role"])
	}
	financePermissions := permissionList(t, financeBody["permissions"])
	if !containsString(financePermissions, "wallet.adjust") || containsString(financePermissions, "learning.tasks.operate") {
		t.Fatalf("finance 权限不正确：%v", financePermissions)
	}

	// 用与业务模块相同的方式验证中间件按角色放行。
	walletHandler := harness.service.Middleware(KindAdmin, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := web.Require(w, r, KindAdmin, "wallet.adjust"); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	learningHandler := harness.service.Middleware(KindAdmin, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := web.Require(w, r, KindAdmin, "learning.tasks.operate"); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	fetch := func(handler http.Handler, cookie string) apiResponse {
		return harness.do(handler, http.MethodGet, "/admin/api/v1/probe", nil, withCookie(adminSessionCookie, cookie))
	}
	requireStatus(t, fetch(walletHandler, financeCookie), http.StatusOK)
	requireStatus(t, fetch(learningHandler, financeCookie), http.StatusForbidden)

	observerSecret := harness.createRoleAdmin("observer1", RoleObserver)
	observerCookie, _ := harness.adminLoginAs("observer1", observerSecret)
	requireStatus(t, fetch(walletHandler, observerCookie), http.StatusForbidden)
	auditHandler := harness.service.Middleware(KindAdmin, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := web.Require(w, r, KindAdmin, "audit.read"); !ok {
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	requireStatus(t, fetch(auditHandler, observerCookie), http.StatusOK)

	// 超过新鲜窗口后必须重新步进验证。
	harness.clock.Advance(11 * time.Minute)
	staleAdmin := harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, ownerCookie, ownerCSRF)
	requireStatus(t, staleAdmin, http.StatusForbidden)
	if harness.errorCode(staleAdmin) != "reauth_required" {
		t.Fatalf("新鲜验证失败返回 %s", harness.errorCode(staleAdmin))
	}
	requireStatus(t, harness.stepUpAdmin(ownerCookie, ownerCSRF, ownerSecret), http.StatusOK)
	requireStatus(t, harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, ownerCookie, ownerCSRF), http.StatusOK)
}

func TestIntegrationLastOwnerProtection(t *testing.T) {
	harness := newHarness(t, nil)
	ownerSecret := harness.createRoleAdmin("owner", RoleOwner)
	secondSecret := harness.createRoleAdmin("owner2", RoleOwner)
	ownerCookie, ownerCSRF := harness.adminLoginAs("owner", ownerSecret)
	secondCookie, secondCSRF := harness.adminLoginAs("owner2", secondSecret)

	list := harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, ownerCookie, ownerCSRF)
	requireStatus(t, list, http.StatusOK)
	first := findAdmin(t, list, "owner")
	second := findAdmin(t, list, "owner2")
	firstID, _ := first["id"].(string)
	secondID, _ := second["id"].(string)
	firstVersion := adminVersion(first)
	secondVersion := adminVersion(second)

	// 两个有效 owner 时允许降级其中一个。
	demoteSecond := harness.changeRole(ownerCookie, ownerCSRF, secondID, RoleOperator, "职责调整", secondVersion)
	requireStatus(t, demoteSecond, http.StatusOK)

	// 现在 owner 是最后一个有效 owner，自我降级必须被拒绝。
	lastOwner := harness.changeRole(ownerCookie, ownerCSRF, firstID, RoleObserver, "自我降级", firstVersion)
	requireStatus(t, lastOwner, http.StatusConflict)
	if harness.errorCode(lastOwner) != "conflict" {
		t.Fatalf("最后一个 owner 降级返回 %s", harness.errorCode(lastOwner))
	}

	// 恢复第二个 owner 后允许降级自己。
	secondCookie, secondCSRF = harness.adminLoginAs("owner2", secondSecret)
	list = harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, ownerCookie, ownerCSRF)
	requireStatus(t, list, http.StatusOK)
	second = findAdmin(t, list, "owner2")
	secondVersion = adminVersion(second)
	promote := harness.changeRole(ownerCookie, ownerCSRF, secondID, RoleOwner, "恢复 owner 职责", secondVersion)
	requireStatus(t, promote, http.StatusOK)

	secondCookie, secondCSRF = harness.adminLoginAs("owner2", secondSecret)
	list = harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, secondCookie, secondCSRF)
	requireStatus(t, list, http.StatusOK)
	second = findAdmin(t, list, "owner2")
	secondVersion = adminVersion(second)
	selfDemote := harness.changeRole(secondCookie, secondCSRF, secondID, RoleObserver, "确认降级自己", secondVersion)
	requireStatus(t, selfDemote, http.StatusOK)
	if selfDemote.json(t)["current_session_revoked"] != true {
		t.Fatal("自身降级响应未标明当前会话已撤销")
	}
	requireStatus(t, harness.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, secondCookie, secondCSRF), http.StatusUnauthorized)
	if harness.count(`SELECT count(*) FROM public.admin_users WHERE role = 'owner' AND disabled = false`) != 1 {
		t.Fatal("降级后有效 owner 数量异常")
	}

	// 即使目标会话仍然有效，撤销会话也需要 owner 权限。
	operatorCookie, operatorCSRF := harness.adminLoginAs("owner2", secondSecret)
	revoke := harness.adminRequest(http.MethodPost, "/admin/api/v1/admin-users/"+firstID+"/revoke-sessions",
		map[string]any{"reason": "怀疑凭据泄露"}, operatorCookie, operatorCSRF)
	requireStatus(t, revoke, http.StatusForbidden)
}

func TestIntegrationConcurrentOwnerDemotion(t *testing.T) {
	harness := newHarness(t, nil)
	aSecret := harness.createRoleAdmin("ownerA", RoleOwner)
	bSecret := harness.createRoleAdmin("ownerB", RoleOwner)
	aCookie, aCSRF := harness.adminLoginAs("ownerA", aSecret)
	bCookie, bCSRF := harness.adminLoginAs("ownerB", bSecret)
	list := harness.adminRequest(http.MethodGet, "/admin/api/v1/admin-users", nil, aCookie, aCSRF)
	requireStatus(t, list, http.StatusOK)
	a := findAdmin(t, list, "ownerA")
	b := findAdmin(t, list, "ownerB")
	aID, _ := a["id"].(string)
	bID, _ := b["id"].(string)

	results := make(chan apiResponse, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		results <- harness.changeRole(aCookie, aCSRF, aID, RoleObserver, "并发自我降级", adminVersion(a))
	}()
	go func() {
		defer wait.Done()
		results <- harness.changeRole(bCookie, bCSRF, bID, RoleObserver, "并发自我降级", adminVersion(b))
	}()
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for response := range results {
		switch response.status {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("并发降级返回未预期状态 %d：%s", response.status, string(response.body))
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("并发降级应恰好一成一败，实际成功 %d 冲突 %d", success, conflict)
	}
	if harness.count(`SELECT count(*) FROM public.admin_users WHERE role = 'owner' AND disabled = false`) != 1 {
		t.Fatal("并发降级后有效 owner 数量不为 1")
	}
}

func TestIntegrationSessionManagement(t *testing.T) {
	h := newHarness(t, nil)
	webA, csrfA := h.userSession()
	webB, _ := h.userSession()

	list := h.do(h.user, http.MethodGet, "/api/v1/me/sessions", nil, withCookie(userSessionCookie, webA))
	requireStatus(t, list, http.StatusOK)
	sessions, _ := list.json(t)["sessions"].([]any)
	if len(sessions) != 2 {
		t.Fatalf("用户会话数量应为 2，实际 %d", len(sessions))
	}
	otherID := ""
	current := 0
	for _, raw := range sessions {
		item, _ := raw.(map[string]any)
		if item["current"] == true {
			current++
		} else {
			otherID, _ = item["id"].(string)
		}
	}
	if current != 1 || otherID == "" {
		t.Fatalf("会话列表 current 标记错误：%v", sessions)
	}
	revoked := h.do(h.user, http.MethodDelete, "/api/v1/me/sessions/"+otherID, nil,
		withCookie(userSessionCookie, webA), withOrigin("http://user.test"), withCSRF(csrfA))
	requireStatus(t, revoked, http.StatusOK)
	requireStatus(t, h.do(h.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, webB)), http.StatusUnauthorized)
	requireStatus(t, h.do(h.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, webA)), http.StatusOK)

	// 不能删除其他用户会话。
	h.verifier.setSubject("uid-second-user")
	webC, _ := h.userSession()
	h.verifier.setSubject(testSubject)
	listC := h.do(h.user, http.MethodGet, "/api/v1/me/sessions", nil, withCookie(userSessionCookie, webC))
	requireStatus(t, listC, http.StatusOK)
	sessionsC, _ := listC.json(t)["sessions"].([]any)
	if len(sessionsC) != 1 {
		t.Fatalf("第二个用户的会话数量异常：%v", sessionsC)
	}
	currentC, _ := sessionsC[0].(map[string]any)
	sessionCID, _ := currentC["id"].(string)
	crossUser := h.do(h.user, http.MethodDelete, "/api/v1/me/sessions/"+sessionCID, nil,
		withCookie(userSessionCookie, webA), withOrigin("http://user.test"), withCSRF(csrfA))
	requireStatus(t, crossUser, http.StatusNotFound)
	requireStatus(t, h.do(h.user, http.MethodGet, "/api/v1/me", nil, withCookie(userSessionCookie, webC)), http.StatusOK)

	// 管理员会话不能通过用户端删除。
	adminSecret := h.createRoleAdmin("owner", RoleOwner)
	adminCookie, adminCSRF := h.adminLoginAs("owner", adminSecret)
	adminList := h.adminRequest(http.MethodGet, "/admin/api/v1/me/sessions", nil, adminCookie, adminCSRF)
	requireStatus(t, adminList, http.StatusOK)
	adminSessions, _ := adminList.json(t)["sessions"].([]any)
	if len(adminSessions) != 1 {
		t.Fatalf("管理员会话数量异常：%v", adminSessions)
	}
	adminCurrent, _ := adminSessions[0].(map[string]any)
	adminSessionID, _ := adminCurrent["id"].(string)
	crossEnd := h.do(h.user, http.MethodDelete, "/api/v1/me/sessions/"+adminSessionID, nil,
		withCookie(userSessionCookie, webA), withOrigin("http://user.test"), withCSRF(csrfA))
	requireStatus(t, crossEnd, http.StatusNotFound)
	requireStatus(t, h.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, adminCookie, adminCSRF), http.StatusOK)

	// 撤销 App 会话必须同时吊销刷新链。
	challengeID, binding := h.createChallenge(ChannelApp)
	login := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelApp)
	requireStatus(t, login, http.StatusOK)
	loginBody := login.json(t)
	access, _ := loginBody["access_token"].(string)
	refresh, _ := loginBody["refresh_token"].(string)
	list = h.do(h.user, http.MethodGet, "/api/v1/me/sessions", nil, withCookie(userSessionCookie, webA))
	requireStatus(t, list, http.StatusOK)
	sessions, _ = list.json(t)["sessions"].([]any)
	appID := ""
	for _, raw := range sessions {
		item, _ := raw.(map[string]any)
		if item["channel"] == ChannelApp {
			appID, _ = item["id"].(string)
		}
	}
	if appID == "" {
		t.Fatal("会话列表缺少 App 会话")
	}
	requireStatus(t, h.do(h.user, http.MethodDelete, "/api/v1/me/sessions/"+appID, nil,
		withCookie(userSessionCookie, webA), withOrigin("http://user.test"), withCSRF(csrfA)), http.StatusOK)
	requireStatus(t, h.do(h.user, http.MethodGet, "/api/v1/me", nil, withBearer(access)), http.StatusUnauthorized)
	refreshed := h.do(h.user, http.MethodPost, "/api/v1/auth/refresh", map[string]any{"refresh_token": refresh})
	requireStatus(t, refreshed, http.StatusUnauthorized)

	// 管理员删除自己的另一台会话。
	operatorSecret := h.createRoleAdmin("operator1", RoleOperator)
	operatorCookie, operatorCSRF := h.adminLoginAs("operator1", operatorSecret)
	operatorList := h.adminRequest(http.MethodGet, "/admin/api/v1/me/sessions", nil, operatorCookie, operatorCSRF)
	requireStatus(t, operatorList, http.StatusOK)
	operatorSessions, _ := operatorList.json(t)["sessions"].([]any)
	if len(operatorSessions) != 1 {
		t.Fatalf("operator 会话数量异常：%v", operatorSessions)
	}
	operatorCurrent, _ := operatorSessions[0].(map[string]any)
	operatorSessionID, _ := operatorCurrent["id"].(string)
	deleted := h.adminRequest(http.MethodDelete, "/admin/api/v1/me/sessions/"+operatorSessionID, nil, operatorCookie, operatorCSRF)
	requireStatus(t, deleted, http.StatusOK)
	if deleted.json(t)["current"] != true {
		t.Fatal("删除当前管理员会话时未标记 current")
	}
	requireStatus(t, h.adminRequest(http.MethodGet, "/admin/api/v1/me", nil, operatorCookie, operatorCSRF), http.StatusUnauthorized)
}

func TestIntegrationChallengeCreationRateLimit(t *testing.T) {
	h := newHarness(t, func(options *Options) {
		options.ChallengeRateLimit = ChallengeRateLimit{MaxPerClient: 3, Window: 10 * time.Minute, MaxPending: 100}
	})
	for i := 0; i < 3; i++ {
		response := h.do(h.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
			map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
		requireStatus(t, response, http.StatusCreated)
	}
	blocked := h.do(h.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, blocked, http.StatusTooManyRequests)
	if blocked.header.Get("Retry-After") == "" {
		t.Fatal("挑战限流缺少 Retry-After")
	}
	// 不信任转发头：不同 X-Forwarded-For 仍共享同一 RemoteAddr 桶。
	spoofed := h.do(h.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb},
		withOrigin("http://user.test"), withHeader("X-Forwarded-For", "198.51.100.20"))
	requireStatus(t, spoofed, http.StatusTooManyRequests)
	// 窗口结束后恢复。
	h.clock.Advance(11 * time.Minute)
	recovered := h.do(h.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, recovered, http.StatusCreated)

	// 注入 ClientKey 后不同客户端互不影响。
	custom := newHarness(t, func(options *Options) {
		options.ChallengeRateLimit = ChallengeRateLimit{MaxPerClient: 2, Window: 10 * time.Minute, MaxPending: 100}
		options.ClientKey = func(r *http.Request) string { return r.Header.Get("X-Test-Client") }
	})
	createWithClient := func(client string) apiResponse {
		return custom.do(custom.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
			map[string]any{"method": "password", "client": ChannelWeb},
			withOrigin("http://user.test"), withHeader("X-Test-Client", client))
	}
	requireStatus(t, createWithClient("client-a"), http.StatusCreated)
	requireStatus(t, createWithClient("client-a"), http.StatusCreated)
	requireStatus(t, createWithClient("client-a"), http.StatusTooManyRequests)
	requireStatus(t, createWithClient("client-b"), http.StatusCreated)

	// 全局未完成挑战上限。
	pending := newHarness(t, func(options *Options) {
		options.ChallengeRateLimit = ChallengeRateLimit{MaxPerClient: 100, Window: 10 * time.Minute, MaxPending: 100}
	})
	now := pending.clock.Now()
	for i := 0; i < 100; i++ {
		if _, err := pending.pool.Exec(context.Background(), `INSERT INTO public.login_challenges
			(id, method, client_channel, binding_hash, client_key_hash, status, attempts, max_attempts, created_at, expires_at)
			VALUES ($1, 'password', 'web', $2, $3, 'pending', 0, 5, $4, $5)`,
			web.NewID(), tokenDigest(fmt.Sprintf("binding-%d", i)), tokenDigest(fmt.Sprintf("client-%d", i)),
			now, now.Add(2*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	overloaded := pending.do(pending.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, overloaded, http.StatusTooManyRequests)

	// 超过保留期的挑战在新建时被有界清理。
	cleanup := newHarness(t, nil)
	staleID := web.NewID()
	staleTime := cleanup.clock.Now()
	if _, err := cleanup.pool.Exec(context.Background(), `INSERT INTO public.login_challenges
		(id, method, client_channel, binding_hash, client_key_hash, status, attempts, max_attempts, created_at, expires_at)
		VALUES ($1, 'password', 'web', $2, $3, 'pending', 0, 5, $4, $5)`,
		staleID, tokenDigest("stale-binding"), tokenDigest("stale-client"),
		staleTime.Add(-3*time.Hour), staleTime.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh := cleanup.do(cleanup.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, fresh, http.StatusCreated)
	if cleanup.count(`SELECT count(*) FROM public.login_challenges WHERE id = $1`, staleID) != 0 {
		t.Fatal("过期挑战未被清理")
	}

	// 未配置 Verifier 时不写入挑战。
	disabled := newHarness(t, func(options *Options) { options.Verifier = nil })
	unavailable := disabled.do(disabled.user, http.MethodPost, "/api/v1/auth/chaoxing/challenges",
		map[string]any{"method": "password", "client": ChannelWeb}, withOrigin("http://user.test"))
	requireStatus(t, unavailable, http.StatusServiceUnavailable)
	if disabled.errorCode(unavailable) != "external_disabled" {
		t.Fatalf("未配置验证器返回 %s", disabled.errorCode(unavailable))
	}
	if disabled.count(`SELECT count(*) FROM public.login_challenges`) != 0 {
		t.Fatal("未配置验证器时仍写入了登录挑战")
	}
}

func TestIntegrationIdentityBoundHookRollback(t *testing.T) {
	var calls int
	var boundUserID string
	var boundSubject VerifiedSubject
	h := newHarness(t, func(options *Options) {
		options.OnIdentityBound = func(_ context.Context, tx pgx.Tx, userID string, subject VerifiedSubject) error {
			if tx == nil {
				t.Error("身份绑定回调缺少事务")
			}
			calls++
			boundUserID = userID
			boundSubject = subject
			if calls == 1 {
				return fault.New("service_unavailable", "学习账号绑定失败")
			}
			return nil
		}
	})
	challengeID, binding := h.createChallenge(ChannelWeb)
	failed := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
	requireStatus(t, failed, http.StatusServiceUnavailable)
	// 回调失败必须回滚用户、外部身份、平台会话和客户端会话。
	for _, table := range []string{"users", "external_identities", "platform_sessions", "client_sessions"} {
		if h.count("SELECT count(*) FROM public."+table) != 0 {
			t.Fatalf("绑定失败后 %s 留下了半绑定记录", table)
		}
	}
	if !web.ValidID(boundUserID) {
		t.Fatalf("回调未收到有效内部用户编号：%q", boundUserID)
	}
	if boundSubject.Subject != testSubject || string(boundSubject.Session) != testPlatformBody {
		t.Fatal("回调未收到验证器返回的稳定主体或平台会话")
	}

	// 同一挑战仍有剩余额度，重试后完整绑定。
	retried := h.verifyChallenge(challengeID, binding, testUserPassword, ChannelWeb)
	requireStatus(t, retried, http.StatusOK)
	if calls != 2 {
		t.Fatalf("身份绑定回调调用次数为 %d", calls)
	}
	if h.count("SELECT count(*) FROM public.users") != 1 ||
		h.count("SELECT count(*) FROM public.external_identities") != 1 ||
		h.count("SELECT count(*) FROM public.platform_sessions") != 1 {
		t.Fatal("重试后身份绑定记录不完整")
	}
	if h.count(`SELECT count(*) FROM public.audit_events WHERE action = 'user.login'`) != 1 {
		t.Fatal("成功登录未写入审计")
	}
}

func TestIntegrationValidateActorRevalidatesRole(t *testing.T) {
	h := newHarness(t, nil)
	secret := h.createRoleAdmin("observer1", RoleObserver)
	cookie, _ := h.adminLoginAs("observer1", secret)
	ctx := context.Background()
	var sessionID, adminID string
	if err := h.pool.QueryRow(ctx, `SELECT id::text, admin_id::text FROM public.admin_sessions WHERE token_hash = $1`,
		tokenDigest(cookie)).Scan(&sessionID, &adminID); err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: adminID, Kind: KindAdmin, SessionID: sessionID,
		Permissions: permissionsFor(KindAdmin, RoleObserver)}
	if err := h.service.ValidateActor(ctx, actor); err != nil {
		t.Fatalf("有效 observer 会话校验失败：%v", err)
	}
	// 模拟带外角色变更（例如维护操作），旧连接的权限不能继续使用。
	if _, err := h.pool.Exec(ctx, `UPDATE public.admin_users SET role = 'finance', version = version + 1 WHERE id = $1`, adminID); err != nil {
		t.Fatal(err)
	}
	if err := h.service.ValidateActor(ctx, actor); err == nil {
		t.Fatal("角色变更后旧权限仍通过 ValidateActor")
	}
	refreshed, err := h.service.EffectiveActor(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(refreshed.Permissions, "wallet.adjust") || containsString(refreshed.Permissions, "audit.read") {
		t.Fatalf("EffectiveActor 未返回最新角色权限：%v", refreshed.Permissions)
	}
	if err := h.service.ValidateActor(ctx, refreshed); err != nil {
		t.Fatalf("刷新后的 Actor 未通过校验：%v", err)
	}
	// 会话撤销后长连接立即失败关闭。
	if _, err := h.pool.Exec(ctx, `UPDATE public.admin_sessions SET status = 'revoked', revoked_at = now(), revoke_reason = 'test' WHERE id = $1`, sessionID); err != nil {
		t.Fatal(err)
	}
	if err := h.service.ValidateActor(ctx, refreshed); err == nil {
		t.Fatal("撤销后的会话仍通过 ValidateActor")
	}
}

func TestIntegrationPasswordVerificationConcurrency(t *testing.T) {
	h := newHarness(t, func(options *Options) { options.PasswordConcurrency = 1 })
	secret := h.createRoleAdmin("owner", RoleOwner)
	release, ok := h.service.acquirePasswordSlot()
	if !ok {
		t.Fatal("初始密码校验名额获取失败")
	}
	busy := h.adminLogin("owner", testAdminPassword, h.totpCode(secret))
	requireStatus(t, busy, http.StatusTooManyRequests)
	if busy.header.Get("Retry-After") == "" {
		t.Fatal("密码校验饱和缺少 Retry-After")
	}
	unknown := h.adminLogin("nobody", "whatever-password", "123456")
	requireStatus(t, unknown, http.StatusTooManyRequests)
	release()

	login := h.adminLogin("owner", testAdminPassword, h.totpCode(secret))
	requireStatus(t, login, http.StatusOK)
	cookie := login.cookie(adminSessionCookie)
	csrf, _ := login.json(t)["csrf_token"].(string)

	// 步进验证同样受全局并发限制。
	release, ok = h.service.acquirePasswordSlot()
	if !ok {
		t.Fatal("第二次密码校验名额获取失败")
	}
	h.clock.Advance(30 * time.Second)
	blockedStepUp := h.adminRequest(http.MethodPost, "/admin/api/v1/auth/step-up", map[string]any{
		"password": testAdminPassword, "totp_code": h.totpCode(secret),
	}, cookie, csrf)
	requireStatus(t, blockedStepUp, http.StatusTooManyRequests)
	release()
	recovered := h.adminRequest(http.MethodPost, "/admin/api/v1/auth/step-up", map[string]any{
		"password": testAdminPassword, "totp_code": h.totpCode(secret),
	}, cookie, csrf)
	requireStatus(t, recovered, http.StatusOK)
}
