package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

type brokerEchoClock struct{ value time.Time }

func (clock brokerEchoClock) Now() time.Time { return clock.value }

type brokerEchoHTTPResult struct {
	status  int
	headers http.Header
	body    []byte
	err     error
}

type brokerEchoSnapshot struct {
	requestHash     []byte
	responseHeaders []byte
	responseBody    []byte
	status          int
	createdAt       time.Time
	expiresAt       time.Time
	scopeRows       int
}

type brokerEchoLegacyStore struct {
	application.IdentityStore
}

func (store brokerEchoLegacyStore) BrokerEcho(
	ctx context.Context,
	principal string,
	idempotencyHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
	response edge.StoredResponse,
) (edge.StoredResponse, error) {
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body, &payload); err != nil {
		return edge.StoredResponse{}, fmt.Errorf(
			"legacy broker echo: decode candidate: %w",
			err,
		)
	}
	body, err := json.Marshal(struct {
		ID          string `json:"id"`
		WireVersion string `json:"wireVersion"`
	}{
		ID:          payload.ID,
		WireVersion: "legacy-v1",
	})
	if err != nil {
		return edge.StoredResponse{}, fmt.Errorf(
			"legacy broker echo: encode response: %w",
			err,
		)
	}
	response.Headers = []byte(
		`{"Content-Type":["application/json"],` +
			`"X-Echo-Wire-Version":["legacy-v1"]}`,
	)
	response.Body = append(body, '\n')
	return store.IdentityStore.BrokerEcho(
		ctx,
		principal,
		idempotencyHash,
		requestHash,
		response,
	)
}

type brokerEchoBarrierStore struct {
	application.IdentityStore
	ready   chan<- struct{}
	release <-chan struct{}
}

func (store brokerEchoBarrierStore) BrokerEcho(
	ctx context.Context,
	principal string,
	idempotencyHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
	response edge.StoredResponse,
) (edge.StoredResponse, error) {
	select {
	case store.ready <- struct{}{}:
	case <-ctx.Done():
		return edge.StoredResponse{}, ctx.Err()
	}
	select {
	case <-store.release:
	case <-ctx.Done():
		return edge.StoredResponse{}, ctx.Err()
	}
	return store.IdentityStore.BrokerEcho(
		ctx,
		principal,
		idempotencyHash,
		requestHash,
		response,
	)
}

type brokerEchoAfterCommitProbe struct {
	edge.IdentityService
	committed chan<- edge.StoredResponse
	release   <-chan struct{}
	first     atomic.Bool
}

func (probe *brokerEchoAfterCommitProbe) BrokerEcho(
	ctx context.Context,
	principal edge.Principal,
	idempotencyKey string,
) (edge.StoredResponse, error) {
	response, err := probe.IdentityService.BrokerEcho(
		ctx,
		principal,
		idempotencyKey,
	)
	if err == nil && probe.first.CompareAndSwap(false, true) {
		probe.committed <- response
		<-probe.release
	}
	return response, err
}

type brokerEchoStoreWrapper func(
	application.IdentityStore,
) application.IdentityStore

