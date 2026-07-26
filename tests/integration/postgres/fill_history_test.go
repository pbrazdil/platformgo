package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
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
		VALUES ($1, 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ($1, 41);
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			$2, 'fill-time', 'fill-time', 'urn:xb:tenant:fill-proof'
		);
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			$2, $1, 'urn:xb:tenant:fill-proof'
		);
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES (
			$1, 1001, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $7,
			'urn:xb:tenant:fill-proof'
		);
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			$3, $1, 'BTC-PERP', 'BUY', 'MARKET',
			'IOC', 'FILLED', 0.01, 0.01, 60000,
			false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time
		) VALUES (
			$4, $3, $5, $1, 'BTC-PERP',
			'BUY', 60000, 0.01, $6, 'OPEN',
			NULL, NULL, 'TAKER', 0.5, 'USDC', $8
		)`,
		accountID,
		userID,
		orderID,
		fillID,
		inputID,
		positionID,
		engineTime,
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
	page, err := platformpostgres.NewCompatibilityStore(apiPool).Fills(
		ctx,
		accountID,
		edge.PageParams{Limit: 1},
	)
	if err != nil {
		t.Fatalf("read fill history: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("fill page = %#v", page)
	}
	if page.Items[0].FilledAt != engineTime.Format(time.RFC3339Nano) {
		t.Fatalf(
			"filledAt = %q, want engine time %q",
			page.Items[0].FilledAt,
			engineTime.Format(time.RFC3339Nano),
		)
	}
}
