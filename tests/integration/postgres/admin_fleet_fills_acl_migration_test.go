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
	adminFleetFillsACLPreviousMigration = "20260728000200_phase3_command_market_sequence_binding.up.sql"
	adminFleetFillsACLMigration         = "20260728000300_phase3_admin_fleet_fills_acl.up.sql"
)

func TestAdminFleetFillsACLUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetFill(t, pool)

	beforeDigest, beforeFileNode := readAdminFleetFillState(t, pool)
	var beforeMigrationCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&beforeMigrationCount); err != nil {
		t.Fatalf("read current-main migration count: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin fills ACL: %v", err)
	}

	afterDigest, afterFileNode := readAdminFleetFillState(t, pool)
	if afterDigest != beforeDigest || afterFileNode != beforeFileNode {
		t.Fatalf(
			"ACL-only upgrade changed fill data or relation file: digest %x->%x filenode %d->%d",
			beforeDigest,
			afterDigest,
			beforeFileNode,
			afterFileNode,
		)
	}
	var (
		afterMigrationCount int
		afterTip            string
	)
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&afterMigrationCount, &afterTip); err != nil {
		t.Fatalf("read upgraded migration history: %v", err)
	}
	if afterTip != adminFleetFillsACLMigration {
		t.Fatalf(
			"upgraded migration tip = %q, want %q (target forward migration is missing)",
			afterTip,
			adminFleetFillsACLMigration,
		)
	}
	if afterMigrationCount != beforeMigrationCount+1 {
		t.Fatalf(
			"upgraded migration count = %d, want %d",
			afterMigrationCount,
			beforeMigrationCount+1,
		)
	}
	assertAdminFleetFillsRawACLAllowlist(t, pool)
	assertAdminFleetFillsReadable(t, pool, "platformgo_api")
	assertAdminFleetFillsReadable(t, pool, "platformgo_engine")
	assertAdminFleetFillsMutationDenied(t, pool, "platformgo_api")

	raw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"migrations",
		adminFleetFillsACLMigration,
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
		adminFleetFillsACLMigration,
	).Scan(&storedChecksum); err != nil {
		t.Fatalf("read target migration checksum: %v", err)
	}
	if !equalBytes(storedChecksum, wantChecksum[:]) {
		t.Fatalf("migration checksum = %x, want %x", storedChecksum, wantChecksum)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current migration history: %v", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLPreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf(
			"previous binary schema verification = %v, want ErrDatabaseSchemaAhead",
			err,
		)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("idempotent target migration rerun: %v", err)
	}
	var rerunMigrationCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&rerunMigrationCount); err != nil {
		t.Fatalf("read rerun migration count: %v", err)
	}
	if rerunMigrationCount != afterMigrationCount {
		t.Fatalf(
			"idempotent rerun changed migration count = %d, want %d",
			rerunMigrationCount,
			afterMigrationCount,
		)
	}
}

