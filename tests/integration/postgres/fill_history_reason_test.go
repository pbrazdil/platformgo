package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	platformedge "github.com/upcomers-org/platformgo/internal/edge"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_fills.rs:656
//	test: fill_reason_derives_from_bracket_leg_and_stopout
//
// Adaptations:
//   - Current durable Go orders plus immutable fills and order intents replace
//     the legacy orders and fill mirror.
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
//   - A fresh API-role connection and reader return the same immutable reasons.
//   - Another account's fill is not exposed.
//   - Fill, order, intent, and command account authority must agree.
//   - Empty, unknown, case-shifted, or whitespace bracket provenance fails
//     closed; only SQL NULL and exact current Go values are accepted.
func TestFillReasonDerivesFromBracketLegAndStopout(t *testing.T) {
	t.Run("canonical reasons are immutable and account scoped", func(t *testing.T) {
		ctx := context.Background()
		pool := postgresPool(t)
		resetDurableSchemas(t, pool)
		migrateFillReasonDatabase(t, ctx, pool)
		seedFillReasonHistory(t, ctx, pool)
		before := fillReasonDurableSnapshot(t, ctx, pool)

		apiPool := runtimeRoleLoginPool(
			t,
			pool,
			"platformgo_fill_reason_api_login",
			"platformgo_api",
		)
		readReasons := func(
			store *platformpostgres.CompatibilityStore,
		) map[string]string {
			t.Helper()
			page, err := store.FilterFillExecutions(
				ctx,
				"urn:xb:account:fill-reason",
				platformpostgres.FillExecutionFilter{Limit: 20},
			)
			if err != nil {
				t.Fatalf("read fill reasons: %v", err)
			}
			if page.Total != 7 || len(page.Items) != 7 {
				t.Fatalf(
					"fill-reason page total/items = %d/%d, want 7/7",
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

		first := readReasons(platformpostgres.NewCompatibilityStore(apiPool))
		want := []struct {
			fillID string
			reason string
		}{
			{"019fa844-26c0-7000-8000-000000000011", "stop_loss"},
			{"019fa844-26c0-7000-8000-000000000012", "take_profit"},
			{"019fa844-26c0-7000-8000-000000000013", "liquidation"},
			{"019fa844-26c0-7000-8000-000000000014", "flatten"},
			{"019fa844-26c0-7000-8000-000000000015", "manual"},
			{"019fa844-26c0-7000-8000-000000000016", "stop_loss"},
			{"019fa844-26c0-7000-8000-000000000019", "manual"},
		}
		for _, expected := range want {
			if first[expected.fillID] != expected.reason {
				t.Fatalf(
					"fill %s reason = %q, want %q",
					expected.fillID,
					first[expected.fillID],
					expected.reason,
				)
			}
		}
		if _, leaked := first["019fa844-26c0-7000-8000-000000000017"]; leaked {
			t.Fatal("fill-reason read leaked another account's fill")
		}
		firstLatest, err := platformpostgres.NewCompatibilityStore(apiPool).
			LatestFillExecution(ctx, "urn:xb:account:fill-reason")
		if err != nil {
			t.Fatalf("read latest fill reason: %v", err)
		}
		assertLatestFillReason(t, firstLatest)

		apiPool.Close()
		restartedAPIPool := runtimeRoleLoginPool(
			t,
			pool,
			"platformgo_fill_reason_restarted_api_login",
			"platformgo_api",
		)
		restartedStore := platformpostgres.NewCompatibilityStore(
			restartedAPIPool,
		)
		second := readReasons(restartedStore)
		for _, expected := range want {
			if second[expected.fillID] != expected.reason {
				t.Fatalf(
					"fresh-reader fill %s reason = %q, want stable %q",
					expected.fillID,
					second[expected.fillID],
					expected.reason,
				)
			}
		}
		latest, err := restartedStore.
			LatestFillExecution(ctx, "urn:xb:account:fill-reason")
		if err != nil {
			t.Fatalf("read latest fill reason: %v", err)
		}
		assertLatestFillReason(t, latest)

		after := fillReasonDurableSnapshot(t, ctx, pool)
		if after != before {
			t.Fatalf(
				"reader mutated durable state:\nbefore=%s\nafter=%s",
				before,
				after,
			)
		}
	})

	for _, testCase := range []struct {
		name string
		kind string
		data string
		want string
	}{
		{
			name: "unknown bracket leg",
			kind: "bracket",
			data: "unknown",
			want: "unknown durable bracket leg",
		},
		{
			name: "empty bracket leg",
			kind: "bracket",
			data: "",
			want: `unknown durable bracket leg ""`,
		},
		{
			name: "whitespace bracket leg",
			kind: "bracket",
			data: " ",
			want: `unknown durable bracket leg " "`,
		},
		{
			name: "case shifted bracket leg",
			kind: "bracket",
			data: "STOP_LOSS",
			want: `unknown durable bracket leg "STOP_LOSS"`,
		},
		{
			name: "mixed case bracket leg",
			kind: "bracket",
			data: "Stop_Loss",
			want: `unknown durable bracket leg "Stop_Loss"`,
		},
		{
			name: "padded bracket leg",
			kind: "bracket",
			data: " stop_loss ",
			want: `unknown durable bracket leg " stop_loss "`,
		},
		{
			name: "invalid bracket does not fall through to stopout",
			kind: "bracket-stopout",
			data: "unknown",
			want: `unknown durable bracket leg "unknown"`,
		},
		{
			name: "invalid bracket does not fall through to flatten",
			kind: "bracket-flatten",
			data: "unknown",
			want: `unknown durable bracket leg "unknown"`,
		},
		{
			name: "fill and order account mismatch",
			kind: "order-account",
			want: "fill/order account authority mismatch",
		},
		{
			name: "intent account mismatch",
			kind: "intent-account",
			want: "fill intent account authority mismatch",
		},
		{
			name: "intent command account mismatch",
			kind: "command-account",
			want: "fill intent account authority mismatch",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			migrateFillReasonDatabase(t, ctx, pool)
			seedFillReasonHistory(t, ctx, pool)
			seedInvalidFillReasonAuthority(
				t,
				ctx,
				pool,
				testCase.kind,
				testCase.data,
			)
			before := fillReasonDurableSnapshot(t, ctx, pool)

			apiPool := runtimeRoleLoginPool(
				t,
				pool,
				"platformgo_invalid_fill_reason_api_login",
				"platformgo_api",
			)
			store := platformpostgres.NewCompatibilityStore(apiPool)
			assertFillReasonStoreFailsClosed(
				t,
				ctx,
				store,
				"current reader",
				testCase.want,
			)
			apiPool.Close()
			restartedAPIPool := runtimeRoleLoginPool(
				t,
				pool,
				"platformgo_invalid_fill_reason_restart_api_login",
				"platformgo_api",
			)
			restartedStore := platformpostgres.NewCompatibilityStore(
				restartedAPIPool,
			)
			assertFillReasonStoreFailsClosed(
				t,
				ctx,
				restartedStore,
				"restarted reader",
				testCase.want,
			)
			after := fillReasonDurableSnapshot(t, ctx, pool)
			if after != before {
				t.Fatalf(
					"failed readers mutated durable state:\nbefore=%s\nafter=%s",
					before,
					after,
				)
			}
		})
	}
}

func assertLatestFillReason(
	t *testing.T,
	latest platformedge.FillExecutionView,
) {
	t.Helper()
	if latest.FillID != "019fa844-26c0-7000-8000-000000000016" ||
		latest.Reason != "stop_loss" {
		t.Fatalf(
			"latest fill = %#v, want bracket-precedence stop_loss",
			latest,
		)
	}
}

func seedFillReasonHistory(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
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
			 '2026-07-26T16:00:00Z'),
			('019fa844-26c0-7000-8000-000000000044',
			 'urn:xb:account:fill-reason', 4, 'submit_order', 1,
			 '{}', 'completed', '{}', 1785081600000000004,
			 '2026-07-26T16:00:00Z'),
			('019fa844-26c0-7000-8000-000000000045',
			 'urn:xb:account:fill-reason', 5, 'submit_order', 1,
			 '{}', 'completed', '{}', 1785081600000000005,
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
			 202, '{}', convert_to('{}', 'UTF8'), '2030-01-01T00:00:00Z'),
			('fill-reason', 'take-profit-precedence',
			 decode(repeat('44', 32), 'hex'),
			 '019fa844-26c0-7000-8000-000000000044', 'completed',
			 202, '{}', convert_to('{}', 'UTF8'), '2030-01-01T00:00:00Z'),
			('fill-reason', 'entry-manual',
			 decode(repeat('55', 32), 'hex'),
			 '019fa844-26c0-7000-8000-000000000045', 'completed',
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
			 '019fa844-26c0-7000-8000-000000000053',
			 'entry', 0, false, 1),
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
			 NULL, NULL, NULL, false, 1),
			('019fa844-26c0-7000-8000-000000000009',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 'MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
			 false, true, '019fa844-26c0-7000-8000-000000000031',
			 NULL, NULL, NULL, false, 1);
		INSERT INTO trading.order_intents (
			order_id, command_id, account_id, intent_id
		) VALUES
			('019fa844-26c0-7000-8000-000000000002',
			 '019fa844-26c0-7000-8000-000000000044',
			 'urn:xb:account:fill-reason',
			 'stopout:TAKE-PROFIT-PRECEDENCE'),
			('019fa844-26c0-7000-8000-000000000003',
			 '019fa844-26c0-7000-8000-000000000041',
			 'urn:xb:account:fill-reason', 'stopout:42:BTC-PERP'),
			('019fa844-26c0-7000-8000-000000000004',
			 '019fa844-26c0-7000-8000-000000000042',
			 'urn:xb:account:fill-reason',
			 'flatten:01JZTESTFLATTEN0000000000'),
			('019fa844-26c0-7000-8000-000000000005',
			 '019fa844-26c0-7000-8000-000000000045',
			 'urn:xb:account:fill-reason', 'manual-entry'),
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
			 'close', 'TAKER', 0, 'USDC', 1785081600000000007, 10),
			('019fa844-26c0-7000-8000-000000000019',
			 '019fa844-26c0-7000-8000-000000000009',
			 '019fa844-26c0-7000-8000-000000000029',
			 'urn:xb:account:fill-reason', 'BTC-PERP', 'SELL',
			 60000, 0.02, '019fa844-26c0-7000-8000-000000000031',
			 'close', 'TAKER', 0, 'USDC', 1785081600000000000, 10)`); err != nil {
		t.Fatalf("seed canonical fill-reason history: %v", err)
	}
}

func migrateFillReasonDatabase(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate fill-reason database: %v", err)
	}
}

func seedInvalidFillReasonAuthority(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	kind string,
	data string,
) {
	t.Helper()
	switch kind {
	case "bracket", "bracket-stopout", "bracket-flatten":
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
				$1, 1, false, 1
			)`,
			data,
		); err != nil {
			t.Fatalf("seed invalid bracket authority: %v", err)
		}
		if kind != "bracket" {
			intentID := "stopout:INVALID-BRACKET"
			if kind == "bracket-flatten" {
				intentID = "flatten:INVALID-BRACKET"
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.commands (
					command_id, account_id, account_sequence, command_type,
					schema_version, canonical_payload, status, result,
					logical_time, completed_at
				) VALUES (
					'019fa844-26c0-7000-8000-000000000046',
					'urn:xb:account:fill-reason', 6, 'submit_order', 1,
					'{}', 'completed', '{}', 1785081600000000006,
					'2026-07-26T16:00:00Z'
				);
				INSERT INTO trading.idempotency_records (
					scope, idempotency_key, request_hash, command_id, state,
					response_status, response_headers, response_body,
					expires_at
				) VALUES (
					'fill-reason', 'invalid-bracket-intent',
					decode(repeat('46', 32), 'hex'),
					'019fa844-26c0-7000-8000-000000000046', 'completed',
					202, '{}', convert_to('{}', 'UTF8'),
					'2030-01-01T00:00:00Z'
				)`); err != nil {
				t.Fatalf("seed invalid bracket command authority: %v", err)
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.order_intents (
					order_id, command_id, account_id, intent_id
				) VALUES (
					'019fa844-26c0-7000-8000-000000000008',
					'019fa844-26c0-7000-8000-000000000046',
					'urn:xb:account:fill-reason', $1
				)`,
				intentID,
			); err != nil {
				t.Fatalf("seed invalid bracket intent authority: %v", err)
			}
		}
	case "order-account":
		if _, err := pool.Exec(ctx, `
			INSERT INTO trading.orders (
				order_id, account_id, instrument_id, side, order_type,
				time_in_force, status, quantity, filled_quantity,
				average_fill_price, triggered, reduce_only, position_id,
				bracket_id, bracket_leg, bracket_leg_index, has_rested,
				version
			) VALUES (
				'019fa844-26c0-7000-8000-000000000008',
				'urn:xb:account:other-fill-reason', 'BTC-PERP', 'SELL',
				'STOP_MARKET', 'GTC', 'filled', 0.02, 0.02, 60000,
				false, true, '019fa844-26c0-7000-8000-000000000032',
				'019fa844-26c0-7000-8000-000000000052',
				'stop_loss', 1, false, 1
			)`); err != nil {
			t.Fatalf("seed mismatched order authority: %v", err)
		}
	case "intent-account", "command-account":
		commandAccount := "urn:xb:account:fill-reason"
		intentAccount := "urn:xb:account:fill-reason"
		commandSequence := int64(6)
		if kind == "intent-account" {
			intentAccount = "urn:xb:account:other-fill-reason"
		} else {
			commandAccount = "urn:xb:account:other-fill-reason"
			commandSequence = 1
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin invalid intent authority fixture: %v", err)
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.commands (
				command_id, account_id, account_sequence, command_type,
				schema_version, canonical_payload, status, result,
				logical_time, completed_at
			) VALUES (
				'019fa844-26c0-7000-8000-000000000047',
				$1, $2, 'submit_order', 1, '{}', 'completed', '{}',
				1785081600000000007, '2026-07-26T16:00:00Z'
			)`,
			commandAccount,
			commandSequence,
		); err != nil {
			t.Fatalf("seed invalid intent command authority: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id, state,
				response_status, response_headers, response_body, expires_at
			) VALUES (
				'fill-reason', 'invalid-intent-authority',
				decode(repeat('47', 32), 'hex'),
				'019fa844-26c0-7000-8000-000000000047', 'completed',
				202, '{}', convert_to('{}', 'UTF8'),
				'2030-01-01T00:00:00Z'
			);
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
				NULL, NULL, NULL, false, 1
			)`); err != nil {
			t.Fatalf("seed invalid intent order authority: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.order_intents (
				order_id, command_id, account_id, intent_id
			) VALUES (
				'019fa844-26c0-7000-8000-000000000008',
				'019fa844-26c0-7000-8000-000000000047',
				$1, 'flatten:INVALID-AUTHORITY'
			)`,
			intentAccount,
		); err != nil {
			t.Fatalf("seed invalid intent authority: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit invalid intent authority fixture: %v", err)
		}
	default:
		t.Fatalf("unknown invalid fill-reason fixture kind %q", kind)
	}
	if _, err := pool.Exec(ctx, `
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

func assertFillReasonStoreFailsClosed(
	t *testing.T,
	ctx context.Context,
	store *platformpostgres.CompatibilityStore,
	name string,
	want string,
) {
	t.Helper()
	page, filterErr := store.FilterFillExecutions(
		ctx,
		"urn:xb:account:fill-reason",
		platformpostgres.FillExecutionFilter{Limit: 20},
	)
	if len(page.Items) != 0 ||
		page.Total != 0 ||
		page.NextCursor != nil ||
		page.PrevCursor != nil {
		t.Fatalf(
			"%s filter returned a partial page on error: %#v",
			name,
			page,
		)
	}
	if filterErr == nil || !strings.Contains(filterErr.Error(), want) {
		t.Fatalf(
			"%s filter error = %v, want fail-closed %q",
			name,
			filterErr,
			want,
		)
	}
	latest, latestErr := store.LatestFillExecution(
		ctx,
		"urn:xb:account:fill-reason",
	)
	if latest != (platformedge.FillExecutionView{}) {
		t.Fatalf(
			"%s latest returned a partial view on error: %#v",
			name,
			latest,
		)
	}
	if latestErr == nil || !strings.Contains(latestErr.Error(), want) {
		t.Fatalf(
			"%s latest error = %v, want fail-closed %q",
			name,
			latestErr,
			want,
		)
	}
}

func fillReasonDurableSnapshot(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'orders', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							order_id::text, account_id, instrument_id,
							side, order_type, status,
							trim_scale(quantity)::text,
							trim_scale(filled_quantity)::text,
							bracket_id::text, bracket_leg,
							bracket_leg_index, version
						)
						ORDER BY order_id
					),
					'[]'::jsonb
				)
				FROM trading.orders
			),
			'fills', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							fill_id::text, order_id::text, input_id::text,
							account_id, instrument_id, side,
							trim_scale(price)::text,
							trim_scale(quantity)::text,
							position_id::text, position_effect,
							trim_scale(fee)::text, fee_currency,
							logical_time,
							trim_scale(effective_leverage)::text
						)
						ORDER BY fill_id
					),
					'[]'::jsonb
				)
				FROM trading.fills
			),
			'intents', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							order_id::text, command_id::text,
							account_id, intent_id
						)
						ORDER BY order_id
					),
					'[]'::jsonb
				)
				FROM trading.order_intents
			),
			'commands', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_array(
							command_id::text, account_id, account_sequence,
							command_type, schema_version, canonical_payload,
							status, result, logical_time
						)
						ORDER BY command_id
					),
					'[]'::jsonb
				)
				FROM trading.commands
			),
			'ledgerTransactions', (
				SELECT count(*) FROM ledger.transactions
			),
			'ledgerEntries', (
				SELECT count(*) FROM ledger.entries
			),
			'inputReceipts', (
				SELECT count(*) FROM engine.input_receipts
			),
			'checkpoints', (
				SELECT count(*) FROM engine.shard_checkpoints
			),
			'outbox', (
				SELECT count(*) FROM messaging.outbox
			)
		)::text`).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot fill-reason durable authority: %v", err)
	}
	return snapshot
}