type brokerEchoIdentityWrapper func(edge.IdentityService) edge.IdentityService

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:26
//	test: api_key_auth_plus_idempotency_replay
//
// Adaptations:
//   - The source-created broker key is represented by the production configured
//     HMAC credential used at the Go bootstrap boundary. The source does not
//     assert the key-creation response or storage; creation only supplies the
//     valid credential used by the asserted ping and echo requests.
//
// Assertions preserved:
//   - A valid broker key can ping, while missing and invalid keys are rejected.
//   - A repeated idempotency key replays the same echo response.
//   - Every request without an idempotency key executes with a fresh response.
//
// Invariant strengthening:
//   - Status, required headers, and exact body bytes survive replay, deployment
//     renderer change, component reconstruction, concurrent duplicate delivery,
//     and an HTTP outcome lost after the PostgreSQL commit.
func TestAPIKeyAuthPlusIdempotencyReplay(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(httpClient.CloseIdleConnections)
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
	).MigrateAndProvision(ctx, 73); err != nil {
		t.Fatal(err)
	}

	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	var databaseNow time.Time
	if err := admin.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(
		&databaseNow,
	); err != nil {
		t.Fatal(err)
	}
	// Broker echo expiry is PostgreSQL-owned. A deliberately stale application
	// clock must not create an already-expired replay generation.
	now := databaseNow.Add(-365 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	var generatedKeySequence atomic.Uint64
	requestID := func() string {
		return fmt.Sprintf(
			"generated-echo-%d",
			generatedKeySequence.Add(1),
		)
	}
	server := newBrokerEchoServer(
		t,
		apiPool,
		now,
		requestID,
		func(store application.IdentityStore) application.IdentityStore {
			return brokerEchoLegacyStore{IdentityStore: store}
		},
		nil,
	)
	const (
		validToken     = "xbk_echo.echo-secret"
		invalidToken   = "xbk_deadbeef.notreal"
		principalScope = "broker-echo\x1furn:xb:apikey:echo"
		keyedRequest   = "k1"
	)

	assertBrokerEchoStatus(
		t,
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodGet,
			validToken,
			"",
			"ping-valid",
		),
		http.StatusOK,
	)
	assertBrokerEchoStatus(
		t,
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodGet,
			"",
			"",
			"ping-anonymous",
		),
		http.StatusUnauthorized,
	)
	assertBrokerEchoStatus(
		t,
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodGet,
			invalidToken,
			"",
			"ping-invalid",
		),
		http.StatusUnauthorized,
	)

	first := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		validToken,
		keyedRequest,
		"attempt-first",
	)
	firstID := requireBrokerEchoID(t, first)
	if first.headers.Get("x-request-id") != "attempt-first" ||
		first.headers.Get("x-echo-wire-version") != "legacy-v1" {
		t.Fatalf("first echo headers = %v", first.headers)
	}
	firstSnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		keyedRequest,
	)
	assertBrokerEchoSnapshot(t, firstSnapshot, first, 1)
	if firstSnapshot.createdAt.Before(databaseNow) {
		t.Fatalf(
			"broker echo used stale application time: created=%s database_before=%s",
			firstSnapshot.createdAt,
			databaseNow,
		)
	}

	replayed := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		validToken,
		keyedRequest,
		"attempt-replay",
	)
	if replayedID := requireBrokerEchoID(t, replayed); replayedID != firstID {
		t.Fatalf(
			"same idempotency key did not replay: first=%q replay=%q",
			firstID,
			replayedID,
		)
	}
	assertBrokerEchoStoredWireEqual(t, first, replayed)
	if replayed.headers.Get("x-request-id") != "attempt-replay" {
		t.Fatalf(
			"per-attempt request ID was frozen: %q",
			replayed.headers.Get("x-request-id"),
		)
	}
	replayedSnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		keyedRequest,
	)
	if !firstSnapshot.equal(replayedSnapshot) {
		t.Fatalf(
			"replay changed durable response:\nfirst=%#v\nreplay=%#v",
			firstSnapshot,
			replayedSnapshot,
		)
	}

	keylessOne := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		validToken,
		"",
		"keyless-one",
	)
	keylessOneID := requireBrokerEchoID(t, keylessOne)
	keylessTwo := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		validToken,
		"",
		"keyless-two",
	)
	keylessTwoID := requireBrokerEchoID(t, keylessTwo)
	if keylessOneID == firstID ||
		keylessTwoID == firstID ||
		keylessTwoID == keylessOneID {
		t.Fatalf(
			"keyless responses were not fresh: keyed=%q first=%q second=%q",
			firstID,
			keylessOneID,
			keylessTwoID,
		)
	}
	if rows := brokerEchoScopeRows(t, ctx, admin, principalScope); rows != 3 {
		t.Fatalf("rows after two keyless executions = %d, want 3", rows)
	}

	server.Close()
	apiPool.Close()
	restartedAPIPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedAPIPool.Close()
	restartedServer := newBrokerEchoServer(
		t,
		restartedAPIPool,
		now,
		requestID,
		nil,
		nil,
	)
	restarted := doBrokerEchoHTTP(
		ctx,
		httpClient,
		restartedServer.URL,
		http.MethodPost,
		validToken,
		keyedRequest,
		"attempt-new-renderer",
	)
	if restartedID := requireBrokerEchoID(t, restarted); restartedID != firstID {
		t.Fatalf(
			"reconstructed API replay = %q, want %q",
			restartedID,
			firstID,
		)
	}
	assertBrokerEchoStoredWireEqual(t, first, restarted)
	if restarted.headers.Get("x-echo-wire-version") != "legacy-v1" {
		t.Fatalf(
			"new renderer replaced stored headers: %v",
			restarted.headers,
		)
	}
	restartedSnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		keyedRequest,
	)
	if !firstSnapshot.sameResponse(restartedSnapshot) ||
		restartedSnapshot.scopeRows != 3 {
		t.Fatalf(
			"API reconstruction changed durable response:\nfirst=%#v\nrestart=%#v",
			firstSnapshot,
			restartedSnapshot,
		)
	}

	runBrokerEchoConcurrentDuplicate(
		t,
		ctx,
		httpClient,
		admin,
		apiDatabaseURL,
		now,
		requestID,
		principalScope,
	)
	runBrokerEchoUnknownOutcome(
		t,
		ctx,
		httpClient,
		admin,
		apiDatabaseURL,
		now,
		requestID,
		principalScope,
	)
	runBrokerEchoEntropyFailure(
		t,
		ctx,
		httpClient,
		admin,
		apiDatabaseURL,
		now,
		principalScope,
	)
}

