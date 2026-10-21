// Package testsupport 为集成测试创建并清理独立 PostgreSQL 数据库。
package testsupport

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onprs/ByebyeOCS/server/internal/platform/config"
	"github.com/onprs/ByebyeOCS/server/internal/platform/postgres"
	"github.com/onprs/ByebyeOCS/server/internal/web"
	"net/url"
	"os"
	"testing"
	"time"
)

func NewDatabase(t *testing.T) *postgres.Database           { t.Helper(); return newDatabase(t, true) }
func NewUnmigratedDatabase(t *testing.T) *postgres.Database { t.Helper(); return newDatabase(t, false) }
func NewPool(t *testing.T) *pgxpool.Pool                    { t.Helper(); return NewDatabase(t).Pool() }
func newDatabase(t *testing.T, migrate bool) *postgres.Database {
	t.Helper()
	path := os.Getenv("BYEBYEOCS_TEST_DATABASE_DSN_FILE")
	if path == "" {
		t.Fatal("集成测试要求显式提供隔离 PostgreSQL 连接文件 BYEBYEOCS_TEST_DATABASE_DSN_FILE")
	}
	cfg, err := config.Load("", func(string) (string, bool) { return "", false }, config.Overrides{DSNFile: &path})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, cfg.Database.DSN)
	if err != nil {
		t.Fatal("无法连接隔离测试数据库")
	}
	name := "byebyeocs_it_" + web.NewID()[:8] + web.NewID()[24:]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		admin.Close(ctx)
		t.Fatal("无法创建临时数据库，测试角色需要 CREATEDB")
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// 正常删除先等待已关闭连接离开；临时后台连接未退场时有界重试。
		// 不扩大测试角色权限，也不终止其他数据库中的会话。
		var dropErr error
		for {
			_, dropErr = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize())
			if dropErr == nil {
				break
			}
			var databaseErr *pgconn.PgError
			if !errors.As(dropErr, &databaseErr) || databaseErr.Code != "55006" {
				break
			}
			_, dropErr = admin.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			if dropErr == nil {
				break
			}
			if !errors.As(dropErr, &databaseErr) || (databaseErr.Code != "42501" && databaseErr.Code != "55006") {
				break
			}
			select {
			case <-ctx.Done():
			case <-time.After(100 * time.Millisecond):
				continue
			}
			break
		}
		if dropErr != nil {
			var databaseErr *pgconn.PgError
			if errors.As(dropErr, &databaseErr) {
				t.Errorf("清理临时数据库失败（SQLSTATE %s）", databaseErr.Code)
			} else {
				t.Error("清理临时数据库失败：连接或清理期限已结束")
			}
		}
		admin.Close(ctx)
	})
	connection, err := url.Parse(cfg.Database.DSN)
	if err != nil {
		t.Fatal("连接文件格式无效")
	}
	connection.Path = "/" + name
	connection.RawPath = ""
	cfg.Database.DSN = connection.String()
	db, err := postgres.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	if migrate {
		if _, err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
