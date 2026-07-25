package postgres_test

import (
	"context"
	"errors"
	"testing"

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

func TestPostgresResetDatabaseNamePolicy(t *testing.T) {
	for _, name := range []string{
		"platformgo_test",
		"platformgo_test_ci",
		"platformgo_test_17",
	} {
		if !postgresfixture.IsDisposableDatabaseName(name) {
			t.Fatalf("disposable database %q was rejected", name)
		}
	}
	for _, name := range []string{
		"postgres",
		"platformgo",
		"platformgo_prod",
		"platformgo_test-production",
		"other_test",
	} {
		if postgresfixture.IsDisposableDatabaseName(name) {
			t.Fatalf("unsafe database %q was accepted", name)
		}
	}
}
