package postgres_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
)

const (
	brokerBalancesAccount = "urn:xb:account:00000000-0000-4000-8000-000000000701"
	brokerBalancesTenant  = "urn:xb:tenant:broker-balances"
	brokerBalancesKey     = "xbk_brokerbalances.secret"
	brokerBalancesPath    = "/broker/v1/accounts/" + brokerBalancesAccount + "/balances"
)

type brokerBalanceScaleAuthority struct {
	currency string
	scale    int16
}

func seedBrokerBalanceScaleAuthorities(
	t *testing.T,
	pool *pgxpool.Pool,
	authorities ...brokerBalanceScaleAuthority,
) {
	t.Helper()
	for index, authority := range authorities {
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO trading.instruments (
				instrument_id, revision, price_scale, quantity_scale,
				settlement_currency, settlement_currency_scale,
				initial_margin_rate, maintenance_margin_rate, max_leverage,
				maker_fee_rate, taker_fee_rate
			) VALUES (
				$1, 1, 2, 3, $2, $3,
				0.1, 0.05, 10, 0, 0
			)`,
			fmt.Sprintf("BALANCE-%s-%d", authority.currency, index),
			authority.currency,
			authority.scale,
		); err != nil {
			t.Fatalf(
				"seed broker balance currency authority %s/%d: %v",
				authority.currency,
				authority.scale,
				err,
			)
		}
	}
}

type brokerBalancesQueryTrace struct {
	count   atomic.Int64
	mu      sync.Mutex
	queries []string
}

func (trace *brokerBalancesQueryTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	trace.count.Add(1)
	trace.mu.Lock()
	trace.queries = append(trace.queries, data.SQL)
	trace.mu.Unlock()
	return ctx
}

func (*brokerBalancesQueryTrace) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

func (trace *brokerBalancesQueryTrace) lastQuery(t *testing.T) string {
	t.Helper()
	trace.mu.Lock()
	defer trace.mu.Unlock()
	if len(trace.queries) == 0 {
		t.Fatal("broker balances did not execute a PostgreSQL statement")
	}
	return trace.queries[len(trace.queries)-1]
}

type brokerBalancesHarness struct {
	handler http.Handler
	trace   *brokerBalancesQueryTrace
}

func newBrokerBalancesHarness(
	t *testing.T,
	admin *pgxpool.Pool,
	login string,
	requestID string,
) brokerBalancesHarness {
	t.Helper()

	basePool := runtimeRoleLoginPool(t, admin, login, "platformgo_api")
	trace := &brokerBalancesQueryTrace{}
	config := basePool.Config().Copy()
	config.ConnConfig.Tracer = trace
	apiPool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open traced broker-balances API pool: %v", err)
	}
	t.Cleanup(apiPool.Close)

	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-balances-client-secret-0123456789"),
		BrokerCredentials: []edge.BrokerCredential{{
			Prefix:     "xbk_brokerbalances",
			SecretHash: edge.HashBrokerSecret("secret"),
			Subject:    "urn:xb:apikey:broker-balances-not-tenant",
			Tenant:     brokerBalancesTenant,
			Scopes:     []string{"accounts:read"},
		}},
	})
	if err != nil {
		t.Fatalf("create broker-balances authenticator: %v", err)
	}
	if brokerBalancesTenant == "urn:xb:apikey:broker-balances-not-tenant" {
		t.Fatal("broker API-key subject must not be used as tenant authority")
	}
	return brokerBalancesHarness{
		handler: edge.NewServer(edge.ServerConfig{
			Authenticator: authenticator,
			BrokerBalances: platformpostgres.NewCompatibilityStore(
				apiPool,
			),
			RequestID: func() string { return requestID },
		}).Handler(),
		trace: trace,
	}
}

func (harness brokerBalancesHarness) get(
	t *testing.T,
	target string,
) *httptest.ResponseRecorder {
	t.Helper()

	before := harness.trace.count.Load()
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("x-api-key", brokerBalancesKey)
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	if got := harness.trace.count.Load() - before; got != 1 {
		t.Fatalf(
			"broker balances HTTP request executed %d SQL statements, want exactly one",
			got,
		)
	}
	return response
}

func TestBrokerBalancesLeastPrivilegeStatementHTTPAndRestart(t *testing.T) {
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
		"urn:xb:user:broker-balances",
		9701,
	)
	before := brokerBalancesDurableSnapshot(t, ctx, pool)

	first := newBrokerBalancesHarness(
		t,
		pool,
		"platformgo_broker_balances_api",
		"broker-balances-exact",
	)
	assertBrokerBalancesResponse(
		t,
		first.get(t, brokerBalancesPath),
		http.StatusOK,
		"[]\n",
	)
	assertBrokerBalancesAuthorityStatement(t, first.trace.lastQuery(t))

	seedBrokerBalanceScaleAuthorities(
		t,
		pool,
		brokerBalanceScaleAuthority{currency: "BTC", scale: 8},
		brokerBalanceScaleAuthority{currency: "USDC", scale: 2},
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence,
			updated_at
		) VALUES
			($1, 'USDC', 1000.00, 0.00, 1000.00, 1000.00, 17,
			 '2026-07-30T00:00:00Z'),
			($1, 'BTC', 1.50000000, 0.00000000, 1.50000000, 1.50000000, 18,
			 '2026-07-30T00:00:01Z')`,
		brokerBalancesAccount,
	); err != nil {
		t.Fatalf("seed exact broker balance projection: %v", err)
	}
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		"USDC",
		"1000",
		701,
	)
	seedBrokerBalancesLedgerFold(
		t,
		ctx,
		pool,
		brokerBalancesAccount,
		"BTC",
		"1.5",
		702,
	)
	assertBrokerBalancesLedgerFold(t, ctx, pool, brokerBalancesAccount)
	seeded := brokerBalancesDurableSnapshot(t, ctx, pool)
	want := `[{"currency":"BTC","total":"1.5","locked":"0","free":"1.5","equity":"1.5"},{"currency":"USDC","total":"1000","locked":"0","free":"1000","equity":"1000"}]` + "\n"
	assertBrokerBalancesResponse(
		t,
		first.get(t, brokerBalancesPath),
		http.StatusOK,
		want,
	)

	restarted := newBrokerBalancesHarness(
		t,
		pool,
		"platformgo_broker_balances_restart_api",
		"broker-balances-exact",
	)
	assertBrokerBalancesResponse(
		t,
		restarted.get(t, brokerBalancesPath),
		http.StatusOK,
		want,
	)
	assertBrokerBalancesAuthorityStatement(t, restarted.trace.lastQuery(t))
	after := brokerBalancesDurableSnapshot(t, ctx, pool)
	if after != seeded {
		t.Fatalf(
			"broker balance reads mutated durable state:\nbefore=%s\nafter=%s",
			seeded,
			after,
		)
	}
	if before == seeded {
		t.Fatal("exact broker balance fixture did not change the durable snapshot")
	}
}

