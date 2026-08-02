package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

func TestTemplateManagerRejectsPrimaryClusterBeforeDDL(t *testing.T) {
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if primaryDSN == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is required")
	}
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)

	before := databaseInventory(t, primaryDSN)
	called := false
	_, err := postgresfixture.NewTemplateDatabaseManager(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: primaryDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
		},
		func(context.Context, *pgxpool.Pool) error {
			called = true
			return nil
		},
	)
	if !errors.Is(err, postgresfixture.ErrTemplateClusterNotDedicated) {
		t.Fatalf("manager error = %v, want ErrTemplateClusterNotDedicated", err)
	}
	if called {
		t.Fatal("template build callback ran on the primary cluster")
	}
	after := databaseInventory(t, primaryDSN)
	if fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("same-cluster refusal changed database inventory: before=%v after=%v", before, after)
	}
}

func TestDatabaseDDLAuthorizationIsIndependentFromSchemaReset(t *testing.T) {
	t.Setenv(
		postgresfixture.ResetAuthorizationEnv,
		postgresfixture.ResetAuthorizationValue,
	)
	t.Setenv(postgresfixture.TemplateDatabaseAuthorizationEnv, "")

	err := postgresfixture.ValidateTemplateDatabaseAuthorization()
	if !errors.Is(err, postgresfixture.ErrTemplateDatabaseDDLNotAuthorized) {
		t.Fatalf(
			"authorization error = %v, want ErrTemplateDatabaseDDLNotAuthorized",
			err,
		)
	}
}

func TestTemplateManagerRejectsDirtyMaintenanceRootBeforeDDL(t *testing.T) {
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if primaryDSN == "" || templateDSN == "" {
		t.Skip("both PostgreSQL test DSNs are required")
	}
	root := poolForDSN(t, templateDSN)
	if _, err := root.Exec(context.Background(), `CREATE SCHEMA root_only`); err != nil {
		t.Fatalf("seed dirty maintenance root: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), `DROP SCHEMA IF EXISTS root_only CASCADE`)
	})
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)

	_, err := postgresfixture.NewTemplateDatabaseManager(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: templateDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
		},
		func(context.Context, *pgxpool.Pool) error {
			t.Fatal("template build callback ran with a dirty maintenance root")
			return nil
		},
	)
	if !errors.Is(err, postgresfixture.ErrTemplateClusterNotPristine) {
		t.Fatalf("manager error = %v, want ErrTemplateClusterNotPristine", err)
	}
}

func TestFailedTemplateBuildRestoresDatabaseAndRoleInventory(t *testing.T) {
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if primaryDSN == "" || templateDSN == "" {
		t.Skip("both PostgreSQL test DSNs are required")
	}
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)
	root := poolForDSN(t, templateDSN)
	const foreignRole = "platformgo_test_failed_build_foreign"
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), `DROP ROLE IF EXISTS platformgo_test_failed_build_foreign`)
	})
	beforeDatabases := databaseInventory(t, templateDSN)
	beforeRoles := roleInventory(t, root)

	_, err := postgresfixture.NewTemplateDatabaseManager(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: templateDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
		},
		func(ctx context.Context, pool *pgxpool.Pool) error {
			if err := postgresfixture.ProvisionRuntimeRoles(ctx, pool); err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `CREATE ROLE platformgo_test_failed_build_foreign NOLOGIN`); err != nil {
				return err
			}
			return errors.New("injected template build failure")
		},
	)
	if err == nil {
		t.Fatal("failed template build unexpectedly succeeded")
	}
	if after := databaseInventory(t, templateDSN); fmt.Sprint(after) != fmt.Sprint(beforeDatabases) {
		t.Fatalf("database inventory after failed build = %v, want %v", after, beforeDatabases)
	}
	afterRoles := roleInventory(t, root)
	if len(afterRoles) != len(beforeRoles)+1 || !containsString(afterRoles, foreignRole) {
		t.Fatalf("role inventory after failed build = %v, want original roles plus foreign role", afterRoles)
	}
	for _, managedRole := range []string{
		"platformgo_admin_bootstrap",
		"platformgo_api",
		"platformgo_engine",
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	} {
		if containsString(afterRoles, managedRole) {
			t.Fatalf("failed build leaked managed role %q: %v", managedRole, afterRoles)
		}
	}
}

