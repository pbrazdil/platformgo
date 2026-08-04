package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const runtimeAuthorityBootstrapDefinitionSHA256 = "16c5c551fdd4570dd44dc8f9d17db54d5e4e4dc0ba95ece691d9ba44bb655f40"

type predictionMarketTip43Fixture struct {
	admin     *pgxpool.Pool
	owner     *pgxpool.Pool
	ownerName string
	ownerID   string
}

func newPredictionMarketTip43Fixture(
	t *testing.T,
	provisionShard bool,
) *predictionMarketTip43Fixture {
	t.Helper()
	ctx := context.Background()
	admin := postgresPool(t)
	resetDurableSchemas(t, admin)
	ownerName := fmt.Sprintf("prediction_catalog_exact_owner_%d", os.Getpid())
	owner := adminBootstrapSuperuserLoginPool(t, admin, ownerName)
	migrator := newCurrentTestMigrator(
		t,
		owner,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	)
	var err error
	if provisionShard {
		err = migrator.MigrateAndProvision(ctx, 7)
	} else {
		err = migrator.Migrate(ctx)
	}
	if err != nil {
		t.Fatalf("apply tip 43 as exact owner: %v", err)
	}
	return &predictionMarketTip43Fixture{
		admin:     admin,
		owner:     owner,
		ownerName: ownerName,
		ownerID:   pgx.Identifier{ownerName}.Sanitize(),
	}
}

func (fixture *predictionMarketTip43Fixture) demote(t *testing.T) {
	t.Helper()
	if _, err := fixture.admin.Exec(
		context.Background(),
		"ALTER ROLE "+fixture.ownerID+" NOSUPERUSER",
	); err != nil {
		t.Fatalf("demote exact migration owner: %v", err)
	}
}

func migratePredictionMarketCatalogAsDemotedExactOwner(
	t *testing.T,
	fixture *predictionMarketTip43Fixture,
) {
	t.Helper()
	fixture.demote(t)
	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("apply migration 44 as demoted exact owner: %v", err)
	}
	if err := migrator.VerifyCurrent(context.Background()); err != nil {
		t.Fatalf("verify migration 44 as demoted exact owner: %v", err)
	}
}

// predictionMarketCatalogCurrentPool deliberately bypasses the optional
// shared-template fast lane. It proves the current tip on the exact owner
// boundary in the same disposable PostgreSQL cluster used by the test.
func predictionMarketCatalogCurrentPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	fixture := newPredictionMarketTip43Fixture(t, true)
	migratePredictionMarketCatalogAsDemotedExactOwner(t, fixture)
	return fixture.admin
}

func predictionMarketCatalogMigrationChecksum(t *testing.T) []byte {
	t.Helper()
	file, ok := migrationFilesThrough(t, predictionMarketCatalogMigration)[predictionMarketCatalogMigration]
	if !ok {
		t.Fatalf("migration %s is missing", predictionMarketCatalogMigration)
	}
	checksum := sha256.Sum256(file.Data)
	return checksum[:]
}

