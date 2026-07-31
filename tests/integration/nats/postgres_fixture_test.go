package nats_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const natsFixtureMigrationAttempts = 6

func migrateNATSFixture(ctx context.Context, pool *pgxpool.Pool) error {
	return retryNATSFixtureMigration(ctx, platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate)
}

func migrateAndProvisionNATSFixture(
	ctx context.Context,
	pool *pgxpool.Pool,
	shardID engine.ShardID,
) error {
	migrator := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	return retryNATSFixtureMigration(ctx, func(ctx context.Context) error {
		return migrator.MigrateAndProvision(ctx, shardID)
	})
}

// Production migrations fail closed on catalog contention. Disposable NATS
// fixtures retry only SQLSTATE 55P03 because PostgreSQL autovacuum can briefly
// hold the system-catalog locks used by that preflight.
func retryNATSFixtureMigration(
	ctx context.Context,
	migrate func(context.Context) error,
) error {
	delay := 100 * time.Millisecond
	for attempt := 1; attempt <= natsFixtureMigrationAttempts; attempt++ {
		err := migrate(ctx)
		if err == nil {
			return nil
		}
		if !isSingleLockNotAvailableError(err) {
			return err
		}
		if attempt == natsFixtureMigrationAttempts {
			return fmt.Errorf(
				"NATS fixture migration remained lock-contended after %d attempts: %w",
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
			return fmt.Errorf("wait to retry NATS fixture migration: %w", ctx.Err())
		case <-timer.C:
		}
		delay *= 2
	}
	panic("unreachable")
}

func isSingleLockNotAvailableError(err error) bool {
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
