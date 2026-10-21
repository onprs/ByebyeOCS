package command

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"gopkg.in/yaml.v3"
)

func initializeConfig(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("config init", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "byebyeocs.yaml", "新配置文件")
	keyOutput := flags.String("key-output", "", "新认证密钥文件")
	identityOutput := flags.String("identity-key-output", "", "独立身份索引密钥文件")
	development := flags.Bool("development", false, "生成仅回环开发配置")
	userOrigin := flags.String("user-origin", "", "用户端Origin")
	adminOrigin := flags.String("admin-origin", "", "管理员Origin")
	dsnFile := flags.String("database-dsn-file", "", "PostgreSQL连接文件")
	dataDir := flags.String("data-dir", "", "数据目录")
	adminAssets := flags.String("admin-assets-dir", "apps/admin/dist", "管理员静态目录")
	clientAssets := flags.String("client-assets-dir", "apps/client_flutter/build/web", "Flutter Web静态目录")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return fault.New("command_invalid", "初始化参数无效，使用 help 查看说明")
	}
	if !*development && (*userOrigin == "" || *adminOrigin == "") {
		return fault.New("command_invalid", "生产初始化须提供两个独立的 HTTPS Origin；本机开发使用 --development")
	}
	path, err := filepath.Abs(*output)
	if err != nil {
		return fault.New("config_invalid", "配置输出路径无效")
	}
	directory := filepath.Dir(path)
	if *keyOutput == "" {
		*keyOutput = filepath.Join(directory, "secrets", "application-v1.key")
	}
	keyPath, err := filepath.Abs(*keyOutput)
	if err != nil {
		return fault.New("config_invalid", "密钥路径无效")
	}
	if *identityOutput == "" {
		*identityOutput = filepath.Join(directory, "secrets", "identity.key")
	}
	identityPath, err := filepath.Abs(*identityOutput)
	if err != nil {
		return fault.New("config_invalid", "身份索引密钥路径无效")
	}
	if keyPath == path || identityPath == path || identityPath == keyPath {
		return fault.New("config_invalid", "配置、加密密钥与身份索引密钥路径必须不同")
	}
	cfg := config.Defaults()
	cfg.Auth.Enabled = true
	cfg.Auth.EncryptionKeyFile = relative(directory, keyPath)
	cfg.Auth.IdentityKeyFile = relative(directory, identityPath)
	if *development {
		cfg.Mode = "development"
		cfg.Auth.CookieSecure = false
		cfg.Operations.RequireFreshBackup = false
	}
	if *userOrigin != "" {
		cfg.Auth.UserOrigin = *userOrigin
	}
	if *adminOrigin != "" {
		cfg.Auth.AdminOrigin = *adminOrigin
	}
	if *dsnFile != "" {
		dsnPath, err := filepath.Abs(*dsnFile)
		if err != nil {
			return fault.New("config_invalid", "数据库连接文件路径无效")
		}
		cfg.Database.DSNFile = relative(directory, dsnPath)
	} else {
		cfg.Database.DSNFile = "secrets/database.dsn"
	}
	if *dataDir != "" {
		dir, err := filepath.Abs(*dataDir)
		if err != nil {
			return fault.New("config_invalid", "数据目录路径无效")
		}
		cfg.Storage.DataDir = dir
	} else {
		cfg.Storage.DataDir = "data"
	}
	for _, entry := range []struct {
		input  string
		target *string
	}{{*adminAssets, &cfg.Storage.AdminAssetsDir}, {*clientAssets, &cfg.Storage.ClientAssetsDir}} {
		if entry.input != "" {
			path, err := filepath.Abs(entry.input)
			if err != nil {
				return fault.New("config_invalid", "静态目录路径无效")
			}
			*entry.target = relative(directory, path)
		}
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	cfg.Auth.EncryptionKey = key
	identityKey := make([]byte, 32)
	_, _ = rand.Read(identityKey)
	cfg.Auth.IdentityKey = identityKey
	cfg.Database.DSN = "postgres://validation@127.0.0.1/validation?sslmode=disable"
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.Database.DSN = ""
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		return fault.New("config_invalid", "无法编码配置")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return fault.New("config_write_failed", "无法创建配置目录")
	}
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return fault.New("config_write_failed", "无法创建密钥目录")
	}
	if err := os.MkdirAll(filepath.Dir(identityPath), 0700); err != nil {
		return fault.New("config_write_failed", "无法创建身份索引密钥目录")
	}
	if _, err := os.Lstat(path); err == nil {
		return fault.New("conflict", "配置文件已存在，未覆盖")
	}
	if err := writePrivateExclusive(keyPath, []byte(base64.StdEncoding.EncodeToString(key)+"\n")); err != nil {
		return err
	}
	if err := writePrivateExclusive(identityPath, []byte(base64.StdEncoding.EncodeToString(identityKey)+"\n")); err != nil {
		_ = os.Remove(keyPath)
		return err
	}
	if err := writePrivateExclusive(path, encoded); err != nil {
		_ = os.Remove(keyPath)
		_ = os.Remove(identityPath)
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"status": "created", "mode": cfg.Mode, "config_path": path, "key_path": keyPath, "identity_key_path": identityPath, "database_verified": false, "message": "请配置专用 PostgreSQL 连接文件，再执行 migrate up 和 admin create"})
}
func relative(base, path string) string {
	value, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return value
}
func writePrivateExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fault.New("private_file_write_failed", "无法创建私有文件，现有文件不会被覆盖")
	}
	done := false
	defer func() {
		if !done {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(data); err != nil {
		file.Close()
		return fault.New("private_file_write_failed", "私有文件写入失败")
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fault.New("private_file_write_failed", "私有文件保存失败")
	}
	if err := file.Close(); err != nil {
		return err
	}
	done = true
	return nil
}
func createAdmin(ctx context.Context, db *postgres.Database, cfg config.Config, username, passwordFile, secretFile, enrollmentFile string, stdout io.Writer) error {
	if !cfg.Auth.Enabled {
		return fault.New("not_configured", "请先启用认证并配置应用加密密钥")
	}
	if username == "" || passwordFile == "" || (secretFile == "" && enrollmentFile == "") || (secretFile != "" && enrollmentFile != "") {
		return fault.New("command_invalid", "管理员创建要求用户名、密码文件，以及 TOTP 密钥文件或新的配置输出文件二选一")
	}
	password, err := config.ReadSecretFile(passwordFile)
	if err != nil {
		return fault.New("private_file_invalid", "无法读取仅所有者可读的密码文件")
	}
	totp := ""
	if secretFile != "" {
		data, err := config.ReadSecretFile(secretFile)
		if err != nil {
			return fault.New("private_file_invalid", "无法读取 TOTP 私有文件")
		}
		totp = strings.TrimSpace(string(data))
	}
	options := auth.Options{CookieSecure: cfg.Auth.CookieSecure, UserOrigin: cfg.Auth.UserOrigin, AdminOrigin: cfg.Auth.AdminOrigin, EncryptionKey: cfg.Auth.EncryptionKey, IdentityKey: cfg.Auth.IdentityKey, EncryptionKeyVersion: cfg.Auth.EncryptionKeyVersion, EncryptionKeys: cfg.Auth.EncryptionKeys, SessionTTL: cfg.Auth.SessionTTL}
	if enrollmentFile != "" {
		options.Enrollment = func(name, secret string) error {
			parameters := url.Values{"secret": {secret}, "issuer": {"ByebyeOCS"}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
			uri := url.URL{Scheme: "otpauth", Host: "totp", Path: "/ByebyeOCS:" + name, RawQuery: parameters.Encode()}
			encoded, err := json.MarshalIndent(map[string]string{"username": name, "totp_secret": secret, "otpauth_uri": uri.String()}, "", "  ")
			if err != nil {
				return fault.New("enrollment_failed", "无法准备双重验证配置")
			}
			if err := os.MkdirAll(filepath.Dir(enrollmentFile), 0700); err != nil {
				return fault.New("enrollment_failed", "无法创建双重验证配置目录")
			}
			return writePrivateExclusive(enrollmentFile, encoded)
		}
	}
	service, err := auth.New(db.Pool(), options)
	if err != nil {
		return err
	}
	pass := strings.TrimSuffix(strings.TrimSuffix(string(password), "\n"), "\r")
	if err := service.CreateAdmin(ctx, username, pass, totp); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]string{"status": "created", "username": username, "totp_setup_path": enrollmentFile})
}
