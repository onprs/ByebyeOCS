// Package audit 在业务事务内记录最小审计事实。
package audit

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
	"regexp"
	"strings"
)

type Event struct {
	Actor                                               web.Actor
	Action, ResourceType, ResourceID, Reason, RequestID string
}

var validRequest = regexp.MustCompile(`^req_[0-9a-f]{32}$`)

func Record(ctx context.Context, tx pgx.Tx, event Event) error {
	if tx == nil {
		return fault.New("internal_error", "审计记录要求显式事务")
	}
	if event.Action == "" || len(event.Action) > 128 || event.ResourceType == "" || len(event.ResourceType) > 64 || len(event.Reason) > 2000 {
		return fault.New("validation_failed", "审计操作信息不完整或超出限制")
	}
	requestID := event.RequestID
	if requestID == "" {
		requestID = web.RequestID(ctx)
	}
	if !validRequest.MatchString(requestID) {
		requestID = web.NewRequestID()
	}
	var userID, adminID any
	kind := event.Actor.Kind
	switch kind {
	case "user":
		userID = event.Actor.ID
	case "admin":
		adminID = event.Actor.ID
	case "system", "":
		kind = "system"
	default:
		return fault.New("validation_failed", "审计主体无效")
	}
	if kind != "system" && !web.ValidID(event.Actor.ID) {
		return fault.New("validation_failed", "审计主体编号无效")
	}
	var resourceID, resourceKey any
	if event.ResourceID != "" {
		if !web.ValidResourceID(event.ResourceID) {
			return fault.New("validation_failed", "审计资源编号无效")
		}
		if web.ValidID(event.ResourceID) {
			resourceID = event.ResourceID
		} else {
			resourceKey = event.ResourceID
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO public.audit_events(id,actor_kind,actor_user_id,actor_admin_id,action,resource_type,resource_id,resource_key,request_id,reason)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, web.NewID(), kind, userID, adminID, event.Action, event.ResourceType, resourceID, resourceKey, requestID, strings.TrimSpace(event.Reason))
	if err != nil {
		return fault.New("audit_write_failed", "无法写入操作审计")
	}
	return nil
}
