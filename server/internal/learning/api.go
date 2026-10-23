package learning

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 管理员细粒度权限字符串；认证服务可为 owner 授予 *。
const (
	PermissionRead     = "learning.read"
	PermissionTasks    = "learning.tasks.operate"
	PermissionAccounts = "learning.accounts.operate"
)

// Register 注册用户端和管理员端完整路径；两端监听与认证中间件由入口分离。
func (s *Service) Register(mux *http.ServeMux) {
	user := func(pattern string, handler func(http.ResponseWriter, *http.Request, Actor)) {
		s.route(mux, pattern, "user", "", handler)
	}
	admin := func(pattern, permission string, handler func(http.ResponseWriter, *http.Request, Actor)) {
		s.route(mux, pattern, "admin", permission, handler)
	}
	user("GET /api/v1/courses", s.handleListCourses)
	user("GET /api/v1/courses/{id}", s.handleGetCourse)
	user("POST /api/v1/course-syncs", s.handleRequestCourseSync)
	user("GET /api/v1/plans", s.handleListPlans)
	user("POST /api/v1/plans", s.handleCreatePlan)
	user("GET /api/v1/plans/{id}", s.handleGetPlan)
	user("PATCH /api/v1/plans/{id}", s.handleUpdatePlan)
	user("POST /api/v1/plans/{id}/controls", s.handleControlPlan)
	user("GET /api/v1/tasks", s.handleListTasks)
	user("GET /api/v1/tasks/{id}", s.handleGetTask)
	user("POST /api/v1/tasks/{id}/controls", s.handleControlTask)

	admin("GET /admin/api/v1/dashboard", PermissionRead, s.handleDashboard)
	admin("GET /admin/api/v1/users", PermissionRead, s.handleAdminListUsers)
	admin("GET /admin/api/v1/users/{id}", PermissionRead, s.handleAdminGetUser)
	admin("GET /admin/api/v1/tasks", PermissionRead, s.handleAdminListTasks)
	admin("POST /admin/api/v1/account-operations", PermissionAccounts, s.handleAdminAccountOperations)
	admin("POST /admin/api/v1/task-operations", PermissionTasks, s.handleAdminTaskOperations)
}

