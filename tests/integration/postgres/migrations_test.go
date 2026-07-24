package postgres_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"

	"github.com/jackc/pgx/v5"
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
		"engine.account_shards",
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
	assertSingleBaselineMigration(t, pool)
	assertLedgerBalanceConstraint(t, pool)
	assertImmutableLedgerFacts(t, pool)
	assertAPIRoleCannotMutateEconomicTables(t, pool)
}

func TestFinalBaselineAcceptsRepresentativePopulatedGraph(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err := pool.Exec(context.Background(), `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-USDC', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('account-netting', 'NETTING'), ('account-hedging', 'HEDGING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-netting', 7), ('account-hedging', 8);
		INSERT INTO trading.risk_configs (
			account_id, instrument_id, margin_mode, leverage
		) VALUES
			('account-netting', 'BTC-USDC', 'CROSS', 5),
			('account-hedging', 'BTC-USDC', 'ISOLATED', 2);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, limit_price, triggered, reduce_only,
			has_rested, has_slippage_band, max_slippage_bps,
			slippage_reference, version
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70001',
			 'account-netting', 'BTC-USDC', 'BUY', 'LIMIT', 'GTC',
			 'WORKING', 2.000, 1.000, 100.00, 100.00, false, false,
			 true, true, 25, 99.50, 1),
			('019f9460-4b36-4e9b-8f44-682611f70002',
			 'account-hedging', 'BTC-USDC', 'SELL', 'MARKET', 'IOC',
			 'FILLED', 1.000, 1.000, 101.00, NULL, false, false,
			 false, false, 0, NULL, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70011',
			 '019f9460-4b36-4e9b-8f44-682611f70001',
			 '019f9460-4b36-4e9b-8f44-682611f70021',
			 'account-netting', 'BTC-USDC', 'BUY', 100.00, 1.000,
			 '019f9460-4b36-4e9b-8f44-682611f70031', 'OPEN',
			 0.00, 'USDC', 'MAKER', -0.01, 'USDC',
			 '2026-07-24T12:00:00Z'),
			('019f9460-4b36-4e9b-8f44-682611f70012',
			 '019f9460-4b36-4e9b-8f44-682611f70002',
			 '019f9460-4b36-4e9b-8f44-682611f70022',
			 'account-hedging', 'BTC-USDC', 'SELL', 101.00, 1.000,
			 '019f9460-4b36-4e9b-8f44-682611f70032', 'OPEN',
			 NULL, NULL, 'TAKER', NULL, NULL,
			 '2026-07-24T12:00:01Z');
		INSERT INTO trading.positions (
			position_id, account_id, instrument_id, side, status,
			signed_quantity, average_open_price, realized_pnl,
			settlement_currency, margin_mode, isolated_collateral, version
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70031',
			 'account-netting', 'BTC-USDC', 'LONG', 'OPEN',
			 1.000, 100.00, 0.00, 'USDC', 'CROSS', 0.00, 1),
			('019f9460-4b36-4e9b-8f44-682611f70032',
			 'account-hedging', 'BTC-USDC', 'SHORT', 'OPEN',
			 -1.000, 101.00, 0.00, 'USDC', 'ISOLATED', 50.50, 1),
			('019f9460-4b36-4e9b-8f44-682611f70033',
			 'account-netting', 'BTC-USDC', 'FLAT', 'CLOSED',
			 0.000, 0.00, 2.00, 'USDC', 'CROSS', 0.00, 1);
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70041',
			'baseline-balanced', '019f9460-4b36-4e9b-8f44-682611f70021',
			'2026-07-24T12:00:00Z'
		);
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70042',
			 '019f9460-4b36-4e9b-8f44-682611f70041',
			 'account-netting', 'USDC', 10.00),
			('019f9460-4b36-4e9b-8f44-682611f70043',
			 '019f9460-4b36-4e9b-8f44-682611f70041',
			 'system:clearing', 'USDC', -10.00);
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES ('account-netting', 'USDC', 10.00, 2.00, 8.00, 10.00, 1);
		INSERT INTO market.books (
			instrument_id, mark_price, bids, asks, stream_sequence
		) VALUES ('BTC-USDC', 100.50, '[]', '[]', 1);
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES
			(7, 3, true, decode(repeat('11', 32), 'hex'), '{}'),
			(8, 2, false, decode(repeat('12', 32), 'hex'), '{}');
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, business_input_hash_version, business_input_hash,
			resulting_state_hash, envelope, decision
		) VALUES
			(7, '019f9460-4b36-4e9b-8f44-682611f70021', 1, 1,
			 1, decode(repeat('21', 32), 'hex'), 1,
			 decode(repeat('31', 32), 'hex'), 1,
			 decode(repeat('41', 32), 'hex'),
			 decode(repeat('51', 32), 'hex'), '{}', '{}'),
			(7, '019f9460-4b36-4e9b-8f44-682611f70022', 2, 1,
			 1, decode(repeat('22', 32), 'hex'), 1,
			 decode(repeat('32', 32), 'hex'), 1,
			 decode(repeat('42', 32), 'hex'),
			 decode(repeat('52', 32), 'hex'), '{}', '{}'),
			(8, '019f9460-4b36-4e9b-8f44-682611f70023', 1, 1,
			 1, decode(repeat('23', 32), 'hex'), 1,
			 decode(repeat('33', 32), 'hex'), 1,
			 decode(repeat('43', 32), 'hex'),
			 decode(repeat('53', 32), 'hex'), '{}', '{}');
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES (
			8, decode(repeat('61', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70024', 2,
			'durable_conflict', 'fixture', '{}', decode('00', 'hex')
		);
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES (
			7, 3, '019f9460-4b36-4e9b-8f44-682611f70021',
			decode(repeat('21', 32), 'hex'),
			decode(repeat('31', 32), 'hex'),
			decode(repeat('71', 32), 'hex'),
			decode(repeat('51', 32), 'hex'), '{}', '{}'
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:account-netting', 'baseline-command',
			decode(repeat('81', 32), 'hex'),
			'019f9460-4b36-4e9b-8f44-682611f70051',
			'in_progress', '2026-07-25T12:00:00Z'
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'019f9460-4b36-4e9b-8f44-682611f70051',
			'account-netting', 1, 'adjust_balance', 1, '{}',
			'pending', '2026-07-24T12:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload,
			published_at, publish_sequence
		) VALUES
			('019f9460-4b36-4e9b-8f44-682611f70051',
			 'engine.input.7.command.v1', 1, '{}', NULL, NULL),
			('019f9460-4b36-4e9b-8f44-682611f70052',
			 'domain.v1.fixture', 1, '{}', clock_timestamp(), 1);
		INSERT INTO messaging.inbox (consumer, message_id)
		VALUES ('baseline-projector',
			'019f9460-4b36-4e9b-8f44-682611f70052');
	`)
	if err != nil {
		t.Fatalf("populate final baseline graph: %v", err)
	}

	for relation, want := range map[string]int{
		"engine.account_shards":              2,
		"engine.input_receipts":              3,
		"engine.shard_checkpoints":           2,
		"engine.shard_faults":                1,
		"engine.duplicate_delivery_receipts": 1,
		"trading.accounts":                   2,
		"trading.orders":                     2,
		"trading.fills":                      2,
		"trading.positions":                  3,
		"ledger.transactions":                1,
		"ledger.entries":                     2,
		"messaging.outbox":                   2,
		"messaging.inbox":                    1,
	} {
		var count int
		query := "SELECT count(*) FROM " + relation
		if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", relation, err)
		}
		if count != want {
			t.Fatalf("%s rows = %d, want %d", relation, count, want)
		}
	}
	if _, err := pool.Exec(
		context.Background(),
		"INSERT INTO trading.accounts (account_id, oms_mode) VALUES ('lowercase', 'netting')",
	); err == nil {
		t.Fatal("former lowercase enum spelling was accepted")
	}
}

