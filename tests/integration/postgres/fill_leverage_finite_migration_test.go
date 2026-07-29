package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const (
	fillLeverageFinitePreviousMigration    = "20260729000400_phase3_command_admission_acl.up.sql"
	fillLeverageFiniteConstraintMigration  = "20260729000500_phase3_fill_leverage_finite_constraint.up.sql"
	fillLeverageFiniteValidationMigration  = "20260729000600_phase3_validate_fill_leverage_finite.up.sql"
	fillLeverageFiniteConstraintDefinition = "CHECK (((effective_leverage IS NULL) OR ((effective_leverage > (0)::numeric) AND (effective_leverage <> ALL (ARRAY['NaN'::numeric, 'Infinity'::numeric, '-Infinity'::numeric])))))"
)

func TestFillLeverageFiniteConstraintUpgradesCurrentTipWithoutRewrite(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillLeverageFinitePreviousTip(t, ctx, pool, false)

	beforeDigest, beforeRelfilenode := fillLeverageFiniteState(t, ctx, pool)
	beforeCatalog := fillLeverageFiniteCatalogState(t, ctx, pool)
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFiniteValidationMigration),
	)
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply finite fill leverage migrations: %v", err)
	}

	afterDigest, afterRelfilenode := fillLeverageFiniteState(t, ctx, pool)
	afterCatalog := fillLeverageFiniteCatalogState(t, ctx, pool)
	if afterDigest != beforeDigest || afterRelfilenode != beforeRelfilenode {
		t.Fatalf(
			"finite leverage migration changed fill storage: digest %q -> %q relfilenode %d -> %d",
			beforeDigest,
			afterDigest,
			beforeRelfilenode,
			afterRelfilenode,
		)
	}
	if afterCatalog != beforeCatalog {
		t.Fatalf(
			"finite leverage migration changed ACL/default/trigger catalogs: %s -> %s",
			beforeCatalog,
			afterCatalog,
		)
	}
	var (
		count       int
		tip         string
		legacyValid bool
		finiteValid bool
		finiteDef   string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT max(filename) FROM engine.schema_migrations),
			(
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname = 'fills_effective_leverage_positive'
			),
			(
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			),
			(
				SELECT pg_get_constraintdef(oid)
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			)`,
	).Scan(
		&count,
		&tip,
		&legacyValid,
		&finiteValid,
		&finiteDef,
	); err != nil {
		t.Fatalf("inspect finite leverage migration state: %v", err)
	}
	if count != 36 ||
		tip != fillLeverageFiniteValidationMigration ||
		!legacyValid ||
		!finiteValid ||
		finiteDef != fillLeverageFiniteConstraintDefinition {
		t.Fatalf(
			"finite leverage migration state = count %d tip %q legacy-valid %t finite-valid %t definition %q",
			count,
			tip,
			legacyValid,
			finiteValid,
			finiteDef,
		)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify finite leverage migration tip: %v", err)
	}

	insertFillLeverageFiniteProbe(
		t,
		ctx,
		pool,
		"019fae0d-9e95-7000-8000-000000000011",
		"019fae0d-9e95-7000-8000-000000000021",
		nil,
	)
	five := "5.00"
	insertFillLeverageFiniteProbe(
		t,
		ctx,
		pool,
		"019fae0d-9e95-7000-8000-000000000012",
		"019fae0d-9e95-7000-8000-000000000022",
		&five,
	)
	for index, invalid := range []struct {
		value string
		code  string
	}{
		{value: "NaN", code: "23514"},
		{value: "Infinity", code: "22003"},
		{value: "-Infinity", code: "22003"},
		{value: "0", code: "23514"},
		{value: "-1", code: "23514"},
	} {
		_, err := pool.Exec(ctx, `
			INSERT INTO trading.fills (
				fill_id, order_id, input_id, account_id, instrument_id,
				side, price, quantity, position_id, position_effect,
				liquidity_side, logical_time, effective_leverage
			) VALUES (
				format(
					'019fae0d-9e95-7000-8000-%s',
					lpad(($1 + 30)::text, 12, '0')
				)::uuid,
				'019fae0d-9e95-7000-8000-000000000001',
				format(
					'019fae0d-9e95-7000-8000-%s',
					lpad(($1 + 40)::text, 12, '0')
				)::uuid,
				'urn:xb:account:fill-leverage-finite-migration',
				'BTC-PERP', 'BUY', 60000, 0.01,
				'019fae0d-9e95-7000-8000-000000000004',
				'increase', 'TAKER', 1785333600000000100 + $1,
				$2::numeric
			)`,
			index,
			invalid.value,
		)
		requireFillLeverageFiniteSQLState(t, err, invalid.code)
	}
}

func TestFillLeverageFiniteConstraintLockTimeoutRollsBackAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillLeverageFinitePreviousTip(t, ctx, pool, false)
	beforeDigest, beforeRelfilenode := fillLeverageFiniteState(t, ctx, pool)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fill writer blocker: %v", err)
	}
	defer func() { _ = writer.Rollback(context.Background()) }()
	if _, err := writer.Exec(
		ctx,
		"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
	); err != nil {
		t.Fatalf("hold fill writer blocker: %v", err)
	}

	constraint := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFiniteConstraintMigration),
	)
	boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	err = constraint.Migrate(boundedContext)
	requireFillLeverageFiniteSQLState(t, err, "55P03")

	var (
		count            int
		tip              string
		constraintExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			)`,
	).Scan(&count, &tip, &constraintExists); err != nil {
		t.Fatalf("inspect contended constraint migration: %v", err)
	}
	afterDigest, afterRelfilenode := fillLeverageFiniteState(t, ctx, pool)
	if count != 34 ||
		tip != fillLeverageFinitePreviousMigration ||
		constraintExists ||
		afterDigest != beforeDigest ||
		afterRelfilenode != beforeRelfilenode {
		t.Fatalf(
			"contended constraint partially applied: count=%d tip=%q constraint=%t digest %q->%q relfilenode %d->%d",
			count,
			tip,
			constraintExists,
			beforeDigest,
			afterDigest,
			beforeRelfilenode,
			afterRelfilenode,
		)
	}

	if err := writer.Rollback(ctx); err != nil {
		t.Fatalf("release fill writer blocker: %v", err)
	}
	if err := constraint.Migrate(ctx); err != nil {
		t.Fatalf("retry finite leverage constraint: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
				   AND NOT convalidated
			)`,
	).Scan(&count, &tip, &constraintExists); err != nil {
		t.Fatalf("inspect retried constraint migration: %v", err)
	}
	if count != 35 ||
		tip != fillLeverageFiniteConstraintMigration ||
		!constraintExists {
		t.Fatalf(
			"retried constraint state = count %d tip %q unvalidated %t",
			count,
			tip,
			constraintExists,
		)
	}
}

func TestFillLeverageFiniteConstraintRejectsActiveEngineOwnerAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillLeverageFinitePreviousTip(t, ctx, pool, false)

	store := platformpostgres.NewEngineStore(pool)
	owner, err := store.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatalf("acquire pre-migration engine owner: %v", err)
	}
	defer func() { _ = owner.Close(context.Background()) }()
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFiniteConstraintMigration),
	)
	boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	err = current.Migrate(boundedContext)
	requireFillLeverageFiniteSQLState(t, err, "55P03")

	var (
		count            int
		tip              string
		constraintExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT max(filename) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			)`,
	).Scan(&count, &tip, &constraintExists); err != nil {
		_ = owner.Close(ctx)
		t.Fatalf("inspect active-owner migration refusal: %v", err)
	}
	if count != 34 ||
		tip != fillLeverageFinitePreviousMigration ||
		constraintExists {
		_ = owner.Close(ctx)
		t.Fatalf(
			"active-owner migration state = count %d tip %q constraint %t",
			count,
			tip,
			constraintExists,
		)
	}
	if err := owner.Close(ctx); err != nil {
		t.Fatalf("release pre-migration engine owner: %v", err)
	}
	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("retry constraint after engine drain: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify constraint after engine drain: %v", err)
	}
}

