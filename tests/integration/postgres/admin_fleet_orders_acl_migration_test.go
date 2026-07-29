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
	adminFleetOrdersACLPreviousMigration = "20260728000300_phase3_admin_fleet_fills_acl.up.sql"
	adminFleetOrdersACLMigration         = "20260728000400_phase3_admin_fleet_orders_acl.up.sql"
)

func TestAdminFleetOrdersACLUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetOrdersACLState(t, pool)
	before := readAdminFleetOrdersRelationState(t, pool)

	var beforeCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM engine.schema_migrations`,
	).Scan(&beforeCount); err != nil {
		t.Fatalf("read current-main migration count: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade admin orders ACL: %v", err)
	}

	after := readAdminFleetOrdersRelationState(t, pool)
	if after != before {
		t.Fatalf("ACL-only upgrade changed order data or relation files: before=%v after=%v", before, after)
	}
	var afterCount int
	var afterTip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`,
	).Scan(&afterCount, &afterTip); err != nil {
		t.Fatalf("read upgraded migration history: %v", err)
	}
	if afterCount != beforeCount+1 || afterTip != adminFleetOrdersACLMigration {
		t.Fatalf(
			"upgraded history = count %d tip %q, want %d/%q",
			afterCount,
			afterTip,
			beforeCount+1,
			adminFleetOrdersACLMigration,
		)
	}
	assertAdminFleetOrdersRawACLAllowlist(t, pool)
	assertAdminFleetOrdersRuntimePrivileges(t, pool)

	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "migrations", adminFleetOrdersACLMigration,
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
		adminFleetOrdersACLMigration,
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
		migrationFilesThrough(t, adminFleetOrdersACLPreviousMigration),
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
	if rerunCount != afterCount {
		t.Fatalf("rerun migration count = %d, want %d", rerunCount, afterCount)
	}
}

func TestAdminFleetOrdersACLScrubsHostileGrantChains(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatalf("read migration owner: %v", err)
	}
	hostileRole := fmt.Sprintf("admin_orders_hostile_%d", os.Getpid())
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
			if err := cleanupAdminFleetOrdersHostileRole(
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
		migrationFilesThrough(t, adminFleetOrdersACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	seedAdminFleetOrdersACLState(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.orders, trading.order_intents TO PUBLIC;
		GRANT UPDATE (status) ON trading.orders TO PUBLIC;
		GRANT UPDATE (intent_id) ON trading.order_intents TO PUBLIC;
		GRANT SELECT ON trading.orders, trading.order_intents TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (status) ON trading.orders TO %[1]s WITH GRANT OPTION;
		GRANT UPDATE (intent_id) ON trading.order_intents TO %[1]s WITH GRANT OPTION`,
		hostileID,
	)); err != nil {
		t.Fatalf("install direct hostile order ACLs: %v", err)
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
		GRANT SELECT ON trading.orders, trading.order_intents TO %[1]s;
		GRANT UPDATE (status) ON trading.orders TO %[1]s;
		GRANT UPDATE (intent_id) ON trading.order_intents TO %[1]s`,
		dependentID,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatalf("install dependent grant chain: %v", err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit dependent grant chain: %v", err)
	}

	hostileACL := readAdminFleetOrdersUnexpectedACL(t, pool)
	if len(hostileACL) <= len(adminFleetOrdersACLAllowlist()) {
		t.Fatalf("hostile fixture did not expand ACL: %v", hostileACL)
	}
	t.Logf("reproduced hostile orders ACL: %v", hostileACL)
	before := readAdminFleetOrdersRelationState(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply admin orders ACL scrub: %v", err)
	}
	if after := readAdminFleetOrdersRelationState(t, pool); after != before {
		t.Fatalf("hostile ACL scrub changed order data/files: before=%v after=%v", before, after)
	}
	assertAdminFleetOrdersRawACLAllowlist(t, pool)
	assertAdminFleetOrdersRuntimePrivileges(t, pool)
	for _, role := range []string{hostileRole, dependentRole} {
		assertAdminFleetOrdersRoleDenied(t, pool, role)
	}

	if err := cleanupAdminFleetOrdersHostileRole(
		ctx, pool, ownerID, hostileID,
	); err != nil {
		t.Fatalf("cleanup hostile ACL fixture: %v", err)
	}
	cleaned = true
}

func TestAdminFleetOrdersACLLockTimeoutRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetOrdersACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT UPDATE (status) ON trading.orders TO PUBLIC;
		GRANT UPDATE (intent_id) ON trading.order_intents TO PUBLIC`,
	); err != nil {
		t.Fatalf("install rollback ACL fixture: %v", err)
	}
	beforeACL := readAdminFleetOrdersRawACLState(t, pool)
	beforeState := readAdminFleetOrdersRelationState(t, pool)

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin AccessExclusive blocker: %v", err)
	}
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.order_intents IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("lock order_intents AccessExclusive: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLMigration),
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
			SELECT 1 FROM engine.schema_migrations WHERE filename = $1
		)`,
		adminFleetOrdersACLMigration,
	).Scan(&journaled); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("inspect contended migration journal: %v", err)
	}
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatalf("release AccessExclusive blocker: %v", err)
	}
	if afterACL := readAdminFleetOrdersRawACLState(t, pool); afterACL != beforeACL {
		t.Fatalf("failed migration left partial first-relation ACL change")
	}
	if afterState := readAdminFleetOrdersRelationState(t, pool); afterState != beforeState {
		t.Fatalf("failed migration changed order data/files")
	}
	if journaled {
		t.Fatalf("failed migration journaled %s", adminFleetOrdersACLMigration)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended admin orders ACL migration: %v", err)
	}
	assertAdminFleetOrdersRawACLAllowlist(t, pool)
	if afterState := readAdminFleetOrdersRelationState(t, pool); afterState != beforeState {
		t.Fatalf("successful retry changed order data/files")
	}
}

