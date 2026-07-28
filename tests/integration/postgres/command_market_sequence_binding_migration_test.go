package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

const (
	commandMarketBindingPreviousTip = "20260728000100_phase3_flat_balance_currency_scale_read.up.sql"
	commandMarketBindingMigration   = "20260728000200_phase3_command_market_sequence_binding.up.sql"
	commandMarketBindingRevision    = "20260728000200_phase3_command_market_sequence_binding"
	previousEngineRuntimeRevision   = "20260725001100_phase3_committed_realtime_outbox"
)

func TestCommandMarketSequenceBindingMigrationClassifiesPopulatedHistory(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply command-binding previous schema: %v", err)
	}

	seed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin historical command binding seed: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES
			(
				'00000000-0000-4000-8000-000000000801',
				'urn:xb:account:binding-upgrade',
				1,
				'adjust_balance',
				1,
				'{"kind":"adjust_balance"}',
				'pending',
				1
			),
			(
				'00000000-0000-4000-8000-000000000802',
				'urn:xb:account:binding-upgrade',
				2,
				'adjust_balance',
				1,
				'{"kind":"adjust_balance"}',
				'pending',
				2
			);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES
			(
				'account:urn:xb:account:binding-upgrade',
				'ordered',
				decode(repeat('01', 32), 'hex'),
				'00000000-0000-4000-8000-000000000801',
				'in_progress',
				'2027-01-01T00:00:00Z'
			),
			(
				'account:urn:xb:account:binding-upgrade',
				'explicit',
				decode(repeat('02', 32), 'hex'),
				'00000000-0000-4000-8000-000000000802',
				'in_progress',
				'2027-01-01T00:00:00Z'
			);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES
			(
				'00000000-0000-4000-8000-000000000801',
				'engine.input.8.command.v1',
				1,
				'{"marketSequence":0}'
			),
			(
				'00000000-0000-4000-8000-000000000802',
				'engine.input.8.command.v1',
				1,
				'{"marketSequence":41}'
			);
		UPDATE trading.commands
		   SET status = 'completed',
		       result = '{}',
		       completed_at = '2026-07-28T00:00:00Z'
		 WHERE command_id = '00000000-0000-4000-8000-000000000802';
		UPDATE trading.idempotency_records
		   SET state = 'completed',
		       response_status = 200,
		       response_headers = '{}',
		       response_body = ''::bytea
		 WHERE command_id = '00000000-0000-4000-8000-000000000802'`); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("seed historical command bindings: %v", err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatalf("commit historical command binding seed: %v", err)
	}
	var relationFileBefore uint32
	if err := pool.QueryRow(ctx, `
		SELECT pg_relation_filenode('trading.commands'::regclass)`).
		Scan(&relationFileBefore); err != nil {
		t.Fatalf("read command relation file before upgrade: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade command market bindings: %v", err)
	}
	var relationFileAfter uint32
	if err := pool.QueryRow(ctx, `
		SELECT pg_relation_filenode('trading.commands'::regclass)`).
		Scan(&relationFileAfter); err != nil {
		t.Fatalf("read command relation file after upgrade: %v", err)
	}
	if relationFileAfter != relationFileBefore {
		t.Fatalf(
			"command-binding migration rewrote trading.commands: %d -> %d",
			relationFileBefore,
			relationFileAfter,
		)
	}

	rows, err := pool.Query(ctx, `
		SELECT
			command.command_id::text,
			command.market_sequence_binding,
			CASE
				WHEN COALESCE(
					(outbox.payload ->> 'marketSequence')::bigint,
					0
				) = 0
				THEN 'ordered'
				ELSE 'explicit'
			END
		  FROM trading.commands AS command
		  JOIN messaging.outbox AS outbox
		    ON outbox.message_id = command.command_id
		 ORDER BY account_sequence`)
	if err != nil {
		t.Fatalf("read upgraded command bindings: %v", err)
	}
	defer rows.Close()
	want := []string{"ordered", "explicit"}
	index := 0
	for rows.Next() {
		var commandID string
		var storedBinding *string
		var effectiveBinding string
		if err := rows.Scan(
			&commandID,
			&storedBinding,
			&effectiveBinding,
		); err != nil {
			t.Fatalf("scan upgraded command binding: %v", err)
		}
		if storedBinding != nil {
			t.Fatalf(
				"historical command %s was backfilled to %q",
				commandID,
				*storedBinding,
			)
		}
		if index >= len(want) {
			t.Fatalf(
				"unexpected upgraded command binding for %s: %q",
				commandID,
				effectiveBinding,
			)
		}
		if effectiveBinding != want[index] {
			t.Fatalf(
				"binding for %s = %q at index %d, want %q",
				commandID,
				effectiveBinding,
				index,
				want[index],
			)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate upgraded command bindings: %v", err)
	}
	if index != len(want) {
		t.Fatalf("upgraded command binding rows = %d, want %d", index, len(want))
	}

	var (
		defaultExpression string
		isNullable        string
	)
	if err := pool.QueryRow(ctx, `
		SELECT column_default, is_nullable
		  FROM information_schema.columns
		 WHERE table_schema = 'trading'
		   AND table_name = 'commands'
		   AND column_name = 'market_sequence_binding'`).
		Scan(&defaultExpression, &isNullable); err != nil {
		t.Fatalf("read command binding definition: %v", err)
	}
	if defaultExpression != "'ordered'::text" || isNullable != "YES" {
		t.Fatalf(
			"command binding definition default=%q nullable=%q",
			defaultExpression,
			isNullable,
		)
	}

	legacyAdmission, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin previous-binary compatibility admission: %v", err)
	}
	_, stageErr := legacyAdmission.Exec(ctx, `
		SET LOCAL ROLE platformgo_api;
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000803',
			'urn:xb:account:binding-upgrade',
			3,
			'adjust_balance',
			1,
			'{"kind":"adjust_balance"}',
			'pending',
			3
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:binding-upgrade',
			'previous-binary-explicit',
			decode(repeat('05', 32), 'hex'),
			'00000000-0000-4000-8000-000000000803',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000803',
			'engine.input.8.command.v1',
			1,
			'{"marketSequence":42}'
		)`)
	var admissionErr error
	if stageErr != nil {
		_ = legacyAdmission.Rollback(ctx)
		admissionErr = stageErr
	} else {
		admissionErr = legacyAdmission.Commit(ctx)
	}
	var commitPostgresError *pgconn.PgError
	if !errors.As(admissionErr, &commitPostgresError) ||
		commitPostgresError.Code != "23514" {
		t.Fatalf(
			"previous-binary explicit admission = %v, want SQLSTATE 23514",
			admissionErr,
		)
	}
	var rejectedRows int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.commands
			  WHERE command_id = '00000000-0000-4000-8000-000000000803')
			+
			(SELECT count(*) FROM trading.idempotency_records
			  WHERE command_id = '00000000-0000-4000-8000-000000000803')
			+
			(SELECT count(*) FROM messaging.outbox
			  WHERE message_id = '00000000-0000-4000-8000-000000000803')`).
		Scan(&rejectedRows); err != nil {
		t.Fatalf("inspect rejected previous-binary admission: %v", err)
	}
	if rejectedRows != 0 {
		t.Fatalf(
			"previous-binary explicit admission committed %d durable rows",
			rejectedRows,
		)
	}
	legacyOrdered, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin previous-binary ordered admission: %v", err)
	}
	if _, err := legacyOrdered.Exec(ctx, `
		SET LOCAL ROLE platformgo_api;
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000805',
			'urn:xb:account:binding-upgrade',
			3,
			'adjust_balance',
			1,
			'{"kind":"adjust_balance"}',
			'pending',
			3
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:binding-upgrade',
			'previous-binary-ordered',
			decode(repeat('06', 32), 'hex'),
			'00000000-0000-4000-8000-000000000805',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000805',
			'engine.input.8.command.v1',
			1,
			'{"marketSequence":0}'
		)`); err != nil {
		_ = legacyOrdered.Rollback(ctx)
		t.Fatalf("stage previous-binary ordered admission: %v", err)
	}
	if err := legacyOrdered.Commit(ctx); err != nil {
		t.Fatalf("commit previous-binary ordered admission: %v", err)
	}
	var (
		orderedRows    int
		orderedBinding string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.commands
			  WHERE command_id = '00000000-0000-4000-8000-000000000805')
			+
			(SELECT count(*) FROM trading.idempotency_records
			  WHERE command_id = '00000000-0000-4000-8000-000000000805')
			+
			(SELECT count(*) FROM messaging.outbox
			  WHERE message_id = '00000000-0000-4000-8000-000000000805'),
			(SELECT market_sequence_binding
			   FROM trading.commands
			  WHERE command_id = '00000000-0000-4000-8000-000000000805')`).
		Scan(&orderedRows, &orderedBinding); err != nil {
		t.Fatalf("inspect previous-binary ordered admission: %v", err)
	}
	if orderedRows != 3 || orderedBinding != "ordered" {
		t.Fatalf(
			"previous-binary ordered admission rows=%d binding=%q",
			orderedRows,
			orderedBinding,
		)
	}
	_, explicitMarkerErr := pool.Exec(ctx, `
		BEGIN;
		SET LOCAL ROLE platformgo_api;
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time,
			market_sequence_binding
		) VALUES (
			'00000000-0000-4000-8000-000000000806',
			'urn:xb:account:binding-upgrade',
			4,
			'adjust_balance',
			1,
			'{"kind":"adjust_balance"}',
			'pending',
			4,
			'explicit'
		);
		COMMIT`)
	var explicitMarkerPostgresError *pgconn.PgError
	if !errors.As(explicitMarkerErr, &explicitMarkerPostgresError) ||
		explicitMarkerPostgresError.Code != "23514" {
		t.Fatalf(
			"API explicit marker admission = %v, want SQLSTATE 23514",
			explicitMarkerErr,
		)
	}
	_, exactMarketErr := pool.Exec(ctx, `
		BEGIN;
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260728000200_phase3_command_market_sequence_binding',
			true
		);
		SELECT set_config(
			'platformgo.engine_decision_hash_version',
			'4',
			true
		);
		SET LOCAL ROLE platformgo_engine;
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000807',
			1,
			1,
			1,
			decode(repeat('41', 32), 'hex'),
			4,
			decode(repeat('42', 32), 'hex'),
			decode(repeat('43', 32), 'hex'),
			'{"Kind":2,"StreamSequence":1,"MarketSequence":1}',
			'{
				"DecisionHashVersion":4,
				"StreamSequence":1,
				"MarketSequence":1,
				"BookChanges":[{"InstrumentID":"BTC-PERP"}]
			}',
			decode(repeat('44', 32), 'hex'),
			1
		);
		COMMIT`)
	if exactMarketErr != nil {
		t.Fatalf("insert exact post-cutover market receipt: %v", exactMarketErr)
	}
	_, zeroMarketErr := pool.Exec(ctx, `
		BEGIN;
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260728000200_phase3_command_market_sequence_binding',
			true
		);
		SELECT set_config(
			'platformgo.engine_decision_hash_version',
			'4',
			true
		);
		SET LOCAL ROLE platformgo_engine;
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000808',
			2,
			1,
			1,
			decode(repeat('45', 32), 'hex'),
			4,
			decode(repeat('46', 32), 'hex'),
			decode(repeat('47', 32), 'hex'),
			'{"Kind":2,"StreamSequence":2,"MarketSequence":0}',
			'{
				"DecisionHashVersion":4,
				"StreamSequence":2,
				"MarketSequence":0,
				"BookChanges":[{"InstrumentID":"BTC-PERP"}]
			}',
			decode(repeat('48', 32), 'hex'),
			1
		);
		COMMIT`)
	var zeroMarketPostgresError *pgconn.PgError
	if !errors.As(zeroMarketErr, &zeroMarketPostgresError) ||
		zeroMarketPostgresError.Code != "23514" {
		t.Fatalf(
			"zero post-cutover market receipt = %v, want SQLSTATE 23514",
			zeroMarketErr,
		)
	}
	_, rejectedMarketErr := pool.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260728000200_phase3_command_market_sequence_binding',
			false
		);
		SELECT set_config(
			'platformgo.engine_decision_hash_version',
			'4',
			false
		);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000804',
			1,
			1,
			1,
			decode(repeat('21', 32), 'hex'),
			4,
			decode(repeat('22', 32), 'hex'),
			decode(repeat('23', 32), 'hex'),
			'{"Kind":2,"StreamSequence":1,"MarketSequence":1}',
			'{"DecisionHashVersion":4,"MarketSequence":1,"BookChanges":[]}',
			decode(repeat('24', 32), 'hex'),
			1
		)`)
	var rejectedMarketPostgresError *pgconn.PgError
	if !errors.As(rejectedMarketErr, &rejectedMarketPostgresError) ||
		rejectedMarketPostgresError.Code != "23514" {
		t.Fatalf(
			"post-cutover rejected market receipt = %v, want SQLSTATE 23514",
			rejectedMarketErr,
		)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE trading.commands
		   SET market_sequence_binding = 'unknown'
		 WHERE command_id = '00000000-0000-4000-8000-000000000801'`); err == nil {
		t.Fatal("invalid command market binding unexpectedly accepted")
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry current command-binding migration: %v", err)
	}
}

func TestCommandMarketSequenceBindingMigrationFencesPreverifiedLegacyEngine(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previousFiles := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	previous := platformpostgres.NewMigrator(pool, previousFiles)
	if err := previous.MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply preverified-engine previous schema: %v", err)
	}
	if err := previous.VerifyCurrent(ctx); err != nil {
		t.Fatalf("preverify legacy engine schema: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			'0.02', '0.01', '50', '0.0002', '0.0005'
		);
		INSERT INTO market.books (
			instrument_id, mark_price, bids, asks, stream_sequence
		) VALUES (
			'BTC-PERP',
			'50000',
			'[{"Price":"49998","Quantity":"1"}]',
			'[{"Price":"50001","Quantity":"1"}]',
			5
		);
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000809',
			'urn:xb:account:preverified-engine',
			1,
			'submit_order',
			1,
			'{"kind":"submit_order"}',
			'pending',
			6
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:preverified-engine',
			'preverified-engine',
			decode(repeat('50', 32), 'hex'),
			'00000000-0000-4000-8000-000000000809',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000809',
			'engine.input.8.command.v1',
			1,
			'{"marketSequence":0}'
		)`); err != nil {
		t.Fatalf("seed preverified-engine cutover state: %v", err)
	}

	legacyConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire preverified legacy engine session: %v", err)
	}
	defer legacyConnection.Release()
	if _, err := legacyConnection.Exec(
		ctx,
		`SELECT
			set_config('platformgo.runtime_schema_revision', $1, false),
			set_config('platformgo.engine_decision_hash_version', '4', false)`,
		previousEngineRuntimeRevision,
	); err != nil {
		t.Fatalf("bind preverified legacy engine runtime: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("cut over after legacy engine preverification: %v", err)
	}
	if _, err := legacyConnection.Exec(ctx, `SET ROLE platformgo_engine`); err != nil {
		t.Fatalf("activate preverified legacy engine role: %v", err)
	}
	defer func() {
		_, _ = legacyConnection.Exec(context.Background(), `RESET ROLE`)
	}()
	requireRevisionFence := func(name string, err error) {
		t.Helper()
		var postgresError *pgconn.PgError
		if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
			t.Fatalf("%s legacy write = %v, want SQLSTATE 55000", name, err)
		}
	}
	var acquired bool
	if err := legacyConnection.QueryRow(
		ctx,
		`SELECT pg_try_advisory_lock(1346850639, 8)`,
	).Scan(&acquired); err != nil {
		t.Fatalf("acquire post-cutover ownership as preverified engine: %v", err)
	}
	if !acquired {
		t.Fatal("preverified legacy engine did not acquire post-cutover ownership")
	}
	_, ownershipEpochErr := legacyConnection.Exec(ctx, `
		INSERT INTO engine.shard_ownership_epochs (
			shard_id, epoch, acquired_at
		) VALUES (8, 1, clock_timestamp())
		ON CONFLICT (shard_id) DO UPDATE SET
			epoch = engine.shard_ownership_epochs.epoch + 1,
			acquired_at = EXCLUDED.acquired_at`)
	requireRevisionFence("ownership epoch", ownershipEpochErr)
	var released bool
	if err := legacyConnection.QueryRow(
		ctx,
		`SELECT pg_advisory_unlock(1346850639, 8)`,
	).Scan(&released); err != nil || !released {
		t.Fatalf("release fenced legacy ownership lock released=%t error=%v", released, err)
	}
	var (
		epochRows         int
		staleEngineActive bool
		staleEngineReady  bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.shard_ownership_epochs WHERE shard_id = 8),
			engine_active,
			engine_ready
		  FROM engine.runtime_command_ready_probe(8)`).
		Scan(&epochRows, &staleEngineActive, &staleEngineReady); err != nil {
		t.Fatalf("inspect fenced legacy ownership readiness: %v", err)
	}
	if epochRows != 0 || staleEngineActive || staleEngineReady {
		t.Fatalf(
			"fenced legacy ownership epoch=%d active=%t ready=%t",
			epochRows,
			staleEngineActive,
			staleEngineReady,
		)
	}

	if err := legacyConnection.QueryRow(
		ctx,
		`SELECT pg_try_advisory_lock(1346850639, 8)`,
	).Scan(&acquired); err != nil || !acquired {
		t.Fatalf(
			"reacquire raw lock for downstream revision fences acquired=%t error=%v",
			acquired,
			err,
		)
	}

	business, err := legacyConnection.Begin(ctx)
	if err != nil {
		t.Fatalf("begin preverified legacy business transaction: %v", err)
	}
	_, businessErr := business.Exec(ctx, `
		UPDATE trading.commands
		   SET status = 'completed',
		       result = '{"status":"accepted"}',
		       completed_at = '2026-07-28T00:00:00Z'
		 WHERE command_id = '00000000-0000-4000-8000-000000000809';
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000809',
			6,
			1,
			1,
			decode(repeat('51', 32), 'hex'),
			4,
			decode(repeat('52', 32), 'hex'),
			decode(repeat('53', 32), 'hex'),
			'{"Kind":1,"StreamSequence":6,"MarketSequence":0}',
			'{
				"DecisionHashVersion":4,
				"StreamSequence":6,
				"MarketSequence":0,
				"BookChanges":[]
			}',
			decode(repeat('54', 32), 'hex'),
			1
		);
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (
			8, 7, true, decode(repeat('53', 32), 'hex'), '{}'
		)`)
	requireRevisionFence("business receipt", businessErr)
	if err := business.Rollback(ctx); err != nil {
		t.Fatalf("roll back fenced legacy business transaction: %v", err)
	}

	_, duplicateErr := legacyConnection.Exec(ctx, `
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES (
			8,
			7,
			'00000000-0000-4000-8000-000000000809',
			decode(repeat('61', 32), 'hex'),
			decode(repeat('62', 32), 'hex'),
			decode(repeat('63', 32), 'hex'),
			decode(repeat('64', 32), 'hex'),
			'{}',
			'{"DecisionHashVersion":4}'
		)`)
	requireRevisionFence("duplicate receipt", duplicateErr)

	_, faultErr := legacyConnection.Exec(ctx, `
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES (
			8,
			decode(repeat('71', 32), 'hex'),
			'00000000-0000-4000-8000-000000000819',
			7,
			'sequence_gap',
			'preverified old engine fault',
			'{}',
			''::bytea
		)`)
	requireRevisionFence("fault", faultErr)

	_, checkpointErr := legacyConnection.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (
			8, 7, false, decode(repeat('81', 32), 'hex'), '{}'
		)`)
	requireRevisionFence("checkpoint", checkpointErr)

	var (
		commandStatus string
		effectRows    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT status
			   FROM trading.commands
			  WHERE command_id = '00000000-0000-4000-8000-000000000809'),
			(SELECT count(*)
			   FROM engine.input_receipts
			  WHERE input_id = '00000000-0000-4000-8000-000000000809')
			+
			(SELECT count(*)
			   FROM engine.duplicate_delivery_receipts
			  WHERE input_id = '00000000-0000-4000-8000-000000000809')
			+
			(SELECT count(*)
			   FROM engine.shard_faults
			  WHERE input_id = '00000000-0000-4000-8000-000000000819')
			+
			(SELECT count(*) FROM engine.shard_checkpoints WHERE shard_id = 8)`).
		Scan(&commandStatus, &effectRows); err != nil {
		t.Fatalf("inspect fenced preverified engine effects: %v", err)
	}
	if commandStatus != "pending" || effectRows != 0 {
		t.Fatalf(
			"preverified engine effects status=%q rows=%d, want pending/0",
			commandStatus,
			effectRows,
		)
	}

	if err := legacyConnection.QueryRow(
		ctx,
		`SELECT pg_advisory_unlock(1346850639, 8)`,
	).Scan(&released); err != nil || !released {
		t.Fatalf("release raw downstream test lock released=%t error=%v", released, err)
	}
	currentStore := platformpostgres.NewEngineStore(pool)
	currentOwnership, err := currentStore.AcquireShardOwnership(ctx, 8)
	if err != nil {
		t.Fatalf("current engine ownership after cutover: %v", err)
	}
	defer func() {
		_ = currentOwnership.Close(context.Background())
	}()
	recovered, err := currentStore.RecoverTradingState(ctx, 8)
	if err != nil || !recovered.Ready() {
		t.Fatalf(
			"current engine recovery after cutover ready=%t error=%v",
			recovered.Ready(),
			err,
		)
	}
	if _, err := legacyConnection.Exec(
		ctx,
		`SELECT set_config('platformgo.runtime_schema_revision', $1, false)`,
		commandMarketBindingRevision,
	); err != nil {
		t.Fatalf("bind current engine runtime after cutover: %v", err)
	}
	recoveredHash := recovered.Hash()
	if _, err := legacyConnection.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (
			8, $1, true, $2, '{}'
		)`,
		recovered.NextStreamSequence(),
		recoveredHash[:],
	); err != nil {
		t.Fatalf("current engine checkpoint after cutover: %v", err)
	}
	currentReady, err := currentStore.AcquireEngineReady(ctx, 8)
	if err != nil {
		t.Fatalf("current engine readiness after recovery: %v", err)
	}
	defer func() {
		_ = currentReady.Close(context.Background())
	}()
	var (
		currentEpoch uint64
		engineActive bool
		engineReady  bool
		checkpointOK bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT epoch FROM engine.shard_ownership_epochs WHERE shard_id = 8),
			engine_active,
			engine_ready,
			checkpoint_ready
		  FROM engine.runtime_command_ready_probe(8)`).
		Scan(
			&currentEpoch,
			&engineActive,
			&engineReady,
			&checkpointOK,
		); err != nil {
		t.Fatalf("inspect current engine readiness after cutover: %v", err)
	}
	if currentEpoch != 1 || !engineActive || !engineReady || !checkpointOK {
		t.Fatalf(
			"current engine epoch=%d active=%t ready=%t checkpoint=%t",
			currentEpoch,
			engineActive,
			engineReady,
			checkpointOK,
		)
	}
}

func TestCommandMarketSequenceBindingMigrationRefusesPendingExplicitHistory(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply pending-explicit previous schema: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin pending-explicit seed: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000811',
			'urn:xb:account:pending-explicit-upgrade',
			1,
			'adjust_balance',
			1,
			'{"kind":"adjust_balance"}',
			'pending',
			1
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:pending-explicit-upgrade',
			'explicit',
			decode(repeat('03', 32), 'hex'),
			'00000000-0000-4000-8000-000000000811',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000811',
			'engine.input.8.command.v1',
			1,
			'{"marketSequence":41}'
		)`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("seed pending explicit command: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit pending explicit command: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	migrationErr := current.Migrate(ctx)
	if migrationErr == nil {
		t.Fatal("pending explicit command upgrade unexpectedly succeeded")
	}
	var postgresError *pgconn.PgError
	if !errors.As(migrationErr, &postgresError) ||
		postgresError.Code != "55000" {
		t.Fatalf(
			"pending explicit command upgrade error = %v, want SQLSTATE 55000",
			migrationErr,
		)
	}
	var (
		columnExists    bool
		migrationExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'commands'
				   AND column_name = 'market_sequence_binding'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		commandMarketBindingMigration,
	).Scan(&columnExists, &migrationExists); err != nil {
		t.Fatalf("inspect refused pending-explicit upgrade: %v", err)
	}
	if columnExists || migrationExists {
		t.Fatalf(
			"refused pending-explicit upgrade leaked column=%t history=%t",
			columnExists,
			migrationExists,
		)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE trading.commands
		   SET status = 'rejected',
		       result = '{"reason":"drained before upgrade"}',
		       completed_at = '2026-07-28T00:00:00Z'
		 WHERE command_id = '00000000-0000-4000-8000-000000000811';
		UPDATE trading.idempotency_records
		   SET state = 'completed',
		       response_status = 409,
		       response_headers = '{}',
		       response_body = ''::bytea
		 WHERE command_id = '00000000-0000-4000-8000-000000000811'`); err != nil {
		t.Fatalf("drain pending explicit command: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry after draining pending explicit command: %v", err)
	}
}

func TestCommandMarketSequenceBindingMigrationPreservesRejectedMarketHistory(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply rejected-market previous schema: %v", err)
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "UNKNOWN-PERP",
			MarkPrice:    "100",
			Bids: []engine.BookLevel{{
				Price: "99", Quantity: "1",
			}},
			Asks: []engine.BookLevel{{
				Price: "101", Quantity: "1",
			}},
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("encode rejected historical market action: %v", err)
	}
	inputID, err := engine.ParseID("00000000-0000-4000-8000-000000000831")
	if err != nil {
		t.Fatalf("parse rejected historical market input ID: %v", err)
	}
	input := engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              8,
		Kind:                 engine.InputKindMarket,
		SourceID:             "legacy-market",
		SourceSequence:       1,
		StreamSequence:       1,
		MarketSequence:       1,
		LogicalTime:          engine.LogicalTime(1),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	next, decision, err := engine.ApplyTrading(engine.NewState(8), input, action)
	if err != nil ||
		decision.CommandResult.Status != engine.CommandStatusRejected ||
		len(decision.BookChanges) != 0 ||
		!next.Ready() {
		t.Fatalf(
			"legacy rejected market decision=%+v ready=%t error=%v",
			decision,
			next.Ready(),
			err,
		)
	}
	envelopeJSON, err := json.Marshal(struct {
		InputID              string
		SchemaVersion        uint32
		ShardID              uint32
		Kind                 uint8
		SourceID             string
		SourceSequence       uint64
		StreamSequence       uint64
		MarketSequence       uint64
		LogicalTime          int64
		ConfigurationVersion uint64
		InstrumentVersion    uint64
		Payload              []byte
	}{
		InputID:              input.InputID.String(),
		SchemaVersion:        input.SchemaVersion,
		ShardID:              uint32(input.ShardID),
		Kind:                 uint8(input.Kind),
		SourceID:             input.SourceID,
		SourceSequence:       input.SourceSequence,
		StreamSequence:       input.StreamSequence,
		MarketSequence:       input.MarketSequence,
		LogicalTime:          input.LogicalTime.UnixNano(),
		ConfigurationVersion: input.ConfigurationVersion,
		InstrumentVersion:    input.InstrumentVersion,
		Payload:              input.Payload.Bytes(),
	})
	if err != nil {
		t.Fatalf("encode rejected historical market envelope: %v", err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("encode rejected historical market decision: %v", err)
	}
	businessHash := engine.BusinessInputHash(input)
	stateHash := next.Hash()
	seed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rejected historical market seed: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		);
		SELECT set_config(
			'platformgo.engine_decision_hash_version',
			'4',
			false
		)`); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("bind rejected historical market runtime: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8, $1, 1, 1, $2, $3, $4, $5, $6, $7, $8, $9, $10
		)`,
		input.InputID.String(),
		decision.InputHashVersion,
		decision.InputHash[:],
		decision.DecisionHashVersion,
		decision.DecisionHash[:],
		stateHash[:],
		envelopeJSON,
		decisionJSON,
		businessHash[:],
		engine.CurrentBusinessHashVersion,
	); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("insert rejected historical market receipt: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (8, $1, true, $2, '{}')`,
		next.NextStreamSequence(),
		stateHash[:],
	); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("insert rejected historical market checkpoint: %v", err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatalf("commit rejected historical market seed: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade rejected-market history: %v", err)
	}
	var (
		receiptRows     int
		columnExists    bool
		migrationExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.input_receipts
			  WHERE input_id = '00000000-0000-4000-8000-000000000831'),
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'commands'
				   AND column_name = 'market_sequence_binding'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		commandMarketBindingMigration,
	).Scan(
		&receiptRows,
		&columnExists,
		&migrationExists,
	); err != nil {
		t.Fatalf("inspect rejected-market refusal: %v", err)
	}
	if receiptRows != 1 || !columnExists || !migrationExists {
		t.Fatalf(
			"rejected-market upgrade receipt=%d column=%t history=%t",
			receiptRows,
			columnExists,
			migrationExists,
		)
	}
	recovered, recoverErr := platformpostgres.NewEngineStore(pool).
		RecoverTradingState(ctx, 8)
	if recoverErr != nil ||
		!recovered.Ready() ||
		recovered.Hash() != next.Hash() {
		t.Fatalf(
			"rejected-market recovery ready=%t hash=%s want=%s error=%v",
			recovered.Ready(),
			recovered.Hash(),
			next.Hash(),
			recoverErr,
		)
	}
	if report, err := platformpostgres.NewEngineStore(pool).
		ReconcileShard(ctx, 8); err != nil || !report.Ready {
		t.Fatalf(
			"rejected-market reconciliation=%+v error=%v",
			report,
			err,
		)
	}
}

