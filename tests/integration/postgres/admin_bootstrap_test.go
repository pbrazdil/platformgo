package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

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
	if replayed != createdWithOutcome(created, "replayed") {
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

func createdWithOutcome(
	result adminBootstrapResult,
	outcome string,
) adminBootstrapResult {
	result.outcome = outcome
	return result
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
