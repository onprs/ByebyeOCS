package operations

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/audit"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/web"
	"gopkg.in/yaml.v3"
)

type runtimeSettingsPatch struct {
	GlobalConcurrency      *int `json:"global_concurrency"`
	AccountConcurrency     *int `json:"account_concurrency"`
	ChapterConcurrency     *int `json:"chapter_concurrency"`
	RequestIntervalMS      *int `json:"request_interval_ms"`
	RequestIntervalFloorMS *int `json:"request_interval_floor_ms"`
}

func runtimeConfiguredValues(cfg config.Config) map[string]any {
	return map[string]any{
		"global_concurrency":        cfg.Learning.GlobalConcurrency,
		"account_concurrency":       cfg.Learning.AccountConcurrency,
		"chapter_concurrency":       cfg.Learning.ChapterConcurrency,
		"request_interval_ms":       cfg.Learning.RequestInterval.Milliseconds(),
		"request_interval_floor_ms": cfg.Learning.RequestIntervalFloor.Milliseconds(),
	}
}

func (s *Service) configuredRuntimeSettings() (map[string]any, error) {
	if s.options.ConfigPath == "" {
		return nil, errors.New("配置文件路径不可用")
	}
	cfg, err := config.Load(s.options.ConfigPath, os.LookupEnv, config.Overrides{})
	if err != nil {
		return nil, err
	}
	return runtimeConfiguredValues(cfg), nil
}

func runtimeValueInt(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		number, err := typed.Int64()
		return number, err == nil
	default:
		return 0, false
	}
}

func runtimeRestartRequired(effective, configured map[string]any) bool {
	for key, value := range configured {
		left, leftOK := runtimeValueInt(effective[key])
		right, rightOK := runtimeValueInt(value)
		if !leftOK || !rightOK || left != right {
			return true
		}
	}
	return false
}

// updateRuntimeSettings 只改写并发和请求节奏这四个键；其他配置与私密路径保持原样。
func (s *Service) updateRuntimeSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := web.Require(w, r, "admin", "admin.users.manage")
	if !ok {
		return
	}
	if s.options.ConfigPath == "" || s.options.LearningRuntime == nil {
		web.Error(w, r, 503, "not_configured", "运行时配置不可写")
		return
	}
	var input runtimeSettingsPatch
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		web.Error(w, r, 422, "validation_failed", "请求内容无效")
		return
	}
	current, err := config.Load(s.options.ConfigPath, os.LookupEnv, config.Overrides{})
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法读取服务设置")
		return
	}
	global, account, chapter, interval := current.Learning.GlobalConcurrency, current.Learning.AccountConcurrency,
		current.Learning.ChapterConcurrency, int(current.Learning.RequestInterval.Milliseconds())
	floor := int(current.Learning.RequestIntervalFloor.Milliseconds())
	if input.GlobalConcurrency != nil {
		global = *input.GlobalConcurrency
	}
	if input.AccountConcurrency != nil {
		account = *input.AccountConcurrency
	}
	if input.ChapterConcurrency != nil {
		chapter = *input.ChapterConcurrency
	}
	if input.RequestIntervalMS != nil {
		interval = *input.RequestIntervalMS
	}
	if input.RequestIntervalFloorMS != nil {
		floor = *input.RequestIntervalFloorMS
	}
	if global < 1 || global > 64 {
		web.Error(w, r, 422, "validation_failed", "全局执行名额必须在 1 到 64 之间")
		return
	}
	if account < 1 || account > global {
		web.Error(w, r, 422, "validation_failed", "账号默认并发必须在 1 到全局名额之间")
		return
	}
	if chapter < 1 || chapter > 64 {
		web.Error(w, r, 422, "validation_failed", "单课同步并发必须在 1 到 64 之间")
		return
	}
	if interval < 50 || interval > 2000 {
		web.Error(w, r, 422, "validation_failed", "请求间隔必须在 50 到 2000 毫秒之间")
		return
	}
	if floor < 0 || floor > interval || (floor > 0 && floor < 10) {
		web.Error(w, r, 422, "validation_failed", "请求间隔下限必须在 0 或 10 毫秒到请求间隔之间")
		return
	}
	raw, err := os.ReadFile(s.options.ConfigPath)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "暂时无法读取配置文件")
		return
	}
	document := map[string]any{}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		web.Error(w, r, 503, "service_unavailable", "配置文件格式无效")
		return
	}
	learning, _ := document["learning"].(map[string]any)
	if learning == nil {
		learning = map[string]any{}
	}
	before := runtimeConfiguredValues(current)
	learning["global_concurrency"] = global
	learning["account_concurrency"] = account
	learning["chapter_concurrency"] = chapter
	learning["request_interval"] = fmt.Sprintf("%dms", interval)
	if floor > 0 {
		learning["request_interval_floor"] = fmt.Sprintf("%dms", floor)
	} else {
		delete(learning, "request_interval_floor")
	}
	document["learning"] = learning
	encoded, err := yaml.Marshal(document)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法编码服务设置")
		return
	}
	directory := filepath.Dir(s.options.ConfigPath)
	temporary, err := os.CreateTemp(directory, ".config-*.yaml.tmp")
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "配置文件不可写")
		return
	}
	tempName := temporary.Name()
	defer os.Remove(tempName)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		web.Error(w, r, 503, "service_unavailable", "无法保护配置文件")
		return
	}
	if _, err := temporary.Write(encoded); err != nil {
		temporary.Close()
		web.Error(w, r, 503, "service_unavailable", "无法写入配置文件")
		return
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		web.Error(w, r, 503, "service_unavailable", "无法保存配置文件")
		return
	}
	if err := temporary.Close(); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法保存配置文件")
		return
	}
	validated, err := config.Load(tempName, os.LookupEnv, config.Overrides{})
	if err != nil {
		web.Error(w, r, 422, "validation_failed", "配置校验失败："+err.Error())
		return
	}
	if err := os.Rename(tempName, s.options.ConfigPath); err != nil {
		web.Error(w, r, 503, "service_unavailable", "无法替换配置文件")
		return
	}
	s.options.LearningRuntime.Set(global, account, chapter,
		time.Duration(interval)*time.Millisecond, time.Duration(floor)*time.Millisecond)
	after := runtimeConfiguredValues(validated)
	detail, _ := json.Marshal(map[string]any{"before": before, "after": after})
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		web.Error(w, r, 503, "service_unavailable", "设置已保存，但审计记录失败，请核对")
		return
	}
	auditErr := audit.Record(ctx, tx, audit.Event{Actor: actor, Action: "settings.runtime.update",
		ResourceType: "runtime_settings", ResourceID: "learning", Reason: string(detail), RequestID: web.RequestID(ctx)})
	if auditErr == nil {
		auditErr = tx.Commit(ctx)
	} else {
		_ = tx.Rollback(ctx)
	}
	if auditErr != nil {
		web.Error(w, r, 503, "service_unavailable", "设置已保存，但审计记录失败，请核对")
		return
	}
	web.JSON(w, r, 200, map[string]any{"configured": after, "runtime": s.runtimeFacts(),
		"restart_required": runtimeRestartRequired(s.runtimeFacts(), after), "saved": true, "applied": true})
}