func TestFillLeverageFiniteValidationLockCompatibilityTimeoutAndRetry(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)

	t.Run("normal writer is compatible", func(t *testing.T) {
		resetDurableSchemas(t, pool)
		seedFillLeverageFinitePreviousTip(t, ctx, pool, false)
		if err := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, fillLeverageFiniteConstraintMigration),
		).Migrate(ctx); err != nil {
			t.Fatalf("apply finite leverage constraint: %v", err)
		}
		writer, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin compatible fill writer: %v", err)
		}
		defer func() { _ = writer.Rollback(context.Background()) }()
		if _, err := writer.Exec(
			ctx,
			"LOCK TABLE trading.fills IN ROW EXCLUSIVE MODE",
		); err != nil {
			t.Fatalf("hold compatible fill writer: %v", err)
		}
		if err := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, fillLeverageFiniteValidationMigration),
		).Migrate(ctx); err != nil {
			t.Fatalf("validate with normal writer: %v", err)
		}
		var valid bool
		if err := pool.QueryRow(ctx, `
			SELECT convalidated
			  FROM pg_constraint
			 WHERE conrelid = 'trading.fills'::regclass
			   AND conname =
			       'fills_effective_leverage_finite_positive'`,
		).Scan(&valid); err != nil {
			t.Fatalf("inspect compatible validation: %v", err)
		}
		if !valid {
			t.Fatal("compatible validation left constraint unvalidated")
		}
	})

	t.Run("conflicting lock times out and retries", func(t *testing.T) {
		resetDurableSchemas(t, pool)
		seedFillLeverageFinitePreviousTip(t, ctx, pool, false)
		if err := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, fillLeverageFiniteConstraintMigration),
		).Migrate(ctx); err != nil {
			t.Fatalf("apply finite leverage constraint: %v", err)
		}
		blocker, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin validation blocker: %v", err)
		}
		defer func() { _ = blocker.Rollback(context.Background()) }()
		if _, err := blocker.Exec(
			ctx,
			"LOCK TABLE trading.fills IN SHARE MODE",
		); err != nil {
			t.Fatalf("hold validation blocker: %v", err)
		}
		current := platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, fillLeverageFiniteValidationMigration),
		)
		boundedContext, cancel := context.WithTimeout(ctx, 6*time.Second)
		defer cancel()
		err = current.Migrate(boundedContext)
		requireFillLeverageFiniteSQLState(t, err, "55P03")

		var (
			count int
			tip   string
			valid bool
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM engine.schema_migrations),
				(SELECT max(filename) FROM engine.schema_migrations),
				(
					SELECT convalidated
					  FROM pg_constraint
					 WHERE conrelid = 'trading.fills'::regclass
					   AND conname =
					       'fills_effective_leverage_finite_positive'
				)`,
		).Scan(&count, &tip, &valid); err != nil {
			t.Fatalf("inspect blocked validation: %v", err)
		}
		if count != 35 ||
			tip != fillLeverageFiniteConstraintMigration ||
			valid {
			t.Fatalf(
				"blocked validation state = count %d tip %q valid %t",
				count,
				tip,
				valid,
			)
		}
		if err := blocker.Rollback(ctx); err != nil {
			t.Fatalf("release validation blocker: %v", err)
		}
		if err := current.Migrate(ctx); err != nil {
			t.Fatalf("retry finite leverage validation: %v", err)
		}
		if err := current.VerifyCurrent(ctx); err != nil {
			t.Fatalf("verify retried finite leverage validation: %v", err)
		}
	})
}

func TestFillLeverageFiniteValidationCompletesAtRepresentativeScale(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillLeverageFinitePreviousTip(t, ctx, pool, false)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		)
		SELECT
			format(
				'40000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'019fae0d-9e95-7000-8000-000000000001'::uuid,
			format(
				'50000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'urn:xb:account:fill-leverage-finite-migration',
			'BTC-PERP',
			'BUY',
			60000,
			0.01,
			'019fae0d-9e95-7000-8000-000000000004'::uuid,
			'increase',
			'TAKER',
			1785333600001000000 + sequence_number,
			CASE
				WHEN sequence_number % 2 = 0 THEN 5.00
				ELSE 10
			END
		  FROM generate_series(1, 100000) AS sequence(sequence_number)`,
	); err != nil {
		t.Fatalf("seed representative fill leverage pages: %v", err)
	}
	var beforeRelfilenode uint32
	if err := pool.QueryRow(ctx, `
		SELECT relfilenode
		  FROM pg_class
		 WHERE oid = 'trading.fills'::regclass`,
	).Scan(&beforeRelfilenode); err != nil {
		t.Fatalf("read representative fill relfilenode: %v", err)
	}
	started := time.Now()
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFiniteValidationMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("validate representative fill leverage pages: %v", err)
	}
	elapsed := time.Since(started)

	var (
		count       int
		finiteValid bool
		relfilenode uint32
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.fills),
			(
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			),
			(
				SELECT relfilenode
				  FROM pg_class
				 WHERE oid = 'trading.fills'::regclass
			)`,
	).Scan(&count, &finiteValid, &relfilenode); err != nil {
		t.Fatalf("inspect representative leverage validation: %v", err)
	}
	if count != 100002 ||
		!finiteValid ||
		relfilenode != beforeRelfilenode {
		t.Fatalf(
			"representative validation state = count %d valid %t relfilenode %d->%d",
			count,
			finiteValid,
			beforeRelfilenode,
			relfilenode,
		)
	}
	t.Logf(
		"validated %d immutable fill rows within the migration's 30s statement timeout in %s",
		count,
		elapsed,
	)
}

func TestFillLeverageFiniteValidationRefusesPreexistingNaNWithoutMutation(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	seedFillLeverageFinitePreviousTip(t, ctx, pool, true)

	var (
		currentNaNPassesPositive bool
		legacyConstraintValid    bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(
				SELECT effective_leverage > 0
				  FROM trading.fills
				 WHERE effective_leverage = 'NaN'::numeric
			),
			(
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname = 'fills_effective_leverage_positive'
			)`,
	).Scan(
		&currentNaNPassesPositive,
		&legacyConstraintValid,
	); err != nil {
		t.Fatalf("prove current-tip NaN constraint gap: %v", err)
	}
	if !currentNaNPassesPositive || !legacyConstraintValid {
		t.Fatalf(
			"current-tip NaN gap = passes-positive %t legacy-valid %t",
			currentNaNPassesPositive,
			legacyConstraintValid,
		)
	}

	beforeDigest, beforeRelfilenode := fillLeverageFiniteState(t, ctx, pool)
	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFiniteValidationMigration),
	)
	err := current.Migrate(ctx)
	requireFillLeverageFiniteSQLState(t, err, "23514")

	afterDigest, afterRelfilenode := fillLeverageFiniteState(t, ctx, pool)
	if afterDigest != beforeDigest || afterRelfilenode != beforeRelfilenode {
		t.Fatalf(
			"failed validation changed immutable fills: digest %q -> %q relfilenode %d -> %d",
			beforeDigest,
			afterDigest,
			beforeRelfilenode,
			afterRelfilenode,
		)
	}
	var (
		count       int
		tip         string
		finiteValid bool
		nanCount    int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.schema_migrations),
			(SELECT max(filename) FROM engine.schema_migrations),
			(
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname =
				       'fills_effective_leverage_finite_positive'
			),
			(
				SELECT count(*)
				  FROM trading.fills
				 WHERE effective_leverage = 'NaN'::numeric
			)`,
	).Scan(&count, &tip, &finiteValid, &nanCount); err != nil {
		t.Fatalf("inspect refused finite leverage validation: %v", err)
	}
	if count != 35 ||
		tip != fillLeverageFiniteConstraintMigration ||
		finiteValid ||
		nanCount != 1 {
		t.Fatalf(
			"refused validation state = count %d tip %q valid %t NaN %d",
			count,
			tip,
			finiteValid,
			nanCount,
		)
	}
	if err := current.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaBehind,
	) {
		t.Fatalf("candidate verification = %v, want schema-behind", err)
	}
	previous := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFinitePreviousMigration),
	)
	if err := previous.VerifyCurrent(ctx); !errors.Is(
		err,
		platformpostgres.ErrDatabaseSchemaAhead,
	) {
		t.Fatalf("previous verification = %v, want schema-ahead", err)
	}

	nan := "NaN"
	_, err = pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES (
			'019fae0d-9e95-7000-8000-000000000013',
			'019fae0d-9e95-7000-8000-000000000001',
			'019fae0d-9e95-7000-8000-000000000023',
			'urn:xb:account:fill-leverage-finite-migration',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fae0d-9e95-7000-8000-000000000004',
			'increase', 'TAKER', 1785333600000000200, $1::numeric
		)`,
		nan,
	)
	requireFillLeverageFiniteSQLState(t, err, "23514")
}

