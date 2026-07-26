package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	fillEffectiveLeveragePreviousTip = "20260726000800_phase3_balance_projection_hash_v3.up.sql"
	fillEffectiveLeverageCutoverTip  = "20260726000900_phase3_fill_effective_leverage_hash_v4.up.sql"
	fillEffectiveLeverageTargetTip   = "20260726001000_phase3_validate_fill_effective_leverage.up.sql"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:952
//	test: fill_surfaces_frozen_effective_leverage
//
// Adaptations:
//   - The source's application fixture is replaced by a PostgreSQL 19 Beta 2
//     forward-migration acceptance test from the exact preceding schema tip.
//   - Existing v2/v3 receipts and their fills represent immutable production
//     history; no old runtime or live service is executed.
//   - Decimal acceptance and rejection are asserted directly at the
//     authoritative NUMERIC boundary.
//
// Assertions preserved:
//   - Frozen leverage 10 is retained exactly.
//   - Stored 5.00 is accepted as the same exact value as 5.
//   - A missing frozen leverage remains null.
func TestFillEffectiveLeverageHashV4MigrationUpgradesPreviousTipWithoutRewrite(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillEffectiveLeveragePreviousTip(t, ctx, pool)

	var (
		beforeTip          string
		beforeJournalCount int
		beforeFillCount    int
		beforeRelfilenode  uint32
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT count(*) FROM trading.fills),
			(SELECT relfilenode
			   FROM pg_class
			  WHERE oid = 'trading.fills'::regclass)`,
	).Scan(
		&beforeTip,
		&beforeJournalCount,
		&beforeFillCount,
		&beforeRelfilenode,
	); err != nil {
		t.Fatalf("inspect exact previous migration tip: %v", err)
	}
	if beforeTip != fillEffectiveLeveragePreviousTip {
		t.Fatalf(
			"previous migration tip = %q, want exact tip %q",
			beforeTip,
			fillEffectiveLeveragePreviousTip,
		)
	}
	if beforeFillCount != 2 {
		t.Fatalf("previous fill history count = %d, want 2", beforeFillCount)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("upgrade to fill effective-leverage hash v4: %v", err)
	}

	var (
		afterTip          string
		afterJournalCount int
		afterFillCount    int
		afterRelfilenode  uint32
		columnType        string
		columnNotNull     bool
		columnHasDefault  bool
		constraintValid   bool
		nullHistoryCount  int
		v2ReceiptCount    int
		v3ReceiptCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT count(*) FROM trading.fills),
			(SELECT relfilenode
			   FROM pg_class
			  WHERE oid = 'trading.fills'::regclass),
			(
				SELECT format_type(attribute.atttypid, attribute.atttypmod)
				  FROM pg_attribute AS attribute
				 WHERE attribute.attrelid = 'trading.fills'::regclass
				   AND attribute.attname = 'effective_leverage'
				   AND NOT attribute.attisdropped
			),
			(
				SELECT attribute.attnotnull
				  FROM pg_attribute AS attribute
				 WHERE attribute.attrelid = 'trading.fills'::regclass
				   AND attribute.attname = 'effective_leverage'
				   AND NOT attribute.attisdropped
			),
			(
				SELECT attribute.atthasdef
				  FROM pg_attribute AS attribute
				 WHERE attribute.attrelid = 'trading.fills'::regclass
				   AND attribute.attname = 'effective_leverage'
				   AND NOT attribute.attisdropped
			),
			(
				SELECT constraint_row.convalidated
				  FROM pg_constraint AS constraint_row
				 WHERE constraint_row.conrelid = 'trading.fills'::regclass
				   AND constraint_row.conname =
				       'fills_effective_leverage_positive'
			),
			(
				SELECT count(*)
				  FROM trading.fills
				 WHERE effective_leverage IS NULL
			),
			(
				SELECT count(*)
				  FROM engine.input_receipts
				 WHERE decision_hash_version = 2
			),
			(
				SELECT count(*)
				  FROM engine.input_receipts
				 WHERE decision_hash_version = 3
			)`,
	).Scan(
		&afterTip,
		&afterJournalCount,
		&afterFillCount,
		&afterRelfilenode,
		&columnType,
		&columnNotNull,
		&columnHasDefault,
		&constraintValid,
		&nullHistoryCount,
		&v2ReceiptCount,
		&v3ReceiptCount,
	); err != nil {
		t.Fatalf("inspect upgraded effective-leverage schema and history: %v", err)
	}
	if afterTip != fillEffectiveLeverageTargetTip {
		t.Fatalf(
			"upgraded migration tip = %q, want %q (target migration is missing)",
			afterTip,
			fillEffectiveLeverageTargetTip,
		)
	}
	if afterJournalCount != beforeJournalCount+2 {
		t.Fatalf(
			"migration journal count = %d, want %d",
			afterJournalCount,
			beforeJournalCount+2,
		)
	}
	if columnType != "numeric(38,18)" ||
		columnNotNull ||
		columnHasDefault ||
		!constraintValid {
		t.Fatalf(
			"effective_leverage shape = type %q not-null %t default %t constraint-valid %t",
			columnType,
			columnNotNull,
			columnHasDefault,
			constraintValid,
		)
	}
	if afterFillCount != beforeFillCount ||
		nullHistoryCount != beforeFillCount ||
		afterRelfilenode != beforeRelfilenode {
		t.Fatalf(
			"upgrade rewrote/backfilled history: fills %d->%d null=%d relfilenode %d->%d",
			beforeFillCount,
			afterFillCount,
			nullHistoryCount,
			beforeRelfilenode,
			afterRelfilenode,
		)
	}
	if v2ReceiptCount != 1 || v3ReceiptCount != 1 {
		t.Fatalf(
			"historical receipt versions = v2 %d v3 %d, want one each",
			v2ReceiptCount,
			v3ReceiptCount,
		)
	}

	insertFillWithEffectiveLeverage(
		t,
		ctx,
		pool,
		"00000000-0000-4000-8000-000000000911",
		"00000000-0000-4000-8000-000000000921",
		"10",
	)
	insertFillWithEffectiveLeverage(
		t,
		ctx,
		pool,
		"00000000-0000-4000-8000-000000000912",
		"00000000-0000-4000-8000-000000000922",
		"5.00",
	)
	var (
		tenCount  int
		fiveCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (
				WHERE fill_id = '00000000-0000-4000-8000-000000000911'
				  AND effective_leverage = '10'::numeric
			),
			count(*) FILTER (
				WHERE fill_id = '00000000-0000-4000-8000-000000000912'
				  AND effective_leverage = '5.00'::numeric
			)
		  FROM trading.fills`,
	).Scan(&tenCount, &fiveCount); err != nil {
		t.Fatalf("read exact effective leverages: %v", err)
	}
	if tenCount != 1 || fiveCount != 1 {
		t.Fatalf(
			"accepted effective leverages = 10:%d 5.00:%d, want one each",
			tenCount,
			fiveCount,
		)
	}

	for _, invalid := range []struct {
		fillID   string
		inputID  string
		leverage string
	}{
		{
			fillID:   "00000000-0000-4000-8000-000000000913",
			inputID:  "00000000-0000-4000-8000-000000000923",
			leverage: "0",
		},
		{
			fillID:   "00000000-0000-4000-8000-000000000914",
			inputID:  "00000000-0000-4000-8000-000000000924",
			leverage: "-1",
		},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO trading.fills (
				fill_id, order_id, input_id, account_id, instrument_id,
				side, price, quantity, position_id, position_effect,
				liquidity_side, logical_time, effective_leverage
			) VALUES (
				$1,
				'00000000-0000-4000-8000-000000000901',
				$2,
				'urn:xb:account:effective-leverage-migration',
				'BTC-PERP',
				'BUY',
				'60000'::numeric,
				'0.01'::numeric,
				'00000000-0000-4000-8000-000000000902',
				'open',
				'TAKER',
				1785060000000000000,
				$3::numeric
			)`,
			invalid.fillID,
			invalid.inputID,
			invalid.leverage,
		)
		requireFillEffectiveLeverageSQLState(t, err, "23514")
	}
}