func TestAdminFleetOrdersACLRejectsPreRevocationWriter(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLPreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	seedAdminFleetOrdersACLState(t, pool)
	if _, err := pool.Exec(ctx, `
		GRANT USAGE ON SCHEMA trading TO platformgo_projector;
		GRANT UPDATE (status) ON trading.orders TO platformgo_projector`,
	); err != nil {
		t.Fatalf("grant hostile pre-revocation writer: %v", err)
	}
	beforeACL := readAdminFleetOrdersRawACLState(t, pool)
	beforeState := readAdminFleetOrdersRelationState(t, pool)

	dmlTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pre-revocation writer: %v", err)
	}
	if _, err := dmlTx.Exec(ctx, `
		SET LOCAL ROLE platformgo_projector;
		UPDATE trading.orders
		   SET status = 'CANCELED'`); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("execute hostile pre-revocation update: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, adminFleetOrdersACLMigration),
	)
	err = current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("pre-revocation writer migration error = %v, want SQLSTATE 55P03", err)
	}
	if err := dmlTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback pre-revocation writer: %v", err)
	}
	var journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM engine.schema_migrations WHERE filename = $1
		)`,
		adminFleetOrdersACLMigration,
	).Scan(&journaled); err != nil {
		t.Fatalf("inspect pre-revocation migration journal: %v", err)
	}
	if journaled {
		t.Fatalf("pre-revocation writer failure journaled migration")
	}
	if afterACL := readAdminFleetOrdersRawACLState(t, pool); afterACL != beforeACL {
		t.Fatalf("pre-revocation writer failure changed ACL")
	}
	if afterState := readAdminFleetOrdersRelationState(t, pool); afterState != beforeState {
		t.Fatalf("pre-revocation writer failure changed committed order state")
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after pre-revocation writer drain: %v", err)
	}
	if afterState := readAdminFleetOrdersRelationState(t, pool); afterState != beforeState {
		t.Fatalf("successful writer-fenced retry changed order state")
	}
	assertAdminFleetOrdersRawACLAllowlist(t, pool)
}

type adminFleetOrdersRelationState struct {
	OrdersDigest  [sha256.Size]byte
	OrdersFile    uint32
	IntentsDigest [sha256.Size]byte
	IntentsFile   uint32
}

func seedAdminFleetOrdersACLState(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	seedAdminFleetOrdersInstrument(t, pool)
	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_admin_fleet_orders_acl_api_login",
		"platformgo_api",
	)
	admitAdminFleetOrderIntent(t, apiPool)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:admin-orders-acl-materialized', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested, version
		) VALUES (
			'019fa920-0000-4000-8000-000000000001',
			'urn:xb:account:admin-orders-acl-materialized',
			'BTC-PERP', 'BUY', 'LIMIT', 'GTC', 'WORKING',
			0.01, 0, 0, false, false, true, 1
		)`,
	); err != nil {
		t.Fatalf("seed materialized ACL order: %v", err)
	}
}