func seedFillLeverageFinitePreviousTip(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	withNaN bool,
) {
	t.Helper()
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillLeverageFinitePreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatalf("apply current 34-file schema: %v", err)
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
		VALUES ('urn:xb:account:fill-leverage-finite-migration', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fae0d-9e95-7000-8000-000000000001',
			'urn:xb:account:fill-leverage-finite-migration',
			'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			0.02, 0.02, 60000, false, false, false, 1
		)`); err != nil {
		t.Fatalf("seed current-tip finite leverage authority: %v", err)
	}
	var secondLeverage *string
	if withNaN {
		nan := "NaN"
		secondLeverage = &nan
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES
			(
				'019fae0d-9e95-7000-8000-000000000002',
				'019fae0d-9e95-7000-8000-000000000001',
				'019fae0d-9e95-7000-8000-000000000003',
				'urn:xb:account:fill-leverage-finite-migration',
				'BTC-PERP', 'BUY', 60000, 0.01,
				'019fae0d-9e95-7000-8000-000000000004',
				'open', 'TAKER', 1785333600000000000, 5.00
			),
			(
				'019fae0d-9e95-7000-8000-000000000005',
				'019fae0d-9e95-7000-8000-000000000001',
				'019fae0d-9e95-7000-8000-000000000006',
				'urn:xb:account:fill-leverage-finite-migration',
				'BTC-PERP', 'BUY', 60000, 0.01,
				'019fae0d-9e95-7000-8000-000000000004',
				'increase', 'TAKER', 1785333600000000001,
				$1::numeric
			)`,
		secondLeverage,
	); err != nil {
		t.Fatalf("seed current-tip finite leverage history: %v", err)
	}
}

