package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

func TestPostgresResetRequiresExplicitAuthorizationBeforeDDL(t *testing.T) {
	pool := postgresPool(t)
	ctx := context.Background()
	t.Setenv(
		postgresfixture.ResetAuthorizationEnv,
		postgresfixture.ResetAuthorizationValue,
	)
	if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
		t.Fatalf("authorized fixture reset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		CREATE SCHEMA engine;
		CREATE TABLE engine.reset_guard_sentinel (value text PRIMARY KEY);
		INSERT INTO engine.reset_guard_sentinel VALUES ('preserved')`,
	); err != nil {
		t.Fatalf("seed reset sentinel: %v", err)
	}
	t.Setenv(postgresfixture.ResetAuthorizationEnv, "")
	if err := postgresfixture.ResetDurableSchemas(ctx, pool); !errors.Is(
		err,
		postgresfixture.ErrResetNotAuthorized,
	) {
		t.Fatalf("unauthorized reset error = %v, want ErrResetNotAuthorized", err)
	}
	var value string
	if err := pool.QueryRow(
		ctx,
		`SELECT value FROM engine.reset_guard_sentinel`,
	).Scan(&value); err != nil {
		t.Fatalf("read preserved reset sentinel: %v", err)
	}
	if value != "preserved" {
		t.Fatalf("reset sentinel = %q, want preserved", value)
	}
	t.Setenv(
		postgresfixture.ResetAuthorizationEnv,
		postgresfixture.ResetAuthorizationValue,
	)
	if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
		t.Fatalf("cleanup authorized fixture reset: %v", err)
	}
}

func TestPostgresResetRefusesLiveDatabaseWithUnsafeNameBeforeDDL(t *testing.T) {
	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL admin pool: %v", err)
	}
	defer admin.Close()

	const unsafeDatabase = "platformgo_reset_guard"
	quotedDatabase := pgx.Identifier{unsafeDatabase}.Sanitize()
	if _, err := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+quotedDatabase+" WITH (FORCE)"); err != nil {
		t.Fatalf("drop stale unsafe-name fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+quotedDatabase); err != nil {
		t.Fatalf("create unsafe-name fixture: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(
			context.Background(),
			"DROP DATABASE IF EXISTS "+quotedDatabase+" WITH (FORCE)",
		)
	}()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	config.ConnConfig.Database = unsafeDatabase
	unsafePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open unsafe-name fixture: %v", err)
	}
	defer unsafePool.Close()
	if _, err := unsafePool.Exec(ctx, `
		CREATE SCHEMA engine;
		CREATE TABLE engine.reset_guard_sentinel (value text PRIMARY KEY);
		INSERT INTO engine.reset_guard_sentinel VALUES ('preserved')`,
	); err != nil {
		t.Fatalf("seed unsafe-name sentinel: %v", err)
	}
	t.Setenv(
		postgresfixture.ResetAuthorizationEnv,
		postgresfixture.ResetAuthorizationValue,
	)
	if err := postgresfixture.ResetDurableSchemas(ctx, unsafePool); !errors.Is(
		err,
		postgresfixture.ErrUnsafeTestDatabase,
	) {
		t.Fatalf("unsafe-name reset error = %v, want ErrUnsafeTestDatabase", err)
	}
	var value string
	if err := unsafePool.QueryRow(
		ctx,
		`SELECT value FROM engine.reset_guard_sentinel`,
	).Scan(&value); err != nil {
		t.Fatalf("read unsafe-name sentinel: %v", err)
	}
	if value != "preserved" {
		t.Fatalf("unsafe-name sentinel = %q, want preserved", value)
	}
}
