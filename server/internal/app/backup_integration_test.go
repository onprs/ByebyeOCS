//go:build integration

package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/ledger"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/web"
)

// 此检查显式选择真实 age/PG 工具，只连接测试工具创建的随机隔离数据库。
func TestActualEncryptedBackupAndIsolatedRestore(t *testing.T) {
	if os.Getenv("BYEBYEOCS_RUN_BACKUP_TEST") != "1" {
		t.Skip("显式启用真实备份工具检查")
	}
	if os.Geteuid() == 0 {
		t.Skip("隔离 initdb 需要普通用户")
	}
	for _, name := range []string{"pg_dump", "pg_restore", "initdb", "pg_ctl", "createdb", "psql", "age", "age-keygen"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("缺少实际工具 %s", name)
		}
	}
	fixture := newRuntimeFixture(t)
	client := fixtureClient()
	defer client.CloseIdleConnections()
	_, userID := loginFixtureUser(t, fixture, client, "learner-a")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var adminID string
	if err := fixture.runtime.db.Pool().QueryRow(ctx, "SELECT id::text FROM public.admin_users WHERE username=$1", fixtureAdminName).Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	wallet, err := fixture.runtime.Ledger.GetWallet(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	actor := web.Actor{ID: adminID, Kind: "admin", Permissions: []string{"*"}, AuthenticatedAt: time.Now()}
	if _, err := fixture.runtime.Ledger.AdjustWallet(ctx, actor, "backup-integrity-funds", ledger.WalletAdjustmentInput{WalletID: wallet.ID, Direction: "credit", AmountMinor: "750", Reason: "隔离备份：核对真实账本与用户关联可恢复"}); err != nil {
		t.Fatal(err)
	}
	workspace, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	configuration := filepath.Join(temporary, "configuration")
	if err := os.Mkdir(configuration, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"database.dsn": fixture.runtime.db.Pool().Config().ConnString(), "application-v1.key": base64.StdEncoding.EncodeToString(fixture.runtime.cfg.Auth.EncryptionKey), "identity.key": base64.StdEncoding.EncodeToString(fixture.runtime.cfg.Auth.IdentityKey), "config.yaml": "mode: development\ndatabase:\n  dsn_file: database.dsn\nauth:\n  enabled: true\n  cookie_secure: false\n  user_origin: http://127.0.0.1:8090\n  admin_origin: http://127.0.0.1:8091\n  encryption_key_file: application-v1.key\n  identity_key_file: identity.key\n"}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(configuration, name), []byte(content+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := config.Load(filepath.Join(configuration, "config.yaml"), func(string) (string, bool) { return "", false }, config.Overrides{}); err != nil {
		t.Fatal("备份中的配置与keyring无效", err)
	}
	run := func(name string, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, name, args...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("实际工具 %s 执行失败：%v", name, err)
		}
		return output
	}
	identity := filepath.Join(temporary, "backup.agekey")
	run("age-keygen", "-o", identity)
	recipient := strings.TrimSpace(string(run("age-keygen", "-y", identity)))
	stage := filepath.Join(temporary, "stage")
	status := filepath.Join(temporary, "backup-status.json")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	environment := filepath.Join(temporary, "backup.env")
	variables := map[string]string{"BYEBYEOCS_CONFIG_DIR": configuration, "BYEBYEOCS_DSN_FILE": filepath.Join(configuration, "database.dsn"), "BYEBYEOCS_BACKUP_STAGING": stage, "BYEBYEOCS_BACKUP_STATUS_FILE": status, "BYEBYEOCS_AGE_RECIPIENT": recipient}
	var content strings.Builder
	for key, value := range variables {
		content.WriteString(key + "=" + quote(value) + "\n")
	}
	if err := os.WriteFile(environment, []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	backup := exec.CommandContext(ctx, "bash", filepath.Join(workspace, "deploy/backup.sh"), "--local-only")
	backup.Env = append(os.Environ(), "BYEBYEOCS_BACKUP_ENV="+environment)
	if output, err := backup.CombinedOutput(); err != nil {
		safe := strings.ReplaceAll(string(output), fixture.runtime.db.Pool().Config().ConnString(), "<私有测试连接>")
		if password := fixture.runtime.db.Pool().Config().ConnConfig.Password; password != "" {
			safe = strings.ReplaceAll(safe, password, "<隐藏>")
		}
		t.Fatalf("实际 pg_dump/age 备份失败：%v，%s", err, safe)
	}
	if _, err := os.Stat(status); !os.IsNotExist(err) {
		t.Fatal("本地备份被错误记录为已验证异机恢复点")
	}
	archives, err := filepath.Glob(filepath.Join(stage, "*.tar.age"))
	if err != nil || len(archives) != 1 {
		t.Fatal("未生成唯一加密归档")
	}
	report := filepath.Join(temporary, "restore-report.json")
	run("bash", filepath.Join(workspace, "deploy/restore-drill.sh"), "--archive", archives[0], "--identity", identity, "--report", report)
	var result struct {
		Status              string
		MainRestored        bool `json:"main_restored"`
		ProductionUntouched bool `json:"production_untouched"`
	}
	data, err := os.ReadFile(report)
	if err != nil || json.Unmarshal(data, &result) != nil || result.Status != "passed" || !result.MainRestored || !result.ProductionUntouched {
		t.Fatal("真实隔离恢复未通过")
	}
	wallet, err = fixture.runtime.Ledger.GetWallet(ctx, userID)
	if err != nil || wallet.AvailableMinor != "750" || wallet.ReservedMinor != "0" {
		t.Fatal("隔离恢复触及了源测试数据库")
	}
}