func TestCommandMarketSequenceBindingMigrationRecoversLegacyMarketFencePhysically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply mismatched-market previous schema: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	state := engine.NewState(8)
	ids := testkit.NewShardIDSequence(8)
	clock := testkit.NewManualClock(engine.LogicalTime(1))
	configAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	configInput := nextStoredInput(t, state, ids, clock, configAction)
	configured, configDecision, err := engine.ApplyTrading(
		state,
		configInput,
		configAction,
	)
	if err != nil || len(configDecision.InstrumentChanges) != 1 {
		t.Fatalf("derive legacy instrument configuration: %+v, %v", configDecision, err)
	}
	configEnvelopeJSON, err := json.Marshal(struct {
		InputID              string
		SchemaVersion        uint32
		ShardID              uint32
		Kind                 uint8
		SourceID             string
		SourceSequence       uint64
		StreamSequence       uint64
		MarketSequence       uint64
		LogicalTime          int64
		ConfigurationVersion uint64
		InstrumentVersion    uint64
		Payload              []byte
	}{
		InputID:              configInput.InputID.String(),
		SchemaVersion:        configInput.SchemaVersion,
		ShardID:              uint32(configInput.ShardID),
		Kind:                 uint8(configInput.Kind),
		SourceID:             configInput.SourceID,
		SourceSequence:       configInput.SourceSequence,
		StreamSequence:       configInput.StreamSequence,
		MarketSequence:       configInput.MarketSequence,
		LogicalTime:          configInput.LogicalTime.UnixNano(),
		ConfigurationVersion: configInput.ConfigurationVersion,
		InstrumentVersion:    configInput.InstrumentVersion,
		Payload:              configInput.Payload.Bytes(),
	})
	if err != nil {
		t.Fatalf("encode legacy instrument envelope: %v", err)
	}
	configDecisionJSON, err := json.Marshal(configDecision)
	if err != nil {
		t.Fatalf("encode legacy instrument decision: %v", err)
	}
	configResultJSON, err := json.Marshal(configDecision.CommandResult)
	if err != nil {
		t.Fatalf("encode legacy instrument command result: %v", err)
	}
	configBusinessHash := engine.BusinessInputHash(configInput)
	configStateHash := configured.Hash()
	seedPendingCommand(t, pool, configInput, configAction)
	configSeed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin legacy instrument seed: %v", err)
	}
	if _, err := configSeed.Exec(
		ctx,
		`SELECT
			set_config('platformgo.runtime_schema_revision', $1, true),
			set_config('platformgo.engine_decision_hash_version', '4', true)`,
		previousEngineRuntimeRevision,
	); err != nil {
		_ = configSeed.Rollback(ctx)
		t.Fatalf("bind legacy instrument runtime: %v", err)
	}
	if _, err := configSeed.Exec(ctx, `
		UPDATE trading.commands
		   SET status = $1,
		       result = $2,
		       completed_at = '2026-07-28T00:00:00Z'
		 WHERE command_id = $3`,
		string(configDecision.CommandResult.Status),
		configResultJSON,
		configInput.InputID.String(),
	); err != nil {
		_ = configSeed.Rollback(ctx)
		t.Fatalf("complete legacy instrument command: %v", err)
	}
	if _, err := configSeed.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			'0.1', '0.05', '10', '0', '0'
		)`); err != nil {
		_ = configSeed.Rollback(ctx)
		t.Fatalf("insert legacy instrument projection: %v", err)
	}
	if _, err := configSeed.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8, $1, $2, 1, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`,
		configInput.InputID.String(),
		configInput.StreamSequence,
		configDecision.InputHashVersion,
		configDecision.InputHash[:],
		configDecision.DecisionHashVersion,
		configDecision.DecisionHash[:],
		configStateHash[:],
		configEnvelopeJSON,
		configDecisionJSON,
		configBusinessHash[:],
		engine.CurrentBusinessHashVersion,
	); err != nil {
		_ = configSeed.Rollback(ctx)
		t.Fatalf("insert legacy instrument receipt: %v", err)
	}
	if _, err := configSeed.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (8, $1, true, $2, '{}')`,
		configured.NextStreamSequence(),
		configStateHash[:],
	); err != nil {
		_ = configSeed.Rollback(ctx)
		t.Fatalf("insert legacy instrument checkpoint: %v", err)
	}
	if err := configSeed.Commit(ctx); err != nil {
		t.Fatalf("commit legacy instrument configuration: %v", err)
	}
	state = configured
	bookAction := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "100",
			Bids: []engine.BookLevel{{
				Price: "99", Quantity: "1",
			}},
			Asks: []engine.BookLevel{{
				Price: "101", Quantity: "1",
			}},
		},
	}
	input := nextStoredInput(t, state, ids, clock, bookAction)
	input.MarketSequence = 999
	next, decision, err := engine.ApplyTrading(state, input, bookAction)
	if err != nil || len(decision.BookChanges) != 1 || !next.Ready() {
		t.Fatalf(
			"legacy mismatched market decision=%+v ready=%t error=%v",
			decision,
			next.Ready(),
			err,
		)
	}
	envelopeJSON, err := json.Marshal(struct {
		InputID              string
		SchemaVersion        uint32
		ShardID              uint32
		Kind                 uint8
		SourceID             string
		SourceSequence       uint64
		StreamSequence       uint64
		MarketSequence       uint64
		LogicalTime          int64
		ConfigurationVersion uint64
		InstrumentVersion    uint64
		Payload              []byte
	}{
		InputID:              input.InputID.String(),
		SchemaVersion:        input.SchemaVersion,
		ShardID:              uint32(input.ShardID),
		Kind:                 uint8(input.Kind),
		SourceID:             input.SourceID,
		SourceSequence:       input.SourceSequence,
		StreamSequence:       input.StreamSequence,
		MarketSequence:       input.MarketSequence,
		LogicalTime:          input.LogicalTime.UnixNano(),
		ConfigurationVersion: input.ConfigurationVersion,
		InstrumentVersion:    input.InstrumentVersion,
		Payload:              input.Payload.Bytes(),
	})
	if err != nil {
		t.Fatalf("encode mismatched historical market envelope: %v", err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		t.Fatalf("encode mismatched historical market decision: %v", err)
	}
	bidsJSON, err := json.Marshal(decision.BookChanges[0].Bids)
	if err != nil {
		t.Fatalf("encode mismatched historical bids: %v", err)
	}
	asksJSON, err := json.Marshal(decision.BookChanges[0].Asks)
	if err != nil {
		t.Fatalf("encode mismatched historical asks: %v", err)
	}
	businessHash := engine.BusinessInputHash(input)
	stateHash := next.Hash()
	seed, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin mismatched historical market seed: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		);
		SELECT set_config(
			'platformgo.engine_decision_hash_version',
			'4',
			false
		)`); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("bind mismatched historical market runtime: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8, $1, $2, 1, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)`,
		input.InputID.String(),
		input.StreamSequence,
		decision.InputHashVersion,
		decision.InputHash[:],
		decision.DecisionHashVersion,
		decision.DecisionHash[:],
		stateHash[:],
		envelopeJSON,
		decisionJSON,
		businessHash[:],
		engine.CurrentBusinessHashVersion,
	); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("insert mismatched historical market receipt: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		INSERT INTO market.books (
			instrument_id, mark_price, bids, asks, stream_sequence
		) VALUES ('BTC-PERP', '100', $1, $2, $3)`,
		bidsJSON,
		asksJSON,
		input.StreamSequence,
	); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("insert mismatched historical market projection: %v", err)
	}
	if _, err := seed.Exec(ctx, `
		UPDATE engine.shard_checkpoints
		   SET next_stream_sequence = $1,
		       ready = true,
		       state_hash = $2,
		       state_snapshot = '{}'
		 WHERE shard_id = 8`,
		next.NextStreamSequence(),
		stateHash[:],
	); err != nil {
		_ = seed.Rollback(ctx)
		t.Fatalf("update mismatched historical market checkpoint: %v", err)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatalf("commit mismatched historical market seed: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade mismatched-market history: %v", err)
	}
	var columnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM information_schema.columns
			 WHERE table_schema = 'trading'
			   AND table_name = 'commands'
			   AND column_name = 'market_sequence_binding'
		)`).Scan(&columnExists); err != nil {
		t.Fatalf("inspect mismatched-market refusal: %v", err)
	}
	if !columnExists {
		t.Fatal("mismatched-market upgrade omitted binding column")
	}
	recovered, recoverErr := store.RecoverTradingState(ctx, 8)
	if recoverErr != nil ||
		!recovered.Ready() ||
		recovered.Hash() != next.Hash() {
		t.Fatalf(
			"mismatched-market recovery ready=%t hash=%s want=%s error=%v",
			recovered.Ready(),
			recovered.Hash(),
			next.Hash(),
			recoverErr,
		)
	}
	if marketSequence, found := recovered.MarketSequence(); !found ||
		marketSequence != input.StreamSequence {
		t.Fatalf(
			"recovered physical market watermark=%d found=%t, want %d",
			marketSequence,
			found,
			input.StreamSequence,
		)
	}
	recovered, boundDecision, _, _ := applyStoredTrading(
		t,
		pool,
		store,
		recovered,
		ids,
		clock,
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "urn:xb:account:legacy-market-fence",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		platformpostgres.ApplyOptions{},
	)
	if boundDecision.MarketSequence != input.StreamSequence {
		t.Fatalf(
			"next ordered command market fence=%d, want %d",
			boundDecision.MarketSequence,
			input.StreamSequence,
		)
	}
	if report, err := store.ReconcileShard(ctx, 8); err != nil ||
		!report.Ready {
		t.Fatalf(
			"mismatched-market reconciliation=%+v error=%v",
			report,
			err,
		)
	}
}

