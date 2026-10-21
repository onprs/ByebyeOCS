package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version             int64
	Name, Checksum, SQL string
}

type MigrationStatus struct {
	Version int64  `json:"version"`
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
}

type Status struct {
	Ready      bool              `json:"ready"`
	Current    int64             `json:"current"`
	Required   int64             `json:"required"`
	Migrations []MigrationStatus `json:"migrations"`
}

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func catalog(files fs.FS) ([]migration, error) {
	entries, err := fs.Glob(files, "migrations/*.sql")
	if err != nil || len(entries) == 0 {
		return nil, fault.New("migration_catalog_invalid", "程序未包含有效迁移")
	}
	result := make([]migration, 0, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(path.Base(entry), ".sql")
		prefix, _, found := strings.Cut(name, "_")
		version, err := strconv.ParseInt(prefix, 10, 64)
		if !found || err != nil || version < 1 {
			return nil, fault.New("migration_catalog_invalid", "迁移文件编号无效")
		}
		data, err := fs.ReadFile(files, entry)
		if err != nil {
			return nil, fault.New("migration_catalog_invalid", "无法读取内置迁移")
		}
		sum := sha256.Sum256(data)
		result = append(result, migration{Version: version, Name: name, Checksum: hex.EncodeToString(sum[:]), SQL: string(data)})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Version < result[j].Version })
	for i := range result {
		if result[i].Version != int64(i+1) {
			return nil, fault.New("migration_catalog_invalid", "迁移编号必须从 1 开始连续递增")
		}
	}
	return result, nil
}

func (db *Database) Status(ctx context.Context) (Status, error) {
	queryCtx, cancel := context.WithTimeout(ctx, db.queryTimeout)
	defer cancel()
	return inspect(queryCtx, db.pool, migrationFiles)
}

func inspect(ctx context.Context, query querier, files fs.FS) (Status, error) {
	migrations, err := catalog(files)
	if err != nil {
		return Status{}, err
	}
	var exists bool
	if err := query.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return Status{}, fault.New("database_unavailable", "无法读取数据库迁移状态")
	}
	applied := make([]migration, 0)
	if exists {
		rows, err := query.Query(ctx, "SELECT version, name, checksum FROM public.schema_migrations ORDER BY version")
		if err != nil {
			return Status{}, fault.New("migration_metadata_invalid", "无法读取迁移记录，请检查 schema_migrations 结构和权限")
		}
		defer rows.Close()
		for rows.Next() {
			var item migration
			if err := rows.Scan(&item.Version, &item.Name, &item.Checksum); err != nil {
				return Status{}, fault.New("migration_metadata_invalid", "迁移记录格式无效")
			}
			applied = append(applied, item)
		}
		if err := rows.Err(); err != nil {
			return Status{}, fault.New("database_unavailable", "读取迁移记录失败")
		}
	}
	if len(applied) > len(migrations) {
		return Status{}, fault.New("schema_incompatible", "数据库版本高于当前程序，拒绝运行")
	}
	for i, item := range applied {
		if item.Version != migrations[i].Version || item.Name != migrations[i].Name || item.Checksum != migrations[i].Checksum {
			return Status{}, fault.New("schema_incompatible", "迁移编号、名称或校验和与程序不一致，拒绝运行")
		}
	}
	status := Status{Ready: len(applied) == len(migrations), Required: migrations[len(migrations)-1].Version, Migrations: make([]MigrationStatus, 0, len(migrations))}
	if len(applied) > 0 {
		status.Current = applied[len(applied)-1].Version
	}
	for i, item := range migrations {
		status.Migrations = append(status.Migrations, MigrationStatus{Version: item.Version, Name: item.Name, Applied: i < len(applied)})
	}
	return status, nil
}

func (db *Database) Migrate(ctx context.Context) (Status, error) {
	return db.migrate(ctx, migrationFiles)
}

func (db *Database) migrate(ctx context.Context, files fs.FS) (Status, error) {
	// 整批迁移共享事务锁；进程退出、失败或上下文取消时 PostgreSQL 自动释放锁。
	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return Status{}, fault.New("migration_begin_failed", "无法开启迁移事务")
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(726239184001)"); err != nil {
		return Status{}, fault.New("migration_lock_failed", "无法取得迁移锁，操作已停止")
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS public.schema_migrations (
		version bigint PRIMARY KEY CHECK (version > 0),
		name text NOT NULL,
		checksum text NOT NULL CHECK (length(checksum) = 64),
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return Status{}, fault.New("migration_metadata_invalid", "无法创建迁移记录表")
	}
	status, err := inspect(ctx, tx, files)
	if err != nil {
		return Status{}, err
	}
	migrations, err := catalog(files)
	if err != nil {
		return Status{}, err
	}
	for _, item := range migrations {
		if item.Version <= status.Current {
			continue
		}
		if _, err := tx.Exec(ctx, item.SQL); err != nil {
			return Status{}, fault.New("migration_apply_failed", fmt.Sprintf("迁移 %06d 执行失败，整批变更已回滚", item.Version))
		}
		if _, err := tx.Exec(ctx, "INSERT INTO public.schema_migrations (version, name, checksum) VALUES ($1, $2, $3)", item.Version, item.Name, item.Checksum); err != nil {
			return Status{}, fault.New("migration_record_failed", "写入迁移记录失败，整批变更已回滚")
		}
	}
	result, err := inspect(ctx, tx, files)
	if err != nil {
		return Status{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Status{}, fault.New("migration_commit_unknown", "迁移提交结果待核对，请重新执行 migrate status")
	}
	return result, nil
}
