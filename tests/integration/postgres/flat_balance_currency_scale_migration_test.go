package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	flatBalancePreviousMigration = "20260727000400_phase3_broker_echo_replay_guards.up.sql"
	flatBalanceScaleMigration    = "20260728000100_phase3_flat_balance_currency_scale_read.up.sql"
)

func TestFlatBalanceCurrencyScaleReadUpgradesPopulatedCurrentMain(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalancePreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current-main schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, 0, 0
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:scale-upgrade', 'NETTING');
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:scale-upgrade', 'USDC',
			1000, 1, 999, 1000, 7
		)`); err != nil {
		t.Fatalf("seed populated current-main schema: %v", err)
	}
	assertFlatBalanceScaleQueryDenied(t, pool, "platformgo_api")
	beforeBalance, beforeScale, balanceFileNode, scaleFileNode :=
		readFlatBalanceMigrationState(t, pool)

	migrator := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalanceScaleMigration),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("upgrade flat-balance scale read: %v", err)
	}
	assertFlatBalanceScaleQuery(t, pool, "platformgo_api", "USDC", 2)
	afterBalance, afterScale, afterBalanceFileNode, afterScaleFileNode :=
		readFlatBalanceMigrationState(t, pool)
	if beforeBalance != afterBalance ||
		beforeScale != afterScale ||
		balanceFileNode != afterBalanceFileNode ||
		scaleFileNode != afterScaleFileNode {
		t.Fatalf(
			"ACL-only upgrade changed data or relation files: before %q/%q/%d/%d after %q/%q/%d/%d",
			beforeBalance,
			beforeScale,
			balanceFileNode,
			scaleFileNode,
			afterBalance,
			afterScale,
			afterBalanceFileNode,
			afterScaleFileNode,
		)
	}
	raw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"..",
		"migrations",
		flatBalanceScaleMigration,
	))
	if err != nil {
		t.Fatal(err)
	}
	wantChecksum := sha256.Sum256(raw)
	var storedChecksum []byte
	if err := pool.QueryRow(ctx, `
		SELECT checksum
		  FROM engine.schema_migrations
		 WHERE filename = $1`,
		flatBalanceScaleMigration,
	).Scan(&storedChecksum); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(storedChecksum, wantChecksum[:]) {
		t.Fatalf("migration checksum = %x, want %x", storedChecksum, wantChecksum)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify upgraded migration history: %v", err)
	}
	previousBinary := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalancePreviousMigration),
	)
	if err := previousBinary.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf(
			"previous binary schema verification = %v, want ErrDatabaseSchemaAhead",
			err,
		)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("idempotent migration retry: %v", err)
	}
	var (
		migrationCount int
		lastMigration  string
	)
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename)
		  FROM engine.schema_migrations`,
	).Scan(&migrationCount, &lastMigration); err != nil {
		t.Fatalf("inspect flat-balance migration history: %v", err)
	}
	if migrationCount != 27 ||
		lastMigration != flatBalanceScaleMigration {
		t.Fatalf(
			"flat-balance migration history count=%d last=%q",
			migrationCount,
			lastMigration,
		)
	}
}

