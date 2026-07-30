package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

const (
	currencyScaleAuthorityFencePreviousMigration = "20260730000100_phase3_broker_balances_acl.up.sql"
	currencyScaleAuthorityFenceMigration         = "20260730000200_phase3_currency_scale_authority_fence.up.sql"
)

func TestCurrencyScaleAuthorityFenceUpgradesCurrentTip(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate, version
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 8,
			0.1, 0.05, 10, 0, 0, 2
		);
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('JPY', 0)`); err != nil {
		t.Fatalf("seed valid current and historical currency authority: %v", err)
	}
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000901",
		1,
		"BTC-PERP",
		"JPY",
		0,
	)
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000902",
		2,
		"BTC-PERP",
		"USDC",
		8,
	)
	beforeDigest, beforeNode, beforeOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)

	files := migrationFilesThrough(t, currencyScaleAuthorityFenceMigration)
	if _, exists := files[currencyScaleAuthorityFenceMigration]; !exists {
		t.Fatalf(
			"RED: expected forward migration %s is missing after current tip %s",
			currencyScaleAuthorityFenceMigration,
			currencyScaleAuthorityFencePreviousMigration,
		)
	}
	if err := platformpostgres.NewMigrator(pool, files).Migrate(ctx); err != nil {
		t.Fatalf("apply currency-scale authority fence: %v", err)
	}
	afterDigest, afterNode, afterOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)
	if beforeDigest != afterDigest ||
		beforeNode != afterNode ||
		beforeOwner != afterOwner {
		t.Fatalf(
			"authority fence changed registry data/storage/owner: before=%q/%d/%q after=%q/%d/%q",
			beforeDigest,
			beforeNode,
			beforeOwner,
			afterDigest,
			afterNode,
			afterOwner,
		)
	}
	assertCurrencyScaleAuthorityFenceCatalog(t, pool)
	assertFinalMigrationHistory(t, pool)
}

func TestCurrencyScaleAuthorityFenceNeutralizesPrecreatedHostileTrigger(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	hostileRole := fmt.Sprintf("currency_fence_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile currency-fence role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT EXECUTE ON FUNCTIONS TO %[2]s;
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT SELECT ON TABLES TO %[2]s`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFencePreviousMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA trading TO "+hostileID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'HOSTILE-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		t.Fatalf("seed expected pair for hostile trigger probe: %v", err)
	}
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000905",
		1,
		"HOSTILE-PERP",
		"USDC",
		2,
	)
	var hostileExecute bool
	if err := pool.QueryRow(ctx, `
		SELECT has_function_privilege(
			$1,
			'trading.require_currency_scale_consistency()',
			'EXECUTE'
		)`,
		hostileRole,
	).Scan(&hostileExecute); err != nil || !hostileExecute {
		t.Fatalf(
			"hostile named function default execute=%t error=%v",
			hostileExecute,
			err,
		)
	}
	var hostileInstrumentInsert bool
	if err := pool.QueryRow(ctx, `
		SELECT has_table_privilege(
			$1,
			'trading.instruments',
			'INSERT'
		)`,
		hostileRole,
	).Scan(&hostileInstrumentInsert); err != nil || hostileInstrumentInsert {
		t.Fatalf(
			"hostile instrument unexpectedly inserts before scrub=%t error=%v",
			hostileInstrumentInsert,
			err,
		)
	}

	hostileConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hostileConnection.Release()
	if _, err := hostileConnection.Exec(ctx, fmt.Sprintf(`
		SET ROLE %[1]s;
		CREATE TEMP TABLE currency_scale_poison (
			settlement_currency text NOT NULL,
			settlement_currency_scale smallint NOT NULL
		);
		CREATE TRIGGER currency_scale_poison
		BEFORE INSERT ON currency_scale_poison
		FOR EACH ROW EXECUTE FUNCTION
			trading.require_currency_scale_consistency();
		RESET ROLE`,
		hostileID,
	)); err != nil {
		t.Fatalf("create pre-fence hostile trigger: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply authority fence over hostile trigger: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT has_function_privilege(
			$1,
			'trading.require_currency_scale_consistency()',
			'EXECUTE'
		)`,
		hostileRole,
	).Scan(&hostileExecute); err != nil || hostileExecute {
		t.Fatalf(
			"hostile execute after scrub=%t error=%v",
			hostileExecute,
			err,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT has_table_privilege(
			$1,
			'trading.instruments',
			'INSERT'
		)`,
		hostileRole,
	).Scan(&hostileInstrumentInsert); err != nil || hostileInstrumentInsert {
		t.Fatalf(
			"hostile instrument insert after scrub=%t error=%v",
			hostileInstrumentInsert,
			err,
		)
	}
	hostileInstrument, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostileInstrument.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		_ = hostileInstrument.Rollback(ctx)
		t.Fatal(err)
	}
	_, err = hostileInstrument.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'POISON-PERP', 1, 2, 3, 'ZZZ', 18,
			0.1, 0.05, 10, 0, 0
		)`)
	assertCurrencyScaleAuthoritySQLState(t, err, "42501")
	_ = hostileInstrument.Rollback(ctx)
	if _, err := hostileConnection.Exec(ctx, "SET ROLE "+hostileID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		currency string
		scale    int
	}{
		{currency: "ZZZ", scale: 18},
		{currency: "USDC", scale: 2},
	} {
		_, err = hostileConnection.Exec(ctx, `
			INSERT INTO currency_scale_poison
			VALUES ($1, $2)`,
			pair.currency,
			pair.scale,
		)
		assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	}
	if _, err := hostileConnection.Exec(ctx, "RESET ROLE"); err != nil {
		t.Fatal(err)
	}
	var poisoned bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM trading.currency_scales
			 WHERE currency = 'ZZZ'
		)`).Scan(&poisoned); err != nil || poisoned {
		t.Fatalf("hostile registry poison exists=%t error=%v", poisoned, err)
	}
	assertCurrencyScaleAuthorityFenceCatalog(t, pool)
}

