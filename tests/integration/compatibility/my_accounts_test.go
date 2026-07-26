package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:175
//	test: trader_lists_own_accounts
//
// Adaptations:
//   - The Rust runtime is replaced by the Go HTTP edge and real PostgreSQL.
//   - A second user's account proves that the authenticated read cannot leak it.
//   - The complete frozen MyAccountView wire shape is asserted.
//
// Assertions preserved:
//   - Anonymous access returns 401.
//   - The authenticated caller receives exactly their one account.
//   - accountId, login, status, marginMode, and baseCurrency are preserved.
func TestTraderListsOwnAccounts(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := resetCompatibilityDatabase(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	passwordHash, err := application.HashPassword(
		"correct horse battery staple",
		bytes.NewReader(bytes.Repeat([]byte{17}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES
			(
				'urn:xb:user:owner-1', 'owner1', 'owner1',
				'owner1@example.com', 'owner1@example.com', $1
			),
			(
				'urn:xb:user:other-1', 'other1', 'other1',
				'other1@example.com', 'other1@example.com', $1
			)`,
		passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (
			account_id, oms_mode
		) VALUES
			('urn:xb:account:owner-1', 'NETTING'),
			('urn:xb:account:other-1', 'HEDGING')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (
			user_id, account_id, created_at
		) VALUES
			(
				'urn:xb:user:owner-1', 'urn:xb:account:owner-1',
				'2026-07-26T08:09:10Z'
			),
			(
				'urn:xb:user:other-1', 'urn:xb:account:other-1',
				'2026-07-26T09:10:11Z'
			)`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.account_profiles (
			account_id, login, base_currency, market_venue,
			permitted_classes, created_at, broker_subject
		) VALUES
			(
				'urn:xb:account:owner-1', 73000001, 'USDC', 'HYPERLIQUID',
				ARRAY['CRYPTOCURRENCY'], '2026-07-26T08:09:10Z',
				'urn:xb:tenant:owner'
			),
			(
				'urn:xb:account:other-1', 73000002, 'USDC', 'HYPERLIQUID',
				ARRAY['CRYPTOCURRENCY'], '2026-07-26T09:10:11Z',
				'urn:xb:tenant:other'
			)`,
	); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		pool,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-my-accounts-client-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(apiPool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{23}, 64)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
	}).Handler())
	defer server.Close()

	anonymous := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me/accounts",
		"",
		nil,
	)
	defer anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", anonymous.StatusCode)
	}

	login := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"owner1","password":"correct horse battery staple"}`,
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, login, &authenticated)
	if login.StatusCode != http.StatusOK || authenticated.AccessToken == "" {
		t.Fatalf("login status=%d body=%#v", login.StatusCode, authenticated)
	}

	response := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me/accounts",
		"",
		map[string]string{
			"authorization": "Bearer " + authenticated.AccessToken,
		},
	)
	var accounts []map[string]any
	decodeAndClose(t, response, &accounts)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, body = %#v", response.StatusCode, accounts)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %#v, want caller's one account", accounts)
	}
	want := map[string]any{
		"accountId":        "urn:xb:account:owner-1",
		"login":            float64(73000001),
		"userId":           "urn:xb:user:owner-1",
		"baseCurrency":     "USDC",
		"marginMode":       "cross",
		"omsMode":          "netting",
		"marketVenue":      "hyperliquid",
		"permittedClasses": []any{"perps"},
		"status":           "active",
		"createdAt":        "2026-07-26T08:09:10Z",
	}
	got, err := json.Marshal(accounts[0])
	if err != nil {
		t.Fatal(err)
	}
	expected, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, expected) {
		t.Fatalf("account = %s, want %s", got, expected)
	}

	if _, err := pool.Exec(ctx, `
		DELETE FROM identity.account_profiles
		 WHERE account_id = 'urn:xb:account:owner-1'`,
	); err != nil {
		t.Fatal(err)
	}
	incomplete := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me/accounts",
		"",
		map[string]string{
			"authorization": "Bearer " + authenticated.AccessToken,
		},
	)
	defer incomplete.Body.Close()
	if incomplete.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"incomplete account status = %d, want 503",
			incomplete.StatusCode,
		)
	}
}