func (s *Service) route(mux *http.ServeMux, pattern, audience, permission string, handler func(http.ResponseWriter, *http.Request, Actor)) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.opts.RequestTimeout)
		defer cancel()
		if web.RequestID(ctx) == "" {
			ctx = web.WithRequestID(ctx, web.NewRequestID())
		}
		r = r.WithContext(ctx)
		actor, ok := web.Require(w, r, audience, permission)
		if !ok {
			return
		}
		handler(w, r, actor)
	})
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Retryable bool   `json:"retryable"`
		Scope     string `json:"scope"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message, retryable, scope := ErrorCode(err)
	var safeFault *fault.Error
	if errors.As(err, &safeFault) {
		code, message = fault.Describe(err)
		status = statusForFault(code)
		retryable = status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable
		scope = "request"
		if status == http.StatusServiceUnavailable {
			scope = "service"
		}
	}
	body := errorEnvelope{}
	body.Error.Code, body.Error.Message = code, message
	body.Error.RequestID = web.RequestID(r.Context())
	if body.Error.RequestID == "" {
		body.Error.RequestID = web.NewRequestID()
	}
	body.Error.Retryable, body.Error.Scope = retryable, scope
	web.JSON(w, r, status, body)
}

func statusForFault(code string) int {
	switch code {
	case "unauthenticated":
		return http.StatusUnauthorized
	case "forbidden":
		return http.StatusForbidden
	case "not_found":
		return http.StatusNotFound
	case "conflict", "idempotency_conflict", "insufficient_balance":
		return http.StatusConflict
	case "version_conflict":
		return http.StatusPreconditionFailed
	case "validation_failed":
		return http.StatusUnprocessableEntity
	case "precondition_required":
		return http.StatusPreconditionRequired
	case "rate_limited":
		return http.StatusTooManyRequests
	case "external_disabled", "not_configured", "service_unavailable":
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func (s *Service) handleListCourses(w http.ResponseWriter, r *http.Request, actor Actor) {
	limit, err := web.Limit(r)
	if err != nil {
		writeError(w, r, validation("每页数量必须为 1 到 100"))
		return
	}
	page, err := s.ListCourses(r.Context(), actor, CourseFilter{Limit: limit,
		Cursor: r.URL.Query().Get("cursor"), Search: r.URL.Query().Get("search")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleGetCourse(w http.ResponseWriter, r *http.Request, actor Actor) {
	detail, err := s.GetCourse(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, detail)
}

func (s *Service) handleRequestCourseSync(w http.ResponseWriter, r *http.Request, actor Actor) {
	if r.ContentLength != 0 {
		var body struct{}
		if !decodeOptional(w, r, &body) {
			return
		}
	}
	item, _, err := s.RequestCourseSync(r.Context(), actor)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusAccepted, item)
}

func (s *Service) handleListPlans(w http.ResponseWriter, r *http.Request, actor Actor) {
	limit, err := web.Limit(r)
	if err != nil {
		writeError(w, r, validation("每页数量必须为 1 到 100"))
		return
	}
	page, err := s.ListPlans(r.Context(), actor, PlanFilter{Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleCreatePlan(w http.ResponseWriter, r *http.Request, actor Actor) {
	var input PlanInput
	if !web.DecodeLimit(w, r, &input, 64*1024) {
		return
	}
	plan, err := s.CreatePlan(r.Context(), actor, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.ETag(w, plan.Version)
	web.JSON(w, r, http.StatusCreated, plan)
}

func (s *Service) handleGetPlan(w http.ResponseWriter, r *http.Request, actor Actor) {
	plan, err := s.GetPlan(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.ETag(w, plan.Version)
	web.JSON(w, r, http.StatusOK, plan)
}

func (s *Service) handleUpdatePlan(w http.ResponseWriter, r *http.Request, actor Actor) {
	version, err := web.MatchVersion(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var input PlanInput
	if !web.DecodeLimit(w, r, &input, 64*1024) {
		return
	}
	plan, err := s.UpdatePlan(r.Context(), actor, r.PathValue("id"), version, input)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.ETag(w, plan.Version)
	web.JSON(w, r, http.StatusOK, plan)
}

func (s *Service) handleControlPlan(w http.ResponseWriter, r *http.Request, actor Actor) {
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !web.DecodeLimit(w, r, &body, 16*1024) {
		return
	}
	result, err := s.ControlPlan(r.Context(), actor, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), body.Action, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, result)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request, actor Actor) {
	limit, err := web.Limit(r)
	if err != nil {
		writeError(w, r, validation("每页数量必须为 1 到 100"))
		return
	}
	filter := taskFilterFromQuery(r, limit)
	page, err := s.ListTasks(r.Context(), actor, filter)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func taskFilterFromQuery(r *http.Request, limit int) TaskFilter {
	query := r.URL.Query()
	states := make([]string, 0)
	for _, value := range query["state"] {
		for _, state := range strings.Split(value, ",") {
			if state = strings.TrimSpace(state); state != "" {
				states = append(states, state)
			}
		}
	}
	return TaskFilter{States: states, CourseID: query.Get("course_id"), PlanID: query.Get("plan_id"),
		UserID: query.Get("user_id"), Limit: limit, Cursor: query.Get("cursor")}
}

func (s *Service) handleGetTask(w http.ResponseWriter, r *http.Request, actor Actor) {
	detail, err := s.GetTask(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, detail)
}

func (s *Service) handleControlTask(w http.ResponseWriter, r *http.Request, actor Actor) {
	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !web.DecodeLimit(w, r, &body, 16*1024) {
		return
	}
	result, err := s.ControlTask(r.Context(), actor, r.PathValue("id"),
		r.Header.Get("Idempotency-Key"), body.Action, body.Reason)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if !result.Applied {
		status = http.StatusAccepted
	}
	web.JSON(w, r, status, result)
}

func (s *Service) handleDashboard(w http.ResponseWriter, r *http.Request, actor Actor) {
	item, err := s.AdminDashboard(r.Context(), actor)
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, item)
}

func (s *Service) handleAdminListUsers(w http.ResponseWriter, r *http.Request, actor Actor) {
	limit, err := web.Limit(r)
	if err != nil {
		writeError(w, r, validation("每页数量必须为 1 到 100"))
		return
	}
	page, err := s.AdminListUsers(r.Context(), actor, AdminUserFilter{
		Search: r.URL.Query().Get("search"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleAdminGetUser(w http.ResponseWriter, r *http.Request, actor Actor) {
	item, err := s.AdminGetUser(r.Context(), actor, r.PathValue("id"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, item)
}

func (s *Service) handleAdminListTasks(w http.ResponseWriter, r *http.Request, actor Actor) {
	limit, err := web.Limit(r)
	if err != nil {
		writeError(w, r, validation("每页数量必须为 1 到 100"))
		return
	}
	page, err := s.AdminListTasks(r.Context(), actor, taskFilterFromQuery(r, limit))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, page)
}

func (s *Service) handleAdminTaskOperations(w http.ResponseWriter, r *http.Request, actor Actor) {
	var body TaskOperationsRequest
	if !web.DecodeLimit(w, r, &body, 64*1024) {
		return
	}
	items, err := s.AdminTaskOperations(r.Context(), actor, body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) handleAdminAccountOperations(w http.ResponseWriter, r *http.Request, actor Actor) {
	var body AccountOperationsRequest
	if !web.DecodeLimit(w, r, &body, 64*1024) {
		return
	}
	// 账号批量操作同样要求幂等键；审计按用户逐条写入。
	if !validIdempotencyKey(r.Header.Get("Idempotency-Key")) {
		writeError(w, r, validation("必须提供有效的 Idempotency-Key"))
		return
	}
	result, err := s.AdminAccountOperations(r.Context(), actor, body, r.Header.Get("Idempotency-Key"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	web.JSON(w, r, http.StatusOK, result)
}

// decodeOptional 接受空对象；未知字段和空 body 分别给出明确错误。
func decodeOptional(w http.ResponseWriter, r *http.Request, target any) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "请求格式无效")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		web.Error(w, r, http.StatusUnprocessableEntity, "validation_failed", "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}
