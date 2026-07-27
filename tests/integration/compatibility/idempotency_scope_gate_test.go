package compatibility_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/migrations"
)

type scopeGateClock struct{ value time.Time }

func (clock scopeGateClock) Now() time.Time { return clock.value }

type scopeGateWriterResult struct {
	status  int
	headers http.Header
	body    []byte
	err     error
}

type scopeGateIdentityProbe struct {
	edge.IdentityService
	observerPool             *pgxpool.Pool
	createBrokerAccountCalls atomic.Uint64
	returnedBeforeCommit     atomic.Bool
}

func (probe *scopeGateIdentityProbe) CreateBrokerAccount(
	ctx context.Context,
	principal edge.Principal,
	idempotencyKey string,
	request edge.BrokerAccountRequest,
) (edge.BrokerAccountAdmission, error) {
	probe.createBrokerAccountCalls.Add(1)
	admission, err := probe.IdentityService.CreateBrokerAccount(
		ctx,
		principal,
		idempotencyKey,
		request,
	)
	if err == nil {
		var committed bool
		queryErr := probe.observerPool.QueryRow(ctx, `
			SELECT
				command.status = 'accepted'
				AND idempotency.state = 'completed'
				AND EXISTS (
					SELECT 1
					  FROM trading.accounts AS trading_account
					  JOIN identity.user_accounts AS ownership
					    ON ownership.account_id = trading_account.account_id
					   AND ownership.user_id = $4
					   AND ownership.broker_subject = $5
					  JOIN identity.account_profiles AS profile
					    ON profile.account_id = trading_account.account_id
					 WHERE trading_account.account_id = $3
				)
			  FROM trading.idempotency_records AS idempotency
			  JOIN trading.commands AS command
			    ON command.command_id = idempotency.command_id
			 WHERE idempotency.scope = $1
			   AND idempotency.idempotency_key = $2`,
			"broker-account\x1f"+principal.Subject,
			idempotencyKey,
			admission.ID,
			admission.UserID,
			principal.Tenant,
		).Scan(&committed)
		if queryErr != nil || !committed {
			probe.returnedBeforeCommit.Store(true)
		}
	}
	return admission, err
}

type scopeGateBodyProbe struct {
	handler   http.Handler
	bodyReads atomic.Uint64
}

func (probe *scopeGateBodyProbe) ServeHTTP(
	writer http.ResponseWriter,
	request *http.Request,
) {
	request.Body = &scopeGateReadCloser{
		ReadCloser: request.Body,
		bodyReads:  &probe.bodyReads,
	}
	probe.handler.ServeHTTP(writer, request)
}

type scopeGateReadCloser struct {
	io.ReadCloser
	bodyReads *atomic.Uint64
}

func (body *scopeGateReadCloser) Read(buffer []byte) (int, error) {
	body.bodyReads.Add(1)
	return body.ReadCloser.Read(buffer)
}

