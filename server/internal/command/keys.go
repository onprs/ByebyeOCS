package command

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"github.com/onprs/ByebyeOCS/server/internal/auth"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"io"
	"os"
	"path/filepath"
)

func generateKey(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("keys generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	output := flags.String("output", "", "新密钥私有文件")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *output == "" {
		return fault.New("command_invalid", "生成密钥须指定新的 --output 文件")
	}
	path, err := filepath.Abs(*output)
	if err != nil {
		return fault.New("private_file_invalid", "密钥输出路径无效")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fault.New("private_file_write_failed", "无法创建密钥目录")
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	if err := writePrivateExclusive(path, []byte(base64.StdEncoding.EncodeToString(key)+"\n")); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]string{"status": "created", "key_path": path})
}

func reencryptKeys(ctx context.Context, db *postgres.Database, cfg config.Config, stdout io.Writer) error {
	if !cfg.Auth.Enabled {
		return fault.New("not_configured", "密钥重封装要求完整认证配置")
	}
	service, err := auth.New(db.Pool(), auth.Options{CookieSecure: cfg.Auth.CookieSecure, UserOrigin: cfg.Auth.UserOrigin, AdminOrigin: cfg.Auth.AdminOrigin, EncryptionKey: cfg.Auth.EncryptionKey, EncryptionKeyVersion: cfg.Auth.EncryptionKeyVersion, EncryptionKeys: cfg.Auth.EncryptionKeys, IdentityKey: cfg.Auth.IdentityKey})
	if err != nil {
		return err
	}
	result, err := service.ReencryptSecrets(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(result)
}
