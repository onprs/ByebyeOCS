package config

import (
	"bytes"
	"encoding/base64"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"net/netip"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Auth struct {
	Enabled              bool           `yaml:"enabled"`
	EncryptionKeyFile    string         `yaml:"encryption_key_file" json:"-"`
	EncryptionKeyVersion int            `yaml:"encryption_key_version"`
	DecryptionKeyFiles   map[int]string `yaml:"decryption_key_files,omitempty" json:"-"`
	IdentityKeyFile      string         `yaml:"identity_key_file" json:"-"`
	EncryptionKey        []byte         `yaml:"-" json:"-"`
	EncryptionKeys       map[int][]byte `yaml:"-" json:"-"`
	IdentityKey          []byte         `yaml:"-" json:"-"`
	CookieSecure         bool           `yaml:"cookie_secure"`
	UserOrigin           string         `yaml:"user_origin"`
	AdminOrigin          string         `yaml:"admin_origin"`
	SessionTTL           time.Duration  `yaml:"session_ttl"`
}
type Storage struct {
	DataDir         string `yaml:"data_dir"`
	AdminAssetsDir  string `yaml:"admin_assets_dir"`
	ClientAssetsDir string `yaml:"client_assets_dir"`
}
type Learning struct {
	Enabled                    bool   `yaml:"enabled"`
	ExecutionEnabled           bool   `yaml:"execution_enabled"`
	AuthorizationReference     string `yaml:"authorization_reference"`
	SubjectNamespace           string `yaml:"subject_namespace"`
	SubjectProbeURL            string `yaml:"subject_probe_url"`
	SubjectProbeMethod         string `yaml:"subject_probe_method"`
	SubjectPath                string `yaml:"subject_path"`
	SessionExpiryPath          string `yaml:"session_expiry_path"`
	SessionExpiryFormat        string `yaml:"session_expiry_format"`
	DisplayNamePath            string `yaml:"display_name_path"`
	SchoolPath                 string `yaml:"school_path"`
	BrowserExecutable          string `yaml:"browser_executable"`
	ExecutionEvidenceReference string `yaml:"execution_evidence_reference"`
	GlobalConcurrency          int    `yaml:"global_concurrency"`
	AccountConcurrency         int    `yaml:"account_concurrency"`
}
type Payments struct {
	Enabled         bool   `yaml:"enabled"`
	ChargingEnabled bool   `yaml:"charging_enabled"`
	MerchantID      string `yaml:"merchant_id"`
	KeyFile         string `yaml:"key_file" json:"-"`
	Key             string `yaml:"-" json:"-"`
	NotifyURL       string `yaml:"notify_url"`
	ReturnURL       string `yaml:"return_url"`
}
type Questions struct {
	Enabled     bool          `yaml:"enabled"`
	URL         string        `yaml:"url"`
	TokenFile   string        `yaml:"token_file" json:"-"`
	Token       string        `yaml:"-" json:"-"`
	Timeout     time.Duration `yaml:"timeout"`
	Concurrency int           `yaml:"concurrency"`
}
type Operations struct {
	DiskProtection     bool          `yaml:"disk_protection"`
	RetentionInterval  time.Duration `yaml:"retention_interval"`
	ExportMaxBytes     int64         `yaml:"export_max_bytes"`
	MaxEventStreams    int           `yaml:"max_event_streams"`
	RequireFreshBackup bool          `yaml:"require_fresh_backup"`
	BackupStatusFile   string        `yaml:"backup_status_file"`
	BackupMaxAge       time.Duration `yaml:"backup_max_age"`
}

func runtimeDefaults(cfg *Config) {
	cfg.Mode = "production"
	cfg.Auth = Auth{EncryptionKeyVersion: 1, CookieSecure: true, UserOrigin: "http://127.0.0.1:8090", AdminOrigin: "http://127.0.0.1:8091", SessionTTL: 12 * time.Hour}
	cfg.Storage = Storage{DataDir: "data"}
	cfg.Learning = Learning{GlobalConcurrency: 4, AccountConcurrency: 1}
	cfg.Questions = Questions{Timeout: 95 * time.Second, Concurrency: 2}
	cfg.Operations = Operations{DiskProtection: true, RetentionInterval: 24 * time.Hour, ExportMaxBytes: 8 << 20, MaxEventStreams: 64, RequireFreshBackup: true, BackupMaxAge: 15 * time.Minute}
}
func resolveRuntimeFiles(cfg *Config, path string) {
	if cfg.Operations.BackupStatusFile != "" && !filepath.IsAbs(cfg.Operations.BackupStatusFile) {
		cfg.Operations.BackupStatusFile = filepath.Join(filepath.Dir(path), cfg.Operations.BackupStatusFile)
	}
	for version, value := range cfg.Auth.DecryptionKeyFiles {
		if value != "" && !filepath.IsAbs(value) {
			cfg.Auth.DecryptionKeyFiles[version] = filepath.Join(filepath.Dir(path), value)
		}
	}
	for _, value := range []*string{&cfg.Auth.EncryptionKeyFile, &cfg.Auth.IdentityKeyFile, &cfg.Storage.DataDir, &cfg.Storage.AdminAssetsDir, &cfg.Storage.ClientAssetsDir, &cfg.Payments.KeyFile, &cfg.Questions.TokenFile} {
		if *value != "" && !filepath.IsAbs(*value) {
			*value = filepath.Join(filepath.Dir(path), *value)
		}
	}
}
func applyRuntimeEnv(cfg *Config, lookup LookupEnv) error {
	for _, entry := range []struct {
		key    string
		target *string
	}{
		{"BYEBYEOCS_MODE", &cfg.Mode},
		{"BYEBYEOCS_BACKUP_STATUS_FILE", &cfg.Operations.BackupStatusFile},
		{"BYEBYEOCS_AUTH_KEY_FILE", &cfg.Auth.EncryptionKeyFile}, {"BYEBYEOCS_IDENTITY_KEY_FILE", &cfg.Auth.IdentityKeyFile}, {"BYEBYEOCS_USER_ORIGIN", &cfg.Auth.UserOrigin}, {"BYEBYEOCS_ADMIN_ORIGIN", &cfg.Auth.AdminOrigin},
		{"BYEBYEOCS_DATA_DIR", &cfg.Storage.DataDir}, {"BYEBYEOCS_ADMIN_ASSETS_DIR", &cfg.Storage.AdminAssetsDir}, {"BYEBYEOCS_CLIENT_ASSETS_DIR", &cfg.Storage.ClientAssetsDir},
		{"BYEBYEOCS_ZPAY_MERCHANT_ID", &cfg.Payments.MerchantID}, {"BYEBYEOCS_ZPAY_KEY_FILE", &cfg.Payments.KeyFile}, {"BYEBYEOCS_ZPAY_NOTIFY_URL", &cfg.Payments.NotifyURL}, {"BYEBYEOCS_ZPAY_RETURN_URL", &cfg.Payments.ReturnURL},
		{"BYEBYEOCS_TIKU_URL", &cfg.Questions.URL}, {"BYEBYEOCS_TIKU_TOKEN_FILE", &cfg.Questions.TokenFile},
	} {
		if value, ok := lookup(entry.key); ok {
			*entry.target = value
		}
	}
	for _, entry := range []struct {
		key    string
		target *bool
	}{
		{"BYEBYEOCS_AUTH_ENABLED", &cfg.Auth.Enabled}, {"BYEBYEOCS_COOKIE_SECURE", &cfg.Auth.CookieSecure}, {"BYEBYEOCS_LEARNING_ENABLED", &cfg.Learning.Enabled}, {"BYEBYEOCS_EXECUTION_ENABLED", &cfg.Learning.ExecutionEnabled},
		{"BYEBYEOCS_PAYMENTS_ENABLED", &cfg.Payments.Enabled}, {"BYEBYEOCS_CHARGING_ENABLED", &cfg.Payments.ChargingEnabled}, {"BYEBYEOCS_TIKU_ENABLED", &cfg.Questions.Enabled},
	} {
		if value, ok := lookup(entry.key); ok {
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return invalid("功能开关环境变量必须为 true 或 false")
			}
			*entry.target = parsed
		}
	}
	if value, ok := lookup("BYEBYEOCS_AUTH_KEY_VERSION"); ok {
		version, err := strconv.Atoi(value)
		if err != nil {
			return invalid("加密密钥版本环境变量必须是正整数")
		}
		cfg.Auth.EncryptionKeyVersion = version
	}
	if cfg.Auth.Enabled {
		if err := loadAuthKeys(cfg); err != nil {
			return err
		}
	}
	if cfg.Payments.Enabled {
		data, err := ReadSecretFile(cfg.Payments.KeyFile)
		if err != nil {
			return fault.New("payment_key_invalid", "支付密钥文件必须存在且仅所有者可读")
		}
		cfg.Payments.Key = strings.TrimSpace(string(data))
	}
	if cfg.Questions.Enabled {
		data, err := ReadSecretFile(cfg.Questions.TokenFile)
		if err != nil {
			return fault.New("question_token_invalid", "题库令牌文件必须存在且仅所有者可读")
		}
		cfg.Questions.Token = strings.TrimSpace(string(data))
	}
	return nil
}
func validateRuntime(cfg *Config) error {
	if cfg.Mode != "production" && cfg.Mode != "development" {
		return invalid("运行模式仅支持 production 或 development")
	}
	if cfg.Mode == "production" && cfg.Auth.Enabled && !cfg.Auth.CookieSecure {
		return invalid("生产模式必须启用安全 Cookie")
	}
	if cfg.Mode == "production" && (cfg.Payments.Enabled || cfg.Payments.ChargingEnabled) && !cfg.Operations.RequireFreshBackup {
		return invalid("生产收费与支付必须启用备份新鲜度保护")
	}
	if cfg.Mode == "development" && (cfg.Payments.Enabled || cfg.Payments.ChargingEnabled || cfg.Learning.ExecutionEnabled) {
		return invalid("开发模式禁止真实付款、自动扣费和学习执行")
	}
	if cfg.Operations.BackupMaxAge < time.Minute || cfg.Operations.BackupMaxAge > 15*time.Minute {
		return invalid("备份可恢复点上限必须在 1 到 15 分钟")
	}
	if cfg.Auth.Enabled {
		if len(cfg.Auth.EncryptionKey) != 32 || len(cfg.Auth.IdentityKey) != 32 || cfg.Auth.EncryptionKeyVersion < 1 || cfg.Auth.EncryptionKeyVersion > 2147483647 {
			return invalid("认证必须配置有效加密版本、32字节加密密钥与独立身份索引密钥")
		}
		if bytes.Equal(cfg.Auth.EncryptionKey, cfg.Auth.IdentityKey) {
			return invalid("身份索引密钥必须独立于加密密钥")
		}
		if len(cfg.Auth.EncryptionKeys) > 8 {
			return invalid("认证解密密钥环最多8个版本")
		}
		for version, key := range cfg.Auth.EncryptionKeys {
			if version < 1 || version > 2147483647 || len(key) != 32 || bytes.Equal(key, cfg.Auth.IdentityKey) {
				return invalid("认证解密密钥环无效或与身份索引密钥混用")
			}
		}
		for _, origin := range []string{cfg.Auth.UserOrigin, cfg.Auth.AdminOrigin} {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
				return invalid("双端 Origin 配置无效")
			}
			if parsed.Scheme != "https" {
				address, e := netip.ParseAddr(parsed.Hostname())
				if cfg.Auth.CookieSecure || parsed.Scheme != "http" || e != nil || !address.IsLoopback() {
					return invalid("安全 Cookie 要求 HTTPS；开发 HTTP 仅允许回环 IP")
				}
			}
		}
		if cfg.Auth.UserOrigin == cfg.Auth.AdminOrigin {
			return invalid("用户和管理员 Origin 必须不同")
		}
		if cfg.Auth.SessionTTL < time.Minute || cfg.Auth.SessionTTL > 30*24*time.Hour {
			return invalid("会话期限必须为 1 分钟到 30 天")
		}
	}
	if cfg.Storage.DataDir == "" {
		return invalid("必须配置数据目录")
	}
	if cfg.Learning.GlobalConcurrency < 1 || cfg.Learning.GlobalConcurrency > 64 || cfg.Learning.AccountConcurrency < 1 || cfg.Learning.AccountConcurrency > cfg.Learning.GlobalConcurrency {
		return invalid("学习并发限额无效")
	}
	if cfg.Learning.Enabled && (!cfg.Auth.Enabled || cfg.Learning.AuthorizationReference == "" || cfg.Learning.SubjectNamespace == "") {
		return invalid("启用平台连接前须配置认证、授权依据和已验证主体命名空间")
	}
	if cfg.Learning.Enabled && (cfg.Learning.SubjectProbeURL == "" || cfg.Learning.SubjectPath == "" || cfg.Learning.SessionExpiryPath == "") {
		return invalid("平台连接须配置已核实的主体查询地址、主体路径和会话到期路径")
	}
	if cfg.Learning.ExecutionEnabled && (cfg.Learning.BrowserExecutable == "" || cfg.Learning.ExecutionEvidenceReference == "") {
		return invalid("启用浏览器执行须配置浏览器和独立的目标验证依据")
	}
	if cfg.Learning.ExecutionEnabled && !cfg.Learning.Enabled {
		return invalid("执行能力要求先启用已授权平台连接")
	}
	if cfg.Payments.Enabled {
		if !cfg.Auth.Enabled || cfg.Payments.MerchantID == "" || cfg.Payments.Key == "" {
			return invalid("支付功能缺少认证或商户配置")
		}
		for _, address := range []string{cfg.Payments.NotifyURL, cfg.Payments.ReturnURL} {
			parsed, err := url.Parse(address)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
				return invalid("支付回调和返回地址必须为有效 HTTPS URL")
			}
		}
	}
	if cfg.Payments.ChargingEnabled && !cfg.Auth.Enabled {
		return invalid("启用收费要求完整认证配置")
	}
	if cfg.Questions.Enabled {
		parsed, err := url.Parse(cfg.Questions.URL)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return invalid("题库服务地址无效")
		}
		if parsed.Scheme != "https" {
			address, e := netip.ParseAddr(parsed.Hostname())
			if parsed.Scheme != "http" || e != nil || !address.IsLoopback() {
				return invalid("题库服务须使用 HTTPS，回环连接可使用 HTTP")
			}
		}
		if !cfg.Auth.Enabled || len(cfg.Questions.Token) < 16 {
			return invalid("题库服务要求启用认证并配置至少 16 字节的服务令牌")
		}
	}
	if cfg.Questions.Timeout < time.Second || cfg.Questions.Timeout > 2*time.Minute || cfg.Questions.Concurrency < 1 || cfg.Questions.Concurrency > 16 {
		return invalid("题库超时或并发设置无效")
	}
	if cfg.Operations.RetentionInterval < time.Minute || cfg.Operations.RetentionInterval > 7*24*time.Hour || cfg.Operations.ExportMaxBytes < 1024 || cfg.Operations.ExportMaxBytes > 32<<20 || cfg.Operations.MaxEventStreams < 1 || cfg.Operations.MaxEventStreams > 1000 {
		return invalid("维护资源限制无效")
	}
	return nil
}
func ReadSecretFile(path string) ([]byte, error) {
	if path == "" {
		return nil, fault.New("config_secret_file_invalid", "未配置私有文件")
	}
	return readLimited(path, 64*1024, true)
}