func TestCurrencyScaleAuthorityFenceRejectsUntrustedSourceMutationGrant(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	hostileRole := fmt.Sprintf("currency_source_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile source role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatal(err)
	}
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA trading, engine TO %[1]s;
		REVOKE ALL PRIVILEGES ON engine.input_receipts FROM %[1]s CASCADE;
		SET ROLE %[1]s;
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'POISON-PERP', 1, 2, 3, 'ZZZ', 18,
			0.1, 0.05, 10, 0, 0
		);
		RESET ROLE`,
		hostileID,
	)); err != nil {
		t.Fatalf("seed hostile source fact: %v", err)
	}
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000910",
		1,
		"POISON-PERP",
		"ZZZ",
		18,
	)
	beforeDigest, beforeNode, beforeOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	assertCurrencyScaleAuthorityMessage(
		t,
		err,
		"currency authority source carried an unexpected mutation grant before cutover",
	)

	afterDigest, afterNode, afterOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)
	if beforeDigest != afterDigest ||
		beforeNode != afterNode ||
		beforeOwner != afterOwner {
		t.Fatal("failed source-authority migration changed registry evidence")
	}
	var (
		instrumentPreserved bool
		registryPreserved   bool
		grantPreserved      bool
		journaled           bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM trading.instruments
				 WHERE instrument_id = 'POISON-PERP'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'ZZZ' AND scale = 18
			),
			has_table_privilege(
				$1,
				'trading.instruments',
				'INSERT'
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $2
			)`,
		hostileRole,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&instrumentPreserved,
		&registryPreserved,
		&grantPreserved,
		&journaled,
	); err != nil {
		t.Fatal(err)
	}
	if !instrumentPreserved ||
		!registryPreserved ||
		!grantPreserved ||
		journaled {
		t.Fatalf(
			"failed source fence instrument=%t registry=%t grant=%t journaled=%t",
			instrumentPreserved,
			registryPreserved,
			grantPreserved,
			journaled,
		)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsReceiptOnlyMutationGrant(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)

	hostileRole := fmt.Sprintf("currency_receipt_hostile_%d", os.Getpid())
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			DROP OWNED BY %[1]s CASCADE;
			DROP ROLE %[1]s`,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile receipt role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		GRANT USAGE ON SCHEMA engine TO %[1]s;
		GRANT INSERT ON engine.input_receipts TO %[1]s;
		SET ROLE %[1]s;
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260728000200_phase3_command_market_sequence_binding',
				false
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			7, '00000000-0000-4000-8000-000000000906', 1, 1,
			1, decode(repeat('51', 32), 'hex'),
			decode(repeat('52', 32), 'hex'), 1, 4,
			decode(repeat('53', 32), 'hex'),
			decode(repeat('54', 32), 'hex'), '{}',
			'{
				"CommandResult":{"Status":"accepted"},
				"InstrumentChanges":[{
					"InstrumentID":"ZZQ-PERP",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZQ",
					"SettlementCurrencyScale":15,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}'
		);
		RESET ROLE;
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('ZZQ', 15)`,
		hostileID,
	)); err != nil {
		t.Fatalf("seed hostile receipt-only authority: %v", err)
	}

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	assertCurrencyScaleAuthorityMessage(
		t,
		err,
		"currency authority source carried an unexpected mutation grant before cutover",
	)

	var receiptPreserved, registryPreserved, grantPreserved, journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.input_receipts
				 WHERE input_id =
				       '00000000-0000-4000-8000-000000000906'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'ZZQ' AND scale = 15
			),
			has_table_privilege(
				$1,
				'engine.input_receipts',
				'INSERT'
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $2
			)`,
		hostileRole,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&receiptPreserved,
		&registryPreserved,
		&grantPreserved,
		&journaled,
	); err != nil {
		t.Fatal(err)
	}
	if !receiptPreserved ||
		!registryPreserved ||
		!grantPreserved ||
		journaled {
		t.Fatalf(
			"receipt-only fence receipt=%t registry=%t grant=%t journaled=%t",
			receiptPreserved,
			registryPreserved,
			grantPreserved,
			journaled,
		)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsCurrentOnlyInstrument(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'CURRENT-ONLY-PERP', 1, 2, 3, 'ZZC', 14,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		t.Fatal(err)
	}

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")

	var instrumentPreserved, registryPreserved, journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM trading.instruments
				 WHERE instrument_id = 'CURRENT-ONLY-PERP'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'ZZC' AND scale = 14
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&instrumentPreserved,
		&registryPreserved,
		&journaled,
	); err != nil {
		t.Fatal(err)
	}
	if !instrumentPreserved || !registryPreserved || journaled {
		t.Fatalf(
			"current-only evidence instrument=%t registry=%t journaled=%t",
			instrumentPreserved,
			registryPreserved,
			journaled,
		)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsMalformedHistoricalInstrumentIdentity(
	t *testing.T,
) {
	for _, test := range []struct {
		name       string
		identityKV string
	}{
		{name: "missing", identityKV: ""},
		{name: "wrong type", identityKV: `"InstrumentID":7,`},
		{name: "empty", identityKV: `"InstrumentID":"",`},
		{name: "whitespace", identityKV: `"InstrumentID":" BAD-PERP",`},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			decision := fmt.Sprintf(`{
				"CommandResult":{"Status":"accepted"},
				"InstrumentChanges":[{
					%s
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZM",
					"SettlementCurrencyScale":13,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}`, test.identityKV)
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.currency_scales (currency, scale)
				VALUES ('ZZM', 13);
				SELECT
					set_config(
						'platformgo.runtime_schema_revision',
						'20260728000200_phase3_command_market_sequence_binding',
						false
					),
					set_config(
						'platformgo.engine_decision_hash_version',
						'4',
						false
					)`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO engine.input_receipts (
					shard_id, input_id, stream_sequence, schema_version,
					input_hash_version, input_hash, business_input_hash,
					business_input_hash_version, decision_hash_version,
					decision_hash, resulting_state_hash, envelope, decision
				) VALUES (
					7, '00000000-0000-4000-8000-000000000909', 1, 1,
					1, decode(repeat('71', 32), 'hex'),
					decode(repeat('72', 32), 'hex'), 1, 4,
					decode(repeat('73', 32), 'hex'),
					decode(repeat('74', 32), 'hex'), '{}', $1::jsonb
				)`,
				decision,
			); err != nil {
				t.Fatal(err)
			}
			beforeDigest, beforeNode, beforeOwner :=
				readCurrencyScaleAuthorityRelation(t, pool)
			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			afterDigest, afterNode, afterOwner :=
				readCurrencyScaleAuthorityRelation(t, pool)
			if beforeDigest != afterDigest ||
				beforeNode != afterNode ||
				beforeOwner != afterOwner {
				t.Fatal("malformed history changed registry evidence")
			}
			var journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM engine.schema_migrations
					 WHERE filename = $1
				)`,
				currencyScaleAuthorityFenceMigration,
			).Scan(&journaled); err != nil || journaled {
				t.Fatalf(
					"malformed history journaled=%t error=%v",
					journaled,
					err,
				)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceRejectsMissingCurrentInstrumentProjection(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('ZZN', 11)`); err != nil {
		t.Fatal(err)
	}
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000918",
		1,
		"MISSING-PERP",
		"ZZN",
		11,
	)

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	assertCurrencyScaleAuthorityMessage(
		t,
		err,
		"current instrument projection does not equal accepted durable history",
	)
	var receiptPreserved, registryPreserved, journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM engine.input_receipts
				 WHERE input_id =
				       '00000000-0000-4000-8000-000000000918'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'ZZN' AND scale = 11
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&receiptPreserved,
		&registryPreserved,
		&journaled,
	); err != nil {
		t.Fatal(err)
	}
	if !receiptPreserved || !registryPreserved || journaled {
		t.Fatalf(
			"missing projection receipt=%t registry=%t journaled=%t",
			receiptPreserved,
			registryPreserved,
			journaled,
		)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsImpossibleInstrumentEffectCardinality(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.currency_scales (currency, scale)
		VALUES ('ZZK', 10);
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260728000200_phase3_command_market_sequence_binding',
				false
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			7, '00000000-0000-4000-8000-000000000919', 1, 1,
			1, decode(repeat('91', 32), 'hex'),
			decode(repeat('92', 32), 'hex'), 1, 4,
			decode(repeat('93', 32), 'hex'),
			decode(repeat('94', 32), 'hex'), '{}',
			'{
				"CommandResult":{"Status":"accepted"},
				"InstrumentChanges":[{
					"InstrumentID":"CARDINALITY-A",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZK",
					"SettlementCurrencyScale":10,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				},{
					"InstrumentID":"CARDINALITY-B",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZK",
					"SettlementCurrencyScale":10,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}'
		)`); err != nil {
		t.Fatal(err)
	}

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	assertCurrencyScaleAuthorityMessage(
		t,
		err,
		"instrument change history has an impossible effect cardinality",
	)
}

func TestCurrencyScaleAuthorityFenceRejectsFullInstrumentProjectionMismatch(
	t *testing.T,
) {
	for _, test := range []struct {
		name   string
		update string
	}{
		{name: "revision", update: "revision = 9"},
		{name: "price scale", update: "price_scale = 7"},
		{name: "quantity scale", update: "quantity_scale = 6"},
		{name: "initial margin", update: "initial_margin_rate = 0.2"},
		{name: "maintenance margin", update: "maintenance_margin_rate = 0.1"},
		{name: "max leverage", update: "max_leverage = 5"},
		{name: "maker fee", update: "maker_fee_rate = 0.01"},
		{name: "taker fee", update: "taker_fee_rate = 0.02"},
		{name: "projection version", update: "version = 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.instruments (
					instrument_id, revision, price_scale, quantity_scale,
					settlement_currency, settlement_currency_scale,
					initial_margin_rate, maintenance_margin_rate, max_leverage,
					maker_fee_rate, taker_fee_rate
				) VALUES (
					'PROJECTION-PERP', 1, 2, 3, 'USDC', 8,
					0.1, 0.05, 10, 0, 0
				)`); err != nil {
				t.Fatal(err)
			}
			seedAcceptedInstrumentCurrencyReceipt(
				t,
				pool,
				"00000000-0000-4000-8000-000000000916",
				1,
				"PROJECTION-PERP",
				"USDC",
				8,
			)
			if _, err := pool.Exec(ctx, fmt.Sprintf(`
				UPDATE trading.instruments
				   SET %s
				 WHERE instrument_id = 'PROJECTION-PERP'`,
				test.update,
			)); err != nil {
				t.Fatal(err)
			}

			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			assertCurrencyScaleAuthorityMessage(
				t,
				err,
				"current instrument projection does not equal accepted durable history",
			)
			var journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM engine.schema_migrations
					 WHERE filename = $1
				)`,
				currencyScaleAuthorityFenceMigration,
			).Scan(&journaled); err != nil || journaled {
				t.Fatalf(
					"mismatched projection journaled=%t error=%v",
					journaled,
					err,
				)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceRejectsInvalidExactInstrumentValues(
	t *testing.T,
) {
	for _, test := range []struct {
		name        string
		initial     string
		maintenance string
		maxLeverage string
		maker       string
		taker       string
		message     string
	}{
		{
			name:    "trailing zero",
			initial: "0.10", maintenance: "0.05", maxLeverage: "10",
			maker: "0", taker: "0",
			message: "instrument change history has a noncanonical exact value",
		},
		{
			name:    "negative zero",
			initial: "0.1", maintenance: "-0.0", maxLeverage: "10",
			maker: "0", taker: "0",
			message: "instrument change history has a noncanonical exact value",
		},
		{
			name:    "scale overflow",
			initial: "0.0000000000000000001", maintenance: "0.05",
			maxLeverage: "10", maker: "0", taker: "0",
			message: "instrument change history has a noncanonical exact value",
		},
		{
			name:    "negative initial",
			initial: "-1", maintenance: "0.05", maxLeverage: "10",
			maker: "0", taker: "0",
			message: "instrument change history has an out-of-domain exact value",
		},
		{
			name:    "negative maintenance",
			initial: "0.1", maintenance: "-1", maxLeverage: "10",
			maker: "0", taker: "0",
			message: "instrument change history has an out-of-domain exact value",
		},
		{
			name:    "zero leverage",
			initial: "0.1", maintenance: "0.05", maxLeverage: "0",
			maker: "0", taker: "0",
			message: "instrument change history has an out-of-domain exact value",
		},
		{
			name:    "maker above one",
			initial: "0.1", maintenance: "0.05", maxLeverage: "10",
			maker: "2", taker: "0",
			message: "instrument change history has an out-of-domain exact value",
		},
		{
			name:    "taker below negative one",
			initial: "0.1", maintenance: "0.05", maxLeverage: "10",
			maker: "0", taker: "-2",
			message: "instrument change history has an out-of-domain exact value",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			decision := fmt.Sprintf(`{
				"CommandResult":{"Status":"accepted"},
				"InstrumentChanges":[{
					"InstrumentID":"EXACT-PERP",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZD",
					"SettlementCurrencyScale":12,
					"InitialMarginRate":%q,
					"MaintenanceMarginRate":%q,
					"MaxLeverage":%q,
					"MakerFeeRate":%q,
					"TakerFeeRate":%q
				}]
			}`,
				test.initial,
				test.maintenance,
				test.maxLeverage,
				test.maker,
				test.taker,
			)
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.currency_scales (currency, scale)
				VALUES ('ZZD', 12);
				SELECT
					set_config(
						'platformgo.runtime_schema_revision',
						'20260728000200_phase3_command_market_sequence_binding',
						false
					),
					set_config(
						'platformgo.engine_decision_hash_version',
						'4',
						false
					)`); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO engine.input_receipts (
					shard_id, input_id, stream_sequence, schema_version,
					input_hash_version, input_hash, business_input_hash,
					business_input_hash_version, decision_hash_version,
					decision_hash, resulting_state_hash, envelope, decision
				) VALUES (
					7, '00000000-0000-4000-8000-000000000917', 1, 1,
					1, decode(repeat('81', 32), 'hex'),
					decode(repeat('82', 32), 'hex'), 1, 4,
					decode(repeat('83', 32), 'hex'),
					decode(repeat('84', 32), 'hex'), '{}', $1::jsonb
				)`,
				decision,
			); err != nil {
				t.Fatal(err)
			}

			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			assertCurrencyScaleAuthorityMessage(t, err, test.message)
			var journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1 FROM engine.schema_migrations
					 WHERE filename = $1
				)`,
				currencyScaleAuthorityFenceMigration,
			).Scan(&journaled); err != nil || journaled {
				t.Fatalf(
					"invalid exact history journaled=%t error=%v",
					journaled,
					err,
				)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceRejectsUnexpectedAuthorityTrigger(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		relation string
	}{
		{name: "instruments", relation: "trading.instruments"},
		{name: "registry", relation: "trading.currency_scales"},
		{name: "receipts", relation: "engine.input_receipts"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			if _, err := pool.Exec(ctx, fmt.Sprintf(`
				CREATE FUNCTION trading.zzz_currency_authority_hostile_trigger()
				RETURNS trigger
				LANGUAGE plpgsql
				AS $body$
				BEGIN
					RETURN NEW;
				END
				$body$;
				CREATE TRIGGER zzz_currency_authority_hostile_trigger
				BEFORE INSERT ON %s
				FOR EACH ROW EXECUTE FUNCTION
					trading.zzz_currency_authority_hostile_trigger()`,
				test.relation,
			)); err != nil {
				t.Fatal(err)
			}

			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			assertCurrencyScaleAuthorityMessage(
				t,
				err,
				"currency authority relation has an unexpected pre-cutover trigger",
			)
			var triggerPreserved, journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT
					EXISTS (
						SELECT 1
						  FROM pg_catalog.pg_trigger
						 WHERE tgrelid = $1::pg_catalog.regclass
						   AND tgname =
						       'zzz_currency_authority_hostile_trigger'
						   AND NOT tgisinternal
					),
					EXISTS (
						SELECT 1 FROM engine.schema_migrations
						 WHERE filename = $2
					)`,
				test.relation,
				currencyScaleAuthorityFenceMigration,
			).Scan(&triggerPreserved, &journaled); err != nil {
				t.Fatal(err)
			}
			if !triggerPreserved || journaled {
				t.Fatalf(
					"unexpected trigger preserved=%t journaled=%t",
					triggerPreserved,
					journaled,
				)
			}
			if _, err := pool.Exec(ctx, fmt.Sprintf(`
				DROP TRIGGER zzz_currency_authority_hostile_trigger ON %s;
				DROP FUNCTION trading.zzz_currency_authority_hostile_trigger()`,
				test.relation,
			)); err != nil {
				t.Fatal(err)
			}
			if err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx); err != nil {
				t.Fatalf("retry after classified trigger removal: %v", err)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceRejectsMissingExpectedAuthorityTrigger(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		DROP TRIGGER input_receipts_require_runtime_schema_revision
		ON engine.input_receipts`); err != nil {
		t.Fatal(err)
	}

	err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	assertCurrencyScaleAuthorityMessage(
		t,
		err,
		"currency authority relation has an unexpected pre-cutover trigger",
	)
	var triggerMissing, journaled bool
	if err := pool.QueryRow(ctx, `
		SELECT
			NOT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_trigger
				 WHERE tgrelid =
				       'engine.input_receipts'::pg_catalog.regclass
				   AND tgname =
				       'input_receipts_require_runtime_schema_revision'
				   AND NOT tgisinternal
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $1
			)`,
		currencyScaleAuthorityFenceMigration,
	).Scan(&triggerMissing, &journaled); err != nil {
		t.Fatal(err)
	}
	if !triggerMissing || journaled {
		t.Fatalf(
			"missing trigger preserved=%t journaled=%t",
			triggerMissing,
			journaled,
		)
	}
	if _, err := pool.Exec(ctx, `
		CREATE TRIGGER input_receipts_require_runtime_schema_revision
		BEFORE INSERT ON engine.input_receipts
		FOR EACH ROW EXECUTE FUNCTION
			engine.require_runtime_schema_revision()`); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("retry after expected trigger restoration: %v", err)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsCommittedPreRevocationInstrumentWriter(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	hostileRole := fmt.Sprintf("currency_race_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON TABLES FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup hostile writer role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT ALL PRIVILEGES ON TABLES TO %[2]s`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatal(err)
	}
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA trading TO "+hostileID); err != nil {
		t.Fatal(err)
	}

	writer, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Release()
	var writerPID int32
	if err := writer.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&writerPID); err != nil {
		t.Fatal(err)
	}
	writerTx, err := writer.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writerTx.Rollback(context.Background()) }()
	if _, err := writerTx.Exec(ctx, "SET LOCAL ROLE "+hostileID); err != nil {
		t.Fatal(err)
	}
	if _, err := writerTx.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'RACE-POISON-PERP', 1, 2, 3, 'ZZR', 17,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		t.Fatal(err)
	}

	migrationResult := make(chan error, 1)
	go func() {
		migrationResult <- platformpostgres.NewMigrator(
			pool,
			migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
		).Migrate(ctx)
	}()
	waitForCurrencyScaleRelationBlocker(
		t,
		ctx,
		pool,
		"trading.instruments",
		"ShareRowExclusiveLock",
		writerPID,
	)
	if err := writerTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-migrationResult
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")

	var (
		instrumentPreserved bool
		registryPreserved   bool
		journaled           bool
		newGuardExists      bool
		newRevisionBody     bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM trading.instruments
				 WHERE instrument_id = 'RACE-POISON-PERP'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'ZZR' AND scale = 17
			),
			EXISTS (
				SELECT 1 FROM engine.schema_migrations
				 WHERE filename = $1
			),
			to_regprocedure(
				'trading.require_currency_scale_registry_authority()'
			) IS NOT NULL,
			position(
				'20260730000200_phase3_currency_scale_authority_fence'
				IN pg_get_functiondef(
					'engine.require_runtime_schema_revision()'::regprocedure
				)
			) > 0`,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&instrumentPreserved,
		&registryPreserved,
		&journaled,
		&newGuardExists,
		&newRevisionBody,
	); err != nil {
		t.Fatal(err)
	}
	if !instrumentPreserved ||
		!registryPreserved ||
		journaled ||
		newGuardExists ||
		newRevisionBody {
		t.Fatalf(
			"writer-race evidence instrument=%t registry=%t journaled=%t guard=%t revision=%t",
			instrumentPreserved,
			registryPreserved,
			journaled,
			newGuardExists,
			newRevisionBody,
		)
	}
}

func TestCurrencyScaleAuthorityFenceStopsPreviouslyLoadedDefinerBody(
	t *testing.T,
) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)

	var owner string
	if err := pool.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	hostileRole := fmt.Sprintf("currency_loaded_hostile_%d", os.Getpid())
	ownerID := pgx.Identifier{owner}.Sanitize()
	hostileID := pgx.Identifier{hostileRole}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE ROLE "+hostileID+" NOLOGIN"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), fmt.Sprintf(`
			ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
				REVOKE ALL PRIVILEGES ON FUNCTIONS FROM %[2]s;
			DROP OWNED BY %[2]s CASCADE;
			DROP ROLE %[2]s`,
			ownerID,
			hostileID,
		)); err != nil {
			t.Errorf("cleanup loaded-body hostile role: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf(`
		ALTER DEFAULT PRIVILEGES FOR ROLE %[1]s
			GRANT EXECUTE ON FUNCTIONS TO %[2]s`,
		ownerID,
		hostileID,
	)); err != nil {
		t.Fatal(err)
	}
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		CREATE OR REPLACE FUNCTION trading.require_currency_scale_consistency()
		RETURNS trigger
		LANGUAGE plpgsql
		SECURITY DEFINER
		SET search_path = pg_catalog
		AS $body$
		BEGIN
			PERFORM pg_catalog.pg_advisory_xact_lock(1732050807, 223606797);
			INSERT INTO trading.currency_scales (currency, scale)
			VALUES (NEW.settlement_currency, NEW.settlement_currency_scale)
			ON CONFLICT (currency) DO NOTHING;
			RETURN NEW;
		END
		$body$`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT USAGE ON SCHEMA trading TO "+hostileID); err != nil {
		t.Fatal(err)
	}

	invoker, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer invoker.Release()
	if _, err := invoker.Exec(ctx, fmt.Sprintf(`
		SET ROLE %[1]s;
		CREATE TEMP TABLE currency_scale_loaded_body (
			settlement_currency text NOT NULL,
			settlement_currency_scale smallint NOT NULL
		);
		CREATE TRIGGER currency_scale_loaded_body
		BEFORE INSERT ON currency_scale_loaded_body
		FOR EACH ROW EXECUTE FUNCTION
			trading.require_currency_scale_consistency();
		RESET ROLE`,
		hostileID,
	)); err != nil {
		t.Fatal(err)
	}
	var invokerPID int32
	if err := invoker.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&invokerPID); err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `
		SELECT pg_catalog.pg_advisory_xact_lock(1732050807, 223606797)`); err != nil {
		t.Fatal(err)
	}

	invocationResult := make(chan error, 1)
	go func() {
		_, invokeErr := invoker.Exec(ctx, fmt.Sprintf(`
			SET ROLE %[1]s;
			INSERT INTO currency_scale_loaded_body
			VALUES ('ZZL', 16)`,
			hostileID,
		))
		invocationResult <- invokeErr
	}()
	waitForCurrencyScaleBackendBlocker(t, ctx, pool, invokerPID)

	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("apply fence while old function body is loaded: %v", err)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-invocationResult
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")

	var poisoned bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM trading.currency_scales
			 WHERE currency = 'ZZL'
		)`,
	).Scan(&poisoned); err != nil || poisoned {
		t.Fatalf("loaded old body poisoned registry=%t error=%v", poisoned, err)
	}
	assertCurrencyScaleAuthorityFenceCatalog(t, pool)
}

func TestCurrencyScaleAuthorityFenceSameScaleFastPathDoesNotReadReceipts(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'FAST-PERP', 1, 2, 3, 'USDC', 8,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		t.Fatal(err)
	}
	seedAcceptedInstrumentCurrencyReceipt(
		t,
		pool,
		"00000000-0000-4000-8000-000000000907",
		1,
		"FAST-PERP",
		"USDC",
		8,
	)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, `
		LOCK TABLE engine.input_receipts IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	updateTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = updateTx.Rollback(context.Background()) }()
	if _, err := updateTx.Exec(ctx, `
		SET LOCAL lock_timeout = '250ms';
		UPDATE trading.instruments
		   SET revision = revision + 1
		 WHERE instrument_id = 'FAST-PERP'`); err != nil {
		t.Fatalf("same-scale update consulted locked receipt history: %v", err)
	}
	if err := updateTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestCurrencyScaleAuthorityFenceRejectsRegistryMismatchWithoutRepair(
	t *testing.T,
) {
	for _, test := range []struct {
		name    string
		corrupt string
	}{
		{
			name: "extra",
			corrupt: `
				INSERT INTO trading.currency_scales (currency, scale)
				VALUES ('ZZZ', 18)`,
		},
		{
			name: "missing",
			corrupt: `
				ALTER TABLE trading.currency_scales
					DISABLE TRIGGER currency_scale_registry_is_append_only;
				DELETE FROM trading.currency_scales WHERE currency = 'USDC';
				ALTER TABLE trading.currency_scales
					ENABLE TRIGGER currency_scale_registry_is_append_only`,
		},
		{
			name: "scale mismatch",
			corrupt: `
				ALTER TABLE trading.currency_scales
					DISABLE TRIGGER currency_scale_registry_is_append_only;
				UPDATE trading.currency_scales
				   SET scale = 7
				 WHERE currency = 'USDC';
				ALTER TABLE trading.currency_scales
					ENABLE TRIGGER currency_scale_registry_is_append_only`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.instruments (
					instrument_id, revision, price_scale, quantity_scale,
					settlement_currency, settlement_currency_scale,
					initial_margin_rate, maintenance_margin_rate, max_leverage,
					maker_fee_rate, taker_fee_rate
				) VALUES (
					'BTC-PERP', 1, 2, 3, 'USDC', 8,
					0.1, 0.05, 10, 0, 0
				)`); err != nil {
				t.Fatal(err)
			}
			seedAcceptedInstrumentCurrencyReceipt(
				t,
				pool,
				"00000000-0000-4000-8000-000000000904",
				1,
				"BTC-PERP",
				"USDC",
				8,
			)
			if _, err := pool.Exec(ctx, test.corrupt); err != nil {
				t.Fatalf("corrupt registry fixture: %v", err)
			}
			beforeDigest, beforeNode, beforeOwner :=
				readCurrencyScaleAuthorityRelation(t, pool)
			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			afterDigest, afterNode, afterOwner :=
				readCurrencyScaleAuthorityRelation(t, pool)
			if beforeDigest != afterDigest ||
				beforeNode != afterNode ||
				beforeOwner != afterOwner {
				t.Fatal("failed authority migration repaired or rewrote registry")
			}
			var journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT EXISTS (
					SELECT 1
					  FROM engine.schema_migrations
					 WHERE filename = $1
				)`,
				currencyScaleAuthorityFenceMigration,
			).Scan(&journaled); err != nil || journaled {
				t.Fatalf(
					"failed authority migration journaled=%t error=%v",
					journaled,
					err,
				)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceRejectsNonAcceptedHistoricalAuthority(
	t *testing.T,
) {
	for _, test := range []struct {
		name     string
		decision string
	}{
		{
			name: "rejected",
			decision: `{
				"CommandResult":{"Status":"rejected"},
				"InstrumentChanges":[{
					"InstrumentID":"ZZZ-PERP",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZZ",
					"SettlementCurrencyScale":18,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}`,
		},
		{
			name: "missing status",
			decision: `{
				"InstrumentChanges":[{
					"InstrumentID":"ZZZ-PERP",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZZ",
					"SettlementCurrencyScale":18,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}`,
		},
		{
			name: "malformed status",
			decision: `{
				"CommandResult":{"Status":7},
				"InstrumentChanges":[{
					"InstrumentID":"ZZZ-PERP",
					"Revision":1,
					"PriceScale":2,
					"QuantityScale":3,
					"SettlementCurrency":"ZZZ",
					"SettlementCurrencyScale":18,
					"InitialMarginRate":"0.1",
					"MaintenanceMarginRate":"0.05",
					"MaxLeverage":"10",
					"MakerFeeRate":"0",
					"TakerFeeRate":"0"
				}]
			}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateCurrencyScaleAuthorityPreviousTip(t, pool)
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.currency_scales (currency, scale)
				VALUES ('ZZZ', 18);
				SELECT
					set_config(
						'platformgo.runtime_schema_revision',
						'20260728000200_phase3_command_market_sequence_binding',
						false
					),
					set_config(
						'platformgo.engine_decision_hash_version',
						'4',
						false
					)`); err != nil {
				t.Fatalf("configure non-accepted history fixture: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO engine.input_receipts (
					shard_id, input_id, stream_sequence, schema_version,
					input_hash_version, input_hash, business_input_hash,
					business_input_hash_version, decision_hash_version,
					decision_hash, resulting_state_hash, envelope, decision
				) VALUES (
					7, '00000000-0000-4000-8000-000000000903', 1, 1,
					1, decode(repeat('31', 32), 'hex'),
					decode(repeat('32', 32), 'hex'), 1, 4,
					decode(repeat('33', 32), 'hex'),
					decode(repeat('34', 32), 'hex'), '{}', $1::jsonb
				)`,
				test.decision,
			); err != nil {
				t.Fatalf("seed non-accepted historical authority: %v", err)
			}
			err := platformpostgres.NewMigrator(
				pool,
				migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
			).Migrate(ctx)
			assertCurrencyScaleAuthoritySQLState(t, err, "55000")
			var registryPreserved, journaled bool
			if err := pool.QueryRow(ctx, `
				SELECT
					EXISTS (
						SELECT 1 FROM trading.currency_scales
						 WHERE currency = 'ZZZ' AND scale = 18
					),
					EXISTS (
						SELECT 1 FROM engine.schema_migrations
						 WHERE filename = $1
					)`,
				currencyScaleAuthorityFenceMigration,
			).Scan(&registryPreserved, &journaled); err != nil {
				t.Fatal(err)
			}
			if !registryPreserved || journaled {
				t.Fatalf(
					"non-accepted authority preserved=%t journaled=%t",
					registryPreserved,
					journaled,
				)
			}
		})
	}
}

func TestCurrencyScaleAuthorityFenceLockTimeoutRollsBackAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateCurrencyScaleAuthorityPreviousTip(t, pool)

	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260728000200_phase3_command_market_sequence_binding',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			7, '00000000-0000-4000-8000-000000000908', 1, 1,
			1, decode(repeat('61', 32), 'hex'),
			decode(repeat('62', 32), 'hex'), 1, 4,
			decode(repeat('63', 32), 'hex'),
			decode(repeat('64', 32), 'hex'), '{}',
			'{"CommandResult":{"Status":"accepted"}}'
		)`); err != nil {
		_ = writer.Rollback(ctx)
		t.Fatal(err)
	}
	beforeDigest, beforeNode, beforeOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)
	migrator := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	)
	err = migrator.Migrate(ctx)
	assertCurrencyScaleAuthoritySQLState(t, err, "55P03")
	var journaled, newGuardExists, newRevisionBody bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1
				  FROM engine.schema_migrations
				 WHERE filename = $1
			),
			to_regprocedure(
				'trading.require_currency_scale_registry_authority()'
			) IS NOT NULL,
			position(
				'20260730000200_phase3_currency_scale_authority_fence'
				IN pg_get_functiondef(
					'engine.require_runtime_schema_revision()'::regprocedure
				)
			) > 0`,
		currencyScaleAuthorityFenceMigration,
	).Scan(
		&journaled,
		&newGuardExists,
		&newRevisionBody,
	); err != nil || journaled || newGuardExists || newRevisionBody {
		_ = writer.Rollback(ctx)
		t.Fatalf(
			"timed-out authority migration journaled=%t guard=%t revision=%t error=%v",
			journaled,
			newGuardExists,
			newRevisionBody,
			err,
		)
	}
	afterDigest, afterNode, afterOwner :=
		readCurrencyScaleAuthorityRelation(t, pool)
	if beforeDigest != afterDigest ||
		beforeNode != afterNode ||
		beforeOwner != afterOwner {
		_ = writer.Rollback(ctx)
		t.Fatal("final-lock timeout changed registry data, storage, or owner")
	}
	if err := writer.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry authority migration after writer rollback: %v", err)
	}
	assertCurrencyScaleAuthorityFenceCatalog(t, pool)
}

func TestCurrencyScaleAuthorityFenceRejectsPreverifiedOldRuntimeAtomically(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFenceMigration),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260728000200_phase3_command_market_sequence_binding',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'OLD-PERP', 1, 2, 3, 'OLD', 4,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("stage old-runtime instrument write: %v", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			7, '00000000-0000-4000-8000-000000000902', 1, 1,
			1, decode(repeat('21', 32), 'hex'),
			decode(repeat('22', 32), 'hex'), 1, 4,
			decode(repeat('23', 32), 'hex'),
			decode(repeat('24', 32), 'hex'), '{}',
			'{"InstrumentChanges":[{
				"SettlementCurrency":"OLD",
				"SettlementCurrencyScale":4
			}]}'
		)`)
	assertCurrencyScaleAuthoritySQLState(t, err, "55000")
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var instrumentExists, registryExists, receiptExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			EXISTS (
				SELECT 1 FROM trading.instruments
				 WHERE instrument_id = 'OLD-PERP'
			),
			EXISTS (
				SELECT 1 FROM trading.currency_scales
				 WHERE currency = 'OLD'
			),
			EXISTS (
				SELECT 1 FROM engine.input_receipts
				 WHERE input_id =
				       '00000000-0000-4000-8000-000000000902'
			)`,
	).Scan(
		&instrumentExists,
		&registryExists,
		&receiptExists,
	); err != nil {
		t.Fatal(err)
	}
	if instrumentExists || registryExists || receiptExists {
		t.Fatalf(
			"old runtime committed partial authority instrument=%t registry=%t receipt=%t",
			instrumentExists,
			registryExists,
			receiptExists,
		)
	}
}

func migrateCurrencyScaleAuthorityPreviousTip(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if err := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, currencyScaleAuthorityFencePreviousMigration),
	).MigrateAndProvision(context.Background(), 7); err != nil {
		t.Fatalf("apply previous currency-scale authority tip: %v", err)
	}
}

func seedAcceptedInstrumentCurrencyReceipt(
	t *testing.T,
	pool *pgxpool.Pool,
	inputID string,
	streamSequence int,
	instrumentID string,
	currency string,
	scale int,
) {
	t.Helper()
	seedAcceptedInstrumentCurrencyReceiptForShard(
		t,
		pool,
		7,
		"20260728000200_phase3_command_market_sequence_binding",
		inputID,
		streamSequence,
		instrumentID,
		currency,
		scale,
		"0.1",
		"0.05",
		"10",
		"0",
		"0",
	)
}

func seedAcceptedInstrumentCurrencyReceiptForShard(
	t *testing.T,
	pool *pgxpool.Pool,
	shardID int,
	runtimeRevision string,
	inputID string,
	streamSequence int,
	instrumentID string,
	currency string,
	scale int,
	initialMargin string,
	maintenanceMargin string,
	maxLeverage string,
	makerFee string,
	takerFee string,
) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				$1,
				false
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			)`,
		runtimeRevision,
	); err != nil {
		t.Fatalf("configure accepted instrument receipt fixture: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			$1::integer, $2::uuid, $3::bigint, 1,
			1, decode(repeat('41', 32), 'hex'),
			decode(repeat('42', 32), 'hex'), 1, 4,
			decode(repeat('43', 32), 'hex'),
			decode(repeat('44', 32), 'hex'), '{}',
			pg_catalog.jsonb_build_object(
				'CommandResult',
				pg_catalog.jsonb_build_object('Status', 'accepted'),
				'InstrumentChanges',
				pg_catalog.jsonb_build_array(
					pg_catalog.jsonb_build_object(
						'InstrumentID', $4::text,
						'Revision', 1,
						'PriceScale', 2,
						'QuantityScale', 3,
						'SettlementCurrency', $5::text,
						'SettlementCurrencyScale', $6::integer,
						'InitialMarginRate', $7::text,
						'MaintenanceMarginRate', $8::text,
						'MaxLeverage', $9::text,
						'MakerFeeRate', $10::text,
						'TakerFeeRate', $11::text
					)
				)
			)
		)`,
		shardID,
		inputID,
		streamSequence,
		instrumentID,
		currency,
		scale,
		initialMargin,
		maintenanceMargin,
		maxLeverage,
		makerFee,
		takerFee,
	); err != nil {
		t.Fatalf("seed accepted instrument currency receipt: %v", err)
	}
}

func seedRecoverableAcceptedInstrumentReceiptForShard(
	t *testing.T,
	pool *pgxpool.Pool,
	shardID int,
	runtimeRevision string,
	inputIDText string,
	instrumentID string,
	currency string,
	scale int,
) {
	t.Helper()
	inputID, err := engine.ParseID(inputIDText)
	if err != nil {
		t.Fatal(err)
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            instrumentID,
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      currency,
			SettlementCurrencyScale: uint8(scale),
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	input, err := (testkit.TradingInput{
		InputID:              inputID,
		ShardID:              engine.ShardID(shardID),
		SourceID:             "currency-scale-upgrade-fixture",
		SourceSequence:       1,
		StreamSequence:       1,
		LogicalTime:          engine.LogicalTime(1),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Action:               action,
	}).CanonicalEnvelope()
	if err != nil {
		t.Fatal(err)
	}
	next, decision, err := engine.ApplyTrading(
		engine.NewState(engine.ShardID(shardID)),
		input,
		action,
	)
	if err != nil ||
		decision.CommandResult.Status != engine.CommandStatusAccepted ||
		len(decision.InstrumentChanges) != 1 {
		t.Fatalf("derive recoverable instrument receipt: %+v, %v", decision, err)
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
		t.Fatal(err)
	}
	decisionJSON, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	businessHash := engine.BusinessInputHash(input)
	stateHash := next.Hash()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		SELECT
			set_config('platformgo.runtime_schema_revision', $1, false),
			set_config('platformgo.engine_decision_hash_version', '4', false)`,
		runtimeRevision,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, business_input_hash,
			business_input_hash_version, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision
		) VALUES (
			$1, $2, 1, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)`,
		shardID,
		input.InputID.String(),
		input.SchemaVersion,
		decision.InputHashVersion,
		decision.InputHash[:],
		businessHash[:],
		engine.CurrentBusinessHashVersion,
		decision.DecisionHashVersion,
		decision.DecisionHash[:],
		stateHash[:],
		envelopeJSON,
		decisionJSON,
	); err != nil {
		t.Fatalf("seed recoverable instrument receipt: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.shard_checkpoints (
			shard_id, next_stream_sequence, ready, state_hash, state_snapshot
		) VALUES (
			$1, $2, $3, $4,
			'{"recovery":"receipt_replay","version":1}'
		)`,
		shardID,
		next.NextStreamSequence(),
		next.Ready(),
		stateHash[:],
	); err != nil {
		t.Fatalf("seed recoverable instrument checkpoint: %v", err)
	}
}

