// Package postgres 管理原生 PostgreSQL 连接和迁移，不在启动时自动改表。
package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

type Database struct {
	pool         *pgxpool.Pool
	queryTimeout time.Duration
}

func Open(ctx context.Context, cfg config.Database) (*Database, error) {
	if err := config.ValidateDSN(cfg.DSN); err != nil {
		return nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fault.New("database_config_invalid", "数据库连接 URL 无效")
	}
	poolConfig.MaxConns = cfg.MaxConnections
	poolConfig.MinConns = 0
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second
	poolConfig.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	poolConfig.ConnConfig.RuntimeParams["application_name"] = "byebyeocs"
	poolConfig.ConnConfig.RuntimeParams["search_path"] = "pg_catalog,public"
	poolConfig.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fault.New("database_connect_failed", "无法建立数据库连接池")
	}
	db := &Database{pool: pool, queryTimeout: cfg.QueryTimeout}
	checkCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout)
	defer cancel()
	var version int
	if err := pool.QueryRow(checkCtx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil {
		pool.Close()
		return nil, fault.New("database_unavailable", "无法连接 PostgreSQL，请检查网络、凭据与 TLS 配置")
	}
	if version < 160000 {
		pool.Close()
		return nil, fault.New("database_version_unsupported", "基础服务要求 PostgreSQL 16 或更高版本")
	}
	return db, nil
}

func (db *Database) Close() { db.pool.Close() }

// Pool 供显式构造的业务服务共享受控连接池。
func (db *Database) Pool() *pgxpool.Pool { return db.pool }

func (db *Database) Check(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, db.queryTimeout)
	defer cancel()
	status, err := db.Status(checkCtx)
	if err != nil {
		return err
	}
	if !status.Ready {
		return fault.New("schema_pending", "数据库迁移未完成，请先执行 migrate up")
	}
	return nil
}
