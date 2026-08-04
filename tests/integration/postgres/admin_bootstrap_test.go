package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	adminBootstrapMigration            = "20260731000100_phase3_admin_bootstrap_authority.up.sql"
	adminBootstrapMigrationChecksumHex = "8f544dcdd68e02d038f16fd4c4e741ad7a7ffc09910747ecb8063269f51d0870"
	adminBootstrapRoleID               = "00000000-0000-4000-8000-000000000001"
	adminBootstrapRoleName             = "platformgo-superadmin"
)

type adminBootstrapResult struct {
	outcome              string
	adminSubject         string
	roleName             string
	configurationVersion int64
	eventID              string
	logicalTimeText      string
}

func TestAdminBootstrapCreatesAndReplaysOneAuditedAuthority(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		),
	); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_test_login",
		"platformgo_admin_bootstrap",
	)
	subject := "admin::urn:xb:admin:00000000-0000-4000-8000-000000000042"
	requestID := "bootstrap-request-0001"
	eventID := "00000000-0000-4000-8000-000000000042"
	logicalTime := "2026-07-31T00:00:00.000000Z"
	keyHash := sha256.Sum256([]byte("stable-bootstrap-key"))

	created := callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	if created != (adminBootstrapResult{
		outcome:              "created",
		adminSubject:         subject,
		roleName:             adminBootstrapRoleName,
		configurationVersion: 1,
		eventID:              eventID,
		logicalTimeText:      logicalTime,
	}) {
		t.Fatalf("created result = %#v", created)
	}

	replayed := callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	if replayed != created {
		t.Fatalf("replayed result = %#v, want %#v", replayed, created)
	}

	expectedRequestHash := sha256.Sum256([]byte(adminBootstrapPreimage(
		"platformgo_admin_bootstrap_test_login",
		requestID,
		subject,
		eventID,
		logicalTime,
	)))
	var (
		roleCount       int
		policyCount     int
		assignmentCount int
		auditCount      int
		storedHash      []byte
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.rbac_roles
			  WHERE role_id = $1 AND name = $2 AND builtin),
			(SELECT count(*) FROM identity.rbac_policies
			  WHERE role_id = $1
			    AND resource = '*' AND action = '*' AND effect = 'allow'),
			(SELECT count(*) FROM identity.rbac_admin_roles
			  WHERE admin_subject = $3 AND role_id = $1),
			(SELECT count(*) FROM audit.admin_bootstrap_events),
			(SELECT request_hash FROM audit.admin_bootstrap_events
			  WHERE request_id = $4)`,
		adminBootstrapRoleID,
		adminBootstrapRoleName,
		subject,
		requestID,
	).Scan(
		&roleCount,
		&policyCount,
		&assignmentCount,
		&auditCount,
		&storedHash,
	); err != nil {
		t.Fatalf("inspect committed bootstrap: %v", err)
	}
	if roleCount != 1 || policyCount != 1 || assignmentCount != 1 ||
		auditCount != 1 || string(storedHash) != string(expectedRequestHash[:]) {
		t.Fatalf(
			"bootstrap rows = role:%d policy:%d assignment:%d audit:%d hash:%x",
			roleCount,
			policyCount,
			assignmentCount,
			auditCount,
			storedHash,
		)
	}
	var concreteAllowed, wildcardRequestAllowed, unknownRequestAllowed bool
	if err := admin.QueryRow(ctx, `
		SELECT
			identity.admin_has_permission($1, 'roles', 'read'),
			identity.admin_has_permission($1, '*', '*'),
			identity.admin_has_permission($1, 'unknown', 'read')`,
		subject,
	).Scan(
		&concreteAllowed,
		&wildcardRequestAllowed,
		&unknownRequestAllowed,
	); err != nil {
		t.Fatalf("verify bootstrapped permission authority: %v", err)
	}
	if !concreteAllowed || wildcardRequestAllowed || unknownRequestAllowed {
		t.Fatalf(
			"bootstrap concrete/wildcard/unknown permission = %t/%t/%t",
			concreteAllowed,
			wildcardRequestAllowed,
			unknownRequestAllowed,
		)
	}
	var exactBootstrapRoleAuthority bool
	if err := admin.QueryRow(ctx, `
		SELECT
			count(*) = 2
			AND bool_and(
				(
					dependency.classid =
						'pg_catalog.pg_namespace'::pg_catalog.regclass
					AND dependency.objid =
						'identity'::pg_catalog.regnamespace
					AND dependency.deptype = 'a'
				)
				OR (
					dependency.classid =
						'pg_catalog.pg_proc'::pg_catalog.regclass
					AND dependency.objid =
						'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'::pg_catalog.regprocedure
					AND dependency.deptype = 'a'
				)
			)
		  FROM pg_catalog.pg_shdepend AS dependency
		 WHERE dependency.refclassid =
				'pg_catalog.pg_authid'::pg_catalog.regclass
		   AND dependency.refobjid = (
				SELECT role.oid
				  FROM pg_catalog.pg_roles AS role
				 WHERE role.rolname = 'platformgo_admin_bootstrap'
		   )
		   AND dependency.deptype IN ('a', 'o')`,
	).Scan(&exactBootstrapRoleAuthority); err != nil {
		t.Fatalf("inspect exact bootstrap role authority: %v", err)
	}
	if !exactBootstrapRoleAuthority {
		t.Fatal("bootstrap role has authority outside identity usage/function execute")
	}

	assertAdminBootstrapSQLState(
		t,
		terminal,
		"22000",
		requestID,
		keyHash[:],
		subject,
		"00000000-0000-4000-8000-000000000043",
		logicalTime,
	)
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"22000",
		requestID,
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000043",
		eventID,
		logicalTime,
	)
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"22000",
		requestID,
		keyHash[:],
		subject,
		eventID,
		"2026-07-31T00:00:00.000001Z",
	)
	otherKey := sha256.Sum256([]byte("different-bootstrap-key"))
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"55000",
		"bootstrap-request-0002",
		otherKey[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000043",
		"00000000-0000-4000-8000-000000000043",
		logicalTime,
	)

	if _, err := terminal.Exec(ctx, `
		INSERT INTO identity.rbac_admin_roles (admin_subject, role_id)
		VALUES (
			'admin::urn:xb:admin:00000000-0000-4000-8000-000000000099',
			$1
		)`,
		adminBootstrapRoleID,
	); !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("terminal direct RBAC DML error = %v", err)
	}
	if _, err := terminal.Exec(ctx, `
		UPDATE audit.admin_bootstrap_events
		   SET outcome = 'success'`); !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("terminal direct audit DML error = %v", err)
	}
}

func TestAdminBootstrapRejectsNullInputs(t *testing.T) {
	tests := []struct {
		name  string
		index int
	}{
		{name: "request id", index: 0},
		{name: "idempotency key hash", index: 1},
		{name: "admin subject", index: 2},
		{name: "event id", index: 3},
		{name: "logical time", index: 4},
		{name: "migration checksum", index: 5},
	}
	for testIndex, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}

			terminal := runtimeRoleLoginPool(
				t,
				admin,
				fmt.Sprintf(
					"platformgo_admin_bootstrap_null_test_login_%d",
					testIndex,
				),
				"platformgo_admin_bootstrap",
			)
			requestID := fmt.Sprintf(
				"bootstrap-request-null-%d",
				testIndex,
			)
			subject := fmt.Sprintf(
				"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
				130+testIndex,
			)
			eventID := fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				130+testIndex,
			)
			logicalTime := "2026-07-31T00:00:00.000000Z"
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-null-key-%d", testIndex)),
			)
			arguments := []any{
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
				runtimeAuthorityACLMigrationChecksum(t),
			}
			arguments[testCase.index] = nil
			var outcome string
			err := terminal.QueryRow(ctx, `
				SELECT outcome
				  FROM identity.bootstrap_first_admin(
					$1::text,
					$2::bytea,
					$3::text,
					$4::uuid,
					$5::text,
					$6::bytea
				  )`,
				arguments...,
			).Scan(&outcome)
			if !adminBootstrapIsPostgresCode(err, "22023") {
				t.Fatalf(
					"null %s bootstrap error = %v outcome %q, want 22023",
					testCase.name,
					err,
					outcome,
				)
			}
			assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)

			got := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
			)
			if got.outcome != "created" {
				t.Fatalf("bootstrap after null-input retry = %#v", got)
			}
		})
	}
}

func TestAdminBootstrapRejectsDivergentRuntimeJournal(t *testing.T) {
	ctx := context.Background()
	for index, testCase := range []struct {
		name   string
		mutate string
		repair string
	}{
		{
			name: "missing migration tip",
			mutate: `
				DELETE FROM engine.schema_migrations
				 WHERE filename = $1`,
			repair: `
				INSERT INTO engine.schema_migrations (filename, checksum)
				VALUES ($1, $2)`,
		},
		{
			name: "wrong migration checksum",
			mutate: `
				UPDATE engine.schema_migrations
				   SET checksum = pg_catalog.decode(
				       repeat('00', 32),
				       'hex'
				   )
				 WHERE filename = $1`,
			repair: `
				UPDATE engine.schema_migrations
				   SET checksum = $2
				 WHERE filename = $1`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}

			login := fmt.Sprintf(
				"platformgo_admin_bootstrap_journal_test_login_%d",
				index,
			)
			terminal := runtimeRoleLoginPool(
				t,
				admin,
				login,
				"platformgo_admin_bootstrap",
			)
			if _, err := admin.Exec(
				ctx,
				testCase.mutate,
				adminBootstrapMigration,
			); err != nil {
				t.Fatalf("diverge runtime journal: %v", err)
			}

			requestID := fmt.Sprintf("bootstrap-request-journal-%d", index)
			subject := fmt.Sprintf(
				"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
				110+index,
			)
			eventID := fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				110+index,
			)
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-journal-key-%d", index)),
			)
			assertAdminBootstrapSQLState(
				t,
				terminal,
				"55000",
				requestID,
				keyHash[:],
				subject,
				eventID,
				"2026-07-31T00:00:00.000000Z",
			)

			var assignments, receipts int
			if err := admin.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM identity.rbac_admin_roles),
					(SELECT count(*) FROM audit.admin_bootstrap_events)`,
			).Scan(&assignments, &receipts); err != nil {
				t.Fatalf("inspect rejected divergent journal: %v", err)
			}
			if assignments != 0 || receipts != 0 {
				t.Fatalf(
					"divergent journal state = assignments %d receipts %d",
					assignments,
					receipts,
				)
			}

			if _, err := admin.Exec(
				ctx,
				testCase.repair,
				adminBootstrapMigration,
				adminBootstrapMigrationChecksum(t),
			); err != nil {
				t.Fatalf("repair runtime journal: %v", err)
			}
			got := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				"2026-07-31T00:00:00.000000Z",
			)
			if got.outcome != "created" {
				t.Fatalf("bootstrap after journal repair = %#v", got)
			}
		})
	}
}

func TestAdminBootstrapRejectsUnsafeEngineNamespaceAuthority(t *testing.T) {
	for index, testCase := range []struct {
		name      string
		install   string
		repair    string
		dropRoles string
	}{
		{
			name: "external engine schema owner",
			install: `
				CREATE ROLE platformgo_admin_bootstrap_engine_owner NOLOGIN;
				ALTER SCHEMA engine
					OWNER TO platformgo_admin_bootstrap_engine_owner`,
			repair: `
				ALTER SCHEMA engine OWNER TO CURRENT_USER`,
			dropRoles: `
				DROP ROLE IF EXISTS platformgo_admin_bootstrap_engine_owner`,
		},
		{
			name: "nonowner engine create privilege",
			install: `
				GRANT CREATE ON SCHEMA engine
					TO platformgo_admin_bootstrap`,
			repair: `
				REVOKE CREATE ON SCHEMA engine
					FROM platformgo_admin_bootstrap`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}
			if _, err := admin.Exec(ctx, testCase.install); err != nil {
				t.Fatalf("install unsafe engine namespace authority: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(context.Background(), testCase.repair)
				if testCase.dropRoles != "" {
					_, _ = admin.Exec(
						context.Background(),
						testCase.dropRoles,
					)
				}
			})

			login := fmt.Sprintf(
				"platformgo_admin_bootstrap_engine_schema_login_%d",
				index,
			)
			terminal := runtimeRoleLoginPool(
				t,
				admin,
				login,
				"platformgo_admin_bootstrap",
			)
			requestID := fmt.Sprintf(
				"bootstrap-request-engine-schema-%d",
				index,
			)
			subject := fmt.Sprintf(
				"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
				120+index,
			)
			eventID := fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				120+index,
			)
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-engine-schema-key-%d", index)),
			)
			assertAdminBootstrapSQLState(
				t,
				terminal,
				"55000",
				requestID,
				keyHash[:],
				subject,
				eventID,
				"2026-07-31T00:00:00.000000Z",
			)
			assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)

			if _, err := admin.Exec(ctx, testCase.repair); err != nil {
				t.Fatalf("repair engine namespace authority: %v", err)
			}
			got := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				"2026-07-31T00:00:00.000000Z",
			)
			if got.outcome != "created" {
				t.Fatalf("bootstrap after engine namespace repair = %#v", got)
			}
		})
	}
}

func TestAdminBootstrapRejectsCatalogContentionWithoutDeadlock(t *testing.T) {
	for index, testCase := range []struct {
		name          string
		heldCatalog   string
		followCatalog string
	}{
		{
			name:          "pg_proc then pg_class",
			heldCatalog:   "pg_proc",
			followCatalog: "pg_class",
		},
		{
			name:          "pg_authid then pg_class",
			heldCatalog:   "pg_authid",
			followCatalog: "pg_class",
		},
		{
			name:          "pg_class then pg_attribute",
			heldCatalog:   "pg_class",
			followCatalog: "pg_attribute",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}

			login := fmt.Sprintf(
				"platformgo_admin_bootstrap_catalog_lock_login_%d",
				index,
			)
			terminal := runtimeRoleLoginPool(
				t,
				admin,
				login,
				"platformgo_admin_bootstrap",
			)
			catalogWriter, err := admin.Begin(ctx)
			if err != nil {
				t.Fatalf("begin catalog writer: %v", err)
			}
			target := "pg_catalog." +
				pgx.Identifier{testCase.heldCatalog}.Sanitize()
			if _, err := catalogWriter.Exec(
				ctx,
				"LOCK TABLE "+target+" IN ROW EXCLUSIVE MODE",
			); err != nil {
				_ = catalogWriter.Rollback(ctx)
				t.Fatalf(
					"lock %s as catalog writer: %v",
					testCase.heldCatalog,
					err,
				)
			}

			requestID := fmt.Sprintf(
				"bootstrap-request-catalog-lock-%d",
				index,
			)
			subject := fmt.Sprintf(
				"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
				140+index,
			)
			eventID := fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				140+index,
			)
			logicalTime := "2026-07-31T00:00:00.000000Z"
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-catalog-lock-key-%d", index)),
			)
			bootstrapCtx, bootstrapCancel := context.WithTimeout(
				ctx,
				2*time.Second,
			)
			defer bootstrapCancel()
			assertAdminBootstrapSQLStateContext(
				t,
				bootstrapCtx,
				terminal,
				"55P03",
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
			)
			assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)

			follow := "pg_catalog." +
				pgx.Identifier{testCase.followCatalog}.Sanitize()
			lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if _, err := catalogWriter.Exec(
				lockCtx,
				"LOCK TABLE "+follow+" IN ROW EXCLUSIVE MODE",
			); err != nil {
				_ = catalogWriter.Rollback(ctx)
				t.Fatalf(
					"catalog writer could not continue %s -> %s: %v",
					testCase.heldCatalog,
					testCase.followCatalog,
					err,
				)
			}
			if err := catalogWriter.Commit(ctx); err != nil {
				t.Fatalf("commit catalog writer: %v", err)
			}
			got := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
			)
			if got.outcome != "created" {
				t.Fatalf("bootstrap after catalog writer drain = %#v", got)
			}
		})
	}
}