func TestCommandMarketSequenceBindingMigrationWaitsForInFlightLegacyAdmission(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply in-flight admission previous schema: %v", err)
	}

	admission, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin in-flight legacy admission: %v", err)
	}
	if _, err := admission.Exec(ctx, `
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000821',
			'urn:xb:account:in-flight-explicit-upgrade',
			1,
			'adjust_balance',
			1,
			'{"kind":"adjust_balance"}',
			'pending',
			1
		);
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state, expires_at
		) VALUES (
			'account:urn:xb:account:in-flight-explicit-upgrade',
			'explicit',
			decode(repeat('04', 32), 'hex'),
			'00000000-0000-4000-8000-000000000821',
			'in_progress',
			'2027-01-01T00:00:00Z'
		);
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			'00000000-0000-4000-8000-000000000821',
			'engine.input.8.command.v1',
			1,
			'{"marketSequence":41}'
		)`); err != nil {
		_ = admission.Rollback(ctx)
		t.Fatalf("stage in-flight legacy admission: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- current.Migrate(ctx)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_locks
				 WHERE relation = 'trading.commands'::regclass
				   AND mode = 'ShareLock'
				   AND NOT granted
			)`).Scan(&waiting); err != nil {
			_ = admission.Rollback(ctx)
			t.Fatalf("inspect migration admission lock: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			_ = admission.Rollback(ctx)
			t.Fatal("migration did not wait behind in-flight legacy admission")
		}
		select {
		case migrationErr := <-migrationResult:
			_ = admission.Rollback(ctx)
			t.Fatalf(
				"migration completed before legacy admission commit: %v",
				migrationErr,
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := admission.Commit(ctx); err != nil {
		t.Fatalf("commit in-flight legacy admission: %v", err)
	}
	migrationErr := <-migrationResult
	var postgresError *pgconn.PgError
	if !errors.As(migrationErr, &postgresError) ||
		postgresError.Code != "55000" {
		t.Fatalf(
			"migration after in-flight explicit admission = %v, want SQLSTATE 55000",
			migrationErr,
		)
	}
	var columnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM information_schema.columns
			 WHERE table_schema = 'trading'
			   AND table_name = 'commands'
			   AND column_name = 'market_sequence_binding'
		)`).Scan(&columnExists); err != nil {
		t.Fatalf("inspect in-flight refusal rollback: %v", err)
	}
	if columnExists {
		t.Fatal("in-flight explicit admission refusal leaked binding column")
	}
}

