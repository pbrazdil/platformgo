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

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:328
//	test: fills_history_filters_by_side_and_trade_id
//
// Adaptations:
//   - Deterministic UUID fill IDs replace the legacy mirror's free-form IDs.
//   - Durable immutable fills replace legacy mirror rows.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//
// Assertions preserved:
//   - A lowercase side filter returns only the matching fill and filtered total.
//   - A trade ID filter returns exactly the requested fill.
func TestFillsHistoryFiltersBySideAndTradeID(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill filtering database: %v", err)
	}

	const (
		accountID  = "urn:xb:account:fill-filter"
		buyFillID  = "019fa844-26c0-7000-8000-000000000011"
		sellFillID = "019fa844-26c0-7000-8000-000000000012"
	)
	if _, err := pool.Exec(ctx, `
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
		VALUES ('urn:xb:account:fill-filter', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES
			(
				'019fa844-26c0-7000-8000-000000000021',
				'urn:xb:account:fill-filter', 'BTC-PERP', 'BUY', 'MARKET',
				'IOC', 'FILLED', 0.01, 0.01, 60000,
				false, false, false, 1
			),
			(
				'019fa844-26c0-7000-8000-000000000022',
				'urn:xb:account:fill-filter', 'BTC-PERP', 'SELL', 'MARKET',
				'IOC', 'FILLED', 0.01, 0.01, 61000,
				false, false, false, 1
			);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			(
				'019fa844-26c0-7000-8000-000000000011',
				'019fa844-26c0-7000-8000-000000000021',
				'019fa844-26c0-7000-8000-000000000031',
				'urn:xb:account:fill-filter', 'BTC-PERP',
				'BUY', 60000, 0.01,
				'019fa844-26c0-7000-8000-000000000041',
				'OPEN', 'TAKER', 1784901600000000001
			),
			(
				'019fa844-26c0-7000-8000-000000000012',
				'019fa844-26c0-7000-8000-000000000022',
				'019fa844-26c0-7000-8000-000000000032',
				'urn:xb:account:fill-filter', 'BTC-PERP',
				'SELL', 61000, 0.01,
				'019fa844-26c0-7000-8000-000000000042',
				'CLOSE', 'TAKER', 1784901600000000002
			)`); err != nil {
		t.Fatalf("seed durable fill filters: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_filter_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	buys, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Side: "buy", Limit: 10},
	)
	if err != nil {
		t.Fatalf("filter fills by side: %v", err)
	}
	if len(buys.Items) != 1 || buys.Items[0].FillID != buyFillID {
		t.Fatalf("buy fills = %#v, want only %s", buys.Items, buyFillID)
	}
	if buys.Total != 1 {
		t.Fatalf("buy filtered total = %d, want 1", buys.Total)
	}

	one, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{TradeID: sellFillID, Limit: 10},
	)
	if err != nil {
		t.Fatalf("filter fills by trade ID: %v", err)
	}
	if len(one.Items) != 1 || one.Items[0].FillID != sellFillID {
		t.Fatalf("trade-ID fills = %#v, want only %s", one.Items, sellFillID)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:252
//	test: fill_history_returns_side_and_trade_type
//
// Adaptations:
//   - Durable immutable fills replace legacy mirror rows.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//   - Current Go behavior remains authoritative: every engine-produced durable
//     fill has a required position effect, so the legacy unclassified fixture
//     is not imported as a nullable trade type.
//
// Assertions preserved:
//   - BUY/open and SELL/close sides retain their source spellings.
//   - Open, increase, reduce, flip, and close trade types project exactly.
//
// Strengthening:
//   - Unknown durable effects fail closed instead of becoming client values.
func TestFillHistoryReturnsSideAndTradeType(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill side/trade-type database: %v", err)
	}

	const accountID = "urn:xb:account:fill-side-trade-type"
	if _, err := pool.Exec(ctx, `
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
		VALUES ('urn:xb:account:fill-side-trade-type', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES
			('019fa844-26c0-7000-8000-000000000081',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000082',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'BUY',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000083',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000084',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1),
			('019fa844-26c0-7000-8000-000000000085',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP', 'SELL',
			 'MARKET', 'IOC', 'FILLED', 0.01, 0.01, 60000,
			 false, false, false, 1);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES
			('019fa844-26c0-7000-8000-000000000071',
			 '019fa844-26c0-7000-8000-000000000081',
			 '019fa844-26c0-7000-8000-000000000091',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'open', 'TAKER', 1784901600000000071),
			('019fa844-26c0-7000-8000-000000000072',
			 '019fa844-26c0-7000-8000-000000000082',
			 '019fa844-26c0-7000-8000-000000000092',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'BUY', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'increase', 'TAKER', 1784901600000000072),
			('019fa844-26c0-7000-8000-000000000073',
			 '019fa844-26c0-7000-8000-000000000083',
			 '019fa844-26c0-7000-8000-000000000093',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'reduce', 'TAKER', 1784901600000000073),
			('019fa844-26c0-7000-8000-000000000074',
			 '019fa844-26c0-7000-8000-000000000084',
			 '019fa844-26c0-7000-8000-000000000094',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'flip', 'TAKER', 1784901600000000074),
			('019fa844-26c0-7000-8000-000000000075',
			 '019fa844-26c0-7000-8000-000000000085',
			 '019fa844-26c0-7000-8000-000000000095',
			 'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			 'SELL', 60000, 0.01,
			 '019fa844-26c0-7000-8000-0000000000a1',
			 'close', 'TAKER', 1784901600000000075)`); err != nil {
		t.Fatalf("seed durable fill side/trade types: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_side_trade_type_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	page, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Limit: 10},
	)
	if err != nil {
		t.Fatalf("read fill side/trade types: %v", err)
	}
	if len(page.Items) != 5 {
		t.Fatalf("fills = %#v, want five classified fills", page.Items)
	}
	want := map[string]struct {
		side      string
		tradeType string
	}{
		"019fa844-26c0-7000-8000-000000000071": {"BUY", "open"},
		"019fa844-26c0-7000-8000-000000000072": {"BUY", "increase"},
		"019fa844-26c0-7000-8000-000000000073": {"SELL", "reduce"},
		"019fa844-26c0-7000-8000-000000000074": {"SELL", "flip"},
		"019fa844-26c0-7000-8000-000000000075": {"SELL", "close"},
	}
	for _, fill := range page.Items {
		expected, ok := want[fill.FillID]
		if !ok {
			t.Fatalf("unexpected fill = %#v", fill)
		}
		if fill.Side != expected.side || fill.TradeType != expected.tradeType {
			t.Fatalf(
				"fill %s = (%q, %q), want (%q, %q)",
				fill.FillID,
				fill.Side,
				fill.TradeType,
				expected.side,
				expected.tradeType,
			)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000076',
			'019fa844-26c0-7000-8000-000000000081',
			'019fa844-26c0-7000-8000-000000000096',
			'urn:xb:account:fill-side-trade-type', 'BTC-PERP',
			'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-0000000000a1',
			'unknown', 'TAKER', 1784901600000000076)`); err != nil {
		t.Fatalf("seed unknown durable fill trade type: %v", err)
	}
	if _, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{Limit: 10},
	); err == nil {
		t.Fatal("unknown durable fill position effect unexpectedly projected")
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:410
//	test: fill_order_id_is_the_correlatable_order_urn
//
// Adaptations:
//   - The immutable durable fill replaces the legacy mirror row.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//   - The accepted Go order surface retains the UUID body inside the stable
//     urn:xb:order: namespace.
//
// Assertions preserved:
//   - Fill orderId is the same typed order URN exposed by the order surface.
func TestFillOrderIDIsTheCorrelatableOrderURN(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill order-correlation database: %v", err)
	}

	const (
		accountID = "urn:xb:account:fill-order-correlation"
		fillID    = "019fa844-26c0-7000-8000-000000000062"
	)
	if _, err := pool.Exec(ctx, `
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
		VALUES ('urn:xb:account:fill-order-correlation', 'NETTING');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000061',
			'urn:xb:account:fill-order-correlation',
			'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			0.01, 0.01, 60000, false, false, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, logical_time
		) VALUES (
			'019fa844-26c0-7000-8000-000000000062',
			'019fa844-26c0-7000-8000-000000000061',
			'019fa844-26c0-7000-8000-000000000063',
			'urn:xb:account:fill-order-correlation',
			'BTC-PERP', 'BUY', 60000, 0.01,
			'019fa844-26c0-7000-8000-000000000064',
			'OPEN', 'TAKER', 1784901600000000062
		)`); err != nil {
		t.Fatalf("seed correlatable fill order: %v", err)
	}

	apiPool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_fill_order_correlation_api_login",
		"platformgo_api",
	)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	page, err := store.FilterFillExecutions(
		ctx,
		accountID,
		platformpostgres.FillExecutionFilter{
			TradeID: fillID,
			Limit:   10,
		},
	)
	if err != nil {
		t.Fatalf("read correlatable fill order: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("fill page = %#v, want one item", page)
	}
	orders, err := store.Orders(ctx, accountID)
	if err != nil {
		t.Fatalf("read correlatable order surface: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("orders = %#v, want one item", orders)
	}
	wantOrderID := orders[0].OrderID
	if page.Items[0].OrderID != wantOrderID {
		t.Fatalf(
			"fill orderId = %q, want correlatable %q",
			page.Items[0].OrderID,
			wantOrderID,
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
		INSERT INTO trading.accounts (account_id, oms_mode)
		SELECT format('account-fill-plan-other-%s', account_number), 'NETTING'
		  FROM generate_series(1, 9) AS accounts(account_number);
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
			CASE
				WHEN sequence_number <= 10000 THEN 'account-fill-plan'
				ELSE format(
					'account-fill-plan-other-%s',
					1 + ((sequence_number - 10001) / 10000)
				)
			END,
			'BTC-PERP',
			CASE
				WHEN sequence_number % 2 = 0 THEN 'BUY'
				ELSE 'SELL'
			END,
			100,
			0.01,
			'30000000-0000-0000-0000-000000000001'::uuid,
			'OPEN',
			'TAKER',
			1784901600000000000 + sequence_number
		  FROM generate_series(1, 100000) AS sequence(sequence_number);
		ANALYZE trading.fills`); err != nil {
		t.Fatalf("seed representative fill history: %v", err)
	}

	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_history_idx",
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
		"fills_account_history_idx",
		`SELECT fill_id
		   FROM trading.fills
		  WHERE account_id = 'account-fill-plan'
		    AND (logical_time, fill_id) >
		        (1784901600000009950, '00000000-0000-0000-0000-000000000000')
		  ORDER BY logical_time ASC, fill_id ASC
		  LIMIT 51`,
	)
	assertFillPlanUsesIndex(
		t,
		pool,
		"fills_account_side_history_idx",
		`SELECT
			fill.fill_id::text,
			fill.order_id::text,
			fill.side,
			lower(fill.position_effect),
			fill.logical_time,
			count(*) OVER ()
		   FROM trading.fills AS fill
		  WHERE fill.account_id = $1
		    AND ($2::text IS NULL OR fill.side = $2)
		    AND ($3::uuid IS NULL OR fill.fill_id = $3)
		  ORDER BY fill.logical_time DESC, fill.fill_id DESC
		  LIMIT $4`,
		"account-fill-plan",
		"BUY",
		nil,
		10,
	)
}

func assertFillPlanUsesIndex(
	t *testing.T,
	pool *pgxpool.Pool,
	indexName string,
	query string,
	args ...any,
) {
	t.Helper()
	var rawPlan []byte
	if err := pool.QueryRow(
		context.Background(),
		"EXPLAIN (FORMAT JSON, COSTS OFF) "+query,
		args...,
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
		indexFound = indexFound || plan.IndexName == indexName
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
