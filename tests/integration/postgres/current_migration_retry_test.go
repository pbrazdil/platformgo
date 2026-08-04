package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const currentMigrationContentionName = "20260731000200_phase3_runtime_authority_acl.up.sql"

func TestCurrentTestMigratorPreservesContentionWhenContextExpires(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, runtimeAuthorityACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}

	blockerPool := postgresPool(t)
	blocker, err := blockerPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin catalog blocker: %v", err)
	}
	t.Cleanup(func() { _ = blocker.Rollback(context.Background()) })
	if _, err := blocker.Exec(
		ctx,
		"LOCK TABLE pg_catalog.pg_attribute IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("lock pg_attribute: %v", err)
	}

	retryCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = newCurrentTestMigrator(
		t,
		pool,
		migrationFilesThrough(t, currentMigrationContentionName),
	).Migrate(retryCtx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retry error = %v, want context deadline", err)
	}
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		t.Fatalf("retry error = %v, want preserved SQLSTATE 55P03", err)
	}
	if !strings.Contains(err.Error(), "pg_catalog.pg_attribute") {
		t.Fatalf("retry error = %v, want preserved pg_attribute cause", err)
	}
}

// currentTestMigrator models the documented explicit operator retry when
// routine PostgreSQL maintenance collides with the current migration's fail-fast
// catalog fence. Partial-migration fixtures use the production migrator
// directly and intentional contention tests bypass this facade.
type currentTestMigrator struct {
	t        *testing.T
	migrator *platformpostgres.Migrator
}

func newCurrentTestMigrator(
	t *testing.T,
	pool *pgxpool.Pool,
	migrations fs.FS,
) *currentTestMigrator {
	t.Helper()
	if _, err := fs.Stat(migrations, predictionMarketCatalogMigration); err == nil {
		// Legacy integration fixtures do not own the migration-44 demotion
		// protocol. Keep them at the final privileged tip; the dedicated
		// current-store and prediction-catalog lanes prove tip 44.
		migrations = migrationFilesThrough(t, runtimeAuthorityACLMigration)
	}
	return &currentTestMigrator{
		t:        t,
		migrator: platformpostgres.NewMigrator(pool, migrations),
	}
}

func newExactCurrentTestMigrator(
	t *testing.T,
	pool *pgxpool.Pool,
	migrations fs.FS,
) *currentTestMigrator {
	t.Helper()
	return &currentTestMigrator{
		t:        t,
		migrator: platformpostgres.NewMigrator(pool, migrations),
	}
}

func (migrator *currentTestMigrator) Migrate(ctx context.Context) error {
	migrator.t.Helper()
	retryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const (
		attempts = 500
		delay    = 20 * time.Millisecond
	)
	var lastContention error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := migrator.migrator.Migrate(retryCtx)
		if !isCurrentMigrationContention(err) {
			if err != nil && lastContention != nil && retryCtx.Err() != nil {
				return fmt.Errorf(
					"explicit current-migration lock-contention retry "+
						"stopped after %d attempts: %w; last contention: %w; "+
						"terminal migration error: %w",
					attempt,
					retryCtx.Err(),
					lastContention,
					err,
				)
			}
			if attempt > 1 {
				migrator.t.Logf(
					"explicit current-migration lock-contention retry "+
						"reached a non-contention result on attempt %d",
					attempt,
				)
			}
			return err
		}
		lastContention = err
		if attempt == attempts {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf(
				"explicit current-migration lock-contention retry "+
					"stopped after %d attempts: %w; last contention: %w",
				attempt,
				retryCtx.Err(),
				lastContention,
			)
		case <-timer.C:
		}
	}
	return fmt.Errorf(
		"explicit current-migration lock contention remained after "+
			"%d attempts: %w",
		attempts,
		lastContention,
	)
}

func (migrator *currentTestMigrator) MigrateAndProvision(
	ctx context.Context,
	shardID engine.ShardID,
) error {
	if err := migrator.Migrate(ctx); err != nil {
		return err
	}
	return migrator.migrator.ProvisionDeploymentShard(ctx, shardID)
}

func (migrator *currentTestMigrator) ProvisionDeploymentShard(
	ctx context.Context,
	shardID engine.ShardID,
) error {
	return migrator.migrator.ProvisionDeploymentShard(ctx, shardID)
}

func (migrator *currentTestMigrator) VerifyCurrent(ctx context.Context) error {
	return migrator.migrator.VerifyCurrent(ctx)
}

func isCurrentMigrationContention(err error) bool {
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		return false
	}
	for _, name := range []string{
		currentMigrationContentionName,
		adminBootstrapMigration,
	} {
		if strings.Contains(err.Error(), "migrate "+name+": execute:") {
			return true
		}
	}
	return false
}
