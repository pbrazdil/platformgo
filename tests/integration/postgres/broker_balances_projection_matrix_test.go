package postgres_test

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBrokerBalancesUsesExplicitCOrderAgainstHostileNumericCollation(
	t *testing.T,
) {
	ctx := context.Background()
	pool := postgresPool(t)
	assertBrokerBalancesACLPostgres19Beta2(t, pool)
	resetDurableSchemas(t, pool)
	migrateBrokerBalancesCurrent(t, ctx, pool)
	seedBrokerBalancesAuthority(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		brokerBalancesTenant,
		"urn:xb:user:broker-balances-collation",
		9901,
	)
	if _, err := pool.Exec(ctx, `
		CREATE SCHEMA broker_balances_collation_test;
		CREATE COLLATION broker_balances_collation_test.numeric_order (
			provider = icu,
			locale = 'und-u-kn-true',
			deterministic = true
		)`); err != nil {
		t.Fatalf("create hostile broker balance collation: %v", err)
	}
	seedBrokerBalanceScaleAuthorities(
		t,
		pool,
		brokerBalanceScaleAuthority{currency: "X10X", scale: 2},
		brokerBalanceScaleAuthority{currency: "X2X", scale: 2},
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES
			($1, 'X2X', 2.00, 0, 2.00, 2.00, 2,
			 '2026-07-30T00:00:02Z'),
			($1, 'X10X', 10.00, 0, 10.00, 10.00, 1,
			 '2026-07-30T00:00:01Z')`,
		brokerBalancesAccount,
	); err != nil {
		t.Fatalf("seed hostile broker balance collation: %v", err)
	}
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		"X2X",
		"2",
		1001,
	)
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		"X10X",
		"10",
		1002,
	)
	assertBrokerBalancesLedgerFold(t, ctx, pool, brokerBalancesAccount)
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"DROP SCHEMA IF EXISTS broker_balances_collation_test CASCADE",
		)
	})

	var cOrder, hostileOrder string
	if err := pool.QueryRow(ctx, `
		SELECT
			string_agg(
				balance.currency,
				',' ORDER BY balance.currency COLLATE pg_catalog."C"
			),
			string_agg(
				balance.currency,
				',' ORDER BY balance.currency
					COLLATE broker_balances_collation_test.numeric_order
			)
		  FROM ledger.balances AS balance
		 WHERE balance.account_id = $1`,
		brokerBalancesAccount,
	).Scan(&cOrder, &hostileOrder); err != nil {
		t.Fatalf("compare C and hostile balance orders: %v", err)
	}
	if cOrder != "X10X,X2X" || hostileOrder != "X2X,X10X" {
		t.Fatalf(
			"collation proof C=%q hostile=%q, want X10X,X2X versus X2X,X10X",
			cOrder,
			hostileOrder,
		)
	}

	before := brokerBalancesDurableSnapshot(t, ctx, pool)
	want := `[{"currency":"X10X","total":"10","locked":"0","free":"10","equity":"10"},{"currency":"X2X","total":"2","locked":"0","free":"2","equity":"2"}]` + "\n"
	first := newBrokerBalancesHarness(
		t,
		pool,
		"platformgo_balance_collation_api",
		"broker-balances-collation",
	)
	assertBrokerBalancesResponse(
		t,
		first.get(t, brokerBalancesPath),
		http.StatusOK,
		want,
	)
	assertBrokerBalancesCOrder(t, first.trace.lastQuery(t))

	restarted := newBrokerBalancesHarness(
		t,
		pool,
		"platformgo_balance_collation_restart",
		"broker-balances-collation",
	)
	assertBrokerBalancesResponse(
		t,
		restarted.get(t, brokerBalancesPath),
		http.StatusOK,
		want,
	)
	assertBrokerBalancesCOrder(t, restarted.trace.lastQuery(t))
	after := brokerBalancesDurableSnapshot(t, ctx, pool)
	if after != before {
		t.Fatalf(
			"collation-stable balance reads mutated durable state:\nbefore=%s\nafter=%s",
			before,
			after,
		)
	}
}

func TestBrokerBalancesAuthorizedCorruptionIsOpaqueAndRestartStable(
	t *testing.T,
) {
	cases := []struct {
		name string
		kind string
	}{
		{name: "missing currency scale", kind: "missing-scale"},
		{name: "invalid currency", kind: "invalid-currency"},
		{name: "nonfinite amount", kind: "nonfinite"},
		{name: "amount exceeds registered scale", kind: "excess-scale"},
	}
	for index, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			assertBrokerBalancesACLPostgres19Beta2(t, pool)
			resetDurableSchemas(t, pool)
			migrateBrokerBalancesCurrent(t, ctx, pool)
			seedBrokerBalancesAuthority(
				t,
				ctx,
				pool,
				brokerBalancesAccount,
				brokerBalancesTenant,
				fmt.Sprintf("urn:xb:user:broker-balances-corrupt-%d", index),
				int64(9910+index),
			)
			seedBrokerBalancesValidBeforeCorruption(t, ctx, pool)
			seedBrokerBalancesCorruption(t, ctx, pool, testCase.kind)
			before := brokerBalancesDurableSnapshot(t, ctx, pool)
			want := `{"code":"unavailable","message":"trading views unavailable","requestId":"broker-balances-corrupt"}` +
				"\n"

			first := newBrokerBalancesHarness(
				t,
				pool,
				fmt.Sprintf("platformgo_balance_corrupt_%02d", index),
				"broker-balances-corrupt",
			)
			response := first.get(t, brokerBalancesPath)
			assertBrokerBalancesResponse(
				t,
				response,
				http.StatusServiceUnavailable,
				want,
			)
			assertBrokerBalancesOpaqueFailure(t, response.Body.String())

			restarted := newBrokerBalancesHarness(
				t,
				pool,
				fmt.Sprintf("platformgo_balance_corrupt_restart_%02d", index),
				"broker-balances-corrupt",
			)
			restartedResponse := restarted.get(t, brokerBalancesPath)
			assertBrokerBalancesResponse(
				t,
				restartedResponse,
				http.StatusServiceUnavailable,
				want,
			)
			assertBrokerBalancesOpaqueFailure(
				t,
				restartedResponse.Body.String(),
			)
			after := brokerBalancesDurableSnapshot(t, ctx, pool)
			if after != before {
				t.Fatalf(
					"corrupt broker balance reads mutated durable state:\nbefore=%s\nafter=%s",
					before,
					after,
				)
			}
		})
	}
}

func assertBrokerBalancesCOrder(t *testing.T, query string) {
	t.Helper()
	normalized := strings.Join(strings.Fields(query), " ")
	if !strings.Contains(
		normalized,
		`ORDER BY balance.currency COLLATE pg_catalog."C"`,
	) {
		t.Fatalf(
			"broker balance query lacks explicit bytewise C ordering:\n%s",
			query,
		)
	}
}

func assertBrokerBalancesOpaqueFailure(t *testing.T, body string) {
	t.Helper()
	for _, leaked := range []string{
		`"currency"`,
		`"total"`,
		`"locked"`,
		`"free"`,
		`"equity"`,
		"AAA",
		"bad!",
		"NaN",
		"1.001",
		"missing",
		"scale",
		"decimal",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("opaque 503 leaked %q: %q", leaked, body)
		}
	}
}

func seedBrokerBalancesValidBeforeCorruption(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	seedBrokerBalanceScaleAuthorities(
		t,
		pool,
		brokerBalanceScaleAuthority{currency: "AAA", scale: 2},
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES (
			$1, 'AAA', 100.00, 0, 100.00, 100.00, 1,
			'2026-07-30T00:00:00Z'
		)`,
		brokerBalancesAccount,
	); err != nil {
		t.Fatalf("seed valid balance before corrupt row: %v", err)
	}
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		"AAA",
		"100",
		1101,
	)
}

