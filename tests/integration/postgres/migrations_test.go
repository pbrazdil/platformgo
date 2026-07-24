package postgres_test

import (
	"context"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInitialMigrationCreatesDurableExecutionSchema(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrations := os.DirFS(filepath.Join("..", "..", "..", "migrations"))
	migrator := platformpostgres.NewMigrator(pool, migrations)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, relation := range []string{
		"engine.schema_migrations",
		"engine.shard_checkpoints",
		"engine.input_receipts",
		"engine.duplicate_delivery_receipts",
		"engine.shard_faults",
		"trading.idempotency_records",
		"trading.commands",
		"trading.orders",
		"trading.fills",
		"trading.positions",
		"market.books",
		"ledger.transactions",
		"ledger.entries",
		"ledger.balances",
		"messaging.outbox",
		"messaging.inbox",
	} {
		var exists bool
		if err := pool.QueryRow(
			context.Background(),
			"SELECT to_regclass($1) IS NOT NULL",
			relation,
		).Scan(&exists); err != nil {
			t.Fatalf("inspect %s: %v", relation, err)
		}
		if !exists {
			t.Fatalf("relation %s does not exist", relation)
		}
	}

	assertReceiptIdentityConstraints(t, pool)
	assertLedgerBalanceConstraint(t, pool)
	assertImmutableLedgerFacts(t, pool)
	assertAPIRoleCannotMutateEconomicTables(t, pool)
}

func TestMigratorEnforcesMinimumPostgresVersion(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var versionNumber int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_setting('server_version_num')::integer",
	).Scan(&versionNumber); err != nil {
		t.Fatalf("read PostgreSQL version: %v", err)
	}

	err := platformpostgres.NewMigrator(pool, fstest.MapFS{}).
		Migrate(context.Background())
	majorVersion := versionNumber / 10000
	if majorVersion < platformpostgres.MinimumPostgresMajorVersion {
		if !errors.Is(err, platformpostgres.ErrUnsupportedPostgresVersion) {
			t.Fatalf(
				"PostgreSQL %d migration error = %v, want ErrUnsupportedPostgresVersion",
				majorVersion,
				err,
			)
		}
		return
	}
	if err != nil {
		t.Fatalf("PostgreSQL %d migration error = %v, want nil", majorVersion, err)
	}
}

func TestMigratorTracksChecksumsAndRejectsHistoryDrift(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrations := fstest.MapFS{
		"20260724000100_test.up.sql": {
			Data: []byte("CREATE SCHEMA migration_probe;"),
		},
	}
	migrator := platformpostgres.NewMigrator(pool, migrations)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("idempotent Migrate: %v", err)
	}

	var checksum []byte
	if err := pool.QueryRow(
		context.Background(),
		`SELECT checksum
		   FROM engine.schema_migrations
		  WHERE filename = $1`,
		"20260724000100_test.up.sql",
	).Scan(&checksum); err != nil {
		t.Fatalf("read migration checksum: %v", err)
	}
	if got := hex.EncodeToString(checksum); len(got) != 64 {
		t.Fatalf("checksum = %q, want 64 hex characters", got)
	}

	changed := fstest.MapFS{
		"20260724000100_test.up.sql": {
			Data: []byte("CREATE SCHEMA migration_probe_changed;"),
		},
	}
	err := platformpostgres.NewMigrator(pool, changed).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrMigrationChecksumMismatch) {
		t.Fatalf("changed migration error = %v, want ErrMigrationChecksumMismatch", err)
	}

	err = platformpostgres.NewMigrator(pool, fstest.MapFS{}).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrDatabaseSchemaAhead) {
		t.Fatalf("missing applied migration error = %v, want ErrDatabaseSchemaAhead", err)
	}
}

func TestMigratorUpgradesDurableExecutionFoundationForwardOnly(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	baselineName := "20260724000100_durable_execution_foundation.up.sql"
	baseline, err := os.ReadFile(filepath.Join(migrationDirectory, baselineName))
	if err != nil {
		t.Fatalf("read baseline migration: %v", err)
	}
	if err := platformpostgres.NewMigrator(pool, fstest.MapFS{
		baselineName: {Data: baseline},
	}).Migrate(context.Background()); err != nil {
		t.Fatalf("apply baseline migration: %v", err)
	}
	var faultTableBefore bool
	if err := pool.QueryRow(
		context.Background(),
		"SELECT to_regclass('engine.shard_faults') IS NOT NULL",
	).Scan(&faultTableBefore); err != nil {
		t.Fatalf("inspect pre-upgrade schema: %v", err)
	}
	if faultTableBefore {
		t.Fatal("future shard fault table exists before forward upgrade")
	}

	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("upgrade durable schema: %v", err)
	}
	var faultTableAfter bool
	var migrationCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT to_regclass('engine.shard_faults') IS NOT NULL",
	).Scan(&faultTableAfter); err != nil {
		t.Fatalf("inspect upgraded schema: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count applied migrations: %v", err)
	}
	if !faultTableAfter || migrationCount != 8 {
		t.Fatalf(
			"upgraded schema = fault table %t, migrations %d, want true and 8",
			faultTableAfter,
			migrationCount,
		)
	}
}

