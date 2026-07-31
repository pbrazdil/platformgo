package postgres_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

const (
	brokerFillsMatrixAccount = "urn:xb:account:00000000-0000-4000-8000-000000000501"
	brokerFillsMatrixOther   = "urn:xb:account:00000000-0000-4000-8000-000000000502"
	brokerFillsMatrixTenant  = "urn:xb:tenant:broker-fills-matrix"
	brokerFillsMatrixKey     = "xbk_brokerfills_matrix.secret"
	brokerFillsMatrixPath    = "/broker/v1/accounts/" + brokerFillsMatrixAccount + "/fills"
)

type brokerFillsMatrixQueryCounter struct {
	count atomic.Int64
}

func (counter *brokerFillsMatrixQueryCounter) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	_ pgx.TraceQueryStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*brokerFillsMatrixQueryCounter) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

type brokerFillsMatrixHarness struct {
	handler http.Handler
	counter *brokerFillsMatrixQueryCounter
}

func newBrokerFillsMatrixHarness(
	t *testing.T,
	admin *pgxpool.Pool,
	login string,
	requestID string,
) brokerFillsMatrixHarness {
	t.Helper()

	basePool := runtimeRoleLoginPool(t, admin, login, "platformgo_api")
	counter := &brokerFillsMatrixQueryCounter{}
	config := basePool.Config().Copy()
	config.ConnConfig.Tracer = counter
	tracedPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced broker-fills API pool: %v", err)
	}
	t.Cleanup(tracedPool.Close)

	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-fills-matrix-client-secret"),
		BrokerCredentials: []edge.BrokerCredential{{
			Prefix:     "xbk_brokerfills_matrix",
			SecretHash: edge.HashBrokerSecret("secret"),
			Subject:    "urn:xb:apikey:broker-fills-matrix",
			Tenant:     brokerFillsMatrixTenant,
			Scopes:     []string{"accounts:read"},
		}},
	})
	if err != nil {
		t.Fatalf("create broker-fills authenticator: %v", err)
	}
	return brokerFillsMatrixHarness{
		handler: edge.NewServer(edge.ServerConfig{
			Authenticator: authenticator,
			BrokerFills: platformpostgres.NewCompatibilityStore(
				tracedPool,
			),
			RequestID: func() string { return requestID },
		}).Handler(),
		counter: counter,
	}
}

func (harness brokerFillsMatrixHarness) get(
	t *testing.T,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()

	before := harness.counter.count.Load()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("x-api-key", brokerFillsMatrixKey)
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if got := harness.counter.count.Load() - before; got != 1 {
		t.Fatalf("broker fills HTTP request executed %d SQL statements, want exactly one", got)
	}
	return response
}

type brokerFillsMatrixExpected struct {
	fillID      string
	orderID     string
	positionID  string
	side        string
	tradeType   string
	reason      string
	realizedPnL *string
	settlement  *string
	leverage    *string
	logicalTime int64
	filledAt    string
}

func TestBrokerFillsPostgresHTTPProjectionMatrixIsExactAndRestartStable(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrateBrokerFillsMatrixCurrent(t, ctx, pool)
	seedBrokerFillsMatrixAuthority(t, ctx, pool)
	expected := seedBrokerFillsProjectionMatrix(t, ctx, pool)
	before := brokerFillsMatrixDurableSnapshot(t, ctx, pool)

	first := newBrokerFillsMatrixHarness(
		t,
		pool,
		"platformgo_broker_fills_matrix_api_login",
		"broker-fills-matrix",
	)
	fullBody := brokerFillsMatrixExpectedBody(expected, nil, nil, 25)
	assertBrokerFillsMatrixResponse(
		t,
		first.get(t, brokerFillsMatrixPath+"?limit=25"),
		http.StatusOK,
		fullBody,
	)

	firstPage := expected[:7]
	firstNext := brokerFillsMatrixCursor(firstPage[len(firstPage)-1])
	assertBrokerFillsMatrixResponse(
		t,
		first.get(t, brokerFillsMatrixPath+"?limit=7"),
		http.StatusOK,
		brokerFillsMatrixExpectedBody(firstPage, &firstNext, nil, 25),
	)

	secondPage := expected[7:14]
	secondNext := brokerFillsMatrixCursor(secondPage[len(secondPage)-1])
	secondPrev := brokerFillsMatrixCursor(secondPage[0])
	assertBrokerFillsMatrixResponse(
		t,
		first.get(
			t,
			brokerFillsMatrixPath+"?limit=7&cursor="+firstNext,
		),
		http.StatusOK,
		brokerFillsMatrixExpectedBody(
			secondPage,
			&secondNext,
			&secondPrev,
			25,
		),
	)

	// A backward read from the second page's newest tuple returns the exact
	// first page. The equal-time rows at the page boundary remain ordered and
	// resumed by the complete (logical_time, fill_id) tuple.
	assertBrokerFillsMatrixResponse(
		t,
		first.get(
			t,
			brokerFillsMatrixPath+"?limit=7&cursor="+secondPrev+
				"&direction=prev",
		),
		http.StatusOK,
		brokerFillsMatrixExpectedBody(firstPage, &firstNext, nil, 25),
	)

	restarted := newBrokerFillsMatrixHarness(
		t,
		pool,
		"platformgo_broker_fills_matrix_restart_api_login",
		"broker-fills-matrix",
	)
	assertBrokerFillsMatrixResponse(
		t,
		restarted.get(t, brokerFillsMatrixPath+"?limit=25"),
		http.StatusOK,
		fullBody,
	)
	after := brokerFillsMatrixDurableSnapshot(t, ctx, pool)
	if after != before {
		t.Fatalf(
			"broker fills success reads mutated durable economics:\nbefore=%s\nafter=%s",
			before,
			after,
		)
	}
}