func TestAdminBootstrapRejectsRelationContentionWithoutDeadlock(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_relation_lock_login",
		"platformgo_admin_bootstrap",
	)

	relationWriter, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin authority relation writer: %v", err)
	}
	if _, err := relationWriter.Exec(ctx, `
		LOCK TABLE identity.rbac_roles IN ACCESS EXCLUSIVE MODE`,
	); err != nil {
		_ = relationWriter.Rollback(ctx)
		t.Fatalf("lock authority relation: %v", err)
	}

	requestID := "bootstrap-request-relation-lock"
	subject := "admin::urn:xb:admin:00000000-0000-4000-8000-000000000150"
	eventID := "00000000-0000-4000-8000-000000000150"
	logicalTime := "2026-07-31T00:00:00.000000Z"
	keyHash := sha256.Sum256([]byte("bootstrap-relation-lock-key"))
	bootstrapCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	assertAdminBootstrapSQLStateContext(
		t,
		bootstrapCtx,
		terminal,
		"55P03",
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)

	followCtx, followCancel := context.WithTimeout(ctx, 2*time.Second)
	defer followCancel()
	if _, err := relationWriter.Exec(followCtx, `
		LOCK TABLE pg_catalog.pg_class IN ROW EXCLUSIVE MODE;
		LOCK TABLE pg_catalog.pg_attribute IN ROW EXCLUSIVE MODE`,
	); err != nil {
		_ = relationWriter.Rollback(ctx)
		t.Fatalf("relation writer could not continue with catalog DDL: %v", err)
	}
	if err := relationWriter.Commit(ctx); err != nil {
		t.Fatalf("commit authority relation writer: %v", err)
	}
	got := callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	if got.outcome != "created" {
		t.Fatalf("bootstrap after relation writer drain = %#v", got)
	}
}

func TestAdminBootstrapReplayFailsClosedWhenCommittedAuthorityDiverges(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_divergence_test_login",
		"platformgo_admin_bootstrap",
	)
	subject := "admin::urn:xb:admin:00000000-0000-4000-8000-000000000044"
	requestID := "bootstrap-request-divergence"
	eventID := "00000000-0000-4000-8000-000000000044"
	logicalTime := "2026-07-31T00:00:00.000000Z"
	keyHash := sha256.Sum256([]byte("stable-bootstrap-divergence-key"))

	callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	if _, err := admin.Exec(ctx, `
		DELETE FROM identity.rbac_admin_roles
		 WHERE admin_subject = $1
		   AND role_id = $2`,
		subject,
		adminBootstrapRoleID,
	); err != nil {
		t.Fatalf("diverge committed bootstrap authority: %v", err)
	}

	assertAdminBootstrapSQLState(
		t,
		terminal,
		"55000",
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
}

func TestAdminBootstrapReplayRejectsCorruptedReceiptFields(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_receipt_corruption_login",
		"platformgo_admin_bootstrap",
	)
	const (
		subject     = "admin::urn:xb:admin:00000000-0000-4000-8000-000000000045"
		requestID   = "bootstrap-request-receipt-corruption"
		eventID     = "00000000-0000-4000-8000-000000000045"
		logicalTime = "2026-07-31T00:00:00.000000Z"
	)
	keyHash := sha256.Sum256([]byte("stable-bootstrap-receipt-corruption-key"))
	callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)

	if _, err := admin.Exec(ctx, `
		ALTER TABLE audit.admin_bootstrap_events
			DISABLE TRIGGER admin_bootstrap_events_are_immutable;
		UPDATE audit.admin_bootstrap_events
		   SET occurred_at = occurred_at + interval '1 microsecond',
		       detail = '{"corrupted":true}'::jsonb;
		ALTER TABLE audit.admin_bootstrap_events
			ENABLE TRIGGER admin_bootstrap_events_are_immutable`); err != nil {
		t.Fatalf("corrupt bootstrap receipt and restore guard: %v", err)
	}
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"55000",
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
}

func TestAdminBootstrapPostWriteGraphValidationRejectsInjectedAssignment(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE FUNCTION public.inject_admin_assignment()
		RETURNS trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
			IF NEW.admin_subject <>
				'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff'
			THEN
				INSERT INTO identity.rbac_admin_roles (
					admin_subject,
					role_id
				) VALUES (
					'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff',
					NEW.role_id
				);
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER inject_admin_assignment
		BEFORE INSERT ON identity.rbac_admin_roles
		FOR EACH ROW
		EXECUTE FUNCTION public.inject_admin_assignment()`); err != nil {
		t.Fatalf("install injected admin assignment trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS inject_admin_assignment
				ON identity.rbac_admin_roles;
			DROP FUNCTION IF EXISTS public.inject_admin_assignment()`)
	})

	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_post_write_test_login",
		"platformgo_admin_bootstrap",
	)
	keyHash := sha256.Sum256([]byte("stable-bootstrap-post-write-key"))
	_, attempts, err := queryAdminBootstrapAfterTransientLockContention(
		ctx,
		terminal,
		"bootstrap-request-post-write",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-00000000004b",
		"00000000-0000-4000-8000-00000000004b",
		"2026-07-31T00:00:00.000000Z",
		runtimeAuthorityACLMigrationChecksum(t),
	)
	if attempts > 1 {
		t.Logf(
			"explicit runtime bootstrap lock-contention retry reached "+
				"the injected-assignment rejection on attempt %d",
			attempts,
		)
	}
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf(
			"bootstrap with injected assignment error = %v, want 55000",
			err,
		)
	}

	var assignments, receipts int
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.rbac_admin_roles),
			(SELECT count(*) FROM audit.admin_bootstrap_events)`,
	).Scan(&assignments, &receipts); err != nil {
		t.Fatalf("inspect rejected injected assignment: %v", err)
	}
	if assignments != 0 || receipts != 0 {
		t.Fatalf(
			"rejected injected assignment state = assignments %d receipts %d",
			assignments,
			receipts,
		)
	}
}

func TestAdminBootstrapRejectsUnexpectedCatalogAuthority(
	t *testing.T,
) {
	tests := []struct {
		name       string
		installSQL string
	}{
		{
			name: "immediate out-of-graph effect",
			installSQL: `
				CREATE FUNCTION public.inject_admin_bootstrap_side_effect()
				RETURNS trigger
				LANGUAGE plpgsql
				SET search_path = pg_catalog
				AS $$
				BEGIN
					INSERT INTO public.admin_bootstrap_side_effect (subject)
					VALUES (NEW.admin_subject);
					RETURN NEW;
				END
				$$;
				CREATE TRIGGER inject_admin_bootstrap_side_effect
				BEFORE INSERT ON identity.rbac_admin_roles
				FOR EACH ROW
				EXECUTE FUNCTION public.inject_admin_bootstrap_side_effect()`,
		},
		{
			name: "deferred assignment",
			installSQL: `
				CREATE FUNCTION public.inject_deferred_admin_assignment()
				RETURNS trigger
				LANGUAGE plpgsql
				SET search_path = pg_catalog
				AS $$
				BEGIN
					IF NEW.admin_subject <>
						'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff'
					THEN
						INSERT INTO identity.rbac_admin_roles (
							admin_subject,
							role_id
						) VALUES (
							'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff',
							NEW.role_id
						);
					END IF;
					RETURN NULL;
				END
				$$;
				CREATE CONSTRAINT TRIGGER inject_deferred_admin_assignment
				AFTER INSERT ON identity.rbac_admin_roles
				DEFERRABLE INITIALLY DEFERRED
				FOR EACH ROW
				EXECUTE FUNCTION public.inject_deferred_admin_assignment()`,
		},
		{
			name: "rewrite rule out-of-graph effect",
			installSQL: `
				CREATE RULE inject_admin_bootstrap_rule_effect
				AS ON INSERT TO identity.rbac_admin_roles
				DO ALSO
					INSERT INTO public.admin_bootstrap_side_effect (subject)
					VALUES (NEW.admin_subject)`,
		},
		{
			name: "migration journal rewrite rule",
			installSQL: `
				CREATE RULE suppress_future_journal
				AS ON INSERT TO engine.schema_migrations
				DO INSTEAD NOTHING`,
		},
		{
			name: "migration journal user trigger",
			installSQL: `
				CREATE FUNCTION public.pass_migration_journal_insert()
				RETURNS trigger
				LANGUAGE plpgsql
				SET search_path = pg_catalog
				AS $$
				BEGIN
					RETURN NEW;
				END
				$$;
				CREATE TRIGGER pass_migration_journal_insert
				BEFORE INSERT ON engine.schema_migrations
				FOR EACH ROW
				EXECUTE FUNCTION public.pass_migration_journal_insert()`,
		},
		{
			name: "immutable trigger false predicate",
			installSQL: `
				DROP TRIGGER admin_bootstrap_events_are_immutable
					ON audit.admin_bootstrap_events;
				CREATE TRIGGER admin_bootstrap_events_are_immutable
				BEFORE UPDATE OR DELETE ON audit.admin_bootstrap_events
				FOR EACH ROW
				WHEN (false)
				EXECUTE FUNCTION engine.reject_immutable_change()`,
		},
		{
			name: "immutable trigger narrowed update columns",
			installSQL: `
				DROP TRIGGER admin_bootstrap_events_are_immutable
					ON audit.admin_bootstrap_events;
				CREATE TRIGGER admin_bootstrap_events_are_immutable
				BEFORE UPDATE OF detail OR DELETE
					ON audit.admin_bootstrap_events
				FOR EACH ROW
				EXECUTE FUNCTION engine.reject_immutable_change()`,
		},
		{
			name: "side-effecting check constraint",
			installSQL: `
				CREATE FUNCTION public.capture_admin_bootstrap_check(text)
				RETURNS boolean
				LANGUAGE plpgsql
				VOLATILE
				SET search_path = pg_catalog
				AS $$
				BEGIN
					INSERT INTO public.admin_bootstrap_side_effect (subject)
					VALUES ($1);
					RETURN true;
				END
				$$;
				ALTER TABLE identity.rbac_admin_roles
					ADD CONSTRAINT injected_check_side_effect
					CHECK (
						public.capture_admin_bootstrap_check(admin_subject)
					)`,
		},
		{
			name: "raw relation privilege",
			installSQL: `
				GRANT INSERT ON identity.rbac_admin_roles TO PUBLIC`,
		},
		{
			name: "bootstrap schema create privilege",
			installSQL: `
				GRANT CREATE ON SCHEMA identity
					TO platformgo_admin_bootstrap`,
		},
		{
			name: "permission function public execute",
			installSQL: `
				GRANT EXECUTE ON FUNCTION
					identity.admin_has_permission(text,text,text)
					TO PUBLIC`,
		},
		{
			name: "disabled internal foreign key triggers",
			installSQL: `
				ALTER TABLE identity.rbac_admin_roles DISABLE TRIGGER ALL`,
		},
		{
			name: "inherited assignment child",
			installSQL: `
				CREATE TABLE public.inherited_admin_assignments ()
					INHERITS (identity.rbac_admin_roles)`,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}
			if _, err := admin.Exec(ctx, `
				DROP TABLE IF EXISTS public.admin_bootstrap_side_effect;
				CREATE TABLE public.admin_bootstrap_side_effect (
					subject text PRIMARY KEY
				)`); err != nil {
				t.Fatalf("prepare bootstrap side-effect table: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(
					context.Background(),
					"DROP TABLE IF EXISTS public.admin_bootstrap_side_effect",
				)
			})
			if _, err := admin.Exec(ctx, test.installSQL); err != nil {
				t.Fatalf("install unexpected bootstrap trigger: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(context.Background(), `
					REVOKE CREATE ON SCHEMA identity
						FROM platformgo_admin_bootstrap;
					REVOKE ALL PRIVILEGES
						ON TABLE identity.rbac_admin_roles FROM PUBLIC;
					REVOKE ALL PRIVILEGES
						ON FUNCTION
							identity.admin_has_permission(text,text,text)
						FROM PUBLIC;
					ALTER TABLE identity.rbac_admin_roles ENABLE TRIGGER ALL;
					DROP TABLE IF EXISTS
						public.inherited_admin_assignments;
					DROP RULE IF EXISTS inject_admin_bootstrap_rule_effect
						ON identity.rbac_admin_roles;
					DROP RULE IF EXISTS suppress_future_journal
						ON engine.schema_migrations;
					DROP FUNCTION IF EXISTS
						public.pass_migration_journal_insert() CASCADE;
					DROP FUNCTION IF EXISTS
						public.capture_admin_bootstrap_check(text) CASCADE;
					DROP FUNCTION IF EXISTS
						public.inject_admin_bootstrap_side_effect() CASCADE;
					DROP FUNCTION IF EXISTS
						public.inject_deferred_admin_assignment() CASCADE`)
			})

			login := fmt.Sprintf(
				"platformgo_admin_bootstrap_trigger_test_login_%d",
				index,
			)
			terminal := runtimeRoleLoginPool(
				t,
				admin,
				login,
				"platformgo_admin_bootstrap",
			)
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-trigger-key-%d", index)),
			)
			bootstrapCtx, cancelBootstrap := context.WithTimeout(
				ctx,
				5*time.Second,
			)
			defer cancelBootstrap()
			got, attempts, err :=
				queryAdminBootstrapAfterTransientLockContention(
					bootstrapCtx,
					terminal,
					fmt.Sprintf("bootstrap-request-trigger-%d", index),
					keyHash[:],
					fmt.Sprintf(
						"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
						80+index,
					),
					fmt.Sprintf(
						"00000000-0000-4000-8000-%012d",
						80+index,
					),
					"2026-07-31T00:00:00.000000Z",
					runtimeAuthorityACLMigrationChecksum(t),
				)
			if attempts > 1 {
				t.Logf(
					"explicit runtime bootstrap lock-contention retry "+
						"reached catalog-authority rejection on attempt %d",
					attempts,
				)
			}
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf(
					"bootstrap with unexpected trigger error = %v "+
						"outcome %q, want 55000",
					err,
					got.outcome,
				)
			}

			var assignments, receipts, sideEffects int
			if err := admin.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM identity.rbac_admin_roles),
					(SELECT count(*) FROM audit.admin_bootstrap_events),
					(SELECT count(*)
					   FROM public.admin_bootstrap_side_effect)`,
			).Scan(&assignments, &receipts, &sideEffects); err != nil {
				t.Fatalf("inspect rejected unexpected trigger state: %v", err)
			}
			if assignments != 0 || receipts != 0 || sideEffects != 0 {
				t.Fatalf(
					"rejected trigger state = assignments %d receipts %d "+
						"side effects %d",
					assignments,
					receipts,
					sideEffects,
				)
			}
		})
	}
}