func readCurrencyScaleAuthorityRelation(
	t *testing.T,
	pool *pgxpool.Pool,
) (string, uint32, string) {
	t.Helper()
	var digest string
	var fileNode uint32
	var owner string
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE(
				(
					SELECT pg_catalog.md5(
						pg_catalog.string_agg(
							currency || ':' || scale::text,
							',' ORDER BY currency COLLATE pg_catalog."C"
						)
					)
					  FROM trading.currency_scales
				),
				pg_catalog.md5('')
			),
			pg_catalog.pg_relation_filenode(
				'trading.currency_scales'::pg_catalog.regclass
			),
			pg_catalog.pg_get_userbyid(relation.relowner)
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid =
		       'trading.currency_scales'::pg_catalog.regclass`,
	).Scan(&digest, &fileNode, &owner); err != nil {
		t.Fatal(err)
	}
	return digest, fileNode, owner
}

func assertCurrencyScaleAuthorityFenceCatalog(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	var (
		instrumentTrigger string
		insertGuard       string
		mutationGuard     string
		nonOwnerFunction  int
		nonOwnerTable     int
		nonOwnerColumn    int
		allowedTable      int
		instrumentExtra   int
		instrumentAllowed int
		receiptTableExtra int
		receiptTableAllow int
		receiptColExtra   int
		receiptColAllow   int
		authorityTriggers int
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(
				SELECT pg_catalog.format(
					'%s|%s|%s',
					pg_catalog.pg_get_triggerdef(trigger.oid, false),
					trigger.tgenabled,
					procedure.prosecdef
				)
				  FROM pg_catalog.pg_trigger AS trigger
				  JOIN pg_catalog.pg_proc AS procedure
				    ON procedure.oid = trigger.tgfoid
				 WHERE trigger.tgrelid =
				       'trading.instruments'::pg_catalog.regclass
				   AND trigger.tgname =
				       'instruments_require_currency_scale_consistency'
				   AND NOT trigger.tgisinternal
			),
			(
				SELECT pg_catalog.format(
					'%s|%s',
					pg_catalog.pg_get_triggerdef(trigger.oid, false),
					trigger.tgenabled
				)
				  FROM pg_catalog.pg_trigger AS trigger
				 WHERE trigger.tgrelid =
				       'trading.currency_scales'::pg_catalog.regclass
				   AND trigger.tgname =
				       'currency_scale_registry_requires_authority'
				   AND NOT trigger.tgisinternal
			),
			(
				SELECT pg_catalog.format(
					'%s|%s',
					pg_catalog.pg_get_triggerdef(trigger.oid, false),
					trigger.tgenabled
				)
				  FROM pg_catalog.pg_trigger AS trigger
				 WHERE trigger.tgrelid =
				       'trading.currency_scales'::pg_catalog.regclass
				   AND trigger.tgname =
				       'currency_scale_registry_is_append_only'
				   AND NOT trigger.tgisinternal
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_proc AS procedure
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          procedure.proacl,
				          pg_catalog.acldefault('f', procedure.proowner)
				      )
				  ) AS privilege
				 WHERE procedure.oid IN (
					'trading.require_currency_scale_consistency()'::pg_catalog.regprocedure,
					'trading.require_currency_scale_registry_authority()'::pg_catalog.regprocedure,
					'trading.reject_currency_scale_registry_mutation()'::pg_catalog.regprocedure,
					'engine.require_runtime_schema_revision()'::pg_catalog.regprocedure
				 )
				   AND privilege.grantee <> procedure.proowner
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'trading.currency_scales'::pg_catalog.regclass
				   AND privilege.grantee <> relation.relowner
				   AND NOT (
				       privilege.privilege_type = 'SELECT'
				       AND privilege.grantee IN (
				           'platformgo_api'::pg_catalog.regrole,
				           'platformgo_engine'::pg_catalog.regrole
				       )
				       AND NOT privilege.is_grantable
				   )
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_attribute AS attribute
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      attribute.attacl
				  ) AS privilege
				  JOIN pg_catalog.pg_class AS relation
				    ON relation.oid = attribute.attrelid
				 WHERE attribute.attrelid =
				       'trading.currency_scales'::pg_catalog.regclass
				   AND privilege.grantee <> relation.relowner
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'trading.currency_scales'::pg_catalog.regclass
				   AND privilege.privilege_type = 'SELECT'
				   AND privilege.grantee IN (
				       'platformgo_api'::pg_catalog.regrole,
				       'platformgo_engine'::pg_catalog.regrole
				   )
				   AND NOT privilege.is_grantable
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'trading.instruments'::pg_catalog.regclass
				   AND privilege.grantee <> relation.relowner
				   AND NOT (
				       (
				           privilege.grantee =
				               'platformgo_api'::pg_catalog.regrole
				           AND privilege.privilege_type = 'SELECT'
				       )
				       OR (
				           privilege.grantee =
				               'platformgo_engine'::pg_catalog.regrole
				           AND privilege.privilege_type IN (
				               'SELECT', 'INSERT', 'UPDATE'
				           )
				       )
				   )
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'trading.instruments'::pg_catalog.regclass
				   AND NOT privilege.is_grantable
				   AND (
				       (
				           privilege.grantee =
				               'platformgo_api'::pg_catalog.regrole
				           AND privilege.privilege_type = 'SELECT'
				       )
				       OR (
				           privilege.grantee =
				               'platformgo_engine'::pg_catalog.regrole
				           AND privilege.privilege_type IN (
				               'SELECT', 'INSERT', 'UPDATE'
				           )
				       )
				   )
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'engine.input_receipts'::pg_catalog.regclass
				   AND privilege.grantee <> relation.relowner
				   AND NOT (
				       privilege.grantee =
				           'platformgo_engine'::pg_catalog.regrole
				       AND privilege.privilege_type IN ('SELECT', 'INSERT')
				       AND NOT privilege.is_grantable
				   )
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_class AS relation
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      COALESCE(
				          relation.relacl,
				          pg_catalog.acldefault('r', relation.relowner)
				      )
				  ) AS privilege
				 WHERE relation.oid =
				       'engine.input_receipts'::pg_catalog.regclass
				   AND privilege.grantee =
				       'platformgo_engine'::pg_catalog.regrole
				   AND privilege.privilege_type IN ('SELECT', 'INSERT')
				   AND NOT privilege.is_grantable
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_attribute AS attribute
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      attribute.attacl
				  ) AS privilege
				  JOIN pg_catalog.pg_class AS relation
				    ON relation.oid = attribute.attrelid
				 WHERE attribute.attrelid =
				       'engine.input_receipts'::pg_catalog.regclass
				   AND privilege.grantee <> relation.relowner
				   AND NOT (
				       privilege.grantee =
				           'platformgo_outbox'::pg_catalog.regrole
				       AND attribute.attname IN ('shard_id', 'input_id')
				       AND privilege.privilege_type = 'SELECT'
				       AND NOT privilege.is_grantable
				   )
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_attribute AS attribute
				  CROSS JOIN LATERAL pg_catalog.aclexplode(
				      attribute.attacl
				  ) AS privilege
				 WHERE attribute.attrelid =
				       'engine.input_receipts'::pg_catalog.regclass
				   AND privilege.grantee =
				       'platformgo_outbox'::pg_catalog.regrole
				   AND attribute.attname IN ('shard_id', 'input_id')
				   AND privilege.privilege_type = 'SELECT'
				   AND NOT privilege.is_grantable
			),
			(
				SELECT count(*)
				  FROM pg_catalog.pg_trigger AS trigger
				 WHERE trigger.tgrelid IN (
				     'trading.instruments'::pg_catalog.regclass,
				     'trading.currency_scales'::pg_catalog.regclass,
				     'engine.input_receipts'::pg_catalog.regclass
				 )
				   AND NOT trigger.tgisinternal
			)`,
	).Scan(
		&instrumentTrigger,
		&insertGuard,
		&mutationGuard,
		&nonOwnerFunction,
		&nonOwnerTable,
		&nonOwnerColumn,
		&allowedTable,
		&instrumentExtra,
		&instrumentAllowed,
		&receiptTableExtra,
		&receiptTableAllow,
		&receiptColExtra,
		&receiptColAllow,
		&authorityTriggers,
	); err != nil {
		t.Fatal(err)
	}
	if instrumentTrigger == "" ||
		insertGuard == "" ||
		mutationGuard == "" ||
		nonOwnerFunction != 0 ||
		nonOwnerTable != 0 ||
		nonOwnerColumn != 0 ||
		allowedTable != 2 ||
		instrumentExtra != 0 ||
		instrumentAllowed != 4 ||
		receiptTableExtra != 0 ||
		receiptTableAllow != 2 ||
		receiptColExtra != 0 ||
		receiptColAllow != 2 ||
		authorityTriggers != 9 {
		t.Fatalf(
			"authority catalog trigger=%q insert=%q mutation=%q function=%d table=%d column=%d allowed=%d instrument-extra=%d instrument-allowed=%d receipt-table-extra=%d receipt-table-allowed=%d receipt-column-extra=%d receipt-column-allowed=%d authority-triggers=%d",
			instrumentTrigger,
			insertGuard,
			mutationGuard,
			nonOwnerFunction,
			nonOwnerTable,
			nonOwnerColumn,
			allowedTable,
			instrumentExtra,
			instrumentAllowed,
			receiptTableExtra,
			receiptTableAllow,
			receiptColExtra,
			receiptColAllow,
			authorityTriggers,
		)
	}
	if !strings.HasSuffix(instrumentTrigger, "|A|t") {
		t.Fatalf(
			"instrument trigger is not ALWAYS SECURITY DEFINER: %q",
			instrumentTrigger,
		)
	}
	for _, trigger := range []struct {
		label   string
		catalog string
	}{
		{label: "insert", catalog: insertGuard},
		{label: "mutation", catalog: mutationGuard},
	} {
		if !strings.HasSuffix(trigger.catalog, "|A") {
			t.Fatalf(
				"%s trigger is not ENABLE ALWAYS: %q",
				trigger.label,
				trigger.catalog,
			)
		}
	}
}

func assertCurrencyScaleAuthoritySQLState(
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

func assertCurrencyScaleAuthorityMessage(
	t *testing.T,
	err error,
	message string,
) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Message != message {
		t.Fatalf(
			"PostgreSQL error = %v, want message %q",
			err,
			message,
		)
	}
}

func waitForCurrencyScaleRelationBlocker(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	relation string,
	mode string,
	blockerPID int32,
) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks AS pending
				 WHERE pending.relation = $1::pg_catalog.regclass
				   AND pending.mode = $2
				   AND NOT pending.granted
				   AND $3 = ANY (
				       pg_catalog.pg_blocking_pids(pending.pid)
				   )
			)`,
			relation,
			mode,
			blockerPID,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("migration did not wait on the expected source writer")
		case <-time.After(time.Millisecond):
		}
	}
}

func waitForCurrencyScaleBackendBlocker(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	waiterPID int32,
) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT pg_catalog.cardinality(
				pg_catalog.pg_blocking_pids($1)
			) > 0`,
			waiterPID,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("old function body did not wait on the advisory latch")
		case <-time.After(time.Millisecond):
		}
	}
}