func TestFlatBalanceCurrencyScaleReadScrubsHostileACLs(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(
			t,
			"20260725000700_phase3_engine_identity_schema_access.up.sql",
		),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply schema before currency registry: %v", err)
	}
	var migrationOwner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&migrationOwner); err != nil {
		t.Fatal(err)
	}
	hostileRole := fmt.Sprintf("flat_balance_hostile_%d", os.Getpid())
	dependentRole := fmt.Sprintf("flat_balance_dependent_%d", os.Getpid())
	ownerIdentifier := pgx.Identifier{migrationOwner}.Sanitize()
	hostileIdentifier := pgx.Identifier{hostileRole}.Sanitize()
	dependentIdentifier := pgx.Identifier{dependentRole}.Sanitize()
	roleTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := roleTx.Exec(ctx, "CREATE ROLE "+hostileIdentifier+" NOLOGIN"); err != nil {
		_ = roleTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := roleTx.Exec(ctx, "CREATE ROLE "+dependentIdentifier+" NOLOGIN"); err != nil {
		_ = roleTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := roleTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cleaned := false
	t.Cleanup(func() {
		if cleaned {
			return
		}
		if err := cleanupFlatBalanceHostileRole(
			context.Background(),
			pool,
			ownerIdentifier,
			dependentIdentifier,
		); err != nil {
			t.Errorf("cleanup dependent ACL fixture: %v", err)
		}
		if err := cleanupFlatBalanceHostileRole(
			context.Background(),
			pool,
			ownerIdentifier,
			hostileIdentifier,
		); err != nil {
			t.Errorf("cleanup hostile ACL fixture: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA trading TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s`,
		ownerIdentifier,
		hostileIdentifier,
	)); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalancePreviousMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply current-main schema under hostile defaults: %v", err)
	}
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.currency_scales TO %[1]s
			WITH GRANT OPTION;
		GRANT UPDATE (scale) ON trading.currency_scales TO %[1]s
			WITH GRANT OPTION`,
		hostileIdentifier,
	)); err != nil {
		t.Fatal(err)
	}
	grantTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := grantTx.Exec(
		ctx,
		"SET LOCAL ROLE "+hostileIdentifier,
	); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := grantTx.Exec(ctx, fmt.Sprintf(`
		GRANT SELECT ON trading.currency_scales TO %[1]s;
		GRANT UPDATE (scale) ON trading.currency_scales TO %[1]s`,
		dependentIdentifier,
	)); err != nil {
		_ = grantTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := grantTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var hostileInsert, hostileColumnUpdate, dependentSelect, dependentUpdate bool
	if err := pool.QueryRow(ctx, `
		SELECT
			has_table_privilege($1, 'trading.currency_scales', 'INSERT'),
			has_column_privilege($1, 'trading.currency_scales', 'scale', 'UPDATE'),
			has_table_privilege($2, 'trading.currency_scales', 'SELECT'),
			has_column_privilege($2, 'trading.currency_scales', 'scale', 'UPDATE')`,
		hostileRole,
		dependentRole,
	).Scan(
		&hostileInsert,
		&hostileColumnUpdate,
		&dependentSelect,
		&dependentUpdate,
	); err != nil {
		t.Fatal(err)
	}
	if !hostileInsert || !hostileColumnUpdate || !dependentSelect ||
		!dependentUpdate {
		t.Fatalf(
			"hostile grant chain did not reproduce table/column ACLs",
		)
	}

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalanceScaleMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply currency-scale ACL scrub: %v", err)
	}
	assertFlatBalanceRawACLAllowlist(t, pool)
	assertFlatBalanceScaleQuery(t, pool, "platformgo_api", "", 0)
	assertFlatBalanceScaleQuery(t, pool, "platformgo_engine", "", 0)
	for _, role := range []string{
		hostileRole,
		dependentRole,
		"platformgo_outbox",
		"platformgo_projector",
		"platformgo_realtime",
		"platformgo_realtime_repair",
	} {
		assertFlatBalanceScaleQueryDenied(t, pool, role)
	}
	for _, role := range []string{
		"platformgo_api",
		"platformgo_engine",
		hostileRole,
		dependentRole,
	} {
		assertFlatBalanceScaleMutationDenied(t, pool, role)
	}

	if err := cleanupFlatBalanceHostileRole(
		ctx,
		pool,
		ownerIdentifier,
		dependentIdentifier,
	); err != nil {
		t.Fatal(err)
	}
	if err := cleanupFlatBalanceHostileRole(
		ctx,
		pool,
		ownerIdentifier,
		hostileIdentifier,
	); err != nil {
		t.Fatal(err)
	}
	cleaned = true
}

func TestFlatBalanceCurrencyScaleReadLockTimeoutRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalancePreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	beforeACL := readFlatBalanceRawACLState(t, pool)
	var engineCouldSelectBefore bool
	if err := pool.QueryRow(ctx, `
		SELECT has_table_privilege(
			'platformgo_engine',
			'trading.currency_scales',
			'SELECT'
		)`,
	).Scan(&engineCouldSelectBefore); err != nil {
		t.Fatal(err)
	}
	if !engineCouldSelectBefore {
		t.Fatal("previous schema unexpectedly denied engine currency-scale SELECT")
	}
	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.currency_scales IN ACCESS EXCLUSIVE MODE",
	); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatal(err)
	}
	migrator := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalanceScaleMigration),
	)
	err = migrator.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf("contended migration error = %v, want SQLSTATE 55P03", err)
	}
	var journaled, apiCanSelect, engineCanSelect bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.schema_migrations WHERE filename = $1
			),
			has_table_privilege(
				'platformgo_api',
				'trading.currency_scales',
				'SELECT'
			),
			has_table_privilege(
				'platformgo_engine',
				'trading.currency_scales',
				'SELECT'
			)`,
		flatBalanceScaleMigration,
	).Scan(&journaled, &apiCanSelect, &engineCanSelect); err != nil {
		_ = lockingTx.Rollback(ctx)
		t.Fatal(err)
	}
	afterFailedACL := readFlatBalanceRawACLState(t, pool)
	if journaled || apiCanSelect || !engineCanSelect ||
		afterFailedACL != beforeACL {
		_ = lockingTx.Rollback(ctx)
		t.Fatalf(
			"failed migration changed journal/API/engine/raw ACL = %t/%t/%t/%t",
			journaled,
			apiCanSelect,
			engineCanSelect,
			afterFailedACL == beforeACL,
		)
	}
	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended migration: %v", err)
	}
	assertFlatBalanceScaleQuery(t, pool, "platformgo_api", "", 0)
}

