package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
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
		migrateCurrentTipAsDemotedExactOwner(t, pool)
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
	migrateCurrentTipAsDemotedExactOwner(t, pool)
	if err := platformpostgres.NewMigrator(pool, currentMigrationFS()).ProvisionDeploymentShard(
		context.Background(), shardID,
	); err != nil {
		t.Fatalf("MigrateAndProvision: %v", err)
	}
	return pool
}

func sharedCurrentTemplateManager(t *testing.T) *postgresfixture.TemplateDatabaseManager {
	t.Helper()
	sharedCurrentTemplate.once.Do(func() {
		sharedCurrentTemplate.manager, sharedCurrentTemplate.err =
			postgresfixture.NewTemplateDatabaseManagerPhased(
				context.Background(),
				postgresfixture.TemplateDatabaseConfig{
					PrimaryDSN:  os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
					TemplateDSN: os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN"),
					Caller:      postgresfixture.TemplateCallerCurrentStore,
					Profile:     postgresfixture.TemplateProfileCurrent,
					Migrations:  currentMigrationFS(),
				},
				func(ctx context.Context, pool *pgxpool.Pool, phase postgresfixture.TemplateBuildPhase) error {
					if phase == postgresfixture.TemplateBuildPhasePreDemotion {
						if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
							return err
						}
						if err := postgresfixture.ProvisionRuntimeRoles(ctx, pool); err != nil {
							return err
						}
						return newCurrentTestMigrator(
							t,
							pool,
							migrationFilesThrough(t, runtimeAuthorityACLMigration),
						).Migrate(ctx)
					}
					migrator := newExactCurrentTestMigrator(t, pool, currentMigrationFS())
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

// migrateCurrentTipAsDemotedExactOwner builds the current fixture through
// migration 43 with a temporary superuser, then applies and verifies migration
// 44 as that same role after demotion. The inspection pool remains the caller's
// administrative connection; the owner role is cleaned up with the disposable
// database/template by the test cleanup stack.
func migrateCurrentTipAsDemotedExactOwner(
	t *testing.T,
	inspectionPool *pgxpool.Pool,
) {
	t.Helper()
	ctx := context.Background()
	ownerName := fmt.Sprintf("platformgo_current_owner_%d", os.Getpid())
	ownerID := pgx.Identifier{ownerName}.Sanitize()
	var inspectionOwner string
	if err := inspectionPool.QueryRow(ctx, "SELECT current_user").Scan(&inspectionOwner); err != nil {
		t.Fatalf("read inspection owner: %v", err)
	}
	inspectionOwnerID := pgx.Identifier{inspectionOwner}.Sanitize()
	if _, err := inspectionPool.Exec(ctx, fmt.Sprintf(`
		CREATE ROLE %s LOGIN SUPERUSER PASSWORD 'platformgo-test-password'`, ownerID)); err != nil {
		t.Fatalf("create current migration owner: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("preserving current migration owner %q and database objects for failure evidence", ownerName)
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := inspectionPool.Exec(cleanupCtx, "REASSIGN OWNED BY "+ownerID+" TO "+inspectionOwnerID); err != nil {
			t.Errorf("reassign current migration owner objects to inspection owner: %v", err)
			return
		}
		if _, err := inspectionPool.Exec(cleanupCtx, "DROP ROLE "+ownerID); err != nil {
			t.Errorf("cleanup current migration owner role without CASCADE: %v", err)
			return
		}
		var exists bool
		if err := inspectionPool.QueryRow(cleanupCtx, `
			SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, ownerName).Scan(&exists); err != nil {
			t.Errorf("verify current migration owner cleanup: %v", err)
			return
		}
		if exists {
			t.Errorf("current migration owner role %q remains after explicit cleanup", ownerName)
		}
	})

	ownerConfig := inspectionPool.Config().Copy()
	ownerConfig.ConnConfig.User = ownerName
	ownerConfig.ConnConfig.Password = "platformgo-test-password"
	ownerConfig.MaxConns = 4
	ownerPool, err := pgxpool.NewWithConfig(ctx, ownerConfig)
	if err != nil {
		t.Fatalf("open current migration owner pool: %v", err)
	}
	t.Cleanup(ownerPool.Close)
	if err := ownerPool.Ping(ctx); err != nil {
		t.Fatalf("ping current migration owner pool: %v", err)
	}

	if err := newCurrentTestMigrator(
		t,
		ownerPool,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply tip 43 as current migration owner: %v", err)
	}
	if _, err := inspectionPool.Exec(ctx, "ALTER ROLE "+ownerID+" NOSUPERUSER"); err != nil {
		t.Fatalf("demote current migration owner: %v", err)
	}
	migrator := platformpostgres.NewMigrator(ownerPool, currentMigrationFS())
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("apply migration 44 as demoted current owner: %v", err)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify migration 44 as demoted current owner: %v", err)
	}
}