func TestBrokerBalancesStatementSnapshotAndAuthorityTransitions(t *testing.T) {
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
		"urn:xb:user:broker-balances-transitions",
		9702,
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES (
			'urn:xb:user:broker-balances-transition-foreign',
			'broker-balances-transition-foreign',
			'broker-balances-transition-foreign',
			'urn:xb:tenant:foreign'
		)`); err != nil {
		t.Fatalf("seed foreign broker authority: %v", err)
	}
	harness := newBrokerBalancesHarness(
		t,
		pool,
		"platformgo_broker_balances_transitions",
		"broker-balances-transitions",
	)
	assertBrokerBalancesResponse(
		t,
		harness.get(t, brokerBalancesPath),
		http.StatusOK,
		"[]\n",
	)

	change, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin uncommitted broker authority transition: %v", err)
	}
	if _, err := change.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = 'urn:xb:tenant:foreign'
		 WHERE account_id = $1`,
		brokerBalancesAccount,
	); err != nil {
		_ = change.Rollback(ctx)
		t.Fatalf("stage broker profile transition: %v", err)
	}
	assertBrokerBalancesResponse(
		t,
		harness.get(t, brokerBalancesPath),
		http.StatusOK,
		"[]\n",
	)
	if err := change.Commit(ctx); err != nil {
		t.Fatalf("commit broker profile transition: %v", err)
	}
	assertBrokerBalancesResponse(
		t,
		harness.get(t, brokerBalancesPath),
		http.StatusForbidden,
		`{"code":"forbidden","message":"forbidden","requestId":"broker-balances-transitions"}`+"\n",
	)

	profileOnly, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin profile-only broker authority transition: %v", err)
	}
	if _, err := profileOnly.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id =
			       'urn:xb:user:broker-balances-transition-foreign',
		       broker_subject = 'urn:xb:tenant:foreign'
		 WHERE account_id = $1`,
		brokerBalancesAccount,
	); err != nil {
		_ = profileOnly.Rollback(ctx)
		t.Fatalf("set foreign broker ownership: %v", err)
	}
	if _, err := profileOnly.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = $2
		 WHERE account_id = $1`,
		brokerBalancesAccount,
		brokerBalancesTenant,
	); err != nil {
		_ = profileOnly.Rollback(ctx)
		t.Fatalf("commit profile-only broker authority: %v", err)
	}
	if err := profileOnly.Commit(ctx); err != nil {
		t.Fatalf("commit profile-only broker authority: %v", err)
	}
	assertBrokerBalancesResponse(
		t,
		harness.get(t, brokerBalancesPath),
		http.StatusForbidden,
		`{"code":"forbidden","message":"forbidden","requestId":"broker-balances-transitions"}`+"\n",
	)
	if _, err := pool.Exec(ctx, `
		UPDATE identity.user_accounts
		   SET user_id = 'urn:xb:user:broker-balances-transitions',
		       broker_subject = $2
		 WHERE account_id = $1`,
		brokerBalancesAccount,
		brokerBalancesTenant,
	); err != nil {
		t.Fatalf("restore complete broker authority: %v", err)
	}
	assertBrokerBalancesResponse(
		t,
		harness.get(t, brokerBalancesPath),
		http.StatusOK,
		"[]\n",
	)
}

