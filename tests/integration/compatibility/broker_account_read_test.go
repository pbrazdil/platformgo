package compatibility_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_tenant_isolation.rs:40
//	test: broker_access_is_scoped_to_its_tenant
//
// The composite source test also covers account listing, balance mutation, and
// channel isolation. This native test ports only its broker account point-read
// assertion and deliberately does not promote the composite ledger row.
func TestBrokerAccountReadIsScopedToItsTenant(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := resetCompatibilityDatabase(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	const (
		tenantA  = "urn:xb:tenant:broker-account-a"
		tenantB  = "urn:xb:tenant:broker-account-b"
		accountA = "urn:xb:account:00000000-0000-4000-8000-000000000801"
		accountB = "urn:xb:account:00000000-0000-4000-8000-000000000802"
		absent   = "urn:xb:account:00000000-0000-4000-8000-000000000899"
	)
	seedBrokerAccountRead(t, ctx, admin, tenantA, accountA, 9801)
	seedBrokerAccountRead(t, ctx, admin, tenantB, accountB, 9802)
	before := brokerAccountReadDigest(t, ctx, admin)

	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	first := newBrokerAccountReadHarness(t, ctx, apiDatabaseURL)

	successBody := `{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000801","login":9801,"userId":"urn:xb:user:broker-account-a","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:10Z"}` + "\n"
	for _, key := range []string{"xbk_accountread.secret", "xbk_accountwild.secret"} {
		response := first.get(t, accountA, key, true)
		assertBrokerAccountReadResponse(t, response, http.StatusOK, successBody)
	}

	for _, test := range []struct {
		name   string
		key    string
		status int
		body   string
	}{
		{
			name:   "invalid credential dominates parsing",
			key:    "invalid",
			status: http.StatusUnauthorized,
			body:   `{"code":"unauthorized","message":"unauthorized","requestId":"broker-account-read"}` + "\n",
		},
		{
			name:   "scope dominates parsing",
			key:    "xbk_accountnoscope.secret",
			status: http.StatusForbidden,
			body:   `{"code":"forbidden","message":"forbidden","requestId":"broker-account-read"}` + "\n",
		},
		{
			name:   "valid scope reaches parsing",
			key:    "xbk_accountread.secret",
			status: http.StatusBadRequest,
			body:   `{"code":"invalid_request","message":"invalid account id","requestId":"broker-account-read"}` + "\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := first.get(t, "not-a-canonical-account", test.key, false)
			assertBrokerAccountReadResponse(t, response, test.status, test.body)
		})
	}

	unknownBody := `{"code":"invalid_request","message":"unknown account","requestId":"broker-account-read"}` + "\n"
	foreign := first.get(t, accountB, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(t, foreign, http.StatusBadRequest, unknownBody)
	missing := first.get(t, absent, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(t, missing, http.StatusBadRequest, unknownBody)

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = $2
		 WHERE account_id = $1`,
		accountA,
		tenantB,
	); err != nil {
		t.Fatal(err)
	}
	inconsistentOwned := first.get(t, accountA, "xbk_accountread.secret", true)
	unavailableBody := `{"code":"unavailable","message":"account view unavailable","requestId":"broker-account-read"}` + "\n"
	assertBrokerAccountReadResponse(
		t,
		inconsistentOwned,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = $2
		 WHERE account_id = $1`,
		accountA,
		tenantA,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = $2
		 WHERE account_id = $1`,
		accountB,
		tenantA,
	); err != nil {
		t.Fatal(err)
	}
	foreignProfileOwn := first.get(t, accountB, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(
		t,
		foreignProfileOwn,
		http.StatusBadRequest,
		unknownBody,
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET broker_subject = $2
		 WHERE account_id = $1`,
		accountB,
		tenantB,
	); err != nil {
		t.Fatal(err)
	}

	// The supported schema rejects this value before it can persist. This
	// disposable-test-only constraint drop proves the reader still fails closed
	// against a corrupt historical projection rather than relying on DDL alone.
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.account_profiles
		DROP CONSTRAINT account_profiles_supported_currency`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'BTC'
		 WHERE account_id = $1`,
		accountB,
	); err != nil {
		t.Fatal(err)
	}
	foreignCorrupt := first.get(t, accountB, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(
		t,
		foreignCorrupt,
		http.StatusBadRequest,
		unknownBody,
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'USDC'
		 WHERE account_id = $1`,
		accountB,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'BTC'
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	corrupt := first.get(t, accountA, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(
		t,
		corrupt,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	if strings.Contains(corrupt.Body.String(), "BTC") {
		t.Fatalf("authorized corrupt response leaked projection: %s", corrupt.Body.String())
	}
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'USDC'
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.account_profiles
		ADD CONSTRAINT account_profiles_supported_currency
		CHECK (base_currency = 'USDC')`); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET created_at = '10000-01-01T00:00:00Z'::timestamptz
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	outOfRangeTime := first.get(t, accountA, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(
		t,
		outOfRangeTime,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	if strings.Contains(outOfRangeTime.Body.String(), "10000") {
		t.Fatalf(
			"out-of-range timestamp leaked projection: %s",
			outOfRangeTime.Body.String(),
		)
	}
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET created_at = '2026-07-30T08:09:10Z'::timestamptz
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `
		DELETE FROM identity.account_profiles
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	incomplete := first.get(t, accountA, "xbk_accountread.secret", true)
	assertBrokerAccountReadResponse(
		t,
		incomplete,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	seedBrokerAccountReadProfile(t, ctx, admin, tenantA, accountA, 9801)

	restarted := newBrokerAccountReadHarness(t, ctx, apiDatabaseURL)
	restartedSuccess := restarted.get(
		t,
		accountA,
		"xbk_accountread.secret",
		true,
	)
	assertBrokerAccountReadResponse(
		t,
		restartedSuccess,
		http.StatusOK,
		successBody,
	)
	restartedForeign := restarted.get(
		t,
		accountB,
		"xbk_accountread.secret",
		true,
	)
	assertBrokerAccountReadResponse(
		t,
		restartedForeign,
		http.StatusBadRequest,
		unknownBody,
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET created_at = '10000-01-01T00:00:00Z'::timestamptz
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}
	restartedOutOfRangeTime := restarted.get(
		t,
		accountA,
		"xbk_accountread.secret",
		true,
	)
	assertBrokerAccountReadResponse(
		t,
		restartedOutOfRangeTime,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET created_at = '2026-07-30T08:09:10Z'::timestamptz
		 WHERE account_id = $1`,
		accountA,
	); err != nil {
		t.Fatal(err)
	}

	after := brokerAccountReadDigest(t, ctx, admin)
	if after != before {
		t.Fatalf("broker account reads changed durable state\nbefore=%s\nafter=%s", before, after)
	}
}

type brokerAccountReadTrace struct {
	count atomic.Int64
	mu    sync.Mutex
	sql   []string
}

func (trace *brokerAccountReadTrace) TraceQueryStart(
	ctx context.Context,
	_ *pgx.Conn,
	data pgx.TraceQueryStartData,
) context.Context {
	trace.count.Add(1)
	trace.mu.Lock()
	trace.sql = append(trace.sql, data.SQL)
	trace.mu.Unlock()
	return ctx
}

func (*brokerAccountReadTrace) TraceQueryEnd(
	context.Context,
	*pgx.Conn,
	pgx.TraceQueryEndData,
) {
}

type brokerAccountReadHarness struct {
	handler http.Handler
	trace   *brokerAccountReadTrace
}

func newBrokerAccountReadHarness(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) brokerAccountReadHarness {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	trace := &brokerAccountReadTrace{}
	config.ConnConfig.Tracer = trace
	apiPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("broker-account-client-secret-0123456789"),
		BrokerCredentials: []edge.BrokerCredential{
			brokerAccountReadCredential(
				"xbk_accountread",
				"urn:xb:apikey:account-read",
				"urn:xb:tenant:broker-account-a",
				[]string{"accounts:read"},
			),
			brokerAccountReadCredential(
				"xbk_accountwild",
				"urn:xb:apikey:account-wild",
				"urn:xb:tenant:broker-account-a",
				[]string{"*"},
			),
			brokerAccountReadCredential(
				"xbk_accountnoscope",
				"urn:xb:apikey:account-noscope",
				"urn:xb:tenant:broker-account-a",
				[]string{"users:write"},
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return brokerAccountReadHarness{
		handler: edge.NewServer(edge.ServerConfig{
			Authenticator: authenticator,
			BrokerAccount: platformpostgres.NewCompatibilityStore(apiPool),
			RequestID:     func() string { return "broker-account-read" },
		}).Handler(),
		trace: trace,
	}
}

func brokerAccountReadCredential(
	prefix string,
	subject string,
	tenant string,
	scopes []string,
) edge.BrokerCredential {
	return edge.BrokerCredential{
		Prefix:     prefix,
		SecretHash: edge.HashBrokerSecret("secret"),
		Subject:    subject,
		Tenant:     tenant,
		Scopes:     scopes,
	}
}

func (harness brokerAccountReadHarness) get(
	t *testing.T,
	accountID string,
	key string,
	wantQuery bool,
) *httptest.ResponseRecorder {
	t.Helper()
	before := harness.trace.count.Load()
	request := httptest.NewRequest(
		http.MethodGet,
		"/broker/v1/accounts/"+accountID,
		nil,
	)
	request.Header.Set("x-api-key", key)
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)
	got := harness.trace.count.Load() - before
	want := int64(0)
	if wantQuery {
		want = 1
	}
	if got != want {
		t.Fatalf("request executed %d SQL statements, want %d", got, want)
	}
	if wantQuery {
		harness.trace.mu.Lock()
		query := harness.trace.sql[len(harness.trace.sql)-1]
		harness.trace.mu.Unlock()
		for _, relation := range []string{
			"identity.user_accounts",
			"identity.account_profiles",
			"trading.accounts",
		} {
			if !strings.Contains(query, relation) {
				t.Fatalf("broker account statement omits %s:\n%s", relation, query)
			}
		}
	}
	return response
}

func seedBrokerAccountRead(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenant string,
	accountID string,
	login int64,
) {
	t.Helper()
	userID := strings.Replace(tenant, "urn:xb:tenant:", "urn:xb:user:", 1)
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject
		) VALUES ($1, $1, $1, $2)`,
		userID,
		tenant,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject
		) VALUES ($1, $2, $3)`,
		userID,
		accountID,
		tenant,
	); err != nil {
		t.Fatal(err)
	}
	seedBrokerAccountReadProfile(t, ctx, pool, tenant, accountID, login)
}

func seedBrokerAccountReadProfile(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenant string,
	accountID string,
	login int64,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, $2, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $3, '2026-07-30T08:09:10Z'
		)`,
		accountID,
		login,
		tenant,
	); err != nil {
		t.Fatal(err)
	}
}

func brokerAccountReadDigest(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) string {
	t.Helper()
	var digest string
	if err := pool.QueryRow(ctx, `
		SELECT jsonb_build_object(
			'ownership', (
				SELECT jsonb_agg(
					jsonb_build_array(user_id, account_id, broker_subject)
					ORDER BY account_id
				)
				FROM identity.user_accounts
			),
			'profiles', (
				SELECT jsonb_agg(
					jsonb_build_array(
						account_id, login, base_currency, market_venue,
						permitted_classes, broker_subject, created_at
					)
					ORDER BY account_id
				)
				FROM identity.account_profiles
			),
			'accounts', (
				SELECT jsonb_agg(
					jsonb_build_array(
						account_id, margin_mode, oms_mode, status, version
					)
					ORDER BY account_id
				)
				FROM trading.accounts
			)
		)::text`,
	).Scan(&digest); err != nil {
		t.Fatal(err)
	}
	return digest
}

func assertBrokerAccountReadResponse(
	t *testing.T,
	response *httptest.ResponseRecorder,
	status int,
	body string,
) {
	t.Helper()
	if response.Code != status || response.Body.String() != body {
		t.Fatalf(
			"status=%d body=%q, want status=%d body=%q",
			response.Code,
			response.Body.String(),
			status,
			body,
		)
	}
}
