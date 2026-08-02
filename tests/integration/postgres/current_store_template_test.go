package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

var sharedCurrentTemplate struct {
	once    sync.Once
	manager *postgresfixture.TemplateDatabaseManager
	err     error
}

func TestMain(testSuite *testing.M) {
	code := testSuite.Run()
	if sharedCurrentTemplate.manager != nil {
		if err := sharedCurrentTemplate.manager.Close(context.Background()); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "close shared current PostgreSQL template: %v\n", err)
			code = 1
		}
	}
	os.Exit(code)
}

// currentStorePool returns a current-tip, unprovisioned database. The ordinary
// integration lane keeps its original schema-reset path; the explicitly opted
// in fast lane clones from a template on a second dedicated cluster.
func currentStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN") == "" {
		pool := postgresPool(t)
		resetDurableSchemas(t, pool)
		if err := newCurrentTestMigrator(t, pool, currentMigrationFS()).Migrate(
			context.Background(),
		); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		return pool
	}

	manager := sharedCurrentTemplateManager(t)
	clone, err := manager.Clone(context.Background(), t.Name())
	if err != nil {
		t.Fatalf("clone current PostgreSQL template: %v", err)
	}
	pool, err := pgxpool.New(context.Background(), clone.DSN())
	if err != nil {
		_ = clone.Close(context.Background())
		t.Fatalf("open current PostgreSQL clone: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		_ = clone.Close(context.Background())
		t.Fatalf("ping current PostgreSQL clone: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		if err := clone.Close(context.Background()); err != nil {
			t.Errorf("drop current PostgreSQL clone: %v", err)
		}
	})
	return pool
}

func currentProvisionedStorePool(t *testing.T, shardID engine.ShardID) *pgxpool.Pool {
	t.Helper()
	pool := currentStorePool(t)
	if err := newCurrentTestMigrator(t, pool, currentMigrationFS()).ProvisionDeploymentShard(
		context.Background(),
		shardID,
	); err != nil {
		t.Fatalf("ProvisionDeploymentShard: %v", err)
	}
	return pool
}

func canonicalCurrentProvisionedStorePool(t *testing.T, shardID engine.ShardID) *pgxpool.Pool {
	t.Helper()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(t, pool, currentMigrationFS()).MigrateAndProvision(
		context.Background(),
		shardID,
	); err != nil {
		t.Fatalf("MigrateAndProvision: %v", err)
	}
	return pool
}

func sharedCurrentTemplateManager(t *testing.T) *postgresfixture.TemplateDatabaseManager {
	t.Helper()
	sharedCurrentTemplate.once.Do(func() {
		sharedCurrentTemplate.manager, sharedCurrentTemplate.err =
			postgresfixture.NewTemplateDatabaseManager(
				context.Background(),
				postgresfixture.TemplateDatabaseConfig{
					PrimaryDSN:  os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
					TemplateDSN: os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN"),
					Caller:      postgresfixture.TemplateCallerCurrentStore,
					Profile:     postgresfixture.TemplateProfileCurrent,
					Migrations:  currentMigrationFS(),
				},
				func(ctx context.Context, pool *pgxpool.Pool) error {
					if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
						return err
					}
					if err := postgresfixture.ProvisionRuntimeRoles(ctx, pool); err != nil {
						return err
					}
					migrator := newCurrentTestMigrator(t, pool, currentMigrationFS())
					if err := migrator.Migrate(ctx); err != nil {
						return err
					}
					return migrator.VerifyCurrent(ctx)
				},
			)
	})
	if sharedCurrentTemplate.err != nil {
		t.Fatalf("create shared current PostgreSQL template: %v", sharedCurrentTemplate.err)
	}
	return sharedCurrentTemplate.manager
}