func TestAdminBootstrapRejectsUnsafeTemporaryMemberAuthority(t *testing.T) {
	tests := []struct {
		name      string
		setupSQL  func(login string) string
		repairSQL func(login string) string
	}{
		{
			name: "second parent membership",
			setupSQL: func(login string) string {
				return `CREATE ROLE platformgo_admin_bootstrap_extra_parent
						NOLOGIN;
					GRANT platformgo_admin_bootstrap_extra_parent TO ` +
					pgx.Identifier{login}.Sanitize()
			},
			repairSQL: func(login string) string {
				return `REVOKE platformgo_admin_bootstrap_extra_parent FROM ` +
					pgx.Identifier{login}.Sanitize() +
					`;
					DROP ROLE platformgo_admin_bootstrap_extra_parent`
			},
		},
		{
			name: "owned object dependency",
			setupSQL: func(login string) string {
				return `CREATE TABLE public.bootstrap_member_owned_object (
						id integer
					);
					ALTER TABLE public.bootstrap_member_owned_object OWNER TO ` +
					pgx.Identifier{login}.Sanitize()
			},
			repairSQL: func(string) string {
				return `DROP TABLE public.bootstrap_member_owned_object`
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}
			login := fmt.Sprintf(
				"platformgo_admin_bootstrap_member_safety_%d",
				index,
			)
			terminal := runtimeRoleLoginPool(
				t,
				admin,
				login,
				"platformgo_admin_bootstrap",
			)
			if _, err := admin.Exec(ctx, test.setupSQL(login)); err != nil {
				t.Fatalf("install unsafe temporary member authority: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(
					context.Background(),
					test.repairSQL(login),
				)
			})

			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-member-safety-key-%d", index)),
			)
			assertAdminBootstrapSQLState(
				t,
				terminal,
				"55000",
				fmt.Sprintf("bootstrap-request-member-safety-%d", index),
				keyHash[:],
				fmt.Sprintf(
					"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
					100+index,
				),
				fmt.Sprintf(
					"00000000-0000-4000-8000-%012d",
					100+index,
				),
				"2026-07-31T00:00:00.000000Z",
			)
			var assignments, receipts int
			if err := admin.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM identity.rbac_admin_roles),
					(SELECT count(*) FROM audit.admin_bootstrap_events)`,
			).Scan(&assignments, &receipts); err != nil {
				t.Fatalf("inspect rejected unsafe temporary member: %v", err)
			}
			if assignments != 0 || receipts != 0 {
				t.Fatalf(
					"unsafe member rejection state = assignments %d receipts %d",
					assignments,
					receipts,
				)
			}
			if _, err := admin.Exec(ctx, test.repairSQL(login)); err != nil {
				t.Fatalf("repair temporary member authority: %v", err)
			}
			result := callAdminBootstrap(
				t,
				terminal,
				fmt.Sprintf("bootstrap-request-member-safety-%d", index),
				keyHash[:],
				fmt.Sprintf(
					"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
					100+index,
				),
				fmt.Sprintf(
					"00000000-0000-4000-8000-%012d",
					100+index,
				),
				"2026-07-31T00:00:00.000000Z",
			)
			if result.outcome != "created" {
				t.Fatalf("repaired temporary member outcome = %q", result.outcome)
			}
		})
	}
}

func TestAdminBootstrapRejectsAmbiguousIndirectCallerIdentity(t *testing.T) {
	tests := []struct {
		name          string
		directLogin   string
		indirectLogin func(t *testing.T, admin *pgxpool.Pool, direct string) string
	}{
		{
			name:        "case folded login",
			directLogin: "platformgo_admin_bootstrap_case_delegate",
			indirectLogin: func(
				_ *testing.T,
				_ *pgxpool.Pool,
				direct string,
			) string {
				return strings.ToUpper(direct)
			},
		},
		{
			name:        "numeric login parsed as role OID",
			directLogin: "platformgo_admin_bootstrap_numeric_delegate",
			indirectLogin: func(
				t *testing.T,
				admin *pgxpool.Pool,
				direct string,
			) string {
				t.Helper()
				var login string
				if err := admin.QueryRow(context.Background(), `
					SELECT oid::text
					  FROM pg_catalog.pg_roles
					 WHERE rolname = $1`,
					direct,
				).Scan(&login); err != nil {
					t.Fatalf("read direct bootstrap member OID: %v", err)
				}
				return login
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}

			directID := pgx.Identifier{test.directLogin}.Sanitize()
			extraLogin := fmt.Sprintf(
				"platformgo_admin_bootstrap_ambiguous_extra_%d",
				index,
			)
			extraID := pgx.Identifier{extraLogin}.Sanitize()
			if _, err := admin.Exec(ctx, fmt.Sprintf(`
				CREATE ROLE %[1]s LOGIN PASSWORD 'platformgo-test-password'
					NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
				GRANT platformgo_admin_bootstrap TO %[1]s;
				CREATE ROLE %[2]s NOLOGIN
					NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION`,
				directID,
				extraID,
			)); err != nil {
				t.Fatalf("create direct bootstrap member: %v", err)
			}
			indirectLogin := test.indirectLogin(t, admin, test.directLogin)
			indirectID := pgx.Identifier{indirectLogin}.Sanitize()
			if _, err := admin.Exec(ctx, fmt.Sprintf(`
				CREATE ROLE %[1]s LOGIN PASSWORD 'platformgo-test-password'
					NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION;
				GRANT %[2]s TO %[1]s;
				GRANT %[3]s TO %[1]s`,
				indirectID,
				directID,
				extraID,
			)); err != nil {
				t.Fatalf("create ambiguous indirect bootstrap caller: %v", err)
			}
			t.Cleanup(func() {
				for _, role := range []string{
					indirectID,
					directID,
					extraID,
				} {
					_, _ = admin.Exec(
						context.Background(),
						"DROP OWNED BY "+role,
					)
					_, _ = admin.Exec(
						context.Background(),
						"DROP ROLE IF EXISTS "+role,
					)
				}
			})

			terminal := existingAdminBootstrapLoginPool(t, indirectLogin)
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-ambiguous-caller-key-%d", index)),
			)
			assertAdminBootstrapSQLState(
				t,
				terminal,
				"55000",
				fmt.Sprintf("bootstrap-request-ambiguous-caller-%d", index),
				keyHash[:],
				fmt.Sprintf(
					"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
					110+index,
				),
				fmt.Sprintf(
					"00000000-0000-4000-8000-%012d",
					110+index,
				),
				"2026-07-31T00:00:00.000000Z",
			)
			assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)
		})
	}
}

func TestAdminBootstrapPreservesExactQuotedCallerIdentity(t *testing.T) {
	tests := []struct {
		name  string
		login string
	}{
		{
			name:  "mixed case login",
			login: "PlatformGo_Admin_Bootstrap_Exact_Caller",
		},
		{
			name:  "numeric login",
			login: "4000000000",
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
				t.Fatalf("migrate admin bootstrap authority: %v", err)
			}

			terminal := runtimeRoleLoginPool(
				t,
				admin,
				test.login,
				"platformgo_admin_bootstrap",
			)
			requestID := fmt.Sprintf(
				"bootstrap-request-exact-caller-%d",
				index,
			)
			subject := fmt.Sprintf(
				"admin::urn:xb:admin:00000000-0000-4000-8000-%012d",
				120+index,
			)
			eventID := fmt.Sprintf(
				"00000000-0000-4000-8000-%012d",
				120+index,
			)
			logicalTime := "2026-07-31T00:00:00.000000Z"
			keyHash := sha256.Sum256(
				[]byte(fmt.Sprintf("bootstrap-exact-caller-key-%d", index)),
			)
			created := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
			)
			replayed := callAdminBootstrap(
				t,
				terminal,
				requestID,
				keyHash[:],
				subject,
				eventID,
				logicalTime,
			)
			if replayed != created || created.outcome != "created" {
				t.Fatalf(
					"exact caller created/replayed = %#v/%#v",
					created,
					replayed,
				)
			}

			wantRequestHash := sha256.Sum256([]byte(adminBootstrapPreimage(
				test.login,
				requestID,
				subject,
				eventID,
				logicalTime,
			)))
			var actorLogin, requestHash string
			if err := admin.QueryRow(ctx, `
				SELECT actor_login, pg_catalog.encode(request_hash, 'hex')
				  FROM audit.admin_bootstrap_events
				 WHERE event_id = $1`,
				eventID,
			).Scan(&actorLogin, &requestHash); err != nil {
				t.Fatalf("read exact caller receipt: %v", err)
			}
			if actorLogin != test.login ||
				requestHash != hex.EncodeToString(wantRequestHash[:]) {
				t.Fatalf(
					"exact caller receipt actor/hash = %q/%q, want %q/%x",
					actorLogin,
					requestHash,
					test.login,
					wantRequestHash,
				)
			}
		})
	}
}

func TestAdminBootstrapSupportsExactMixedCaseMigrationOwner(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)

	const ownerLogin = "PlatformGo_Admin_Bootstrap_Mixed_Owner"
	owner := adminBootstrapSuperuserLoginPool(t, admin, ownerLogin)
	if err := migrateAdminBootstrapCurrent(t, ctx, owner); err != nil {
		t.Fatalf("migrate as exact mixed-case owner: %v", err)
	}

	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_mixed_owner_terminal",
		"platformgo_admin_bootstrap",
	)
	keyHash := sha256.Sum256([]byte("bootstrap-mixed-owner-key"))
	result := callAdminBootstrap(
		t,
		terminal,
		"bootstrap-request-mixed-owner",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000070",
		"00000000-0000-4000-8000-000000000070",
		"2026-07-31T00:00:00.000000Z",
	)
	if result.outcome != "created" {
		t.Fatalf("mixed-case owner bootstrap outcome = %q, want created", result.outcome)
	}
}

func TestAdminBootstrapMigrationRejectsAmbiguousOwnerAlias(t *testing.T) {
	tests := []struct {
		name       string
		aliasLogin func(t *testing.T, admin *pgxpool.Pool, owner string) string
	}{
		{
			name: "case folded owner",
			aliasLogin: func(
				_ *testing.T,
				_ *pgxpool.Pool,
				owner string,
			) string {
				return strings.ToUpper(owner)
			},
		},
		{
			name: "numeric owner parsed as role OID",
			aliasLogin: func(
				t *testing.T,
				admin *pgxpool.Pool,
				owner string,
			) string {
				t.Helper()
				var login string
				if err := admin.QueryRow(context.Background(), `
					SELECT oid::text
					  FROM pg_catalog.pg_roles
					 WHERE rolname = $1`,
					owner,
				).Scan(&login); err != nil {
					t.Fatalf("read predecessor owner OID: %v", err)
				}
				return login
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)

			lowerOwnerLogin := fmt.Sprintf(
				"platformgo_admin_bootstrap_alias_owner_%d",
				index,
			)
			lowerOwner := adminBootstrapSuperuserLoginPool(
				t,
				admin,
				lowerOwnerLogin,
			)
			if err := platformpostgres.NewMigrator(
				lowerOwner,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip as exact owner: %v", err)
			}

			aliasOwner := adminBootstrapSuperuserLoginPool(
				t,
				admin,
				test.aliasLogin(t, admin, lowerOwnerLogin),
			)
			migrationCtx, cancelMigration := context.WithTimeout(
				ctx,
				10*time.Second,
			)
			defer cancelMigration()
			err := migrateAdminBootstrapAfterTransientLockContention(
				t,
				migrationCtx,
				platformpostgres.NewMigrator(
					aliasOwner,
					migrationFilesThrough(t, runtimeAuthorityACLMigration),
				),
			)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf("ambiguous owner alias migration error = %v, want 55000", err)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			assertMigrationHistoryTip(t, admin, 41, adminPermissionMigration)
		})
	}
}

func TestAdminBootstrapUnknownCommitReplaysAfterTerminalRestart(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	const login = "platformgo_admin_bootstrap_restart_test_login"
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		login,
		"platformgo_admin_bootstrap",
	)
	subject := "admin::urn:xb:admin:00000000-0000-4000-8000-000000000047"
	requestID := "bootstrap-request-restart"
	eventID := "00000000-0000-4000-8000-000000000047"
	logicalTime := "2026-07-31T00:00:00.000000Z"
	keyHash := sha256.Sum256([]byte("stable-bootstrap-restart-key"))

	_ = callAdminBootstrap(
		t,
		terminal,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	terminal.Close()

	restarted := existingAdminBootstrapLoginPool(t, login)
	replayed := callAdminBootstrap(
		t,
		restarted,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	)
	want := adminBootstrapResult{
		outcome:              "created",
		adminSubject:         subject,
		roleName:             adminBootstrapRoleName,
		configurationVersion: 1,
		eventID:              eventID,
		logicalTimeText:      logicalTime,
	}
	if replayed != want {
		t.Fatalf("restart replay result = %#v, want %#v", replayed, want)
	}
}

func TestAdminBootstrapAcknowledgesOnlyAfterCommitAndFreshSessionVerification(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	const login = "platformgo_admin_bootstrap_commit_test_login"
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		login,
		"platformgo_admin_bootstrap",
	)
	const (
		requestID   = "bootstrap-request-commit-boundary"
		subject     = "admin::urn:xb:admin:00000000-0000-4000-8000-00000000004a"
		eventID     = "00000000-0000-4000-8000-00000000004a"
		logicalTime = "2026-07-31T00:00:00.000000Z"
	)
	keyHash := sha256.Sum256([]byte("stable-bootstrap-commit-boundary-key"))

	bootstrapTx, err := terminal.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bootstrap transaction: %v", err)
	}
	var provisionalOutcome string
	if err := bootstrapTx.QueryRow(ctx, `
		SELECT outcome
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5, $6)`,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
		runtimeAuthorityACLMigrationChecksum(t),
	).Scan(&provisionalOutcome); err != nil {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("bootstrap inside explicit transaction: %v", err)
	}
	if provisionalOutcome != "created" {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("provisional outcome = %q, want created", provisionalOutcome)
	}

	var visibleBeforeCommit int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.rbac_admin_roles
		 WHERE admin_subject = $1`,
		subject,
	).Scan(&visibleBeforeCommit); err != nil {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("inspect authority before commit: %v", err)
	}
	if visibleBeforeCommit != 0 {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf(
			"authority visible before commit = %d, want 0",
			visibleBeforeCommit,
		)
	}
	if err := bootstrapTx.Commit(ctx); err != nil {
		t.Fatalf("commit bootstrap transaction: %v", err)
	}
	terminal.Close()

	fresh := postgresPool(t)
	requestHash := sha256.Sum256([]byte(adminBootstrapPreimage(
		login,
		requestID,
		subject,
		eventID,
		logicalTime,
	)))
	var (
		exactAuthority    bool
		receipts          int
		permitted         bool
		migrationChecksum string
	)
	if err := fresh.QueryRow(ctx, `
		SELECT
			(
				(SELECT count(*) FROM identity.rbac_roles) = 1
				AND (
					SELECT count(*)
					  FROM identity.rbac_roles
					 WHERE role_id = $9
					   AND name = $10
					   AND builtin
				) = 1
				AND (
					SELECT count(*) FROM identity.rbac_role_parents
				) = 0
				AND (
					SELECT count(*) FROM identity.rbac_admin_roles
				) = 1
				AND (
					SELECT count(*)
					  FROM identity.rbac_admin_roles
					 WHERE admin_subject = $1
					   AND role_id = $9
				) = 1
				AND (
					SELECT count(*) FROM identity.rbac_policies
				) = 1
				AND (
					SELECT count(*)
					  FROM identity.rbac_policies
					 WHERE role_id = $9
					   AND resource = '*'
					   AND action = '*'
					   AND effect = 'allow'
				) = 1
				AND (
					SELECT count(*)
					  FROM audit.admin_bootstrap_events
				) = 1
				AND (
					SELECT count(*)
					  FROM audit.admin_bootstrap_events AS event
					 WHERE event.event_id = $6::uuid
					   AND event.admin_sequence = 1
					   AND event.actor_login = $8
					   AND event.request_id = $2
					   AND event.idempotency_key_hash = $4
					   AND event.request_hash = $5
					   AND event.admin_subject = $1
					   AND event.logical_time_text = $7
					   AND event.occurred_at = $7::timestamptz
					   AND event.role_id = $9
					   AND event.role_name = $10
					   AND event.configuration_version = 1
					   AND event.outcome = 'success'
					   AND event.detail = pg_catalog.jsonb_build_object(
						   'after',
						   pg_catalog.jsonb_build_object(
							   'adminSubject', $1::text,
							   'roleName', $10::text,
							   'configurationVersion', 1
						   ),
						   'before',
						   NULL,
						   'operationVersion',
						   'platformgo.admin-bootstrap.request.v1',
						   'policy',
						   pg_catalog.jsonb_build_object(
							   'resource', '*',
							   'action', '*',
							   'effect', 'allow'
						   )
					   )
				) = 1
			),
			(
				SELECT count(*)
				  FROM audit.admin_bootstrap_events
				 WHERE request_id = $2
				   AND admin_subject = $1
			),
			identity.admin_has_permission($1, 'roles', 'read'),
			(
				SELECT pg_catalog.encode(checksum, 'hex')
				  FROM engine.schema_migrations
				 WHERE filename = $3
			)`,
		subject,
		requestID,
		runtimeAuthorityACLMigration,
		keyHash[:],
		requestHash[:],
		eventID,
		logicalTime,
		login,
		adminBootstrapRoleID,
		adminBootstrapRoleName,
	).Scan(
		&exactAuthority,
		&receipts,
		&permitted,
		&migrationChecksum,
	); err != nil {
		t.Fatalf("fresh-session durable bootstrap verification: %v", err)
	}
	if !exactAuthority ||
		receipts != 1 ||
		!permitted ||
		migrationChecksum != hex.EncodeToString(runtimeAuthorityACLMigrationChecksum(t)) {
		t.Fatalf(
			"fresh-session authority = exact %t receipts %d "+
				"permitted %t checksum %q",
			exactAuthority,
			receipts,
			permitted,
			migrationChecksum,
		)
	}
	if err := verifyAdminBootstrapMigrationBeforeTerminalCleanup(
		ctx,
		fresh,
		runtimeAuthorityACLMigrationChecksum(t),
	); err != nil {
		t.Fatalf("fresh-session migration verification before cleanup: %v", err)
	}

	loginIdentifier := pgx.Identifier{login}.Sanitize()
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		REVOKE platformgo_admin_bootstrap FROM %s;
		ALTER ROLE %s NOLOGIN`,
		loginIdentifier,
		loginIdentifier,
	)); err != nil {
		t.Fatalf("remove terminal bootstrap authority: %v", err)
	}
	var canLogin, remainsMember bool
	if err := fresh.QueryRow(ctx, `
		SELECT
			role.rolcanlogin,
			pg_catalog.pg_has_role(
				role.rolname,
				'platformgo_admin_bootstrap',
				'member'
			)
		  FROM pg_catalog.pg_roles AS role
		 WHERE role.rolname = $1`,
		login,
	).Scan(&canLogin, &remainsMember); err != nil {
		t.Fatalf("verify terminal bootstrap removal: %v", err)
	}
	if canLogin || remainsMember {
		t.Fatalf(
			"terminal bootstrap login after cleanup = login %t member %t",
			canLogin,
			remainsMember,
		)
	}
}

func verifyAdminBootstrapMigrationBeforeTerminalCleanup(
	ctx context.Context,
	pool *pgxpool.Pool,
	expectedChecksum []byte,
) error {
	var checksum string
	if err := pool.QueryRow(ctx, `
		SELECT pg_catalog.encode(checksum, 'hex')
		  FROM engine.schema_migrations
		 WHERE filename = $1`, runtimeAuthorityACLMigration).Scan(&checksum); err != nil {
		return fmt.Errorf("read runtime-authority migration checksum: %w", err)
	}
	if checksum != hex.EncodeToString(expectedChecksum) {
		return fmt.Errorf("runtime-authority migration checksum = %q", checksum)
	}
	return nil
}

func TestAdminBootstrapAuditReceiptRejectsOwnerTruncate(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_truncate_test_login",
		"platformgo_admin_bootstrap",
	)
	keyHash := sha256.Sum256([]byte("stable-bootstrap-truncate-key"))
	callAdminBootstrap(
		t,
		terminal,
		"bootstrap-request-truncate",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000048",
		"00000000-0000-4000-8000-000000000048",
		"2026-07-31T00:00:00.000000Z",
	)

	if _, err := admin.Exec(ctx, `
		TRUNCATE TABLE audit.admin_bootstrap_events`,
	); !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("owner truncate bootstrap audit error = %v, want 55000", err)
	}
	var receiptCount, truncateTriggerCount int
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM audit.admin_bootstrap_events),
			(SELECT count(*)
			   FROM pg_catalog.pg_trigger AS trigger
			  WHERE trigger.tgrelid =
					'audit.admin_bootstrap_events'::pg_catalog.regclass
			    AND trigger.tgname =
					'admin_bootstrap_events_reject_truncate'
			    AND NOT trigger.tgisinternal
			    AND trigger.tgenabled = 'O'
			    AND (trigger.tgtype & 32) = 32
			    AND (trigger.tgtype & 1) = 0)`,
	).Scan(&receiptCount, &truncateTriggerCount); err != nil {
		t.Fatalf("inspect bootstrap truncate protection: %v", err)
	}
	if receiptCount != 1 || truncateTriggerCount != 1 {
		t.Fatalf(
			"bootstrap receipt/statement truncate trigger = %d/%d",
			receiptCount,
			truncateTriggerCount,
		)
	}
}

