package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	adminBootstrapMigration = "20260731000100_phase3_admin_bootstrap_authority.up.sql"
	adminBootstrapRoleID    = "00000000-0000-4000-8000-000000000001"
	adminBootstrapRoleName  = "platformgo-superadmin"
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
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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
						'identity.bootstrap_first_admin(text,bytea,text,uuid,text)'::pg_catalog.regprocedure
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

func TestAdminBootstrapReplayFailsClosedWhenCommittedAuthorityDiverges(
	t *testing.T,
) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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

func TestAdminBootstrapUnknownCommitReplaysAfterTerminalRestart(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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

	if _, err := terminal.Exec(ctx, `
		SELECT outcome, admin_subject, role_name, configuration_version,
		       event_id, logical_time_text
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
	); err != nil {
		t.Fatalf("commit bootstrap before simulated lost acknowledgment: %v", err)
	}
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
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
		requestID,
		keyHash[:],
		subject,
		eventID,
		logicalTime,
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
	var (
		assignments       int
		receipts          int
		permitted         bool
		migrationChecksum string
	)
	if err := fresh.QueryRow(ctx, `
		SELECT
			(
				SELECT count(*)
				  FROM identity.rbac_admin_roles
				 WHERE admin_subject = $1
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
		adminBootstrapMigration,
	).Scan(
		&assignments,
		&receipts,
		&permitted,
		&migrationChecksum,
	); err != nil {
		t.Fatalf("fresh-session durable bootstrap verification: %v", err)
	}
	const expectedMigrationChecksum = "cafa605e33b21577b96b982dd3fc4eca10672177943dd9a646d909545f6230ea"
	if assignments != 1 ||
		receipts != 1 ||
		!permitted ||
		migrationChecksum != expectedMigrationChecksum {
		t.Fatalf(
			"fresh-session authority = assignments %d receipts %d "+
				"permitted %t checksum %q",
			assignments,
			receipts,
			permitted,
			migrationChecksum,
		)
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

func TestAdminBootstrapAuditReceiptRejectsOwnerTruncate(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx); err != nil {
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
	err := current.Migrate(ctx)
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
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry bootstrap migration after graph repair: %v", err)
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
	err := current.Migrate(ctx)
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
	err = current.Migrate(ctx)
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
	err = current.Migrate(ctx)
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
	if err := current.Migrate(ctx); err != nil {
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

	err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx)
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
	err = current.Migrate(ctx)
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("contended bootstrap migration error = %v, want 55P03", err)
	}
	assertAdminBootstrapMigrationAbsent(t, admin)
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatalf("release bootstrap migration blocker: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry bootstrap migration after lock drain: %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
}

func TestAdminBootstrapTransactionCannotBlockMigratorIndefinitely(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
		"bootstrap-request-migrator-lock",
		keyHash[:],
		"admin::urn:xb:admin:00000000-0000-4000-8000-000000000049",
		"00000000-0000-4000-8000-000000000049",
		"2026-07-31T00:00:00.000000Z",
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
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(migrateCtx)
	if !adminBootstrapIsPostgresCode(err, "55P03") {
		_ = bootstrapTx.Rollback(ctx)
		t.Fatalf("blocked migrator error = %v, want SQLSTATE 55P03", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
	if err := bootstrapTx.Rollback(ctx); err != nil {
		t.Fatalf("release open bootstrap transaction: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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

	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminBootstrapMigration),
	).Migrate(ctx); err != nil {
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
		"identity.bootstrap_first_admin(text,bytea,text,uuid,text)",
		"platformgo_admin_bootstrap",
	)
}

func TestAdminBootstrapConcurrentDistinctRequestsCreateOneAdmin(t *testing.T) {
	ctx := context.Background()
	admin := postgresPool(t)
	requireBrokerFundingPostgres19Beta2(t, admin)
	resetDurableSchemas(t, admin)
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
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
		outcome string
		err     error
	}
	results := make(chan result, 2)
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
		var got adminBootstrapResult
		err := pool.QueryRow(ctx, `
			SELECT outcome, admin_subject, role_name,
			       configuration_version, event_id::text, logical_time_text
			  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
			requestID,
			keyHash[:],
			subject,
			eventID,
			"2026-07-31T00:00:00.000000Z",
		).Scan(
			&got.outcome,
			&got.adminSubject,
			&got.roleName,
			&got.configurationVersion,
			&got.eventID,
			&got.logicalTimeText,
		)
		results <- result{outcome: got.outcome, err: err}
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
	if err := platformpostgres.NewMigrator(
		admin,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate admin bootstrap authority: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrationFilesThrough(t, adminPermissionMigration),
	).VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous artifact VerifyCurrent error = %v", err)
	}
	assertMigrationHistoryTip(t, admin, 42, adminBootstrapMigration)
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
	var got adminBootstrapResult
	if err := pool.QueryRow(context.Background(), `
		SELECT outcome, admin_subject, role_name,
		       configuration_version, event_id::text, logical_time_text
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
		requestID,
		keyHash,
		subject,
		eventID,
		logicalTime,
	).Scan(
		&got.outcome,
		&got.adminSubject,
		&got.roleName,
		&got.configurationVersion,
		&got.eventID,
		&got.logicalTimeText,
	); err != nil {
		t.Fatalf("bootstrap first admin: %v", err)
	}
	return got
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
	var ignored adminBootstrapResult
	err := pool.QueryRow(context.Background(), `
		SELECT outcome, admin_subject, role_name,
		       configuration_version, event_id::text, logical_time_text
		  FROM identity.bootstrap_first_admin($1, $2, $3, $4, $5)`,
		requestID,
		keyHash,
		subject,
		eventID,
		logicalTime,
	).Scan(
		&ignored.outcome,
		&ignored.adminSubject,
		&ignored.roleName,
		&ignored.configurationVersion,
		&ignored.eventID,
		&ignored.logicalTimeText,
	)
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
				'identity.bootstrap_first_admin(text,bytea,text,uuid,text)'
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
