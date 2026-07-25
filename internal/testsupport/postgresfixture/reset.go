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
		`DROP SCHEMA IF EXISTS identity, market, messaging, ledger, trading, engine CASCADE`,
	); err != nil {
		return fmt.Errorf("drop durable test schemas: %w", err)
	}
	return nil
}

// IsDisposableDatabaseName applies the independent live-database naming guard.
func IsDisposableDatabaseName(databaseName string) bool {
	return disposableDatabaseName.MatchString(databaseName)
}