func assertAPIRoleCannotMutateEconomicTables(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var canInsertCommand bool
	var canUpdateBalance bool
	if err := pool.QueryRow(context.Background(), `
		SELECT
			has_table_privilege(
				'platformgo_api',
				'trading.commands',
				'INSERT'
			),
			has_table_privilege(
				'platformgo_api',
				'ledger.balances',
				'UPDATE'
			)`,
	).Scan(&canInsertCommand, &canUpdateBalance); err != nil {
		t.Fatalf("inspect API role privileges: %v", err)
	}
	if !canInsertCommand || canUpdateBalance {
		t.Fatalf(
			"API privileges = insert command %t update balance %t",
			canInsertCommand,
			canUpdateBalance,
		)
	}
}

func postgresPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is required for PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func resetDurableSchemas(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	_, err := pool.Exec(
		context.Background(),
		`DROP SCHEMA IF EXISTS market, messaging, ledger, trading, engine CASCADE`,
	)
	if err != nil {
		t.Fatalf("reset durable schemas: %v", err)
	}
}

func assertReceiptIdentityConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	const insertReceipt = `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (7, $1, $2, 1, 1, decode(repeat('01', 32), 'hex'),
		          decode(repeat('04', 32), 'hex'), 1, 2,
		          decode(repeat('02', 32), 'hex'), decode(repeat('03', 32), 'hex'),
		          '{}'::jsonb, '{}'::jsonb)`
	inputID := "019f9460-4b36-4e9b-8f44-682611f7ee01"
	if _, err := pool.Exec(context.Background(), insertReceipt, inputID, 1); err != nil {
		t.Fatalf("insert first receipt: %v", err)
	}
	if _, err := pool.Exec(context.Background(), insertReceipt, inputID, 2); !isUniqueViolation(err) {
		t.Fatalf("duplicate input ID error = %v, want unique violation", err)
	}
	otherID := "019f9460-4b36-4e9b-8f44-682611f7ee02"
	if _, err := pool.Exec(context.Background(), insertReceipt, otherID, 1); !isUniqueViolation(err) {
		t.Fatalf("duplicate stream sequence error = %v, want unique violation", err)
	}
}

func assertLedgerBalanceConstraint(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin unbalanced ledger transaction: %v", err)
	}
	_, err = tx.Exec(
		context.Background(),
		`INSERT INTO ledger.transactions (transaction_id, business_key, input_id, logical_time)
		 VALUES ($1, 'unbalanced', $2, '2026-07-24T10:00:00Z')`,
		"019f9460-4b36-4e9b-8f44-682611f7ee10",
		"019f9460-4b36-4e9b-8f44-682611f7ee01",
	)
	if err == nil {
		_, err = tx.Exec(
			context.Background(),
			`INSERT INTO ledger.entries (
				entry_id, transaction_id, account_id, currency, amount
			 ) VALUES ($1, $2, 'account-1', 'USDC', 1)`,
			"019f9460-4b36-4e9b-8f44-682611f7ee11",
			"019f9460-4b36-4e9b-8f44-682611f7ee10",
		)
	}
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("prepare unbalanced ledger transaction: %v", err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("unbalanced ledger transaction committed")
	}
}

func assertImmutableLedgerFacts(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	transactionID := "019f9460-4b36-4e9b-8f44-682611f7ee20"
	_, err := pool.Exec(
		context.Background(),
		`INSERT INTO ledger.transactions (transaction_id, business_key, input_id, logical_time)
		 VALUES ($1, 'balanced', $2, '2026-07-24T10:00:00Z')`,
		transactionID,
		"019f9460-4b36-4e9b-8f44-682611f7ee01",
	)
	if err == nil {
		_, err = pool.Exec(
			context.Background(),
			`INSERT INTO ledger.entries (
				entry_id, transaction_id, account_id, currency, amount
			 ) VALUES
			 ($1, $2, 'account-1', 'USDC', 1),
			 ($3, $2, 'system:clearing', 'USDC', -1)`,
			"019f9460-4b36-4e9b-8f44-682611f7ee21",
			transactionID,
			"019f9460-4b36-4e9b-8f44-682611f7ee22",
		)
	}
	if err != nil {
		t.Fatalf("insert balanced ledger facts: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		`UPDATE ledger.entries SET amount = 2 WHERE transaction_id = $1`,
		transactionID,
	); err == nil {
		t.Fatal("ledger entry update succeeded")
	}
	if _, err := pool.Exec(
		context.Background(),
		`DELETE FROM ledger.transactions WHERE transaction_id = $1`,
		transactionID,
	); err == nil {
		t.Fatal("ledger transaction delete succeeded")
	}
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
