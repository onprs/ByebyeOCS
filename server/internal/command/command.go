// Package command 提供维护命令；所有输出均使用经过筛选的安全字段。
package command

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log/slog"
	"time"

	"github.com/onprs/ByebyeOCS/server/internal/app"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
)

var Version = "dev"
var Commit = "unknown"
var BuildTime = "unknown"

func Run(ctx context.Context, args []string, lookup config.LookupEnv, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		if len(args) > 1 {
			return fault.New("command_invalid", "帮助命令不接受额外参数")
		}
		_, err := io.WriteString(stdout, helpText)
		return err
	}
	command := args[0]
	remaining := args[1:]
	if command == "migrate" || command == "config" || command == "admin" || command == "backup" || command == "keys" {
		if len(remaining) == 0 {
			return fault.New("command_invalid", "请指定维护子命令，使用 help 查看用法")
		}
		command += " " + remaining[0]
		remaining = remaining[1:]
	}
	switch command {
	case "version":
		if len(remaining) != 0 {
			return fault.New("command_invalid", "version 不接受额外参数")
		}
		return json.NewEncoder(stdout).Encode(map[string]string{"version": Version, "commit": Commit, "build_time": BuildTime})
	case "config init":
		return initializeConfig(remaining, stdout)
	case "keys generate":
		return generateKey(remaining, stdout)
	case "serve", "migrate up", "migrate status", "config check", "doctor", "admin create", "keys reencrypt":
	default:
		return fault.New("command_invalid", "命令不存在，使用 help 查看用法")
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "", "配置文件")
	userListen := flags.String("user-listen", "", "用户端监听地址")
	adminListen := flags.String("admin-listen", "", "管理员监听地址")
	logLevel := flags.String("log-level", "", "日志级别")
	dsnFile := flags.String("database-dsn-file", "", "数据库连接文件")
	var username, passwordFile, totpSecretFile, totpOutputFile string
	if command == "admin create" {
		flags.StringVar(&username, "username", "", "管理员用户名")
		flags.StringVar(&passwordFile, "password-file", "", "私有密码文件")
		flags.StringVar(&totpSecretFile, "totp-secret-file", "", "已有 TOTP 私有文件")
		flags.StringVar(&totpOutputFile, "totp-output-file", "", "新的 TOTP 私有配置输出文件")
	}
	if err := flags.Parse(remaining); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, err = io.WriteString(stdout, helpText)
			return err
		}
		return fault.New("command_invalid", "命令参数无效，使用 help 查看用法")
	}
	if flags.NArg() != 0 {
		return fault.New("command_invalid", "命令包含多余的位置参数")
	}
	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	if !visited["config"] {
		if value, ok := lookup("BYEBYEOCS_CONFIG"); ok {
			*configPath = value
		}
	}
	overrides := config.Overrides{}
	if visited["user-listen"] {
		overrides.UserListen = userListen
	}
	if visited["admin-listen"] {
		overrides.AdminListen = adminListen
	}
	if visited["log-level"] {
		overrides.LogLevel = logLevel
	}
	if visited["database-dsn-file"] {
		overrides.DSNFile = dsnFile
	}
	cfg, err := config.Load(*configPath, lookup, overrides)
	if err != nil {
		return err
	}
	if command == "config check" {
		return json.NewEncoder(stdout).Encode(map[string]any{
			"status": "ok", "user_listen": cfg.Server.UserListen, "admin_listen": cfg.Server.AdminListen,
			"database_configured": true, "database_max_connections": cfg.Database.MaxConnections, "log_level": cfg.Log.Level, "mode": cfg.Mode, "authentication_enabled": cfg.Auth.Enabled, "platform_enabled": cfg.Learning.Enabled, "execution_enabled": cfg.Learning.ExecutionEnabled, "payments_enabled": cfg.Payments.Enabled, "charging_enabled": cfg.Payments.ChargingEnabled, "question_service_enabled": cfg.Questions.Enabled,
		})
	}
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Log.Level))
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: level}))
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer db.Close()
	switch command {
	case "migrate up":
		migrationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		status, err := db.Migrate(migrationCtx)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(status)
	case "migrate status":
		status, err := db.Status(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(status)
	case "keys reencrypt":
		if err := db.Check(ctx); err != nil {
			return err
		}
		return reencryptKeys(ctx, db, cfg, stdout)
	case "admin create":
		if err := db.Check(ctx); err != nil {
			return err
		}
		return createAdmin(ctx, db, cfg, username, passwordFile, totpSecretFile, totpOutputFile, stdout)
	case "doctor":
		if err := db.Check(ctx); err != nil {
			return err
		}
		return json.NewEncoder(stdout).Encode(map[string]string{"status": "ok", "database": "ok", "schema": "current"})
	case "serve":
		if err := db.Check(ctx); err != nil {
			return err
		}
		runtime, err := app.NewRuntime(cfg, db, logger)
		if err != nil {
			return err
		}
		return runtime.Run(ctx)
	}
	return fault.New("internal_error", "无法执行命令")
}

func WriteError(writer io.Writer, err error) {
	code, message := fault.Describe(err)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code, "message": message})
}

const helpText = `ByebyeOCS 服务与维护命令

用法：
  byebyeocs config init [参数]      生成新配置、加密密钥和独立身份索引密钥，不覆盖已有文件
  byebyeocs serve [参数]            启动双端服务，要求迁移已完成
  byebyeocs migrate up [参数]       显式执行内置数据库迁移
  byebyeocs migrate status [参数]   只读检查数据库迁移
  byebyeocs config check [参数]     校验配置，不连接数据库
  byebyeocs doctor [参数]           只读检查数据库连接和迁移
  byebyeocs admin create [参数]     通过私有文件创建管理员并配置 TOTP
  byebyeocs keys generate [参数]    生成新的私有加密密钥文件
  byebyeocs keys reencrypt [参数]   按当前版本重封装密文，保留用户及钱包归属
  byebyeocs version                显示构建版本

参数：
  --config PATH                    YAML 配置文件
  --user-listen IP:PORT            用户端回环监听地址
  --admin-listen IP:PORT           管理员回环监听地址
  --log-level LEVEL                debug / info / warn / error
  --database-dsn-file PATH          仅所有者可读的 PostgreSQL 连接文件

初始化：
  config init --development --output PATH --database-dsn-file PATH
  生产初始化使用 --user-origin HTTPS_ORIGIN --admin-origin HTTPS_ORIGIN
  可指定 --key-output、--identity-key-output、--data-dir、--admin-assets-dir、--client-assets-dir。
管理员：
  admin create --config PATH --username NAME --password-file PATH
    --totp-output-file NEW_PATH 或 --totp-secret-file EXISTING_PATH

优先级：命令行 > BYEBYEOCS_* 环境变量 > YAML 文件 > 内置默认值。
数据库连接只从 BYEBYEOCS_DATABASE_DSN 或连接文件读取。
部署与密钥轮换见 docs/deployment/README.md。
`
