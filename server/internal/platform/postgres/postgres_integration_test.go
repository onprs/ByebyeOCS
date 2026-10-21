//go:build integration

package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/fault"
)

func isolatedDatabase(t *testing.T) (*Database, config.Database) {
	t.Helper()
	secretFile := os.Getenv("BYEBYEOCS_TEST_DATABASE_DSN_FILE")
	if secretFile == "" {
		t.Fatal("集成测试要求显式设置 BYEBYEOCS_TEST_DATABASE_DSN_FILE，且连接到可创建数据库的隔离测试实例")
	}
	cfg, err := config.Load("", func(string) (string, bool) { return "", false }, config.Overrides{DSNFile: &secretFile})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, cfg.Database.DSN)
	if err != nil {
		t.Fatal("无法连接隔离 PostgreSQL 实例")
	}
	var random [8]byte
	_, _ = rand.Read(random[:])
	databaseName := "byebyeocs_it_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{databaseName}.Sanitize()); err != nil {
		admin.Close(context.Background())
		t.Fatal("无法创建临时数据库；测试角色需要 CREATEDB 权限")
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+pgx.Identifier{databaseName}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("清理临时数据库失败")
		}
		admin.Close(cleanupCtx)
	})
	connection, err := url.Parse(cfg.Database.DSN)
	if err != nil {
		t.Fatal("测试连接配置无效")
	}
	connection.Path = "/" + databaseName
	connection.RawPath = ""
	cfg.Database.DSN = connection.String()
	db, err := Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db, cfg.Database
}