// TestPostgresPredictionMarketCatalogMigrationRunsAsDemotedExactOwner fixes
// the ordinary successor boundary after the exceptional migration-43
// cutover. Migration 44 must not require the exact owner to regain SUPERUSER.
func TestPostgresPredictionMarketCatalogMigrationRunsAsDemotedExactOwner(t *testing.T) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	admin := fixture.admin
	exactOwner := fixture.owner
	exactOwnerName := fixture.ownerName
	var oldDefinitionHash string
	if err := admin.QueryRow(ctx, `
		SELECT pg_catalog.encode(
			pg_catalog.sha256(pg_catalog.convert_to(
				pg_catalog.pg_get_functiondef(
					'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
						::pg_catalog.regprocedure
				),
				'UTF8'
			)),
			'hex'
		)`).Scan(&oldDefinitionHash); err != nil {
		t.Fatalf("hash tip-43 bootstrap definition: %v", err)
	}
	if oldDefinitionHash != runtimeAuthorityBootstrapDefinitionSHA256 {
		t.Fatalf(
			"tip-43 bootstrap definition hash = %s, want %s",
			oldDefinitionHash,
			runtimeAuthorityBootstrapDefinitionSHA256,
		)
	}

	fixture.demote(t)
	if err := platformpostgres.NewMigrator(
		exactOwner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply migration 44 as demoted exact owner: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		exactOwner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify migration 44 as demoted exact owner: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 44, predictionMarketCatalogMigration)

	var ownerStillSuperuser bool
	if err := admin.QueryRow(ctx, `
		SELECT rolsuper
		  FROM pg_catalog.pg_roles
		 WHERE rolname = $1`, exactOwnerName).Scan(&ownerStillSuperuser); err != nil {
		t.Fatalf("inspect exact owner after migration 44: %v", err)
	}
	if ownerStillSuperuser {
		t.Fatal("migration 44 retained or restored exact-owner superuser authority")
	}
	for _, relation := range []string{
		"trading.prediction_events",
		"trading.prediction_markets",
		"trading.prediction_legs",
	} {
		var owner string
		if err := admin.QueryRow(ctx, `
			SELECT owner.rolname
			  FROM pg_catalog.pg_class AS relation
			  JOIN pg_catalog.pg_roles AS owner ON owner.oid = relation.relowner
			 WHERE relation.oid = $1::pg_catalog.regclass`, relation).Scan(&owner); err != nil {
			t.Fatalf("inspect owner of %s: %v", relation, err)
		}
		if owner != exactOwnerName {
			t.Fatalf("owner of %s = %q, want %q", relation, owner, exactOwnerName)
		}
	}
}