func TestTemplateManagerRejectsContaminatedTemplate0(t *testing.T) {
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if primaryDSN == "" || templateDSN == "" {
		t.Skip("both PostgreSQL test DSNs are required")
	}
	root := poolForDSN(t, templateDSN)
	if _, err := root.Exec(context.Background(), `ALTER DATABASE template0 WITH ALLOW_CONNECTIONS true`); err != nil {
		t.Fatalf("enable template0 contamination fixture: %v", err)
	}
	template0 := poolForDatabase(t, templateDSN, "template0")
	if _, err := template0.Exec(context.Background(), `CREATE TABLE public.template0_contamination_sentinel (id integer)`); err != nil {
		t.Fatalf("seed template0 contamination: %v", err)
	}
	template0.Close()
	if _, err := root.Exec(context.Background(), `ALTER DATABASE template0 WITH ALLOW_CONNECTIONS false`); err != nil {
		t.Fatalf("reseal contaminated template0: %v", err)
	}
	cleanupConfig, err := pgxpool.ParseConfig(templateDSN)
	if err != nil {
		t.Fatalf("parse template0 cleanup config: %v", err)
	}
	cleanupConfig.ConnConfig.Database = "template0"
	t.Cleanup(func() {
		ctx := context.Background()
		if _, cleanupErr := root.Exec(ctx, `ALTER DATABASE template0 WITH ALLOW_CONNECTIONS true`); cleanupErr != nil {
			t.Errorf("enable template0 cleanup: %v", cleanupErr)
			return
		}
		cleanupPool, cleanupErr := pgxpool.NewWithConfig(ctx, cleanupConfig)
		if cleanupErr != nil {
			t.Errorf("open template0 cleanup pool: %v", cleanupErr)
			return
		}
		if _, cleanupErr = cleanupPool.Exec(ctx, `DROP TABLE IF EXISTS public.template0_contamination_sentinel`); cleanupErr != nil {
			t.Errorf("remove template0 contamination: %v", cleanupErr)
		}
		cleanupPool.Close()
		if _, cleanupErr = root.Exec(ctx, `ALTER DATABASE template0 WITH ALLOW_CONNECTIONS false`); cleanupErr != nil {
			t.Errorf("reseal template0 after cleanup: %v", cleanupErr)
		}
	})
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)

	_, err = postgresfixture.NewTemplateDatabaseManager(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: templateDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
		},
		func(context.Context, *pgxpool.Pool) error {
			t.Fatal("prepare callback ran for contaminated template0")
			return nil
		},
	)
	if !errors.Is(err, postgresfixture.ErrTemplateClusterNotPristine) {
		t.Fatalf("manager error = %v, want ErrTemplateClusterNotPristine", err)
	}
}

