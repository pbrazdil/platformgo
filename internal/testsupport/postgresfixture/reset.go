// Package postgresfixture contains destructive setup shared by PostgreSQL-backed
// integration tests. It refuses to touch a database unless both the operator and
// the connected database identify it as disposable.
package postgresfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ResetAuthorizationEnv   = "PLATFORMGO_TEST_POSTGRES_RESET_AUTHORIZED"
	ResetAuthorizationValue = "YES_I_UNDERSTAND_THIS_DROPS_SCHEMAS"
)

var (
	ErrResetNotAuthorized = errors.New("destructive PostgreSQL test reset is not authorized")
	ErrUnsafeTestDatabase = errors.New("PostgreSQL database is not a disposable platformgo test database")

	disposableDatabaseName = regexp.MustCompile(`^platformgo_test(?:_[a-z0-9_]+)?$`)
	runtimeRoleNames       = []string{
		"platformgo_admin_bootstrap",
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	}
)

// ResetDurableSchemas drops all platformgo durable schemas after validating an
// explicit destructive opt-in and the live server's current database name.
func ResetDurableSchemas(ctx context.Context, pool *pgxpool.Pool) error {
	if os.Getenv(ResetAuthorizationEnv) != ResetAuthorizationValue {
		return fmt.Errorf(
			"%w: set %s to the exact required value only for a disposable database",
			ErrResetNotAuthorized,
			ResetAuthorizationEnv,
		)
	}
	var databaseName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&databaseName); err != nil {
		return fmt.Errorf("read current PostgreSQL database: %w", err)
	}
	if !IsDisposableDatabaseName(databaseName) {
		return fmt.Errorf("%w: current database %q", ErrUnsafeTestDatabase, databaseName)
	}
	if _, err := pool.Exec(
		ctx,
		`DROP SCHEMA IF EXISTS audit, realtime, identity, market, messaging, ledger, trading, engine CASCADE`,
	); err != nil {
		return fmt.Errorf("drop durable test schemas: %w", err)
	}
	return nil
}

// IsDisposableDatabaseName applies the independent live-database naming guard.
func IsDisposableDatabaseName(databaseName string) bool {
	return disposableDatabaseName.MatchString(databaseName)
}

// ProvisionRuntimeRoles creates the exact non-login roles required by current
// migrations. It returns failures so infrastructure constructors can unwind
// database DDL instead of terminating a test goroutine inside a callback.
func ProvisionRuntimeRoles(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_api') THEN
				CREATE ROLE platformgo_api NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_engine') THEN
				CREATE ROLE platformgo_engine NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_outbox') THEN
				CREATE ROLE platformgo_outbox NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_projector') THEN
				CREATE ROLE platformgo_projector NOLOGIN;
			END IF;
			IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_realtime') THEN
				CREATE ROLE platformgo_realtime NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_realtime_repair'
			) THEN
				CREATE ROLE platformgo_realtime_repair NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_admin_bootstrap'
			) THEN
				CREATE ROLE platformgo_admin_bootstrap NOLOGIN;
			END IF;
		END;
		$$`)
	if err != nil {
		return fmt.Errorf("provision PostgreSQL test runtime roles: %w", err)
	}
	return nil
}