func readAdminFleetOrdersRelationState(
	t *testing.T,
	pool *pgxpool.Pool,
) adminFleetOrdersRelationState {
	t.Helper()
	var orders, intents string
	var state adminFleetOrdersRelationState
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(
				(SELECT jsonb_agg(to_jsonb(row_value) ORDER BY order_id)::text
				   FROM trading.orders AS row_value),
				'[]'
			),
			pg_relation_filenode('trading.orders'::regclass),
			COALESCE(
				(SELECT jsonb_agg(to_jsonb(row_value) ORDER BY order_id)::text
				   FROM trading.order_intents AS row_value),
				'[]'
			),
			pg_relation_filenode('trading.order_intents'::regclass)`,
	).Scan(
		&orders,
		&state.OrdersFile,
		&intents,
		&state.IntentsFile,
	); err != nil {
		t.Fatalf("read order relation state: %v", err)
	}
	state.OrdersDigest = sha256.Sum256([]byte(orders))
	state.IntentsDigest = sha256.Sum256([]byte(intents))
	return state
}

func readAdminFleetOrdersRawACLState(
	t *testing.T,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT
			relation.oid::regclass::text,
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
		 WHERE relation.oid IN (
			'trading.orders'::regclass,
			'trading.order_intents'::regclass
		 )
		 ORDER BY relation.oid::regclass::text`)
	if err != nil {
		t.Fatalf("read raw order ACL state: %v", err)
	}
	defer rows.Close()
	var result string
	for rows.Next() {
		var relation, tableACL, columnACL string
		if err := rows.Scan(&relation, &tableACL, &columnACL); err != nil {
			t.Fatalf("scan raw order ACL state: %v", err)
		}
		result += relation + "\n" + tableACL + "\n" + columnACL + "\n"
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate raw order ACL state: %v", err)
	}
	return result
}