func TestTemplateRootIsMaintenanceOnlyAndCurrentClonesAreIsolated(t *testing.T) {
	manager := currentTemplateManager(t, nil)
	ctx := context.Background()

	first, err := manager.Clone(ctx, "isolation-a")
	if err != nil {
		t.Fatalf("clone A: %v", err)
	}
	firstPool := clonePool(t, first)
	if _, err := firstPool.Exec(ctx, `CREATE TABLE public.clone_only (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("mutate clone A: %v", err)
	}
	assertCurrentTemplateStartsEmpty(t, firstPool)
	firstPool.Close()
	if err := first.Close(ctx); err != nil {
		t.Fatalf("close clone A: %v", err)
	}

	second, err := manager.Clone(ctx, "isolation-b")
	if err != nil {
		t.Fatalf("clone B: %v", err)
	}
	secondPool := clonePool(t, second)
	var cloneOnlyExists bool
	if err := secondPool.QueryRow(
		ctx,
		"SELECT pg_catalog.to_regclass('public.clone_only') IS NOT NULL",
	).Scan(&cloneOnlyExists); err != nil {
		t.Fatalf("inspect clone B: %v", err)
	}
	if cloneOnlyExists {
		t.Fatal("clone B inherited clone A mutation")
	}
	assertCurrentTemplateStartsEmpty(t, secondPool)
}

func TestTemplateManagerReconcilesUnknownCreateAndDrop(t *testing.T) {
	var createInjected bool
	var dropInjected bool
	var cancelCreate context.CancelFunc
	var cancelDrop context.CancelFunc
	manager := currentTemplateManager(t, func(operation, _ string) error {
		switch operation {
		case postgresfixture.TemplateOperationCreateClone:
			if !createInjected {
				createInjected = true
				cancelCreate()
				return context.Canceled
			}
		case postgresfixture.TemplateOperationDropClone:
			if !dropInjected {
				dropInjected = true
				cancelDrop()
				return context.Canceled
			}
		}
		return nil
	})

	createCtx, createCancel := context.WithCancel(context.Background())
	cancelCreate = createCancel
	clone, err := manager.Clone(createCtx, "unknown-outcome")
	if err != nil {
		t.Fatalf("reconcile committed CREATE: %v", err)
	}
	clonePool(t, clone).Close()
	dropCtx, dropCancel := context.WithCancel(context.Background())
	cancelDrop = dropCancel
	if err := clone.Close(dropCtx); err != nil {
		t.Fatalf("reconcile committed DROP: %v", err)
	}
	if !createInjected || !dropInjected {
		t.Fatalf("fault hooks = create %t drop %t, want both", createInjected, dropInjected)
	}
}

func TestTemplateManagerFailsClosedOnForeignRoleDrift(t *testing.T) {
	manager := currentTemplateManager(t, nil)
	root := poolForDSN(t, os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN"))
	const foreignRole = "platformgo_test_foreign_drift"
	if _, err := root.Exec(context.Background(), `CREATE ROLE platformgo_test_foreign_drift NOLOGIN`); err != nil {
		t.Fatalf("create foreign role drift: %v", err)
	}
	t.Cleanup(func() {
		_, _ = root.Exec(context.Background(), `DROP ROLE IF EXISTS platformgo_test_foreign_drift`)
	})

	_, err := manager.Clone(context.Background(), "must-fail-role-drift")
	if !errors.Is(err, postgresfixture.ErrTemplateRoleDrift) {
		t.Fatalf("clone error = %v, want ErrTemplateRoleDrift", err)
	}
	if err := manager.Close(context.Background()); !errors.Is(err, postgresfixture.ErrTemplateRoleDrift) {
		t.Fatalf("close error = %v, want ErrTemplateRoleDrift", err)
	}
	var exists bool
	if err := root.QueryRow(context.Background(), `
		SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`,
		foreignRole,
	).Scan(&exists); err != nil {
		t.Fatalf("inspect foreign role after cleanup: %v", err)
	}
	if !exists {
		t.Fatal("manager silently removed a foreign role")
	}
}

func TestTemplateManagerRefusesDropWithActiveSessions(t *testing.T) {
	manager := currentTemplateManager(t, nil)
	clone, err := manager.Clone(context.Background(), "active-session")
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	pool := clonePool(t, clone)
	dropContext, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := clone.Close(dropContext); err == nil {
		t.Fatal("DROP with an active clone session unexpectedly succeeded")
	}
	pool.Close()
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("manager cleanup after session drain: %v", err)
	}
}

func TestCanonicalPostgresPoolIgnoresTemplateDSN(t *testing.T) {
	if os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN") == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN is required")
	}
	pool := postgresPool(t)
	var databaseName string
	if err := pool.QueryRow(context.Background(), "SELECT current_database()").Scan(&databaseName); err != nil {
		t.Fatalf("read canonical database: %v", err)
	}
	if databaseName != "platformgo_test" {
		t.Fatalf("canonical database = %q, want platformgo_test", databaseName)
	}
}

func currentTemplateManager(
	t *testing.T,
	afterDDL func(operation, databaseName string) error,
) *postgresfixture.TemplateDatabaseManager {
	t.Helper()
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if primaryDSN == "" || templateDSN == "" {
		t.Skip("both PostgreSQL test DSNs are required")
	}
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)
	manager, err := postgresfixture.NewTemplateDatabaseManager(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: templateDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
			AfterDDL:    afterDDL,
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
	if err != nil {
		t.Fatalf("create template manager: %v", err)
	}
	t.Cleanup(func() {
		if err := manager.Close(context.Background()); err != nil {
			t.Errorf("close template manager: %v", err)
		}
	})
	return manager
}

func currentMigrationFS() fstest.MapFS {
	entries, err := os.ReadDir(filepath.Join("..", "..", "..", "migrations"))
	if err != nil {
		panic(err)
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", entry.Name()))
		if err != nil {
			panic(err)
		}
		files[entry.Name()] = &fstest.MapFile{Data: raw}
	}
	return files
}

func clonePool(t *testing.T, clone *postgresfixture.TemplateDatabase) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), clone.DSN())
	if err != nil {
		t.Fatalf("open clone pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping clone pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func poolForDSN(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func poolForDatabase(t *testing.T, dsn string, database string) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL pool config: %v", err)
	}
	config.ConnConfig.Database = database
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open PostgreSQL database %q: %v", database, err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL database %q: %v", database, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func assertCurrentTemplateStartsEmpty(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var rootSentinel bool
	var deploymentShards int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			pg_catalog.to_regnamespace('root_only') IS NOT NULL,
			(SELECT count(*) FROM engine.deployment_shard)`,
	).Scan(&rootSentinel, &deploymentShards); err != nil {
		t.Fatalf("inspect cloned baseline: %v", err)
	}
	if rootSentinel || deploymentShards != 0 {
		t.Fatalf("clone baseline = root sentinel %t shards %d", rootSentinel, deploymentShards)
	}
	if err := platformpostgres.NewMigrator(pool, currentMigrationFS()).VerifyCurrent(
		context.Background(),
	); err != nil {
		t.Fatalf("verify cloned current manifest: %v", err)
	}
}

func databaseInventory(t *testing.T, dsn string) []string {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open inventory pool: %v", err)
	}
	defer pool.Close()
	rows, err := pool.Query(context.Background(), `
		SELECT datname
		  FROM pg_database
		 ORDER BY datname`)
	if err != nil {
		t.Fatalf("query database inventory: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan database inventory: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate database inventory: %v", err)
	}
	return names
}

func roleInventory(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT rolname
		  FROM pg_roles
		 WHERE rolname !~ '^pg_'
		 ORDER BY rolname`)
	if err != nil {
		t.Fatalf("query role inventory: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan role inventory: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate role inventory: %v", err)
	}
	return names
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