// Ported from:
//
//	platform: 50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:204
//	test: idempotency_replay_does_not_bypass_scope_gate
//
// Adaptations:
//   - Source-created broker keys are represented by the production configured
//     HMAC credentials used at the Go bootstrap boundary.
//   - The real HTTP, API-role, engine-role, and PostgreSQL paths prove that the
//     writer response is durable before the reader attempts the same key.
//
// Assertions preserved:
//   - An accounts:write broker credential creates and durably caches an account.
//   - An accounts:read credential cannot replay that response with the same
//     request and idempotency key because authorization precedes idempotency.
func TestIdempotencyReplayDoesNotBypassScopeGate(t *testing.T) {
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
	).MigrateAndProvision(ctx, 71); err != nil {
		t.Fatal(err)
	}

	const (
		brokerTenant = "urn:xb:tenant:scope-gate"
		userID       = "urn:xb:user:scope-gate"
		writerScope  = "broker-account\x1furn:xb:apikey:scope-gate-writer"
		idempotency  = "replay-bypass-probe"
	)
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			broker_subject
		) VALUES (
			$1, 'scope-gate', 'scope-gate',
			'scope-gate@example.com', 'scope-gate@example.com', $2
		)`,
		userID,
		brokerTenant,
	); err != nil {
		t.Fatal(err)
	}

	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	engineDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_engine",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	enginePool, err := pgxpool.New(ctx, engineDatabaseURL)
	if err != nil {
		apiPool.Close()
		t.Fatal(err)
	}
	defer enginePool.Close()

	now := time.Date(2026, time.July, 27, 18, 0, 0, 123456789, time.UTC)
	server, identityProbe, bodyProbe := newScopeGateServer(
		t,
		apiPool,
		now,
	)
	requestBody := `{"userId":"` + userID + `"}`
	writerDone := make(chan scopeGateWriterResult, 1)
	go func() {
		request, requestErr := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			server.URL+"/broker/v1/accounts",
			bytes.NewBufferString(requestBody),
		)
		if requestErr != nil {
			writerDone <- scopeGateWriterResult{err: requestErr}
			return
		}
		request.Header.Set("content-type", "application/json")
		request.Header.Set("x-api-key", "xbk_scope_writer.writer-secret")
		request.Header.Set("idempotency-key", idempotency)
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			writerDone <- scopeGateWriterResult{err: requestErr}
			return
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		writerDone <- scopeGateWriterResult{
			status:  response.StatusCode,
			headers: response.Header.Clone(),
			body:    body,
			err:     errors.Join(readErr, closeErr),
		}
	}()

	payload := waitForScopeGateCommand(
		t,
		ctx,
		admin,
		writerScope,
		idempotency,
		writerDone,
	)
	input, action, err := engine.DecodeInputMessage(payload)
	if err != nil {
		t.Fatal(err)
	}
	input.StreamSequence = 1
	engineStore := platformpostgres.NewEngineStore(enginePool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, 71)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := ownership.Close(context.Background()); closeErr != nil {
			t.Errorf("close engine ownership: %v", closeErr)
		}
	}()
	state, err := engineStore.RecoverTradingState(ctx, 71)
	if err != nil {
		t.Fatal(err)
	}
	_, decision, duplicate, err := engineStore.ApplyTrading(
		ctx,
		state,
		input,
		action,
		platformpostgres.ApplyOptions{Ownership: ownership},
	)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate || decision.CommandResult.Status != engine.CommandStatusAccepted {
		t.Fatalf(
			"writer engine result duplicate=%t status=%q",
			duplicate,
			decision.CommandResult.Status,
		)
	}
	writer := <-writerDone
	if writer.err != nil {
		t.Fatal(writer.err)
	}
	if writer.status != http.StatusCreated ||
		writer.headers.Get("content-type") != "application/json" {
		t.Fatalf(
			"writer response status=%d headers=%v body=%s",
			writer.status,
			writer.headers,
			writer.body,
		)
	}
	var account edge.BrokerAccountResult
	if err := json.Unmarshal(writer.body, &account); err != nil {
		t.Fatal(err)
	}
	if account.ID == "" || account.UserID != userID {
		t.Fatalf("writer account = %#v", account)
	}
	if got := identityProbe.createBrokerAccountCalls.Load(); got != 1 {
		t.Fatalf("writer application calls = %d, want 1", got)
	}
	if identityProbe.returnedBeforeCommit.Load() {
		t.Fatal("writer application returned before the engine commit completed")
	}
	writerBodyReads := bodyProbe.bodyReads.Load()
	if writerBodyReads == 0 {
		t.Fatal("writer request body was not read")
	}

	before := readScopeGateSnapshot(
		t,
		admin,
		writerScope,
		"broker-account\x1furn:xb:apikey:scope-gate-reader",
		idempotency,
	)
	if before.idempotencyRows != 1 ||
		before.commandRows != 1 ||
		before.provisioningRows != 1 ||
		before.readerRows != 0 ||
		before.responseStatus != http.StatusCreated ||
		before.idempotencyState != "completed" ||
		len(before.requestHash) != 32 ||
		!bytes.Equal(before.responseBody, writer.body) {
		t.Fatalf("writer durable snapshot = %#v", before)
	}

	assertScopeGateDenied(
		t,
		server.URL,
		requestBody,
		idempotency,
		writer.body,
		account.ID,
	)
	assertScopeGateDenied(
		t,
		server.URL,
		`{`,
		idempotency,
		writer.body,
		account.ID,
	)
	if got := identityProbe.createBrokerAccountCalls.Load(); got != 1 {
		t.Fatalf("reader crossed authorization boundary: application calls = %d", got)
	}
	if got := bodyProbe.bodyReads.Load(); got != writerBodyReads {
		t.Fatalf(
			"reader crossed authorization boundary: body reads = %d, want %d",
			got,
			writerBodyReads,
		)
	}
	after := readScopeGateSnapshot(
		t,
		admin,
		writerScope,
		"broker-account\x1furn:xb:apikey:scope-gate-reader",
		idempotency,
	)
	if !before.equal(after) {
		t.Fatalf("reader denial changed durable state:\nbefore=%#v\nafter=%#v", before, after)
	}

	server.Close()
	apiPool.Close()
	restartedAPIPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer restartedAPIPool.Close()
	restartedServer, restartedIdentityProbe, restartedBodyProbe := newScopeGateServer(
		t,
		restartedAPIPool,
		now,
	)
	assertScopeGateDenied(
		t,
		restartedServer.URL,
		requestBody,
		idempotency,
		writer.body,
		account.ID,
	)
	assertScopeGateDenied(
		t,
		restartedServer.URL,
		`{`,
		idempotency,
		writer.body,
		account.ID,
	)
	if got := restartedIdentityProbe.createBrokerAccountCalls.Load(); got != 0 {
		t.Fatalf(
			"reader crossed restarted authorization boundary: application calls = %d",
			got,
		)
	}
	if got := restartedBodyProbe.bodyReads.Load(); got != 0 {
		t.Fatalf(
			"reader crossed restarted authorization boundary: body reads = %d",
			got,
		)
	}
	restarted := readScopeGateSnapshot(
		t,
		admin,
		writerScope,
		"broker-account\x1furn:xb:apikey:scope-gate-reader",
		idempotency,
	)
	if !before.equal(restarted) {
		t.Fatalf(
			"reader denial after API restart changed durable state:\nbefore=%#v\nafter=%#v",
			before,
			restarted,
		)
	}
}

func newScopeGateServer(
	t *testing.T,
	apiPool *pgxpool.Pool,
	now time.Time,
) (*httptest.Server, *scopeGateIdentityProbe, *scopeGateBodyProbe) {
	t.Helper()
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-scope-gate-client-secret!"),
		BrokerCredentials: []edge.BrokerCredential{
			{
				Prefix:     "xbk_scope_writer",
				SecretHash: edge.HashBrokerSecret("writer-secret"),
				Subject:    "urn:xb:apikey:scope-gate-writer",
				Tenant:     "urn:xb:tenant:scope-gate",
				Scopes:     []string{"accounts:write"},
			},
			{
				Prefix:     "xbk_scope_reader",
				SecretHash: edge.HashBrokerSecret("reader-secret"),
				Subject:    "urn:xb:apikey:scope-gate-reader",
				Tenant:     "urn:xb:tenant:scope-gate",
				Scopes:     []string{"accounts:read"},
			},
		},
		Clock: scopeGateClock{value: now},
	})
	if err != nil {
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(apiPool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Clock:                      scopeGateClock{value: now},
			Entropy:                    bytes.NewReader(bytes.Repeat([]byte{71}, 128)),
			AccountProvisioningTimeout: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	identityProbe := &scopeGateIdentityProbe{
		IdentityService: identity,
		observerPool:    apiPool,
	}
	bodyProbe := &scopeGateBodyProbe{handler: edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identityProbe,
	}).Handler()}
	server := httptest.NewServer(bodyProbe)
	t.Cleanup(server.Close)
	return server, identityProbe, bodyProbe
}

func waitForScopeGateCommand(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	scope string,
	idempotencyKey string,
	writerDone <-chan scopeGateWriterResult,
) []byte {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		var payload []byte
		err := admin.QueryRow(deadline, `
			SELECT outbox.payload
			  FROM trading.idempotency_records AS idempotency
			  JOIN trading.commands AS command
			    ON command.command_id = idempotency.command_id
			  JOIN messaging.outbox AS outbox
			    ON outbox.message_id = command.command_id
			 WHERE idempotency.scope = $1
			   AND idempotency.idempotency_key = $2`,
			scope,
			idempotencyKey,
		).Scan(&payload)
		if err == nil {
			select {
			case premature := <-writerDone:
				t.Fatalf("writer returned before engine commit: %#v", premature)
			default:
			}
			return payload
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case premature := <-writerDone:
			t.Fatalf("writer returned before engine commit: %#v", premature)
		case <-deadline.Done():
			t.Fatal("writer command was not durably admitted")
		case <-time.After(time.Millisecond):
		}
	}
}

func assertScopeGateDenied(
	t *testing.T,
	serverURL string,
	requestBody string,
	idempotencyKey string,
	writerBody []byte,
	accountID string,
) {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/broker/v1/accounts",
		requestBody,
		map[string]string{
			"x-api-key":       "xbk_scope_reader.reader-secret",
			"idempotency-key": idempotencyKey,
		},
	)
	body, err := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read reader denial: read=%v close=%v", err, closeErr)
	}
	var denial struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&denial); err != nil {
		t.Fatalf("decode reader denial %q: %v", body, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("reader denial has trailing JSON %q: %v", body, err)
	}
	if response.StatusCode != http.StatusForbidden ||
		denial.Code != "forbidden" ||
		denial.Message != "forbidden" ||
		bytes.Equal(body, writerBody) ||
		bytes.Contains(body, []byte(accountID)) {
		t.Fatalf(
			"reader response status=%d body=%s, want isolated 403 forbidden",
			response.StatusCode,
			body,
		)
	}
}

type scopeGateSnapshot struct {
	commandID        string
	requestHash      []byte
	responseHeaders  []byte
	responseBody     []byte
	responseStatus   int
	idempotencyState string
	idempotencyRows  int
	commandRows      int
	provisioningRows int
	readerRows       int
}

func readScopeGateSnapshot(
	t *testing.T,
	admin *pgxpool.Pool,
	writerScope string,
	readerScope string,
	idempotencyKey string,
) scopeGateSnapshot {
	t.Helper()
	var snapshot scopeGateSnapshot
	if err := admin.QueryRow(context.Background(), `
		SELECT
			command_id::text,
			request_hash,
			response_headers,
			response_body,
			response_status,
			state,
			(
				SELECT count(*)
				  FROM trading.idempotency_records
				 WHERE idempotency_key = $2
			),
			(
				SELECT count(*)
				  FROM trading.commands AS command
				  JOIN trading.idempotency_records AS idempotency
				    ON idempotency.command_id = command.command_id
				 WHERE idempotency.idempotency_key = $2
			),
			(
				SELECT count(*)
				  FROM identity.account_provisioning_intents AS intent
				  JOIN trading.idempotency_records AS idempotency
				    ON idempotency.command_id = intent.command_id
				 WHERE idempotency.idempotency_key = $2
			),
			(
				SELECT count(*)
				  FROM trading.idempotency_records
				 WHERE scope = $3
				   AND idempotency_key = $2
			)
		  FROM trading.idempotency_records
		 WHERE scope = $1
		   AND idempotency_key = $2`,
		writerScope,
		idempotencyKey,
		readerScope,
	).Scan(
		&snapshot.commandID,
		&snapshot.requestHash,
		&snapshot.responseHeaders,
		&snapshot.responseBody,
		&snapshot.responseStatus,
		&snapshot.idempotencyState,
		&snapshot.idempotencyRows,
		&snapshot.commandRows,
		&snapshot.provisioningRows,
		&snapshot.readerRows,
	); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func (snapshot scopeGateSnapshot) equal(other scopeGateSnapshot) bool {
	return snapshot.commandID == other.commandID &&
		bytes.Equal(snapshot.requestHash, other.requestHash) &&
		bytes.Equal(snapshot.responseHeaders, other.responseHeaders) &&
		bytes.Equal(snapshot.responseBody, other.responseBody) &&
		snapshot.responseStatus == other.responseStatus &&
		snapshot.idempotencyState == other.idempotencyState &&
		snapshot.idempotencyRows == other.idempotencyRows &&
		snapshot.commandRows == other.commandRows &&
		snapshot.provisioningRows == other.provisioningRows &&
		snapshot.readerRows == other.readerRows
}
