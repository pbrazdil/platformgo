package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformv1 "github.com/upcomers-org/platformgo/contracts/gen/platform/v1"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
	"github.com/upcomers-org/platformgo/migrations"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_lifecycle.rs:14
// test: server_boots_rest_openapi_via_real_composition
func TestRuntimeServesRESTAndGRPCFromRealComposition(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	centrifugoURL := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_URL")
	if databaseURL == "" || natsURL == "" || centrifugoURL == "" {
		t.Skip("Phase 3 loopback dependencies are not configured")
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
	apiDatabaseURL := provisionRuntimeLogin(
		t, ctx, pool, databaseURL, "platformgo_api",
	)
	engineDatabaseURL := provisionRuntimeLogin(
		t, ctx, pool, databaseURL, "platformgo_engine",
	)
	outboxDatabaseURL := provisionRuntimeLogin(
		t, ctx, pool, databaseURL, "platformgo_outbox",
	)
	passwordHash, err := application.HashPassword(
		"correct horse battery staple",
		bytes.NewReader(bytes.Repeat([]byte{3}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES (
			'urn:xb:user:trader-1', 'trader1', 'trader1',
			'trader1@example.com', 'trader1@example.com', $1
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
		)`,
	); err != nil {
		t.Fatal(err)
	}
	natsConnection, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(natsConnection)
	if err != nil {
		natsConnection.Close()
		t.Fatal(err)
	}
	_ = js.DeleteStream(ctx, "ENGINE_INPUTS_7")
	natsConnection.Close()
	admitRuntimeAccountConfiguration(t, ctx, pool)
	streamLimits := platformnats.StreamLimits{
		Replicas: 1, MaxMessages: 1_000_000, MaxBytes: 2 << 30,
		MaxMessageBytes: 1 << 20, MaxAge: 30 * 24 * time.Hour,
		DuplicateWindow: 24 * time.Hour,
	}
	restAddress := unusedAddress(t)
	grpcAddress := unusedAddress(t)
	workerHealthAddress := unusedAddress(t)
	bootstrapEngineHealthAddress := unusedAddress(t)
	engineHealthAddress := unusedAddress(t)
	if err := platformruntime.Doctor(ctx, platformruntime.Config{
		DatabaseURL:      apiDatabaseURL,
		NATSURL:          natsURL,
		CentrifugoAPIURL: centrifugoURL,
		CentrifugoAPIKey: os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY"),
	}); err != nil {
		t.Fatalf("doctor: %v", err)
	}
	bootstrapContext, stopBootstrap := context.WithCancel(ctx)
	bootstrapOutboxResult := make(chan error, 1)
	go func() {
		bootstrapOutboxResult <- platformruntime.RunWorkers(
			bootstrapContext,
			platformruntime.Config{
				DatabaseURL:      outboxDatabaseURL,
				NATSURL:          natsURL,
				NATSStreamLimits: streamLimits,
				HealthAddress:    workerHealthAddress,
				ShardID:          7,
			},
			[]string{"outbox-publisher"},
		)
	}()
	bootstrapEngineResult := make(chan error, 1)
	go func() {
		bootstrapEngineResult <- platformruntime.RunWorkers(
			bootstrapContext,
			platformruntime.Config{
				DatabaseURL:      engineDatabaseURL,
				NATSURL:          natsURL,
				NATSStreamLimits: streamLimits,
				HealthAddress:    bootstrapEngineHealthAddress,
				ShardID:          7,
			},
			[]string{"event-consumer"},
		)
	}()
	waitForRuntimeAccount(t, ctx, pool)
	stopBootstrap()
	for name, result := range map[string]<-chan error{
		"outbox": bootstrapOutboxResult,
		"engine": bootstrapEngineResult,
	} {
		select {
		case bootstrapErr := <-result:
			if bootstrapErr != nil {
				t.Fatalf("bootstrap %s worker shutdown: %v", name, bootstrapErr)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("bootstrap %s worker did not shut down", name)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES ('urn:xb:user:trader-1', 'urn:xb:account:acct-1');
		INSERT INTO ledger.balances (
			account_id, currency, total, used, free, equity, ledger_sequence
		) VALUES (
			'urn:xb:account:acct-1', 'USDC', 1000, 0, 1000, 1000, 0
	)`); err != nil {
		t.Fatal(err)
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	serveConfig := platformruntime.Config{
		DatabaseURL:      apiDatabaseURL,
		NATSURL:          natsURL,
		NATSStreamLimits: streamLimits,
		RESTAddress:      restAddress,
		GRPCAddress:      grpcAddress,
		AllowedOrigin:    "*",
		TrustedProxies: []netip.Prefix{
			netip.MustParsePrefix("127.0.0.0/8"),
			netip.MustParsePrefix("::1/128"),
		},
		ClientTokenSecret: []byte("0123456789abcdef0123456789abcdef"),
		APIKeyReplayKeys: []platformruntime.APIKeyReplayKey{{
			ID: "test-v1",
			Key: [32]byte{
				1, 2, 3, 4, 5, 6, 7, 8,
			},
		}},
		BrokerCredentials: []edge.BrokerCredential{
			{
				Prefix: "xbk_full", SecretHash: edge.HashBrokerSecret("full-secret"),
				Subject: "urn:xb:apikey:full",
				Tenant:  "urn:xb:tenant:full",
				Scopes:  []string{"*"},
			},
			{
				Prefix: "xbk_read", SecretHash: edge.HashBrokerSecret("read-secret"),
				Subject: "urn:xb:apikey:read",
				Tenant:  "urn:xb:tenant:read",
				Scopes:  []string{"accounts:read"},
			},
			{
				Prefix: "xbk_write", SecretHash: edge.HashBrokerSecret("write-secret"),
				Subject: "urn:xb:apikey:write",
				Tenant:  "urn:xb:tenant:write",
				Scopes:  []string{"accounts:write"},
			},
			{
				Prefix: "xbk_mint", SecretHash: edge.HashBrokerSecret("mint-secret"),
				Subject: "urn:xb:apikey:mint",
				Tenant:  "urn:xb:tenant:full",
				Scopes:  []string{"accounts:write", "tokens:mint"},
			},
			{
				Prefix: "xbk_ip", SecretHash: edge.HashBrokerSecret("ip-secret"),
				Subject:     "urn:xb:apikey:ip",
				Tenant:      "urn:xb:tenant:ip",
				Scopes:      []string{"accounts:read"},
				IPAllowlist: []netip.Addr{netip.MustParseAddr("203.0.113.7")},
			},
		},
		CentrifugoAPIURL: centrifugoURL,
		CentrifugoAPIKey: os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY"),
		CentrifugoTokenSecret: []byte(
			os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_TOKEN_SECRET"),
		),
		CentrifugoTokenTTL: time.Hour,
		ShardID:            7,
	}
	serveContext, stopServe := context.WithCancel(runContext)
	startServer := func(serverContext context.Context) <-chan error {
		result := make(chan error, 1)
		go func() {
			result <- platformruntime.Serve(serverContext, serveConfig)
		}()
		return result
	}
	serverResult := startServer(serveContext)
	baseURL := "http://" + restAddress
	waitForHealth(t, baseURL+"/healthz")
	unreadyResponse := requestJSON(t, http.MethodGet, baseURL+"/readyz", "", nil)
	if unreadyResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"readyz without engine/outbox status=%d, want 503",
			unreadyResponse.StatusCode,
		)
	}
	_ = unreadyResponse.Body.Close()
	loginResponse := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/auth/login",
		`{"login":"trader1","password":"correct horse battery staple"}`,
		nil,
	)
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	decodeAndClose(t, loginResponse, &login)
	if loginResponse.StatusCode != http.StatusOK || login.AccessToken == "" {
		t.Fatalf("login status=%d body=%#v", loginResponse.StatusCode, login)
	}
	unreadyMutation := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/accounts/urn:xb:account:acct-1/orders",
		`{"intentId":"unready","symbol":"BTC-PERP","side":"buy","type":"MARKET","quantity":"0.001"}`,
		map[string]string{
			"authorization":   "Bearer " + login.AccessToken,
			"idempotency-key": "unready-idem",
		},
	)
	if unreadyMutation.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"mutation without engine/outbox status=%d, want 503",
			unreadyMutation.StatusCode,
		)
	}
	_ = unreadyMutation.Body.Close()
	var unreadyEffects int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.idempotency_records
		 WHERE idempotency_key = 'unready-idem'`,
	).Scan(&unreadyEffects); err != nil {
		t.Fatal(err)
	}
	if unreadyEffects != 0 {
		t.Fatalf("unready mutation created %d durable commands", unreadyEffects)
	}
	outboxWorkerResult := make(chan error, 1)
	go func() {
		outboxWorkerResult <- platformruntime.RunWorkers(
			runContext,
			platformruntime.Config{
				DatabaseURL:      outboxDatabaseURL,
				NATSURL:          natsURL,
				NATSStreamLimits: streamLimits,
				HealthAddress:    workerHealthAddress,
				ShardID:          7,
			},
			[]string{"outbox-publisher"},
		)
	}()
	engineWorkerResult := make(chan error, 1)
	go func() {
		engineWorkerResult <- platformruntime.RunWorkers(
			runContext,
			platformruntime.Config{
				DatabaseURL:      engineDatabaseURL,
				NATSURL:          natsURL,
				NATSStreamLimits: streamLimits,
				HealthAddress:    engineHealthAddress,
				ShardID:          7,
			},
			[]string{"event-consumer"},
		)
	}()
	waitForReadyOrWorkerFailure(
		t,
		baseURL+"/readyz",
		outboxWorkerResult,
		engineWorkerResult,
	)
	readyResponse := requestJSON(t, http.MethodGet, baseURL+"/readyz", "", nil)
	var readiness struct {
		Ready      bool `json:"ready"`
		Components []struct {
			Name    string `json:"name"`
			Healthy bool   `json:"healthy"`
		} `json:"components"`
	}
	decodeAndClose(t, readyResponse, &readiness)
	if readyResponse.StatusCode != http.StatusOK ||
		!readiness.Ready ||
		len(readiness.Components) != 3 {
		t.Fatalf(
			"readyz status=%d body=%#v",
			readyResponse.StatusCode,
			readiness,
		)
	}
	for index, name := range []string{"postgres", "redis", "rabbitmq"} {
		if readiness.Components[index].Name != name ||
			!readiness.Components[index].Healthy {
			t.Fatalf("readyz components=%#v", readiness.Components)
		}
	}
	preflight := requestJSON(
		t,
		http.MethodOptions,
		baseURL+"/v1/auth/login",
		"",
		map[string]string{
			"origin":                        "https://terminal.example.com",
			"access-control-request-method": "POST",
		},
	)
	if preflight.StatusCode != http.StatusNoContent ||
		preflight.Header.Get("access-control-allow-origin") == "" {
		t.Fatalf(
			"preflight status=%d headers=%v",
			preflight.StatusCode,
			preflight.Header,
		)
	}
	_ = preflight.Body.Close()
	health := requestJSON(t, http.MethodGet, baseURL+"/healthz", "", nil)
	for name, wanted := range map[string]string{
		"x-content-type-options": "nosniff",
		"x-frame-options":        "DENY",
		"referrer-policy":        "no-referrer",
	} {
		if health.Header.Get(name) != wanted {
			t.Fatalf("%s = %q, want %q", name, health.Header.Get(name), wanted)
		}
	}
	if health.Header.Get("x-request-id") == "" {
		t.Fatal("health response omitted x-request-id")
	}
	healthBody, err := io.ReadAll(health.Body)
	_ = health.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if health.StatusCode != http.StatusOK || string(healthBody) != "ok" {
		t.Fatalf("health status=%d body=%q, want 200 and exact body ok", health.StatusCode, healthBody)
	}

	openAPIDocuments := make(map[string]map[string]any)
	for _, path := range []string{
		"/v1/openapi.json",
		"/admin/v1/openapi.json",
		"/broker/v1/openapi.json",
	} {
		response := requestJSON(t, http.MethodGet, baseURL+path, "", nil)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s status = %d", path, response.StatusCode)
		}
		var document map[string]any
		decodeAndClose(t, response, &document)
		if document["openapi"] == nil {
			t.Fatalf("%s missing OpenAPI version", path)
		}
		openAPIDocuments[path] = document
	}
	adminOpenAPI := openAPIDocuments["/admin/v1/openapi.json"]
	requireJSONPath(t, adminOpenAPI, "components", "schemas", "LoginRequest")
	requireJSONPath(t, adminOpenAPI, "paths", "/admin/v1/auth/login", "post")
	requireJSONPath(t, adminOpenAPI, "components", "securitySchemes", "bearer")
	clientOpenAPI := openAPIDocuments["/v1/openapi.json"]
	requireJSONPath(t, clientOpenAPI, "paths", "/v1/accounts/{accountId}/funding", "get")
	requireJSONPath(t, clientOpenAPI, "components", "schemas", "FundingView")
	requireJSONPath(t, clientOpenAPI, "components", "securitySchemes", "bearer")
	requireJSONPath(
		t,
		clientOpenAPI,
		"paths",
		"/v1/accounts/{accountId}/funding",
		"get",
		"security",
	)
	parameters, ok := requireJSONPath(
		t,
		clientOpenAPI,
		"paths",
		"/v1/accounts/{accountId}/orders",
		"post",
		"parameters",
	).([]any)
	if !ok {
		t.Fatal("client OpenAPI order parameters are not an array")
	}
	foundIdempotencyKey := false
	for _, parameter := range parameters {
		object, ok := parameter.(map[string]any)
		if ok &&
			object["name"] == "Idempotency-Key" &&
			object["in"] == "header" {
			foundIdempotencyKey = true
		}
	}
	if !foundIdempotencyKey {
		t.Fatal("client OpenAPI omitted Idempotency-Key header")
	}
	authHeader := map[string]string{"authorization": "Bearer " + login.AccessToken}
	submitBody := `{"intentId":"intent-rest","symbol":"BTC-PERP","side":"buy","type":"MARKET","quantity":"0.001"}`
	submitHeaders := map[string]string{
		"authorization":   "Bearer " + login.AccessToken,
		"idempotency-key": "rest-idem",
	}
	first := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/accounts/urn:xb:account:acct-1/orders",
		submitBody,
		submitHeaders,
	)
	firstBody, err := io.ReadAll(first.Body)
	_ = first.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf(
			"first submit status=%d body=%s, want 202",
			first.StatusCode,
			firstBody,
		)
	}
	waitForRuntimeOrderRejection(
		t,
		ctx,
		pool,
		"rest-idem",
		outboxWorkerResult,
		engineWorkerResult,
	)
	waitForReady(t, baseURL+"/readyz")
	second := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/accounts/urn:xb:account:acct-1/orders",
		submitBody,
		submitHeaders,
	)
	secondBody, err := io.ReadAll(second.Body)
	_ = second.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if first.StatusCode != http.StatusAccepted ||
		second.StatusCode != http.StatusAccepted ||
		!bytes.Equal(firstBody, secondBody) {
		t.Fatalf(
			"submit statuses=%d,%d bodies=%s,%s",
			first.StatusCode,
			second.StatusCode,
			firstBody,
			secondBody,
		)
	}
	var storedOrderStatus int
	var storedOrderHeaders []byte
	var storedOrderBody []byte
	if err := pool.QueryRow(ctx, `
		SELECT response_status, response_headers, response_body
		  FROM trading.idempotency_records
		 WHERE idempotency_key = 'rest-idem'`,
	).Scan(
		&storedOrderStatus,
		&storedOrderHeaders,
		&storedOrderBody,
	); err != nil {
		t.Fatal(err)
	}
	if storedOrderStatus != first.StatusCode ||
		!bytes.Equal(storedOrderBody, firstBody) ||
		!bytes.Contains(storedOrderHeaders, []byte(`"content-type"`)) ||
		first.Header.Get("content-type") != "application/json" {
		t.Fatalf(
			"stored order response status=%d headers=%s body=%s wire status=%d headers=%v body=%s",
			storedOrderStatus,
			storedOrderHeaders,
			storedOrderBody,
			first.StatusCode,
			first.Header,
			firstBody,
		)
	}
	stopServe()
	select {
	case serveErr := <-serverResult:
		if serveErr != nil {
			t.Fatalf("server restart shutdown: %v", serveErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not stop for replay restart")
	}
	serveContext, stopServe = context.WithCancel(runContext)
	defer stopServe()
	serverResult = startServer(serveContext)
	waitForHealth(t, baseURL+"/healthz")
	waitForReady(t, baseURL+"/readyz")
	afterRestart := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/accounts/urn:xb:account:acct-1/orders",
		submitBody,
		submitHeaders,
	)
	afterRestartBody, err := io.ReadAll(afterRestart.Body)
	_ = afterRestart.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if afterRestart.StatusCode != http.StatusAccepted ||
		!bytes.Equal(firstBody, afterRestartBody) {
		t.Fatalf(
			"post-restart replay status=%d bodies=%s,%s",
			afterRestart.StatusCode,
			firstBody,
			afterRestartBody,
		)
	}
	var restAccepted edge.OrderAccepted
	if err := json.Unmarshal(firstBody, &restAccepted); err != nil {
		t.Fatal(err)
	}
	for _, resource := range []string{"orders", "positions", "balances"} {
		response := requestJSON(
			t,
			http.MethodGet,
			baseURL+"/v1/accounts/urn:xb:account:acct-1/"+resource,
			"",
			authHeader,
		)
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || response.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d body=%s err=%v", resource, response.StatusCode, body, readErr)
		}
		if resource == "orders" &&
			bytes.Contains(body, []byte(`"intentId":"intent-rest"`)) {
			t.Fatalf("terminally rejected order remained visible: %s", body)
		}
		if resource == "balances" &&
			!bytes.Contains(body, []byte(`"currency":"USDC"`)) {
			t.Fatalf("balances=%s", body)
		}
	}
	for _, denial := range []struct {
		name    string
		path    string
		headers map[string]string
		want    int
	}{
		{
			name:    "cross-account",
			path:    "/v1/accounts/urn:xb:account:acct-2/positions",
			headers: authHeader,
			want:    http.StatusForbidden,
		},
		{
			name: "anonymous",
			path: "/v1/accounts/urn:xb:account:acct-1/positions",
			want: http.StatusUnauthorized,
		},
		{
			name:    "invalid-account",
			path:    "/v1/accounts/not-a-urn/positions",
			headers: authHeader,
			want:    http.StatusBadRequest,
		},
	} {
		response := requestJSON(
			t, http.MethodGet, baseURL+denial.path, "", denial.headers,
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
	crossSubmit := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/accounts/urn:xb:account:acct-2/orders",
		submitBody,
		submitHeaders,
	)
	if crossSubmit.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-account submit status=%d, want 403", crossSubmit.StatusCode)
	}
	_ = crossSubmit.Body.Close()
	realtime := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/me/realtime/token",
		`{}`,
		authHeader,
	)
	var realtimeToken edge.RealtimeToken
	decodeAndClose(t, realtime, &realtimeToken)
	if realtime.StatusCode != http.StatusOK ||
		realtimeToken.Token == "" ||
		len(realtimeToken.Channels) != 1 ||
		realtimeToken.Channels[0] != "user:trader-1" {
		t.Fatalf("realtime token status=%d body=%#v", realtime.StatusCode, realtimeToken)
	}
	anonymousRealtime := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/v1/me/realtime/token",
		`{}`,
		nil,
	)
	if anonymousRealtime.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"anonymous realtime token status=%d, want 401",
			anonymousRealtime.StatusCode,
		)
	}
	_ = anonymousRealtime.Body.Close()

	for _, brokerAuth := range []struct {
		name   string
		key    string
		source string
		want   int
	}{
		{
			name: "allowlisted source",
			key:  "xbk_ip.ip-secret", source: "203.0.113.7",
			want: http.StatusOK,
		},
		{
			name: "non-allowlisted source",
			key:  "xbk_ip.ip-secret", source: "198.51.100.9",
			want: http.StatusUnauthorized,
		},
		{
			name: "missing forwarded source",
			key:  "xbk_ip.ip-secret",
			want: http.StatusUnauthorized,
		},
		{
			name: "malformed key",
			key:  "xbk_deadbeef.notreal",
			want: http.StatusUnauthorized,
		},
		{
			name: "missing key",
			want: http.StatusUnauthorized,
		},
	} {
		t.Run("broker auth "+brokerAuth.name, func(t *testing.T) {
			headers := map[string]string{}
			if brokerAuth.key != "" {
				headers["x-api-key"] = brokerAuth.key
			}
			if brokerAuth.source != "" {
				headers["x-forwarded-for"] = brokerAuth.source
			}
			response := requestJSON(
				t,
				http.MethodGet,
				baseURL+"/broker/v1/ping",
				"",
				headers,
			)
			_ = response.Body.Close()
			if response.StatusCode != brokerAuth.want {
				t.Fatalf(
					"status=%d, want %d",
					response.StatusCode,
					brokerAuth.want,
				)
			}
		})
	}
	fullBrokerHeaders := map[string]string{
		"x-api-key": "xbk_full.full-secret", "idempotency-key": "echo-key",
	}
	firstEcho := requestJSON(
		t, http.MethodPost, baseURL+"/broker/v1/echo", "{}", fullBrokerHeaders,
	)
	var firstEchoBody map[string]string
	decodeAndClose(t, firstEcho, &firstEchoBody)
	secondEcho := requestJSON(
		t, http.MethodPost, baseURL+"/broker/v1/echo", "{}", fullBrokerHeaders,
	)
	var secondEchoBody map[string]string
	decodeAndClose(t, secondEcho, &secondEchoBody)
	if firstEcho.StatusCode != http.StatusOK ||
		secondEcho.StatusCode != http.StatusOK ||
		firstEchoBody["id"] == "" ||
		firstEchoBody["id"] != secondEchoBody["id"] {
		t.Fatalf(
			"broker echo statuses=%d,%d bodies=%v,%v",
			firstEcho.StatusCode,
			secondEcho.StatusCode,
			firstEchoBody,
			secondEchoBody,
		)
	}
	withoutKeyFirst := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/echo",
		"{}",
		map[string]string{"x-api-key": "xbk_full.full-secret"},
	)
	var withoutKeyFirstBody map[string]string
	decodeAndClose(t, withoutKeyFirst, &withoutKeyFirstBody)
	withoutKeySecond := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/echo",
		"{}",
		map[string]string{"x-api-key": "xbk_full.full-secret"},
	)
	var withoutKeySecondBody map[string]string
	decodeAndClose(t, withoutKeySecond, &withoutKeySecondBody)
	if withoutKeyFirst.StatusCode != http.StatusOK ||
		withoutKeySecond.StatusCode != http.StatusOK ||
		withoutKeyFirstBody["id"] == "" ||
		withoutKeyFirstBody["id"] == withoutKeySecondBody["id"] ||
		withoutKeyFirstBody["id"] == firstEchoBody["id"] {
		t.Fatalf(
			"keyless echo statuses=%d,%d keyed=%v first=%v second=%v",
			withoutKeyFirst.StatusCode,
			withoutKeySecond.StatusCode,
			firstEchoBody,
			withoutKeyFirstBody,
			withoutKeySecondBody,
		)
	}
	brokerUser := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/users",
		`{"login":"crm-trader-1","email":"trader1@crm.example"}`,
		map[string]string{"x-api-key": "xbk_mint.mint-secret"},
	)
	var createdUser edge.BrokerUserResult
	decodeAndClose(t, brokerUser, &createdUser)
	if brokerUser.StatusCode != http.StatusCreated ||
		!createdUser.Created ||
		createdUser.ID == "" {
		t.Fatalf("broker user status=%d body=%#v", brokerUser.StatusCode, createdUser)
	}
	otherTenantUserResponse := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/users",
		`{"login":"other-crm-trader","email":"TRADER1@crm.example"}`,
		map[string]string{"x-api-key": "xbk_write.write-secret"},
	)
	var otherTenantUser edge.BrokerUserResult
	decodeAndClose(t, otherTenantUserResponse, &otherTenantUser)
	if otherTenantUserResponse.StatusCode != http.StatusCreated ||
		!otherTenantUser.Created ||
		otherTenantUser.ID == "" ||
		otherTenantUser.ID == createdUser.ID {
		t.Fatalf(
			"tenant-scoped user convergence failed: first=%#v other=%#v status=%d",
			createdUser,
			otherTenantUser,
			otherTenantUserResponse.StatusCode,
		)
	}
	crossTenantToken := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/users/"+otherTenantUser.ID+"/token",
		`{}`,
		map[string]string{"x-api-key": "xbk_full.full-secret"},
	)
	if crossTenantToken.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"cross-tenant token mint status=%d, want 400",
			crossTenantToken.StatusCode,
		)
	}
	_ = crossTenantToken.Body.Close()
	crossTenantAccount := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/accounts",
		fmt.Sprintf(`{"userId":%q}`, otherTenantUser.ID),
		map[string]string{
			"x-api-key":       "xbk_full.full-secret",
			"idempotency-key": "cross-tenant-account",
		},
	)
	if crossTenantAccount.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"cross-tenant account status=%d, want 400",
			crossTenantAccount.StatusCode,
		)
	}
	_ = crossTenantAccount.Body.Close()
	var crossTenantEffects int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.idempotency_records
		 WHERE idempotency_key = 'cross-tenant-account'`,
	).Scan(&crossTenantEffects); err != nil {
		t.Fatal(err)
	}
	if crossTenantEffects != 0 {
		t.Fatalf("cross-tenant denial created %d command effects", crossTenantEffects)
	}
	accountHeaders := map[string]string{
		"x-api-key": "xbk_full.full-secret", "idempotency-key": "account-key",
	}
	accountBody := fmt.Sprintf(`{"userId":%q}`, createdUser.ID)
	firstAccount := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/accounts",
		accountBody,
		accountHeaders,
	)
	firstAccountBytes, err := io.ReadAll(firstAccount.Body)
	_ = firstAccount.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var createdAccount edge.BrokerAccountResult
	if err := json.Unmarshal(firstAccountBytes, &createdAccount); err != nil {
		t.Fatal(err)
	}
	waitForBrokerAccount(t, ctx, pool, "account-key", createdAccount.ID)
	waitForReady(t, baseURL+"/readyz")
	replayedAccount := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/accounts",
		accountBody,
		accountHeaders,
	)
	replayedAccountBytes, err := io.ReadAll(replayedAccount.Body)
	_ = replayedAccount.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	var replayedAccountBody edge.BrokerAccountResult
	if err := json.Unmarshal(replayedAccountBytes, &replayedAccountBody); err != nil {
		t.Fatal(err)
	}
	if firstAccount.StatusCode != http.StatusCreated ||
		replayedAccount.StatusCode != http.StatusCreated ||
		!bytes.Equal(firstAccountBytes, replayedAccountBytes) ||
		createdAccount.ID == "" ||
		createdAccount.ID != replayedAccountBody.ID ||
		createdAccount.CreatedAt != replayedAccountBody.CreatedAt {
		t.Fatalf(
			"broker account statuses=%d,%d bodies=%#v,%#v",
			firstAccount.StatusCode,
			replayedAccount.StatusCode,
			createdAccount,
			replayedAccountBody,
		)
	}
	var storedAccountStatus int
	var storedAccountHeaders []byte
	var storedAccountBody []byte
	if err := pool.QueryRow(ctx, `
		SELECT response_status, response_headers, response_body
		  FROM trading.idempotency_records
		 WHERE idempotency_key = 'account-key'`,
	).Scan(
		&storedAccountStatus,
		&storedAccountHeaders,
		&storedAccountBody,
	); err != nil {
		t.Fatal(err)
	}
	if storedAccountStatus != firstAccount.StatusCode ||
		!bytes.Equal(storedAccountBody, firstAccountBytes) ||
		!bytes.Contains(storedAccountHeaders, []byte(`"content-type"`)) ||
		firstAccount.Header.Get("content-type") != "application/json" {
		t.Fatalf(
			"stored account response status=%d headers=%s body=%s wire status=%d headers=%v body=%s",
			storedAccountStatus,
			storedAccountHeaders,
			storedAccountBody,
			firstAccount.StatusCode,
			firstAccount.Header,
			firstAccountBytes,
		)
	}
	readOnlyAccount := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/accounts",
		accountBody,
		map[string]string{
			"x-api-key": "xbk_read.read-secret", "idempotency-key": "account-key",
		},
	)
	if readOnlyAccount.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"read-only broker account status=%d, want 403",
			readOnlyAccount.StatusCode,
		)
	}
	_ = readOnlyAccount.Body.Close()
	brokerUserReplay := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/users",
		`{"login":"different-handle","email":"TRADER1@crm.example"}`,
		map[string]string{"x-api-key": "xbk_full.full-secret"},
	)
	var convergedUser edge.BrokerUserResult
	decodeAndClose(t, brokerUserReplay, &convergedUser)
	if brokerUserReplay.StatusCode != http.StatusCreated ||
		convergedUser.Created ||
		convergedUser.ID != createdUser.ID {
		t.Fatalf(
			"broker user convergence status=%d first=%#v second=%#v",
			brokerUserReplay.StatusCode,
			createdUser,
			convergedUser,
		)
	}
	tokenPath := baseURL + "/broker/v1/users/" + createdUser.ID + "/token"
	brokerToken := requestJSON(
		t,
		http.MethodPost,
		tokenPath,
		`{"ttlSecs":120}`,
		map[string]string{"x-api-key": "xbk_mint.mint-secret"},
	)
	var delegated edge.BrokerTokenResponse
	decodeAndClose(t, brokerToken, &delegated)
	if brokerToken.StatusCode != http.StatusOK ||
		delegated.AccessToken == "" ||
		delegated.ExpiresInSecs != 120 {
		t.Fatalf("broker token status=%d body=%#v", brokerToken.StatusCode, delegated)
	}
	delegatedProfile := requestJSON(
		t,
		http.MethodGet,
		baseURL+"/v1/me",
		"",
		map[string]string{"authorization": "Bearer " + delegated.AccessToken},
	)
	var profile edge.UserProfile
	decodeAndClose(t, delegatedProfile, &profile)
	if delegatedProfile.StatusCode != http.StatusOK ||
		profile.UserID != createdUser.ID ||
		profile.Login != "crm-trader-1" {
		t.Fatalf(
			"delegated profile status=%d body=%#v",
			delegatedProfile.StatusCode,
			profile,
		)
	}
	noMint := requestJSON(
		t,
		http.MethodPost,
		tokenPath,
		`{}`,
		map[string]string{"x-api-key": "xbk_write.write-secret"},
	)
	if noMint.StatusCode != http.StatusForbidden {
		t.Fatalf("no-mint broker token status=%d, want 403", noMint.StatusCode)
	}
	_ = noMint.Body.Close()
	unknownUser := requestJSON(
		t,
		http.MethodPost,
		baseURL+"/broker/v1/users/urn:xb:user:2zzzzzzzzzzzzzzzzzzzzzz/token",
		`{}`,
		map[string]string{"x-api-key": "xbk_full.full-secret"},
	)
	if unknownUser.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown broker user token status=%d, want 400", unknownUser.StatusCode)
	}
	_ = unknownUser.Body.Close()

	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	grpcContext := metadata.NewOutgoingContext(
		ctx,
		metadata.Pairs("authorization", "Bearer "+login.AccessToken),
	)
	crossTransportReplay, err := platformv1.NewTradingServiceClient(connection).
		SubmitOrder(
			grpcContext,
			&platformv1.SubmitOrderRequest{
				AccountId:      "urn:xb:account:acct-1",
				IntentId:       "intent-rest",
				Symbol:         "BTC-PERP",
				Side:           platformv1.Side_SIDE_BUY,
				Type:           platformv1.OrderType_ORDER_TYPE_MARKET,
				Quantity:       "0.0010",
				IdempotencyKey: "rest-idem",
			},
		)
	if err != nil ||
		crossTransportReplay.GetOrderId() != restAccepted.OrderID ||
		crossTransportReplay.GetIntentId() != restAccepted.IntentID {
		t.Fatalf(
			"cross-transport replay=%#v REST=%#v err=%v",
			crossTransportReplay,
			restAccepted,
			err,
		)
	}
	_, err = platformv1.NewTradingServiceClient(connection).SubmitOrder(
		grpcContext,
		&platformv1.SubmitOrderRequest{
			AccountId:      "urn:xb:account:acct-1",
			IntentId:       "intent-rest",
			Symbol:         "BTC-PERP",
			Side:           platformv1.Side_SIDE_BUY,
			Type:           platformv1.OrderType_ORDER_TYPE_MARKET,
			Quantity:       "0.002",
			IdempotencyKey: "rest-idem",
		},
	)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("cross-transport changed replay error=%v, want AlreadyExists", err)
	}
	grpcAccepted, err := platformv1.NewTradingServiceClient(connection).SubmitOrder(
		grpcContext,
		&platformv1.SubmitOrderRequest{
			AccountId:      "urn:xb:account:acct-1",
			IntentId:       "intent-grpc",
			Symbol:         "BTC-PERP",
			Side:           platformv1.Side_SIDE_BUY,
			Type:           platformv1.OrderType_ORDER_TYPE_MARKET,
			Quantity:       "0.002",
			IdempotencyKey: "grpc-idem",
		},
	)
	if err != nil || !strings.HasPrefix(grpcAccepted.GetOrderId(), "urn:xb:order:") {
		t.Fatalf("gRPC accepted=%#v err=%v", grpcAccepted, err)
	}
	waitForRuntimeOrderRejection(
		t,
		ctx,
		pool,
		"grpc-idem",
		outboxWorkerResult,
		engineWorkerResult,
	)
	waitForReady(t, baseURL+"/readyz")

	cancel()
	for _, runtime := range []struct {
		name   string
		result <-chan error
	}{
		{name: "server", result: serverResult},
		{name: "outbox worker", result: outboxWorkerResult},
		{name: "engine worker", result: engineWorkerResult},
	} {
		select {
		case runtimeErr := <-runtime.result:
			if runtimeErr != nil {
				t.Fatalf("%s shutdown: %v", runtime.name, runtimeErr)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not shut down", runtime.name)
		}
	}
	report, err := platformpostgres.NewEngineStore(pool).ReconcileShard(ctx, 7)
	// This compatibility fixture deliberately installs one catalog instrument
	// and one balance directly so read contracts have legacy data. Those
	// account for the only expected projection mismatches; the engine-created
	// accounts and every admitted command must reconcile exactly.
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		report.ConfigurationMismatchCount != 1 ||
		report.LedgerMismatchCount != 2 ||
		report.DeliveryMismatchCount != 0 ||
		report.OrderFillMismatchCount != 0 ||
		report.PositionMismatchCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.MessagingMismatchCount != 0 ||
		report.PendingOutboxMessages != 0 {
		t.Fatalf(
			"post-restart account/order reconciliation=%+v err=%v",
			report,
			err,
		)
	}
}

