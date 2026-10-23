package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
)

func noEnvironment(string) (string, bool) { return "", false }

// 公开安装通过 config init 生成活动配置；这里固定生成结果中与代理、
// 备份恢复点和题库超时相关的接线，避免再次出现“能启动但正常功能不可用”。
func TestInitializeConfigWiresProxyAndBackupDefaults(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	dsnPath := filepath.Join(directory, "secrets", "database.dsn")
	if err := os.MkdirAll(filepath.Dir(dsnPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dsnPath, []byte("postgres://service@127.0.0.1/byebyeocs?sslmode=disable\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := initializeConfig([]string{
		"--output", configPath,
		"--user-origin", "https://learn.example.com",
		"--admin-origin", "https://admin.example.com",
		"--data-dir", filepath.Join(directory, "data"),
		"--admin-assets-dir", filepath.Join(directory, "admin"),
		"--client-assets-dir", filepath.Join(directory, "client"),
		"--database-dsn-file", dsnPath,
	}, &output); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(configPath, noEnvironment, config.Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "production" || !cfg.Auth.CookieSecure {
		t.Fatalf("生产配置默认值不符：mode=%s cookie_secure=%v", cfg.Mode, cfg.Auth.CookieSecure)
	}
	expected := []string{"127.0.0.1/32", "::1/128"}
	if len(cfg.Server.TrustedProxies) != len(expected) {
		t.Fatalf("未默认信任回环代理：%v", cfg.Server.TrustedProxies)
	}
	for i, value := range expected {
		if cfg.Server.TrustedProxies[i] != value {
			t.Fatalf("回环代理信任范围不符：%v", cfg.Server.TrustedProxies)
		}
	}
	if cfg.Operations.BackupStatusFile != filepath.Join(directory, "secrets", "backup-status.json") {
		t.Fatalf("恢复点状态文件未接线：%s", cfg.Operations.BackupStatusFile)
	}
	if !cfg.Operations.RequireFreshBackup {
		t.Fatal("生产配置未要求新鲜恢复点")
	}
	if cfg.Questions.Timeout != 95*time.Second {
		t.Fatalf("题库超时未覆盖 provider 边界：%s", cfg.Questions.Timeout)
	}
}

func TestInitializeConfigRejectsInvalidOriginsWithoutWriting(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.yaml")
	err := initializeConfig([]string{
		"--output", configPath,
		"--user-origin", "https://same.example.com",
		"--admin-origin", "https://same.example.com",
		"--data-dir", filepath.Join(directory, "data"),
	}, &strings.Builder{})
	if err == nil {
		t.Fatal("相同双端 Origin 被接受")
	}
	for _, path := range []string{configPath, filepath.Join(directory, "secrets", "application-v1.key")} {
		if _, statErr := os.Stat(path); statErr == nil {
			t.Fatalf("失败初始化写入了文件：%s", path)
		}
	}
}