func TestBrokerFillsAuthorizedLateRowCorruptionIsOpaqueAndRestartStable(
	t *testing.T,
) {
	cases := []struct {
		name             string
		kind             string
		historicalTip    bool
		dropPositiveOnly bool
	}{
		{name: "over-scale realized PnL", kind: "over-scale"},
		{name: "mismatched realized PnL currency", kind: "pnl-pair"},
		{name: "invalid trade type", kind: "trade-type"},
		{name: "invalid reason provenance", kind: "reason"},
		{name: "mismatched order authority", kind: "order-account"},
		{name: "mismatched intent authority", kind: "intent-account"},
		{name: "mismatched intent command authority", kind: "command-account"},
		{
			name:          "historical nonfinite NaN leverage",
			kind:          "leverage-nan",
			historicalTip: true,
		},
		{
			name:          "historical nonfinite positive infinity leverage",
			kind:          "leverage-infinity",
			historicalTip: true,
		},
		{
			name:             "historical zero leverage",
			kind:             "leverage-zero",
			historicalTip:    true,
			dropPositiveOnly: true,
		},
		{
			name:             "historical negative leverage",
			kind:             "leverage-negative",
			historicalTip:    true,
			dropPositiveOnly: true,
		},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if testCase.historicalTip {
				if err := platformpostgres.NewMigrator(
					pool,
					migrationFilesThrough(
						t,
						"20260729000400_phase3_command_admission_acl.up.sql",
					),
				).Migrate(ctx); err != nil {
					t.Fatalf("migrate compatible historical fill schema: %v", err)
				}
			} else {
				migrateBrokerFillsMatrixCurrent(t, ctx, pool)
			}
			seedBrokerFillsMatrixAuthority(t, ctx, pool)
			seedBrokerFillsMatrixValidRow(t, ctx, pool)
			if testCase.dropPositiveOnly {
				if _, err := pool.Exec(ctx, `
					ALTER TABLE trading.fills
					DROP CONSTRAINT fills_effective_leverage_positive`); err != nil {
					t.Fatalf("remove positive-leverage constraint in disposable schema: %v", err)
				}
			}
			seedBrokerFillsMatrixCorruptRow(
				t,
				ctx,
				pool,
				testCase.kind,
			)
			before := brokerFillsMatrixDurableSnapshot(t, ctx, pool)
			want := `{"code":"unavailable","message":"trading views unavailable","requestId":"broker-fills-corrupt"}` +
				"\n"

			first := newBrokerFillsMatrixHarness(
				t,
				pool,
				fmt.Sprintf("platformgo_broker_fills_corrupt_%02d", index),
				"broker-fills-corrupt",
			)
			assertBrokerFillsMatrixResponse(
				t,
				first.get(t, brokerFillsMatrixPath+"?limit=10"),
				http.StatusServiceUnavailable,
				want,
			)

			restarted := newBrokerFillsMatrixHarness(
				t,
				pool,
				fmt.Sprintf("platformgo_broker_fills_corrupt_restart_%02d", index),
				"broker-fills-corrupt",
			)
			assertBrokerFillsMatrixResponse(
				t,
				restarted.get(t, brokerFillsMatrixPath+"?limit=10"),
				http.StatusServiceUnavailable,
				want,
			)
			after := brokerFillsMatrixDurableSnapshot(t, ctx, pool)
			if after != before {
				t.Fatalf(
					"corrupt broker fills reads mutated durable economics:\nbefore=%s\nafter=%s",
					before,
					after,
				)
			}
		})
	}
}

