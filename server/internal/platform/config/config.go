// Package config 读取维护配置；密钥只从环境变量或受保护文件取得。
package config

import (
	"bytes"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"gopkg.in/yaml.v3"
)

type Server struct {
	UserListen        string        `yaml:"user_listen"`
	AdminListen       string        `yaml:"admin_listen"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
	TrustedProxies    []string      `yaml:"trusted_proxies"`
}

type Database struct {
	DSN            string        `yaml:"-" json:"-"`
	DSNFile        string        `yaml:"dsn_file" json:"-"`
	MaxConnections int32         `yaml:"max_connections"`
	ConnectTimeout time.Duration `yaml:"connect_timeout"`
	QueryTimeout   time.Duration `yaml:"query_timeout"`
}

type Log struct {
	Level string `yaml:"level"`
}

type Config struct {
	Mode       string     `yaml:"mode"`
	Server     Server     `yaml:"server"`
	Database   Database   `yaml:"database"`
	Log        Log        `yaml:"log"`
	Auth       Auth       `yaml:"auth"`
	Storage    Storage    `yaml:"storage"`
	Learning   Learning   `yaml:"learning"`
	Payments   Payments   `yaml:"payments"`
	Questions  Questions  `yaml:"questions"`
	Operations Operations `yaml:"operations"`
}

type Overrides struct {
	// 指针区分未提供与显式空值。
	UserListen, AdminListen, LogLevel, DSNFile *string
}

type LookupEnv func(string) (string, bool)

func Defaults() Config {
	cfg := Config{
		Server: Server{
			UserListen: "127.0.0.1:8090", AdminListen: "127.0.0.1:8091",
			ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
			WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute,
			ShutdownTimeout: 10 * time.Second,
		},
		Database: Database{MaxConnections: 12, ConnectTimeout: 5 * time.Second, QueryTimeout: 3 * time.Second},
		Log:      Log{Level: "info"},
	}
	runtimeDefaults(&cfg)
	return cfg
}

func Load(path string, lookup LookupEnv, overrides Overrides) (Config, error) {
	cfg := Defaults()
	if path != "" {
		data, err := readLimited(path, 64*1024, false)
		if err != nil {
			return Config{}, fault.New("config_read_failed", "无法读取配置文件（上限 64 KiB）")
		}
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&cfg); err != nil {
			return Config{}, fault.New("config_invalid", "配置格式错误或包含未知字段，请核对示例配置")
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return Config{}, fault.New("config_invalid", "配置文件必须仅包含一个 YAML 文档")
		}
		resolveRuntimeFiles(&cfg, path)
		if cfg.Database.DSNFile != "" && !filepath.IsAbs(cfg.Database.DSNFile) {
			cfg.Database.DSNFile = filepath.Join(filepath.Dir(path), cfg.Database.DSNFile)
		}
	}
	for _, entry := range []struct {
		key    string
		target *string
	}{
		{"BYEBYEOCS_USER_LISTEN", &cfg.Server.UserListen},
		{"BYEBYEOCS_ADMIN_LISTEN", &cfg.Server.AdminListen},
		{"BYEBYEOCS_LOG_LEVEL", &cfg.Log.Level},
		{"BYEBYEOCS_DATABASE_DSN_FILE", &cfg.Database.DSNFile},
	} {
		if value, ok := lookup(entry.key); ok {
			*entry.target = value
		}
	}
	if value, ok := lookup("BYEBYEOCS_DATABASE_DSN"); ok {
		if _, also := lookup("BYEBYEOCS_DATABASE_DSN_FILE"); also && overrides.DSNFile == nil {
			return Config{}, fault.New("config_secret_conflict", "数据库连接环境变量与连接文件环境变量只能设置一个")
		}
		cfg.Database.DSN = value
		cfg.Database.DSNFile = ""
	}
	for _, entry := range []struct {
		value  *string
		target *string
	}{
		{overrides.UserListen, &cfg.Server.UserListen},
		{overrides.AdminListen, &cfg.Server.AdminListen},
		{overrides.LogLevel, &cfg.Log.Level},
	} {
		if entry.value != nil {
			*entry.target = *entry.value
		}
	}
	if overrides.DSNFile != nil {
		cfg.Database.DSNFile = *overrides.DSNFile
		cfg.Database.DSN = ""
	}
	if cfg.Database.DSNFile != "" {
		data, err := readLimited(cfg.Database.DSNFile, 16*1024, true)
		if err != nil {
			return Config{}, fault.New("config_secret_file_invalid", "数据库连接文件必须可读、为普通文件、仅所有者可访问且不超过 16 KiB")
		}
		cfg.Database.DSN = strings.TrimSpace(string(data))
	}
	if err := applyRuntimeEnv(&cfg, lookup); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg *Config) Validate() error {
	if len(cfg.Server.TrustedProxies) > 8 {
		return invalid("受信任代理范围最多8条")
	}
	for _, value := range cfg.Server.TrustedProxies {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Bits() == 0 {
			return invalid("受信任代理必须为明确的非全网CIDR范围")
		}
	}
	for _, listen := range []*string{&cfg.Server.UserListen, &cfg.Server.AdminListen} {
		host, port, err := net.SplitHostPort(*listen)
		if err != nil {
			return invalid("监听地址必须是回环 IP 和端口")
		}
		address, err := netip.ParseAddr(host)
		if err != nil || !address.IsLoopback() {
			return invalid("当前基础服务仅接受回环 IP 监听")
		}
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return invalid("监听端口必须为 1 到 65535")
		}
		*listen = net.JoinHostPort(address.Unmap().String(), strconv.Itoa(number))
	}
	if cfg.Server.UserListen == cfg.Server.AdminListen {
		return invalid("用户与管理员监听地址必须不同")
	}
	for _, duration := range []time.Duration{
		cfg.Server.ReadHeaderTimeout, cfg.Server.ReadTimeout, cfg.Server.WriteTimeout,
		cfg.Server.IdleTimeout, cfg.Server.ShutdownTimeout,
		cfg.Database.ConnectTimeout, cfg.Database.QueryTimeout,
	} {
		if duration < time.Millisecond || duration > 5*time.Minute {
			return invalid("超时配置必须在 1ms 到 5m 之间")
		}
	}
	if cfg.Server.ReadTimeout < cfg.Server.ReadHeaderTimeout {
		return invalid("读取超时不能小于请求头读取超时")
	}
	if cfg.Database.MaxConnections < 1 || cfg.Database.MaxConnections > 100 {
		return invalid("数据库连接数必须在 1 到 100 之间")
	}
	switch cfg.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return invalid("日志级别仅支持 debug、info、warn、error")
	}
	if err := validateRuntime(cfg); err != nil {
		return err
	}
	return ValidateDSN(cfg.Database.DSN)
}

func ValidateDSN(dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") ||
		u.Hostname() == "" || u.User == nil || u.User.Username() == "" ||
		len(u.Path) < 2 || u.Fragment != "" {
		return invalid("请提供包含主机、用户和数据库名的 PostgreSQL 连接 URL")
	}
	values, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return invalid("数据库连接 URL 参数无效")
	}
	// 禁止通过查询参数覆盖已核对的主机和身份，避免 TLS 校验与实际连接目标不一致。
	for _, key := range []string{"host", "hostaddr", "port", "user", "password", "dbname", "service", "servicefile"} {
		if values.Has(key) {
			return invalid("数据库连接身份和目标必须写在 URL 中")
		}
	}
	modes := values["sslmode"]
	if len(modes) != 1 {
		return invalid("数据库连接 URL 必须显式设置一次 sslmode")
	}
	if modes[0] == "verify-full" {
		return nil
	}
	address, err := netip.ParseAddr(u.Hostname())
	if modes[0] == "disable" && err == nil && address.IsLoopback() {
		return nil
	}
	return invalid("远程数据库必须使用 sslmode=verify-full；回环 IP 可显式使用 disable")
}

func readLimited(path string, limit int64, private bool) ([]byte, error) {
	// 打开前排除管道和设备，避免配置读取等待无关输入。打开后再次核对实际文件。
	before, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fault.New("file_invalid", "配置必须为普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit || (private && info.Mode().Perm()&0077 != 0) {
		return nil, fault.New("file_invalid", "文件类型、大小或权限不符合要求")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, fault.New("file_invalid", "读取文件失败")
	}
	return data, nil
}

func invalid(message string) error { return fault.New("config_invalid", message) }