func runBrokerEchoConcurrentDuplicate(
	t *testing.T,
	ctx context.Context,
	httpClient *http.Client,
	admin *pgxpool.Pool,
	apiDatabaseURL string,
	now time.Time,
	requestID func() string,
	principalScope string,
) {
	t.Helper()
	poolOne, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer poolOne.Close()
	poolTwo, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer poolTwo.Close()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	wrapStore := func(store application.IdentityStore) application.IdentityStore {
		return brokerEchoBarrierStore{
			IdentityStore: store,
			ready:         ready,
			release:       release,
		}
	}
	serverOne := newBrokerEchoServer(
		t, poolOne, now, requestID, wrapStore, nil,
	)
	serverTwo := newBrokerEchoServer(
		t, poolTwo, now, requestID, wrapStore, nil,
	)
	start := make(chan struct{})
	results := make(chan brokerEchoHTTPResult, 2)
	for index, serverURL := range []string{serverOne.URL, serverTwo.URL} {
		index := index
		serverURL := serverURL
		go func() {
			<-start
			results <- doBrokerEchoHTTP(
				ctx,
				httpClient,
				serverURL,
				http.MethodPost,
				"xbk_echo.echo-secret",
				"concurrent-key",
				fmt.Sprintf("concurrent-%d", index),
			)
		}()
	}
	close(start)
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal("concurrent echo requests did not reach the claim barrier")
		}
	}
	close(release)
	first := <-results
	second := <-results
	if requireBrokerEchoID(t, first) != requireBrokerEchoID(t, second) {
		t.Fatalf(
			"concurrent duplicates diverged: first=%s second=%s",
			first.body,
			second.body,
		)
	}
	assertBrokerEchoStoredWireEqual(t, first, second)
	snapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		"concurrent-key",
	)
	assertBrokerEchoSnapshot(t, snapshot, first, 4)
}

