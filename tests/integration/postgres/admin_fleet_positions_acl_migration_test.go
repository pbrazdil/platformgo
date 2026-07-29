package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	adminFleetPositionsACLPreviousMigration = "20260728000400_phase3_admin_fleet_orders_acl.up.sql"
	adminFleetPositionsACLMigration         = "20260729000100_phase3_admin_fleet_positions_acl.up.sql"
)

func TestAdminFleetPositionsACLUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetPositionACLState(t, pool)
	beforePosition := readAdminFleetPositionState(t, pool)
	beforeFillDigest, beforeFillFile := readAdminFleetFillState(t, pool)
	beforeFillACL := readAdminFleetFillsRawACLState(t, pool)

	var beforeCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("read current-main migration count: %v", err)
	}
	if beforeCount != 30 {
		t.Fatalf("current-main migration count = %d, want 30", beforeCount)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin positions ACL: %v", err)
	}

	afterPosition := readAdminFleetPositionState(t, pool)
	afterFillDigest, afterFillFile := readAdminFleetFillState(t, pool)
	afterFillACL := readAdminFleetFillsRawACLState(t, pool)
	if afterPosition != beforePosition {
		t.Fatalf(
			"ACL-only upgrade changed position data or relation file: before=%v after=%v",
			beforePosition,
			afterPosition,
		)
	}
	if afterFillDigest != beforeFillDigest ||
		afterFillFile != beforeFillFile ||
		afterFillACL != beforeFillACL {
		t.Fatalf(
			"positions ACL upgrade changed fill digest/file/ACL = %t/%t/%t",
			afterFillDigest == beforeFillDigest,
			afterFillFile == beforeFillFile,
			afterFillACL == beforeFillACL,
		)
	}

	var afterCount int
	var afterTip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&afterCount, &afterTip); err != nil {
		t.Fatalf("read upgraded migration history: %v", err)
	}
	if afterCount != 31 || afterTip != adminFleetPositionsACLMigration {
		t.Fatalf(
			"upgraded history = count %d tip %q, want 31/%q",
			afterCount,
			afterTip,
			adminFleetPositionsACLMigration,
		)
	}

	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", adminFleetPositionsACLMigration,
	))
	if err != nil {
		t.Fatalf("read target migration: %v", err)
	}
	wantChecksum := sha256.Sum256(raw)
	var storedChecksum []byte
	if err := pool.QueryRow(ctx, `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		adminFleetPositionsACLMigration,
	).Scan(&storedChecksum); err != nil {
		t.Fatalf("read target migration checksum: %v", err)
	}
	if !equalBytes(storedChecksum, wantChecksum[:]) {
		t.Fatalf("migration checksum = %x, want %x", storedChecksum, wantChecksum)
	}
	assertAdminFleetPositionsRawACLAllowlist(t, pool)
	assertAdminFleetPositionsRuntimePrivileges(t, pool)
	assertAdminFleetPositionsAndFillsAPIReadable(t, pool)
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current migration history: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous binary verification = %v, want schema-ahead", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent target migration rerun: %v", err)
	}
	var rerunCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&rerunCount); err != nil {
		t.Fatalf("read rerun migration count: %v", err)
	}
	if rerunCount != 31 {
		t.Fatalf("rerun migration count = %d, want 31", rerunCount)
	}
}

func TestAdminFleetPositionsACLScrubsHostileGrantChains(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("admin_positions_hostile_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	dependentID := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	cleaned := false
	t.Cleanup(func() {
		if !cleaned {
			if err := cleanupAdminFleetPositionsHostileRole(
				context.Background(), pool, ownerID, hostileID,
			); err != nil {
				t.Errorf("cleanup hostile ACL fixture: %v", err)
			}
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatalf("install hostile owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedAdminFleetPositionACLState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.positions TO PUBLIC;
		GRANT UPDATE (status) ON trading.positions TO PUBLIC;
		GRANT SELECT ON trading.positions TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (status) ON trading.positions TO %[1]s WITH GRANT OPTION`,
		hostileID,
	)); err != nil {
		t.Fatalf("install direct hostile position ACLs: %v", err)
	}
	grantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dependent grant chain: %v", err)
	}
	if _, err := grantTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("assume hostile grantor role: %v", err)
	}
	if _, err := grantTx.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.positions TO %[1]s;
		GRANT UPDATE (status) ON trading.positions TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("install dependent grant chain: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit dependent grant chain: %v", err)
	}
	hostileACL := readAdminFleetPositionsUnexpectedACL(t, pool)
	if len(hostileACL) <= len(adminFleetPositionsACLAllowlist()) {
		t.Fatalf("hostile fixture did not expand ACL: %v", hostileACL)
	}

	beforePosition := readAdminFleetPositionState(t, pool)
	beforeFillDigest, beforeFillFile := readAdminFleetFillState(t, pool)
	beforeFillACL := readAdminFleetFillsRawACLState(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply admin positions ACL scrub: %v", err)
	}
	if after := readAdminFleetPositionState(t, pool); after != beforePosition {
		t.Fatalf("hostile ACL scrub changed position data/file: before=%v after=%v", beforePosition, after)
	}
	afterFillDigest, afterFillFile := readAdminFleetFillState(t, pool)
	if afterFillDigest != beforeFillDigest ||
		afterFillFile != beforeFillFile ||
		readAdminFleetFillsRawACLState(t, pool) != beforeFillACL {
		t.Fatal("positions hostile ACL scrub changed existing fill state")
	}
	assertAdminFleetPositionsRawACLAllowlist(t, pool)
	assertAdminFleetPositionsRuntimePrivileges(t, pool)
	assertAdminFleetPositionsAndFillsAPIReadable(t, pool)
	for _, role := range []string{hostileRole, dependentRole} {
		assertAdminFleetPositionsRoleDenied(t, pool, role)
	}

	if err := cleanupAdminFleetPositionsHostileRole(
		ctx, pool, ownerID, hostileID,
	); err != nil {
		t.Fatalf("cleanup hostile ACL fixture: %v", err)
	}
	cleaned = true
}