func TestFinalBaselineRuntimeRolesEnforceTransactionOwnership(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	apiTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin API transaction: %v", err)
	}
	if _, err = apiTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err == nil {
		_, err = apiTransaction.Exec(context.Background(), `
			INSERT INTO engine.account_shards (account_id, shard_id)
			VALUES ('role-account', 9);
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id,
				state, expires_at
			) VALUES (
				'account:role-account', 'role-command',
				decode(repeat('91', 32), 'hex'),
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'in_progress', '2026-07-25T12:00:00Z'
			);
			INSERT INTO trading.commands (
				command_id, account_id, account_sequence, command_type,
				schema_version, canonical_payload, status, logical_time
			) VALUES (
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'role-account', 1, 'adjust_balance', 1, '{}',
				'pending', '2026-07-24T12:00:00Z'
			);
			INSERT INTO messaging.outbox (
				message_id, subject, schema_version, payload
			) VALUES (
				'019f9460-4b36-4e9b-8f44-682611f70101',
				'engine.input.9.command.v1', 1, '{}'
			)`)
	}
	if err != nil {
		_ = apiTransaction.Rollback(context.Background())
		t.Fatalf("API command transaction: %v", err)
	}
	if err := apiTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit API transaction: %v", err)
	}

	assertRoleStatementDenied(t, pool, "platformgo_api", `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES ('role-account', 'USDC', 1, 0, 1, 1, 1)`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		UPDATE trading.commands
		   SET status = 'completed', completed_at = clock_timestamp()
		 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	assertRoleStatementDenied(t, pool, "platformgo_api", `
		UPDATE trading.idempotency_records
		   SET request_hash = decode(repeat('ff', 32), 'hex')
		 WHERE scope = 'account:role-account'
		   AND idempotency_key = 'role-command'`)

	engineTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin engine transaction: %v", err)
	}
	if _, err = engineTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_engine",
	); err == nil {
		_, err = engineTransaction.Exec(context.Background(), `
			INSERT INTO engine.shard_checkpoints (
				shard_id, next_stream_sequence, ready, state_hash, state_snapshot
			) VALUES (
				9, 2, true, decode(repeat('92', 32), 'hex'), '{}'
			);
			INSERT INTO engine.input_receipts (
				shard_id, input_id, stream_sequence, schema_version,
				input_hash_version, input_hash, decision_hash_version,
				decision_hash, business_input_hash_version,
				business_input_hash, resulting_state_hash, envelope, decision
			) VALUES (
				9, '019f9460-4b36-4e9b-8f44-682611f70101', 1, 1,
				1, decode(repeat('93', 32), 'hex'), 1,
				decode(repeat('94', 32), 'hex'), 1,
				decode(repeat('95', 32), 'hex'),
				decode(repeat('92', 32), 'hex'), '{}', '{}'
			);
			UPDATE trading.commands
			   SET status = 'completed', result = '{}',
			       completed_at = '2026-07-24T12:00:01Z'
			 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	}
	if err != nil {
		_ = engineTransaction.Rollback(context.Background())
		t.Fatalf("engine decision transaction: %v", err)
	}
	if err := engineTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit engine transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		UPDATE trading.commands
		   SET canonical_payload = '{"tampered":true}'
		 WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	assertRoleStatementDenied(t, pool, "platformgo_engine", `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('engine-squatted-account', 10)`)

	apiCompletion, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin API completion transaction: %v", err)
	}
	if _, err = apiCompletion.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_api",
	); err == nil {
		_, err = apiCompletion.Exec(context.Background(), `
			UPDATE trading.idempotency_records
			   SET state = 'completed',
			       response_status = 200,
			       response_headers = '{}',
			       response_body = decode('7b7d', 'hex')
			 WHERE scope = 'account:role-account'
			   AND idempotency_key = 'role-command'`)
	}
	if err != nil {
		_ = apiCompletion.Rollback(context.Background())
		t.Fatalf("API completion transaction: %v", err)
	}
	if err := apiCompletion.Commit(context.Background()); err != nil {
		t.Fatalf("commit API completion transaction: %v", err)
	}

	outboxTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin outbox transaction: %v", err)
	}
	if _, err = outboxTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_outbox",
	); err == nil {
		_, err = outboxTransaction.Exec(context.Background(), `
			UPDATE messaging.outbox
			   SET attempts = attempts + 1, claimed_at = clock_timestamp()
			 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)
	}
	if err != nil {
		_ = outboxTransaction.Rollback(context.Background())
		t.Fatalf("outbox claim transaction: %v", err)
	}
	if err := outboxTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit outbox transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_outbox", `
		UPDATE messaging.outbox
		   SET payload = '{"tampered":true}'
		 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)

	projectorTransaction, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin projector transaction: %v", err)
	}
	if _, err = projectorTransaction.Exec(
		context.Background(),
		"SET LOCAL ROLE platformgo_projector",
	); err == nil {
		_, err = projectorTransaction.Exec(context.Background(), `
			INSERT INTO messaging.inbox (consumer, message_id)
			VALUES (
				'role-projector',
				'019f9460-4b36-4e9b-8f44-682611f70101'
			)`)
	}
	if err != nil {
		_ = projectorTransaction.Rollback(context.Background())
		t.Fatalf("projector inbox transaction: %v", err)
	}
	if err := projectorTransaction.Commit(context.Background()); err != nil {
		t.Fatalf("commit projector transaction: %v", err)
	}
	assertRoleStatementDenied(t, pool, "platformgo_projector", `
		UPDATE messaging.outbox
		   SET attempts = attempts + 1
		 WHERE message_id = '019f9460-4b36-4e9b-8f44-682611f70101'`)

	var commandStatus string
	var receiptCount int
	var inboxCount int
	if err := pool.QueryRow(
		context.Background(),
		`SELECT status
		   FROM trading.commands
		  WHERE command_id = '019f9460-4b36-4e9b-8f44-682611f70101'`,
	).Scan(&commandStatus); err != nil {
		t.Fatalf("read role-owned command: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.input_receipts",
	).Scan(&receiptCount); err != nil {
		t.Fatalf("count role-owned receipts: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.inbox",
	).Scan(&inboxCount); err != nil {
		t.Fatalf("count role-owned inbox rows: %v", err)
	}
	if commandStatus != "completed" || receiptCount != 1 || inboxCount != 1 {
		t.Fatalf(
			"role-owned effects = command %s receipts %d inbox %d",
			commandStatus,
			receiptCount,
			inboxCount,
		)
	}
}

func TestFinalBaselineMigratesWithNoCreateRole(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	dropTestMigratorRole(t, pool)

	var databaseName string
	if err := pool.QueryRow(
		context.Background(),
		"SELECT current_database()",
	).Scan(&databaseName); err != nil {
		t.Fatalf("read test database name: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		CREATE ROLE platformgo_migrator_test
			LOGIN NOCREATEROLE PASSWORD 'platformgo-migrator-test'`); err != nil {
		t.Fatalf("create NOCREATEROLE migrator: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"GRANT CREATE ON DATABASE "+
			pgx.Identifier{databaseName}.Sanitize()+
			" TO platformgo_migrator_test",
	); err != nil {
		t.Fatalf("grant test database create to migrator: %v", err)
	}
	t.Cleanup(func() {
		dropDurableSchemas(t, pool)
		dropTestMigratorRole(t, pool)
	})

	config, err := pgxpool.ParseConfig(os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatalf("parse test PostgreSQL configuration: %v", err)
	}
	config.ConnConfig.User = "platformgo_migrator_test"
	config.ConnConfig.Password = "platformgo-migrator-test"
	migratorPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open NOCREATEROLE migrator pool: %v", err)
	}
	defer migratorPool.Close()
	if err := migratorPool.Ping(context.Background()); err != nil {
		t.Fatalf("ping as NOCREATEROLE migrator: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		migratorPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate as NOCREATEROLE role: %v", err)
	}

	var canCreateRole bool
	if err := pool.QueryRow(context.Background(), `
		SELECT rolcreaterole
		  FROM pg_roles
		 WHERE rolname = 'platformgo_migrator_test'`,
	).Scan(&canCreateRole); err != nil {
		t.Fatalf("inspect migrator role: %v", err)
	}
	if canCreateRole {
		t.Fatal("test migrator unexpectedly has CREATEROLE")
	}
	assertSingleBaselineMigration(t, pool)
}

func TestFinalBaselineFailsWhenPreprovisionedRuntimeRoleIsMissing(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	dropDurableSchemas(t, pool)
	if _, err := pool.Exec(
		context.Background(),
		"DROP ROLE platformgo_projector",
	); err != nil {
		t.Fatalf("remove required runtime role: %v", err)
	}
	t.Cleanup(func() {
		dropDurableSchemas(t, pool)
		if _, err := pool.Exec(
			context.Background(),
			"CREATE ROLE platformgo_projector NOLOGIN",
		); err != nil {
			t.Errorf("restore required runtime role: %v", err)
		}
	})

	err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background())
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) ||
		postgresError.Code != "42501" ||
		!strings.Contains(err.Error(), "pre-provisioned runtime role") {
		t.Fatalf(
			"missing runtime role migration error = %v, want clear SQLSTATE 42501",
			err,
		)
	}
	var appliedCount int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.schema_migrations",
	).Scan(&appliedCount); err != nil {
		t.Fatalf("count migrations after prerequisite failure: %v", err)
	}
	if appliedCount != 0 {
		t.Fatalf("prerequisite failure recorded %d migrations", appliedCount)
	}
}

func TestFinalBaselineRejectsUnsafeRuntimeRoleAttributes(t *testing.T) {
	for _, test := range []struct {
		name    string
		unsafe  string
		restore string
	}{
		{
			name:    "login capability",
			unsafe:  "ALTER ROLE platformgo_projector LOGIN",
			restore: "ALTER ROLE platformgo_projector NOLOGIN",
		},
		{
			name:    "role creation capability",
			unsafe:  "ALTER ROLE platformgo_projector CREATEROLE",
			restore: "ALTER ROLE platformgo_projector NOCREATEROLE",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			dropDurableSchemas(t, pool)
			if _, err := pool.Exec(context.Background(), test.unsafe); err != nil {
				t.Fatalf("make runtime role unsafe: %v", err)
			}
			defer func() {
				dropDurableSchemas(t, pool)
				if _, err := pool.Exec(
					context.Background(),
					test.restore,
				); err != nil {
					t.Errorf("restore safe runtime role attributes: %v", err)
				}
			}()

			err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).Migrate(context.Background())
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) ||
				postgresError.Code != "42501" ||
				!strings.Contains(err.Error(), "missing or unsafe") {
				t.Fatalf(
					"unsafe runtime role migration error = %v, want clear SQLSTATE 42501",
					err,
				)
			}
			var appliedCount int
			var durableTableExists bool
			if err := pool.QueryRow(
				context.Background(),
				"SELECT count(*) FROM engine.schema_migrations",
			).Scan(&appliedCount); err != nil {
				t.Fatalf("count migrations after unsafe-role failure: %v", err)
			}
			if err := pool.QueryRow(
				context.Background(),
				"SELECT to_regclass('engine.shard_checkpoints') IS NOT NULL",
			).Scan(&durableTableExists); err != nil {
				t.Fatalf("inspect schema after unsafe-role failure: %v", err)
			}
			if appliedCount != 0 || durableTableExists {
				t.Fatalf(
					"unsafe role failure left applied=%d durableTable=%t",
					appliedCount,
					durableTableExists,
				)
			}
		})
	}
}

func dropTestMigratorRole(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			  FROM pg_roles
			 WHERE rolname = 'platformgo_migrator_test'
		)`,
	).Scan(&exists); err != nil {
		t.Fatalf("inspect test migrator role: %v", err)
	}
	if !exists {
		return
	}
	if _, err := pool.Exec(
		context.Background(),
		"DROP OWNED BY platformgo_migrator_test",
	); err != nil {
		t.Fatalf("drop test migrator ownership: %v", err)
	}
	if _, err := pool.Exec(
		context.Background(),
		"DROP ROLE platformgo_migrator_test",
	); err != nil {
		t.Fatalf("drop test migrator role: %v", err)
	}
}

func assertRoleStatementDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	role string,
	statement string,
) {
	t.Helper()
	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin forbidden %s transaction: %v", role, err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
	}()
	if _, err := tx.Exec(
		context.Background(),
		"SET LOCAL ROLE "+role,
	); err != nil {
		t.Fatalf("set role %s: %v", role, err)
	}
	if _, err := tx.Exec(context.Background(), statement); err == nil {
		t.Fatalf("role %s executed forbidden statement", role)
	} else {
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
			t.Fatalf(
				"role %s denial error = %v, want SQLSTATE 42501",
				role,
				err,
			)
		}
	}
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
			Data: []byte("CREATE SCHEMA IF NOT EXISTS migration_probe;"),
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