func TestMigrationLifecycle(t *testing.T) {
	migrations, err := catalog(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(len(migrations))
	db, _ := isolatedDatabase(t)
	ctx := context.Background()
	status, err := db.Status(ctx)
	if err != nil || status.Current != 0 || status.Ready || int64(len(status.Migrations)) != expected {
		t.Fatal("空库状态错误")
	}
	var tableExists bool
	if err := db.pool.QueryRow(ctx, "SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&tableExists); err != nil || tableExists {
		t.Fatal("只读状态检查隐式创建了迁移表")
	}
	if err := db.Check(ctx); err == nil {
		t.Fatal("未迁移的数据库被标为就绪")
	}
	for range 2 {
		status, err = db.Migrate(ctx)
		if err != nil || !status.Ready || status.Current != expected {
			t.Fatal("迁移或重复迁移失败", err)
		}
	}
	if err := db.Check(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM public.schema_migrations").Scan(&count); err != nil || int64(count) != expected {
		t.Fatal("迁移记录不幂等")
	}
	for _, table := range []string{"users", "admin_users", "user_preferences", "admin_preferences", "audit_events"} {
		if err := db.pool.QueryRow(ctx, "SELECT count(*) FROM public."+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil || count != 0 {
			t.Fatal("初始迁移生成了业务数据或表缺失")
		}
	}
}

func TestConcurrentMigrations(t *testing.T) {
	db, _ := isolatedDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); _, err := db.Migrate(ctx); results <- err }()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestFailedMigrationRollsBackWholeBatch(t *testing.T) {
	db, _ := isolatedDatabase(t)
	files := fstest.MapFS{
		"migrations/000001_first.sql":   {Data: []byte("CREATE TABLE public.rollback_probe (id integer PRIMARY KEY);")},
		"migrations/000002_failure.sql": {Data: []byte("SELECT * FROM public.intentionally_missing_table;")},
	}
	if _, err := db.migrate(context.Background(), files); err == nil {
		t.Fatal("无效迁移意外成功")
	}
	var count int
	if err := db.pool.QueryRow(context.Background(), "SELECT count(*) FROM pg_tables WHERE schemaname = 'public'").Scan(&count); err != nil || count != 0 {
		t.Fatal("失败迁移留下了表或迁移记录")
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal("回滚后不能重新迁移", err)
	}
}

func TestIncompatibleMigrationRecords(t *testing.T) {
	migrations, err := catalog(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, mutation string }{
		{"校验和更改", "UPDATE public.schema_migrations SET checksum = repeat('0', 64) WHERE version = 1"},
		{"名称更改", "UPDATE public.schema_migrations SET name = 'modified' WHERE version = 1"},
		{"迁移缺失", "DELETE FROM public.schema_migrations WHERE version = 1"},
		{"未来版本", fmt.Sprintf("INSERT INTO public.schema_migrations (version, name, checksum) VALUES (%d, 'future', repeat('0', 64))", len(migrations)+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, _ := isolatedDatabase(t)
			if _, err := db.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.pool.Exec(context.Background(), test.mutation); err != nil {
				t.Fatal("测试不能修改迁移记录")
			}
			for _, operation := range []func() error{
				func() error { return db.Check(context.Background()) },
				func() error { _, err := db.Migrate(context.Background()); return err },
			} {
				err := operation()
				code, _ := fault.Describe(err)
				if err == nil || code != "schema_incompatible" {
					t.Fatal("未拒绝不兼容数据库")
				}
			}
		})
	}
}

func TestCancelledMigrationReleasesLock(t *testing.T) {
	db, cfg := isolatedDatabase(t)
	blocker, err := pgx.Connect(context.Background(), cfg.DSN)
	if err != nil {
		t.Fatal("无法建立迁移锁测试连接")
	}
	defer blocker.Close(context.Background())
	transaction, err := blocker.Begin(context.Background())
	if err != nil {
		t.Fatal("无法开启迁移锁测试事务")
	}
	defer transaction.Rollback(context.Background())
	if _, err := transaction.Exec(context.Background(), "SELECT pg_advisory_xact_lock(726239184001)"); err != nil {
		t.Fatal("无法持有测试锁")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := db.Migrate(ctx); err == nil {
		t.Fatal("迁移锁等待未响应取消")
	}
	if err := transaction.Rollback(context.Background()); err != nil {
		t.Fatal("无法释放测试锁")
	}
	if _, err := db.Migrate(context.Background()); err != nil {
		t.Fatal("取消后迁移锁未释放", err)
	}
}

func TestIdentityPreferenceAndAuditConstraints(t *testing.T) {
	db, _ := isolatedDatabase(t)
	ctx := context.Background()
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	userID := "00000000-0000-4000-8000-000000000001"
	adminID := "00000000-0000-4000-8000-000000000002"
	if _, err := db.pool.Exec(ctx, "INSERT INTO public.users (id) VALUES ($1)", userID); err != nil {
		t.Fatal("无法建立合成用户")
	}
	if _, err := db.pool.Exec(ctx, "INSERT INTO public.admin_users (id, username, password_hash) VALUES ($1, 'operator', 'synthetic-hash')", adminID); err != nil {
		t.Fatal("无法建立合成管理员")
	}
	if _, err := db.pool.Exec(ctx, "INSERT INTO public.user_preferences (user_id) VALUES ($1)", userID); err != nil {
		t.Fatal("无法建立用户偏好")
	}
	if _, err := db.pool.Exec(ctx, "INSERT INTO public.admin_preferences (admin_id) VALUES ($1)", adminID); err != nil {
		t.Fatal("无法建立管理员偏好")
	}
	var scheme string
	if err := db.pool.QueryRow(ctx, "SELECT color_scheme FROM public.user_preferences WHERE user_id = $1", userID).Scan(&scheme); err != nil || scheme != "graphite" {
		t.Fatal("用户偏好默认值错误")
	}
	if _, err := db.pool.Exec(ctx, "UPDATE public.user_preferences SET color_scheme = 'grove' WHERE user_id = $1", userID); err != nil {
		t.Fatal("用户偏好更新失败")
	}
	if err := db.pool.QueryRow(ctx, "SELECT color_scheme FROM public.admin_preferences WHERE admin_id = $1", adminID).Scan(&scheme); err != nil || scheme != "graphite" {
		t.Fatal("两端偏好没有隔离")
	}
	for _, sql := range []string{
		"UPDATE public.user_preferences SET color_scheme = 'unknown'",
		"UPDATE public.admin_preferences SET version = 0",
		"INSERT INTO public.user_preferences (user_id) VALUES ('00000000-0000-4000-8000-000000000099')",
		"INSERT INTO public.audit_events (id, actor_kind, request_id, action, resource_type) VALUES ('00000000-0000-4000-8000-000000000003', 'admin', 'req_00000000000000000000000000000000', 'settings.updated', 'settings')",
	} {
		if _, err := db.pool.Exec(ctx, sql); err == nil {
			t.Fatal("数据库约束未拒绝错误数据")
		}
	}
	if _, err := db.pool.Exec(ctx, "INSERT INTO public.audit_events (id, actor_kind, request_id, action, resource_type) VALUES ('00000000-0000-4000-8000-000000000003', 'system', 'req_00000000000000000000000000000000', 'service.started', 'service')"); err != nil {
		t.Fatal("系统审计事件写入失败")
	}
	for _, sql := range []string{"UPDATE public.audit_events SET reason = 'changed'", "DELETE FROM public.audit_events", "TRUNCATE public.audit_events"} {
		if _, err := db.pool.Exec(ctx, sql); err == nil {
			t.Fatal("允许修改或清空审计事件")
		}
	}
}
