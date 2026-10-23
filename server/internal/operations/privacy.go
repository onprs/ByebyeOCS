package operations

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/events"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

type dataRequest struct {
	ID           string     `json:"id"`
	UserID       string     `json:"user_id,omitempty"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ReviewReason string     `json:"review_reason,omitempty"`
}

func (s *Service) createExport(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	if !web.Fresh(actor, time.Now()) {
		web.Error(w, r, 403, "step_up_required", "导出前请重新验证身份")
		return
	}
	if s.options.Export == nil {
		web.Error(w, r, 503, "not_configured", "数据导出暂未开放")
		return
	}
	var body struct {
		Format string `json:"format"`
	}
	if r.ContentLength != 0 && !web.Decode(w, r, &body) {
		return
	}
	if body.Format != "" && body.Format != "json" {
		web.Error(w, r, 422, "validation_failed", "当前支持 JSON 导出")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Fail(w, r, fault.New("service_unavailable", "暂时无法申请导出"))
		return
	}
	defer rollbackTx(tx)
	var count int
	if tx.QueryRow(r.Context(), `SELECT count(*) FROM public.data_requests WHERE user_id=$1 AND kind='export' AND created_at>now()-interval '1 day'`, actor.ID).Scan(&count) != nil {
		web.Error(w, r, 503, "service_unavailable", "无法读取导出请求")
		return
	}
	if count >= 3 {
		web.Error(w, r, 429, "rate_limited", "一天最多申请三次数据导出")
		return
	}
	id := web.NewID()
	var actual string
	err = tx.QueryRow(r.Context(), `INSERT INTO public.data_requests(id,user_id,kind,status) VALUES($1,$2,'export','pending') ON CONFLICT(user_id,kind) WHERE status IN ('pending','processing','needs_review','deferred') DO UPDATE SET updated_at=data_requests.updated_at RETURNING id::text`, id, actor.ID).Scan(&actual)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法创建导出请求")
		return
	}
	if actual == id {
		if err := s.record(r.Context(), tx, actor, "privacy.export_requested", id, ""); err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	if tx.Commit(r.Context()) != nil {
		web.Error(w, r, 503, "service_unavailable", "导出请求结果待查询")
		return
	}
	web.JSON(w, r, 202, map[string]any{"id": actual, "status": "pending"})
}
func (s *Service) createDeletion(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	if !web.Fresh(actor, time.Now()) {
		web.Error(w, r, 403, "step_up_required", "删除申请前请重新验证身份")
		return
	}
	var body struct {
		Confirm bool `json:"confirm"`
	}
	if !web.Decode(w, r, &body) {
		return
	}
	if !body.Confirm {
		web.Error(w, r, 422, "validation_failed", "请确认停止安排并申请删除")
		return
	}
	if s.options.RevokeUser == nil {
		web.Error(w, r, 503, "not_configured", "删除申请暂未开放")
		return
	}
	tx, err := s.pool.Begin(r.Context())
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法申请删除")
		return
	}
	defer rollbackTx(tx)
	id := web.NewID()
	var actual string
	err = tx.QueryRow(r.Context(), `INSERT INTO public.data_requests(id,user_id,kind,status) VALUES($1,$2,'deletion','needs_review') ON CONFLICT(user_id,kind) WHERE status IN ('pending','processing','needs_review','deferred') DO UPDATE SET updated_at=data_requests.updated_at RETURNING id::text`, id, actor.ID).Scan(&actual)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法保存删除申请")
		return
	}
	// 不覆盖其他风控状态：受限账号保持 restricted，但删除申请仍会被记录并撤销会话。
	if _, err = tx.Exec(r.Context(), `UPDATE public.users SET status='deletion_requested',version=version+1,updated_at=now() WHERE id=$1 AND status='active'`, actor.ID); err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法冻结账号")
		return
	}
	if err = s.options.RevokeUser(r.Context(), tx, actor.ID); err != nil {
		web.Fail(w, r, err)
		return
	}
	for _, statement := range []string{
		`UPDATE public.learning_plans SET status='paused',updated_at=now() WHERE user_id=$1 AND status='active'`,
		`UPDATE public.learning_tasks SET desired_state='pause',stop_requested_at=now(),version=version+1,updated_at=now() WHERE user_id=$1 AND state NOT IN ('confirmed','cancelled')`,
		`UPDATE public.learning_accounts SET blocked_code=CASE WHEN blocked_code='' THEN 'deletion_requested' ELSE blocked_code END,blocked_reason=CASE WHEN blocked_code='' THEN '用户申请删除' ELSE blocked_reason END,blocked_at=CASE WHEN blocked_code='' THEN now() ELSE blocked_at END,version=version+1,updated_at=now() WHERE user_id=$1`,
	} {
		if _, err = tx.Exec(r.Context(), statement, actor.ID); err != nil {
			web.Error(w, r, 503, "service_unavailable", "暂停学习授权失败")
			return
		}
	}
	if actual == id {
		if err = s.record(r.Context(), tx, actor, "privacy.deletion_requested", id, "用户确认停止所有安排并撤销会话"); err != nil {
			web.Fail(w, r, err)
			return
		}
	}
	if err = events.Record(r.Context(), tx, events.Event{Audience: "admin", Type: "privacy.deletion_requested", ResourceID: actual, Version: 1}); err != nil {
		web.Fail(w, r, err)
		return
	}
	if tx.Commit(r.Context()) != nil {
		web.Error(w, r, 503, "service_unavailable", "删除申请结果待查询")
		return
	}
	web.JSON(w, r, 202, map[string]any{"id": actual, "status": "needs_review", "sessions_revoked": true, "message": "已撤销会话并停止后续安排，资金与必要保留记录将单独核对"})
}
func (s *Service) listExports(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	s.listRequests(w, r, actor.ID, "export", false)
}
func (s *Service) listDeletions(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	s.listRequests(w, r, actor.ID, "deletion", false)
}
func (s *Service) adminRequests(w http.ResponseWriter, r *http.Request) {
	if _, ok := web.Require(w, r, "admin", "privacy.review"); !ok {
		return
	}
	s.listRequests(w, r, "", "", true)
}
func (s *Service) listRequests(w http.ResponseWriter, r *http.Request, userID, kind string, admin bool) {
	limit, err := web.Limit(r)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	cursor := auditCursor{Time: time.Now().Add(time.Second), ID: "ffffffff-ffff-ffff-ffff-ffffffffffff"}
	if value := r.URL.Query().Get("cursor"); value != "" {
		data, e := base64.RawURLEncoding.DecodeString(value)
		if e != nil || json.Unmarshal(data, &cursor) != nil || !web.ValidID(cursor.ID) {
			web.Error(w, r, 422, "validation_failed", "分页游标无效")
			return
		}
	}
	var owner any
	if userID != "" {
		owner = userID
	}
	rows, err := s.pool.Query(r.Context(), `SELECT id::text,user_id::text,kind,status,created_at,updated_at,expires_at,COALESCE(error_code,''),COALESCE(review_reason,'') FROM public.data_requests WHERE ($1::uuid IS NULL OR user_id=$1) AND ($2='' OR kind=$2) AND (created_at,id)<($3,$4::uuid) ORDER BY created_at DESC,id DESC LIMIT $5`, owner, kind, cursor.Time, cursor.ID, limit+1)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法读取隐私请求")
		return
	}
	defer rows.Close()
	items := []dataRequest{}
	var last auditCursor
	more := false
	for rows.Next() {
		var item dataRequest
		if rows.Scan(&item.ID, &item.UserID, &item.Kind, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt, &item.ErrorCode, &item.ReviewReason) != nil {
			web.Error(w, r, 503, "service_unavailable", "隐私请求暂不可读")
			return
		}
		if len(items) == limit {
			more = true
			break
		}
		last = auditCursor{Time: item.CreatedAt, ID: item.ID}
		if !admin {
			item.UserID = ""
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		web.Error(w, r, 503, "service_unavailable", "读取隐私请求失败")
		return
	}
	var next any
	if more {
		data, _ := json.Marshal(last)
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	web.JSON(w, r, 200, map[string]any{"items": items, "next_cursor": next})
}
func (s *Service) getExport(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	id := r.PathValue("id")
	if !web.ValidID(id) {
		web.Error(w, r, 404, "not_found", "导出请求不存在")
		return
	}
	var item dataRequest
	err := s.pool.QueryRow(r.Context(), `SELECT id::text,kind,status,created_at,updated_at,expires_at,COALESCE(error_code,''),COALESCE(review_reason,'') FROM public.data_requests WHERE id=$1 AND user_id=$2 AND kind='export'`, id, actor.ID).Scan(&item.ID, &item.Kind, &item.Status, &item.CreatedAt, &item.UpdatedAt, &item.ExpiresAt, &item.ErrorCode, &item.ReviewReason)
	if err == pgx.ErrNoRows {
		web.Error(w, r, 404, "not_found", "导出请求不存在")
		return
	}
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法读取导出状态")
		return
	}
	web.JSON(w, r, 200, item)
}

// acquireDownload 占用一个导出下载名额；满载时不排队，直接让客户端重试。
func (s *Service) acquireDownload() (func(), bool) {
	select {
	case s.downloadSlots <- struct{}{}:
		return func() { <-s.downloadSlots }, true
	default:
		return nil, false
	}
}

func (s *Service) downloadExport(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "user", "")
	if !ok {
		return
	}
	if !web.Fresh(actor, time.Now()) {
		web.Error(w, r, 403, "step_up_required", "下载前请重新验证身份")
		return
	}
	id := r.PathValue("id")
	if !web.ValidID(id) {
		web.Error(w, r, 404, "not_found", "导出请求不存在")
		return
	}
	release, ok := s.acquireDownload()
	if !ok {
		w.Header().Set("Retry-After", "3")
		web.Error(w, r, 429, "rate_limited", "正在处理其他导出下载，请稍后重试")
		return
	}
	defer release()
	var state string
	var expires *time.Time
	var ciphertext []byte
	err := s.pool.QueryRow(r.Context(), `SELECT status,expires_at,result_ciphertext FROM public.data_requests WHERE id=$1 AND user_id=$2 AND kind='export'`, id, actor.ID).Scan(&state, &expires, &ciphertext)
	if err == pgx.ErrNoRows {
		web.Error(w, r, 404, "not_found", "导出请求不存在")
		return
	}
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法下载数据")
		return
	}
	if state == "expired" || expires != nil && expires.Before(time.Now()) {
		web.Error(w, r, 410, "export_expired", "导出文件已过期，请重新申请")
		return
	}
	if state != "ready" {
		web.Error(w, r, 409, "export_not_ready", "导出文件尚未准备好")
		return
	}
	body, err := s.decryptExport(actor.ID, id, ciphertext)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	// 已鉴权且准备就绪的有界导出使用独立下载期限，慢链路不受普通API短期限截断。
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Minute))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="byebyeocs-data-%s.json"`, id))
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	if r.Method != "HEAD" {
		_, _ = w.Write(body)
	}
}
func (s *Service) encryptExport(user, id string, body []byte) ([]byte, error) {
	block, err := aes.NewCipher(s.options.Key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	_, _ = rand.Read(nonce)
	return gcm.Seal(nonce, nonce, body, []byte("privacy-export:v1:"+user+":"+id)), nil
}
func (s *Service) decryptExport(user, id string, body []byte) ([]byte, error) {
	keys := append([][]byte{s.options.Key}, s.options.DecryptionKeys...)
	for _, key := range keys {
		block, err := aes.NewCipher(key)
		if err != nil {
			continue
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil || len(body) < gcm.NonceSize()+gcm.Overhead() {
			continue
		}
		plain, err := gcm.Open(nil, body[:gcm.NonceSize()], body[gcm.NonceSize():], []byte("privacy-export:v1:"+user+":"+id))
		if err == nil {
			return plain, nil
		}
	}
	return nil, fault.New("internal_error", "导出文件验证失败或所需解密版本不可用")
}
func (s *Service) processExport(ctx context.Context) error {
	if s.options.Export == nil {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(tx)
	var id, user string
	err = tx.QueryRow(ctx, `SELECT id::text,user_id::text FROM public.data_requests WHERE kind='export' AND status='pending' ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id, &user)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE public.data_requests SET status='processing',updated_at=now() WHERE id=$1`, id); err != nil {
		return err
	}
	if tx.Commit(ctx) != nil {
		return fault.New("service_unavailable", "导出任务领取结果待核对")
	}
	value, err := s.options.Export(ctx, user)
	var body []byte
	if err == nil {
		body, err = json.Marshal(value)
	}
	state, code := "ready", ""
	if err != nil {
		kind, _ := fault.Describe(err)
		if kind == "export_too_large" {
			state, code = "needs_review", "export_too_large"
		} else {
			state, code = "failed", "export_failed"
		}
	} else if int64(len(body)) > s.options.ExportMaxBytes {
		state, code = "needs_review", "export_too_large"
	}
	var encrypted []byte
	if state == "ready" {
		encrypted, err = s.encryptExport(user, id, body)
		if err != nil {
			state, code = "failed", "encryption_failed"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	_, err = s.pool.Exec(finishCtx, `UPDATE public.data_requests SET status=$2,result_ciphertext=$3,error_code=NULLIF($4,''),expires_at=CASE WHEN $2='ready' THEN now()+interval '24 hours' ELSE NULL END,updated_at=now() WHERE id=$1 AND status='processing'`, id, state, encrypted, code)
	return err
}
func (s *Service) reviewDeletion(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "privacy.review")
	if !ok {
		return
	}
	if !web.Fresh(actor, time.Now()) {
		web.Error(w, r, 403, "step_up_required", "请先完成管理员二次验证")
		return
	}
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !web.Decode(w, r, &body) {
		return
	}
	reason := strings.TrimSpace(body.Reason)
	if len(reason) < 4 || len(reason) > 2000 {
		web.Error(w, r, 422, "validation_failed", "请填写具体复核理由（4–2000 字符）")
		return
	}
	action := strings.TrimSpace(body.Action)
	if action == "" {
		// 旧客户端只提交 {reason}：兼容为延后，绝不据此宣称删除完成。
		action = "defer"
	}
	if action != "defer" && action != "reject" && action != "complete" {
		web.Error(w, r, 422, "validation_failed", "复核动作只支持 defer、reject 或 complete")
		return
	}
	id := r.PathValue("id")
	if !web.ValidID(id) {
		web.Error(w, r, 404, "not_found", "请求不存在")
		return
	}
	result, blockers, err := s.decideDeletion(r.Context(), actor, id, action, reason)
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	switch result {
	case deletionCompleted:
		web.JSON(w, r, 200, map[string]any{
			"id": id, "status": "completed", "closed": true,
			"message": "账号已关闭；会话与原始主体密文已清除，账本、收费去重与审计记录按保留策略保留",
		})
	case deletionRejected:
		web.JSON(w, r, 200, map[string]any{
			"id": id, "status": "rejected", "closed": false,
			"message": "已驳回删除申请；可重新验证身份进入，任务保持暂停，不会自动恢复旧会话或执行",
		})
	case deletionDeferred:
		web.JSON(w, r, 200, map[string]any{
			"id": id, "status": "deferred", "closed": false,
			"message": "已记录延后处理，删除尚未完成",
		})
	case deletionBlocked:
		writeClosureBlocked(w, r, blockers)
	default:
		web.Error(w, r, 503, "service_unavailable", "删除复核结果未知，请重新读取申请状态")
	}
}