func TestCommandMarketSequenceBindingMigrationRejectsActiveEngineOwnerAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply active-owner previous schema: %v", err)
	}
	store := platformpostgres.NewEngineStore(pool)
	owner, err := store.AcquireShardOwnership(ctx, 8)
	if err != nil {
		t.Fatalf("acquire pre-cutover engine owner: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	migrationErr := current.Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(migrationErr, &postgresError) ||
		postgresError.Code != "55P03" {
		_ = owner.Close(ctx)
		t.Fatalf(
			"active-owner command-binding migration = %v, want SQLSTATE 55P03",
			migrationErr,
		)
	}
	var (
		columnExists    bool
		migrationExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'commands'
				   AND column_name = 'market_sequence_binding'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		commandMarketBindingMigration,
	).Scan(&columnExists, &migrationExists); err != nil {
		_ = owner.Close(ctx)
		t.Fatalf("inspect active-owner rollback: %v", err)
	}
	if columnExists || migrationExists {
		_ = owner.Close(ctx)
		t.Fatalf(
			"active-owner refusal leaked column=%t history=%t",
			columnExists,
			migrationExists,
		)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatalf("release pre-cutover engine owner: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry command-binding migration after owner drain: %v", err)
	}
}

func TestCommandMarketSequenceBindingMigrationRollsBackAndRetriesAfterLockTimeout(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply command-binding lock previous schema: %v", err)
	}

	lock, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire command-binding blocker: %v", err)
	}
	defer lock.Release()
	tx, err := lock.Begin(ctx)
	if err != nil {
		t.Fatalf("begin command-binding blocker: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		LOCK TABLE trading.commands IN ACCESS SHARE MODE`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("lock command table: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := current.Migrate(ctx); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("blocked command-binding migration unexpectedly succeeded")
	}
	var (
		columnExists    bool
		migrationExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'commands'
				   AND column_name = 'market_sequence_binding'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		commandMarketBindingMigration,
	).Scan(&columnExists, &migrationExists); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("inspect command-binding rollback: %v", err)
	}
	if columnExists || migrationExists {
		_ = tx.Rollback(ctx)
		t.Fatalf(
			"failed migration leaked column=%t history=%t",
			columnExists,
			migrationExists,
		)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("release command-binding blocker: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry command-binding migration: %v", err)
	}
}

