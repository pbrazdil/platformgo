package postgres_test

import (
	"bytes"
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

var errSyntheticPostCommitMigrationAckLoss = errors.New(
	"synthetic post-commit migration acknowledgment loss",
)

// migrateOnceThenLoseAcknowledgment models only the caller's uncertainty
// after a successful Migrate return. It is intentionally not a transport
// failpoint: the migration call happens exactly once and has already
// committed before the sentinel error is injected.
func migrateOnceThenLoseAcknowledgment(migrate func() error) error {
	if err := migrate(); err != nil {
		return err
	}
	return errSyntheticPostCommitMigrationAckLoss
}

// TestPostgresPredictionMarketCatalogMigrationReconcilesSyntheticUnknownCommit
// proves the safe operator action after a caller loses the migration
// acknowledgment. A new connection verifies the committed tip and catalog;
// it does not issue a second Migrate call.
func TestPostgresPredictionMarketCatalogMigrationReconcilesSyntheticUnknownCommit(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	fixture.demote(t)

	migrateCalls := 0
	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	err := migrateOnceThenLoseAcknowledgment(func() error {
		migrateCalls++
		return migrator.Migrate(ctx)
	})
	if !errors.Is(err, errSyntheticPostCommitMigrationAckLoss) {
		t.Fatalf("synthetic migration result = %v, want acknowledgment-loss sentinel", err)
	}
	if migrateCalls != 1 {
		t.Fatalf("migration calls before acknowledgment loss = %d, want 1", migrateCalls)
	}

	// Release every connection opened under the uncertain caller before
	// reconciling from a fresh session as the same demoted exact owner.
	fixture.owner.Close()
	freshOwner := existingAdminBootstrapLoginPool(t, fixture.ownerName)
	freshMigrator := platformpostgres.NewMigrator(
		freshOwner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := freshMigrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("fresh-session VerifyCurrent after synthetic acknowledgment loss: %v", err)
	}
	if migrateCalls != 1 {
		t.Fatalf("fresh-session reconciliation issued %d migration calls, want 1", migrateCalls)
	}

	assertPredictionMarketCatalogTip44(t, fixture.admin, fixture.ownerName)
	assertPredictionMarketCatalogFunctionAuthority(t, fixture.admin, fixture.ownerName)
}

func assertPredictionMarketCatalogTip44(
	t *testing.T,
	admin *pgxpool.Pool,
	ownerName string,
) {
	t.Helper()
	ctx := context.Background()
	wantChecksum := predictionMarketCatalogMigrationChecksum(t)
	var (
		count    int
		filename string
		checksum []byte
	)
	if err := admin.QueryRow(ctx, `
		SELECT count(*)::integer,
		       max(filename),
		       (
			   SELECT checksum
			     FROM engine.schema_migrations
			    WHERE filename = $1
		       )
		  FROM engine.schema_migrations`, predictionMarketCatalogMigration).Scan(
		&count,
		&filename,
		&checksum,
	); err != nil {
		t.Fatalf("inspect committed migration-44 journal: %v", err)
	}
	if count != 44 || filename != predictionMarketCatalogMigration ||
		!bytes.Equal(checksum, wantChecksum) {
		t.Fatalf(
			"migration-44 journal = count %d tip %q checksum %x, want count 44 tip %q checksum %x",
			count,
			filename,
			checksum,
			predictionMarketCatalogMigration,
			wantChecksum,
		)
	}

	for _, relation := range []string{
		"trading.prediction_events",
		"trading.prediction_markets",
		"trading.prediction_legs",
	} {
		var owner string
		if err := admin.QueryRow(ctx, `
			SELECT pg_catalog.pg_get_userbyid(relation.relowner)
			  FROM pg_catalog.pg_class AS relation
			 WHERE relation.oid = $1::pg_catalog.regclass`, relation).Scan(&owner); err != nil {
			t.Fatalf("inspect migration-44 owner for %s: %v", relation, err)
		}
		if owner != ownerName {
			t.Fatalf("migration-44 owner for %s = %q, want %q", relation, owner, ownerName)
		}
	}
}

func assertPredictionMarketCatalogFunctionAuthority(
	t *testing.T,
	admin *pgxpool.Pool,
	ownerName string,
) {
	t.Helper()
	ctx := context.Background()
	const function = "identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)"
	var (
		functionOwner string
		superuser     bool
		securityDef   bool
	)
	if err := admin.QueryRow(ctx, `
		SELECT pg_catalog.pg_get_userbyid(procedure.proowner),
		       role.rolsuper,
		       procedure.prosecdef
		  FROM pg_catalog.pg_proc AS procedure
		  JOIN pg_catalog.pg_roles AS role ON role.oid = procedure.proowner
		 WHERE procedure.oid = $1::pg_catalog.regprocedure`, function).Scan(
		&functionOwner,
		&superuser,
		&securityDef,
	); err != nil {
		t.Fatalf("inspect migration-44 bootstrap function authority: %v", err)
	}
	if functionOwner != ownerName || superuser || !securityDef {
		t.Fatalf(
			"migration-44 bootstrap function authority = owner %q superuser %t security-definer %t, want owner %q nonsuperuser definer",
			functionOwner,
			superuser,
			securityDef,
			ownerName,
		)
	}
	assertAdminBootstrapFunctionACL(t, admin, function, "platformgo_admin_bootstrap")
}

// TestPostgresPredictionMarketCatalogTerminalRejectsStaleAndBadChecksums
// proves that the terminal bootstrap deployment fence rejects every checksum
// other than migration 44 before creating any authority graph. It then proves
// exact-tip creation and an identical replay from a fresh terminal session.
func TestPostgresPredictionMarketCatalogTerminalRejectsStaleAndBadChecksums(
	t *testing.T,
) {
	ctx := context.Background()
	admin := currentStorePool(t)
	bootstrapOwner := bootstrapFunctionOwner(t, admin)
	bootstrapOwnerID := pgx.Identifier{bootstrapOwner}.Sanitize()
	if _, err := admin.Exec(ctx, "ALTER ROLE "+bootstrapOwnerID+" SUPERUSER"); err != nil {
		t.Fatalf("temporarily elevate migration-44 bootstrap function owner %q: %v", bootstrapOwner, err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(
			context.Background(),
			"ALTER ROLE "+bootstrapOwnerID+" NOSUPERUSER",
		); err != nil {
			t.Errorf("restore migration-44 bootstrap function owner %q demotion: %v", bootstrapOwner, err)
		}
	})

	terminalLogin := fmt.Sprintf("prediction_catalog_terminal_recovery_%d", os.Getpid())
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		terminalLogin,
		"platformgo_admin_bootstrap",
	)
	badChecksums := []struct {
		name     string
		checksum []byte
	}{
		{name: "stale-tip-43", checksum: runtimeAuthorityACLMigrationChecksum(t)},
		{name: "malformed-wrong-32-byte", checksum: bytes.Repeat([]byte{0x44}, sha256.Size)},
	}
	for index, testCase := range badChecksums {
		t.Run(testCase.name, func(t *testing.T) {
			requestID := fmt.Sprintf("prediction-catalog-recovery-bad-%d", index)
			keyHash := sha256.Sum256([]byte(requestID))
			_, err := queryAdminBootstrapOnce(
				ctx,
				terminal,
				requestID,
				keyHash[:],
				"admin::urn:xb:admin:00000000-0000-4000-8000-000000000095",
				fmt.Sprintf("00000000-0000-4000-8000-00000000009%d", index+5),
				"2026-08-04T00:00:00.000000Z",
				testCase.checksum,
			)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
				t.Fatalf("%s checksum error = %v, want SQLSTATE 55000", testCase.name, err)
			}
			assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)
			assertMigrationHistoryTip(t, admin, 44, predictionMarketCatalogMigration)
		})
	}

	requestID := "prediction-catalog-recovery-exact"
	keyHash := sha256.Sum256([]byte(requestID))
	subject := "admin::urn:xb:admin:00000000-0000-4000-8000-000000000095"
	eventID := "00000000-0000-4000-8000-000000000095"
	logicalTime := "2026-08-04T00:00:00.000000Z"
	created, err := queryAdminBootstrapOnce(
		ctx,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
		predictionMarketCatalogMigrationChecksum(t),
	)
	if err != nil {
		t.Fatalf("exact migration-44 terminal bootstrap: %v", err)
	}
	if created.outcome != "created" {
		t.Fatalf("exact migration-44 terminal outcome = %q, want created", created.outcome)
	}

	// A new pool uses the same login and password but a fresh backend session;
	// the replay must return the stored response without adding graph rows.
	terminal.Close()
	freshTerminal := existingAdminBootstrapLoginPool(t, terminalLogin)
	replayed, err := queryAdminBootstrapOnce(
		ctx,
		freshTerminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
		predictionMarketCatalogMigrationChecksum(t),
	)
	if err != nil {
		t.Fatalf("fresh-session migration-44 terminal replay: %v", err)
	}
	if replayed != created {
		t.Fatalf("fresh-session migration-44 replay = %#v, want %#v", replayed, created)
	}
	assertPredictionMarketCatalogBootstrapGraph(t, admin, subject, requestID)

	if _, err := admin.Exec(ctx, "ALTER ROLE "+bootstrapOwnerID+" NOSUPERUSER"); err != nil {
		t.Fatalf("demote migration-44 bootstrap function owner after verification: %v", err)
	}
	var stillSuperuser bool
	if err := admin.QueryRow(ctx, `
		SELECT rolsuper FROM pg_catalog.pg_roles WHERE rolname = $1`, bootstrapOwner).Scan(&stillSuperuser); err != nil {
		t.Fatalf("inspect migration-44 bootstrap function owner cleanup: %v", err)
	}
	if stillSuperuser {
		t.Fatal("migration-44 bootstrap function owner retained superuser authority after replay")
	}
}