func TestAdminBootstrapMigrationUpgradesPopulatedPreviousTipWithoutRewrite(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email
		) VALUES (
			'urn:xb:user:admin-bootstrap-preserved',
			'bootstrap-preserved',
			'bootstrap-preserved',
			'bootstrap-preserved@example.com',
			'bootstrap-preserved@example.com'
		);
		INSERT INTO identity.api_keys (
			api_key_id, owner_user_id, name, key_hash, prefix, scopes, created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000045',
			'urn:xb:user:admin-bootstrap-preserved',
			'preserved',
			decode(repeat('45', 32), 'hex'),
			'000000000045',
			ARRAY['accounts:read'],
			'2026-07-31T00:00:00Z'
		);
		INSERT INTO audit.events (
			event_id, occurred_at, request_id, actor_kind, actor_id,
			action, target_kind, target_id, outcome, detail
		) VALUES (
			'00000000-0000-4000-8000-000000000045',
			'2026-07-31T00:00:00Z',
			'preserved-audit',
			'user',
			'urn:xb:user:admin-bootstrap-preserved',
			'api_key.created',
			'api_key',
			'00000000-0000-4000-8000-000000000045',
			'success',
			'{"preserved":true}'::jsonb
		)`,
	); err != nil {
		t.Fatalf("seed populated previous tip: %v", err)
	}

	beforeDigest, beforeFiles := readAdminBootstrapPreservedState(t, admin)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, adminBootstrapMigration),
		),
	); err != nil {
		t.Fatalf("upgrade admin bootstrap authority: %v", err)
	}
	afterDigest, afterFiles := readAdminBootstrapPreservedState(t, admin)
	if beforeDigest != afterDigest || beforeFiles != afterFiles {
		t.Fatalf(
			"bootstrap upgrade changed prior data/files: digest %s->%s files %s->%s",
			beforeDigest,
			afterDigest,
			beforeFiles,
			afterFiles,
		)
	}
	var roleCount, policyCount, assignmentCount, eventCount int
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.rbac_roles),
			(SELECT count(*) FROM identity.rbac_policies),
			(SELECT count(*) FROM identity.rbac_admin_roles),
			(SELECT count(*) FROM audit.admin_bootstrap_events)`,
	).Scan(&roleCount, &policyCount, &assignmentCount, &eventCount); err != nil {
		t.Fatalf("inspect upgraded bootstrap graph: %v", err)
	}
	if roleCount != 1 || policyCount != 1 ||
		assignmentCount != 0 || eventCount != 0 {
		t.Fatalf(
			"upgraded bootstrap graph = roles:%d policies:%d assignments:%d events:%d",
			roleCount,
			policyCount,
			assignmentCount,
			eventCount,
		)
	}
}

func TestAdminBootstrapMigrationRejectsNonemptyGraphAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.rbac_roles (role_id, name, builtin)
		VALUES (
			'00000000-0000-4000-8000-000000000046',
			'preexisting-role',
			false
		)`,
	); err != nil {
		t.Fatalf("seed nonempty RBAC graph: %v", err)
	}
	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	err := migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("nonempty graph migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		DELETE FROM identity.rbac_roles
		 WHERE role_id = '00000000-0000-4000-8000-000000000046'`,
	); err != nil {
		t.Fatalf("remove rejected graph fixture: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry bootstrap migration after graph repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRejectsEnabledEventTriggers(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE TABLE public.admin_bootstrap_ddl_side_effect (
			tag text NOT NULL
		);
		CREATE FUNCTION public.observe_admin_bootstrap_ddl()
		RETURNS event_trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
			INSERT INTO public.admin_bootstrap_ddl_side_effect (tag)
			VALUES (TG_TAG);
		END
		$$;
		CREATE EVENT TRIGGER observe_admin_bootstrap_ddl
		ON ddl_command_start
		WHEN TAG IN ('CREATE SCHEMA', 'CREATE TABLE')
		EXECUTE FUNCTION public.observe_admin_bootstrap_ddl()`); err != nil {
		t.Fatalf("install enabled event trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP EVENT TRIGGER IF EXISTS observe_admin_bootstrap_ddl;
			DROP FUNCTION IF EXISTS public.observe_admin_bootstrap_ddl();
			DROP TABLE IF EXISTS public.admin_bootstrap_ddl_side_effect`)
	})

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("enabled event-trigger migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	var sideEffects int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM public.admin_bootstrap_ddl_side_effect`,
	).Scan(&sideEffects); err != nil {
		t.Fatalf("inspect pre-fence event-trigger effects: %v", err)
	}
	if sideEffects != 0 {
		t.Fatalf(
			"pre-fence event-trigger effects = %d, want 0",
			sideEffects,
		)
	}
	if _, err := admin.Exec(ctx, `
		DROP EVENT TRIGGER observe_admin_bootstrap_ddl;
		DROP FUNCTION public.observe_admin_bootstrap_ddl()`); err != nil {
		t.Fatalf("remove enabled event-trigger fixture: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after event-trigger repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationFencesExactJournalCatalog(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE RULE suppress_admin_bootstrap_journal
		AS ON INSERT TO engine.schema_migrations
		WHERE NEW.filename =
			'20260731000100_phase3_admin_bootstrap_authority.up.sql'
		DO INSTEAD NOTHING`); err != nil {
		t.Fatalf("install hostile migration journal rule: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP RULE IF EXISTS suppress_admin_bootstrap_journal
				ON engine.schema_migrations`)
	})

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("divergent journal migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		DROP RULE suppress_admin_bootstrap_journal
			ON engine.schema_migrations`); err != nil {
		t.Fatalf("repair migration journal catalog: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after journal repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigratorRejectsSuppressedJournalInsert(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	files := migrationFilesThrough(t, adminBootstrapMigration)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(admin, files),
	); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	const testMigration = "99999999999999_test_journal_insert_guard.up.sql"
	files[testMigration] = &fstest.MapFile{
		Data: []byte(
			"CREATE TABLE public.admin_bootstrap_journal_guard_sentinel (" +
				"singleton boolean PRIMARY KEY);",
		),
	}
	if _, err := admin.Exec(ctx, `
		CREATE RULE suppress_test_journal_insert
		AS ON INSERT TO engine.schema_migrations
		WHERE NEW.filename =
			'99999999999999_test_journal_insert_guard.up.sql'
		DO INSTEAD NOTHING`,
	); err != nil {
		t.Fatalf("install suppressed journal insert: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP RULE IF EXISTS suppress_test_journal_insert
				ON engine.schema_migrations;
			DROP TABLE IF EXISTS
				public.admin_bootstrap_journal_guard_sentinel`)
	})

	current := platformpostgres.NewMigrator(admin, files)
	if err := current.Migrate(ctx); err == nil {
		t.Fatal("migration with suppressed journal insert unexpectedly succeeded")
	}
	var journaled, sentinelPresent bool
	if err := admin.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			pg_catalog.to_regclass(
				'public.admin_bootstrap_journal_guard_sentinel'
			) IS NOT NULL`,
		testMigration,
	).Scan(&journaled, &sentinelPresent); err != nil {
		t.Fatalf("inspect rejected journal insert: %v", err)
	}
	if journaled || sentinelPresent {
		t.Fatalf(
			"suppressed journal insert state = journaled %t sentinel %t",
			journaled,
			sentinelPresent,
		)
	}

	if _, err := admin.Exec(ctx, `
		DROP RULE suppress_test_journal_insert
			ON engine.schema_migrations`); err != nil {
		t.Fatalf("repair suppressed journal insert: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after journal-rule repair: %v", err)
	}
	if err := admin.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			pg_catalog.to_regclass(
				'public.admin_bootstrap_journal_guard_sentinel'
			) IS NOT NULL`,
		testMigration,
	).Scan(&journaled, &sentinelPresent); err != nil {
		t.Fatalf("inspect retried journal insert: %v", err)
	}
	if !journaled || !sentinelPresent {
		t.Fatalf(
			"retried journal insert state = journaled %t sentinel %t",
			journaled,
			sentinelPresent,
		)
	}
}