// A different superuser must not be able to run the successor merely because
// it can inspect or mutate every PostgreSQL catalog. The owner preflight must
// reject that session before any prediction relation or migration journal row
// is created.
func TestPostgresPredictionMarketCatalogMigrationRejectsWrongSuperuserWithoutDelta(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	wrongName := fmt.Sprintf("prediction_catalog_wrong_superuser_%d", os.Getpid())
	wrong := adminBootstrapSuperuserLoginPool(t, fixture.admin, wrongName)

	var beforeCount int
	var beforeTip string
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer, max(filename)
		  FROM engine.schema_migrations`).Scan(&beforeCount, &beforeTip); err != nil {
		t.Fatalf("inspect tip-43 history: %v", err)
	}
	err := platformpostgres.NewMigrator(
		wrong,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("wrong-superuser migration error = %v, want SQLSTATE 55000", err)
	}

	var (
		afterCount   int
		afterTip     string
		marketExists bool
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT
			count(*)::integer,
			max(filename),
			to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(
		&afterCount,
		&afterTip,
		&marketExists,
	); err != nil {
		t.Fatalf("inspect wrong-superuser rollback: %v", err)
	}
	if afterCount != beforeCount || afterTip != beforeTip || marketExists {
		t.Fatalf(
			"wrong-superuser changed tip-43 state: count %d/%d tip %q/%q markets=%t",
			afterCount, beforeCount, afterTip, beforeTip, marketExists,
		)
	}
}

// A runtime role may be an allowed relation grantee while still being an
// unauthorized executor of the terminal bootstrap function. Migration 44
// must freeze that function ACL before CREATE OR REPLACE and leave tip 43
// unchanged until the hostile grant is removed.
func TestPostgresPredictionMarketCatalogMigrationRejectsHostileBootstrapFunctionACLWithoutDelta(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	const bootstrapFunction = "identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)"
	if _, err := fixture.owner.Exec(ctx, `
		GRANT EXECUTE ON FUNCTION identity.bootstrap_first_admin(
			text, bytea, text, uuid, text, bytea
		) TO platformgo_api`); err != nil {
		t.Fatalf("install hostile bootstrap-function ACL: %v", err)
	}
	var hostile bool
	if err := fixture.admin.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_proc AS procedure
			  CROSS JOIN LATERAL pg_catalog.aclexplode(procedure.proacl) AS privilege
			 WHERE procedure.oid = $1::pg_catalog.regprocedure
			   AND privilege.grantee = 'platformgo_api'::pg_catalog.regrole
			   AND privilege.privilege_type = 'EXECUTE'
		)`, bootstrapFunction).Scan(&hostile); err != nil {
		t.Fatalf("inspect hostile bootstrap-function ACL: %v", err)
	}
	if !hostile {
		t.Fatal("hostile bootstrap-function ACL grant was not installed")
	}

	fixture.demote(t)
	var beforeCount int
	var beforeTip string
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer, max(filename)
		  FROM engine.schema_migrations`).Scan(&beforeCount, &beforeTip); err != nil {
		t.Fatalf("inspect hostile tip-43 history: %v", err)
	}
	err := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile bootstrap-function migration error = %v, want SQLSTATE 55000", err)
	}
	var (
		afterCount  int
		afterTip    string
		marketExist bool
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer,
		       max(filename),
		       to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(
		&afterCount,
		&afterTip,
		&marketExist,
	); err != nil {
		t.Fatalf("inspect hostile bootstrap-function rollback: %v", err)
	}
	if afterCount != beforeCount || afterTip != beforeTip || marketExist {
		t.Fatalf(
			"hostile bootstrap-function changed tip-43 state: count %d/%d tip %q/%q markets=%t",
			afterCount,
			beforeCount,
			afterTip,
			beforeTip,
			marketExist,
		)
	}
	if _, err := fixture.owner.Exec(ctx, `
		REVOKE EXECUTE ON FUNCTION identity.bootstrap_first_admin(
			text, bytea, text, uuid, text, bytea
		) FROM platformgo_api`); err != nil {
		t.Fatalf("remove hostile bootstrap-function ACL: %v", err)
	}
	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after hostile bootstrap-function ACL removal: %v", err)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify migration after hostile bootstrap-function ACL removal: %v", err)
	}
}

// TestPostgresPredictionMarketCatalogMigrationAdvancesTerminalBootstrap proves
// that the exact deployment precondition follows the current migration tip.
// The terminal caller supplies migration 44's checksum, not migration 43's.
func TestPostgresPredictionMarketCatalogMigrationAdvancesTerminalBootstrap(t *testing.T) {
	ctx := context.Background()
	admin := currentStorePool(t)
	bootstrapOwner := bootstrapFunctionOwner(t, admin)
	bootstrapOwnerID := pgx.Identifier{bootstrapOwner}.Sanitize()
	if _, err := admin.Exec(ctx, "ALTER ROLE "+bootstrapOwnerID+" SUPERUSER"); err != nil {
		t.Fatalf("temporarily elevate bootstrap function owner %q: %v", bootstrapOwner, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(
			context.Background(),
			"ALTER ROLE "+bootstrapOwnerID+" NOSUPERUSER",
		); err != nil {
			t.Errorf("restore bootstrap function owner %q demotion: %v", bootstrapOwner, err)
		}
	})
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		fmt.Sprintf("prediction_catalog_bootstrap_%d", os.Getpid()),
		"platformgo_admin_bootstrap",
	)
	keyHash := sha256.Sum256([]byte("prediction-catalog-tip44-bootstrap"))
	got, err := queryAdminBootstrapOnce(
		ctx,
		terminal,
		"prediction-catalog-tip44-bootstrap",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000094",
		"00000000-0000-4000-8000-000000000094",
		"2026-08-04T00:00:00.000000Z",
		predictionMarketCatalogMigrationChecksum(t),
	)
	if err != nil {
		t.Fatalf("bootstrap at exact tip 44: %v", err)
	}
	if got.outcome != "created" {
		t.Fatalf("bootstrap at exact tip 44 outcome = %q, want created", got.outcome)
	}
	assertAdminBootstrapAuthorityCounts(t, admin, 1, 1)
}

func bootstrapFunctionOwner(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var owner string
	if err := pool.QueryRow(context.Background(), `
		SELECT pg_catalog.pg_get_userbyid(proowner)
		  FROM pg_catalog.pg_proc
		 WHERE oid = 'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
			::pg_catalog.regprocedure`).Scan(&owner); err != nil {
		t.Fatalf("inspect bootstrap function owner: %v", err)
	}
	if owner == "" {
		t.Fatal("bootstrap function has no owner")
	}
	return owner
}
