package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

// TestTemplateManagerBuildsCurrentTipThroughDemotedOwner is the focused
// managed-owner checkpoint for the current-tip template.  The pre-demotion
// phase may create the runtime roles and apply the predecessor migration set;
// the post-demotion phase must apply migration 44 and verify the full current
// manifest as the same exact NOSUPERUSER owner.
func TestTemplateManagerBuildsCurrentTipThroughDemotedOwner(t *testing.T) {
	primaryDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if primaryDSN == "" || templateDSN == "" {
		t.Skip("both PostgreSQL test DSNs are required")
	}
	t.Setenv(
		postgresfixture.TemplateDatabaseAuthorizationEnv,
		postgresfixture.TemplateDatabaseAuthorizationValue,
	)

	var phases []postgresfixture.TemplateBuildPhase
	manager, err := postgresfixture.NewTemplateDatabaseManagerPhased(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:  primaryDSN,
			TemplateDSN: templateDSN,
			Caller:      postgresfixture.TemplateCallerCurrentStore,
			Profile:     postgresfixture.TemplateProfileCurrent,
			Migrations:  currentMigrationFS(),
		},
		func(ctx context.Context, pool *pgxpool.Pool, phase postgresfixture.TemplateBuildPhase) error {
			phases = append(phases, phase)
			switch phase {
			case postgresfixture.TemplateBuildPhasePreDemotion:
				if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
					return err
				}
				if err := postgresfixture.ProvisionRuntimeRoles(ctx, pool); err != nil {
					return err
				}
				migrator := newCurrentTestMigrator(t, pool, migrationFilesThrough(t, runtimeAuthorityACLMigration))
				return migrator.Migrate(ctx)
			case postgresfixture.TemplateBuildPhasePostDemotion:
				migrator := newExactCurrentTestMigrator(t, pool, currentMigrationFS())
				if err := migrator.Migrate(ctx); err != nil {
					return err
				}
				return migrator.VerifyCurrent(ctx)
			default:
				return fmt.Errorf("unexpected template build phase %d", phase)
			}
		},
	)
	if err != nil {
		t.Fatalf("build managed current template: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := manager.Close(context.Background()); closeErr != nil {
			t.Errorf("close managed current template: %v", closeErr)
		}
	})
	if len(phases) != 2 || phases[0] != postgresfixture.TemplateBuildPhasePreDemotion || phases[1] != postgresfixture.TemplateBuildPhasePostDemotion {
		t.Fatalf("template build phases = %v, want pre-demotion then post-demotion", phases)
	}

	clone, err := manager.Clone(context.Background(), "managed-owner")
	if err != nil {
		t.Fatalf("clone managed current template: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := clone.Close(context.Background()); closeErr != nil {
			t.Errorf("close managed-owner clone: %v", closeErr)
		}
	})
	root := poolForDSN(t, templateDSN)
	var templateOwner, cloneOwner string
	var templateOwnerOID, cloneOwnerOID uint32
	if err := root.QueryRow(context.Background(), `
		SELECT
			(SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $1),
			(SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = $2),
			(SELECT datdba::integer FROM pg_database WHERE datname = $1),
			(SELECT datdba::integer FROM pg_database WHERE datname = $2)`,
		manager.TemplateName(), clone.Name(),
	).Scan(&templateOwner, &cloneOwner, &templateOwnerOID, &cloneOwnerOID); err != nil {
		t.Fatalf("inspect managed template/clone owners: %v", err)
	}
	if templateOwner == "" || templateOwner != cloneOwner || templateOwnerOID == 0 || templateOwnerOID != cloneOwnerOID {
		t.Fatalf("template owner %q/%d and clone owner %q/%d differ", templateOwner, templateOwnerOID, cloneOwner, cloneOwnerOID)
	}
	var superuser, createdb, createrole, replication, bypassrls bool
	if err := root.QueryRow(context.Background(), `
		SELECT rolsuper, rolcreatedb, rolcreaterole, rolreplication, rolbypassrls
		  FROM pg_roles WHERE rolname = $1`, templateOwner,
	).Scan(&superuser, &createdb, &createrole, &replication, &bypassrls); err != nil {
		t.Fatalf("inspect managed template owner role: %v", err)
	}
	if superuser || createdb || createrole || replication || bypassrls {
		t.Fatalf("managed template owner role is not demoted: superuser=%t createdb=%t createrole=%t replication=%t bypassrls=%t", superuser, createdb, createrole, replication, bypassrls)
	}

	clonePool := clonePool(t, clone)
	if err := newExactCurrentTestMigrator(t, clonePool, currentMigrationFS()).VerifyCurrent(context.Background()); err != nil {
		t.Fatalf("verify clone current migration manifest: %v", err)
	}
	var relationOwnerOID uint32
	if err := clonePool.QueryRow(context.Background(), `
		SELECT c.relowner::integer
		  FROM pg_class AS c
		 WHERE c.oid = 'engine.schema_migrations'::regclass`).Scan(&relationOwnerOID); err != nil {
		t.Fatalf("inspect clone migration relation owner: %v", err)
	}
	if relationOwnerOID != cloneOwnerOID {
		t.Fatalf("clone migration relation owner OID = %d, want database owner OID %d", relationOwnerOID, cloneOwnerOID)
	}
}

