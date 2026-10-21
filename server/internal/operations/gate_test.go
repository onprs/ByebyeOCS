package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func TestBackupGateDoesNotInventRecoveryPoint(t *testing.T) {
	directory := t.TempDir()
	file := filepath.Join(directory, "backup.json")
	service := &Service{options: Options{DataDir: directory, Key: bytes.Repeat([]byte{7}, 32), RequireFreshBackup: true, BackupMaxAge: 15 * time.Minute, BackupStatusFile: file}}
	if err := service.Gate(context.Background()); err == nil {
		t.Fatal("缺少备份证明仍允许收费")
	}
	write := func(point, verified time.Time, offsite bool) {
		body, _ := json.Marshal(map[string]any{"status": "ok", "offsite": offsite, "last_recoverable_at": point, "verified_at": verified})
		if err := os.WriteFile(file, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	write(now.Add(-time.Minute), now, true)
	if err := service.Gate(context.Background()); err != nil {
		t.Fatal(err)
	}
	write(now.Add(-16*time.Minute), now, true)
	if err := service.Gate(context.Background()); err == nil {
		t.Fatal("过期恢复点仍允许收费")
	}
	write(now, now, false)
	if err := service.Gate(context.Background()); err == nil {
		t.Fatal("本机备份伪称异机恢复点")
	}
	write(now.Add(time.Hour), now, true)
	if err := service.Gate(context.Background()); err == nil {
		t.Fatal("接受了未来时间")
	}
	write(now, now, true)
	if err := os.WriteFile(filepath.Join(directory, ".recovery-required"), []byte("review"), 0600); err != nil {
		t.Fatal(err)
	}
	err := service.Gate(context.Background())
	code, _ := fault.Describe(err)
	if code != "recovery_required" {
		t.Fatal("恢复复核期间仍允许新收费")
	}
}