func TestAdminFleetPositionsACLRejectsPreRevocationWriter(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetPositionACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT USAGE ON SCHEMA trading TO platformgo_projector;
		GRANT UPDATE (status) ON trading.positions TO platformgo_projector`,
	); err != nil {
		t.Fatalf("grant hostile pre-revocation writer: %v", err)
	}
	beforeACL := readAdminFleetPositionsRawACLState(t, pool)
	beforePosition := readAdminFleetPositionState(t, pool)
	beforeFillDigest, beforeFillFile := readAdminFleetFillState(t, pool)
	beforeFillACL := readAdminFleetFillsRawACLState(t, pool)

	dmlTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation writer: %v", err)
	}
	if _, err := dmlTx.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		UPDATE trading.positions
		   SET status = 'hostile-uncommitted'`); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("execute hostile pre-revocation update: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetPositionsACLMigration),
	)
	err = current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("pre-revocation writer migration error = %v, want SQLSTATE 55P03", err)
	}

	var journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM engine.schema_migrations WHERE filename = $1
		)`,
		adminFleetPositionsACLMigration,
	).Scan(&journaled); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("inspect pre-revocation migration journal: %v", err)
	}
	if journaled {
		_ = dmlTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure journaled migration")
	}
	if afterACL := readAdminFleetPositionsRawACLState(t, pool); afterACL != beforeACL {
		_ = dmlTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure changed position ACL")
	}
	if after := readAdminFleetPositionState(t, pool); after != beforePosition {
		_ = dmlTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure changed committed position state")
	}
	afterFillDigest, afterFillFile := readAdminFleetFillState(t, pool)
	if afterFillDigest != beforeFillDigest ||
		afterFillFile != beforeFillFile ||
		readAdminFleetFillsRawACLState(t, pool) != beforeFillACL {
		_ = dmlTx.Rollback(ctx)
		t.Fatal("pre-revocation writer failure changed fill state")
	}
	if err := dmlTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation writer: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after pre-revocation writer drain: %v", err)
	}
	if after := readAdminFleetPositionState(t, pool); after != beforePosition {
		t.Fatal("successful writer-fenced retry changed position state")
	}
	afterFillDigest, afterFillFile = readAdminFleetFillState(t, pool)
	if afterFillDigest != beforeFillDigest ||
		afterFillFile != beforeFillFile ||
		readAdminFleetFillsRawACLState(t, pool) != beforeFillACL {
		t.Fatal("successful writer-fenced retry changed fill state")
	}
	assertAdminFleetPositionsRawACLAllowlist(t, pool)
	assertAdminFleetPositionsRuntimePrivileges(t, pool)
	assertAdminFleetPositionsAndFillsAPIReadable(t, pool)
}

type adminFleetPositionState struct {
	Digest   [sha256.Size]byte
	FileNode uint32
}

func seedAdminFleetPositionACLState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seedAdminFleetFill(t, pool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.positions (
			position_id,
			account_id,
			instrument_id,
			side,
			status,
			signed_quantity,
			average_open_price,
			realized_pnl,
			settlement_currency,
			margin_mode,
			isolated_collateral,
			version,
			updated_at
		) VALUES (
			'019fa930-0000-4000-8000-000000000001',
			'urn:xb:account:admin-fills-acl',
			'BTC-PERP',
			'LONG',
			'OPEN',
			0.01,
			60000,
			12.34,
			'USDC',
			'CROSS',
			0,
			3,
			'2026-07-29T12:00:00Z'
		)`,
	); err != nil {
		t.Fatalf("seed populated admin positions ACL fixture: %v", err)
	}
}