func TestAdminBootstrapMigrationRevalidatesLockedJournalRows(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	var exactChecksum []byte
	if err := admin.QueryRow(ctx, `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		adminPermissionMigration,
	).Scan(&exactChecksum); err != nil {
		t.Fatalf("read exact previous-tip checksum: %v", err)
	}

	hostileTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent journal corruption: %v", err)
	}
	if _, err := hostileTx.Exec(ctx, `
		UPDATE engine.schema_migrations
		   SET checksum = pg_catalog.decode(pg_catalog.repeat('00', 32), 'hex')
		 WHERE filename = $1`,
		adminPermissionMigration,
	); err != nil {
		_ = hostileTx.Rollback(ctx)
		t.Fatalf("stage concurrent journal corruption: %v", err)
	}

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- current.Migrate(ctx)
	}()
	awaitAdminBootstrapRelationLockWait(
		t,
		admin,
		migrationResult,
		"engine.schema_migrations",
		"ShareRowExclusiveLock",
	)
	if err := hostileTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent journal corruption: %v", err)
	}
	migrationErr := <-migrationResult
	if !errors.Is(migrationErr, platformpostgres.ErrMigrationChecksumMismatch) {
		t.Fatalf(
			"migration after journal corruption error = %v, want checksum mismatch",
			migrationErr,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	var (
		corruptedChecksum []byte
		roleCount         int
		policyCount       int
		auditExists       bool
		functionExists    bool
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(
				SELECT checksum
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			(SELECT count(*) FROM identity.rbac_roles),
			(SELECT count(*) FROM identity.rbac_policies),
			pg_catalog.to_regclass(
				'audit.admin_bootstrap_events'
			) IS NOT NULL,
			pg_catalog.to_regprocedure(
				'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
			) IS NOT NULL`,
		adminPermissionMigration,
	).Scan(
		&corruptedChecksum,
		&roleCount,
		&policyCount,
		&auditExists,
		&functionExists,
	); err != nil {
		t.Fatalf("inspect rejected journal corruption: %v", err)
	}
	if string(corruptedChecksum) == string(exactChecksum) ||
		roleCount != 0 ||
		policyCount != 0 ||
		auditExists ||
		functionExists {
		t.Fatalf(
			"journal corruption rollback = checksum %x roles %d policies %d "+
				"audit %t function %t",
			corruptedChecksum,
			roleCount,
			policyCount,
			auditExists,
			functionExists,
		)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE engine.schema_migrations
		   SET checksum = $2
		 WHERE filename = $1`,
		adminPermissionMigration,
		exactChecksum,
	); err != nil {
		t.Fatalf("repair previous-tip checksum: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after journal repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRejectsDisabledInternalTriggers(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.rbac_admin_roles DISABLE TRIGGER ALL`); err != nil {
		t.Fatalf("disable RBAC internal triggers: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			ALTER TABLE identity.rbac_admin_roles ENABLE TRIGGER ALL`)
	})

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("disabled internal-trigger migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.rbac_admin_roles ENABLE TRIGGER ALL`); err != nil {
		t.Fatalf("repair RBAC internal triggers: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after internal-trigger repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRejectsExternalAuthoritySchemaOwners(
	t *testing.T,
) {
	for _, schemaName := range []string{"engine", "identity", "audit"} {
		t.Run(schemaName, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip: %v", err)
			}
			const hostileOwner = "platformgo_admin_bootstrap_schema_owner"
			_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+hostileOwner)
			if _, err := admin.Exec(ctx, `
				CREATE ROLE platformgo_admin_bootstrap_schema_owner NOLOGIN;
				ALTER SCHEMA `+pgx.Identifier{schemaName}.Sanitize()+`
					OWNER TO platformgo_admin_bootstrap_schema_owner`); err != nil {
				t.Fatalf("install external schema owner: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(context.Background(), `
					ALTER SCHEMA `+pgx.Identifier{schemaName}.Sanitize()+`
						OWNER TO CURRENT_USER;
					DROP ROLE IF EXISTS `+hostileOwner)
			})

			current := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminBootstrapMigration),
			)
			if err := migrateAdminBootstrapAfterTransientLockContention(
				t,
				ctx,
				current,
			); !adminBootstrapIsPostgresCode(
				err,
				"55000",
			) {
				t.Fatalf(
					"external %s schema owner migration error = %v, want 55000",
					schemaName,
					err,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			var owner string
			if err := admin.QueryRow(ctx, `
				SELECT namespace.nspowner::pg_catalog.regrole::text
				  FROM pg_catalog.pg_namespace AS namespace
				 WHERE namespace.nspname = $1`,
				schemaName,
			).Scan(&owner); err != nil {
				t.Fatalf("inspect preserved schema owner: %v", err)
			}
			if owner != hostileOwner {
				t.Fatalf("preserved schema owner = %q, want %q", owner, hostileOwner)
			}
			if _, err := admin.Exec(
				ctx,
				"ALTER SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+
					" OWNER TO CURRENT_USER",
			); err != nil {
				t.Fatalf("repair schema owner: %v", err)
			}
			if err := migrateAdminBootstrapAfterTransientLockContention(
				t,
				ctx,
				current,
			); err != nil {
				t.Fatalf("retry migration after schema-owner repair: %v", err)
			}
			assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
		})
	}
}

func TestAdminBootstrapMigrationRejectsInheritedAuthorityRelations(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE TABLE public.inherited_admin_assignments ()
			INHERITS (identity.rbac_admin_roles)`); err != nil {
		t.Fatalf("install inherited authority relation: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP TABLE IF EXISTS public.inherited_admin_assignments`)
	})

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("inherited authority migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		DROP TABLE public.inherited_admin_assignments`); err != nil {
		t.Fatalf("remove inherited authority relation: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after inheritance repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationMapsMissingObjectPrelockToDivergence(
	t *testing.T,
) {
	for _, test := range []struct {
		name       string
		installSQL string
	}{
		{
			name:       "missing audit schema",
			installSQL: `DROP SCHEMA audit CASCADE`,
		},
		{
			name: "missing permission function",
			installSQL: `
				DROP FUNCTION
					identity.admin_has_permission(text,text,text)`,
		},
		{
			name: "permission procedure replaces function",
			installSQL: `
				DROP FUNCTION
					identity.admin_has_permission(text,text,text);
				CREATE PROCEDURE identity.admin_has_permission(
					text,
					text,
					text
				)
				LANGUAGE sql
				AS 'SELECT'`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip: %v", err)
			}
			if _, err := admin.Exec(ctx, test.installSQL); err != nil {
				t.Fatalf("install divergent object catalog: %v", err)
			}

			current := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminBootstrapMigration),
			)
			err := migrateAdminBootstrapAfterTransientLockContention(
				t,
				ctx,
				current,
			)
			if !adminBootstrapIsPostgresCode(err, "55000") {
				t.Fatalf(
					"divergent object catalog error = %v, want 55000",
					err,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
		})
	}
}

func TestAdminBootstrapMigrationRejectsMissingEarlierObjectBeforeLaterWait(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		ALTER SCHEMA audit RENAME TO audit_prelock_missing`); err != nil {
		t.Fatalf("hide earlier prelock schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			ALTER SCHEMA audit_prelock_missing RENAME TO audit`)
	})

	laterObjectTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin later prelock object blocker: %v", err)
	}
	t.Cleanup(func() {
		_ = laterObjectTx.Rollback(context.Background())
	})
	if _, err := laterObjectTx.Exec(ctx, `
		DROP FUNCTION identity.admin_has_permission(text,text,text)`); err != nil {
		t.Fatalf("lock later prelock function object: %v", err)
	}

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	err = current.Migrate(migrateCtx)
	cancel()
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf(
			"missing earlier prelock object error = %v, want 55000",
			err,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)

	if err := laterObjectTx.Rollback(ctx); err != nil {
		t.Fatalf("release later prelock object blocker: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		ALTER SCHEMA audit_prelock_missing RENAME TO audit`); err != nil {
		t.Fatalf("restore earlier prelock schema: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry after prelock object repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRejectsDivergentPermissionAuthority(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	previous := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	)
	if err := previous.Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE OR REPLACE FUNCTION identity.admin_has_permission(
			requested_subject text,
			requested_resource text,
			requested_action text
		)
		RETURNS boolean
		LANGUAGE sql
		STABLE
		SECURITY DEFINER
		SET search_path = pg_catalog
		AS 'SELECT true'`,
	); err != nil {
		t.Fatalf("alter trusted permission function: %v", err)
	}
	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	err := migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("altered permission function migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	var alteredBody string
	if err := admin.QueryRow(ctx, `
		SELECT procedure.prosrc
		  FROM pg_catalog.pg_proc AS procedure
		 WHERE procedure.oid =
			'identity.admin_has_permission(text,text,text)'::pg_catalog.regprocedure`,
	).Scan(&alteredBody); err != nil {
		t.Fatalf("inspect preserved altered function: %v", err)
	}
	if alteredBody != "SELECT true" {
		t.Fatalf("rejected migration changed forensic function body = %q", alteredBody)
	}

	resetDurableSchemas(t, admin)
	if err := previous.Migrate(ctx); err != nil {
		t.Fatalf("reapply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.rbac_policies
		DROP CONSTRAINT rbac_policy_effect_valid`,
	); err != nil {
		t.Fatalf("drop trusted RBAC constraint: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("missing RBAC constraint migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	var constraintPresent bool
	if err := admin.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_catalog.pg_constraint AS constraint_row
			 WHERE constraint_row.conrelid =
				'identity.rbac_policies'::pg_catalog.regclass
			   AND constraint_row.conname = 'rbac_policy_effect_valid'
		)`,
	).Scan(&constraintPresent); err != nil {
		t.Fatalf("inspect preserved missing constraint: %v", err)
	}
	if constraintPresent {
		t.Fatal("rejected migration recreated divergent RBAC constraint")
	}

	resetDurableSchemas(t, admin)
	if err := previous.Migrate(ctx); err != nil {
		t.Fatalf("reapply previous tip for immutable guard fixture: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE OR REPLACE FUNCTION engine.reject_immutable_change()
		RETURNS trigger
		LANGUAGE plpgsql
		AS $function$
		BEGIN
			IF TG_OP = 'DELETE' THEN
				RETURN OLD;
			ELSIF TG_OP = 'UPDATE' THEN
				RETURN NEW;
			END IF;
			RETURN NULL;
		END
		$function$`,
	); err != nil {
		t.Fatalf("alter immutable-change guard: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "55000") {
		t.Fatalf("altered immutable guard migration error = %v, want 55000", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	var alteredGuardBody string
	if err := admin.QueryRow(ctx, `
		SELECT procedure.prosrc
		  FROM pg_catalog.pg_proc AS procedure
		 WHERE procedure.oid =
			'engine.reject_immutable_change()'::pg_catalog.regprocedure`,
	).Scan(&alteredGuardBody); err != nil {
		t.Fatalf("inspect preserved altered immutable guard: %v", err)
	}
	if !strings.Contains(alteredGuardBody, "RETURN NEW") {
		t.Fatalf(
			"rejected migration changed forensic immutable guard = %q",
			alteredGuardBody,
		)
	}
	if _, err := admin.Exec(ctx, `CREATE OR REPLACE FUNCTION engine.reject_immutable_change()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    RAISE EXCEPTION 'immutable relation %.% cannot be %',
        TG_TABLE_SCHEMA, TG_TABLE_NAME, lower(TG_OP)
        USING ERRCODE = '55000';
END;
$function$`,
	); err != nil {
		t.Fatalf("repair immutable-change guard: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry migration after immutable guard repair: %v", err)
	}
}

func TestAdminBootstrapMigrationRejectsUnsafeProvisionedRole(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	const (
		parentRole = "platformgo_admin_bootstrap_unsafe_parent"
		memberRole = "platformgo_admin_bootstrap_unsafe_member"
	)
	_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+parentRole)
	_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+memberRole)
	if _, err := admin.Exec(ctx, `
		CREATE ROLE platformgo_admin_bootstrap_unsafe_parent NOLOGIN;
		GRANT platformgo_admin_bootstrap_unsafe_parent
		   TO platformgo_admin_bootstrap`,
	); err != nil {
		t.Fatalf("make bootstrap role unsafe: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"DROP TABLE IF EXISTS public.platformgo_admin_bootstrap_owned",
		)
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES "+
				"REVOKE SELECT ON TABLES FROM platformgo_admin_bootstrap",
		)
		_, _ = admin.Exec(
			context.Background(),
			"REVOKE CREATE ON SCHEMA public FROM platformgo_admin_bootstrap",
		)
		_, _ = admin.Exec(
			context.Background(),
			`REVOKE EXECUTE ON FUNCTION identity.create_user_api_key(
				text, uuid, text, bytea, text, text[], uuid, text,
				bytea, bytea, text, bytea, bytea
			) FROM platformgo_admin_bootstrap CASCADE`,
		)
		_, _ = admin.Exec(
			context.Background(),
			"REVOKE "+parentRole+" FROM platformgo_admin_bootstrap",
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+parentRole,
		)
		_, _ = admin.Exec(
			context.Background(),
			"REVOKE platformgo_admin_bootstrap FROM "+memberRole,
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+memberRole,
		)
	})

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	err := migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("unsafe bootstrap role migration error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		REVOKE platformgo_admin_bootstrap_unsafe_parent
		   FROM platformgo_admin_bootstrap;
		DROP ROLE platformgo_admin_bootstrap_unsafe_parent;
		CREATE ROLE platformgo_admin_bootstrap_unsafe_member NOLOGIN;
		GRANT platformgo_admin_bootstrap
		   TO platformgo_admin_bootstrap_unsafe_member`,
	); err != nil {
		t.Fatalf("replace unsafe parent with unsafe member: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("bootstrap role with member migration error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		REVOKE platformgo_admin_bootstrap
		   FROM platformgo_admin_bootstrap_unsafe_member;
		DROP ROLE platformgo_admin_bootstrap_unsafe_member;
		GRANT EXECUTE ON FUNCTION identity.create_user_api_key(
			text, uuid, text, bytea, text, text[], uuid, text,
			bytea, bytea, text, bytea, bytea
		) TO platformgo_admin_bootstrap WITH GRANT OPTION`,
	); err != nil {
		t.Fatalf("install residual bootstrap function privilege: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("bootstrap role with function grant error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		REVOKE EXECUTE ON FUNCTION identity.create_user_api_key(
			text, uuid, text, bytea, text, text[], uuid, text,
			bytea, bytea, text, bytea, bytea
		) FROM platformgo_admin_bootstrap CASCADE;
		GRANT CREATE ON SCHEMA public TO platformgo_admin_bootstrap`,
	); err != nil {
		t.Fatalf("install residual bootstrap schema privilege: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("bootstrap role with schema grant error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		REVOKE CREATE ON SCHEMA public FROM platformgo_admin_bootstrap;
		CREATE TABLE public.platformgo_admin_bootstrap_owned (id bigint);
		ALTER TABLE public.platformgo_admin_bootstrap_owned
			OWNER TO platformgo_admin_bootstrap`,
	); err != nil {
		t.Fatalf("install residual bootstrap ownership: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("bootstrap role with object ownership error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if _, err := admin.Exec(ctx, `
		DROP TABLE public.platformgo_admin_bootstrap_owned;
		ALTER DEFAULT PRIVILEGES
			GRANT SELECT ON TABLES TO platformgo_admin_bootstrap`,
	); err != nil {
		t.Fatalf("install residual bootstrap default privilege: %v", err)
	}
	err = migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(err, "42501") {
		t.Fatalf("bootstrap role with default grant error = %v, want 42501", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
}

func TestAdminBootstrapMigrationLockTimeoutRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	lockingTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bootstrap migration blocker: %v", err)
	}
	if _, err := lockingTx.Exec(ctx, `
		LOCK TABLE identity.rbac_roles IN ACCESS EXCLUSIVE MODE`,
	); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("lock bootstrap RBAC role table: %v", err)
	}

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	migrateCtx, migrateCancel := context.WithTimeout(ctx, 2*time.Second)
	defer migrateCancel()
	err = current.Migrate(migrateCtx)
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("contended bootstrap migration error = %v, want 55P03", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := lockingTx.Exec(lockCtx, `
		LOCK TABLE pg_catalog.pg_class IN ROW EXCLUSIVE MODE;
		LOCK TABLE pg_catalog.pg_attribute IN ROW EXCLUSIVE MODE`,
	); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("relation blocker could not continue with catalog DDL: %v", err)
	}
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatalf("release bootstrap migration blocker: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry bootstrap migration after lock drain: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRevalidatesCatalogAfterWaitingForDDL(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE FUNCTION public.concurrent_admin_assignment()
		RETURNS trigger
		LANGUAGE plpgsql
		SET search_path = pg_catalog
		AS $$
		BEGIN
			IF NEW.admin_subject <>
				'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff'
			THEN
				INSERT INTO identity.rbac_admin_roles (
					admin_subject,
					role_id
				) VALUES (
					'admin::urn:xb:admin:00000000-0000-4000-8000-0000000000ff',
					NEW.role_id
				);
			END IF;
			RETURN NEW;
		END
		$$`); err != nil {
		t.Fatalf("create concurrent bootstrap trigger function: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP TRIGGER IF EXISTS concurrent_admin_assignment
				ON identity.rbac_admin_roles;
			DROP FUNCTION IF EXISTS public.concurrent_admin_assignment()`)
	})

	ddlTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent bootstrap DDL: %v", err)
	}
	if _, err := ddlTx.Exec(ctx, `
		CREATE TRIGGER concurrent_admin_assignment
		BEFORE INSERT ON identity.rbac_admin_roles
		FOR EACH ROW
		EXECUTE FUNCTION public.concurrent_admin_assignment()`,
	); err != nil {
		_ = ddlTx.Rollback(ctx)
		t.Fatalf("create concurrent bootstrap trigger: %v", err)
	}

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	if err := current.Migrate(ctx); !adminBootstrapIsPostgresCode(
		err,
		"55P03",
	) {
		_ = ddlTx.Rollback(ctx)
		t.Fatalf(
			"migration during concurrent DDL error = %v, want 55P03",
			err,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)

	if err := ddlTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent bootstrap catalog change: %v", err)
	}

	migrationErr :=
		migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
	if !adminBootstrapIsPostgresCode(migrationErr, "55000") {
		t.Fatalf(
			"bootstrap migration after concurrent DDL error = %v, want 55000",
			migrationErr,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)

	if _, err := admin.Exec(ctx, `
		DROP TRIGGER concurrent_admin_assignment
			ON identity.rbac_admin_roles;
		DROP FUNCTION public.concurrent_admin_assignment()`); err != nil {
		t.Fatalf("repair concurrent bootstrap catalog change: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry bootstrap migration after catalog repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationRejectsCatalogContentionWithoutDeadlock(
	t *testing.T,
) {
	for _, testCase := range []struct {
		name          string
		heldCatalog   string
		followCatalog string
	}{
		{
			name:          "pg_proc then pg_class",
			heldCatalog:   "pg_proc",
			followCatalog: "pg_class",
		},
		{
			name:          "pg_authid then pg_class",
			heldCatalog:   "pg_authid",
			followCatalog: "pg_class",
		},
		{
			name:          "pg_class then pg_attribute",
			heldCatalog:   "pg_class",
			followCatalog: "pg_attribute",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip: %v", err)
			}

			catalogWriter, err := admin.Begin(ctx)
			if err != nil {
				t.Fatalf("begin catalog writer: %v", err)
			}
			target := "pg_catalog." +
				pgx.Identifier{testCase.heldCatalog}.Sanitize()
			if _, err := catalogWriter.Exec(
				ctx,
				"LOCK TABLE "+target+" IN ROW EXCLUSIVE MODE",
			); err != nil {
				_ = catalogWriter.Rollback(ctx)
				t.Fatalf(
					"lock %s as catalog writer: %v",
					testCase.heldCatalog,
					err,
				)
			}

			current := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminBootstrapMigration),
			)
			migrateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			migrationErr := current.Migrate(migrateCtx)
			if !adminBootstrapIsPostgresCode(migrationErr, "55P03") {
				_ = catalogWriter.Rollback(ctx)
				t.Fatalf(
					"catalog contention error = %v, want 55P03",
					migrationErr,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)

			follow := "pg_catalog." +
				pgx.Identifier{testCase.followCatalog}.Sanitize()
			lockCtx, lockCancel := context.WithTimeout(ctx, 2*time.Second)
			defer lockCancel()
			if _, err := catalogWriter.Exec(
				lockCtx,
				"LOCK TABLE "+follow+" IN ROW EXCLUSIVE MODE",
			); err != nil {
				_ = catalogWriter.Rollback(ctx)
				t.Fatalf(
					"catalog writer could not continue %s -> %s: %v",
					testCase.heldCatalog,
					testCase.followCatalog,
					err,
				)
			}
			if err := catalogWriter.Commit(ctx); err != nil {
				t.Fatalf("commit ordered catalog writer: %v", err)
			}
			if err := migrateAdminBootstrapAfterTransientLockContention(
				t,
				ctx,
				current,
			); err != nil {
				t.Fatalf("retry after catalog writer drain: %v", err)
			}
			assertMigrationHistoryTip(
				t,
				admin,
				42,
				adminBootstrapMigration,
			)
		})
	}
}