func TestFillEffectiveLeverageHashV4MigrationLockTimeoutRollsBackAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillEffectiveLeveragePreviousTip(t, ctx, pool)

	var (
		beforeTip          string
		beforeJournalCount int
		beforeFillCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT count(*) FROM trading.fills)`,
	).Scan(
		&beforeTip,
		&beforeJournalCount,
		&beforeFillCount,
	); err != nil {
		t.Fatalf("inspect pre-contention state: %v", err)
	}
	if beforeTip != fillEffectiveLeveragePreviousTip {
		t.Fatalf("pre-contention tip = %q", beforeTip)
	}

	lockingTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fill-table lock: %v", err)
	}
	defer func() { _ = lockingTx.Rollback(context.Background()) }()
	if _, err := lockingTx.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("lock fills against migration: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	err = current.Migrate(boundedContext)
	requireFillEffectiveLeverageSQLState(t, err, "55P03")

	var (
		contendedTip          string
		contendedJournalCount int
		contendedFillCount    int
		columnExists          bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT count(*) FROM trading.fills),
			EXISTS (
				SELECT 1
				  FROM pg_attribute AS attribute
				 WHERE attribute.attrelid = 'trading.fills'::regclass
				   AND attribute.attname = 'effective_leverage'
				   AND NOT attribute.attisdropped
			)`,
	).Scan(
		&contendedTip,
		&contendedJournalCount,
		&contendedFillCount,
		&columnExists,
	); err != nil {
		t.Fatalf("inspect contended migration rollback: %v", err)
	}
	if contendedTip != beforeTip ||
		contendedJournalCount != beforeJournalCount ||
		contendedFillCount != beforeFillCount ||
		columnExists {
		t.Fatalf(
			"contended migration partially applied: tip %q->%q journal %d->%d fills %d->%d column=%t",
			beforeTip,
			contendedTip,
			beforeJournalCount,
			contendedJournalCount,
			beforeFillCount,
			contendedFillCount,
			columnExists,
		)
	}

	if err := lockingTx.Rollback(ctx); err != nil {
		t.Fatalf("release fill-table lock: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry uncontended effective-leverage migration: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried effective-leverage migration: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_attribute AS attribute
				 WHERE attribute.attrelid = 'trading.fills'::regclass
				   AND attribute.attname = 'effective_leverage'
				   AND NOT attribute.attisdropped
			)`,
	).Scan(&contendedTip, &columnExists); err != nil {
		t.Fatalf("inspect clean retry: %v", err)
	}
	if contendedTip != fillEffectiveLeverageTargetTip || !columnExists {
		t.Fatalf(
			"clean retry state = tip %q column=%t",
			contendedTip,
			columnExists,
		)
	}
}

func TestFillEffectiveLeverageValidationDoesNotBlockNormalFillWriter(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillEffectiveLeveragePreviousTip(t, ctx, pool)

	cutover := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageCutoverTip),
	)
	if err := cutover.Migrate(ctx); err != nil {
		t.Fatalf("apply effective-leverage cutover: %v", err)
	}
	var constraintValid bool
	if err := pool.QueryRow(ctx, `
		SELECT convalidated
		  FROM pg_constraint
		 WHERE conrelid = 'trading.fills'::regclass
		   AND conname = 'fills_effective_leverage_positive'`,
	).Scan(&constraintValid); err != nil {
		t.Fatalf("inspect cutover constraint: %v", err)
	}
	if constraintValid {
		t.Fatal("cutover unexpectedly validated fill leverage constraint")
	}

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin normal fill writer lock: %v", err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if _, err := writer.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("hold normal fill writer lock: %v", err)
	}

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if err := current.Migrate(boundedContext); err != nil {
		t.Fatalf("validate while normal fill writer remains active: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT convalidated
		  FROM pg_constraint
		 WHERE conrelid = 'trading.fills'::regclass
		   AND conname = 'fills_effective_leverage_positive'`,
	).Scan(&constraintValid); err != nil {
		t.Fatalf("inspect validated constraint: %v", err)
	}
	if !constraintValid {
		t.Fatal("separate validation migration left constraint unvalidated")
	}
}