func TestAdminFleetFillsACLScrubsHostileGrantChains(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var migrationOwner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&migrationOwner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("admin_fills_hostile_%d", os.Getpid())
	dependentRole := "platformgo_projector"
	ownerIdentifier := pgx.Identifier{migrationOwner}.Sanitize()
	hostileIdentifier := pgx.Identifier{hostileRole}.Sanitize()
	dependentIdentifier := pgx.Identifier{dependentRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileIdentifier+" NOLOGIN"); err != nil {
		t.Fatalf("create hostile role: %v", err)
	}
	cleaned := false
	t.Cleanup(func() {
		if cleaned {
			return
		}
		if err := cleanupAdminFleetHostileRole(
			context.Background(),
			pool,
			ownerIdentifier,
			hostileIdentifier,
		); err != nil {
			t.Errorf("cleanup hostile ACL fixture: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		CREATE SCHEMA trading;
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s`,
		ownerIdentifier,
		hostileIdentifier,
	)); err != nil {
		t.Fatalf("install hostile owner defaults: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedAdminFleetFill(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.fills TO PUBLIC;
		GRANT UPDATE (fee) ON trading.fills TO PUBLIC;
		GRANT SELECT ON trading.fills TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (fee) ON trading.fills TO %[1]s WITH GRANT OPTION`,
		hostileIdentifier,
	)); err != nil {
		t.Fatalf("install direct hostile fills ACLs: %v", err)
	}
	grantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin dependent grant chain: %v", err)
	}
	if _, err := grantTx.Exec(ctx, "SET LOCAL ROLE "+hostileIdentifier); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("assume hostile grantor role: %v", err)
	}
	if _, err := grantTx.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.fills TO %[1]s;
		GRANT UPDATE (fee) ON trading.fills TO %[1]s`,
		dependentIdentifier,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("install dependent grant chain: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit dependent grant chain: %v", err)
	}

	hostileACL := readAdminFleetFillsUnexpectedACL(t, pool)
	wantHostileACL := []string{
		"PUBLIC|SELECT|false|table",
		"PUBLIC|UPDATE|false|column:fee",
		dependentRole + "|SELECT|false|table",
		dependentRole + "|UPDATE|false|column:fee",
		hostileRole + "|DELETE|false|table",
		hostileRole + "|INSERT|false|table",
		hostileRole + "|MAINTAIN|false|table",
		hostileRole + "|REFERENCES|false|table",
		hostileRole + "|SELECT|true|table",
		hostileRole + "|TRIGGER|false|table",
		hostileRole + "|TRUNCATE|false|table",
		hostileRole + "|UPDATE|false|table",
		hostileRole + "|UPDATE|true|column:fee",
		"platformgo_api|SELECT|false|table",
		"platformgo_engine|INSERT|false|table",
		"platformgo_engine|SELECT|false|table",
	}
	sort.Strings(wantHostileACL)
	if !slices.Equal(hostileACL, wantHostileACL) {
		t.Fatalf(
			"reproduced hostile fills ACL = %v, want exact %v",
			hostileACL,
			wantHostileACL,
		)
	}
	t.Logf("reproduced exact hostile fills ACL: %v", hostileACL)

	beforeDigest, beforeFileNode := readAdminFleetFillState(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply admin fills ACL scrub: %v", err)
	}
	afterDigest, afterFileNode := readAdminFleetFillState(t, pool)
	if afterDigest != beforeDigest || afterFileNode != beforeFileNode {
		t.Fatalf(
			"hostile ACL scrub changed fill data or relation file: digest %x->%x filenode %d->%d",
			beforeDigest,
			afterDigest,
			beforeFileNode,
			afterFileNode,
		)
	}
	assertAdminFleetFillsRawACLAllowlist(t, pool)
	assertAdminFleetFillsReadable(t, pool, "platformgo_api")
	assertAdminFleetFillsReadable(t, pool, "platformgo_engine")
	assertAdminFleetFillsMutationDenied(t, pool, "platformgo_api")
	for _, role := range []string{hostileRole, dependentRole} {
		assertAdminFleetFillsReadDenied(t, pool, role)
		assertAdminFleetFillsMutationDenied(t, pool, role)
	}

	if err := cleanupAdminFleetHostileRole(
		ctx,
		pool,
		ownerIdentifier,
		hostileIdentifier,
	); err != nil {
		t.Fatalf("cleanup hostile ACL fixture: %v", err)
	}
	cleaned = true
}

func TestAdminFleetFillsACLLockTimeoutRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetFill(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT UPDATE (fee) ON trading.fills TO PUBLIC`,
	); err != nil {
		t.Fatalf("install rollback ACL fixture: %v", err)
	}
	beforeACL := readAdminFleetFillsRawACLState(t, pool)
	beforeDigest, beforeFileNode := readAdminFleetFillState(t, pool)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin AccessExclusive blocker: %v", err)
	}
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("lock fills AccessExclusive: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLMigration),
	)
	err = current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("contended migration error = %v, want SQLSTATE 55P03", err)
	}

	var journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		adminFleetFillsACLMigration,
	).Scan(&journaled); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("inspect contended migration journal: %v", err)
	}
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatalf("release AccessExclusive blocker: %v", err)
	}
	afterFailedACL := readAdminFleetFillsRawACLState(t, pool)
	afterFailedDigest, afterFailedFileNode := readAdminFleetFillState(t, pool)
	if journaled ||
		afterFailedACL != beforeACL ||
		afterFailedDigest != beforeDigest ||
		afterFailedFileNode != beforeFileNode {
		t.Fatalf(
			"failed migration changed journal/ACL/digest/filenode = %t/%t/%t/%t",
			journaled,
			afterFailedACL == beforeACL,
			afterFailedDigest == beforeDigest,
			afterFailedFileNode == beforeFileNode,
		)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended admin fills ACL migration: %v", err)
	}
	assertAdminFleetFillsRawACLAllowlist(t, pool)
	afterRetryDigest, afterRetryFileNode := readAdminFleetFillState(t, pool)
	if afterRetryDigest != beforeDigest || afterRetryFileNode != beforeFileNode {
		t.Fatalf(
			"successful retry changed fill data or relation file: digest %x->%x filenode %d->%d",
			beforeDigest,
			afterRetryDigest,
			beforeFileNode,
			afterRetryFileNode,
		)
	}
}