func runBrokerEchoUnknownOutcome(
	t *testing.T,
	ctx context.Context,
	httpClient *http.Client,
	admin *pgxpool.Pool,
	apiDatabaseURL string,
	now time.Time,
	requestID func() string,
	principalScope string,
) {
	t.Helper()
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	committed := make(chan edge.StoredResponse, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	probe := &brokerEchoAfterCommitProbe{
		committed: committed,
		release:   release,
	}
	server := newBrokerEchoServer(
		t,
		apiPool,
		now,
		requestID,
		nil,
		func(identity edge.IdentityService) edge.IdentityService {
			probe.IdentityService = identity
			return probe
		},
	)
	attemptContext, cancelAttempt := context.WithCancel(ctx)
	result := make(chan brokerEchoHTTPResult, 1)
	go func() {
		result <- doBrokerEchoHTTP(
			attemptContext,
			httpClient,
			server.URL,
			http.MethodPost,
			"xbk_echo.echo-secret",
			"unknown-outcome-key",
			"unknown-first",
		)
	}()
	var committedResponse edge.StoredResponse
	select {
	case committedResponse = <-committed:
	case <-ctx.Done():
		t.Fatal("unknown-outcome request did not commit")
	}
	cancelAttempt()
	releaseOnce.Do(func() { close(release) })
	firstAttempt := <-result
	if firstAttempt.err == nil {
		t.Fatalf(
			"first unknown-outcome attempt unexpectedly acknowledged: %#v",
			firstAttempt,
		)
	}
	committedSnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		"unknown-outcome-key",
	)
	if !bytes.Equal(committedSnapshot.responseBody, committedResponse.Body) ||
		committedSnapshot.status != committedResponse.Status {
		t.Fatalf(
			"unknown-outcome durable response=%#v committed=%#v",
			committedSnapshot,
			committedResponse,
		)
	}

	server.Close()
	apiPool.Close()
	restartedPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedPool.Close()
	restartedServer := newBrokerEchoServer(
		t,
		restartedPool,
		now,
		requestID,
		nil,
		nil,
	)
	retry := doBrokerEchoHTTP(
		ctx,
		httpClient,
		restartedServer.URL,
		http.MethodPost,
		"xbk_echo.echo-secret",
		"unknown-outcome-key",
		"unknown-retry",
	)
	requireBrokerEchoID(t, retry)
	assertStoredResponseMatchesHTTP(t, committedResponse, retry)
	retrySnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		principalScope,
		"unknown-outcome-key",
	)
	if !committedSnapshot.equal(retrySnapshot) ||
		retrySnapshot.scopeRows != 5 {
		t.Fatalf(
			"unknown-outcome retry changed durable response:\ncommit=%#v\nretry=%#v",
			committedSnapshot,
			retrySnapshot,
		)
	}
}