func TestFillEffectiveLeverageCutoverRejectsActiveEngineOwnerAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillEffectiveLeveragePreviousTip(t, ctx, pool)

	store := platformpostgres.NewEngineStore(pool)
	owner, err := store.AcquireShardOwnership(ctx, 8)
	if err != nil {
		t.Fatalf("acquire pre-cutover engine owner: %v", err)
	}
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	err = current.Migrate(boundedContext)
	requireFillEffectiveLeverageSQLState(t, err, "55P03")

	var (
		currentTip   string
		columnExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			)`,
	).Scan(&currentTip, &columnExists); err != nil {
		t.Fatalf("inspect rejected active-owner cutover: %v", err)
	}
	if currentTip != fillEffectiveLeveragePreviousTip || columnExists {
		t.Fatalf(
			"active-owner cutover partially applied: tip %q column=%t",
			currentTip,
			columnExists,
		)
	}

	if err := owner.Close(ctx); err != nil {
		t.Fatalf("release pre-cutover engine owner: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry cutover after owner drain: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify cutover after owner drain: %v", err)
	}
}

func TestFillEffectiveLeverageHashV4MigrationFencesV3WritersAndAcceptsV4(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillEffectiveLeveragePreviousTip(t, ctx, pool)

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply effective-leverage v4 migration: %v", err)
	}
	var currentTip string
	if err := pool.QueryRow(ctx, `
		SELECT max(filename) FROM engine.schema_migrations`,
	).Scan(&currentTip); err != nil {
		t.Fatalf("read effective-leverage migration tip: %v", err)
	}
	if currentTip != fillEffectiveLeverageTargetTip {
		t.Fatalf(
			"migration tip = %q, want %q (target migration is missing)",
			currentTip,
			fillEffectiveLeverageTargetTip,
		)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire writer-fence connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		)`); err != nil {
		t.Fatalf("bind unchanged runtime schema revision: %v", err)
	}

	oldWriter, err := connection.Begin(ctx)
	if err != nil {
		t.Fatalf("begin old v3 business writer: %v", err)
	}
	defer func() { _ = oldWriter.Rollback(context.Background()) }()
	if _, err := oldWriter.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000000931',
			'00000000-0000-4000-8000-000000000901',
			'00000000-0000-4000-8000-000000000941',
			'urn:xb:account:effective-leverage-migration',
			'BTC-PERP',
			'BUY',
			'60000'::numeric,
			'0.01'::numeric,
			'00000000-0000-4000-8000-000000000902',
			'open',
			'TAKER',
			1785060000000000000,
			'10'::numeric
		)`); err != nil {
		t.Fatalf("stage old-writer fill before its receipt: %v", err)
	}
	_, err = oldWriter.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000941',
			3,
			1,
			1,
			decode(repeat('91', 32), 'hex'),
			3,
			decode(repeat('92', 32), 'hex'),
			decode(repeat('93', 32), 'hex'),
			'{}',
			'{"FillChanges":[{"FillID":"old-v3-fill"}]}',
			decode(repeat('94', 32), 'hex'),
			1
		)`)
	requireFillEffectiveLeverageSQLState(t, err, "55000")
	if err := oldWriter.Rollback(ctx); err != nil {
		t.Fatalf("roll back fenced old business writer: %v", err)
	}

	var (
		oldFillCount    int
		oldReceiptCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*)
			   FROM trading.fills
			  WHERE fill_id =
			        '00000000-0000-4000-8000-000000000931'),
			(SELECT count(*)
			   FROM engine.input_receipts
			  WHERE input_id =
			        '00000000-0000-4000-8000-000000000941')`,
	).Scan(&oldFillCount, &oldReceiptCount); err != nil {
		t.Fatalf("inspect fenced old business transaction: %v", err)
	}
	if oldFillCount != 0 || oldReceiptCount != 0 {
		t.Fatalf(
			"fenced v3 business transaction committed fill=%d receipt=%d",
			oldFillCount,
			oldReceiptCount,
		)
	}

	_, err = connection.Exec(ctx, `
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES (
			8,
			1,
			'00000000-0000-4000-8000-000000000942',
			decode(repeat('a1', 32), 'hex'),
			decode(repeat('a2', 32), 'hex'),
			decode(repeat('a3', 32), 'hex'),
			decode(repeat('a4', 32), 'hex'),
			'{}',
			'{"DecisionHashVersion":3}'
		)`)
	requireFillEffectiveLeverageSQLState(t, err, "55000")

	_, err = connection.Exec(ctx, `
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES (
			8,
			decode(repeat('a5', 32), 'hex'),
			'00000000-0000-4000-8000-000000000944',
			3,
			'sequence_gap',
			'old v3 writer fault',
			'{}',
			''::bytea
		)`)
	requireFillEffectiveLeverageSQLState(t, err, "55000")
	var oldFaultCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.shard_faults
		 WHERE input_id =
		       '00000000-0000-4000-8000-000000000944'`,
	).Scan(&oldFaultCount); err != nil {
		t.Fatalf("count fenced old fault: %v", err)
	}
	if oldFaultCount != 0 {
		t.Fatalf("old v3 fault count = %d, want 0", oldFaultCount)
	}

	if _, err := connection.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260725001100_phase3_committed_realtime_outbox',
				false
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			)`); err != nil {
		t.Fatalf("bind v4 runtime decision-hash version: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000943',
			3,
			1,
			1,
			decode(repeat('b1', 32), 'hex'),
			4,
			decode(repeat('b2', 32), 'hex'),
			decode(repeat('b3', 32), 'hex'),
			'{}',
			'{"FillChanges":[]}',
			decode(repeat('b4', 32), 'hex'),
			1
		);
		INSERT INTO engine.duplicate_delivery_receipts (
			shard_id, stream_sequence, input_id, input_hash,
			original_decision_hash, decision_hash, resulting_state_hash,
			envelope, decision
		) VALUES (
			8,
			1,
			'00000000-0000-4000-8000-000000000943',
			decode(repeat('c1', 32), 'hex'),
			decode(repeat('c2', 32), 'hex'),
			decode(repeat('c3', 32), 'hex'),
			decode(repeat('c4', 32), 'hex'),
			'{}',
			'{"DecisionHashVersion":4}'
		)`); err != nil {
		t.Fatalf("insert v4 business and duplicate receipts: %v", err)
	}

	v4Fault, err := connection.Begin(ctx)
	if err != nil {
		t.Fatalf("begin v4 fault writer: %v", err)
	}
	defer func() { _ = v4Fault.Rollback(context.Background()) }()
	if _, err := v4Fault.Exec(ctx, `
		INSERT INTO engine.shard_faults (
			shard_id, resulting_state_hash, input_id, stream_sequence,
			error_kind, error_detail, envelope, supplied_action
		) VALUES (
			8,
			decode(repeat('d1', 32), 'hex'),
			'00000000-0000-4000-8000-000000000945',
			4,
			'sequence_gap',
			'v4 writer fault',
			'{}',
			''::bytea
		);
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash,
			state_snapshot
		) VALUES (
			8,
			5,
			false,
			decode(repeat('d1', 32), 'hex'),
			'{"recovery":"receipt_replay","version":1}'
		)`); err != nil {
		t.Fatalf("stage v4 fault and checkpoint: %v", err)
	}
	if err := v4Fault.Commit(ctx); err != nil {
		t.Fatalf("commit v4 fault and checkpoint: %v", err)
	}

	var (
		businessReceiptCount  int
		duplicateReceiptCount int
		v4FaultCount          int
		checkpointCount       int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.input_receipts),
			(SELECT count(*) FROM engine.duplicate_delivery_receipts),
			(SELECT count(*)
			   FROM engine.shard_faults
			  WHERE input_id =
			        '00000000-0000-4000-8000-000000000945'),
			(SELECT count(*) FROM engine.shard_checkpoints)`,
	).Scan(
		&businessReceiptCount,
		&duplicateReceiptCount,
		&v4FaultCount,
		&checkpointCount,
	); err != nil {
		t.Fatalf("count accepted v4 durable rows: %v", err)
	}
	if businessReceiptCount != 3 ||
		duplicateReceiptCount != 1 ||
		v4FaultCount != 1 ||
		checkpointCount != 1 {
		t.Fatalf(
			"post-fence rows = business %d duplicate %d fault %d checkpoint %d, want 3/1/1/1",
			businessReceiptCount,
			duplicateReceiptCount,
			v4FaultCount,
			checkpointCount,
		)
	}
}

func seedFillEffectiveLeveragePreviousTip(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()

	preV3 := migrationFilesThrough(
		t,
		"20260726000700_phase3_user_api_keys.up.sql",
	)
	if err := platformpostgres.NewMigrator(pool, preV3).
		MigrateAndProvision(ctx, 8); err != nil {
		t.Fatalf("apply pre-v3 schema: %v", err)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire previous-tip history connection: %v", err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, `
		SELECT set_config(
			'platformgo.runtime_schema_revision',
			'20260725001100_phase3_committed_realtime_outbox',
			false
		);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP',
			1,
			2,
			3,
			'USDC',
			2,
			'0.1'::numeric,
			'0.05'::numeric,
			'10'::numeric,
			'0'::numeric,
			'0'::numeric
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES (
			'urn:xb:account:effective-leverage-migration',
			'NETTING'
		);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'00000000-0000-4000-8000-000000000901',
			'urn:xb:account:effective-leverage-migration',
			'BTC-PERP',
			'BUY',
			'MARKET',
			'IOC',
			'FILLED',
			'0.02'::numeric,
			'0.02'::numeric,
			'60000'::numeric,
			false,
			false,
			false,
			1
		);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000903',
			1,
			1,
			1,
			decode(repeat('11', 32), 'hex'),
			2,
			decode(repeat('12', 32), 'hex'),
			decode(repeat('13', 32), 'hex'),
			'{}',
			'{"FillChanges":[{"FillID":"historical-v2-fill"}]}',
			decode(repeat('14', 32), 'hex'),
			1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000904',
			'00000000-0000-4000-8000-000000000901',
			'00000000-0000-4000-8000-000000000903',
			'urn:xb:account:effective-leverage-migration',
			'BTC-PERP',
			'BUY',
			'60000'::numeric,
			'0.01'::numeric,
			'00000000-0000-4000-8000-000000000902',
			'open',
			'TAKER',
			1785060000000000000
		)`); err != nil {
		t.Fatalf("seed v2 fill and receipt history: %v", err)
	}

	v3 := migrationFilesThrough(t, fillEffectiveLeveragePreviousTip)
	if err := platformpostgres.NewMigrator(pool, v3).Migrate(ctx); err != nil {
		t.Fatalf("upgrade history to exact v3 tip: %v", err)
	}
	if _, err := connection.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			8,
			'00000000-0000-4000-8000-000000000905',
			2,
			1,
			1,
			decode(repeat('21', 32), 'hex'),
			3,
			decode(repeat('22', 32), 'hex'),
			decode(repeat('23', 32), 'hex'),
			'{}',
			'{"FillChanges":[{"FillID":"historical-v3-fill"}]}',
			decode(repeat('24', 32), 'hex'),
			1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'00000000-0000-4000-8000-000000000906',
			'00000000-0000-4000-8000-000000000901',
			'00000000-0000-4000-8000-000000000905',
			'urn:xb:account:effective-leverage-migration',
			'BTC-PERP',
			'BUY',
			'60000'::numeric,
			'0.01'::numeric,
			'00000000-0000-4000-8000-000000000902',
			'open',
			'TAKER',
			1785060000000000001
		)`); err != nil {
		t.Fatalf("seed v3 fill and receipt history: %v", err)
	}
}

func insertFillWithEffectiveLeverage(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fillID string,
	inputID string,
	leverage string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES (
			$1,
			'00000000-0000-4000-8000-000000000901',
			$2,
			'urn:xb:account:effective-leverage-migration',
			'BTC-PERP',
			'BUY',
			'60000'::numeric,
			'0.01'::numeric,
			'00000000-0000-4000-8000-000000000902',
			'open',
			'TAKER',
			1785060000000000000,
			$3::numeric
		)`,
		fillID,
		inputID,
		leverage,
	); err != nil {
		t.Fatalf("insert fill with effective leverage %q: %v", leverage, err)
	}
}

func requireFillEffectiveLeverageSQLState(
	t *testing.T,
	err error,
	code string,
) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != code {
		t.Fatalf("PostgreSQL error = %v, want SQLSTATE %s", err, code)
	}
}