func admitRuntimeAccountConfiguration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	journal := platformpostgres.NewCommandJournal(pool)
	configurationVersion, err := journal.ConfigurationVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := engine.ParseID("019f9518-8ac8-4ca0-8905-69bc6c165f09")
	if err != nil {
		t.Fatal(err)
	}
	logicalTime := time.Date(2026, time.July, 25, 16, 0, 0, 0, time.UTC)
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "urn:xb:account:acct-1",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatal(err)
	}
	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              7,
		Kind:                 engine.InputKindCommand,
		SourceID:             "phase3-runtime-fixture",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: configurationVersion,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Begin(ctx, application.BeginCommandRequest{
		Scope:            "phase3-runtime-fixture",
		IdempotencyKey:   "configure-account",
		RequestHash:      sha256.Sum256([]byte("configure-account")),
		CommandID:        commandID,
		AccountID:        "urn:xb:account:acct-1",
		AccountSequence:  1,
		CommandType:      string(engine.TradingActionConfigureAccount),
		SchemaVersion:    engine.CurrentSchemaVersion,
		CanonicalPayload: payload.Bytes(),
		OutboxSubject:    "engine.input.7.command.v1",
		OutboxPayload:    outboxPayload,
		LogicalTime:      logicalTime,
		ExpiresAt:        logicalTime.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func waitForRuntimeAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var exists bool
		err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM trading.accounts
				 WHERE account_id = 'urn:xb:account:acct-1'
			)`).Scan(&exists)
		if err == nil && exists {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("runtime account configuration did not commit: %v", err)
		case <-ticker.C:
		}
	}
}

func waitForRuntimeOrderRejection(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	idempotencyKey string,
	workerResults ...<-chan error,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var commandStatus string
		var idempotencyState string
		err := pool.QueryRow(ctx, `
			SELECT command.status, idempotency.state
			  FROM trading.commands AS command
			  JOIN trading.idempotency_records AS idempotency
			    ON idempotency.command_id = command.command_id
			 WHERE idempotency.idempotency_key = $1`,
			idempotencyKey,
		).Scan(&commandStatus, &idempotencyState)
		if err == nil &&
			commandStatus == "rejected" &&
			idempotencyState == "completed" {
			return
		}
		for index, result := range workerResults {
			select {
			case workerErr := <-result:
				t.Fatalf(
					"worker %d exited before command completion: %v",
					index,
					workerErr,
				)
			default:
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf(
				"runtime order did not reject and complete replay: status=%q state=%q err=%v",
				commandStatus,
				idempotencyState,
				err,
			)
		case <-ticker.C:
		}
	}
}

func waitForBrokerAccount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	idempotencyKey string,
	accountID string,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ready bool
		err := pool.QueryRow(ctx, `
			SELECT
				command.status = 'accepted'
				AND idempotency.state = 'completed'
				AND EXISTS (
					SELECT 1
					  FROM identity.user_accounts
					 WHERE account_id = $2
				)
				AND EXISTS (
					SELECT 1
					  FROM identity.account_profiles
					 WHERE account_id = $2
				)
			  FROM trading.commands AS command
			  JOIN trading.idempotency_records AS idempotency
			    ON idempotency.command_id = command.command_id
			 WHERE idempotency.idempotency_key = $1`,
			idempotencyKey,
			accountID,
		).Scan(&ready)
		if err == nil && ready {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("broker account did not converge: ready=%t err=%v", ready, err)
		case <-ticker.C:
		}
	}
}

func waitForReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := http.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("runtime readiness did not recover: %v", err)
		case <-ticker.C:
		}
	}
}

func waitForReadyOrWorkerFailure(
	t *testing.T,
	url string,
	outboxResult <-chan error,
	engineResult <-chan error,
) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var lastStatus int
	var lastBody []byte
	for {
		response, err := http.Get(url)
		if err == nil {
			lastStatus = response.StatusCode
			lastBody, _ = io.ReadAll(response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case workerErr := <-outboxResult:
			t.Fatalf("outbox worker exited before readiness: %v", workerErr)
		case workerErr := <-engineResult:
			t.Fatalf("engine worker exited before readiness: %v", workerErr)
		case <-deadline.C:
			t.Fatalf(
				"runtime readiness did not recover: status=%d body=%s err=%v",
				lastStatus,
				lastBody,
				err,
			)
		case <-ticker.C:
		}
	}
}

func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitForHealth(t *testing.T, url string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		response, err := http.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-deadline.C:
			t.Fatalf("runtime health never became ready: %v", err)
		case <-ticker.C:
		}
	}
}

func requestJSON(
	t *testing.T,
	method string,
	url string,
	body string,
	headers map[string]string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(),
		method,
		url,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("content-type", "application/json")
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func requireJSONPath(
	t *testing.T,
	document map[string]any,
	path ...string,
) any {
	t.Helper()
	var current any = document
	for index, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("OpenAPI path %q is not an object at %q", path, path[:index])
		}
		current, ok = object[segment]
		if !ok {
			t.Fatalf("OpenAPI path %q is missing", path)
		}
	}
	return current
}

func decodeAndClose(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil &&
		!errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}
