//go:build integration

package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/testsupport"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func operationFixture(t *testing.T) (*Service, web.Actor, web.Actor) {
	t.Helper()
	pool := testsupport.NewPool(t)
	user, other := web.NewID(), web.NewID()
	for _, id := range []string{user, other} {
		if _, err := pool.Exec(context.Background(), "INSERT INTO public.users(id)VALUES($1)", id); err != nil {
			t.Fatal(err)
		}
	}
	service, err := New(pool, Options{DataDir: t.TempDir(), Key: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Prepare(); err != nil {
		t.Fatal(err)
	}
	return service, web.Actor{ID: user, Kind: "user", SessionID: web.NewID(), AuthenticatedAt: time.Now()}, web.Actor{ID: other, Kind: "user", SessionID: web.NewID(), AuthenticatedAt: time.Now()}
}
func operationRequest(service *Service, actor web.Actor, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	service.Register(mux)
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r = r.WithContext(web.WithRequestID(web.WithActor(r.Context(), actor), web.NewRequestID()))
	w := httptest.NewRecorder()
	web.Router(mux).ServeHTTP(w, r)
	return w
}
func TestExportsAreOwnedEncryptedAndExpire(t *testing.T) {
	service, user, other := operationFixture(t)
	w := operationRequest(service, user, "POST", "/api/v1/me/data-exports", "{}")
	if w.Code != 202 {
		t.Fatal("导出申请失败", w.Code, w.Body.String())
	}
	var created map[string]string
	if json.Unmarshal(w.Body.Bytes(), &created) != nil {
		t.Fatal("导出申请响应无效")
	}
	id := created["id"]
	if err := service.processExport(context.Background()); err != nil {
		t.Fatal(err)
	}
	var status string
	var encrypted []byte
	if service.pool.QueryRow(context.Background(), "SELECT status,result_ciphertext FROM public.data_requests WHERE id=$1", id).Scan(&status, &encrypted) != nil || status != "ready" {
		t.Fatal("导出未准备完成")
	}
	if json.Valid(encrypted) {
		t.Fatal("导出内容以明文保存")
	}
	if _, err := service.decryptExport(other.ID, id, encrypted); err == nil {
		t.Fatal("导出密文没有绑定用户")
	}
	for _, path := range []string{"/api/v1/me/data-exports/" + id, "/api/v1/me/data-exports/" + id + "/download"} {
		if w := operationRequest(service, other, "GET", path, ""); w.Code != 404 {
			t.Fatal("其他用户读到了导出状态或内容")
		}
	}
	w = operationRequest(service, user, "GET", "/api/v1/me/data-exports/"+id+"/download", "")
	if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
		t.Fatal("本人下载失败", w.Code)
	}
	for _, field := range []string{"password_hash", "subject_index", "platform_sessions", "csrf_digest"} {
		if strings.Contains(w.Body.String(), field) {
			t.Fatal("导出包含凭据字段")
		}
	}
	if _, err := service.pool.Exec(context.Background(), "UPDATE public.data_requests SET expires_at=now()-interval '1 hour' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if w := operationRequest(service, user, "GET", "/api/v1/me/data-exports/"+id+"/download", ""); w.Code != 410 {
		t.Fatal("到期导出仍可下载")
	}
	if err := service.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if service.pool.QueryRow(context.Background(), "SELECT status,result_ciphertext FROM public.data_requests WHERE id=$1", id).Scan(&status, &encrypted) != nil || status != "expired" || len(encrypted) != 0 {
		t.Fatal("过期导出密文未清理")
	}
}
func TestDeletionRequestRollsBackThenRevokes(t *testing.T) {
	service, user, _ := operationFixture(t)
	service.SetRevokeUser(func(ctx context.Context, tx pgx.Tx, id string) error {
		return fault.New("service_unavailable", "撤销暂不可用")
	})
	w := operationRequest(service, user, "POST", "/api/v1/me/deletion-requests", `{"confirm":true}`)
	if w.Code != 503 {
		t.Fatal("失败撤销未拒绝删除请求")
	}
	var state string
	if service.pool.QueryRow(context.Background(), "SELECT status FROM public.users WHERE id=$1", user.ID).Scan(&state) != nil || state != "active" {
		t.Fatal("失败撤销遗留半冻结用户")
	}
	var requests int
	if service.pool.QueryRow(context.Background(), "SELECT count(*) FROM public.data_requests").Scan(&requests) != nil || requests != 0 {
		t.Fatal("失败请求没有整体回滚")
	}
	called := false
	service.SetRevokeUser(func(ctx context.Context, tx pgx.Tx, id string) error {
		if id != user.ID {
			t.Fatal("撤销归属错误")
		}
		called = true
		return nil
	})
	w = operationRequest(service, user, "POST", "/api/v1/me/deletion-requests", `{"confirm":true}`)
	if w.Code != 202 || !called {
		t.Fatal("删除请求未调用撤销器", w.Code, w.Body.String())
	}
	if service.pool.QueryRow(context.Background(), "SELECT status FROM public.users WHERE id=$1", user.ID).Scan(&state) != nil || state != "deletion_requested" {
		t.Fatal("用户未冻结")
	}
	if service.pool.QueryRow(context.Background(), "SELECT status FROM public.data_requests WHERE user_id=$1", user.ID).Scan(&state) != nil || state != "needs_review" {
		t.Fatal("在财务复核前伪称已删除")
	}
}
func TestPrivacyRequiresFreshAuthentication(t *testing.T) {
	service, user, _ := operationFixture(t)
	user.AuthenticatedAt = time.Now().Add(-10 * time.Minute)
	if w := operationRequest(service, user, "POST", "/api/v1/me/data-exports", "{}"); w.Code != 403 {
		t.Fatal("旧认证可以导出")
	}
	if w := operationRequest(service, user, "POST", "/api/v1/me/deletion-requests", `{"confirm":true}`); w.Code != 403 {
		t.Fatal("旧认证可以申请删除")
	}
	if w := operationRequest(service, user, "GET", "/admin/api/v1/audit-events", ""); w.Code != 403 {
		t.Fatal("用户读到了管理员审计")
	}
}