func readAdminFleetPositionState(
	t *testing.T,
	pool *pgxpool.Pool,
) adminFleetPositionState {
	t.Helper()
	var canonical string
	var state adminFleetPositionState
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(
				jsonb_agg(
					jsonb_build_object(
						'position_id', position_id::text,
						'account_id', account_id,
						'instrument_id', instrument_id,
						'side', side,
						'status', status,
						'signed_quantity', signed_quantity::text,
						'average_open_price', average_open_price::text,
						'realized_pnl', realized_pnl::text,
						'settlement_currency', settlement_currency,
						'margin_mode', margin_mode,
						'isolated_collateral', isolated_collateral::text,
						'version', version,
						'updated_at', updated_at
					)
					ORDER BY position_id
				),
				'[]'::jsonb
			)::text,
			pg_relation_filenode('trading.positions'::regclass)
		  FROM trading.positions`,
	).Scan(&canonical, &state.FileNode); err != nil {
		t.Fatalf("read explicit-column position digest and relation file: %v", err)
	}
	state.Digest = sha256.Sum256([]byte(canonical))
	return state
}

func readAdminFleetPositionsRawACLState(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var tableACL, columnACL string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(relation.relacl::text, '<NULL>'),
			COALESCE(
				(
					SELECT string_agg(
						pg_catalog.format(
							'%s=%s',
							attribute.attname,
							COALESCE(attribute.attacl::text, '<NULL>')
						),
						E'\n'
						ORDER BY attribute.attnum
					)
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid = relation.oid
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				),
				''
			)
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid = 'trading.positions'::pg_catalog.regclass`,
	).Scan(&tableACL, &columnACL); err != nil {
		t.Fatalf("read raw positions ACL state: %v", err)
	}
	return tableACL + "\n" + columnACL
}