func TestAdminFleetFillsACLDoesNotBlockOrdinaryRowExclusiveDML(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetFill(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT SELECT ON trading.fills TO PUBLIC`,
	); err != nil {
		t.Fatalf("install ordinary-DML ACL fixture: %v", err)
	}

	dmlTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ordinary fills DML: %v", err)
	}
	if _, err := dmlTx.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES (
			'019fa900-0000-4000-8000-000000000005',
			'019fa900-0000-4000-8000-000000000002',
			'019fa900-0000-4000-8000-000000000006',
			'urn:xb:account:admin-fills-acl', 'BTC-PERP',
			'BUY', 60000, 0.01,
			'019fa900-0000-4000-8000-000000000007',
			'open', 'TAKER', 1785312000000000001, 10
		)`,
	); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("hold ordinary RowExclusive fills write: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetFillsACLMigration),
	).Migrate(ctx); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("ordinary RowExclusive DML blocked ACL-only migration: %v", err)
	}
	if err := dmlTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback ordinary fills DML: %v", err)
	}
	var journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM engine.schema_migrations
			 WHERE filename = $1
		)`,
		adminFleetFillsACLMigration,
	).Scan(&journaled); err != nil {
		t.Fatalf("inspect ordinary-DML migration journal: %v", err)
	}
	if !journaled {
		t.Fatalf("ordinary-DML migration did not apply %s", adminFleetFillsACLMigration)
	}
	assertAdminFleetFillsRawACLAllowlist(t, pool)
}

func seedAdminFleetFill(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:admin-fills-acl', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa900-0000-4000-8000-000000000002',
			'urn:xb:account:admin-fills-acl', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 0.01, 0.01, 60000,
			false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'019fa900-0000-4000-8000-000000000001',
			'019fa900-0000-4000-8000-000000000002',
			'019fa900-0000-4000-8000-000000000003',
			'urn:xb:account:admin-fills-acl', 'BTC-PERP',
			'BUY', 60000, 0.01,
			'019fa900-0000-4000-8000-000000000004', 'open',
			NULL, NULL, 'TAKER', 0.5, 'USDC',
			1785312000000000000, 10
		)`,
	); err != nil {
		t.Fatalf("seed populated admin fills ACL fixture: %v", err)
	}
}

func readAdminFleetFillState(
	t *testing.T,
	pool *pgxpool.Pool,
) ([sha256.Size]byte, uint32) {
	t.Helper()
	var (
		canonical string
		fileNode  uint32
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(
				jsonb_agg(
					jsonb_build_object(
						'fill_id', fill_id::text,
						'order_id', order_id::text,
						'input_id', input_id::text,
						'account_id', account_id,
						'instrument_id', instrument_id,
						'side', side,
						'price', price::text,
						'quantity', quantity::text,
						'position_id', position_id::text,
						'position_effect', position_effect,
						'realized_pnl', realized_pnl::text,
						'settlement_currency', settlement_currency,
						'liquidity_side', liquidity_side,
						'fee', fee::text,
						'fee_currency', fee_currency,
						'logical_time', logical_time,
						'effective_leverage', effective_leverage::text
					)
					ORDER BY fill_id
				),
				'[]'::jsonb
			)::text,
			pg_relation_filenode('trading.fills'::regclass)
		  FROM trading.fills`,
	).Scan(&canonical, &fileNode); err != nil {
		t.Fatalf("read fill digest and relation file: %v", err)
	}
	return sha256.Sum256([]byte(canonical)), fileNode
}

func readAdminFleetFillsRawACLState(
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
		 WHERE relation.oid = 'trading.fills'::pg_catalog.regclass`,
	).Scan(&tableACL, &columnACL); err != nil {
		t.Fatalf("read raw fills ACL state: %v", err)
	}
	return tableACL + "\n" + columnACL
}

