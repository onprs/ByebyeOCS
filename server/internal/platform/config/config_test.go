package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDSN = "postgres://service:private-test-value@127.0.0.1:5432/byebyeocs_test?sslmode=disable"

func environment(values map[string]string) LookupEnv {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestDefaultsAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")
	if err := os.WriteFile(path, []byte("server:\n  user_listen: 127.0.0.1:8100\nlog:\n  level: warn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	address := "127.0.0.1:8300"
	cfg, err := Load(path, environment(map[string]string{
		"BYEBYEOCS_DATABASE_DSN": testDSN, "BYEBYEOCS_USER_LISTEN": "127.0.0.1:8200",
	}), Overrides{UserListen: &address})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.UserListen != address || cfg.Server.AdminListen != "127.0.0.1:8091" || cfg.Log.Level != "warn" {
		t.Fatal("配置优先级错误")
	}
	if cfg.Database.MaxConnections != 12 || cfg.Database.QueryTimeout != 3*time.Second {
		t.Fatal("数据库默认值错误")
	}
}

func TestSecretFilesAndOverrides(t *testing.T) {
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "database.dsn")
	configPath := filepath.Join(dir, "service.yaml")
	if err := os.WriteFile(secretPath, []byte(testDSN+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("database:\n  dsn_file: database.dsn\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath, environment(nil), Overrides{})
	if err != nil || cfg.Database.DSN != testDSN {
		t.Fatal("配置文件相对连接路径读取失败")
	}
	cfg, err = Load(configPath, environment(map[string]string{"BYEBYEOCS_DATABASE_DSN": testDSN}), Overrides{})
	if err != nil || cfg.Database.DSNFile != "" {
		t.Fatal("环境连接串未覆盖配置文件连接来源")
	}
	cfg, err = Load("", environment(map[string]string{
		"BYEBYEOCS_DATABASE_DSN": "invalid", "BYEBYEOCS_DATABASE_DSN_FILE": "missing",
	}), Overrides{DSNFile: &secretPath})
	if err != nil || cfg.Database.DSN != testDSN {
		t.Fatal("命令行连接文件未覆盖环境连接来源")
	}
	if err := os.Chmod(secretPath, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configPath, environment(nil), Overrides{}); err == nil {
		t.Fatal("接受了可被其他用户读取的连接文件")
	}
	if _, err := Load("", environment(map[string]string{
		"BYEBYEOCS_DATABASE_DSN": testDSN, "BYEBYEOCS_DATABASE_DSN_FILE": secretPath,
	}), Overrides{}); err == nil {
		t.Fatal("未拒绝冲突的连接环境变量")
	}
}

func TestInvalidYAMLAndSafeErrors(t *testing.T) {
	for _, content := range []string{
		"unknown: private-test-value", "database:\n  dsn: private-test-value",
		"server:\n  read_timeout: no-duration", "server: [", "---\n{}\n---\n{}",
		"log:\n  level: info\n  level: error", strings.Repeat("#", 64*1024+1),
	} {
		t.Run(content[:min(len(content), 24)], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "service.yaml")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path, environment(map[string]string{"BYEBYEOCS_DATABASE_DSN": testDSN}), Overrides{})
			if err == nil {
				t.Fatal("接受了错误的 YAML 配置")
			}
			if strings.Contains(err.Error(), "private-test-value") {
				t.Fatal("配置错误泄露原始内容")
			}
		})
	}
}

func TestValidateRuntimeLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"相同监听", func(c *Config) { c.Server.AdminListen = c.Server.UserListen }},
		{"等价监听", func(c *Config) { c.Server.AdminListen = "[::ffff:127.0.0.1]:8090" }},
		{"公开监听", func(c *Config) { c.Server.UserListen = "0.0.0.0:8090" }},
		{"名称监听", func(c *Config) { c.Server.UserListen = "localhost:8090" }},
		{"缺少端口", func(c *Config) { c.Server.UserListen = "127.0.0.1" }},
		{"随机端口", func(c *Config) { c.Server.UserListen = "127.0.0.1:0" }},
		{"端口越界", func(c *Config) { c.Server.UserListen = "127.0.0.1:65536" }},
		{"负超时", func(c *Config) { c.Database.QueryTimeout = -time.Second }},
		{"过长超时", func(c *Config) { c.Server.IdleTimeout = 6 * time.Minute }},
		{"请求头超时", func(c *Config) { c.Server.ReadTimeout = time.Second }},
		{"零连接", func(c *Config) { c.Database.MaxConnections = 0 }},
		{"过多连接", func(c *Config) { c.Database.MaxConnections = 101 }},
		{"未知日志", func(c *Config) { c.Log.Level = "trace" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := Defaults()
			cfg.Database.DSN = testDSN
			test.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("配置未被拒绝")
			}
		})
	}
	cfg := Defaults()
	cfg.Database.DSN = testDSN
	cfg.Server.UserListen = "[::1]:8090"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDSNTransportPolicy(t *testing.T) {
	for _, dsn := range []string{
		"", "postgres://private-test-value", "mysql://user@127.0.0.1/db?sslmode=disable",
		"postgres://user@db.example/db", "postgres://user@db.example/db?sslmode=disable",
		"postgres://user@db.example/db?sslmode=require", "postgres://user@db.example/db?sslmode=verify-full&host=127.0.0.1",
		"postgres://user@127.0.0.1/db?sslmode=disable&sslmode=verify-full",
		"postgres://user@127.0.0.1/db?sslmode=disable&dbname=other", "postgres://user@db.example/db?sslmode=verify-full#private-test-value",
	} {
		if err := ValidateDSN(dsn); err == nil {
			t.Fatalf("未拒绝第 %d 个错误连接配置", len(dsn))
		} else if strings.Contains(err.Error(), "private-test-value") {
			t.Fatal("连接错误泄露原始内容")
		}
	}
	for _, dsn := range []string{testDSN, "postgresql://user@db.example/db?sslmode=verify-full", "postgres://user@[::1]/db?sslmode=disable"} {
		if err := ValidateDSN(dsn); err != nil {
			t.Fatal(err)
		}
	}
}

