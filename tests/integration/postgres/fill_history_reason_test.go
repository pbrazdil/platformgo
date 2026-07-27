package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:656
//	test: fill_reason_derives_from_bracket_leg_and_stopout
//
// Adaptations:
//   - Current immutable Go orders, fills, and order intents replace the legacy
//     orders and fill mirror.
//   - The PostgreSQL compatibility reader replaces the Rust query dispatcher.
//   - Exact deterministic UUIDs and logical times replace random UUIDs and
//     insertion-time ordering.
//   - The external fills route remains inventory; this test proves the current
//     internal compatibility projection through the least-privilege API role.
//
// Assertions preserved:
//   - A stop-loss bracket-leg fill reports reason stop_loss.
//   - A take-profit bracket-leg fill reports reason take_profit.
//   - A stop-out intent fill reports reason liquidation.
//   - A flatten intent fill reports reason flatten.
//   - A plain order fill reports reason manual.
//
// Strengthening:
//   - Bracket provenance takes precedence over an intent prefix.
//   - A fresh compatibility reader returns the same immutable reasons.
//   - Another account's fill is not exposed.
//   - Unknown durable bracket provenance fails closed.
func TestFillReasonDerivesFromBracketLegAndStopout(t *testing.T) {
	t.Run("canonical reasons are immutable and account scoped", func(t *testing.T) {
		ctx := context.Background()
		pool := postgresPool(t)
		resetDurableSchemas(t, pool)
		if err := platformpostgres.NewMigrator(
			pool,
			os.DirFS(filepath.Join("..", "..", "..", "migrations")),
		).Migrate(ctx); err != nil {
			t.Fatalf("migrate fill-reason database: %v", err)
		}
		seedFillReasonHistory(t, ctx, pool, false)

		apiPool := runtimeRoleLoginPool(
			t,
			pool,
			"platformgo_fill_reason_api_login",
			"platformgo_api",
		)
		readReasons := func() map[string]string {
			t.Helper()
			page, err := platformpostgres.NewCompatibilityStore(apiPool).
				FilterFillExecutions(
					ctx,
					"urn:xb:account:fill-reason",
					platformpostgres.FillExecutionFilter{Limit: 20},
				)
			if err != nil {
				t.Fatalf("read fill reasons: %v", err)
			}
			if page.Total != 6 || len(page.Items) != 6 {
				t.Fatalf(
					"fill-reason page total/items = %d/%d, want 6/6",
					page.Total,
					len(page.Items),
				)
			}
			reasons := make(map[string]string, len(page.Items))
			for _, fill := range page.Items {
				reasons[fill.FillID] = fill.Reason
			}
			return reasons
		}

		first := readReasons()
		want := map[string]string{
			"019fa844-26c0-7000-8000-000000000011": "stop_loss",
			"019fa844-26c0-7000-8000-000000000012": "take_profit",
			"019fa844-26c0-7000-8000-000000000013": "liquidation",
			"019fa844-26c0-7000-8000-000000000014": "flatten",
			"019fa844-26c0-7000-8000-000000000015": "manual",
			"019fa844-26c0-7000-8000-000000000016": "stop_loss",
		}
		for fillID, reason := range want {
			if first[fillID] != reason {
				t.Fatalf(
					"fill %s reason = %q, want %q",
					fillID,
					first[fillID],
					reason,
				)
			}
		}
		if _, leaked := first["019fa844-26c0-7000-8000-000000000017"]; leaked {
			t.Fatal("fill-reason read leaked another account's fill")
		}

		second := readReasons()
		for fillID, reason := range first {
			if second[fillID] != reason {
				t.Fatalf(
					"fresh-reader fill %s reason = %q, want stable %q",
					fillID,
					second[fillID],
					reason,
				)
			}
		}
		latest, err := platformpostgres.NewCompatibilityStore(apiPool).
			LatestFillExecution(ctx, "urn:xb:account:fill-reason")
		if err != nil {
			t.Fatalf("read latest fill reason: %v", err)
		}
		if latest.FillID != "019fa844-26c0-7000-8000-000000000016" ||
			latest.Reason != "stop_loss" {
			t.Fatalf(
				"latest fill = %#v, want bracket-precedence stop_loss",
				latest,
			)
		}

		var (
			orderCount  int
			fillCount   int
			ledgerCount int
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT count(*) FROM trading.orders),
				(SELECT count(*) FROM trading.fills),
				(SELECT count(*) FROM ledger.transactions)`).
			Scan(&orderCount, &fillCount, &ledgerCount); err != nil {
			t.Fatalf("read fill-reason durable counts: %v", err)
		}
		if orderCount != 7 || fillCount != 7 || ledgerCount != 0 {
			t.Fatalf(
				"reader mutated durable state: orders=%d fills=%d ledger=%d",
				orderCount,
				fillCount,
				ledgerCount,
			)
		}
	})

	t.Run("unknown bracket provenance fails closed", func(t *testing.T) {
		ctx := context.Background()
		pool := postgresPool(t)
		resetDurableSchemas(t, pool)
		if err := platformpostgres.NewMigrator(
			pool,
			os.DirFS(filepath.Join("..", "..", "..", "migrations")),
		).Migrate(ctx); err != nil {
			t.Fatalf("migrate invalid fill-reason database: %v", err)
		}
		seedFillReasonHistory(t, ctx, pool, true)

		apiPool := runtimeRoleLoginPool(
			t,
			pool,
			"platformgo_invalid_fill_reason_api_login",
			"platformgo_api",
		)
		_, err := platformpostgres.NewCompatibilityStore(apiPool).
			FilterFillExecutions(
				ctx,
				"urn:xb:account:fill-reason",
				platformpostgres.FillExecutionFilter{Limit: 20},
			)
		if err == nil || !strings.Contains(err.Error(), "unknown durable bracket leg") {
			t.Fatalf(
				"invalid bracket provenance error = %v, want fail-closed classification",
				err,
			)
		}
	})
}

func seedFillReasonHistory(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	includeInvalid bool,
) {
	t.Helper()
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
		INSERT INTO trading.accounts (account_id, oms_mode) VALUES
			('urn:xb:account:fill-reason', 'NETTING'),
			('urn:xb:account:other-fill-reason', 'NETTING');
		INSERT INTO trading.commands (
			command_id, account_id, account_sequence, command_type,
			schema_version, canonical_payload, status, result,
			logical_time, completed_at
		) VALUES
			('019fa844-26c0-7000-8000-000000000041',
			 'urn:xb:account:fill-reason', 1, 'submit_order', 1,
			 '{}', 'completed', '{}', 1785081600000000001,
			 '2026-07-26T16:00:00Z'),
			('019fa844-26c0-7000-8000-000000000042',
			 'urn:xb:account:fill-reason', 2, 'submit_order', 1,
			 '{}', 'completed', '{}', 1785081600000000002,
			 '2026-07-26T16:00:00Z'),
			('019fa844-26c0-7000-8000-000000000043',
			 'urn:xb:account:fill-reason', 3, 'submit_order', 1,
			 '{}', 'completed', '{}', 1785081600000000003,
			 '2026-07-26T16:00:00Z');
		INSERT INTO trading.idempotency_records (
			scope, idempotency_key, request_hash, command_id, state,
			response_status, response_headers, response_body, expires_at
		) VALUES
			('fill-reason', 'stopout',
			 decode(repeat('11', 32), 'hex'),
			 '019fa844-26c0-7000-8000-000000000041', 'completed',
			 202, '{}', convert_to('{}', 'UTF8'), '2030-01-01T00:00:00Z'),
			('fill-reason', 'flatten',
			 decode(repeat('22', 32), 'hex'),
			 '019fa844-26c0-7000-8000-000000000042', 'completed',
			 202, '{}', convert_to('{}', 'UTF8'), '2030-01-01T00:00:00Z'),
			('fill-reason', 'bracket-precedence',
			 decode(repeat('33', 32), 'hex'),
			 '019fa844-26c0-7000-8000-000000000043', 'completed',
			 202, '{}', convert_to('{}', 'UTF8'), '2030-01-01T00:00:00Z');
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, position_id,
			bracket_id, bracket_leg, bracket_leg_index, has_rested,
			version
		) VALUES
			('019fa844-26c0-7000-8000-000000000001',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'STOP_MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 '019fa844-26c0-7000-8000-000000000050',
			 'stop_loss', 1, false, 1),
			('019fa844-26c0-7000-8000-000000000002',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'TAKE_PROFIT_MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 '019fa844-26c0-7000-8000-000000000050',
			 'take_profit', 1, false, 1),
			('019fa844-26c0-7000-8000-000000000003',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 NULL, NULL, NULL, false, 1),
			('019fa844-26c0-7000-8000-000000000004',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 NULL, NULL, NULL, false, 1),
			('019fa844-26c0-7000-8000-000000000005',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 NULL, NULL, NULL, false, 1),
			('019fa844-26c0-7000-8000-000000000006',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'STOP_MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 '019fa844-26c0-7000-8000-000000000051',
			 'stop_loss', 1, false, 1),
			('019fa844-26c0-7000-8000-000000000007',
			 'urn:xb:account:other-fill-reason', 'BTC-PERP', 'SELL',
			 'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000032',
			 NULL, NULL, NULL, false, 1);
		INSERT INTO trading.order_intents (
			order_id, command_id, account_id, intent_id
		) VALUES
			('019fa844-26c0-7000-8000-000000000003',
			 '019fa844-26c0-7000-8000-000000000041',
			 'urn:xb:account:fill-reason', 'stopout:42:BTC-PERP'),
			('019fa844-26c0-7000-8000-000000000004',
			 '019fa844-26c0-7000-8000-000000000042',
			 'urn:xb:account:fill-reason',
			 'flatten:01JZTESTFLATTEN0000000000'),
			('019fa844-26c0-7000-8000-000000000006',
			 '019fa844-26c0-7000-8000-000000000043',
			 'urn:xb:account:fill-reason',
			 'flatten:BRACKET-PRECEDENCE');
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, fee, fee_currency, logical_time,
			effective_leverage
		) VALUES
			('019fa844-26c0-7000-8000-000000000011',
			 '019fa844-26c0-7000-8000-000000000001',
			 '019fa844-26c0-7000-8000-000000000021',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000001, 10),
			('019fa844-26c0-7000-8000-000000000012',
			 '019fa844-26c0-7000-8000-000000000002',
			 '019fa844-26c0-7000-8000-000000000022',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000002, 10),
			('019fa844-26c0-7000-8000-000000000013',
			 '019fa844-26c0-7000-8000-000000000003',
			 '019fa844-26c0-7000-8000-000000000023',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000003, 10),
			('019fa844-26c0-7000-8000-000000000014',
			 '019fa844-26c0-7000-8000-000000000004',
			 '019fa844-26c0-7000-8000-000000000024',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000004, 10),
			('019fa844-26c0-7000-8000-000000000015',
			 '019fa844-26c0-7000-8000-000000000005',
			 '019fa844-26c0-7000-8000-000000000025',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000005, 10),
			('019fa844-26c0-7000-8000-000000000016',
			 '019fa844-26c0-7000-8000-000000000006',
			 '019fa844-26c0-7000-8000-000000000026',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000006, 10),
			('019fa844-26c0-7000-8000-000000000017',
			 '019fa844-26c0-7000-8000-000000000007',
			 '019fa844-26c0-7000-8000-000000000027',
			 'urn:xb:account:other-fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000032',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000007, 10)`); err != nil {
		t.Fatalf("seed canonical fill-reason history: %v", err)
	}
	if !includeInvalid {
		return
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, position_id,
			bracket_id, bracket_leg, bracket_leg_index, has_rested,
			version
		) VALUES (
			'019fa844-26c0-7000-8000-000000000008',
			'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			false, true, '019fa844-26c0-7000-8000-000000000031',
			'019fa844-26c0-7000-8000-000000000052',
			'unknown', 1, false, 1
		);
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			liquidity_side, fee, fee_currency, logical_time,
			effective_leverage
		) VALUES (
			'019fa844-26c0-7000-8000-000000000018',
			'019fa844-26c0-7000-8000-000000000008',
			'019fa844-26c0-7000-8000-000000000028',
			'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			'close', 'TAKER', 0, 'USDC', 1785081600000000008, 10
		)`); err != nil {
		t.Fatalf("seed invalid fill-reason history: %v", err)
	}
}