func TestAdminBootstrapMigrationFencesFunctionObjectBeforeCatalogs(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE FUNCTION public.admin_bootstrap_lock_guard()
		RETURNS void
		LANGUAGE sql
		AS 'SELECT'`); err != nil {
		t.Fatalf("create function object lock guard: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP FUNCTION IF EXISTS public.admin_bootstrap_lock_guard()`)
	})

	guardTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin function object lock guard: %v", err)
	}
	t.Cleanup(func() {
		_ = guardTx.Rollback(context.Background())
	})
	if _, err := guardTx.Exec(ctx, `
		SELECT pg_catalog.pg_get_object_address(
			'function',
			ARRAY['public', 'admin_bootstrap_lock_guard'],
			ARRAY[]::text[]
		)`); err != nil {
		_ = guardTx.Rollback(ctx)
		t.Fatalf("lock function object guard: %v", err)
	}

	dropTx, err := admin.Begin(ctx)
	if err != nil {
		_ = guardTx.Rollback(ctx)
		t.Fatalf("begin multi-function drop: %v", err)
	}
	t.Cleanup(func() {
		_ = dropTx.Rollback(context.Background())
	})
	var dropPID int32
	if err := dropTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(
		&dropPID,
	); err != nil {
		_ = dropTx.Rollback(ctx)
		_ = guardTx.Rollback(ctx)
		t.Fatalf("read multi-function drop backend PID: %v", err)
	}
	dropResult := make(chan error, 1)
	go func() {
		_, dropErr := dropTx.Exec(ctx, `
			DROP FUNCTION
				identity.admin_has_permission(text,text,text),
				public.admin_bootstrap_lock_guard()`)
		dropResult <- dropErr
	}()
	awaitAdminBootstrapFunctionObjectLockWait(
		t,
		admin,
		dropResult,
		dropPID,
		"identity.admin_has_permission(text,text,text)",
		"public.admin_bootstrap_lock_guard()",
	)

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	migrateCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- current.Migrate(migrateCtx)
	}()
	awaitAdminBootstrapFunctionObjectAccessWait(
		t,
		admin,
		migrationResult,
		"identity.admin_has_permission(text,text,text)",
	)

	catalogProbe, err := admin.Begin(ctx)
	if err != nil {
		_ = guardTx.Rollback(ctx)
		_ = dropTx.Rollback(ctx)
		t.Fatalf("begin catalog progress probe: %v", err)
	}
	if _, err := catalogProbe.Exec(ctx, `
		LOCK TABLE pg_catalog.pg_proc
		IN ROW EXCLUSIVE MODE NOWAIT`); err != nil {
		_ = catalogProbe.Rollback(ctx)
		_ = guardTx.Rollback(ctx)
		_ = dropTx.Rollback(ctx)
		t.Fatalf(
			"catalog writer blocked behind object-waiting migration: %v",
			err,
		)
	}
	if err := catalogProbe.Rollback(ctx); err != nil {
		_ = guardTx.Rollback(ctx)
		_ = dropTx.Rollback(ctx)
		t.Fatalf("release catalog progress probe: %v", err)
	}

	var migrationErr error
	select {
	case migrationErr = <-migrationResult:
	case <-time.After(2 * time.Second):
		_ = guardTx.Rollback(ctx)
		_ = dropTx.Rollback(ctx)
		t.Fatal("function object contention exceeded its pre-fence bound")
	}
	if !adminBootstrapIsPostgresCode(migrationErr, "55P03") {
		_ = guardTx.Rollback(ctx)
		_ = dropTx.Rollback(ctx)
		t.Fatalf(
			"function object contention error = %v, want 55P03",
			migrationErr,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)

	if err := guardTx.Rollback(ctx); err != nil {
		_ = dropTx.Rollback(ctx)
		t.Fatalf("release function object lock guard: %v", err)
	}
	select {
	case err := <-dropResult:
		if err != nil {
			_ = dropTx.Rollback(ctx)
			t.Fatalf("multi-function drop did not continue: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = dropTx.Rollback(ctx)
		t.Fatal("multi-function drop remained blocked after guard release")
	}
	if err := dropTx.Rollback(ctx); err != nil {
		t.Fatalf("repair rolled-back multi-function drop: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry after function object blocker repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationTimesOutBeforeObjectDeadlockDetection(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		CREATE FUNCTION public.admin_bootstrap_object_order_guard()
		RETURNS void
		LANGUAGE sql
		AS 'SELECT'`); err != nil {
		t.Fatalf("create object-order guard: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `
			DROP FUNCTION IF EXISTS
				public.admin_bootstrap_object_order_guard()`)
	})

	guardTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin object-order guard: %v", err)
	}
	t.Cleanup(func() {
		_ = guardTx.Rollback(context.Background())
	})
	if _, err := guardTx.Exec(ctx, `
		SELECT pg_catalog.pg_get_object_address(
			'function',
			ARRAY['public', 'admin_bootstrap_object_order_guard'],
			ARRAY[]::text[]
		)`); err != nil {
		t.Fatalf("lock object-order guard: %v", err)
	}

	dropTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin reverse-order multi-function drop: %v", err)
	}
	t.Cleanup(func() {
		_ = dropTx.Rollback(context.Background())
	})
	var dropPID int32
	if err := dropTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(
		&dropPID,
	); err != nil {
		t.Fatalf("read reverse-order drop backend PID: %v", err)
	}
	dropResult := make(chan error, 1)
	go func() {
		_, dropErr := dropTx.Exec(ctx, `
			DROP FUNCTION
				identity.admin_has_permission(text,text,text),
				public.admin_bootstrap_object_order_guard(),
				engine.reject_immutable_change()
			CASCADE`)
		dropResult <- dropErr
	}()
	awaitAdminBootstrapFunctionObjectLockWait(
		t,
		admin,
		dropResult,
		dropPID,
		"identity.admin_has_permission(text,text,text)",
		"public.admin_bootstrap_object_order_guard()",
	)

	current := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	)
	migrateCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- current.Migrate(migrateCtx)
	}()
	awaitAdminBootstrapFunctionObjectAccessWait(
		t,
		admin,
		migrationResult,
		"identity.admin_has_permission(text,text,text)",
	)
	if err := guardTx.Rollback(ctx); err != nil {
		t.Fatalf("release object-order guard: %v", err)
	}

	var migrationErr error
	select {
	case migrationErr = <-migrationResult:
	case <-time.After(2 * time.Second):
		t.Fatal("migration did not resolve reverse object-lock order")
	}
	if !adminBootstrapIsPostgresCode(migrationErr, "55P03") {
		t.Fatalf(
			"reverse object-lock order migration error = %v, want 55P03",
			migrationErr,
		)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)

	select {
	case err := <-dropResult:
		if err != nil {
			t.Fatalf(
				"reverse-order drop did not drain after migration timeout: %v",
				err,
			)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverse-order drop remained blocked after migration timeout")
	}
	if err := dropTx.Rollback(ctx); err != nil {
		t.Fatalf("repair reverse-order multi-function drop: %v", err)
	}
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		current,
	); err != nil {
		t.Fatalf("retry after reverse object-order repair: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapMigrationFencesConcurrentTrustedFunctionReplacement(
	t *testing.T,
) {
	tests := []struct {
		name          string
		replacement   string
		inspectBody   string
		hostileMarker string
	}{
		{
			name: "permission function",
			replacement: `
				CREATE OR REPLACE FUNCTION identity.admin_has_permission(
					requested_subject text,
					requested_resource text,
					requested_action text
				)
				RETURNS boolean
				LANGUAGE sql
				STABLE
				SECURITY DEFINER
				SET search_path = pg_catalog
				AS 'SELECT true'`,
			inspectBody: `
				SELECT procedure.prosrc
				  FROM pg_catalog.pg_proc AS procedure
				 WHERE procedure.oid =
					'identity.admin_has_permission(text,text,text)'::pg_catalog.regprocedure`,
			hostileMarker: "SELECT true",
		},
		{
			name: "immutable guard",
			replacement: `
				CREATE OR REPLACE FUNCTION engine.reject_immutable_change()
				RETURNS trigger
				LANGUAGE plpgsql
				AS $function$
				BEGIN
					IF TG_OP = 'DELETE' THEN
						RETURN OLD;
					END IF;
					RETURN NEW;
				END
				$function$`,
			inspectBody: `
				SELECT procedure.prosrc
				  FROM pg_catalog.pg_proc AS procedure
				 WHERE procedure.oid =
					'engine.reject_immutable_change()'::pg_catalog.regprocedure`,
			hostileMarker: "RETURN NEW",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip: %v", err)
			}
			hostileTx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatalf("begin concurrent function replacement: %v", err)
			}
			if _, err := hostileTx.Exec(ctx, test.replacement); err != nil {
				_ = hostileTx.Rollback(ctx)
				t.Fatalf("replace trusted function concurrently: %v", err)
			}

			current := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminBootstrapMigration),
			)
			if err := current.Migrate(ctx); !adminBootstrapIsPostgresCode(
				err,
				"55P03",
			) {
				_ = hostileTx.Rollback(ctx)
				t.Fatalf(
					"migration during concurrent function replacement "+
						"error = %v, want 55P03",
					err,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			if err := hostileTx.Commit(ctx); err != nil {
				t.Fatalf("commit concurrent function replacement: %v", err)
			}

			migrationErr :=
				migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
			if !adminBootstrapIsPostgresCode(migrationErr, "55000") {
				t.Fatalf(
					"migration after concurrent function replacement "+
						"error = %v, want 55000",
					migrationErr,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			var body string
			if err := admin.QueryRow(ctx, test.inspectBody).Scan(&body); err != nil {
				t.Fatalf("inspect preserved hostile function: %v", err)
			}
			if !strings.Contains(body, test.hostileMarker) {
				t.Fatalf("hostile function body was not preserved: %q", body)
			}
		})
	}
}

func TestAdminBootstrapMigrationFencesConcurrentRoleAuthorityChange(
	t *testing.T,
) {
	for _, roleName := range []string{
		"platformgo_admin_bootstrap",
		"platformgo_api",
	} {
		t.Run(roleName+"_attribute", func(t *testing.T) {
			ctx := context.Background()
			admin := postgresPool(t)
			requireBrokerFundingPostgres19Beta2(t, admin)
			resetDurableSchemas(t, admin)
			if err := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminPermissionMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("apply previous migration tip: %v", err)
			}
			t.Cleanup(func() {
				_, _ = admin.Exec(
					context.Background(),
					"ALTER ROLE "+roleName+" NOCREATEROLE NOLOGIN",
				)
			})

			hostileTx, err := admin.Begin(ctx)
			if err != nil {
				t.Fatalf("begin concurrent role attribute change: %v", err)
			}
			if _, err := hostileTx.Exec(
				ctx,
				"ALTER ROLE "+roleName+" CREATEROLE",
			); err != nil {
				_ = hostileTx.Rollback(ctx)
				t.Fatalf("stage concurrent role attribute change: %v", err)
			}
			current := platformpostgres.NewMigrator(
				admin,
				migrationFilesThrough(t, adminBootstrapMigration),
			)
			if err := current.Migrate(ctx); !adminBootstrapIsPostgresCode(
				err,
				"55P03",
			) {
				_ = hostileTx.Rollback(ctx)
				t.Fatalf(
					"migration during concurrent role attribute change "+
						"error = %v, want 55P03",
					err,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			if err := hostileTx.Commit(ctx); err != nil {
				t.Fatalf("commit concurrent role attribute change: %v", err)
			}
			migrationErr :=
				migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
			if !adminBootstrapIsPostgresCode(migrationErr, "42501") {
				t.Fatalf(
					"migration after concurrent role attribute change "+
						"error = %v, want 42501",
					migrationErr,
				)
			}
			assertAdminBootstrapMigrationAbsent(t, admin)
			var canCreateRole bool
			if err := admin.QueryRow(ctx, `
			SELECT rolcreaterole
			  FROM pg_catalog.pg_roles
			 WHERE rolname = $1`,
				roleName,
			).Scan(&canCreateRole); err != nil {
				t.Fatalf("inspect preserved hostile role attribute: %v", err)
			}
			if !canCreateRole {
				t.Fatal("concurrent hostile role attribute was not preserved")
			}
		})
	}

	t.Run("membership", func(t *testing.T) {
		ctx := context.Background()
		admin := postgresPool(t)
		requireBrokerFundingPostgres19Beta2(t, admin)
		resetDurableSchemas(t, admin)
		if err := platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, adminPermissionMigration),
		).Migrate(ctx); err != nil {
			t.Fatalf("apply previous migration tip: %v", err)
		}
		const parentRole = "platformgo_admin_bootstrap_concurrent_parent"
		_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+parentRole)
		if _, err := admin.Exec(
			ctx,
			"CREATE ROLE "+parentRole+" NOLOGIN",
		); err != nil {
			t.Fatalf("create concurrent parent role: %v", err)
		}
		t.Cleanup(func() {
			_, _ = admin.Exec(
				context.Background(),
				"REVOKE "+parentRole+" FROM platformgo_admin_bootstrap",
			)
			_, _ = admin.Exec(
				context.Background(),
				"DROP ROLE IF EXISTS "+parentRole,
			)
		})

		hostileTx, err := admin.Begin(ctx)
		if err != nil {
			t.Fatalf("begin concurrent role membership change: %v", err)
		}
		if _, err := hostileTx.Exec(ctx, `
			GRANT platformgo_admin_bootstrap_concurrent_parent
			   TO platformgo_admin_bootstrap;
			`,
		); err != nil {
			_ = hostileTx.Rollback(ctx)
			t.Fatalf("stage concurrent role membership change: %v", err)
		}
		current := platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, adminBootstrapMigration),
		)
		if err := current.Migrate(ctx); !adminBootstrapIsPostgresCode(
			err,
			"55P03",
		) {
			_ = hostileTx.Rollback(ctx)
			t.Fatalf(
				"migration during concurrent role membership change "+
					"error = %v, want 55P03",
				err,
			)
		}
		assertAdminBootstrapMigrationAbsent(t, admin)
		if err := hostileTx.Commit(ctx); err != nil {
			t.Fatalf("commit concurrent role membership change: %v", err)
		}
		migrationErr :=
			migrateAdminBootstrapAfterTransientLockContention(t, ctx, current)
		if !adminBootstrapIsPostgresCode(migrationErr, "42501") {
			t.Fatalf(
				"migration after concurrent role membership change "+
					"error = %v, want 42501",
				migrationErr,
			)
		}
		assertAdminBootstrapMigrationAbsent(t, admin)
		var membershipPresent bool
		if err := admin.QueryRow(ctx, `
			SELECT pg_catalog.pg_has_role(
				'platformgo_admin_bootstrap',
				'platformgo_admin_bootstrap_concurrent_parent',
				'member'
			)`,
		).Scan(&membershipPresent); err != nil {
			t.Fatalf("inspect preserved hostile role membership: %v", err)
		}
		if !membershipPresent {
			t.Fatal("concurrent hostile role membership was not preserved")
		}
	})
}

func TestAdminBootstrapFencesConcurrentAuthorityACLUpgrade(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		),
	); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_acl_race_login",
		"platformgo_admin_bootstrap",
	)
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			`REVOKE CREATE ON SCHEMA identity
			   FROM platformgo_admin_bootstrap`,
		)
	})

	hostileTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent bootstrap ACL upgrade: %v", err)
	}
	if _, err := hostileTx.Exec(ctx, `
		GRANT CREATE ON SCHEMA identity
			TO platformgo_admin_bootstrap`); err != nil {
		_ = hostileTx.Rollback(ctx)
		t.Fatalf("stage concurrent bootstrap ACL upgrade: %v", err)
	}

	keyHash := sha256.Sum256([]byte("bootstrap-acl-race-key"))
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"55P03",
		"bootstrap-request-acl-race",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-00000000008a",
		"00000000-0000-4000-8000-00000000008a",
		"2026-07-31T00:00:00.000000Z",
	)
	assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)
	if err := hostileTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent bootstrap ACL upgrade: %v", err)
	}
	assertAdminBootstrapSQLState(
		t,
		terminal,
		"55000",
		"bootstrap-request-acl-race",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-00000000008a",
		"00000000-0000-4000-8000-00000000008a",
		"2026-07-31T00:00:00.000000Z",
	)
	assertAdminBootstrapAuthorityCounts(t, admin, 0, 0)
}

func TestAdminBootstrapTransactionCannotBlockMigratorIndefinitely(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		),
	); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	terminal := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_migrator_lock_test_login",
		"platformgo_admin_bootstrap",
	)
	bootstrapTx, err := terminal.Begin(ctx)
	if err != nil {
		t.Fatalf("begin open bootstrap transaction: %v", err)
	}
	keyHash := sha256.Sum256([]byte("stable-bootstrap-migrator-lock-key"))
	var outcome string
	if err := bootstrapTx.QueryRow(ctx, `
		SELECT outcome
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5, $6)`,
		"bootstrap-request-migrator-lock",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000049",
		"00000000-0000-4000-8000-000000000049",
		"2026-07-31T00:00:00.000000Z",
		runtimeAuthorityACLMigrationChecksum(t),
	).Scan(&outcome); err != nil {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("bootstrap inside open transaction: %v", err)
	}
	if outcome != "created" {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("open transaction bootstrap outcome = %q", outcome)
	}

	migrateCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, runtimeAuthorityACLMigration),
	).Migrate(migrateCtx)
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("blocked migrator error = %v, want SQLSTATE 55P03", err)
	}
	assertMigrationHistoryTip(t, admin, 43, runtimeAuthorityACLMigration)
	if err := bootstrapTx.Rollback(ctx); err != nil {
		t.Fatalf("release open bootstrap transaction: %v", err)
	}
	if err := migrateAdminBootstrapCurrent(t, ctx, admin); err != nil {
		t.Fatalf("retry migrator after bootstrap rollback: %v", err)
	}
}

func TestAdminBootstrapMigrationScrubsHostileGrantChains(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply previous migration tip: %v", err)
	}
	const hostile = "platformgo_admin_bootstrap_hostile"
	const dependent = "platformgo_admin_bootstrap_dependent"
	hostileID := pgx.Identifier{hostile}.Sanitize()
	dependentID := pgx.Identifier{dependent}.Sanitize()
	for _, roleID := range []string{dependentID, hostileID} {
		_, _ = admin.Exec(ctx, "DROP OWNED BY "+roleID)
		_, _ = admin.Exec(ctx, "DROP ROLE IF EXISTS "+roleID)
	}
	if _, err := admin.Exec(ctx, fmt.Sprintf(`
		CREATE ROLE %[1]s NOLOGIN;
		CREATE ROLE %[2]s NOLOGIN;
		ALTER DEFAULT PRIVILEGES IN SCHEMA audit
			GRANT ALL PRIVILEGES ON TABLES TO %[1]s WITH GRANT OPTION;
		ALTER DEFAULT PRIVILEGES IN SCHEMA identity
			GRANT EXECUTE ON FUNCTIONS TO %[1]s WITH GRANT OPTION;
		GRANT USAGE ON SCHEMA identity TO %[1]s;
		GRANT SELECT ON identity.rbac_roles
			TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (name) ON identity.rbac_roles TO PUBLIC`,
		hostileID,
		dependentID,
	)); err != nil {
		t.Fatalf("install hostile bootstrap ACLs: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES IN SCHEMA audit "+
				"REVOKE ALL PRIVILEGES ON TABLES FROM "+hostileID,
		)
		_, _ = admin.Exec(
			context.Background(),
			"ALTER DEFAULT PRIVILEGES IN SCHEMA identity "+
				"REVOKE EXECUTE ON FUNCTIONS FROM "+hostileID,
		)
		for _, roleID := range []string{dependentID, hostileID} {
			_, _ = admin.Exec(context.Background(), "DROP OWNED BY "+roleID)
			_, _ = admin.Exec(
				context.Background(),
				"DROP ROLE IF EXISTS "+roleID,
			)
		}
	})
	grantTx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin hostile bootstrap delegation: %v", err)
	}
	if _, err := grantTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("assume hostile bootstrap role: %v", err)
	}
	if _, err := grantTx.Exec(ctx, `
		GRANT SELECT ON identity.rbac_roles
		   TO `+dependentID); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("delegate hostile bootstrap grant: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit hostile bootstrap delegation: %v", err)
	}

	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, adminBootstrapMigration),
		),
	); err != nil {
		t.Fatalf("apply bootstrap ACL scrub: %v", err)
	}
	for _, relation := range []string{
		"identity.rbac_roles",
		"identity.rbac_role_parents",
		"identity.rbac_admin_roles",
		"identity.rbac_policies",
		"audit.admin_bootstrap_events",
	} {
		var tableACLs, columnACLs int
		if err := admin.QueryRow(ctx, `
			SELECT
				(SELECT count(*)
				   FROM pg_catalog.pg_class AS relation
				   CROSS JOIN LATERAL pg_catalog.aclexplode(
					COALESCE(
						relation.relacl,
						pg_catalog.acldefault('r', relation.relowner)
					)
				   ) AS privilege
				  WHERE relation.oid = $1::pg_catalog.regclass
				    AND privilege.grantee <> relation.relowner),
				(SELECT count(*)
				   FROM pg_catalog.pg_attribute AS attribute
				   JOIN pg_catalog.pg_class AS relation
				     ON relation.oid = attribute.attrelid
				   CROSS JOIN LATERAL pg_catalog.aclexplode(
					attribute.attacl
				   ) AS privilege
				  WHERE attribute.attrelid = $1::pg_catalog.regclass
				    AND attribute.attnum > 0
				    AND NOT attribute.attisdropped
				    AND attribute.attacl IS NOT NULL
				    AND privilege.grantee <> relation.relowner)`,
			relation,
		).Scan(&tableACLs, &columnACLs); err != nil {
			t.Fatalf("inspect scrubbed %s ACL: %v", relation, err)
		}
		if tableACLs != 0 || columnACLs != 0 {
			t.Fatalf(
				"%s unexpected table/column ACL rows = %d/%d",
				relation,
				tableACLs,
				columnACLs,
			)
		}
	}
	assertAdminBootstrapFunctionACL(
		t,
		admin,
		"identity.admin_has_permission(text,text,text)",
		"platformgo_api",
	)
	assertAdminBootstrapFunctionACL(
		t,
		admin,
		"identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)",
		"platformgo_admin_bootstrap",
	)
}

func TestAdminBootstrapConcurrentDistinctRequestsCreateOneAdmin(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		),
	); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}

	first := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_race_first",
		"platformgo_admin_bootstrap",
	)
	second := runtimeRoleLoginPool(
		t,
		admin,
		"platformgo_admin_bootstrap_race_second",
		"platformgo_admin_bootstrap",
	)
	start := make(chan struct{})
	type result struct {
		outcome  string
		attempts int
		err      error
	}
	results := make(chan result, 2)
	migrationChecksum := runtimeAuthorityACLMigrationChecksum(t)
	var ready sync.WaitGroup
	ready.Add(2)
	invoke := func(
		pool *pgxpool.Pool,
		requestID string,
		keyMaterial string,
		subject string,
		eventID string,
	) {
		defer ready.Done()
		<-start
		keyHash := sha256.Sum256([]byte(keyMaterial))
		got, attempts, err :=
			queryAdminBootstrapAfterTransientLockContention(
				ctx,
				pool,
				requestID,
				keyHash[:],
				subject,
				eventID,
				"2026-07-31T00:00:00.000000Z",
				migrationChecksum,
			)
		results <- result{
			outcome:  got.outcome,
			attempts: attempts,
			err:      err,
		}
	}

	go invoke(
		first,
		"race-request-first",
		"race-key-first",
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000051",
		"00000000-0000-4000-8000-000000000051",
	)
	go invoke(
		second,
		"race-request-second",
		"race-key-second",
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000052",
		"00000000-0000-4000-8000-000000000052",
	)
	close(start)
	ready.Wait()
	close(results)

	created := 0
	rejected := 0
	for got := range results {
		if got.attempts > 1 {
			t.Logf(
				"concurrent runtime bootstrap lock-contention retry "+
					"reached its result on attempt %d",
				got.attempts,
			)
		}
		switch {
		case got.err == nil && got.outcome == "created":
			created++
		case adminBootstrapIsPostgresCode(got.err, "55000"):
			rejected++
		default:
			t.Fatalf("unexpected concurrent result = %#v", got)
		}
	}
	if created != 1 || rejected != 1 {
		t.Fatalf("concurrent outcomes = created:%d rejected:%d", created, rejected)
	}
	var assignments, events int
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.rbac_admin_roles),
			(SELECT count(*) FROM audit.admin_bootstrap_events)`,
	).Scan(&assignments, &events); err != nil {
		t.Fatal(err)
	}
	if assignments != 1 || events != 1 {
		t.Fatalf("concurrent durable rows = assignments:%d events:%d", assignments, events)
	}
}

func TestAdminBootstrapMigrationMakesPreviousArtifactSchemaAhead(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	migrateCurrentTipAsDemotedExactOwner(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous artifact VerifyCurrent error = %v", err)
	}
	assertMigrationHistoryTip(t, admin, 44, predictionMarketCatalogMigration)
}

func callAdminBootstrap(
	t *testing.T,
	pool *pgxpool.Pool,
	requestID string,
	keyHash []byte,
	subject string,
	eventID string,
	logicalTime string,
) adminBootstrapResult {
	t.Helper()
	got, attempts, err := queryAdminBootstrapAfterTransientLockContention(
		context.Background(),
		pool,
		requestID,
		keyHash,
		subject,
		eventID,
		logicalTime,
		runtimeAuthorityACLMigrationChecksum(t),
	)
	if attempts > 1 {
		t.Logf(
			"explicit runtime bootstrap lock-contention retry succeeded "+
				"on attempt %d",
			attempts,
		)
	}
	if err != nil {
		t.Fatalf("bootstrap first admin: %v", err)
	}
	return got
}

func queryAdminBootstrapOnce(
	ctx context.Context,
	pool *pgxpool.Pool,
	requestID string,
	keyHash []byte,
	subject string,
	eventID string,
	logicalTime string,
	migrationChecksum []byte,
) (adminBootstrapResult, error) {
	var got adminBootstrapResult
	err := pool.QueryRow(ctx, `
		SELECT outcome, admin_subject, role_name,
		       configuration_version, event_id::text, logical_time_text
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5, $6)`,
		requestID,
		keyHash,
		subject,
		eventID,
		logicalTime,
		migrationChecksum,
	).Scan(
		&got.outcome,
		&got.adminSubject,
		&got.roleName,
		&got.configurationVersion,
		&got.eventID,
		&got.logicalTimeText,
	)
	return got, err
}

func queryAdminBootstrapAfterTransientLockContention(
	ctx context.Context,
	pool *pgxpool.Pool,
	requestID string,
	keyHash []byte,
	subject string,
	eventID string,
	logicalTime string,
	migrationChecksum []byte,
) (adminBootstrapResult, int, error) {
	const (
		attempts = 250
		delay    = 20 * time.Millisecond
	)
	var last adminBootstrapResult
	for attempt := 1; attempt <= attempts; attempt++ {
		var err error
		last, err = queryAdminBootstrapOnce(
			ctx,
			pool,
			requestID,
			keyHash,
			subject,
			eventID,
			logicalTime,
			migrationChecksum,
		)
		if !adminBootstrapIsPostgresCode(err, "55P03") {
			return last, attempt, err
		}
		if attempt == attempts {
			return last, attempt, fmt.Errorf(
				"explicit runtime bootstrap lock contention remained "+
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
			return last, attempt, fmt.Errorf(
				"explicit runtime bootstrap lock-contention retry "+
					"stopped after %d attempts: %w",
				attempt,
				ctx.Err(),
			)
		case <-timer.C:
		}
	}
	return last, attempts, errors.New(
		"runtime bootstrap retry exhausted without a result",
	)
}

func adminBootstrapMigrationChecksum(t *testing.T) []byte {
	t.Helper()
	checksum, err := hex.DecodeString(adminBootstrapMigrationChecksumHex)
	if err != nil {
		t.Fatalf("decode admin bootstrap migration checksum: %v", err)
	}
	return checksum
}

func assertAdminBootstrapAuthorityCounts(
	t *testing.T,
	admin *pgxpool.Pool,
	wantAssignments int,
	wantReceipts int,
) {
	t.Helper()
	var assignments, receipts int
	if err := admin.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM identity.rbac_admin_roles),
			(SELECT count(*) FROM audit.admin_bootstrap_events)`,
	).Scan(&assignments, &receipts); err != nil {
		t.Fatalf("inspect admin bootstrap authority counts: %v", err)
	}
	if assignments != wantAssignments || receipts != wantReceipts {
		t.Fatalf(
			"admin bootstrap authority counts = assignments %d receipts %d, "+
				"want %d/%d",
			assignments,
			receipts,
			wantAssignments,
			wantReceipts,
		)
	}
}

func assertAdminBootstrapSQLState(
	t *testing.T,
	pool *pgxpool.Pool,
	code string,
	requestID string,
	keyHash []byte,
	subject string,
	eventID string,
	logicalTime string,
) {
	t.Helper()
	assertAdminBootstrapSQLStateContext(
		t,
		context.Background(),
		pool,
		code,
		requestID,
		keyHash,
		subject,
		eventID,
		logicalTime,
	)
}

func assertAdminBootstrapSQLStateContext(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	code string,
	requestID string,
	keyHash []byte,
	subject string,
	eventID string,
	logicalTime string,
) {
	t.Helper()
	checksum := runtimeAuthorityACLMigrationChecksum(t)
	var (
		attempts int
		err      error
	)
	if code == "55P03" {
		_, err = queryAdminBootstrapOnce(
			ctx,
			pool,
			requestID,
			keyHash,
			subject,
			eventID,
			logicalTime,
			checksum,
		)
		attempts = 1
	} else {
		_, attempts, err = queryAdminBootstrapAfterTransientLockContention(
			ctx,
			pool,
			requestID,
			keyHash,
			subject,
			eventID,
			logicalTime,
			checksum,
		)
	}
	if attempts > 1 {
		t.Logf(
			"explicit runtime bootstrap lock-contention retry reached "+
				"SQLSTATE %s on attempt %d",
			code,
			attempts,
		)
	}
	if !adminBootstrapIsPostgresCode(err, code) {
		t.Fatalf("bootstrap SQLSTATE = %v, want %s", err, code)
	}
}

func readAdminBootstrapPreservedState(
	t *testing.T,
	admin *pgxpool.Pool,
) (string, string) {
	t.Helper()
	var digest, files string
	if err := admin.QueryRow(context.Background(), `
		SELECT
			pg_catalog.md5(pg_catalog.concat_ws(
				'|',
				(SELECT pg_catalog.concat_ws(
					':', user_id, login, normalized_login, email, normalized_email
				) FROM identity.users
				  WHERE user_id = 'urn:xb:user:admin-bootstrap-preserved'),
				(SELECT pg_catalog.concat_ws(
					':', api_key_id, owner_user_id, name, encode(key_hash, 'hex'),
					prefix, array_to_string(scopes, ','), created_at
				) FROM identity.api_keys
				  WHERE api_key_id =
					'00000000-0000-4000-8000-000000000045'),
				(SELECT pg_catalog.concat_ws(
					':', event_id, occurred_at, request_id, actor_kind, actor_id,
					action, target_kind, target_id, outcome, detail::text
				) FROM audit.events
				  WHERE event_id =
					'00000000-0000-4000-8000-000000000045')
			)),
			pg_catalog.concat_ws(
				'|',
				pg_catalog.pg_relation_filenode(
					'identity.users'::pg_catalog.regclass
				),
				pg_catalog.pg_relation_filenode(
					'identity.api_keys'::pg_catalog.regclass
				),
				pg_catalog.pg_relation_filenode(
					'audit.events'::pg_catalog.regclass
				)
			)`,
	).Scan(&digest, &files); err != nil {
		t.Fatalf("read preserved bootstrap state: %v", err)
	}
	return digest, files
}

func assertAdminBootstrapMigrationAbsent(
	t *testing.T,
	admin *pgxpool.Pool,
) {
	t.Helper()
	var journaled, auditTablePresent, functionPresent bool
	if err := admin.QueryRow(context.Background(), `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.schema_migrations WHERE filename = $1
			),
			pg_catalog.to_regclass(
				'audit.admin_bootstrap_events'
			) IS NOT NULL,
			pg_catalog.to_regprocedure(
				'identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)'
			) IS NOT NULL`,
		adminBootstrapMigration,
	).Scan(&journaled, &auditTablePresent, &functionPresent); err != nil {
		t.Fatalf("inspect rejected bootstrap migration: %v", err)
	}
	if journaled || auditTablePresent || functionPresent {
		t.Fatalf(
			"rejected migration journal/table/function = %t/%t/%t",
			journaled,
			auditTablePresent,
			functionPresent,
		)
	}
}

func assertAdminBootstrapFunctionACL(
	t *testing.T,
	admin *pgxpool.Pool,
	function string,
	allowedRole string,
) {
	t.Helper()
	var exact bool
	if err := admin.QueryRow(context.Background(), `
		SELECT
			count(*) = 1
			AND bool_and(role.rolname = $2)
			AND bool_and(privilege.privilege_type = 'EXECUTE')
			AND bool_and(NOT privilege.is_grantable)
		  FROM pg_catalog.pg_proc AS procedure
		  CROSS JOIN LATERAL pg_catalog.aclexplode(
			COALESCE(
				procedure.proacl,
				pg_catalog.acldefault('f', procedure.proowner)
			)
		  ) AS privilege
		  JOIN pg_catalog.pg_roles AS role
		    ON role.oid = privilege.grantee
		 WHERE procedure.oid = $1::pg_catalog.regprocedure
		   AND privilege.grantee <> procedure.proowner`,
		function,
		allowedRole,
	).Scan(&exact); err != nil {
		t.Fatalf("inspect %s ACL: %v", function, err)
	}
	if !exact {
		t.Fatalf("%s ACL is not execute-only for %s", function, allowedRole)
	}
}

func existingAdminBootstrapLoginPool(
	t *testing.T,
	login string,
) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(
		os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
	)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.User = login
	config.ConnConfig.Password = "platformgo-test-password"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("restart admin bootstrap login: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func adminBootstrapSuperuserLoginPool(
	t *testing.T,
	admin *pgxpool.Pool,
	login string,
) *pgxpool.Pool {
	t.Helper()
	loginID := pgx.Identifier{login}.Sanitize()
	if _, err := admin.Exec(context.Background(), fmt.Sprintf(`
		CREATE ROLE %s LOGIN SUPERUSER PASSWORD 'platformgo-test-password'`,
		loginID,
	)); err != nil {
		t.Fatalf("create admin bootstrap migration owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(
			context.Background(),
			"DROP OWNED BY "+loginID+" CASCADE",
		)
		_, _ = admin.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+loginID,
		)
	})
	return existingAdminBootstrapLoginPool(t, login)
}

func awaitAdminBootstrapRelationLockWait(
	t *testing.T,
	admin *pgxpool.Pool,
	result <-chan error,
	relation string,
	mode string,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := admin.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks
				 WHERE relation = pg_catalog.to_regclass($1)
				   AND mode = $2
				   AND NOT granted
			)`,
			relation,
			mode,
		).Scan(&waiting); err != nil {
			t.Fatalf("inspect waiting bootstrap relation fence: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("bootstrap migration did not wait at a relation fence")
		}
		select {
		case resultErr := <-result:
			t.Fatalf(
				"bootstrap operation completed before concurrent relation "+
					"change committed: %v",
				resultErr,
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func awaitAdminBootstrapFunctionObjectAccessWait(
	t *testing.T,
	admin *pgxpool.Pool,
	result <-chan error,
	function string,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := admin.QueryRow(context.Background(), `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks
				 WHERE locktype = 'object'
				   AND classid = 'pg_catalog.pg_proc'::pg_catalog.regclass
				   AND objid = $1::pg_catalog.regprocedure
				   AND mode = 'AccessShareLock'
				   AND NOT granted
			)`,
			function,
		).Scan(&waiting); err != nil {
			t.Fatalf("inspect migration function object lock wait: %v", err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not reach the pre-fence object lock wait")
		}
		select {
		case resultErr := <-result:
			t.Fatalf(
				"migration completed before function object wait: %v",
				resultErr,
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func awaitAdminBootstrapFunctionObjectLockWait(
	t *testing.T,
	admin *pgxpool.Pool,
	result <-chan error,
	pid int32,
	heldFunction string,
	waitedFunction string,
) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var ready bool
		if err := admin.QueryRow(context.Background(), `
			SELECT
				EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_locks
					 WHERE pid = $1
					   AND locktype = 'object'
					   AND classid = 'pg_catalog.pg_proc'::pg_catalog.regclass
					   AND objid = $2::pg_catalog.regprocedure
					   AND mode = 'AccessExclusiveLock'
					   AND granted
				)
				AND EXISTS (
					SELECT 1
					  FROM pg_catalog.pg_locks
					 WHERE pid = $1
					   AND locktype = 'object'
					   AND classid = 'pg_catalog.pg_proc'::pg_catalog.regclass
					   AND objid = $3::pg_catalog.regprocedure
					   AND mode = 'AccessExclusiveLock'
					   AND NOT granted
				)`,
			pid,
			heldFunction,
			waitedFunction,
		).Scan(&ready); err != nil {
			t.Fatalf("inspect multi-function object lock wait: %v", err)
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("multi-function drop did not reach object lock wait")
		}
		select {
		case resultErr := <-result:
			t.Fatalf(
				"multi-function drop completed before guard release: %v",
				resultErr,
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func migrateAdminBootstrapCurrent(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
) error {
	t.Helper()
	// Historical admin-bootstrap tests intentionally stop at the immutable
	// runtime-authority tip (migration 43). Current-tip migration 44 fixtures
	// use migrateCurrentTipAsDemotedExactOwner explicitly.
	return migrateAdminBootstrapAfterTransientLockContention(
		t,
		ctx,
		platformpostgres.NewMigrator(
			admin,
			migrationFilesThrough(t, runtimeAuthorityACLMigration),
		),
	)
}

func migrateAdminBootstrapAfterTransientLockContention(
	t *testing.T,
	ctx context.Context,
	migrator *platformpostgres.Migrator,
) error {
	t.Helper()
	return (&currentTestMigrator{t: t, migrator: migrator}).Migrate(ctx)
}

func adminBootstrapIsPostgresCode(err error, code string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == code
}

func adminBootstrapPreimage(
	actor string,
	requestID string,
	subject string,
	eventID string,
	logicalTime string,
) string {
	return fmt.Sprintf(
		"platformgo.admin-bootstrap.request.v1\n%s\n%s\n%s\n%s\n%s\n"+
			"%s\n%s\n1\n*\n*\nallow\n",
		actor,
		requestID,
		subject,
		eventID,
		logicalTime,
		adminBootstrapRoleID,
		adminBootstrapRoleName,
	)
}