// 服务只能监听回环地址，公开入口必然是回环代理或本机隧道；默认信任回环
// 转发能让登录限流读取真实客户端，同时不扩大对非回环来源的信任。
func TestDefaultsTrustLoopbackProxies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "service.yaml")
	if err := os.WriteFile(path, []byte("server:\n  user_listen: 127.0.0.1:8100\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path, environment(map[string]string{"BYEBYEOCS_DATABASE_DSN": testDSN}), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	trusted := map[string]bool{}
	for _, prefix := range cfg.Server.TrustedProxies {
		trusted[prefix] = true
	}
	for _, prefix := range []string{"127.0.0.1/32", "::1/128"} {
		if !trusted[prefix] {
			t.Fatalf("未默认信任回环代理 %s：%v", prefix, cfg.Server.TrustedProxies)
		}
	}
}

func TestQuestionsSharedCacheSetting(t *testing.T) {
	if Defaults().Questions.AllowSharedCache {
		t.Fatal("共享缓存默认开启")
	}
	path := filepath.Join(t.TempDir(), "service.yaml")
	if err := os.WriteFile(path, []byte("questions:\n  allow_shared_cache: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path, environment(map[string]string{"BYEBYEOCS_DATABASE_DSN": testDSN}), Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Questions.AllowSharedCache {
		t.Fatal("未加载 allow_shared_cache")
	}

	cfg := Defaults()
	cfg.Database.DSN = testDSN
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKey = make([]byte, 32)
	cfg.Auth.EncryptionKey[0] = 1
	cfg.Auth.IdentityKey = make([]byte, 32)
	cfg.Auth.IdentityKey[0] = 2
	cfg.Auth.UserOrigin = "https://learn.example.test"
	cfg.Auth.AdminOrigin = "https://admin.example.test"
	cfg.Questions = Questions{
		Enabled: true, AllowSharedCache: true, URL: "http://127.0.0.1:8092",
		Token: strings.Repeat("k", 16), Timeout: 95 * time.Second, Concurrency: 2,
	}
	// 启用共享缓存不需要主服务读取题库服务的私有配置；该事实由题库服务自行校验。
	if err := cfg.Validate(); err != nil {
		t.Fatalf("启用共享缓存未通过配置校验：%v", err)
	}
}