func runBrokerEchoEntropyFailure(
	t *testing.T,
	ctx context.Context,
	httpClient *http.Client,
	admin *pgxpool.Pool,
	apiDatabaseURL string,
	now time.Time,
	principalScope string,
) {
	t.Helper()
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	rowsBefore := brokerEchoScopeRows(t, ctx, admin, principalScope)
	failedServer := newBrokerEchoServer(
		t,
		apiPool,
		now,
		func() string { return "" },
		nil,
		nil,
	)
	for attempt := 1; attempt <= 2; attempt++ {
		result := doBrokerEchoHTTP(
			ctx,
			httpClient,
			failedServer.URL,
			http.MethodPost,
			"xbk_echo.echo-secret",
			"",
			fmt.Sprintf("entropy-failure-%d", attempt),
		)
		assertBrokerEchoStatus(t, result, http.StatusServiceUnavailable)
	}
	if rows := brokerEchoScopeRows(t, ctx, admin, principalScope); rows != rowsBefore {
		t.Fatalf(
			"entropy failure persisted broker echo rows: before=%d after=%d",
			rowsBefore,
			rows,
		)
	}
	failedServer.Close()

	var recoveredSequence atomic.Uint64
	recoveredServer := newBrokerEchoServer(
		t,
		apiPool,
		now,
		func() string {
			return fmt.Sprintf(
				"entropy-recovered-%d",
				recoveredSequence.Add(1),
			)
		},
		nil,
		nil,
	)
	first := doBrokerEchoHTTP(
		ctx,
		httpClient,
		recoveredServer.URL,
		http.MethodPost,
		"xbk_echo.echo-secret",
		"",
		"entropy-recovered-first",
	)
	second := doBrokerEchoHTTP(
		ctx,
		httpClient,
		recoveredServer.URL,
		http.MethodPost,
		"xbk_echo.echo-secret",
		"",
		"entropy-recovered-second",
	)
	if firstID, secondID := requireBrokerEchoID(t, first),
		requireBrokerEchoID(t, second); firstID == secondID {
		t.Fatalf("recovered keyless echoes collapsed to %q", firstID)
	}
	if rows := brokerEchoScopeRows(t, ctx, admin, principalScope); rows != rowsBefore+2 {
		t.Fatalf(
			"recovered keyless rows = %d, want %d",
			rows,
			rowsBefore+2,
		)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:93
//	test: idempotency_key_is_scoped_per_principal
//
// Adaptations:
//   - Source-created broker keys are represented by two production configured
//     HMAC credentials with distinct subjects and the source's shared tenant.
//   - The Rust runtime is replaced by the production Go HTTP edge, identity
//     application service, least-privilege API role, and real PostgreSQL.
//
// Assertions preserved:
//   - Two principals may use the same idempotency key without sharing a cached
//     response.
//   - The first principal reusing that key receives its own original response.
//
// Invariant strengthening:
//   - Both principal scopes own exactly one durable PostgreSQL row.
//   - The first principal's replay preserves exact status, logical headers,
//     body bytes, creation time, and expiry.
func TestIdempotencyKeyIsScopedPerPrincipal(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(httpClient.CloseIdleConnections)
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
	).MigrateAndProvision(ctx, 73); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	var databaseNow time.Time
	if err := admin.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(
		&databaseNow,
	); err != nil {
		t.Fatal(err)
	}
	var requestSequence atomic.Uint64
	server := newBrokerEchoServerWithCredentials(
		t,
		apiPool,
		databaseNow.UTC().Truncate(time.Microsecond),
		func() string {
			return fmt.Sprintf(
				"principal-scope-%d",
				requestSequence.Add(1),
			)
		},
		[]edge.BrokerCredential{
			{
				Prefix:     "xbk_echo_a",
				SecretHash: edge.HashBrokerSecret("echo-secret-a"),
				Subject:    "urn:xb:apikey:echo-a",
				Tenant:     "urn:xb:tenant:echo-shared",
				Scopes:     []string{"*"},
			},
			{
				Prefix:     "xbk_echo_b",
				SecretHash: edge.HashBrokerSecret("echo-secret-b"),
				Subject:    "urn:xb:apikey:echo-b",
				Tenant:     "urn:xb:tenant:echo-shared",
				Scopes:     []string{"*"},
			},
		},
		nil,
		nil,
	)
	const (
		sharedKey = "shared-idem-key"
		tokenA    = "xbk_echo_a.echo-secret-a"
		tokenB    = "xbk_echo_b.echo-secret-b"
		scopeA    = "broker-echo\x1furn:xb:apikey:echo-a"
		scopeB    = "broker-echo\x1furn:xb:apikey:echo-b"
	)

	firstA := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		tokenA,
		sharedKey,
		"principal-a-first",
	)
	firstAID := requireBrokerEchoID(t, firstA)
	firstASnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		scopeA,
		sharedKey,
	)
	assertBrokerEchoSnapshot(t, firstASnapshot, firstA, 1)

	firstB := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		tokenB,
		sharedKey,
		"principal-b-first",
	)
	firstBID := requireBrokerEchoID(t, firstB)
	if firstAID == firstBID {
		t.Fatalf(
			"principals shared an idempotent response: A=%q B=%q",
			firstAID,
			firstBID,
		)
	}
	firstBSnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		scopeB,
		sharedKey,
	)
	assertBrokerEchoSnapshot(t, firstBSnapshot, firstB, 1)

	replayedA := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		tokenA,
		sharedKey,
		"principal-a-replay",
	)
	if replayedAID := requireBrokerEchoID(t, replayedA); replayedAID != firstAID {
		t.Fatalf(
			"principal did not replay its response: first=%q replay=%q",
			firstAID,
			replayedAID,
		)
	}
	assertBrokerEchoStoredWireEqual(t, firstA, replayedA)
	replayedASnapshot := readBrokerEchoSnapshot(
		t,
		ctx,
		admin,
		scopeA,
		sharedKey,
	)
	if !firstASnapshot.equal(replayedASnapshot) {
		t.Fatalf(
			"principal replay changed durable response:\nfirst=%#v\nreplay=%#v",
			firstASnapshot,
			replayedASnapshot,
		)
	}
	if rowsA, rowsB := brokerEchoScopeRows(t, ctx, admin, scopeA),
		brokerEchoScopeRows(t, ctx, admin, scopeB); rowsA != 1 || rowsB != 1 {
		t.Fatalf("principal scope rows A=%d B=%d, want 1 each", rowsA, rowsB)
	}
}