func TestBrokerBalancesCancellationReleasesSingleConnectionPool(t *testing.T) {
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
		"urn:xb:user:broker-balances-cancellation",
		9703,
	)
	basePool := runtimeRoleLoginPool(
		t,
		pool,
		"platformgo_broker_balances_cancellation",
		"platformgo_api",
	)
	config := basePool.Config().Copy()
	config.MaxConns = 1
	singlePool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open single-connection broker balance pool: %v", err)
	}
	t.Cleanup(singlePool.Close)
	store := platformpostgres.NewCompatibilityStore(singlePool)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	values, err := store.BrokerBalances(
		canceled,
		brokerBalancesTenant,
		brokerBalancesAccount,
	)
	if err == nil || values != nil {
		t.Fatalf(
			"canceled broker balance read values=%#v error=%v, want nil/error",
			values,
			err,
		)
	}
	values, err = store.BrokerBalances(
		ctx,
		brokerBalancesTenant,
		brokerBalancesAccount,
	)
	if err != nil || values == nil || len(values) != 0 {
		t.Fatalf(
			"post-cancellation broker balance read values=%#v error=%v",
			values,
			err,
		)
	}
}

func assertBrokerBalancesAuthorityStatement(t *testing.T, query string) {
	t.Helper()
	for _, relation := range []string{
		"identity.user_accounts",
		"identity.account_profiles",
		"ledger.balances",
		"trading.currency_scales",
	} {
		if !strings.Contains(query, relation) {
			t.Fatalf(
				"broker balance statement does not bind %s:\n%s",
				relation,
				query,
			)
		}
	}
}

func assertBrokerBalancesResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	body string,
) {
	t.Helper()
	if response.Code != status || response.Body.String() != body {
		t.Fatalf(
			"broker balances response status=%d body=%q, want status=%d body=%q",
			response.Code,
			response.Body.String(),
			status,
			body,
		)
	}
}

func migrateBrokerBalancesCurrent(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate broker-balances database: %v", err)
	}
}