func TestFlatBalanceCurrencyScaleReadDoesNotBlockOrdinaryDML(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalancePreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	dmlTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dmlTx.Exec(ctx, `
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('ZZW', 2)`); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, flatBalanceScaleMigration),
	).Migrate(ctx); err != nil {
		_ = dmlTx.Rollback(ctx)
		t.Fatalf("ordinary DML blocked ACL-only migration: %v", err)
	}
	if err := dmlTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertFlatBalanceScaleQuery(t, pool, "platformgo_api", "", 0)
}

func readFlatBalanceMigrationState(
	t *testing.T,
	pool *pgxpool.Pool,
) (string, string, uint32, uint32) {
	t.Helper()
	var (
		balance     string
		scale       string
		balanceNode uint32
		scaleNode   uint32
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(
				SELECT account_id || '|' || currency || '|' ||
					trim_scale(total)::text || '|' ||
					trim_scale(used)::text || '|' ||
					trim_scale(free)::text || '|' ||
					trim_scale(equity)::text || '|' ||
					ledger_sequence::text
				  FROM ledger.balances
				 WHERE account_id = 'urn:xb:account:scale-upgrade'
			),
			(
				SELECT currency || '|' || scale::text
				  FROM trading.currency_scales
				 WHERE currency = 'USDC'
			),
			pg_relation_filenode('ledger.balances'::regclass),
			pg_relation_filenode('trading.currency_scales'::regclass)`,
	).Scan(&balance, &scale, &balanceNode, &scaleNode); err != nil {
		t.Fatal(err)
	}
	return balance, scale, balanceNode, scaleNode
}

func readFlatBalanceRawACLState(
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
		 WHERE relation.oid = 'trading.currency_scales'::pg_catalog.regclass`,
	).Scan(&tableACL, &columnACL); err != nil {
		t.Fatal(err)
	}
	return tableACL + "\n" + columnACL
}

