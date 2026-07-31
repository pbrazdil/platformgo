package compatibility_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/migrations"
)

type scopeGateBusinessCounts struct {
	idempotency  int
	commands     int
	provisioning int
	outbox       int
	accounts     int
	ownership    int
	profiles     int
}

type scopeGateAccountWire struct {
	ID               *string   `json:"id"`
	Login            *int64    `json:"login"`
	UserID           *string   `json:"userId"`
	BaseCurrency     *string   `json:"baseCurrency"`
	MarketVenue      *string   `json:"marketVenue"`
	PermittedClasses *[]string `json:"permittedClasses"`
	CreatedAt        *string   `json:"createdAt"`
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:155
//	test: scope_gate_403s_write_route_but_admits_probe
//
// Adaptations:
//   - Source-created broker keys are represented by production configured
//     HMAC credentials with the source scopes and one shared tenant.
//   - Source keyless account creation uses the production generated request
//     identity and completes through the real engine and PostgreSQL roles.
//   - The owner-approved current Go identifier representation is retained;
//     see ports/decisions/broker-account-identifiers-preserve-current-go-urns.md.
//
// Assertions preserved:
//   - A valid accounts:read broker can call the scope-free ping route.
//   - That credential cannot provision an account.
//   - A distinct wildcard-only broker can provision the same tenant's user.
//
// Invariant strengthening:
//   - The denial stops before body parsing, business request identity,
//     application entry, or durable business effects.
//   - The successful current-Go response shape is backed field-for-field by
//     one committed account, ownership, profile, command, and provisioning
//     graph.
func TestScopeGate403sWriteRouteButAdmitsProbe(t *testing.T) {
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
	const shardID engine.ShardID = 72
	if err := migrateAndProvisionCompatibilityFixture(t, ctx, admin, migrations.Files, shardID); err != nil {
		t.Fatal(err)
	}

	const (
		brokerTenant  = "urn:xb:tenant:scope-gate"
		userID        = "urn:xb:user:scope-gate-source"
		wildcardScope = "broker-account\x1furn:xb:apikey:scope-gate-wildcard"
		readerScope   = "broker-account\x1furn:xb:apikey:scope-gate-reader"
		generatedKey  = "scope-gate-generated-4"
	)
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			broker_subject
		) VALUES (
			$1, 'scope-gate-source', 'scope-gate-source',
			'scope-gate-source@example.com',
			'scope-gate-source@example.com',
			$2
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
	defer apiPool.Close()
	enginePool, err := pgxpool.New(ctx, engineDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer enginePool.Close()

	now := time.Date(2026, time.July, 27, 19, 0, 0, 123456000, time.UTC)
	var requestIdentities atomic.Uint64
	server, identityProbe, bodyProbe := newScopeGateServerWithRequestID(
		t,
		apiPool,
		now,
		func() string {
			return fmt.Sprintf(
				"scope-gate-generated-%d",
				requestIdentities.Add(1),
			)
		},
	)
	baseline := readScopeGateBusinessCounts(t, ctx, admin)

	ping := doScopeGateSourceRequest(
		t,
		ctx,
		httpClient,
		http.MethodGet,
		server.URL+"/broker/v1/ping",
		"xbk_scope_reader.reader-secret",
		"",
	)
	if ping.status != http.StatusOK || ping.err != nil {
		t.Fatalf(
			"read-only ping status=%d error=%v body=%s, want 200",
			ping.status,
			ping.err,
			ping.body,
		)
	}
	afterPingIdentities := requestIdentities.Load()
	if afterPingIdentities != 1 {
		t.Fatalf(
			"read-only ping request identity calls=%d, want one transport ID",
			afterPingIdentities,
		)
	}

	requestBody := `{"userId":"` + userID + `"}`
	denied := doScopeGateSourceRequest(
		t,
		ctx,
		httpClient,
		http.MethodPost,
		server.URL+"/broker/v1/accounts",
		"xbk_scope_reader.reader-secret",
		requestBody,
	)
	requireScopeGateSourceDenial(t, denied)
	if got := requestIdentities.Load(); got != afterPingIdentities+1 {
		t.Fatalf(
			"read-only denial crossed business request identity: calls=%d, want one additional transport ID",
			got,
		)
	}
	if got := identityProbe.createBrokerAccountCalls.Load(); got != 0 {
		t.Fatalf(
			"read-only denial crossed application boundary: calls=%d",
			got,
		)
	}
	if got := bodyProbe.bodyReads.Load(); got != 0 {
		t.Fatalf("read-only denial parsed request body: reads=%d", got)
	}
	afterDenied := readScopeGateBusinessCounts(t, ctx, admin)
	if baseline != afterDenied {
		t.Fatalf(
			"read-only denial changed durable business state:\nbefore=%#v\nafter=%#v",
			baseline,
			afterDenied,
		)
	}

	wildcardDone := beginScopeGateSourceAccount(
		t,
		ctx,
		httpClient,
		server.URL,
		"xbk_scope_wildcard.wildcard-secret",
		requestBody,
	)
	payload := waitForScopeGateCommand(
		t,
		ctx,
		admin,
		wildcardScope,
		generatedKey,
		wildcardDone,
	)
	applyScopeGateSourceCommand(
		t,
		ctx,
		enginePool,
		shardID,
		payload,
	)
	wildcard := <-wildcardDone
	account := requireScopeGateSourceAccount(
		t,
		wildcard,
		userID,
		now,
	)
	if got := requestIdentities.Load(); got != 4 {
		t.Fatalf(
			"wildcard keyless request identity calls=%d, want transport plus business identity",
			got,
		)
	}
	if got := identityProbe.createBrokerAccountCalls.Load(); got != 1 {
		t.Fatalf("wildcard application calls=%d, want 1", got)
	}
	if identityProbe.returnedBeforeCommit.Load() {
		t.Fatal("wildcard response returned before the engine commit")
	}
	if got := bodyProbe.bodyReads.Load(); got == 0 {
		t.Fatal("wildcard request body was not parsed")
	}

	snapshot := readScopeGateSnapshot(
		t,
		ctx,
		admin,
		wildcardScope,
		readerScope,
		generatedKey,
	)
	if snapshot.idempotencyRows != 1 ||
		snapshot.commandRows != 1 ||
		snapshot.provisioningRows != 1 ||
		snapshot.readerRows != 0 ||
		snapshot.responseStatus != http.StatusCreated ||
		snapshot.idempotencyState != "completed" ||
		len(snapshot.requestHash) != 32 ||
		!bytes.Equal(snapshot.responseBody, wildcard.body) {
		t.Fatalf("wildcard durable snapshot=%#v", snapshot)
	}
	if rows := scopeGateSourceAccountGraphRows(
		t,
		ctx,
		admin,
		account,
		snapshot.commandID,
		userID,
		brokerTenant,
		now,
	); rows != 1 {
		t.Fatalf("wildcard committed account graph rows=%d, want 1", rows)
	}
	afterSuccess := readScopeGateBusinessCounts(t, ctx, admin)
	wantSuccess := baseline
	wantSuccess.idempotency++
	wantSuccess.commands++
	wantSuccess.provisioning++
	wantSuccess.outbox++
	wantSuccess.accounts++
	wantSuccess.ownership++
	wantSuccess.profiles++
	if afterSuccess != wantSuccess {
		t.Fatalf(
			"wildcard durable business deltas:\ngot=%#v\nwant=%#v",
			afterSuccess,
			wantSuccess,
		)
	}
}

func doScopeGateSourceRequest(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	method string,
	url string,
	token string,
	body string,
) scopeGateWriterResult {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
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
	request.Header.Set("x-api-key", token)
	response, err := client.Do(request)
	if err != nil {
		return scopeGateWriterResult{err: err}
	}
	responseBody, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	return scopeGateWriterResult{
		status:  response.StatusCode,
		headers: response.Header.Clone(),
		body:    responseBody,
		err:     errors.Join(readErr, closeErr),
	}
}

func beginScopeGateSourceAccount(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	serverURL string,
	token string,
	body string,
) <-chan scopeGateWriterResult {
	t.Helper()
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		serverURL+"/broker/v1/accounts",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-api-key", token)
	done := make(chan scopeGateWriterResult, 1)
	go func() {
		response, requestErr := client.Do(request)
		if requestErr != nil {
			done <- scopeGateWriterResult{err: requestErr}
			return
		}
		responseBody, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		done <- scopeGateWriterResult{
			status:  response.StatusCode,
			headers: response.Header.Clone(),
			body:    responseBody,
			err:     errors.Join(readErr, closeErr),
		}
	}()
	return done
}

func applyScopeGateSourceCommand(
	t *testing.T,
	ctx context.Context,
	enginePool *pgxpool.Pool,
	shardID engine.ShardID,
	payload []byte,
) {
	t.Helper()
	input, action, err := engine.DecodeInputMessage(payload)
	if err != nil {
		t.Fatal(err)
	}
	input.StreamSequence = 1
	store := platformpostgres.NewEngineStore(enginePool)
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			2*time.Second,
		)
		defer cancel()
		if closeErr := ownership.Close(cleanupContext); closeErr != nil {
			t.Errorf("close engine ownership: %v", closeErr)
		}
	}()
	state, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		t.Fatal(err)
	}
	_, decision, duplicate, err := store.ApplyTrading(
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
			"wildcard engine result duplicate=%t result=%#v",
			duplicate,
			decision.CommandResult,
		)
	}
}