func TestBrokerEchoCapacityReturnsTyped429WithoutLosingReplay(
	t *testing.T,
) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	t.Cleanup(httpClient.CloseIdleConnections)
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
	).MigrateAndProvision(ctx, 74); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var generated atomic.Uint64
	server := newBrokerEchoServer(
		t,
		apiPool,
		time.Date(2026, time.July, 27, 8, 0, 0, 0, time.UTC),
		func() string {
			return fmt.Sprintf("capacity-generated-%d", generated.Add(1))
		},
		nil,
		nil,
	)
	const token = "xbk_echo.echo-secret"
	var first brokerEchoHTTPResult
	for index := range 100 {
		response := doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodPost,
			token,
			fmt.Sprintf("capacity-key-%d", index),
			fmt.Sprintf("capacity-request-%d", index),
		)
		assertBrokerEchoStatus(t, response, http.StatusOK)
		if index == 0 {
			first = response
		}
	}
	replay := doBrokerEchoHTTP(
		ctx,
		httpClient,
		server.URL,
		http.MethodPost,
		token,
		"capacity-key-0",
		"capacity-replay",
	)
	assertBrokerEchoStoredWireEqual(t, first, replay)

	assertLimited := func(name string, response brokerEchoHTTPResult) {
		t.Helper()
		assertBrokerEchoStatus(t, response, http.StatusTooManyRequests)
		retryAfter, parseErr := strconv.ParseUint(
			response.headers.Get("retry-after"),
			10,
			64,
		)
		if parseErr != nil || retryAfter == 0 {
			t.Fatalf(
				"%s Retry-After = %q, error = %v",
				name,
				response.headers.Get("retry-after"),
				parseErr,
			)
		}
		var body struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(response.body, &body); err != nil {
			t.Fatalf("%s decode error: %v", name, err)
		}
		if body.Code != "too_many_requests" ||
			body.Message != "rate_limited" {
			t.Fatalf("%s body = %#v", name, body)
		}
	}
	assertLimited(
		"explicit",
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodPost,
			token,
			"capacity-key-100",
			"capacity-limited-explicit",
		),
	)
	assertLimited(
		"keyless",
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			server.URL,
			http.MethodPost,
			token,
			"",
			"capacity-limited-keyless",
		),
	)
	const scope = "broker-echo\x1furn:xb:apikey:echo"
	if rows := brokerEchoScopeRows(t, ctx, admin, scope); rows != 100 {
		t.Fatalf("capacity rejections left %d rows, want 100", rows)
	}

	apiPool.Close()
	reopenedDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	reopenedPool, err := pgxpool.New(ctx, reopenedDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer reopenedPool.Close()
	reopenedServer := newBrokerEchoServer(
		t,
		reopenedPool,
		time.Date(2026, time.July, 27, 9, 0, 0, 0, time.UTC),
		func() string {
			return fmt.Sprintf("capacity-reopened-%d", generated.Add(1))
		},
		nil,
		nil,
	)
	reopenedReplay := doBrokerEchoHTTP(
		ctx,
		httpClient,
		reopenedServer.URL,
		http.MethodPost,
		token,
		"capacity-key-0",
		"capacity-reopened-replay",
	)
	assertBrokerEchoStoredWireEqual(t, first, reopenedReplay)
	assertLimited(
		"reconstructed",
		doBrokerEchoHTTP(
			ctx,
			httpClient,
			reopenedServer.URL,
			http.MethodPost,
			token,
			"capacity-key-reconstructed",
			"capacity-reconstructed-limited",
		),
	)
	if rows := brokerEchoScopeRows(t, ctx, admin, scope); rows != 100 {
		t.Fatalf("reconstructed capacity left %d rows, want 100", rows)
	}
}