func fillLeverageFiniteState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (string, uint32) {
	t.Helper()
	var (
		digest      string
		relfilenode uint32
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(
				SELECT jsonb_agg(
					jsonb_build_array(
						fill_id::text,
						order_id::text,
						input_id::text,
						account_id,
						instrument_id,
						effective_leverage::text
					)
					ORDER BY fill_id
				)::text
				  FROM trading.fills
			),
			(
				SELECT relfilenode
				  FROM pg_class
				 WHERE oid = 'trading.fills'::regclass
			)`,
	).Scan(&digest, &relfilenode); err != nil {
		t.Fatalf("read finite leverage fill state: %v", err)
	}
	return digest, relfilenode
}

func fillLeverageFiniteCatalogState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var state string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'relacl',
			COALESCE(
				(
					SELECT relacl::text
					  FROM pg_class
					 WHERE oid = 'trading.fills'::regclass
				),
				''
			),
			'triggers',
			COALESCE(
				(
					SELECT jsonb_agg(
						jsonb_build_array(
							tgname,
							pg_get_triggerdef(oid),
							tgenabled
						)
						ORDER BY tgname
					)
					  FROM pg_trigger
					 WHERE tgrelid = 'trading.fills'::regclass
					   AND NOT tgisinternal
				),
				'[]'::jsonb
			),
			'column_acl',
			COALESCE(
				(
					SELECT jsonb_agg(
						jsonb_build_array(attname, attacl::text)
						ORDER BY attname
					)
					  FROM pg_attribute
					 WHERE attrelid = 'trading.fills'::regclass
					   AND NOT attisdropped
					   AND attacl IS NOT NULL
				),
				'[]'::jsonb
			),
			'indexes',
			COALESCE(
				(
					SELECT jsonb_agg(
						pg_get_indexdef(indexrelid)
						ORDER BY indexrelid::regclass::text
					)
					  FROM pg_index
					 WHERE indrelid = 'trading.fills'::regclass
				),
				'[]'::jsonb
			),
			'default_acl',
			COALESCE(
				(
					SELECT jsonb_agg(
						jsonb_build_array(
							defaclrole::regrole::text,
							defaclnamespace,
							defaclobjtype,
							defaclacl::text
						)
						ORDER BY defaclrole, defaclnamespace, defaclobjtype
					)
					  FROM pg_default_acl
				),
				'[]'::jsonb
			)
		)::text`,
	).Scan(&state); err != nil {
		t.Fatalf("read fill leverage ACL/default/trigger catalogs: %v", err)
	}
	return state
}

func insertFillLeverageFiniteProbe(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	fillID string,
	inputID string,
	leverage *string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time, effective_leverage
		) VALUES (
			$1,
			'019fae0d-9e95-7000-8000-000000000001',
			$2,
			'urn:xb:account:fill-leverage-finite-migration',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fae0d-9e95-7000-8000-000000000004',
			'increase', 'TAKER', 1785333600000000002, $3::numeric
		)`,
		fillID,
		inputID,
		leverage,
	); err != nil {
		t.Fatalf("insert accepted finite leverage %v: %v", leverage, err)
	}
}

func requireFillLeverageFiniteSQLState(
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