func readAdminFleetFillsUnexpectedACL(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relation AS (
			SELECT oid, relowner, relacl
			  FROM pg_catalog.pg_class
			 WHERE oid = 'trading.fills'::pg_catalog.regclass
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
			SELECT rolname
			  FROM pg_catalog.pg_roles
			 WHERE oid = acl.relowner
		 )`)
	if err != nil {
		t.Fatalf("inspect complete fills ACL: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &scope); err != nil {
			t.Fatalf("scan complete fills ACL: %v", err)
		}
		got = append(
			got,
			fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, scope),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate complete fills ACL: %v", err)
	}
	sort.Strings(got)
	return got
}

func assertAdminFleetFillsRawACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	got := readAdminFleetFillsUnexpectedACL(t, pool)
	want := []string{
		"platformgo_api|SELECT|false|table",
		"platformgo_engine|INSERT|false|table",
		"platformgo_engine|SELECT|false|table",
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("fills raw ACL = %v, want exact %v", got, want)
	}
}

func assertAdminFleetFillsReadable(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin %s fills read: %v", role, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatalf("assume role %s: %v", role, err)
	}
	var count int
	if err := tx.QueryRow(context.Background(), `
		SELECT count(fill_id) FROM trading.fills`,
	).Scan(&count); err != nil {
		t.Fatalf("role %s read fills: %v", role, err)
	}
	if count != 1 {
		t.Fatalf("role %s fill count = %d, want 1", role, count)
	}
}

func assertAdminFleetFillsReadDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin %s denied fills read: %v", role, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatalf("assume role %s: %v", role, err)
	}
	_, statementErr := tx.Exec(
		context.Background(),
		"SELECT fill_id FROM trading.fills",
	)
	var postgresError *pgconn.PgError
	if !errors.As(statementErr, &postgresError) ||
		postgresError.Code != "42501" {
		t.Fatalf(
			"role %s fills read error = %v, want SQLSTATE 42501",
			role,
			statementErr,
		)
	}
}

func assertAdminFleetFillsMutationDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		`INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		)
		SELECT
			'019fa900-0000-4000-8000-000000000011',
			order_id,
			'019fa900-0000-4000-8000-000000000012',
			account_id,
			instrument_id,
			side,
			price,
			quantity,
			'019fa900-0000-4000-8000-000000000013',
			position_effect,
			realized_pnl,
			settlement_currency,
			liquidity_side,
			fee,
			fee_currency,
			logical_time + 1,
			effective_leverage
		  FROM trading.fills
		 LIMIT 1`,
		"UPDATE trading.fills SET fee = fee",
		"DELETE FROM trading.fills",
		"TRUNCATE trading.fills",
	} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s denied fills mutation: %v", role, err)
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
				"role %s mutation %q error = %v, want SQLSTATE 42501",
				role,
				statement,
				statementErr,
			)
		}
	}
}

func cleanupAdminFleetHostileRole(
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
			IF pg_catalog.to_regclass('trading.fills') IS NOT NULL THEN
				EXECUTE
					'REVOKE ALL PRIVILEGES ON TABLE trading.fills FROM %[1]s CASCADE';
				FOR column_name IN
					SELECT attribute.attname
					  FROM pg_catalog.pg_attribute AS attribute
					 WHERE attribute.attrelid =
					       'trading.fills'::pg_catalog.regclass
					   AND attribute.attnum > 0
					   AND NOT attribute.attisdropped
				LOOP
					EXECUTE pg_catalog.format(
						'REVOKE ALL PRIVILEGES (%%I) ON TABLE trading.fills FROM %[1]s CASCADE',
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
