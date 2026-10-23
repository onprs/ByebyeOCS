package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// ownSessionView 是“我的设备”列表项，不含令牌摘要或客户端指纹。
type ownSessionView struct {
	ID              string    `json:"id"`
	Channel         string    `json:"channel"`
	CreatedAt       time.Time `json:"created_at"`
	LastSeenAt      time.Time `json:"last_seen_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	AuthenticatedAt time.Time `json:"authenticated_at"`
	Current         bool      `json:"current"`
}

// handleListSessions 列出当前主体的在用会话；用户端与管理员端分表查询，不跨端。
func (s *Service) handleListSessions(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return
	}
	state, _ := sessionFrom(r.Context())
	now := s.clock()
	var rows pgx.Rows
	var err error
	if kind == KindAdmin {
		rows, err = s.pool.Query(r.Context(), `SELECT id::text, 'web'::text, created_at, last_seen_at, expires_at, authenticated_at
			FROM public.admin_sessions
			WHERE admin_id = $1 AND status = 'active' AND expires_at > $2
			ORDER BY last_seen_at DESC, id
			LIMIT 50`, actor.ID, now)
	} else {
		// 活跃资格包含“访问令牌已过期但刷新链仍有效”的 App 会话：它们仍可续期，
		// 必须能被用户看到并撤销，不能因 15 分钟访问期限而隐藏。
		// 返回契约不变：expires_at 对这种情况取刷新链到期时间，其余会话保持原值。
		rows, err = s.pool.Query(r.Context(), `SELECT cs.id::text, cs.channel, cs.created_at,
				cs.last_seen_at,
				CASE WHEN cs.expires_at > $2 THEN cs.expires_at ELSE refresh.expires_at END,
				cs.authenticated_at
			FROM public.client_sessions cs
			LEFT JOIN LATERAL (
				SELECT max(rt.expires_at) AS expires_at
				FROM public.refresh_tokens rt
				WHERE rt.session_id = cs.id AND rt.revoked_at IS NULL AND rt.expires_at > $2
			) refresh ON true
			WHERE cs.user_id = $1 AND cs.status = 'active'
			  AND (cs.expires_at > $2 OR refresh.expires_at IS NOT NULL)
			ORDER BY cs.last_seen_at DESC, cs.id
			LIMIT 50`, actor.ID, now)
	}
	if err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取会话列表")
		return
	}
	defer rows.Close()
	sessions := make([]ownSessionView, 0, 8)
	for rows.Next() {
		var item ownSessionView
		if err := rows.Scan(&item.ID, &item.Channel, &item.CreatedAt, &item.LastSeenAt, &item.ExpiresAt, &item.AuthenticatedAt); err != nil {
			web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取会话列表")
			return
		}
		item.Current = item.ID == state.ID
		sessions = append(sessions, item)
	}
	if err := rows.Err(); err != nil {
		web.Error(w, r, http.StatusServiceUnavailable, "service_unavailable", "无法读取会话列表")
		return
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"sessions": sessions})
}

// handleRevokeSession 撤销当前主体自己的某个会话。
// 归属条件写在 SQL 中：其他用户、其他端或不存在的编号统一返回 404，不泄露存在性。
func (s *Service) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return
	}
	currentState, _ := sessionFrom(r.Context())
	id := r.PathValue("id")
	if !validUUID(id) {
		web.Error(w, r, http.StatusNotFound, "not_found", "会话不存在")
		return
	}
	now := s.clock()
	currentRevoked := false
	err := s.inTx(r.Context(), func(tx pgx.Tx) error {
		if kind == KindAdmin {
			var ownerID string
			err := tx.QueryRow(r.Context(), `SELECT admin_id::text FROM public.admin_sessions
				WHERE id = $1 AND admin_id = $2 FOR UPDATE`, id, actor.ID).Scan(&ownerID)
			if errors.Is(err, pgx.ErrNoRows) {
				return fault.New("not_found", "会话不存在")
			}
			if err != nil {
				return fault.New("service_unavailable", "无法读取会话")
			}
			if err := s.revokeAdminSession(r.Context(), tx, id, "self_revoked", now); err != nil {
				return err
			}
			currentRevoked = id == currentState.ID
			return audit.Record(r.Context(), tx, audit.Event{
				Actor: actor, Action: "admin.session.revoked",
				ResourceType: "admin_session", ResourceID: id, RequestID: requestID(r.Context()),
			})
		}
		var ownerID, channel string
		err := tx.QueryRow(r.Context(), `SELECT user_id::text, channel FROM public.client_sessions
			WHERE id = $1 AND user_id = $2 FOR UPDATE`, id, actor.ID).Scan(&ownerID, &channel)
		if errors.Is(err, pgx.ErrNoRows) {
			return fault.New("not_found", "会话不存在")
		}
		if err != nil {
			return fault.New("service_unavailable", "无法读取会话")
		}
		if channel == ChannelApp {
			// App 会话撤销必须同时吊销刷新链，避免刷新令牌继续续期。
			if err := s.revokeRefreshFamily(r.Context(), tx, id, "self_revoked", now); err != nil {
				return err
			}
		}
		if err := s.revokeClientSession(r.Context(), tx, id, "self_revoked", now); err != nil {
			return err
		}
		currentRevoked = id == currentState.ID
		return audit.Record(r.Context(), tx, audit.Event{
			Actor: actor, Action: "user.session.revoked",
			ResourceType: "client_session", ResourceID: id, RequestID: requestID(r.Context()),
		})
	})
	if err != nil {
		web.Fail(w, r, err)
		return
	}
	if currentRevoked {
		s.clearSessionCookie(w, kind)
	}
	web.JSON(w, r, http.StatusOK, map[string]any{"revoked": true, "current": currentRevoked})
}