func seedBrokerBalancesAuthority(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	accountID string,
	tenant string,
	userID string,
	login int64,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatalf("seed broker-balances account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES ($1, $1, $1, $2)`,
		userID,
		tenant,
	); err != nil {
		t.Fatalf("seed broker-balances user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES ($1, $2, $3)`,
		userID,
		accountID,
		tenant,
	); err != nil {
		t.Fatalf("seed broker-balances ownership: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, $2, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $3, '2026-07-30T00:00:00Z'
		)`,
		accountID,
		login,
		tenant,
	); err != nil {
		t.Fatalf("seed broker-balances profile: %v", err)
	}
}

func brokerBalancesDurableSnapshot(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	economic := brokerFillsDurableProjectionSnapshot(t, ctx, pool)
	var authority string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'ownership', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_object(
							'user_id', ownership.user_id,
							'account_id', ownership.account_id,
							'broker_subject', ownership.broker_subject
						)
						ORDER BY ownership.account_id
					),
					'[]'::jsonb
				)
				  FROM identity.user_accounts AS ownership
			),
			'profiles', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_object(
							'account_id', profile.account_id,
							'login', profile.login,
							'broker_subject', profile.broker_subject
						)
						ORDER BY profile.account_id
					),
					'[]'::jsonb
				)
				  FROM identity.account_profiles AS profile
			),
			'currency_scales', (
				SELECT COALESCE(
					jsonb_agg(
						jsonb_build_object(
							'currency', scale.currency,
							'scale', scale.scale
						)
						ORDER BY scale.currency COLLATE pg_catalog."C"
					),
					'[]'::jsonb
				)
				  FROM trading.currency_scales AS scale
			)
		)::text`,
	).Scan(&authority); err != nil {
		t.Fatalf("snapshot broker balance authority: %v", err)
	}
	return economic + "|" + authority
}

func seedBrokerBalancesLedgerFold(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	accountID string,
	currency string,
	total string,
	ordinal int,
) {
	t.Helper()
	transactionID := fmt.Sprintf(
		"00000000-0000-4000-8101-%012d",
		ordinal,
	)
	inputID := fmt.Sprintf(
		"00000000-0000-4000-8102-%012d",
		ordinal,
	)
	debitID := fmt.Sprintf(
		"00000000-0000-4000-8103-%012d",
		ordinal,
	)
	creditID := fmt.Sprintf(
		"00000000-0000-4000-8104-%012d",
		ordinal,
	)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin broker balance ledger fold: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger.transactions (
			transaction_id, business_key, input_id, logical_time
		) VALUES ($1, $2, $3, $4)`,
		transactionID,
		fmt.Sprintf("broker-balances-fixture:%d", ordinal),
		inputID,
		int64(ordinal),
	); err != nil {
		t.Fatalf("seed broker balance ledger transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger.entries (
			entry_id, transaction_id, account_id, currency, amount
		) VALUES
			($1, $2, $3, $4, $5::numeric),
			($6, $2, 'system:clearing', $4, -$5::numeric)`,
		debitID,
		transactionID,
		accountID,
		currency,
		total,
		creditID,
	); err != nil {
		t.Fatalf("seed balanced broker balance ledger entries: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit broker balance ledger fold: %v", err)
	}
}

func assertBrokerBalancesLedgerFold(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	accountID string,
) {
	t.Helper()
	var unbalanced, projectionMismatch int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM (
			SELECT transaction_id, currency
			  FROM ledger.entries
			 GROUP BY transaction_id, currency
			HAVING sum(amount) <> 0
		  ) AS invalid`,
	).Scan(&unbalanced); err != nil {
		t.Fatalf("inspect broker balance ledger balancing: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH fold AS (
			SELECT account_id, currency, sum(amount) AS total
			  FROM ledger.entries
			 GROUP BY account_id, currency
		)
		SELECT count(*)
		  FROM ledger.balances AS balance
		  LEFT JOIN fold
		    ON fold.account_id = balance.account_id
		   AND fold.currency = balance.currency
		 WHERE balance.account_id = $1
		   AND balance.total <> COALESCE(fold.total, 0)`,
		accountID,
	).Scan(&projectionMismatch); err != nil {
		t.Fatalf("inspect broker balance projection fold: %v", err)
	}
	if unbalanced != 0 || projectionMismatch != 0 {
		t.Fatalf(
			"broker balance ledger authority unbalanced=%d projection_mismatch=%d",
			unbalanced,
			projectionMismatch,
		)
	}
}
