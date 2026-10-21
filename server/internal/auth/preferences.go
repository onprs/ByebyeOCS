package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 外观档位与模式固定为已确认取值；通知偏好只保存布尔开关。
var (
	allowedColorSchemes = map[string]struct{}{"graphite": {}, "grove": {}, "indigo": {}}
	allowedThemeModes   = map[string]struct{}{"system": {}, "light": {}, "dark": {}}
)

type notificationPreferences struct {
	TaskAttention bool `json:"task_attention"`
	WalletChanges bool `json:"wallet_changes"`
}

type preferencesView struct {
	ColorScheme   string                  `json:"color_scheme"`
	ThemeMode     string                  `json:"theme_mode"`
	Notifications notificationPreferences `json:"notifications"`
	Version       int64                   `json:"version"`
}

type preferenceNotificationsPatch struct {
	TaskAttention *bool `json:"task_attention"`
	WalletChanges *bool `json:"wallet_changes"`
}

type preferencesPatch struct {
	ColorScheme   *string                       `json:"color_scheme"`
	ThemeMode     *string                       `json:"theme_mode"`
	Notifications *preferenceNotificationsPatch `json:"notifications"`
}

type preferencesRecord struct {
	ColorScheme   string
	ThemeMode     string
	TaskAttention bool
	WalletChanges bool
	Version       int64
}

func (record preferencesRecord) view() preferencesView {
	return preferencesView{
		ColorScheme: record.ColorScheme,
		ThemeMode:   record.ThemeMode,
		Notifications: notificationPreferences{
			TaskAttention: record.TaskAttention,
			WalletChanges: record.WalletChanges,
		},
		Version: record.Version,
	}
}

func defaultPreferences() preferencesRecord {
	return preferencesRecord{ColorScheme: "graphite", ThemeMode: "system", TaskAttention: true, WalletChanges: true}
}

// handlePreferences 提供双端隔离的外观与通知偏好，使用 ETag/If-Match 处理并发修改。
func (s *Service) handlePreferences(w http.ResponseWriter, r *http.Request) {
	kind := kindFor(r)
	actor, ok := web.Require(w, r, kind, permissionFor(kind))
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		record, err := s.readPreferences(r.Context(), kind, actor.ID)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
		web.ETag(w, record.Version)
		web.JSON(w, r, http.StatusOK, record.view())
	case http.MethodPatch:
		expected, err := web.MatchVersion(r)
		if err != nil {
			web.Error(w, r, http.StatusPreconditionRequired, "precondition_required", "修改偏好需要 If-Match 版本条件")
			return
		}
		var patch preferencesPatch
		if !web.DecodeLimit(w, r, &patch, 8*1024) {
			return
		}
		if err := validatePreferencePatch(patch); err != nil {
			web.Fail(w, r, err)
			return
		}
		record, err := s.updatePreferences(r.Context(), kind, actor, expected, patch)
		if err != nil {
			web.Fail(w, r, err)
			return
		}
		web.ETag(w, record.Version)
		web.JSON(w, r, http.StatusOK, record.view())
	default:
		web.Error(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "此接口只支持 GET 和 PATCH")
	}
}

func validatePreferencePatch(patch preferencesPatch) error {
	if patch.ColorScheme != nil {
		if _, ok := allowedColorSchemes[*patch.ColorScheme]; !ok {
			return fault.New("validation_failed", "配色方案只支持 graphite、grove 或 indigo")
		}
	}
	if patch.ThemeMode != nil {
		if _, ok := allowedThemeModes[*patch.ThemeMode]; !ok {
			return fault.New("validation_failed", "外观模式只支持 system、light 或 dark")
		}
	}
	return nil
}

func (patch preferencesPatch) apply(base preferencesRecord) preferencesRecord {
	next := base
	if patch.ColorScheme != nil {
		next.ColorScheme = *patch.ColorScheme
	}
	if patch.ThemeMode != nil {
		next.ThemeMode = *patch.ThemeMode
	}
	if patch.Notifications != nil {
		if patch.Notifications.TaskAttention != nil {
			next.TaskAttention = *patch.Notifications.TaskAttention
		}
		if patch.Notifications.WalletChanges != nil {
			next.WalletChanges = *patch.Notifications.WalletChanges
		}
	}
	return next
}