func assertPredictionMarketCatalogBootstrapGraph(
	t *testing.T,
	admin *pgxpool.Pool,
	subject string,
	requestID string,
) {
	t.Helper()
	var (
		roles       int
		policies    int
		assignments int
		receipts    int
	)
	if err := admin.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*)::integer
			   FROM identity.rbac_roles
			  WHERE role_id = $1 AND name = $2 AND builtin),
			(SELECT count(*)::integer
			   FROM identity.rbac_policies
			  WHERE role_id = $1
			    AND resource = '*' AND action = '*' AND effect = 'allow'),
			(SELECT count(*)::integer
			   FROM identity.rbac_admin_roles
			  WHERE admin_subject = $3 AND role_id = $1),
			(SELECT count(*)::integer
			   FROM audit.admin_bootstrap_events
			  WHERE request_id = $4)`,
		adminBootstrapRoleID,
		adminBootstrapRoleName,
		subject,
		requestID,
	).Scan(&roles, &policies, &assignments, &receipts); err != nil {
		t.Fatalf("inspect migration-44 terminal authority graph: %v", err)
	}
	if roles != 1 || policies != 1 || assignments != 1 || receipts != 1 {
		t.Fatalf(
			"migration-44 terminal authority graph = roles:%d policies:%d assignments:%d receipts:%d, want 1/1/1/1",
			roles,
			policies,
			assignments,
			receipts,
		)
	}
	assertAdminBootstrapAuthorityCounts(t, admin, 1, 1)
}