func TestCommandMarketSequenceBindingMigrationFencesWaitingLegacyMarketWriter(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	previous := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, previous).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply legacy-market race previous schema: %v", err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin command ALTER blocker: %v", err)
	}
	if _, err := blocker.Exec(ctx, `
		LOCK TABLE trading.commands IN ACCESS SHARE MODE`); err != nil {
		_ = blocker.Rollback(ctx)
		t.Fatalf("block command ALTER: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- current.Migrate(ctx)
	}()

	waitForRelationLock := func(relation string, mode string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					  FROM pg_locks
					 WHERE relation = $1::regclass
					   AND mode = $2
					   AND NOT granted
				)`,
				relation,
				mode,
			).Scan(&waiting); err != nil {
				t.Fatalf("inspect %s %s lock: %v", relation, mode, err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s %s lock", relation, mode)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForRelationLock("trading.commands", "AccessExclusiveLock")

	legacyResult := make(chan error, 1)
	go func() {
		_, insertErr := pool.Exec(ctx, `
			SELECT set_config(
				'platformgo.runtime_schema_revision',
				'20260725001100_phase3_committed_realtime_outbox',
				false
			);
			SELECT set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			);
			INSERT INTO engine.input_receipts (
				shard_id, input_id, stream_sequence, schema_version,
				input_hash_version, input_hash, decision_hash_version,
				decision_hash, resulting_state_hash, envelope, decision,
				business_input_hash, business_input_hash_version
			) VALUES (
				8,
				'00000000-0000-4000-8000-000000000841',
				1,
				1,
				1,
				decode(repeat('31', 32), 'hex'),
				4,
				decode(repeat('32', 32), 'hex'),
				decode(repeat('33', 32), 'hex'),
				'{"Kind":2,"StreamSequence":1,"MarketSequence":1}',
				'{"DecisionHashVersion":4,"MarketSequence":1,"BookChanges":[]}',
				decode(repeat('34', 32), 'hex'),
				1
			)`)
		legacyResult <- insertErr
	}()
	waitForRelationLock("engine.input_receipts", "RowExclusiveLock")

	if err := blocker.Rollback(ctx); err != nil {
		t.Fatalf("release command ALTER blocker: %v", err)
	}
	if err := <-migrationResult; err != nil {
		t.Fatalf("migrate while legacy market writer waits: %v", err)
	}
	legacyErr := <-legacyResult
	var postgresError *pgconn.PgError
	if !errors.As(legacyErr, &postgresError) ||
		postgresError.Code != "23514" {
		t.Fatalf(
			"waiting legacy market writer = %v, want SQLSTATE 23514",
			legacyErr,
		)
	}
	var receiptRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE input_id = '00000000-0000-4000-8000-000000000841'`).
		Scan(&receiptRows); err != nil {
		t.Fatalf("inspect fenced legacy market writer: %v", err)
	}
	if receiptRows != 0 {
		t.Fatalf("fenced legacy market writer committed %d receipts", receiptRows)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("rerun after legacy market writer fence: %v", err)
	}
}

