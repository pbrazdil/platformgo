package postgres_test

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const currentMigrationContentionName = "20260731000100_phase3_admin_bootstrap_authority.up.sql"

// currentTestMigrator models the documented explicit operator retry when
// routine PostgreSQL maintenance collides with migration 42's fail-fast
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
		attempts = 250
		delay    = 20 * time.Millisecond
	)
	var lastContention error
	for attempt := 1; attempt <= attempts; attempt++ {
		err := migrator.migrator.Migrate(retryCtx)
		if !isCurrentMigrationContention(err) {
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
					"stopped after %d attempts: %w",
				attempt,
				retryCtx.Err(),
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
	return adminBootstrapIsPostgresCode(err, "55P03") &&
		strings.Contains(
			err.Error(),
			"migrate "+currentMigrationContentionName+": execute:",
		)
}