func seedBrokerBalancesCorruption(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	kind string,
) {
	t.Helper()

	currency := "ZZZ"
	total := "2"
	switch kind {
	case "missing-scale":
	case "invalid-currency":
		if _, err := pool.Exec(ctx, `
			ALTER TABLE trading.currency_scales
				DROP CONSTRAINT currency_scales_currency_check;
			ALTER TABLE trading.currency_scales
				DISABLE TRIGGER currency_scale_registry_requires_authority;
			INSERT INTO trading.currency_scales (currency, scale)
			VALUES ('bad!', 2);
			ALTER TABLE trading.currency_scales
				ENABLE ALWAYS TRIGGER
					currency_scale_registry_requires_authority`); err != nil {
			t.Fatalf("permit invalid currency in disposable schema: %v", err)
		}
		currency = "bad!"
	case "nonfinite":
		seedBrokerBalanceScaleAuthorities(
			t,
			pool,
			brokerBalanceScaleAuthority{currency: "ZZZ", scale: 2},
		)
		total = "NaN"
	case "excess-scale":
		seedBrokerBalanceScaleAuthorities(
			t,
			pool,
			brokerBalanceScaleAuthority{currency: "ZZZ", scale: 2},
		)
		total = "1.001"
	default:
		t.Fatalf("unknown broker balance corruption kind %q", kind)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES (
			$1, $2, $3::numeric, 0, 1, 1, 2,
			'2026-07-30T00:00:01Z'
		)`,
		brokerBalancesAccount,
		currency,
		total,
	); err != nil {
		t.Fatalf("seed corrupt broker balance: %v", err)
	}
	if total != "NaN" {
		seedBrokerBalancesLedgerFold(
			t,
			ctx,
			pool,
			brokerBalancesAccount,
			currency,
			total,
			1200+len(kind),
		)
	}
}