// readPreferences 读取当前偏好；首次读取时建立默认记录，使版本从 1 开始。
func (s *Service) readPreferences(ctx context.Context, kind, subjectID string) (preferencesRecord, error) {
	record := defaultPreferences()
	now := s.clock()
	if err := s.queryPreferences(ctx, kind, subjectID, &record); err == nil {
		return record, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return preferencesRecord{}, err
	}
	// 默认值写入是幂等初始化，不改变已有记录。
	var insertErr error
	switch kind {
	case KindAdmin:
		_, insertErr = s.pool.Exec(ctx, `INSERT INTO public.admin_preferences
			(admin_id, color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version, updated_at)
			VALUES ($1, 'graphite', 'system', true, true, 1, $2) ON CONFLICT (admin_id) DO NOTHING`, subjectID, now)
	default:
		_, insertErr = s.pool.Exec(ctx, `INSERT INTO public.user_preferences
			(user_id, color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version, updated_at)
			VALUES ($1, 'graphite', 'system', true, true, 1, $2) ON CONFLICT (user_id) DO NOTHING`, subjectID, now)
	}
	if insertErr != nil {
		return preferencesRecord{}, fault.New("service_unavailable", "无法建立默认偏好设置")
	}
	record = defaultPreferences()
	if err := s.queryPreferences(ctx, kind, subjectID, &record); err != nil {
		return preferencesRecord{}, err
	}
	return record, nil
}

// queryPreferences 执行单端偏好读取，并把数据库错误转换为安全错误。
func (s *Service) queryPreferences(ctx context.Context, kind, subjectID string, record *preferencesRecord) error {
	var err error
	switch kind {
	case KindAdmin:
		err = s.pool.QueryRow(ctx, `SELECT color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version
			FROM public.admin_preferences WHERE admin_id = $1`, subjectID).
			Scan(&record.ColorScheme, &record.ThemeMode, &record.TaskAttention, &record.WalletChanges, &record.Version)
	default:
		err = s.pool.QueryRow(ctx, `SELECT color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version
			FROM public.user_preferences WHERE user_id = $1`, subjectID).
			Scan(&record.ColorScheme, &record.ThemeMode, &record.TaskAttention, &record.WalletChanges, &record.Version)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	if err != nil {
		return fault.New("service_unavailable", "无法读取偏好设置")
	}
	return nil
}

// updatePreferences 在事务中完成乐观版本检查与写入，冲突返回 412。
func (s *Service) updatePreferences(ctx context.Context, kind string, actor web.Actor, expected int64, patch preferencesPatch) (preferencesRecord, error) {
	now := s.clock()
	result := defaultPreferences()
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		base := defaultPreferences()
		var err error
		if kind == KindAdmin {
			err = tx.QueryRow(ctx, `SELECT color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version
				FROM public.admin_preferences WHERE admin_id = $1 FOR UPDATE`, actor.ID).
				Scan(&base.ColorScheme, &base.ThemeMode, &base.TaskAttention, &base.WalletChanges, &base.Version)
		} else {
			err = tx.QueryRow(ctx, `SELECT color_scheme, theme_mode, notify_task_attention, notify_wallet_changes, version
				FROM public.user_preferences WHERE user_id = $1 FOR UPDATE`, actor.ID).
				Scan(&base.ColorScheme, &base.ThemeMode, &base.TaskAttention, &base.WalletChanges, &base.Version)
		}
		notFound := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !notFound {
			return fault.New("service_unavailable", "无法读取偏好设置")
		}
		next := patch.apply(base)
		if notFound || base.Version != expected {
			return fault.New("version_conflict", "偏好已在其他位置更新，请重新加载后再修改")
		}
		next.Version = base.Version + 1
		var updateErr error
		if kind == KindAdmin {
			_, updateErr = tx.Exec(ctx, `UPDATE public.admin_preferences
				SET color_scheme = $3, theme_mode = $4, notify_task_attention = $5, notify_wallet_changes = $6,
					version = version + 1, updated_at = $7
				WHERE admin_id = $1 AND version = $2`,
				actor.ID, expected, next.ColorScheme, next.ThemeMode, next.TaskAttention, next.WalletChanges, now)
		} else {
			_, updateErr = tx.Exec(ctx, `UPDATE public.user_preferences
				SET color_scheme = $3, theme_mode = $4, notify_task_attention = $5, notify_wallet_changes = $6,
					version = version + 1, updated_at = $7
				WHERE user_id = $1 AND version = $2`,
				actor.ID, expected, next.ColorScheme, next.ThemeMode, next.TaskAttention, next.WalletChanges, now)
		}
		if updateErr != nil {
			return fault.New("version_conflict", "偏好已在其他位置更新，请重新加载后再修改")
		}
		event := audit.Event{
			Actor:        actor,
			Action:       "preferences.update",
			ResourceType: kind + "_preferences",
			ResourceID:   actor.ID,
			RequestID:    requestID(ctx),
		}
		if err := audit.Record(ctx, tx, event); err != nil {
			return fault.New("service_unavailable", "无法记录偏好修改审计")
		}
		result = next
		return nil
	})
	if err != nil {
		return preferencesRecord{}, err
	}
	return result, nil
}