func TestCommandMarketSequenceBindingMigrationRollsBackOnStatementTimeout(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	files := migrationFilesThrough(t, commandMarketBindingPreviousTip)
	if err := platformpostgres.NewMigrator(pool, files).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply statement-timeout previous schema: %v", err)
	}
	migrationPath := filepath.Join(
		"..",
		"..",
		"..",
		"migrations",
		commandMarketBindingMigration,
	)
	raw, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read command-binding migration: %v", err)
	}
	files[commandMarketBindingMigration] = &fstest.MapFile{
		Data: append(raw, []byte("\nSELECT pg_sleep(11);\n")...),
	}
	timeoutErr := platformpostgres.NewMigrator(pool, files).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(timeoutErr, &postgresError) ||
		postgresError.Code != "57014" {
		t.Fatalf(
			"command-binding statement timeout = %v, want SQLSTATE 57014",
			timeoutErr,
		)
	}
	var (
		columnExists    bool
		migrationExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM information_schema.columns
				 WHERE table_schema = 'trading'
				   AND table_name = 'commands'
				   AND column_name = 'market_sequence_binding'
			),
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		commandMarketBindingMigration,
	).Scan(&columnExists, &migrationExists); err != nil {
		t.Fatalf("inspect statement-timeout rollback: %v", err)
	}
	if columnExists || migrationExists {
		t.Fatalf(
			"statement timeout leaked column=%t history=%t",
			columnExists,
			migrationExists,
		)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("retry after command-binding statement timeout: %v", err)
	}
}
