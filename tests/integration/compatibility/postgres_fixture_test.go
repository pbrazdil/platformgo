package compatibility_test

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const compatibilityFixtureMigrationAttempts = 6

const compatibilityContentionMigrationName = "20260731000100_phase3_admin_bootstrap_authority.up.sql"

func migrateAndProvisionCompatibilityFixture(
	t testing.TB,
	ctx context.Context,
	pool *pgxpool.Pool,
	migrations fs.FS,
	shardID engine.ShardID,
) error {
	t.Helper()
	migrator := platformpostgres.NewMigrator(pool, migrations)
	retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	attempts := 0
	err := migrateAndProvisionCompatibilityFixtureWith(
		retryCtx,
		func(ctx context.Context) error {
			attempts++
			return migrator.Migrate(ctx)
		},
		func(ctx context.Context) error {
			return migrator.ProvisionDeploymentShard(ctx, shardID)
		},
	)
	if attempts > 1 {
		t.Logf(
			"explicit compatibility fixture migration retry completed "+
				"after %d attempts",
			attempts,
		)
	}
	return err
}

func migrateAndProvisionCompatibilityFixtureWith(
	ctx context.Context,
	migrate func(context.Context) error,
	provision func(context.Context) error,
) error {
	if err := retryCompatibilityFixtureMigration(ctx, migrate); err != nil {
		return err
	}
	return provision(ctx)
}

// Production migrations fail closed on catalog contention. Disposable
// compatibility fixtures retry only a single SQLSTATE 55P03 from migration
// 42's execute phase because PostgreSQL autovacuum can briefly hold its catalog
// locks. Commit and shard-provisioning outcomes are never retried.
func retryCompatibilityFixtureMigration(
	ctx context.Context,
	migrate func(context.Context) error,
) error {
	delay := 100 * time.Millisecond
	for attempt := 1; attempt <= compatibilityFixtureMigrationAttempts; attempt++ {
		err := migrate(ctx)
		if err == nil {
			return nil
		}
		if !isSingleCompatibilityMigrationContention(err) {
			return err
		}
		if attempt == compatibilityFixtureMigrationAttempts {
			return fmt.Errorf(
				"compatibility fixture migration remained lock-contended "+
					"after %d attempts: %w",
				attempt,
				err,
			)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf(
				"wait to retry compatibility fixture migration: %w",
				ctx.Err(),
			)
		case <-timer.C:
		}
		delay *= 2
	}
	panic("unreachable")
}

func isSingleCompatibilityMigrationContention(err error) bool {
	if !strings.HasPrefix(
		err.Error(),
		"migrate "+compatibilityContentionMigrationName+": execute: ",
	) {
		return false
	}
	for err != nil {
		if _, multiple := err.(interface{ Unwrap() []error }); multiple {
			return false
		}
		if postgresError, ok := err.(*pgconn.PgError); ok {
			return postgresError.Code == "55P03"
		}
		single, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = single.Unwrap()
	}
	return false
}