func readAdminFleetPositionsUnexpectedACL(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relation AS (
			SELECT oid, relowner, relacl
			  FROM pg_catalog.pg_class
			 WHERE oid = 'trading.positions'::pg_catalog.regclass
		),
		table_acl AS (
			SELECT
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'table'::text AS scope,
				relation.relowner
			  FROM relation
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relation.relacl,
					pg_catalog.acldefault('r', relation.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
		),
		column_acl AS (
			SELECT
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'column:' || attribute.attname AS scope,
				relation.relowner
			  FROM relation
			  JOIN pg_catalog.pg_attribute AS attribute
			    ON attribute.attrelid = relation.oid
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				attribute.attacl
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
			 WHERE attribute.attacl IS NOT NULL
		)
		SELECT grantee, privilege_type, is_grantable, scope
		  FROM (
			SELECT grantee, privilege_type, is_grantable, scope, relowner
			  FROM table_acl
			UNION ALL
			SELECT grantee, privilege_type, is_grantable, scope, relowner
			  FROM column_acl
		  ) AS acl
		 WHERE acl.grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_catalog.pg_roles WHERE oid = acl.relowner
		 )`)
	if err != nil {
		t.Fatalf("inspect complete positions ACL: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &scope); err != nil {
			t.Fatalf("scan complete positions ACL: %v", err)
		}
		got = append(
			got,
			fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, scope),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate complete positions ACL: %v", err)
	}
	sort.Strings(got)
	return got
}

func adminFleetPositionsACLAllowlist() []string {
	want := []string{
		"platformgo_api|SELECT|false|table",
		"platformgo_engine|INSERT|false|table",
		"platformgo_engine|SELECT|false|table",
		"platformgo_engine|UPDATE|false|table",
	}
	sort.Strings(want)
	return want
}

func assertAdminFleetPositionsRawACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	got := readAdminFleetPositionsUnexpectedACL(t, pool)
	want := adminFleetPositionsACLAllowlist()
	if !slices.Equal(got, want) {
		t.Fatalf("positions raw ACL = %v, want exact %v", got, want)
	}
}

func assertAdminFleetPositionsRuntimePrivileges(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	type privilegeCheck struct {
		role, privilege string
		want            bool
	}
	checks := []privilegeCheck{
		{"platformgo_api", "SELECT", true},
		{"platformgo_api", "INSERT", false},
		{"platformgo_api", "UPDATE", false},
		{"platformgo_api", "DELETE", false},
		{"platformgo_api", "TRUNCATE", false},
		{"platformgo_engine", "SELECT", true},
		{"platformgo_engine", "INSERT", true},
		{"platformgo_engine", "UPDATE", true},
		{"platformgo_engine", "DELETE", false},
		{"platformgo_engine", "TRUNCATE", false},
	}
	for _, check := range checks {
		var got bool
		if err := pool.QueryRow(context.Background(), `
			SELECT has_table_privilege($1, 'trading.positions', $2)`,
			check.role,
			check.privilege,
		).Scan(&got); err != nil {
			t.Fatalf(
				"inspect %s positions %s: %v",
				check.role,
				check.privilege,
				err,
			)
		}
		if got != check.want {
			t.Fatalf(
				"%s positions %s = %t, want %t",
				check.role,
				check.privilege,
				got,
				check.want,
			)
		}
	}
}

func assertAdminFleetPositionsAndFillsAPIReadable(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin combined API read: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err != nil {
		t.Fatalf("assume platformgo_api: %v", err)
	}
	var positions, fills int
	if err := tx.QueryRow(context.Background(), `
		SELECT
			(SELECT count(position_id) FROM trading.positions),
			(SELECT count(fill_id) FROM trading.fills)`,
	).Scan(&positions, &fills); err != nil {
		t.Fatalf("combined API positions/fills read: %v", err)
	}
	if positions != 1 || fills != 1 {
		t.Fatalf(
			"combined API positions/fills counts = %d/%d, want 1/1",
			positions,
			fills,
		)
	}
}

func assertAdminFleetPositionsRoleDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		"SELECT position_id FROM trading.positions",
		"UPDATE trading.positions SET status = status",
	} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s denied positions operation: %v", role, err)
		}
		if _, err := tx.Exec(
			context.Background(),
			"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
		); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatalf("assume role %s: %v", role, err)
		}
		_, statementErr := tx.Exec(context.Background(), statement)
		_ = tx.Rollback(context.Background())
		var postgresError *pgconn.PgError
		if !errors.As(statementErr, &postgresError) ||
			postgresError.Code != "42501" {
			t.Fatalf(
				"role %s statement %q error = %v, want SQLSTATE 42501",
				role,
				statement,
				statementErr,
			)
		}
	}
}

func cleanupAdminFleetPositionsHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerIdentifier string,
	roleIdentifier string,
) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		DO $cleanup$
		DECLARE
			column_name name;
		BEGIN
			IF pg_catalog.to_regclass('trading.positions') IS NOT NULL THEN
				EXECUTE
					'REVOKE ALL PRIVILEGES ON TABLE trading.positions FROM %[1]s CASCADE';
				FOR column_name IN
					SELECT attribute.attname
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid =
					       'trading.positions'::pg_catalog.regclass
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				LOOP
					EXECUTE pg_catalog.format(
						'REVOKE ALL PRIVILEGES (%%I) ON TABLE trading.positions FROM %[1]s CASCADE',
						column_name
					);
				END LOOP;
			END IF;
		END
		$cleanup$`,
		roleIdentifier,
	)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		REVOKE ALL PRIVILEGES ON SCHEMA trading FROM %[2]s CASCADE;
		DROP OWNED BY %[2]s CASCADE`,
		ownerIdentifier,
		roleIdentifier,
	)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "DROP ROLE "+roleIdentifier)
	return err
}