func newManagedOwnerTemplate(
	t *testing.T,
	afterDDL func(operation, databaseName string) error,
	afterRoleDDL func(operation, roleName string) error,
	post func(context.Context, *pgxpool.Pool) error,
) (*postgresfixture.TemplateDatabaseManager, error) {
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
	return postgresfixture.NewTemplateDatabaseManagerPhased(
		context.Background(),
		postgresfixture.TemplateDatabaseConfig{
			PrimaryDSN:   primaryDSN,
			TemplateDSN:  templateDSN,
			Caller:       postgresfixture.TemplateCallerCurrentStore,
			Profile:      postgresfixture.TemplateProfileCurrent,
			Migrations:   currentMigrationFS(),
			AfterDDL:     afterDDL,
			AfterRoleDDL: afterRoleDDL,
		},
		func(ctx context.Context, pool *pgxpool.Pool, phase postgresfixture.TemplateBuildPhase) error {
			if phase == postgresfixture.TemplateBuildPhasePreDemotion {
				if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
					return err
				}
				if err := postgresfixture.ProvisionRuntimeRoles(ctx, pool); err != nil {
					return err
				}
				return newCurrentTestMigrator(t, pool, migrationFilesThrough(t, runtimeAuthorityACLMigration)).Migrate(ctx)
			}
			if err := newExactCurrentTestMigrator(t, pool, currentMigrationFS()).Migrate(ctx); err != nil {
				return err
			}
			if err := newExactCurrentTestMigrator(t, pool, currentMigrationFS()).VerifyCurrent(ctx); err != nil {
				return err
			}
			if post != nil {
				return post(ctx, pool)
			}
			return nil
		},
	)
}

func TestTemplateManagerRejectsPostDemotionOwnerDrift(t *testing.T) {
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if templateDSN == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN is required")
	}
	root := poolForDSN(t, templateDSN)
	manager, err := newManagedOwnerTemplate(t, nil, nil, func(ctx context.Context, pool *pgxpool.Pool) error {
		var owner string
		if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
			return err
		}
		_, err := root.Exec(ctx, "ALTER ROLE "+pgx.Identifier{owner}.Sanitize()+" SUPERUSER")
		return err
	})
	if manager != nil {
		_ = manager.Close(context.Background())
		t.Fatal("post-demotion owner drift unexpectedly produced a template manager")
	}
	if !errors.Is(err, postgresfixture.ErrTemplateRoleDrift) {
		t.Fatalf("post-demotion owner drift error = %v, want ErrTemplateRoleDrift", err)
	}
	for _, name := range databaseInventory(t, templateDSN) {
		if len(name) >= len("platformgo_test_tpl_") && name[:len("platformgo_test_tpl_")] == "platformgo_test_tpl_" {
			t.Fatalf("post-demotion drift leaked template database %q", name)
		}
	}
	for _, name := range roleInventory(t, root) {
		if len(name) >= len("platformgo_tpl_owner_") && name[:len("platformgo_tpl_owner_")] == "platformgo_tpl_owner_" {
			t.Fatalf("post-demotion drift leaked owner role %q", name)
		}
	}
}

func TestTemplateManagerRejectsOwnerDriftAfterSeal(t *testing.T) {
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if templateDSN == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN is required")
	}
	root := poolForDSN(t, templateDSN)
	manager, err := newManagedOwnerTemplate(t, func(operation, databaseName string) error {
		if operation != postgresfixture.TemplateOperationSealBase {
			return nil
		}
		var owner string
		if err := root.QueryRow(context.Background(), `
			SELECT pg_get_userbyid(datdba)
			  FROM pg_database
			 WHERE datname = $1`, databaseName).Scan(&owner); err != nil {
			return err
		}
		_, err := root.Exec(context.Background(), "ALTER ROLE "+pgx.Identifier{owner}.Sanitize()+" SUPERUSER")
		return err
	}, nil, nil)
	if manager != nil {
		_ = manager.Close(context.Background())
		t.Fatal("sealed owner drift unexpectedly produced a template manager")
	}
	if !errors.Is(err, postgresfixture.ErrTemplateRoleDrift) {
		t.Fatalf("sealed owner drift error = %v, want ErrTemplateRoleDrift", err)
	}
	for _, name := range databaseInventory(t, templateDSN) {
		if len(name) >= len("platformgo_test_tpl_") && name[:len("platformgo_test_tpl_")] == "platformgo_test_tpl_" {
			t.Fatalf("sealed owner drift leaked template database %q", name)
		}
	}
	for _, name := range roleInventory(t, root) {
		if len(name) >= len("platformgo_tpl_owner_") && name[:len("platformgo_tpl_owner_")] == "platformgo_tpl_owner_" {
			t.Fatalf("sealed owner drift leaked owner role %q", name)
		}
	}
}

func TestTemplateManagerCleansOwnerAfterCreatePostconditionMismatch(t *testing.T) {
	templateDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN")
	if templateDSN == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_TEMPLATE_DSN is required")
	}
	root := poolForDSN(t, templateDSN)
	manager, err := newManagedOwnerTemplate(t, nil, func(operation, roleName string) error {
		if operation != "create-template-owner" {
			return nil
		}
		_, err := root.Exec(context.Background(), "ALTER ROLE "+pgx.Identifier{roleName}.Sanitize()+" NOLOGIN")
		return err
	}, nil)
	if manager != nil {
		_ = manager.Close(context.Background())
		t.Fatal("create postcondition mismatch unexpectedly produced a template manager")
	}
	if err == nil {
		t.Fatal("create postcondition mismatch unexpectedly succeeded")
	}
	for _, name := range roleInventory(t, root) {
		if len(name) >= len("platformgo_tpl_owner_") && name[:len("platformgo_tpl_owner_")] == "platformgo_tpl_owner_" {
			t.Fatalf("create postcondition mismatch leaked owner role %q: %v", name, err)
		}
	}
}
