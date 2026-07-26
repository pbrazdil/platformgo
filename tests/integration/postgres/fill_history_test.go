package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:207
//	test: fill_filled_at_is_engine_execution_time_not_insert_now
//
// Adaptations:
//   - The native durable fill and its logical time replace the legacy mirror row.
//   - A fixed nanosecond timestamp replaces insert-time now().
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//
// Assertions preserved:
//   - filledAt is the engine execution time, not database insertion time.
func TestFillFilledAtIsEngineExecutionTimeNotInsertNow(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill history database: %v", err)
	}

	const (
		accountID  = "urn:xb:account:fill-time"
		userID     = "urn:xb:user:fill-time"
		orderID    = "019fa844-26c0-7000-8000-000000000001"
		fillID     = "019fa844-26c0-7000-8000-000000000002"
		inputID    = "019fa844-26c0-7000-8000-000000000003"
		positionID = "019fa844-26c0-7000-8000-000000000004"
	)
	engineTime := time.Date(2020, time.September, 13, 12, 26, 40, 123456789, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.deployment_shard (shard_id) VALUES (41);
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
		VALUES ('urn:xb:account:fill-time', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('urn:xb:account:fill-time', 41);
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:fill-time', 'fill-time', 'fill-time',
			'urn:xb:tenant:fill-proof'
		);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:fill-time', 'urn:xb:account:fill-time',
			'urn:xb:tenant:fill-proof'
		)`); err != nil {
		t.Fatalf("seed fill identities: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			'urn:xb:account:fill-time', 1001, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $1,
			'urn:xb:tenant:fill-proof'
		)`,
		engineTime,
	); err != nil {
		t.Fatalf("seed fill profile: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'urn:xb:account:fill-time', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 0.01, 0.01, 60000,
			false, false, false, 1
		)`); err != nil {
		t.Fatalf("seed fill order: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000002',
			'019fa844-26c0-7000-8000-000000000001',
			'019fa844-26c0-7000-8000-000000000003',
			'urn:xb:account:fill-time', 'BTC-PERP',
			'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000004', 'OPEN',
			NULL, NULL, 'TAKER', 0.5, 'USDC', $1
		)`,
		engineTime.UnixNano(),
	); err != nil {
		t.Fatalf("seed durable fill: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_history_api_login",
		"platformgo_api",
	)
	latest, err := platformpostgres.NewCompatibilityStore(apiPool).LatestFillExecution(
		ctx,
		accountID,
	)
	if err != nil {
		t.Fatalf("read latest fill execution: %v", err)
	}
	if latest.FilledAt != engineTime.Format(time.RFC3339Nano) {
		t.Fatalf(
			"filledAt = %q, want engine time %q",
			latest.FilledAt,
			engineTime.Format(time.RFC3339Nano),
		)
	}
}

func TestFillHistoryQueriesUseKeysetIndex(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill query-plan database: %v", err)
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
		VALUES ('account-fill-plan', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000001',
			'account-fill-plan', 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 100, 100, 100,
			false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		)
		SELECT
			format(
				'10000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'019fa844-26c0-7000-8000-000000000001'::uuid,
			format(
				'20000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'account-fill-plan',
			'BTC-PERP',
			'BUY',
			100,
			0.01,
			'30000000-0000-0000-0000-000000000001'::uuid,
			'OPEN',
			'TAKER',
			1784901600000000000 + sequence_number
		  FROM generate_series(1, 10000) AS sequence(sequence_number);
		ANALYZE trading.fills`); err != nil {
		t.Fatalf("seed representative fill history: %v", err)
	}

	assertFillPlanUsesIndex(
		t,
		pool,
		`SELECT fill_id
		   FROM trading.fills
		  WHERE account_id = 'account-fill-plan'
		    AND (logical_time, fill_id) <
		        (1784901600000010001, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
		  ORDER BY logical_time DESC, fill_id DESC
		  LIMIT 51`,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		`SELECT fill_id
		   FROM trading.fills
		  WHERE account_id = 'account-fill-plan'
		    AND (logical_time, fill_id) >
		        (1784901600000009950, '00000000-0000-0000-0000-000000000000')
		  ORDER BY logical_time ASC, fill_id ASC
		  LIMIT 51`,
	)
}

func assertFillPlanUsesIndex(t *testing.T, pool *pgxpool.Pool, query string) {
	t.Helper()
	var rawPlan []byte
	if err := pool.QueryRow(
		context.Background(),
		"EXPLAIN (FORMAT JSON, COSTS OFF) "+query,
	).Scan(&rawPlan); err != nil {
		t.Fatalf("explain fill query: %v", err)
	}
	var explained []struct {
		Plan postgresExplainPlan `json:"Plan"`
	}
	if err := json.Unmarshal(rawPlan, &explained); err != nil {
		t.Fatalf("decode fill plan: %v", err)
	}
	if len(explained) != 1 {
		t.Fatalf("fill plans = %d, want 1", len(explained))
	}
	var (
		indexFound  bool
		fillSeqScan bool
	)
	walkPostgresPlan(explained[0].Plan, func(plan postgresExplainPlan) {
		indexFound = indexFound || plan.IndexName == "fills_account_history_idx"
		fillSeqScan = fillSeqScan ||
			(plan.NodeType == "Seq Scan" && plan.RelationName == "fills")
	})
	if !indexFound || fillSeqScan {
		t.Fatalf(
			"fill plan required index found=%t fill-seq=%t: %s",
			indexFound,
			fillSeqScan,
			rawPlan,
		)
	}
}