func TestMigratorFinalBaselineRerunPreservesPopulatedData(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	migrator := platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	)
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("apply final baseline: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-rerun', 41)`); err != nil {
		t.Fatalf("seed final baseline: %v", err)
	}
	var appliedAt time.Time
	if err := pool.QueryRow(
		context.Background(),
		`SELECT applied_at
		   FROM engine.schema_migrations
		  WHERE filename = '20260724000100_durable_execution_foundation.up.sql'`,
	).Scan(&appliedAt); err != nil {
		t.Fatalf("read baseline application time: %v", err)
	}
	if err := migrator.Migrate(context.Background()); err != nil {
		t.Fatalf("idempotent final baseline rerun: %v", err)
	}
	assertSingleBaselineMigration(t, pool)
	var assignedShard int64
	var appliedAtAfter time.Time
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'account-rerun'",
	).Scan(&assignedShard); err != nil {
		t.Fatalf("read populated row after rerun: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		`SELECT applied_at
		   FROM engine.schema_migrations
		  WHERE filename = '20260724000100_durable_execution_foundation.up.sql'`,
	).Scan(&appliedAtAfter); err != nil {
		t.Fatalf("read baseline application time after rerun: %v", err)
	}
	if assignedShard != 41 || !appliedAtAfter.Equal(appliedAt) {
		t.Fatalf(
			"rerun changed populated baseline: shard=%d applied_at=%s want 41 and %s",
			assignedShard,
			appliedAtAfter,
			appliedAt,
		)
	}
}

func TestMigratorRejectsDisposableEightFileHistoryWithoutChangingData(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrationDirectory := filepath.Join("..", "..", "..", "migrations")
	baselineName := "20260724000100_durable_execution_foundation.up.sql"
	baseline, err := os.ReadFile(filepath.Join(migrationDirectory, baselineName))
	if err != nil {
		t.Fatalf("read final baseline: %v", err)
	}
	staleHistory := fstest.MapFS{
		baselineName: {
			Data: append(append([]byte(nil), baseline...), []byte("\n-- stale development bytes\n")...),
		},
	}
	for sequence := 2; sequence <= 8; sequence++ {
		name := fmt.Sprintf(
			"20260724000%d00_stale_development_step.up.sql",
			sequence,
		)
		staleHistory[name] = &fstest.MapFile{Data: []byte("SELECT 1;\n")}
	}
	if err := platformpostgres.NewMigrator(pool, staleHistory).
		Migrate(context.Background()); err != nil {
		t.Fatalf("apply stale eight-file history: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('stale-account', 17)`); err != nil {
		t.Fatalf("seed stale history: %v", err)
	}

	err = platformpostgres.NewMigrator(
		pool,
		os.DirFS(migrationDirectory),
	).Migrate(context.Background())
	if !errors.Is(err, platformpostgres.ErrMigrationChecksumMismatch) &&
		!errors.Is(err, platformpostgres.ErrDatabaseSchemaAhead) {
		t.Fatalf(
			"final baseline over stale history error = %v, want history refusal",
			err,
		)
	}
	var migrationCount int
	var shardID int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM engine.schema_migrations",
	).Scan(&migrationCount); err != nil {
		t.Fatalf("count stale history after refusal: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'stale-account'",
	).Scan(&shardID); err != nil {
		t.Fatalf("read stale data after refusal: %v", err)
	}
	if migrationCount != 8 || shardID != 17 {
		t.Fatalf(
			"refusal changed stale history: migrations=%d shard=%d",
			migrationCount,
			shardID,
		)
	}
}

func assertSingleBaselineMigration(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var count int
	var filename string
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*), min(filename)
		  FROM engine.schema_migrations`,
	).Scan(&count, &filename); err != nil {
		t.Fatalf("inspect final baseline history: %v", err)
	}
	if count != 1 ||
		filename != "20260724000100_durable_execution_foundation.up.sql" {
		t.Fatalf(
			"final baseline history = count %d filename %q",
			count,
			filename,
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

	dropDurableSchemas(t, pool)
	_, err := pool.Exec(
		context.Background(),
		`DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_api'
			) THEN
				CREATE ROLE platformgo_api NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_engine'
			) THEN
				CREATE ROLE platformgo_engine NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_outbox'
			) THEN
				CREATE ROLE platformgo_outbox NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_projector'
			) THEN
				CREATE ROLE platformgo_projector NOLOGIN;
			END IF;
		END;
		$$`,
	)
	if err != nil {
		t.Fatalf("provision test runtime roles: %v", err)
	}
}

func dropDurableSchemas(t *testing.T, pool *pgxpool.Pool) {
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
