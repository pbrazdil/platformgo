package compatibility_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

// TestBrokerAccountListIsScopedToItsTenant ports only the list assertion from
// the composite source test.
//
// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_tenant_isolation.rs:40
//	test: broker_access_is_scoped_to_its_tenant
//
// Adaptations:
//   - Rust composition queries are replaced by the real Go HTTP edge and
//     least-privilege PostgreSQL 19 reader.
//   - The accepted current-Go UUID URNs and ten-field MyAccountView are used.
//
// Assertions preserved:
//   - A broker lists only accounts owned by its tenant.
//   - The listed accounts are the tenant's accounts.
//
// The source function also asserts point-read, balance-mutation, and channel
// isolation behavior. This test deliberately leaves its composite port-ledger
// row unpromoted.
func TestBrokerAccountListIsScopedToItsTenant(t *testing.T) {
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
		tenantA = "urn:xb:tenant:broker-account-a"
		tenantB = "urn:xb:tenant:broker-account-b"

		filterUser  = "urn:xb:user:00000000-0000-4000-8000-000000000811"
		otherUser   = "urn:xb:user:00000000-0000-4000-8000-000000000812"
		foreignUser = "urn:xb:user:00000000-0000-4000-8000-000000000891"
		absentUser  = "urn:xb:user:00000000-0000-4000-8000-000000000899"

		accountLow     = "urn:xb:account:00000000-0000-4000-8000-000000000801"
		accountHigh    = "urn:xb:account:00000000-0000-4000-8000-000000000802"
		accountEarly   = "urn:xb:account:00000000-0000-4000-8000-000000000803"
		foreignAccount = "urn:xb:account:00000000-0000-4000-8000-000000000892"
	)
	if _, err := admin.Exec(ctx, `
		ALTER TABLE identity.account_profiles
		DROP CONSTRAINT account_profiles_login_key,
		DROP CONSTRAINT account_profiles_supported_currency`); err != nil {
		t.Fatal(err)
	}
	seedBrokerAccountListUser(t, ctx, admin, tenantA, filterUser, "list-filter")
	seedBrokerAccountListUser(t, ctx, admin, tenantA, otherUser, "list-other")
	seedBrokerAccountListUser(t, ctx, admin, tenantB, foreignUser, "list-foreign")
	// Insert in an order deliberately different from the required wire order.
	seedBrokerAccountListAccount(
		t, ctx, admin, tenantA, filterUser, accountHigh, 9801,
		"2026-07-30T08:09:12Z",
	)
	seedBrokerAccountListAccount(
		t, ctx, admin, tenantA, filterUser, accountLow, 9801,
		"2026-07-30T08:09:11Z",
	)
	seedBrokerAccountListAccount(
		t, ctx, admin, tenantA, otherUser, accountEarly, 9700,
		"2026-07-30T08:09:10Z",
	)
	seedBrokerAccountListAccount(
		t, ctx, admin, tenantB, foreignUser, foreignAccount, 9600,
		"2026-07-30T08:09:09Z",
	)
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'FOREIGN-CORRUPTION-MUST-NOT-LEAK'
		 WHERE account_id = $1`,
		foreignAccount,
	); err != nil {
		t.Fatal(err)
	}
	before := brokerAccountReadDigest(t, ctx, admin)

	unfilteredBody := `[{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000803","login":9700,"userId":"urn:xb:user:00000000-0000-4000-8000-000000000812","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:10Z"},{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000801","login":9801,"userId":"urn:xb:user:00000000-0000-4000-8000-000000000811","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:11Z"},{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000802","login":9801,"userId":"urn:xb:user:00000000-0000-4000-8000-000000000811","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:12Z"}]` + "\n"
	filteredBody := `[{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000801","login":9801,"userId":"urn:xb:user:00000000-0000-4000-8000-000000000811","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:11Z"},{"accountId":"urn:xb:account:00000000-0000-4000-8000-000000000802","login":9801,"userId":"urn:xb:user:00000000-0000-4000-8000-000000000811","baseCurrency":"USDC","marginMode":"cross","omsMode":"netting","marketVenue":"hyperliquid","permittedClasses":["perps"],"status":"active","createdAt":"2026-07-30T08:09:12Z"}]` + "\n"

	firstDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	first := newBrokerAccountListHarness(t, ctx, firstDatabaseURL)
	assertBrokerAccountListStableReads(
		t,
		first,
		filterUser,
		foreignUser,
		absentUser,
		unfilteredBody,
		filteredBody,
	)

	unauthorizedBody := `{"code":"unauthorized","message":"unauthorized","requestId":"broker-account-read"}` + "\n"
	for _, test := range []struct {
		name string
		key  string
		body string
		code int
	}{
		{name: "missing HMAC dominates parsing", code: http.StatusUnauthorized, body: unauthorizedBody},
		{name: "invalid HMAC dominates parsing", key: "xbk_accountread.wrong", code: http.StatusUnauthorized, body: unauthorizedBody},
		{name: "scope dominates parsing", key: "xbk_accountnoscope.secret", code: http.StatusForbidden, body: `{"code":"forbidden","message":"forbidden","requestId":"broker-account-read"}` + "\n"},
		{name: "exact scope reaches parsing", key: "xbk_accountread.secret", code: http.StatusBadRequest, body: `{"code":"invalid_request","message":"invalid user id","requestId":"broker-account-read"}` + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := brokerAccountListGet(
				t,
				first,
				"?userId=not-a-canonical-user",
				test.key,
				false,
			)
			assertBrokerAccountReadResponse(t, response, test.code, test.body)
		})
	}

	invalidUserBody := `{"code":"invalid_request","message":"invalid user id","requestId":"broker-account-read"}` + "\n"
	for _, rawQuery := range []string{
		"?userId=",
		"?userId=" + filterUser + "&userId=" + filterUser,
		"?userId=urn:xb:user:not-a-uuid",
		"?userId=urn:xb:user:00000000-0000-4000-8000-00000000081A",
	} {
		response := brokerAccountListGet(
			t,
			first,
			rawQuery,
			"xbk_accountwild.secret",
			false,
		)
		assertBrokerAccountReadResponse(
			t,
			response,
			http.StatusBadRequest,
			invalidUserBody,
		)
	}

	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'LATE-CORRUPTION-MUST-NOT-LEAK'
		 WHERE account_id = $1`,
		accountHigh,
	); err != nil {
		t.Fatal(err)
	}
	corrupt := brokerAccountListGet(
		t,
		first,
		"",
		"xbk_accountread.secret",
		true,
	)
	unavailableBody := `{"code":"unavailable","message":"account list unavailable","requestId":"broker-account-read"}` + "\n"
	assertBrokerAccountReadResponse(
		t,
		corrupt,
		http.StatusServiceUnavailable,
		unavailableBody,
	)
	if strings.Contains(corrupt.Body.String(), accountEarly) ||
		strings.Contains(corrupt.Body.String(), "CORRUPTION") {
		t.Fatalf("corrupt list leaked a valid prefix or storage value: %s", corrupt.Body.String())
	}
	if _, err := admin.Exec(ctx, `
		UPDATE identity.account_profiles
		   SET base_currency = 'USDC'
		 WHERE account_id = $1`,
		accountHigh,
	); err != nil {
		t.Fatal(err)
	}

	secondDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	restarted := newBrokerAccountListHarness(t, ctx, secondDatabaseURL)
	assertBrokerAccountListStableReads(
		t,
		restarted,
		filterUser,
		foreignUser,
		absentUser,
		unfilteredBody,
		filteredBody,
	)

	after := brokerAccountReadDigest(t, ctx, admin)
	if after != before {
		t.Fatalf("broker account list reads changed durable state\nbefore=%s\nafter=%s", before, after)
	}

	const malformedAccount = "urn:xb:account:not-a-uuid"
	seedBrokerAccountListAccount(
		t,
		ctx,
		admin,
		tenantA,
		filterUser,
		malformedAccount,
		9900,
		"2026-07-30T08:09:13Z",
	)
	beforeMalformedReads := brokerAccountReadDigest(t, ctx, admin)
	for _, harness := range []brokerAccountReadHarness{first, restarted} {
		response := brokerAccountListGet(
			t,
			harness,
			"",
			"xbk_accountread.secret",
			true,
		)
		assertBrokerAccountReadResponse(
			t,
			response,
			http.StatusServiceUnavailable,
			unavailableBody,
		)
		if strings.Contains(response.Body.String(), accountEarly) ||
			strings.Contains(response.Body.String(), malformedAccount) {
			t.Fatalf(
				"invalid account id leaked a valid prefix or corrupt id: %s",
				response.Body.String(),
			)
		}
	}
	afterMalformedReads := brokerAccountReadDigest(t, ctx, admin)
	if afterMalformedReads != beforeMalformedReads {
		t.Fatalf(
			"malformed account reads changed durable state\nbefore=%s\nafter=%s",
			beforeMalformedReads,
			afterMalformedReads,
		)
	}
}

func brokerAccountListGet(
	t *testing.T,
	harness brokerAccountReadHarness,
	rawQuery string,
	key string,
	wantQuery bool,
) *httptest.ResponseRecorder {
	t.Helper()
	before := harness.trace.count.Load()
	request := httptest.NewRequest(
		http.MethodGet,
		"/broker/v1/accounts"+rawQuery,
		nil,
	)
	if key != "" {
		request.Header.Set("x-api-key", key)
	}
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
				t.Fatalf("broker account list statement omits %s:\n%s", relation, query)
			}
		}
		if strings.Contains(rawQuery, "userId=") &&
			!strings.Contains(query, "identity.users") {
			t.Fatalf("filtered broker account list statement omits identity.users:\n%s", query)
		}
	}
	return response
}

func newBrokerAccountListHarness(
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
			brokerAccountReadCredential(
				"xbk_accountempty",
				"urn:xb:apikey:account-empty",
				"urn:xb:tenant:broker-account-empty",
				[]string{"accounts:read"},
			),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return brokerAccountReadHarness{
		handler: edge.NewServer(edge.ServerConfig{
			Authenticator:  authenticator,
			BrokerAccounts: platformpostgres.NewCompatibilityStore(apiPool),
			RequestID:      func() string { return "broker-account-read" },
		}).Handler(),
		trace: trace,
	}
}

func assertBrokerAccountListStableReads(
	t *testing.T,
	harness brokerAccountReadHarness,
	filterUser string,
	foreignUser string,
	absentUser string,
	unfilteredBody string,
	filteredBody string,
) {
	t.Helper()
	for _, test := range []struct {
		name     string
		rawQuery string
		key      string
		body     string
	}{
		{name: "exact scope", key: "xbk_accountread.secret", body: unfilteredBody},
		{name: "wildcard scope", key: "xbk_accountwild.secret", body: unfilteredBody},
		{name: "ignored unknown key", rawQuery: "?ignored=value", key: "xbk_accountread.secret", body: unfilteredBody},
		{name: "canonical user filter", rawQuery: "?userId=" + filterUser, key: "xbk_accountread.secret", body: filteredBody},
		{name: "foreign user filter", rawQuery: "?userId=" + foreignUser, key: "xbk_accountread.secret", body: "[]\n"},
		{name: "absent user filter", rawQuery: "?userId=" + absentUser, key: "xbk_accountwild.secret", body: "[]\n"},
		{name: "unfiltered empty tenant", key: "xbk_accountempty.secret", body: "[]\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := brokerAccountListGet(
				t,
				harness,
				test.rawQuery,
				test.key,
				true,
			)
			assertBrokerAccountReadResponse(
				t,
				response,
				http.StatusOK,
				test.body,
			)
		})
	}
}

func seedBrokerAccountListUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenant string,
	userID string,
	login string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, broker_subject, created_at
		) VALUES ($1, $2, $2, $3, '2026-07-30T08:00:00Z')`,
		userID,
		login,
		tenant,
	); err != nil {
		t.Fatal(err)
	}
}

func seedBrokerAccountListAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	tenant string,
	userID string,
	accountID string,
	login int64,
	createdAt string,
) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id, account_id, broker_subject, created_at
		) VALUES ($1, $2, $3, $4)`,
		userID,
		accountID,
		tenant,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, broker_subject, created_at
		) VALUES (
			$1, $2, 'USDC', 'HYPERLIQUID',
			ARRAY['CRYPTOCURRENCY'], $3, $4
		)`,
		accountID,
		login,
		tenant,
		createdAt,
	); err != nil {
		t.Fatal(err)
	}
}