func requireScopeGateSourceDenial(
	t *testing.T,
	result scopeGateWriterResult,
) {
	t.Helper()
	if result.err != nil ||
		result.status != http.StatusForbidden ||
		result.headers.Get("content-type") != "application/json" {
		t.Fatalf(
			"read-only denial status=%d headers=%v error=%v body=%s",
			result.status,
			result.headers,
			result.err,
			result.body,
		)
	}
	var denial struct {
		Code    *string `json:"code"`
		Message *string `json:"message"`
	}
	if err := json.Unmarshal(result.body, &denial); err != nil {
		t.Fatalf("decode read-only denial %q: %v", result.body, err)
	}
	if denial.Code == nil ||
		*denial.Code != "forbidden" ||
		denial.Message == nil ||
		*denial.Message != "forbidden" {
		t.Fatalf("read-only denial body=%s", result.body)
	}
}

func requireScopeGateSourceAccount(
	t *testing.T,
	result scopeGateWriterResult,
	userID string,
	now time.Time,
) scopeGateAccountWire {
	t.Helper()
	if result.err != nil ||
		result.status != http.StatusCreated ||
		result.headers.Get("content-type") != "application/json" {
		t.Fatalf(
			"wildcard response status=%d headers=%v error=%v body=%s",
			result.status,
			result.headers,
			result.err,
			result.body,
		)
	}
	var account scopeGateAccountWire
	decoder := json.NewDecoder(bytes.NewReader(result.body))
	if err := decoder.Decode(&account); err != nil {
		t.Fatalf("decode wildcard account %q: %v", result.body, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("wildcard account has trailing JSON %q: %v", result.body, err)
	}
	if account.ID == nil ||
		!isScopeGateCurrentGoAccountID(*account.ID) ||
		account.Login == nil ||
		*account.Login <= 0 ||
		account.UserID == nil ||
		*account.UserID != userID ||
		account.BaseCurrency == nil ||
		*account.BaseCurrency != "USDC" ||
		account.MarketVenue == nil ||
		*account.MarketVenue != "HYPERLIQUID" ||
		account.PermittedClasses == nil ||
		len(*account.PermittedClasses) != 1 ||
		(*account.PermittedClasses)[0] != "CRYPTOCURRENCY" ||
		account.CreatedAt == nil ||
		*account.CreatedAt != now.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("wildcard account=%#v", account)
	}
	return account
}