func newBrokerEchoServer(
	t *testing.T,
	apiPool *pgxpool.Pool,
	now time.Time,
	requestID func() string,
	wrapStore brokerEchoStoreWrapper,
	wrapIdentity brokerEchoIdentityWrapper,
) *httptest.Server {
	t.Helper()
	return newBrokerEchoServerWithCredentials(
		t,
		apiPool,
		now,
		requestID,
		[]edge.BrokerCredential{{
			Prefix:     "xbk_echo",
			SecretHash: edge.HashBrokerSecret("echo-secret"),
			Subject:    "urn:xb:apikey:echo",
			Tenant:     "urn:xb:tenant:echo",
			Scopes:     []string{"accounts:read"},
		}},
		wrapStore,
		wrapIdentity,
	)
}

func newBrokerEchoServerWithCredentials(
	t *testing.T,
	apiPool *pgxpool.Pool,
	now time.Time,
	requestID func() string,
	credentials []edge.BrokerCredential,
	wrapStore brokerEchoStoreWrapper,
	wrapIdentity brokerEchoIdentityWrapper,
) *httptest.Server {
	t.Helper()
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-broker-echo-client-secret!"),
		BrokerCredentials: credentials,
		Clock:             brokerEchoClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	var store application.IdentityStore = platformpostgres.NewCompatibilityStore(
		apiPool,
	)
	if wrapStore != nil {
		store = wrapStore(store)
	}
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Clock:   brokerEchoClock{value: now},
			Entropy: bytes.NewReader(bytes.Repeat([]byte{73}, 128)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var identityService edge.IdentityService = identity
	if wrapIdentity != nil {
		identityService = wrapIdentity(identityService)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identityService,
		RequestID:     requestID,
	}).Handler())
	t.Cleanup(server.Close)
	return server
}

