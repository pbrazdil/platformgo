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
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/migrations"
)

type ownershipGateClock struct {
	value time.Time
}

func (clock ownershipGateClock) Now() time.Time {
	return clock.value
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:116
//	test: ownership_gate_blocks_cross_account_submit
//
// Adaptations:
//   - The source handler's error/success distinction is exercised through the
//     accepted current-Go HTTP contract: generic 403 versus durable 202.
//   - Current Go authorizes at the HTTP boundary from PostgreSQL-derived,
//     signed account claims. This test does not claim a second application-layer
//     ownership check or ownership revocation for an already-issued token.
//   - The foreign request's permitted rate-limit effect is separated from
//     forbidden command, outbox, and economic effects.
//
// Assertions preserved:
//   - User A cannot submit an order for user B's account.
//   - User B can submit the same source-shaped order for their own account.
//
// Invariant strengthening:
//   - Current Go returns the generic 403 before any command admission.
//   - Current Go returns 202 only after one account-bound command,
//     idempotency, replay, order-intent, shard, and command-outbox graph commits.
//   - The foreign request records one caller-scoped rate claim while leaving
//     the enumerated business and economic projections unchanged.
func TestOwnershipGateBlocksCrossAccountSubmit(t *testing.T) {
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
	if err := migrateAndProvisionCompatibilityFixture(t, ctx, admin, migrations.Files, 7); err != nil {
		t.Fatal(err)
	}

	const (
		userAID     = "urn:xb:user:ownership-a"
		userBID     = "urn:xb:user:ownership-b"
		accountBID  = "urn:xb:account:ownership-b"
		password    = "correct horse battery staple"
		sharedKey   = "ownership-shared-key"
		instrument  = "BTC-PERP"
		foreignID   = "x-on-b"
		ownedID     = "b-on-b"
		runtimeRole = "platformgo_api"
	)
	passwordHash, err := application.HashPassword(
		password,
		bytes.NewReader(bytes.Repeat([]byte{31}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES
			($1, 'trader-a', 'trader-a', 'trader-a@xb.local',
				'trader-a@xb.local', $3),
			($2, 'trader-b', 'trader-b', 'trader-b@xb.local',
				'trader-b@xb.local', $3)`,
		userAID,
		userBID,
		passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			$1, 3, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		)`,
		instrument,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ($1, 'NETTING')`,
		accountBID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ($1, $2)`,
		userBID,
		accountBID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES ($1, 'USDC', 1000, 0, 1000, 1000, 0)`,
		accountBID,
	); err != nil {
		t.Fatal(err)
	}

	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		runtimeRole,
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	now := time.Date(2026, time.July, 27, 21, 30, 0, 0, time.UTC)
	clock := ownershipGateClock{value: now}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-ownership-gate-client-secret"),
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(apiPool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Clock: clock,
			Entropy: bytes.NewReader(append(
				bytes.Repeat([]byte{37}, 32),
				bytes.Repeat([]byte{41}, 32)...,
			)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(apiPool),
		application.OrderSubmissionConfig{
			ShardID:        7,
			Clock:          clock,
			IdempotencyTTL: 24 * time.Hour,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Commands:      submission,
		Identity:      identity,
	}).Handler())
	defer server.Close()

	tokenA := ownershipGateLogin(t, server.URL, "trader-a", password)
	tokenB := ownershipGateLogin(t, server.URL, "trader-b", password)
	ownershipGateRequireCount(t, admin, "SELECT count(*) FROM identity.sessions", 2)

	foreign := ownershipGateSubmit(
		t,
		server.URL,
		accountBID,
		foreignID,
		sharedKey,
		tokenA,
		"ownership-foreign",
	)
	ownershipGateRequireError(
		t,
		foreign,
		http.StatusForbidden,
		"forbidden",
		"ownership-foreign",
	)
	ownershipGateRequireRateCount(t, admin, userAID, 1)
	ownershipGateRequireRateCount(t, admin, userBID, 0)
	ownershipGateRequireForbiddenBoundary(t, admin, accountBID)

	owned := ownershipGateSubmit(
		t,
		server.URL,
		accountBID,
		ownedID,
		sharedKey,
		tokenB,
		"ownership-owned",
	)
	var accepted edge.OrderAccepted
	decodeAndClose(t, owned, &accepted)
	if owned.StatusCode != http.StatusAccepted ||
		accepted.IntentID != ownedID ||
		!strings.HasPrefix(accepted.OrderID, "urn:xb:order:") {
		t.Fatalf(
			"owned submit status=%d intent=%q order-prefix=%t",
			owned.StatusCode,
			accepted.IntentID,
			strings.HasPrefix(accepted.OrderID, "urn:xb:order:"),
		)
	}
	ownershipGateRequireRateCount(t, admin, userAID, 1)
	ownershipGateRequireRateCount(t, admin, userBID, 1)
	ownershipGateRequireAdmission(
		t,
		admin,
		userBID,
		accountBID,
		ownedID,
		sharedKey,
		accepted.OrderID,
		now,
	)
	ownershipGateRequireCount(t, admin, "SELECT count(*) FROM identity.sessions", 2)
}

func ownershipGateLogin(
	t *testing.T,
	serverURL string,
	login string,
	password string,
) string {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/auth/login",
		`{"login":"`+login+`","password":"`+password+`"}`,
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, response, &authenticated)
	if response.StatusCode != http.StatusOK || authenticated.AccessToken == "" {
		t.Fatalf(
			"login %q status=%d access-present=%t",
			login,
			response.StatusCode,
			authenticated.AccessToken != "",
		)
	}
	return authenticated.AccessToken
}

func ownershipGateSubmit(
	t *testing.T,
	serverURL string,
	accountID string,
	intentID string,
	idempotencyKey string,
	token string,
	requestID string,
) *http.Response {
	t.Helper()
	return requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/accounts/"+accountID+"/orders",
		`{"intentId":"`+intentID+`","symbol":"BTC-PERP","side":"BUY",`+
			`"type":"MARKET","quantity":"0.001","timeInForce":"GTC"}`,
		map[string]string{
			"authorization":   "Bearer " + token,
			"idempotency-key": idempotencyKey,
			"x-request-id":    requestID,
		},
	)
}

func ownershipGateRequireError(
	t *testing.T,
	response *http.Response,
	status int,
	code string,
	requestID string,
) {
	t.Helper()
	var body map[string]string
	decodeAndClose(t, response, &body)
	want := map[string]string{
		"code":      code,
		"message":   code,
		"requestId": requestID,
	}
	if response.StatusCode != status ||
		body["code"] != want["code"] ||
		body["message"] != want["message"] ||
		body["requestId"] != want["requestId"] ||
		len(body) != len(want) {
		t.Errorf(
			"error response status=%d body=%#v, want status=%d body=%#v",
			response.StatusCode,
			body,
			status,
			want,
		)
	}
}

func ownershipGateRequireRateCount(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	want int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE((
			SELECT request_count
			  FROM identity.client_rate_limits
			 WHERE owner_user_id = $1
		), 0)`,
		userID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("rate count for %s=%d, want %d", userID, count, want)
	}
}

func ownershipGateRequireForbiddenBoundary(
	t *testing.T,
	pool *pgxpool.Pool,
	accountID string,
) {
	t.Helper()
	var (
		idempotency  int
		commands     int
		replays      int
		intents      int
		outbox       int
		shards       int
		orders       int
		fills        int
		positions    int
		transactions int
		entries      int
		balanceOK    bool
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.idempotency_records),
			(SELECT count(*) FROM trading.commands),
			(SELECT count(*) FROM trading.command_replay_responses),
			(SELECT count(*) FROM trading.order_intents),
			(SELECT count(*) FROM messaging.outbox),
			(SELECT count(*) FROM engine.account_shards),
			(SELECT count(*) FROM trading.orders),
			(SELECT count(*) FROM trading.fills),
			(SELECT count(*) FROM trading.positions),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			EXISTS (
				SELECT 1
				  FROM ledger.balances
				 WHERE account_id = $1
				   AND currency = 'USDC'
				   AND total = 1000
				   AND used = 0
				   AND free = 1000
				   AND equity = 1000
				   AND ledger_sequence = 0
			)`,
		accountID,
	).Scan(
		&idempotency,
		&commands,
		&replays,
		&intents,
		&outbox,
		&shards,
		&orders,
		&fills,
		&positions,
		&transactions,
		&entries,
		&balanceOK,
	); err != nil {
		t.Fatal(err)
	}
	if idempotency != 0 || commands != 0 || replays != 0 || intents != 0 ||
		outbox != 0 || shards != 0 || orders != 0 || fills != 0 ||
		positions != 0 || transactions != 0 || entries != 0 || !balanceOK {
		t.Fatalf(
			"foreign business effects idem=%d commands=%d replays=%d intents=%d outbox=%d shards=%d orders=%d fills=%d positions=%d transactions=%d entries=%d balance-ok=%t",
			idempotency,
			commands,
			replays,
			intents,
			outbox,
			shards,
			orders,
			fills,
			positions,
			transactions,
			entries,
			balanceOK,
		)
	}
}

func ownershipGateRequireAdmission(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	accountID string,
	intentID string,
	idempotencyKey string,
	acceptedOrderID string,
	now time.Time,
) {
	t.Helper()
	scope := strings.Join([]string{
		string(edge.AudienceClient),
		userID,
		http.MethodPost,
		"/v1/accounts/" + accountID + "/orders",
	}, "\x1f")
	var (
		idempotencyState string
		commandStatus    string
		commandSequence  int64
		storedAccountID  string
		responseStatus   int
		storedIntentID   string
		intentAccountID  string
		orderID          string
		outboxSubject    string
		messageID        string
		commandID        string
		outboxPayload    []byte
		shardAccountID   string
		shardID          int64
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			idempotency.state,
			command.status,
			command.account_sequence,
			command.account_id,
			replay.response_status,
			intent.intent_id,
			intent.account_id,
			intent.order_id::text,
			outbox.subject,
			outbox.message_id::text,
			command.command_id::text,
			outbox.payload,
			shard.account_id,
			shard.shard_id
		  FROM trading.idempotency_records AS idempotency
		  JOIN trading.commands AS command
		    ON command.command_id = idempotency.command_id
		  JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = command.command_id
		  JOIN trading.order_intents AS intent
		    ON intent.command_id = command.command_id
		  JOIN messaging.outbox AS outbox
		    ON outbox.message_id = command.command_id
		  JOIN engine.account_shards AS shard
		    ON shard.account_id = command.account_id
		 WHERE idempotency.scope = $1
		   AND idempotency.idempotency_key = $2`,
		scope,
		idempotencyKey,
	).Scan(
		&idempotencyState,
		&commandStatus,
		&commandSequence,
		&storedAccountID,
		&responseStatus,
		&storedIntentID,
		&intentAccountID,
		&orderID,
		&outboxSubject,
		&messageID,
		&commandID,
		&outboxPayload,
		&shardAccountID,
		&shardID,
	); err != nil {
		t.Fatal(err)
	}
	if idempotencyState != "in_progress" ||
		commandStatus != "pending" ||
		commandSequence != 1 ||
		storedAccountID != accountID ||
		responseStatus != http.StatusAccepted ||
		storedIntentID != intentID ||
		intentAccountID != accountID ||
		acceptedOrderID != "urn:xb:order:"+orderID ||
		outboxSubject != "engine.input.7.command.v1" ||
		messageID != commandID ||
		shardAccountID != accountID ||
		shardID != 7 {
		t.Fatalf(
			"admission state=%q command=%q sequence=%d account=%q response=%d intent=%q intent-account=%q accepted-order-match=%t subject=%q id-match=%t shard-account=%q shard=%d",
			idempotencyState,
			commandStatus,
			commandSequence,
			storedAccountID,
			responseStatus,
			storedIntentID,
			intentAccountID,
			acceptedOrderID == "urn:xb:order:"+orderID,
			outboxSubject,
			messageID == commandID,
			shardAccountID,
			shardID,
		)
	}
	input, action, err := engine.DecodeInputMessage(outboxPayload)
	if err != nil {
		t.Fatal(err)
	}
	if input.InputID.String() != commandID ||
		input.SourceID != userID ||
		input.SourceSequence != 1 ||
		input.ShardID != 7 ||
		input.LogicalTime.UnixNano() != now.UnixNano() ||
		input.InstrumentVersion != 3 ||
		action.SubmitOrder == nil ||
		action.SubmitOrder.AccountID != accountID ||
		action.SubmitOrder.InstrumentID != "BTC-PERP" ||
		action.SubmitOrder.Side != engine.SideBuy ||
		action.SubmitOrder.Type != engine.OrderTypeMarket ||
		action.SubmitOrder.TimeInForce != engine.TimeInForceGTC ||
		action.SubmitOrder.Quantity != "0.001" ||
		action.SubmitOrder.ReduceOnly {
		t.Fatalf(
			"admission input=%#v submit=%#v",
			input,
			action.SubmitOrder,
		)
	}
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.idempotency_records", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.commands", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.command_replay_responses", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.order_intents", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM messaging.outbox", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM engine.account_shards", 1)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.orders", 0)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.fills", 0)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM trading.positions", 0)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM ledger.transactions", 0)
	ownershipGateRequireCount(t, pool, "SELECT count(*) FROM ledger.entries", 0)
}

func ownershipGateRequireCount(
	t *testing.T,
	pool *pgxpool.Pool,
	query string,
	want int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s = %d, want %d", query, count, want)
	}
}
