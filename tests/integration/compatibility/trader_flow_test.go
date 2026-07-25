package compatibility_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/trading/e2e_rest.rs:10
// test: trader_trading_flow_transport
func TestTraderTradingFlowTransportAgainstPostgreSQL(t *testing.T) {
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
		bytes.NewReader(bytes.Repeat([]byte{7}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES (
			'urn:xb:user:trader-7', 'trader7', 'trader7',
			'trader7@example.com', 'trader7@example.com', $1
		)`,
		passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 3, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:acct-7', 'NETTING');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ('urn:xb:user:trader-7', 'urn:xb:account:acct-7');
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:acct-7', 'USDC', 1000, 0, 1000, 1000, 0
		)`,
	); err != nil {
		t.Fatal(err)
	}

	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-trader-flow-client-secret-32"),
	})
	if err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(pool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{9}, 64)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(pool),
		application.OrderSubmissionConfig{
			ShardID: 7, IdempotencyTTL: 24 * time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Commands:      submission,
		Identity:      identity,
		Trading:       store,
	}).Handler())
	defer server.Close()

	instruments := requestJSON(t, http.MethodGet, server.URL+"/v1/instruments", "", nil)
	var catalog []edge.InstrumentView
	decodeAndClose(t, instruments, &catalog)
	if instruments.StatusCode != http.StatusOK ||
		len(catalog) != 1 ||
		catalog[0].Symbol != "BTC-PERP" {
		t.Fatalf("instruments status=%d body=%#v", instruments.StatusCode, catalog)
	}
	login := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"trader7","password":"correct horse battery staple"}`,
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, login, &authenticated)
	if login.StatusCode != http.StatusOK || authenticated.AccessToken == "" {
		t.Fatalf("login status=%d body=%#v", login.StatusCode, authenticated)
	}
	authHeaders := map[string]string{
		"authorization": "Bearer " + authenticated.AccessToken,
	}
	submitHeaders := map[string]string{
		"authorization":   "Bearer " + authenticated.AccessToken,
		"idempotency-key": "trader-flow-7",
	}
	submitBody := `{"intentId":"intent-7","symbol":"BTC-PERP","side":"BUY","type":"MARKET","quantity":"0.001"}`
	submitted := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/accounts/urn:xb:account:acct-7/orders",
		submitBody,
		submitHeaders,
	)
	var accepted edge.OrderAccepted
	decodeAndClose(t, submitted, &accepted)
	if submitted.StatusCode != http.StatusAccepted ||
		accepted.IntentID != "intent-7" ||
		!strings.HasPrefix(accepted.OrderID, "urn:xb:order:") {
		t.Fatalf("submit status=%d body=%#v", submitted.StatusCode, accepted)
	}

	orders := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/accounts/urn:xb:account:acct-7/orders",
		"",
		authHeaders,
	)
	var pending []edge.OrderView
	decodeAndClose(t, orders, &pending)
	if orders.StatusCode != http.StatusOK ||
		len(pending) != 1 ||
		pending[0].OrderID != accepted.OrderID ||
		pending[0].IntentID != "intent-7" ||
		pending[0].Status != "pending" {
		t.Fatalf("orders status=%d body=%#v", orders.StatusCode, pending)
	}
	positions := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/accounts/urn:xb:account:acct-7/positions",
		"",
		authHeaders,
	)
	var openPositions []edge.PositionView
	decodeAndClose(t, positions, &openPositions)
	if positions.StatusCode != http.StatusOK || len(openPositions) != 0 {
		t.Fatalf("positions status=%d body=%#v", positions.StatusCode, openPositions)
	}
	balances := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/accounts/urn:xb:account:acct-7/balances",
		"",
		authHeaders,
	)
	var available []edge.BalanceView
	decodeAndClose(t, balances, &available)
	if balances.StatusCode != http.StatusOK ||
		len(available) != 1 ||
		available[0].Currency != "USDC" {
		t.Fatalf("balances status=%d body=%#v", balances.StatusCode, available)
	}

	for _, denial := range []struct {
		name    string
		method  string
		path    string
		body    string
		headers map[string]string
		want    int
	}{
		{
			name:    "foreign account",
			method:  http.MethodPost,
			path:    "/v1/accounts/urn:xb:account:foreign/orders",
			body:    submitBody,
			headers: submitHeaders,
			want:    http.StatusForbidden,
		},
		{
			name:   "anonymous",
			method: http.MethodPost,
			path:   "/v1/accounts/urn:xb:account:acct-7/orders",
			body:   submitBody,
			want:   http.StatusUnauthorized,
		},
		{
			name:    "malformed account",
			method:  http.MethodGet,
			path:    "/v1/accounts/not-a-urn/positions",
			headers: authHeaders,
			want:    http.StatusBadRequest,
		},
	} {
		response := requestJSON(
			t,
			denial.method,
			server.URL+denial.path,
			denial.body,
			denial.headers,
		)
		if response.StatusCode != denial.want {
			t.Fatalf(
				"%s status=%d, want %d",
				denial.name,
				response.StatusCode,
				denial.want,
			)
		}
		_ = response.Body.Close()
	}

	var durablePending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.commands AS command
		  JOIN trading.order_intents AS intent
		    ON intent.command_id = command.command_id
		 WHERE command.status = 'pending'
		   AND intent.intent_id = 'intent-7'`,
	).Scan(&durablePending); err != nil {
		t.Fatal(err)
	}
	if durablePending != 1 {
		t.Fatalf("durable pending order intents=%d, want 1", durablePending)
	}
}