func doBrokerEchoHTTP(
	ctx context.Context,
	client *http.Client,
	serverURL string,
	method string,
	token string,
	idempotencyKey string,
	requestID string,
) brokerEchoHTTPResult {
	path := "/broker/v1/ping"
	if method == http.MethodPost {
		path = "/broker/v1/echo"
	}
	request, err := http.NewRequestWithContext(ctx, method, serverURL+path, nil)
	if err != nil {
		return brokerEchoHTTPResult{err: err}
	}
	if token != "" {
		request.Header.Set("x-api-key", token)
	}
	if idempotencyKey != "" {
		request.Header.Set("idempotency-key", idempotencyKey)
	}
	if requestID != "" {
		request.Header.Set("x-request-id", requestID)
	}
	response, err := client.Do(request)
	if err != nil {
		return brokerEchoHTTPResult{err: err}
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return brokerEchoHTTPResult{
		status:  response.StatusCode,
		headers: response.Header.Clone(),
		body:    body,
		err:     errors.Join(readErr, closeErr),
	}
}

func assertBrokerEchoStatus(
	t *testing.T,
	result brokerEchoHTTPResult,
	status int,
) {
	t.Helper()
	if result.err != nil ||
		result.status != status {
		t.Fatalf(
			"broker response status=%d error=%v body=%s, want %d",
			result.status,
			result.err,
			result.body,
			status,
		)
	}
}

func requireBrokerEchoID(
	t *testing.T,
	result brokerEchoHTTPResult,
) string {
	t.Helper()
	assertBrokerEchoStatus(t, result, http.StatusOK)
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(result.body, &response); err != nil {
		t.Fatalf("decode echo response %q: %v", result.body, err)
	}
	if response.ID == "" {
		t.Fatalf("echo response has empty ID: %s", result.body)
	}
	return response.ID
}

func readBrokerEchoSnapshot(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	scope string,
	idempotencyKey string,
) brokerEchoSnapshot {
	t.Helper()
	idempotencyHash := sha256.Sum256([]byte(idempotencyKey))
	var snapshot brokerEchoSnapshot
	if err := admin.QueryRow(ctx, `
		SELECT
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at,
			(
				SELECT count(*)
				  FROM identity.broker_echo_replays
				 WHERE scope = $1
			)
		  FROM identity.broker_echo_replays
		 WHERE scope = $1
		   AND idempotency_key_hash = $2`,
		scope,
		idempotencyHash[:],
	).Scan(
		&snapshot.requestHash,
		&snapshot.status,
		&snapshot.responseHeaders,
		&snapshot.responseBody,
		&snapshot.createdAt,
		&snapshot.expiresAt,
		&snapshot.scopeRows,
	); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func brokerEchoScopeRows(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	scope string,
) int {
	t.Helper()
	var rows int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.broker_echo_replays
		 WHERE scope = $1`,
		scope,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertBrokerEchoSnapshot(
	t *testing.T,
	snapshot brokerEchoSnapshot,
	response brokerEchoHTTPResult,
	scopeRows int,
) {
	t.Helper()
	wantRequestHash := sha256.Sum256([]byte("{}"))
	if !bytes.Equal(snapshot.requestHash, wantRequestHash[:]) ||
		snapshot.status != response.status ||
		!bytes.Equal(snapshot.responseBody, response.body) ||
		snapshot.createdAt.IsZero() ||
		snapshot.expiresAt.Sub(snapshot.createdAt) != 24*time.Hour ||
		snapshot.scopeRows != scopeRows {
		t.Fatalf(
			"durable echo snapshot=%#v response=%#v rows=%d",
			snapshot,
			response,
			scopeRows,
		)
	}
	var headers map[string][]string
	if err := json.Unmarshal(snapshot.responseHeaders, &headers); err != nil {
		t.Fatalf("decode durable headers: %v", err)
	}
	if len(headers["Content-Type"]) != 1 ||
		headers["Content-Type"][0] != response.headers.Get("content-type") {
		t.Fatalf(
			"durable headers=%v HTTP headers=%v",
			headers,
			response.headers,
		)
	}
	if values := headers["X-Echo-Wire-Version"]; len(values) > 0 &&
		values[0] != response.headers.Get("x-echo-wire-version") {
		t.Fatalf(
			"durable wire header=%v HTTP headers=%v",
			headers,
			response.headers,
		)
	}
}

func assertBrokerEchoStoredWireEqual(
	t *testing.T,
	first brokerEchoHTTPResult,
	replay brokerEchoHTTPResult,
) {
	t.Helper()
	if first.err != nil ||
		replay.err != nil ||
		first.status != replay.status ||
		!bytes.Equal(first.body, replay.body) ||
		first.headers.Get("content-type") !=
			replay.headers.Get("content-type") ||
		first.headers.Get("x-echo-wire-version") !=
			replay.headers.Get("x-echo-wire-version") {
		t.Fatalf(
			"stored wire replay differs:\nfirst=%#v\nreplay=%#v",
			first,
			replay,
		)
	}
}

func assertStoredResponseMatchesHTTP(
	t *testing.T,
	stored edge.StoredResponse,
	response brokerEchoHTTPResult,
) {
	t.Helper()
	var headers map[string][]string
	if err := json.Unmarshal(stored.Headers, &headers); err != nil {
		t.Fatal(err)
	}
	if stored.Status != response.status ||
		!bytes.Equal(stored.Body, response.body) ||
		len(headers["Content-Type"]) != 1 ||
		headers["Content-Type"][0] != response.headers.Get("content-type") {
		t.Fatalf(
			"stored response=%#v HTTP response=%#v",
			stored,
			response,
		)
	}
}

func (snapshot brokerEchoSnapshot) equal(other brokerEchoSnapshot) bool {
	return snapshot.sameResponse(other) &&
		snapshot.scopeRows == other.scopeRows
}

func (snapshot brokerEchoSnapshot) sameResponse(
	other brokerEchoSnapshot,
) bool {
	return bytes.Equal(snapshot.requestHash, other.requestHash) &&
		bytes.Equal(snapshot.responseHeaders, other.responseHeaders) &&
		bytes.Equal(snapshot.responseBody, other.responseBody) &&
		snapshot.status == other.status &&
		snapshot.createdAt.Equal(other.createdAt) &&
		snapshot.expiresAt.Equal(other.expiresAt)
}
