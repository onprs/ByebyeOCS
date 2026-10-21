package operations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

type BackupState struct {
	Status            string     `json:"status"`
	LastRecoverableAt *time.Time `json:"last_recoverable_at,omitempty"`
	VerifiedAt        *time.Time `json:"verified_at,omitempty"`
	Required          bool       `json:"required"`
}

func (s *Service) Backup() BackupState {
	result := BackupState{Status: "not_configured", Required: s.options.RequireFreshBackup}
	if s.options.BackupStatusFile == "" {
		return result
	}
	data, err := config.ReadSecretFile(s.options.BackupStatusFile)
	if err != nil {
		result.Status = "unavailable"
		return result
	}
	var report struct {
		Status            string    `json:"status"`
		LastRecoverableAt time.Time `json:"last_recoverable_at"`
		VerifiedAt        time.Time `json:"verified_at"`
		Offsite           bool      `json:"offsite"`
	}
	if json.Unmarshal(data, &report) != nil || report.Status != "ok" || !report.Offsite || report.LastRecoverableAt.IsZero() || report.VerifiedAt.IsZero() {
		result.Status = "unverified"
		return result
	}
	result.LastRecoverableAt = &report.LastRecoverableAt
	result.VerifiedAt = &report.VerifiedAt
	now := time.Now().UTC()
	if report.LastRecoverableAt.After(now.Add(30*time.Second)) || report.VerifiedAt.After(now.Add(30*time.Second)) || report.LastRecoverableAt.After(report.VerifiedAt.Add(30*time.Second)) {
		result.Status = "invalid_clock"
		return result
	}
	if now.Sub(report.LastRecoverableAt) > s.options.BackupMaxAge || now.Sub(report.VerifiedAt) > s.options.BackupMaxAge {
		result.Status = "stale"
		return result
	}
	result.Status = "ok"
	return result
}

// resourceGate 保护新的外部调用，备份新鲜度由产生资金责任的操作另行核查。
func (s *Service) resourceGate(ctx context.Context) error {
	if ctx.Err() != nil {
		return fault.New("service_unavailable", "操作已取消")
	}
	if _, err := os.Stat(filepath.Join(s.options.DataDir, ".recovery-required")); err == nil {
		return fault.New("recovery_required", "数据处于恢复复核阶段，新收费和付款暂不可用")
	} else if !os.IsNotExist(err) {
		return fault.New("recovery_required", "无法核对恢复状态，新收费暂不可用")
	}
	if s.options.DiskProtection {
		disk := s.Disk()
		if disk.Status == "unknown" || disk.Status == "protected" || disk.Status == "maintenance" {
			return fault.New("storage_pressure", "存储受限，新收费和付款暂不可用")
		}
	}
	return nil
}

// Gate 只用于新付费/收费操作，不能阻断已有支付回调、释放和必要恢复。
func (s *Service) Gate(ctx context.Context) error {
	if err := s.resourceGate(ctx); err != nil {
		return err
	}
	if s.options.RequireFreshBackup && s.Backup().Status != "ok" {
		return fault.New("backup_unavailable", "备份恢复点未达到要求，新收费和付款暂不可用")
	}
	return nil
}