func readAdminFleetOrdersUnexpectedACL(
	t *testing.T,
	pool *pgxpool.Pool,
) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relations AS (
			SELECT oid, relowner, relacl, oid::regclass::text AS relation_name
			  FROM pg_catalog.pg_class
			 WHERE oid IN (
				'trading.orders'::regclass,
				'trading.order_intents'::regclass
			 )
		),
		table_acl AS (
			SELECT
				relation_name,
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'table'::text AS scope,
				relations.relowner
			  FROM relations
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relations.relacl,
					pg_catalog.acldefault('r', relations.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
		),
		column_acl AS (
			SELECT
				relations.relation_name,
				CASE
					WHEN privilege.grantee = 0 THEN 'PUBLIC'
					ELSE role.rolname
				END AS grantee,
				privilege.privilege_type,
				privilege.is_grantable,
				'column:' || attribute.attname AS scope,
				relations.relowner
			  FROM relations
			  JOIN pg_catalog.pg_attribute AS attribute
			    ON attribute.attrelid = relations.oid
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			  CROSS JOIN LATERAL pg_catalog.aclexplode(
				attribute.attacl
			  ) AS privilege
			  LEFT JOIN pg_catalog.pg_roles AS role
			    ON role.oid = privilege.grantee
			 WHERE attribute.attacl IS NOT NULL
		)
		SELECT relation_name, grantee, privilege_type, is_grantable, scope
		  FROM (
			SELECT * FROM table_acl
			UNION ALL
			SELECT * FROM column_acl
		  ) AS acl
		 WHERE acl.grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_catalog.pg_roles WHERE oid = acl.relowner
		 )`)
	if err != nil {
		t.Fatalf("inspect complete order ACL: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var relation, grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(
			&relation, &grantee, &privilege, &grantable, &scope,
		); err != nil {
			t.Fatalf("scan complete order ACL: %v", err)
		}
		got = append(got, fmt.Sprintf(
			"%s|%s|%s|%t|%s",
			relation,
			grantee,
			privilege,
			grantable,
			scope,
		))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate complete order ACL: %v", err)
	}
	sort.Strings(got)
	return got
}

func adminFleetOrdersACLAllowlist() []string {
	want := []string{
		"trading.order_intents|platformgo_api|INSERT|false|table",
		"trading.order_intents|platformgo_api|SELECT|false|table",
		"trading.order_intents|platformgo_engine|SELECT|false|table",
		"trading.orders|platformgo_api|SELECT|false|table",
		"trading.orders|platformgo_engine|INSERT|false|table",
		"trading.orders|platformgo_engine|SELECT|false|table",
		"trading.orders|platformgo_engine|UPDATE|false|table",
	}
	sort.Strings(want)
	return want
}

func assertAdminFleetOrdersRawACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	got := readAdminFleetOrdersUnexpectedACL(t, pool)
	want := adminFleetOrdersACLAllowlist()
	if !slices.Equal(got, want) {
		t.Fatalf("order raw ACL = %v, want exact %v", got, want)
	}
}

func assertAdminFleetOrdersRuntimePrivileges(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	type privilegeCheck struct {
		role, relation, privilege string
		want                      bool
	}
	checks := []privilegeCheck{
		{"platformgo_api", "trading.orders", "SELECT", true},
		{"platformgo_api", "trading.orders", "INSERT", false},
		{"platformgo_api", "trading.orders", "UPDATE", false},
		{"platformgo_api", "trading.orders", "DELETE", false},
		{"platformgo_api", "trading.orders", "TRUNCATE", false},
		{"platformgo_api", "trading.order_intents", "SELECT", true},
		{"platformgo_api", "trading.order_intents", "INSERT", true},
		{"platformgo_api", "trading.order_intents", "UPDATE", false},
		{"platformgo_api", "trading.order_intents", "DELETE", false},
		{"platformgo_api", "trading.order_intents", "TRUNCATE", false},
		{"platformgo_engine", "trading.orders", "SELECT", true},
		{"platformgo_engine", "trading.orders", "INSERT", true},
		{"platformgo_engine", "trading.orders", "UPDATE", true},
		{"platformgo_engine", "trading.orders", "DELETE", false},
		{"platformgo_engine", "trading.orders", "TRUNCATE", false},
		{"platformgo_engine", "trading.order_intents", "SELECT", true},
		{"platformgo_engine", "trading.order_intents", "INSERT", false},
		{"platformgo_engine", "trading.order_intents", "UPDATE", false},
		{"platformgo_engine", "trading.order_intents", "DELETE", false},
		{"platformgo_engine", "trading.order_intents", "TRUNCATE", false},
	}
	for _, check := range checks {
		var got bool
		if err := pool.QueryRow(context.Background(), `
			SELECT has_table_privilege($1, $2, $3)`,
			check.role,
			check.relation,
			check.privilege,
		).Scan(&got); err != nil {
			t.Fatalf("inspect %s %s %s: %v", check.role, check.relation, check.privilege, err)
		}
		if got != check.want {
			t.Fatalf(
				"%s %s %s = %t, want %t",
				check.role,
				check.relation,
				check.privilege,
				got,
				check.want,
			)
		}
	}
}

func assertAdminFleetOrdersRoleDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		"SELECT order_id FROM trading.orders",
		"SELECT order_id FROM trading.order_intents",
		"UPDATE trading.orders SET status = status",
		"UPDATE trading.order_intents SET intent_id = intent_id",
	} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin %s denied order operation: %v", role, err)
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
				"role %s operation %q error = %v, want SQLSTATE 42501",
				role,
				statement,
				statementErr,
			)
		}
	}
}

func cleanupAdminFleetOrdersHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerID string,
	roleID string,
) error {
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		DO $cleanup$
		DECLARE
			relation_name text;
			column_name name;
		BEGIN
			FOREACH relation_name IN ARRAY ARRAY[
				'trading.orders',
				'trading.order_intents'
			]
			LOOP
				IF pg_catalog.to_regclass(relation_name) IS NOT NULL THEN
					EXECUTE pg_catalog.format(
						'REVOKE ALL PRIVILEGES ON TABLE %%s FROM %[1]s CASCADE',
						pg_catalog.to_regclass(relation_name)
					);
					FOR column_name IN
						SELECT attribute.attname
						  FROM pg_catalog.pg_attribute AS attribute
						 WHERE attribute.attrelid =
						       pg_catalog.to_regclass(relation_name)
						   AND attribute.attnum > 0
						   AND NOT attribute.attisdropped
					LOOP
						EXECUTE pg_catalog.format(
							'REVOKE ALL PRIVILEGES (%%I) ON TABLE %%s FROM %[1]s CASCADE',
							column_name,
							pg_catalog.to_regclass(relation_name)
						);
					END LOOP;
				END IF;
			END LOOP;
		END
		$cleanup$`,
		roleID,
	)); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		REVOKE ALL PRIVILEGES ON SCHEMA trading FROM %[2]s CASCADE;
		DROP OWNED BY %[2]s CASCADE`,
		ownerID,
		roleID,
	)); err != nil {
		return err
	}
	_, err := pool.Exec(ctx, "DROP ROLE "+roleID)
	return err
}