func isScopeGateCurrentGoAccountID(value string) bool {
	const prefix = "urn:xb:account:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	suffix := strings.TrimPrefix(value, prefix)
	if len(suffix) != 36 ||
		suffix[8] != '-' ||
		suffix[13] != '-' ||
		suffix[18] != '-' ||
		suffix[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(strings.ReplaceAll(suffix, "-", ""))
	return err == nil
}

func readScopeGateBusinessCounts(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
) scopeGateBusinessCounts {
	t.Helper()
	var counts scopeGateBusinessCounts
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.idempotency_records),
			(SELECT count(*) FROM trading.commands),
			(SELECT count(*) FROM identity.account_provisioning_intents),
			(SELECT count(*) FROM messaging.outbox),
			(SELECT count(*) FROM trading.accounts),
			(SELECT count(*) FROM identity.user_accounts),
			(SELECT count(*) FROM identity.account_profiles)`).
		Scan(
			&counts.idempotency,
			&counts.commands,
			&counts.provisioning,
			&counts.outbox,
			&counts.accounts,
			&counts.ownership,
			&counts.profiles,
		); err != nil {
		t.Fatal(err)
	}
	return counts
}

func scopeGateSourceAccountGraphRows(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	account scopeGateAccountWire,
	commandID string,
	userID string,
	brokerTenant string,
	now time.Time,
) int {
	t.Helper()
	var rows int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.accounts AS account
		  JOIN identity.user_accounts AS ownership
		    ON ownership.account_id = account.account_id
		   AND ownership.user_id = $2
		   AND ownership.broker_subject = $3
		   AND ownership.created_at = $9
		  JOIN identity.account_profiles AS profile
		    ON profile.account_id = account.account_id
		   AND profile.broker_subject = $3
		   AND profile.login = $4
		   AND profile.base_currency = $5
		   AND profile.market_venue = $6
		   AND profile.permitted_classes = $7
		   AND profile.created_at = $9
		  JOIN identity.account_provisioning_intents AS intent
		    ON intent.command_id = $8::uuid
		   AND intent.account_id = account.account_id
		   AND intent.broker_subject = $3
		   AND intent.user_id = $2
		   AND intent.login = $4
		   AND intent.base_currency = $5
		   AND intent.market_venue = $6
		   AND intent.permitted_classes = $7
		   AND intent.created_at = $9
		 WHERE account.account_id = $1`,
		*account.ID,
		userID,
		brokerTenant,
		*account.Login,
		*account.BaseCurrency,
		*account.MarketVenue,
		*account.PermittedClasses,
		commandID,
		now,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