func migrateBrokerFillsMatrixCurrent(
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
		t.Fatalf("migrate broker-fills matrix database: %v", err)
	}
}

func seedBrokerFillsMatrixAuthority(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	statements := []struct {
		sql  string
		args []any
	}{
		{sql: `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		)`},
		{sql: `
		INSERT INTO trading.accounts (account_id, oms_mode) VALUES
			($1, 'NETTING'),
			($2, 'NETTING')`,
			args: []any{brokerFillsMatrixAccount, brokerFillsMatrixOther},
		},
		{sql: `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:broker-fills-matrix',
			'broker-fills-matrix',
			'broker-fills-matrix',
			$1
		)`,
			args: []any{brokerFillsMatrixTenant},
		},
		{sql: `
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES (
			'urn:xb:user:broker-fills-matrix', $1, $2
		)`,
			args: []any{brokerFillsMatrixAccount, brokerFillsMatrixTenant},
		},
		{sql: `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, 9501, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $2, '2026-07-30T00:00:00Z'
		)`,
			args: []any{brokerFillsMatrixAccount, brokerFillsMatrixTenant},
		},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed broker-fills matrix authority: %v", err)
		}
	}
}

func seedBrokerFillsProjectionMatrix(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) []brokerFillsMatrixExpected {
	t.Helper()

	tradeTypes := []string{"open", "increase", "reduce", "flip", "close"}
	reasons := []string{
		"manual",
		"stop_loss",
		"take_profit",
		"liquidation",
		"flatten",
	}
	base := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	inserted := make([]brokerFillsMatrixExpected, 0, 25)
	for tradeIndex, tradeType := range tradeTypes {
		for reasonIndex, reason := range reasons {
			index := tradeIndex*len(reasons) + reasonIndex + 1
			orderID := brokerFillsMatrixUUID(2000 + index)
			fillID := brokerFillsMatrixUUID(1000 + index)
			inputID := brokerFillsMatrixUUID(3000 + index)
			positionID := brokerFillsMatrixUUID(4000 + index)
			side := "BUY"
			if index%2 == 0 {
				side = "SELL"
			}
			var bracketLeg any
			switch reason {
			case "stop_loss", "take_profit":
				bracketLeg = reason
			}
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.orders (
					order_id, account_id, instrument_id, side, order_type,
					time_in_force, status, quantity, filled_quantity,
					average_fill_price, triggered, reduce_only, bracket_leg,
					has_rested, version
				) VALUES (
					$1, $2, 'BTC-PERP', $3, 'MARKET', 'IOC', 'FILLED',
					1, 1, 60000, false, false, $4, false, 1
				)`,
				orderID,
				brokerFillsMatrixAccount,
				side,
				bracketLeg,
			); err != nil {
				t.Fatalf("seed matrix order %d: %v", index, err)
			}
			if reason == "liquidation" || reason == "flatten" {
				commandID := brokerFillsMatrixUUID(5000 + index)
				intentID := reason + ":matrix"
				if reason == "liquidation" {
					intentID = "stopout:matrix"
				}
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatalf("begin matrix intent %d: %v", index, err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO trading.commands (
						command_id, account_id, account_sequence, command_type,
						schema_version, canonical_payload, status, result,
						logical_time, completed_at
					) VALUES (
						$1, $2, $3, 'submit_order', 1, '{}', 'completed',
						'{}', $4, '2026-07-30T12:30:00Z'
					)`,
					commandID,
					brokerFillsMatrixAccount,
					index,
					base.UnixNano()+int64(index),
				); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatalf("seed matrix intent command %d: %v", index, err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO trading.idempotency_records (
						scope, idempotency_key, request_hash, command_id, state,
						response_status, response_headers, response_body,
						expires_at
					) VALUES (
						'broker-fills-matrix', $1,
						decode(repeat('ab', 32), 'hex'), $2, 'completed',
						202, '{}', convert_to('{}', 'UTF8'),
						'2030-01-01T00:00:00Z'
					)`,
					fmt.Sprintf("matrix-%02d", index),
					commandID,
				); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatalf("seed matrix intent idempotency %d: %v", index, err)
				}
				if _, err := tx.Exec(ctx, `
					INSERT INTO trading.order_intents (
						order_id, command_id, account_id, intent_id
					) VALUES ($1, $2, $3, $4)`,
					orderID,
					commandID,
					brokerFillsMatrixAccount,
					intentID,
				); err != nil {
					_ = tx.Rollback(ctx)
					t.Fatalf("seed matrix intent %d: %v", index, err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatalf("commit matrix intent %d: %v", index, err)
				}
			}

			var realized, settlement any
			var expectedRealized, expectedSettlement *string
			switch tradeType {
			case "reduce":
				realized = "-12.340000000000000000"
				settlement = "USDC"
				expectedRealized = brokerFillsMatrixString("-12.34")
				expectedSettlement = brokerFillsMatrixString("USDC")
			case "flip":
				realized = "0.000000000000000000"
				settlement = "USDC"
				expectedRealized = brokerFillsMatrixString("0")
				expectedSettlement = brokerFillsMatrixString("USDC")
			case "close":
				realized = "9876.500000000000000000"
				settlement = "USDC"
				expectedRealized = brokerFillsMatrixString("9876.5")
				expectedSettlement = brokerFillsMatrixString("USDC")
			}
			var leverage any
			var expectedLeverage *string
			if index != 1 {
				canonical := []string{"1.25", "2", "3.5", "4", "10"}[tradeIndex]
				leverage = []string{
					"1.250000000000000000",
					"2.000000000000000000",
					"3.500000000000000000",
					"4.000000000000000000",
					"10.000000000000000000",
				}[tradeIndex]
				expectedLeverage = brokerFillsMatrixString(canonical)
			}
			seconds := index
			if index == 18 {
				seconds = 19
			}
			logicalTime := base.Add(time.Duration(seconds) * time.Second).UnixNano()
			if _, err := pool.Exec(ctx, `
				INSERT INTO trading.fills (
					fill_id, order_id, input_id, account_id, instrument_id,
					side, price, quantity, position_id, position_effect,
					realized_pnl, settlement_currency, liquidity_side,
					fee, fee_currency, logical_time, effective_leverage
				) VALUES (
					$1, $2, $3, $4, 'BTC-PERP', $5, 60000, 1, $6, $7,
					$8::numeric, $9, 'TAKER', 0, 'USDC', $10, $11::numeric
				)`,
				fillID,
				orderID,
				inputID,
				brokerFillsMatrixAccount,
				side,
				positionID,
				tradeType,
				realized,
				settlement,
				logicalTime,
				leverage,
			); err != nil {
				t.Fatalf("seed matrix fill %d: %v", index, err)
			}
			inserted = append(inserted, brokerFillsMatrixExpected{
				fillID:      fillID,
				orderID:     "urn:xb:order:" + orderID,
				positionID:  positionID,
				side:        side,
				tradeType:   tradeType,
				reason:      reason,
				realizedPnL: expectedRealized,
				settlement:  expectedSettlement,
				leverage:    expectedLeverage,
				logicalTime: logicalTime,
				filledAt:    base.Add(time.Duration(seconds) * time.Second).Format(time.RFC3339),
			})
		}
	}
	newestFirst := make([]brokerFillsMatrixExpected, len(inserted))
	for index := range inserted {
		newestFirst[index] = inserted[len(inserted)-1-index]
	}
	return newestFirst
}

func brokerFillsMatrixExpectedBody(
	items []brokerFillsMatrixExpected,
	next *string,
	previous *string,
	total int,
) string {
	var body strings.Builder
	body.WriteString(`{"items":[`)
	for index, item := range items {
		if index != 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(
			&body,
			`{"fillId":%q,"orderId":%q,"positionId":%q,"side":%q,"tradeType":%q,"reason":%q,`,
			item.fillID,
			item.orderID,
			item.positionID,
			item.side,
			item.tradeType,
			item.reason,
		)
		if item.realizedPnL == nil {
			body.WriteString(`"realizedPnl":null,"settlementCurrency":null`)
		} else {
			fmt.Fprintf(
				&body,
				`"realizedPnl":%q,"settlementCurrency":%q`,
				*item.realizedPnL,
				*item.settlement,
			)
		}
		if item.leverage != nil {
			fmt.Fprintf(&body, `,"leverage":%q`, *item.leverage)
		}
		fmt.Fprintf(&body, `,"filledAt":%q}`, item.filledAt)
	}
	body.WriteByte(']')
	if next != nil {
		fmt.Fprintf(&body, `,"nextCursor":%q`, *next)
	}
	if previous != nil {
		fmt.Fprintf(&body, `,"prevCursor":%q`, *previous)
	}
	fmt.Fprintf(&body, `,"total":%d}`+"\n", total)
	return body.String()
}

func brokerFillsMatrixCursor(item brokerFillsMatrixExpected) string {
	return base64.RawURLEncoding.EncodeToString([]byte(
		fmt.Sprintf("%d:%s", item.logicalTime, item.fillID),
	))
}

func brokerFillsMatrixUUID(value int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", value)
}

func brokerFillsMatrixString(value string) *string {
	return &value
}

func assertBrokerFillsMatrixResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	body string,
) {
	t.Helper()
	if response.Code != status || response.Body.String() != body {
		t.Fatalf(
			"broker fills response status=%d body=%q, want status=%d body=%q",
			response.Code,
			response.Body.String(),
			status,
			body,
		)
	}
}

func seedBrokerFillsMatrixValidRow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, has_rested, version
		) VALUES (
			'00000000-0000-4000-8000-000000009001',
			$1, 'BTC-PERP', 'BUY', 'MARKET', 'IOC', 'FILLED',
			1, 1, 60000, false, false, false, 1
		)`,
		brokerFillsMatrixAccount,
	); err != nil {
		t.Fatalf("seed valid order before corrupt broker fill: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000009002',
			'00000000-0000-4000-8000-000000009001',
			'00000000-0000-4000-8000-000000009003',
			$1, 'BTC-PERP', 'BUY', 60000, 1,
			'00000000-0000-4000-8000-000000009004', 'open',
			NULL, NULL, 'TAKER', 0, 'USDC', 200, 2
		)`,
		brokerFillsMatrixAccount,
	); err != nil {
		t.Fatalf("seed valid row before corrupt broker fill: %v", err)
	}
}

func seedBrokerFillsMatrixCorruptRow(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	kind string,
) {
	t.Helper()

	orderAccount := brokerFillsMatrixAccount
	bracketLeg := any(nil)
	realized := any(nil)
	settlement := any(nil)
	tradeType := "close"
	leverage := any("2")
	switch kind {
	case "over-scale":
		realized = "1.234"
		settlement = "USDC"
	case "pnl-pair":
		if _, err := pool.Exec(ctx, `
			ALTER TABLE trading.fills
			DROP CONSTRAINT fills_realized_pnl_pair_check`); err != nil {
			t.Fatalf("remove realized-PnL pair constraint in disposable schema: %v", err)
		}
		realized = "1.23"
	case "trade-type":
		tradeType = "reopen"
	case "reason":
		bracketLeg = "trailing_stop"
	case "order-account":
		orderAccount = brokerFillsMatrixOther
	case "intent-account", "command-account":
	case "leverage-nan":
		leverage = "NaN"
	case "leverage-infinity":
		if _, err := pool.Exec(ctx, `
			ALTER TABLE trading.fills
			ALTER COLUMN effective_leverage TYPE numeric`); err != nil {
			t.Fatalf("remove leverage typmod in disposable historical schema: %v", err)
		}
		leverage = "Infinity"
	case "leverage-zero":
		leverage = "0"
	case "leverage-negative":
		leverage = "-1"
	default:
		t.Fatalf("unknown broker fills corruption kind %q", kind)
	}

	const corruptOrderID = "00000000-0000-4000-8000-000000009011"
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.orders (
			order_id, account_id, instrument_id, side, order_type,
			time_in_force, status, quantity, filled_quantity,
			average_fill_price, triggered, reduce_only, bracket_leg,
			has_rested, version
		) VALUES (
			$1, $2, 'BTC-PERP', 'SELL', 'MARKET', 'IOC', 'FILLED',
			1, 1, 60000, false, true, $3, false, 1
		)`,
		corruptOrderID,
		orderAccount,
		bracketLeg,
	); err != nil {
		t.Fatalf("seed corrupt broker fill order: %v", err)
	}
	if kind == "intent-account" || kind == "command-account" {
		commandAccount := brokerFillsMatrixAccount
		intentAccount := brokerFillsMatrixOther
		if kind == "command-account" {
			commandAccount = brokerFillsMatrixOther
			intentAccount = brokerFillsMatrixAccount
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin corrupt broker fill intent authority: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.commands (
				command_id, account_id, account_sequence, command_type,
				schema_version, canonical_payload, status, result,
				logical_time, completed_at
			) VALUES (
				'00000000-0000-4000-8000-000000009012',
				$1, 1, 'submit_order', 1, '{}', 'completed', '{}',
				100, '2026-07-30T00:00:00Z'
			)`,
			commandAccount,
		); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed corrupt broker fill intent command: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.idempotency_records (
				scope, idempotency_key, request_hash, command_id, state,
				response_status, response_headers, response_body, expires_at
			) VALUES (
				'broker-fills-corrupt', 'corrupt-intent',
				decode(repeat('cd', 32), 'hex'),
				'00000000-0000-4000-8000-000000009012', 'completed',
				202, '{}', convert_to('{}', 'UTF8'),
				'2030-01-01T00:00:00Z'
			)`); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed corrupt broker fill intent idempotency: %v", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO trading.order_intents (
				order_id, command_id, account_id, intent_id
			) VALUES (
				$1,
				'00000000-0000-4000-8000-000000009012',
				$2,
				'flatten:corrupt'
			)`,
			corruptOrderID,
			intentAccount,
		); err != nil {
			_ = tx.Rollback(ctx)
			t.Fatalf("seed corrupt broker fill intent authority: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit corrupt broker fill intent authority: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.fills (
			fill_id, order_id, input_id, account_id, instrument_id,
			side, price, quantity, position_id, position_effect,
			realized_pnl, settlement_currency, liquidity_side,
			fee, fee_currency, logical_time, effective_leverage
		) VALUES (
			'00000000-0000-4000-8000-000000009013',
			$1,
			'00000000-0000-4000-8000-000000009014',
			$2, 'BTC-PERP', 'SELL', 59999, 1,
			'00000000-0000-4000-8000-000000009015', $3,
			$4::numeric, $5, 'TAKER', 0, 'USDC', 100, $6::numeric
		)`,
		corruptOrderID,
		brokerFillsMatrixAccount,
		tradeType,
		realized,
		settlement,
		leverage,
	); err != nil {
		t.Fatalf("seed corrupt broker fill: %v", err)
	}
}

func brokerFillsMatrixDurableSnapshot(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var snapshot string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'fills', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(history) ORDER BY history.fill_id),
					'[]'::jsonb
				)
				  FROM (
					SELECT
						fill_id::text AS fill_id,
						order_id::text AS order_id,
						account_id,
						side,
						position_effect,
						trim_scale(realized_pnl)::text AS realized_pnl,
						settlement_currency,
						trim_scale(effective_leverage)::text AS leverage,
						logical_time
					  FROM trading.fills
				) AS history
			),
			'orders', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(history) ORDER BY history.order_id),
					'[]'::jsonb
				)
				  FROM (
					SELECT
						order_id::text AS order_id,
						account_id,
						side,
						status,
						trim_scale(filled_quantity)::text AS filled_quantity,
						version
					  FROM trading.orders
				) AS history
			),
			'positions', (
				SELECT COALESCE(
					jsonb_agg(to_jsonb(history) ORDER BY history.position_id),
					'[]'::jsonb
				)
				  FROM (
					SELECT
						position_id::text AS position_id,
						account_id,
						side,
						status,
						trim_scale(signed_quantity)::text AS signed_quantity,
						trim_scale(realized_pnl)::text AS realized_pnl,
						version
					  FROM trading.positions
				) AS history
			),
			'balances', (
				SELECT COALESCE(
					jsonb_agg(
						to_jsonb(history)
						ORDER BY history.account_id, history.currency
					),
					'[]'::jsonb
				)
				  FROM (
					SELECT
						account_id,
						currency,
						trim_scale(total)::text AS total,
						trim_scale(used)::text AS used,
						trim_scale(free)::text AS free,
						trim_scale(equity)::text AS equity,
						ledger_sequence
					  FROM ledger.balances
				) AS history
			),
			'ledger_entries', (
				SELECT count(*) FROM ledger.entries
			)
		)::text`,
	).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot broker fills durable economics: %v", err)
	}
	return snapshot
}