func loadAuthKeys(cfg *Config) error {
	readKey := func(path string) ([]byte, error) {
		data, err := ReadSecretFile(path)
		if err != nil {
			return nil, fault.New("auth_key_invalid", "认证与身份索引密钥文件必须存在且仅所有者可读")
		}
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != 32 {
			return nil, fault.New("auth_key_invalid", "密钥文件须包含 Base64 编码的32字节密钥")
		}
		return key, nil
	}
	current, err := readKey(cfg.Auth.EncryptionKeyFile)
	if err != nil {
		return err
	}
	identity, err := readKey(cfg.Auth.IdentityKeyFile)
	if err != nil {
		return err
	}
	keys := map[int][]byte{cfg.Auth.EncryptionKeyVersion: current}
	for version, path := range cfg.Auth.DecryptionKeyFiles {
		key, err := readKey(path)
		if err != nil {
			return err
		}
		if existing, ok := keys[version]; ok && !bytes.Equal(existing, key) {
			return invalid("同一加密密钥版本不能关联不同密钥")
		}
		keys[version] = key
	}
	if len(keys) > 8 {
		return invalid("认证解密密钥环最多8个版本")
	}
	cfg.Auth.EncryptionKey = current
	cfg.Auth.EncryptionKeys = keys
	cfg.Auth.IdentityKey = identity
	return nil
}
