package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/adapters/centrifugo"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
	"github.com/upcomers-org/platformgo/migrations"
)

func TestRealtimeWorkerRetriesCommittedPublicationWithStableIdentity(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration dependency is not configured")
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
	if err := migrateAndProvisionCompatibilityFixture(t, ctx, pool, migrations.Files, 23); err != nil {
		t.Fatal(err)
	}
	projectorURL := provisionRuntimeLogin(
		t,
		ctx,
		pool,
		databaseURL,
		"platformgo_realtime",
	)
	if _, err := pool.Exec(
		ctx,
		"REVOKE UPDATE (published_at) ON realtime.publications FROM platformgo_realtime",
	); err != nil {
		t.Fatalf("revoke published acknowledgment privilege: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			`GRANT UPDATE (published_at, claimed_at)
				 ON realtime.publications TO platformgo_realtime`,
		)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:realtime-worker', 'NETTING');
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES ('urn:xb:user:realtime-worker', 'realtime-worker', 'realtime-worker');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES (
			'urn:xb:user:realtime-worker',
			'urn:xb:account:realtime-worker'
		);
		INSERT INTO realtime.channel_sequences (channel, last_sequence)
		VALUES ('user:realtime-worker', 8);
		INSERT INTO realtime.publications (
			channel, event_id, sequence, schema_version, event_type,
			account_id, logical_time, data
		) VALUES
			(
				'user:realtime-worker',
				'019f9460-4b36-4e9b-8f44-682611f72301',
				7,
				1,
				'order.updated',
				'urn:xb:account:realtime-worker',
				123,
				'{"symbol":"BTC-PERP","status":"working"}'
			),
			(
				'user:realtime-worker',
				'019f9460-4b36-4e9b-8f44-682611f72302',
				8,
				1,
				'order.cancelled',
				'urn:xb:account:realtime-worker',
				124,
				'{"symbol":"BTC-PERP","status":"cancelled"}'
			)`,
	); err != nil {
		t.Fatal(err)
	}

	var (
		mu                 sync.Mutex
		allowFirstResponse = make(chan struct{})
		allowRecovery      = make(chan struct{})
		attempts           []struct {
			Channel string        `json:"channel"`
			Data    edgeWireEvent `json:"data"`
		}
	)
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == "/health" {
			writer.WriteHeader(http.StatusOK)
			return
		}
		var body struct {
			Channel string        `json:"channel"`
			Data    edgeWireEvent `json:"data"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(writer, "invalid JSON", http.StatusBadRequest)
			return
		}
		mu.Lock()
		attempts = append(attempts, body)
		attempt := len(attempts)
		mu.Unlock()
		if attempt == 1 {
			select {
			case <-allowFirstResponse:
			case <-request.Context().Done():
				return
			}
			writer.Header().Set("content-type", "application/json")
			// Centrifugo accepted the publication, but the database role is
			// temporarily unable to persist the acknowledgment below.
			_, _ = writer.Write([]byte(`{"result":{}}`))
			return
		}
		select {
		case <-allowRecovery:
		case <-request.Context().Done():
			return
		}
		if attempt == 2 {
			writer.Header().Set("content-type", "application/json")
			_, _ = writer.Write([]byte(
				`{"error":{"code":100,"message":"temporary internal error"}}`,
			))
			return
		}
		if attempt == 3 {
			http.Error(writer, "temporary outage", http.StatusServiceUnavailable)
			return
		}
		writer.Header().Set("content-type", "application/json")
		_, _ = writer.Write([]byte(`{"result":{}}`))
	}))
	defer server.Close()

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	healthAddress := unusedAddress(t)
	go func() {
		result <- platformruntime.RunWorkers(
			runContext,
			platformruntime.Config{
				DatabaseURL:      projectorURL,
				HealthAddress:    healthAddress,
				CentrifugoAPIURL: server.URL,
				CentrifugoAPIKey: "api-key",
				ShardID:          23,
			},
			[]string{"realtime-publisher"},
		)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		firstAttemptObserved := len(attempts) >= 1
		mu.Unlock()
		if firstAttemptObserved {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("realtime worker did not publish the first attempt")
		}
		time.Sleep(20 * time.Millisecond)
	}
	inFlightResponse, err := http.Get("http://" + healthAddress + "/readyz")
	if err != nil {
		t.Fatalf("read in-flight realtime readiness: %v", err)
	}
	_ = inFlightResponse.Body.Close()
	if inFlightResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"realtime worker ready during unacknowledged delivery: %d",
			inFlightResponse.StatusCode,
		)
	}
	if _, err := pool.Exec(
		ctx,
		"REVOKE UPDATE (claimed_at) ON realtime.publications FROM platformgo_realtime",
	); err != nil {
		t.Fatalf("revoke failure-finalization privilege: %v", err)
	}
	close(allowFirstResponse)
	select {
	case runErr := <-result:
		if runErr == nil {
			t.Fatal("worker stayed healthy after both delivery finalizations failed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not fail closed after unknown delivery outcome")
	}
	var unresolvedClaim bool
	if err := pool.QueryRow(ctx, `
		SELECT claimed_at IS NOT NULL
		       AND published_at IS NULL
		       AND last_error IS NULL
		  FROM realtime.publications
		 WHERE channel = 'user:realtime-worker'
		   AND event_id = '019f9460-4b36-4e9b-8f44-682611f72301'`,
	).Scan(&unresolvedClaim); err != nil {
		t.Fatal(err)
	}
	if !unresolvedClaim {
		t.Fatal("dual finalization failure did not retain the fenced claim")
	}
	if _, err := pool.Exec(ctx, `
		GRANT UPDATE (published_at, claimed_at)
		ON realtime.publications TO platformgo_realtime`); err != nil {
		t.Fatalf("restore realtime finalization privileges: %v", err)
	}
	runContext, cancel = context.WithCancel(ctx)
	result = make(chan error, 1)
	go func() {
		result <- platformruntime.RunWorkers(
			runContext,
			platformruntime.Config{
				DatabaseURL:      projectorURL,
				HealthAddress:    healthAddress,
				CentrifugoAPIURL: server.URL,
				CentrifugoAPIKey: "api-key",
				ShardID:          23,
			},
			[]string{"realtime-publisher"},
		)
	}()
	deadline = time.Now().Add(10 * time.Second)
	for {
		response, requestErr := http.Get("http://" + healthAddress + "/readyz")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf(
					"restarted worker ready with unknown delivery outcome: %d",
					response.StatusCode,
				)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restarted realtime worker did not expose readiness")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE realtime.publications
		   SET claimed_at = clock_timestamp() - interval '31 seconds'
		 WHERE channel = 'user:realtime-worker'
		   AND event_id = '019f9460-4b36-4e9b-8f44-682611f72301'
		   AND published_at IS NULL`); err != nil {
		t.Fatalf("accelerate interrupted delivery lease expiry: %v", err)
	}
	close(allowRecovery)
	deadline = time.Now().Add(15 * time.Second)
	for {
		var (
			firstPublished  bool
			secondPublished bool
			firstAttempts   uint32
			secondAttempts  uint32
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				(SELECT published_at IS NOT NULL
				   FROM realtime.publications
				  WHERE channel = 'user:realtime-worker'
				    AND event_id = '019f9460-4b36-4e9b-8f44-682611f72301'),
				(SELECT attempts
				   FROM realtime.publications
				  WHERE channel = 'user:realtime-worker'
				    AND event_id = '019f9460-4b36-4e9b-8f44-682611f72301'),
				(SELECT published_at IS NOT NULL
				   FROM realtime.publications
				  WHERE channel = 'user:realtime-worker'
				    AND event_id = '019f9460-4b36-4e9b-8f44-682611f72302'),
				(SELECT attempts
				   FROM realtime.publications
				  WHERE channel = 'user:realtime-worker'
				    AND event_id = '019f9460-4b36-4e9b-8f44-682611f72302')`,
		).Scan(
			&firstPublished,
			&firstAttempts,
			&secondPublished,
			&secondAttempts,
		); err != nil {
			t.Fatal(err)
		}
		if firstPublished && secondPublished {
			if firstAttempts != 4 || secondAttempts != 1 {
				t.Fatalf(
					"durable attempts = first %d second %d, want 4 and 1",
					firstAttempts,
					secondAttempts,
				)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("realtime publication was not retried and acknowledged")
		}
		time.Sleep(20 * time.Millisecond)
	}
	readyResponse, err := http.Get("http://" + healthAddress + "/readyz")
	if err != nil {
		t.Fatalf("read recovered realtime worker readiness: %v", err)
	}
	_ = readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"recovered realtime worker readiness = %d, want 200",
			readyResponse.StatusCode,
		)
	}
	if _, err := pool.Exec(
		ctx,
		"REVOKE SELECT ON realtime.publications FROM platformgo_realtime",
	); err != nil {
		t.Fatalf("revoke projector readiness privilege: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(
			context.Background(),
			"GRANT SELECT ON realtime.publications TO platformgo_realtime",
		)
	})
	unreadyResponse, err := http.Get("http://" + healthAddress + "/readyz")
	if err != nil {
		t.Fatalf("read privilege-loss readiness: %v", err)
	}
	_ = unreadyResponse.Body.Close()
	if unreadyResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf(
			"realtime worker readiness after privilege loss = %d, want 503",
			unreadyResponse.StatusCode,
		)
	}
	if _, err := pool.Exec(
		ctx,
		"GRANT SELECT ON realtime.publications TO platformgo_realtime",
	); err != nil {
		t.Fatalf("restore projector readiness privilege: %v", err)
	}
	recoveredResponse, err := http.Get("http://" + healthAddress + "/readyz")
	if err != nil {
		t.Fatalf("read restored projector readiness: %v", err)
	}
	_ = recoveredResponse.Body.Close()
	if recoveredResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"realtime worker readiness after privilege restore = %d, want 200",
			recoveredResponse.StatusCode,
		)
	}
	cancel()
	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("realtime worker shutdown: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("realtime worker did not shut down")
	}

	mu.Lock()
	got := append([]struct {
		Channel string        `json:"channel"`
		Data    edgeWireEvent `json:"data"`
	}(nil), attempts...)
	mu.Unlock()
	if len(got) != 5 {
		t.Fatalf("publish attempts = %d, want 5", len(got))
	}
	for _, attempt := range got[:4] {
		if attempt.Channel != "user:realtime-worker" ||
			attempt.Data.EventID != "019f9460-4b36-4e9b-8f44-682611f72301" ||
			attempt.Data.Sequence != 7 ||
			attempt.Data.Type != "order.updated" {
			t.Fatalf("realtime retry changed identity: %+v", attempt)
		}
	}
	if successor := got[4]; successor.Channel != "user:realtime-worker" ||
		successor.Data.EventID != "019f9460-4b36-4e9b-8f44-682611f72302" ||
		successor.Data.Sequence != 8 ||
		successor.Data.Type != "order.cancelled" {
		t.Fatalf("realtime successor bypassed or changed identity: %+v", successor)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/realtime/e2e_gateway.rs:15
//	test: realtime_gateway_json_event_publish_and_token
//
// Adaptations:
//   - The legacy Redis stream is replaced by the committed PostgreSQL
//     publication and the production realtime worker.
//   - Additive event identity and sequence fields are asserted alongside the
//     source-visible envelope.
//
// Assertions preserved:
//   - The committed JSON event reaches real Centrifugo history at offset >= 1.
//   - The wire type and canonical account URN are preserved without internal
//     execution names.
//   - Production password login succeeds and the authenticated token endpoint
//     returns one exact user channel, while anonymous access returns 401.
func TestRealtimeGatewayJSONEventPublishAndToken(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	centrifugoURL := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_URL")
	apiKey := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY")
	tokenSecret := []byte(os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_TOKEN_SECRET"))
	if databaseURL == "" || centrifugoURL == "" || len(tokenSecret) < 32 {
		t.Skip("PostgreSQL and Centrifugo dependencies are not configured")
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
	if err := migrateAndProvisionCompatibilityFixture(t, ctx, pool, migrations.Files, 24); err != nil {
		t.Fatal(err)
	}
	projectorURL := provisionRuntimeLogin(
		t,
		ctx,
		pool,
		databaseURL,
		"platformgo_realtime",
	)
	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		pool,
		databaseURL,
		"platformgo_api",
	)
	passwordHash, err := application.HashPassword(
		"correct horse battery staple",
		bytes.NewReader(bytes.Repeat([]byte{29}, 16)),
	)
	if err != nil {
		t.Fatal(err)
	}
	const (
		channel = "user:committed-realtime"
		eventID = "019f9460-4b36-4e9b-8f44-682611f72401"
	)
	if _, err := pool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES (
			'urn:xb:user:committed-realtime',
			'committed-realtime',
			'committed-realtime',
			'committed-realtime@example.com',
			'committed-realtime@example.com',
			$1
		)`,
		passwordHash,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:committed-realtime', 'NETTING');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES (
			'urn:xb:user:committed-realtime',
			'urn:xb:account:committed-realtime'
		);
		INSERT INTO realtime.channel_sequences (channel, last_sequence)
		VALUES ('user:committed-realtime', 1);
		INSERT INTO realtime.publications (
			channel, event_id, sequence, schema_version, event_type,
			account_id, logical_time, data
		) VALUES (
			'user:committed-realtime',
			'019f9460-4b36-4e9b-8f44-682611f72401',
			1,
			1,
			'order.updated',
			'urn:xb:account:committed-realtime',
			456,
			'{"symbol":"BTC-PERP","status":"working"}'
		)`,
	); err != nil {
		t.Fatal(err)
	}

	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	clientTokenSecret := []byte(
		"phase3-realtime-client-token-secret-0123456789abcdef",
	)
	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: clientTokenSecret,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{31}, 64)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	realtimeGateway, err := centrifugo.New(centrifugo.Config{
		APIURL:      centrifugoURL,
		APIKey:      apiKey,
		TokenSecret: tokenSecret,
		TokenTTL:    time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Realtime:      realtimeGateway,
	}).Handler())
	defer server.Close()

	loginResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"committed-realtime","password":"correct horse battery staple"}`,
		nil,
	)
	var login edge.LoginResponse
	decodeAndClose(t, loginResponse, &login)
	if loginResponse.StatusCode != http.StatusOK || login.AccessToken == "" {
		t.Fatalf(
			"login status=%d body=%#v",
			loginResponse.StatusCode,
			login,
		)
	}
	tokenResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/me/realtime/token",
		"",
		map[string]string{
			"authorization": "Bearer " + login.AccessToken,
		},
	)
	var realtimeToken edge.RealtimeToken
	decodeAndClose(t, tokenResponse, &realtimeToken)
	if tokenResponse.StatusCode != http.StatusOK ||
		realtimeToken.Token == "" ||
		len(realtimeToken.Channels) != 1 ||
		realtimeToken.Channels[0] != channel {
		t.Fatalf(
			"realtime token status=%d body=%#v",
			tokenResponse.StatusCode,
			realtimeToken,
		)
	}
	anonymousResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/me/realtime/token",
		"",
		nil,
	)
	if anonymousResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"anonymous realtime token status=%d, want 401",
			anonymousResponse.StatusCode,
		)
	}
	_ = anonymousResponse.Body.Close()

	baselineHistory := readCentrifugoHistory(
		t,
		ctx,
		centrifugoURL,
		apiKey,
		channel,
	)
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	healthAddress := unusedAddress(t)
	go func() {
		result <- platformruntime.RunWorkers(
			runContext,
			platformruntime.Config{
				DatabaseURL:      projectorURL,
				HealthAddress:    healthAddress,
				CentrifugoAPIURL: centrifugoURL,
				CentrifugoAPIKey: apiKey,
				ShardID:          24,
			},
			[]string{"realtime-publisher"},
		)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		var published bool
		if err := pool.QueryRow(ctx, `
			SELECT published_at IS NOT NULL
			  FROM realtime.publications
			 WHERE channel = $1 AND event_id = $2`,
			channel,
			eventID,
		).Scan(&published); err != nil {
			t.Fatal(err)
		}
		if published {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("committed realtime publication was not acknowledged")
		}
		time.Sleep(20 * time.Millisecond)
	}

	history := readCentrifugoHistory(t, ctx, centrifugoURL, apiKey, channel)
	found := false
	var foundEnvelope struct {
		Type          string `json:"type"`
		AccountID     string `json:"accountId"`
		EventID       string `json:"eventId"`
		SchemaVersion uint32 `json:"schemaVersion"`
		Sequence      uint64 `json:"sequence"`
	}
	for _, publication := range history.Result.Publications {
		if publication.Offset <= baselineHistory.Result.Offset {
			continue
		}
		var envelope struct {
			Type          string `json:"type"`
			AccountID     string `json:"accountId"`
			EventID       string `json:"eventId"`
			SchemaVersion uint32 `json:"schemaVersion"`
			Sequence      uint64 `json:"sequence"`
		}
		if err := json.Unmarshal(publication.Data, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.EventID != eventID {
			continue
		}
		foundEnvelope = envelope
		for _, internalName := range []string{
			"SHAREDNETTING",
			"SHAREDHEDGING",
			"BBOOK",
		} {
			if bytes.Contains(publication.Data, []byte(internalName)) {
				t.Fatalf(
					"Centrifugo history exposed internal name %q: %s",
					internalName,
					publication.Data,
				)
			}
		}
		found = envelope.Type == "order.updated" &&
			strings.HasPrefix(envelope.AccountID, "urn:xb:account:") &&
			envelope.AccountID == "urn:xb:account:committed-realtime" &&
			envelope.SchemaVersion == 1 &&
			envelope.Sequence == 1
	}
	if history.Result.Offset <= baselineHistory.Result.Offset ||
		len(history.Result.Publications) == 0 ||
		!found {
		t.Fatalf(
			"Centrifugo history baseline=%d offset=%d envelope=%#v publications=%#v",
			baselineHistory.Result.Offset,
			history.Result.Offset,
			foundEnvelope,
			history.Result.Publications,
		)
	}

	cancel()
	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("realtime worker shutdown: %v", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("realtime worker did not shut down")
	}
}

type centrifugoHistory struct {
	Result struct {
		Offset       uint64 `json:"offset"`
		Publications []struct {
			Data   json.RawMessage `json:"data"`
			Offset uint64          `json:"offset"`
		} `json:"publications"`
	} `json:"result"`
}

func readCentrifugoHistory(
	t *testing.T,
	ctx context.Context,
	apiURL string,
	apiKey string,
	channel string,
) centrifugoHistory {
	t.Helper()
	historyBody, err := json.Marshal(map[string]any{
		"channel": channel,
		"limit":   100,
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(apiURL, "/")+"/api/history",
		bytes.NewReader(historyBody),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-api-key", apiKey)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var history centrifugoHistory
	if err := json.NewDecoder(response.Body).Decode(&history); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"Centrifugo history status=%d body=%#v",
			response.StatusCode,
			history,
		)
	}
	return history
}

type edgeWireEvent struct {
	Type     string `json:"type"`
	EventID  string `json:"eventId"`
	Sequence uint64 `json:"sequence"`
}