func assertFlatBalanceScaleQuery(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	wantCurrency string,
	wantScale int16,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if wantCurrency == "" {
		var allowed bool
		if err := tx.QueryRow(context.Background(), `
			SELECT has_table_privilege(
				current_user,
				'trading.currency_scales',
				'SELECT'
			)`,
		).Scan(&allowed); err != nil {
			t.Fatal(err)
		}
		if !allowed {
			t.Fatalf("role %s lacks currency-scale SELECT", role)
		}
		return
	}
	var currency string
	var scale int16
	if err := tx.QueryRow(context.Background(), `
		SELECT currency, scale
		  FROM trading.currency_scales
		 WHERE currency = $1`,
		wantCurrency,
	).Scan(&currency, &scale); err != nil {
		t.Fatalf("role %s read currency scale: %v", role, err)
	}
	if currency != wantCurrency || scale != wantScale {
		t.Fatalf("role %s scale row = %s/%d", role, currency, scale)
	}
}

func assertFlatBalanceScaleQueryDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
	); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if _, err := tx.Exec(
		context.Background(),
		"SELECT currency, scale FROM trading.currency_scales",
	); err == nil {
		t.Fatalf("role %s unexpectedly read currency scales", role)
	} else {
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
			t.Fatalf("role %s query error = %v, want 42501", role, err)
		}
	}
}

func assertFlatBalanceScaleMutationDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
) {
	t.Helper()
	for _, statement := range []string{
		"INSERT INTO trading.currency_scales (currency, scale) VALUES ('ZZX', 2)",
		"UPDATE trading.currency_scales SET scale = scale",
		"DELETE FROM trading.currency_scales",
		"TRUNCATE trading.currency_scales",
	} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(
			context.Background(),
			"SET LOCAL ROLE "+pgx.Identifier{role}.Sanitize(),
		); err != nil {
			_ = tx.Rollback(context.Background())
			t.Fatal(err)
		}
		_, statementErr := tx.Exec(context.Background(), statement)
		_ = tx.Rollback(context.Background())
		if statementErr == nil {
			t.Fatalf("role %s executed %q", role, statement)
		}
	}
}

func assertFlatBalanceRawACLAllowlist(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		WITH relation AS (
			SELECT oid, relowner, relacl
			  FROM pg_class
			 WHERE oid = 'trading.currency_scales'::regclass
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
			  CROSS JOIN LATERAL aclexplode(
				COALESCE(
					relation.relacl,
					acldefault('r', relation.relowner)
				)
			  ) AS privilege
			  LEFT JOIN pg_roles AS role ON role.oid = privilege.grantee
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
			  JOIN pg_attribute AS attribute
			    ON attribute.attrelid = relation.oid
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			  CROSS JOIN LATERAL aclexplode(
				attribute.attacl
			  ) AS privilege
			  LEFT JOIN pg_roles AS role ON role.oid = privilege.grantee
			 WHERE attribute.attacl IS NOT NULL
		)
		SELECT grantee, privilege_type, is_grantable, scope
		  FROM (
			SELECT * FROM table_acl
			UNION ALL
			SELECT * FROM column_acl
		  ) AS acl
		  JOIN relation ON true
		 WHERE acl.grantee IS DISTINCT FROM (
			SELECT rolname FROM pg_roles WHERE oid = relation.relowner
		 )
		 ORDER BY scope, grantee, privilege_type`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var grantee, privilege, scope string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &scope); err != nil {
			t.Fatal(err)
		}
		got = append(
			got,
			fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, scope),
		)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"platformgo_api|SELECT|false|table",
		"platformgo_engine|SELECT|false|table",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("currency-scale raw ACL = %v, want %v", got, want)
	}
}

func cleanupFlatBalanceHostileRole(
	ctx context.Context,
	pool *pgxpool.Pool,
	ownerIdentifier string,
	hostileIdentifier string,
) error {
	_, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s IN SCHEMA trading
			REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
		DROP OWNED BY %[2]s CASCADE;
		DROP ROLE %[2]s`,
		ownerIdentifier,
		hostileIdentifier,
	))
	return err
}

func equalBytes(left []byte, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
