package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:229
//	test: client_creates_own_api_key
//
// Adaptations:
//   - The Rust runtime is replaced by the Go HTTP edge and real PostgreSQL 19 Beta 2.
//   - Fixture password length follows the accepted Go credential policy; the
//     source test asserts successful authentication, not a password minimum.
//   - Shared request fields unused by the source client handler are accepted
//     but do not alter the user-owned key.
//   - Persistence is inspected to prove that the returned secret is shown once
//     and only its SHA-256 digest is stored.
//
// Assertions preserved:
//   - An authenticated trader receives 201 and an xbk_ API-key token.
//   - Anonymous creation returns 401.
func TestClientCreatesOwnAPIKey(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()

	anonymous := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"x"}`,
		nil,
	)
	defer anonymous.Body.Close()
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d, want 401", anonymous.StatusCode)
	}

	accessToken := loginMyAPIKeyOwner(t, serverURL)
	missingIdempotency := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"lost-secret-risk"}`,
		map[string]string{
			"authorization": "Bearer " + accessToken,
			"x-request-id":  "request-missing-idempotency",
		},
	)
	var missingBody map[string]any
	decodeAndClose(t, missingIdempotency, &missingBody)
	if missingIdempotency.StatusCode != http.StatusBadRequest ||
		missingBody["code"] != "invalid_request" {
		t.Fatalf(
			"missing idempotency status = %d body = %#v",
			missingIdempotency.StatusCode,
			missingBody,
		)
	}
	var missingEffectCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*) FROM identity.api_keys`,
	).Scan(&missingEffectCount); err != nil || missingEffectCount != 0 {
		t.Fatalf(
			"missing-idempotency durable effects = %d, error = %v",
			missingEffectCount,
			err,
		)
	}
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"my-bot","scopes":["orders:write"],"ipAllowlist":["203.0.113.7"],"ttlSecs":3600,"tenantId":"urn:xb:tenant:AbC123"}`,
		map[string]string{
			"authorization":   "Bearer " + accessToken,
			"x-request-id":    "request-create-my-bot",
			"idempotency-key": "create-my-bot",
		},
	)
	var created edge.APIKeyCreated
	decodeAndClose(t, response, &created)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, body = %#v", response.StatusCode, created)
	}
	if !strings.HasPrefix(created.ID, "urn:xb:apikey:") ||
		len(created.Prefix) != 12 ||
		!strings.HasPrefix(created.Token, "xbk_"+created.Prefix+".") {
		t.Fatalf("created API key = %#v", created)
	}
	parts := strings.Split(strings.TrimPrefix(created.Token, "xbk_"), ".")
	if len(parts) != 2 || parts[0] != created.Prefix {
		t.Fatalf("token format = %q", created.Token)
	}
	secret, err := hex.DecodeString(parts[1])
	if err != nil || len(secret) != 24 {
		t.Fatalf("token secret = %q, error = %v", parts[1], err)
	}

	var (
		storedName   string
		storedPrefix string
		storedHash   []byte
		storedScopes []string
	)
	if err := admin.QueryRow(ctx, `
		SELECT name, prefix, key_hash, scopes
		  FROM identity.api_keys
		 WHERE api_key_id::text = $1`,
		strings.TrimPrefix(created.ID, "urn:xb:apikey:"),
	).Scan(
		&storedName,
		&storedPrefix,
		&storedHash,
		&storedScopes,
	); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(parts[1]))
	if storedName != "my-bot" ||
		storedPrefix != created.Prefix ||
		!bytes.Equal(storedHash, wantHash[:]) ||
		len(storedScopes) != 1 ||
		storedScopes[0] != "orders:write" {
		t.Fatalf(
			"stored name=%q prefix=%q hash=%x scopes=%v",
			storedName,
			storedPrefix,
			storedHash,
			storedScopes,
		)
	}
	if strings.Contains(string(storedHash), created.Token) {
		t.Fatal("stored API-key hash contains the returned token")
	}
	manyScopes := make([]string, 33)
	for index := range manyScopes {
		manyScopes[index] = "scope-" + string(rune('a'+index))
	}
	for index, request := range []edge.CreateAPIKeyRequest{
		{
			Name:   " bad ",
			Scopes: []string{"orders:write", "orders:write"},
		},
		{Name: strings.Repeat("x", 129)},
		{Name: "many-scopes", Scopes: manyScopes},
		{Name: "empty-scope", Scopes: []string{""}},
	} {
		if request.Scopes == nil {
			request.Scopes = []string{}
		}
		if request.IPAllowlist == nil {
			request.IPAllowlist = []string{}
		}
		encoded, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		sourceCompatible := createMyAPIKey(
			t,
			serverURL,
			accessToken,
			string(encoded),
			"request-source-compatible-"+string(rune('a'+index)),
		)
		if sourceCompatible.status != http.StatusCreated {
			t.Fatalf(
				"source-compatible case %d status = %d, body = %s",
				index,
				sourceCompatible.status,
				sourceCompatible.body,
			)
		}
	}
	unknownFirst := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"additive","futureScalar":7,"futureObject":{"x":true},"futureArray":[1,2],"futureNull":null}`,
		"request-additive-first",
		"create-additive",
	)
	unknownReplay := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"additive","futureScalar":8,"futureObject":{"x":false}}`,
		"request-additive-replay",
		"create-additive",
	)
	if unknownFirst.status != http.StatusCreated ||
		unknownReplay.status != http.StatusCreated ||
		unknownFirst.body != unknownReplay.body {
		t.Fatalf(
			"ignored-field replay = first %d %q replay %d %q",
			unknownFirst.status,
			unknownFirst.body,
			unknownReplay.status,
			unknownReplay.body,
		)
	}
	for index, body := range []string{
		`{"name":"ttl-zero","ttlSecs":0}`,
		`{"name":"nullable-optionals","ttlSecs":null,"tenantId":null}`,
		`{"name":"max-tenant","tenantId":"urn:xb:tenant:7n42DGM5Tflk9n8mt7Fhc7"}`,
	} {
		result := createMyAPIKey(
			t,
			serverURL,
			accessToken,
			body,
			"request-valid-optionals-"+string(rune('a'+index)),
		)
		if result.status != http.StatusCreated {
			t.Fatalf(
				"valid optional case %d status = %d body = %s",
				index,
				result.status,
				result.body,
			)
		}
	}
	for index, body := range []string{
		`{"name":"bad-tenant","tenantId":"urn:xb:tenant:not-valid"}`,
		`{"name":"overflow-tenant","tenantId":"urn:xb:tenant:7n42DGM5Tflk9n8mt7Fhc8"}`,
		`{"name":"negative-ttl","ttlSecs":-1}`,
		`{"name":"null-scopes","scopes":null}`,
		`{"name":"null-ips","ipAllowlist":null}`,
		`{"name":"bad-known","scopes":"orders:write"}`,
		`{"name":"trailing"} {}`,
	} {
		result := createMyAPIKey(
			t,
			serverURL,
			accessToken,
			body,
			"request-invalid-"+string(rune('a'+index)),
		)
		if result.status != http.StatusBadRequest {
			t.Fatalf(
				"invalid source case %d status = %d body = %s",
				index,
				result.status,
				result.body,
			)
		}
	}
	var compatibleCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.api_keys
		 WHERE owner_user_id = 'urn:xb:user:bot-owner'`,
	).Scan(&compatibleCount); err != nil {
		t.Fatal(err)
	}
	if compatibleCount != 9 {
		t.Fatalf(
			"key count after source-compatible requests = %d, want 9",
			compatibleCount,
		)
	}
}

// The pinned client router applies its idempotency middleware to every
// authenticated mutating route. This implementation-only recovery test closes
// the source test's commit-before-response-loss gap under the stronger project
// idempotency invariant.
func TestUserAPIKeyCreationReplaysOneDurableCredential(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET client_rate_limit_max_requests = 1,
		       client_rate_limit_window_seconds = 60,
		       version = version + 1
		 WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	accessToken := loginMyAPIKeyOwner(t, serverURL)

	authenticator := newMyAPIKeyAuthenticator(t)
	faultStore := &postCommitUnknownAPIKeyStore{
		CompatibilityStore: platformpostgres.NewCompatibilityStore(apiPool),
	}
	faultServerURL := newMyAPIKeyServer(
		t,
		faultStore,
		authenticator,
		[]application.APIKeyReplayKey{testAPIKeyReplayKey("old-v1", 1)},
	)
	lost := createMyAPIKeyWithIdempotency(
		t,
		faultServerURL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		"request-replay-first",
		"create-replay-bot",
	)
	if lost.status != http.StatusServiceUnavailable {
		t.Fatalf(
			"post-commit unknown status = %d, body = %s",
			lost.status,
			lost.body,
		)
	}

	noEntropyIdentity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Entropy:                 failingReader{},
			APIKeyReplayKeys:        []application.APIKeyReplayKey{testAPIKeyReplayKey("old-v1", 1)},
			APIKeyReplayActiveKeyID: "old-v1",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	noEntropyServer := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      noEntropyIdentity,
	}).Handler())
	t.Cleanup(noEntropyServer.Close)
	noEntropyReplay := createMyAPIKeyWithIdempotency(
		t,
		noEntropyServer.URL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		"request-replay-no-entropy",
		"create-replay-bot",
	)
	if noEntropyReplay.status != http.StatusCreated {
		t.Fatalf(
			"no-entropy replay status = %d body = %s",
			noEntropyReplay.status,
			noEntropyReplay.body,
		)
	}
	changedRequestIDReplay := createMyAPIKeyWithIdempotency(
		t,
		noEntropyServer.URL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		strings.Repeat("r", 129),
		"create-replay-bot",
	)
	if changedRequestIDReplay.status != http.StatusCreated ||
		changedRequestIDReplay.body != noEntropyReplay.body {
		t.Fatalf(
			"ancillary request-ID replay = %d %q, want exact %q",
			changedRequestIDReplay.status,
			changedRequestIDReplay.body,
			noEntropyReplay.body,
		)
	}
	noEntropyConflict := createMyAPIKeyWithIdempotency(
		t,
		noEntropyServer.URL,
		accessToken,
		`{"name":"changed-bot","scopes":["orders:write"]}`,
		"request-conflict-no-entropy",
		"create-replay-bot",
	)
	if noEntropyConflict.status != http.StatusConflict {
		t.Fatalf(
			"no-entropy conflict status = %d body = %s",
			noEntropyConflict.status,
			noEntropyConflict.body,
		)
	}

	missingRotationKeyURL := newMyAPIKeyServer(
		t,
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		[]application.APIKeyReplayKey{testAPIKeyReplayKey("new-v2", 2)},
	)
	unrecoverable := createMyAPIKeyWithIdempotency(
		t,
		missingRotationKeyURL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		"request-replay-missing-key",
		"create-replay-bot",
	)
	if unrecoverable.status != http.StatusServiceUnavailable {
		t.Fatalf(
			"missing rotation key status = %d, body = %s",
			unrecoverable.status,
			unrecoverable.body,
		)
	}

	recoveryServerURL := newMyAPIKeyServer(
		t,
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		[]application.APIKeyReplayKey{
			testAPIKeyReplayKey("new-v2", 2),
			testAPIKeyReplayKey("old-v1", 1),
		},
	)
	recovered := createMyAPIKeyWithIdempotency(
		t,
		recoveryServerURL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		"request-replay-second",
		"create-replay-bot",
	)
	replay := createMyAPIKeyWithIdempotency(
		t,
		recoveryServerURL,
		accessToken,
		`{"name":"replay-bot","scopes":["orders:write"]}`,
		"request-replay-third",
		"create-replay-bot",
	)
	if recovered.status != http.StatusCreated ||
		replay.status != http.StatusCreated ||
		recovered.body != replay.body ||
		recovered.header.Get("content-type") !=
			replay.header.Get("content-type") {
		t.Fatalf(
			"idempotent responses = recovered %d %q replay %d %q",
			recovered.status,
			recovered.body,
			replay.status,
			replay.body,
		)
	}

	conflict := createMyAPIKeyWithIdempotency(
		t,
		recoveryServerURL,
		accessToken,
		`{"name":"changed-bot","scopes":["orders:write"]}`,
		"request-replay-conflict",
		"create-replay-bot",
	)
	if conflict.status != http.StatusConflict {
		t.Fatalf(
			"idempotency conflict status = %d, body = %s",
			conflict.status,
			conflict.body,
		)
	}

	if _, err := admin.Exec(ctx, `
		DELETE FROM identity.client_rate_limits
		 WHERE owner_user_id = 'urn:xb:user:bot-owner'`); err != nil {
		t.Fatal(err)
	}
	const duplicateDeliveries = 8
	type deliveryResult struct {
		status int
		body   string
		err    error
	}
	start := make(chan struct{})
	deliveries := make(chan deliveryResult, duplicateDeliveries)
	var wait sync.WaitGroup
	for range duplicateDeliveries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			request, err := http.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				recoveryServerURL+"/v1/me/api-keys",
				strings.NewReader(
					`{"name":"concurrent-bot","scopes":["orders:read"]}`,
				),
			)
			if err != nil {
				deliveries <- deliveryResult{err: err}
				return
			}
			request.Header.Set("content-type", "application/json")
			request.Header.Set("authorization", "Bearer "+accessToken)
			request.Header.Set("idempotency-key", "create-concurrent-bot")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				deliveries <- deliveryResult{err: err}
				return
			}
			var raw bytes.Buffer
			_, readErr := raw.ReadFrom(response.Body)
			_ = response.Body.Close()
			deliveries <- deliveryResult{
				status: response.StatusCode,
				body:   raw.String(),
				err:    readErr,
			}
		}()
	}
	close(start)
	wait.Wait()
	close(deliveries)
	var concurrentBody string
	for delivery := range deliveries {
		if delivery.err != nil {
			t.Fatal(delivery.err)
		}
		if delivery.status != http.StatusCreated {
			t.Fatalf(
				"duplicate delivery status = %d, body = %s",
				delivery.status,
				delivery.body,
			)
		}
		if concurrentBody == "" {
			concurrentBody = delivery.body
		} else if delivery.body != concurrentBody {
			t.Fatalf(
				"concurrent replay body = %q, want %q",
				delivery.body,
				concurrentBody,
			)
		}
	}

	var (
		keyCount    int
		auditCount  int
		replayCount int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.api_keys),
			(SELECT count(*) FROM audit.events
			  WHERE action = 'user-key.create'),
			(SELECT count(*) FROM identity.api_key_replays)`,
	).Scan(&keyCount, &auditCount, &replayCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 2 || auditCount != 2 || replayCount != 2 {
		t.Fatalf(
			"durable replay state = keys %d audits %d replays %d",
			keyCount,
			auditCount,
			replayCount,
		)
	}

	rows, err := admin.Query(ctx, `
		SELECT response_ciphertext
		  FROM identity.api_key_replays`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var encryptedReplay []byte
		if err := rows.Scan(&encryptedReplay); err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(encryptedReplay, []byte(recovered.created.Token)) ||
			bytes.Contains(encryptedReplay, []byte("xbk_")) {
			t.Fatal("encrypted replay contains plaintext token material")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestLongIdempotencyKeyUsesFixedDurableDigest(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	accessToken := loginMyAPIKeyOwner(t, serverURL)
	idempotencyKey := strings.Repeat("incompressible-0123456789-", 140)
	first := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"long-idempotency"}`,
		"request-long-idempotency-first",
		idempotencyKey,
	)
	replay := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"long-idempotency"}`,
		"request-long-idempotency-replay",
		idempotencyKey,
	)
	conflict := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"long-idempotency-changed"}`,
		"request-long-idempotency-conflict",
		idempotencyKey,
	)
	if first.status != http.StatusCreated ||
		replay.status != http.StatusCreated ||
		replay.body != first.body ||
		conflict.status != http.StatusConflict {
		t.Fatalf(
			"long idempotency = first %d replay %d conflict %d",
			first.status,
			replay.status,
			conflict.status,
		)
	}
	var (
		keyCount      int
		auditCount    int
		replayCount   int
		rateCount     int64
		rawKeyColumns int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.api_keys),
			(SELECT count(*) FROM audit.events
			  WHERE action = 'user-key.create'),
			(SELECT count(*) FROM identity.api_key_replays
			  WHERE octet_length(idempotency_key_hash) = 32),
			(SELECT request_count FROM identity.client_rate_limits
			  WHERE owner_user_id = 'urn:xb:user:bot-owner'),
			(SELECT count(*) FROM information_schema.columns
			  WHERE table_schema = 'identity'
			    AND table_name = 'api_key_replays'
			    AND column_name = 'idempotency_key')`,
	).Scan(
		&keyCount,
		&auditCount,
		&replayCount,
		&rateCount,
		&rawKeyColumns,
	); err != nil {
		t.Fatal(err)
	}
	if keyCount != 1 || auditCount != 1 || replayCount != 1 ||
		rateCount != 1 || rawKeyColumns != 0 {
		t.Fatalf(
			"long-key durable state = keys %d audits %d replays %d rate %d raw-columns %d",
			keyCount,
			auditCount,
			replayCount,
			rateCount,
			rawKeyColumns,
		)
	}
}

func TestUserAPIKeyReplayRotationUsesDistributeThenPromote(t *testing.T) {
	_, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	accessToken := loginMyAPIKeyOwner(t, serverURL)
	authenticator := newMyAPIKeyAuthenticator(t)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	oldKey := testAPIKeyReplayKey("old-v1", 1)
	newKey := testAPIKeyReplayKey("new-v2", 2)

	oldOnly := newMyAPIKeyServerWithActiveKey(
		t,
		store,
		authenticator,
		[]application.APIKeyReplayKey{oldKey},
		"old-v1",
	)
	oldCreated := createMyAPIKeyWithIdempotency(
		t,
		oldOnly,
		accessToken,
		`{"name":"rotation-old"}`,
		"rotation-old-first",
		"rotation-old",
	)
	if oldCreated.status != http.StatusCreated {
		t.Fatalf("old-only create = %d %s", oldCreated.status, oldCreated.body)
	}

	dualDecryptOldActive := newMyAPIKeyServerWithActiveKey(
		t,
		store,
		authenticator,
		[]application.APIKeyReplayKey{oldKey, newKey},
		"old-v1",
	)
	oldReplayed := createMyAPIKeyWithIdempotency(
		t,
		dualDecryptOldActive,
		accessToken,
		`{"name":"rotation-old"}`,
		"rotation-old-replay",
		"rotation-old",
	)
	if oldReplayed.body != oldCreated.body {
		t.Fatalf("phase-one old replay = %q, want %q", oldReplayed.body, oldCreated.body)
	}
	phaseOneCreated := createMyAPIKeyWithIdempotency(
		t,
		dualDecryptOldActive,
		accessToken,
		`{"name":"rotation-distributed"}`,
		"rotation-distributed-first",
		"rotation-distributed",
	)
	phaseOneOldReplay := createMyAPIKeyWithIdempotency(
		t,
		oldOnly,
		accessToken,
		`{"name":"rotation-distributed"}`,
		"rotation-distributed-old-replay",
		"rotation-distributed",
	)
	if phaseOneCreated.status != http.StatusCreated ||
		phaseOneOldReplay.body != phaseOneCreated.body {
		t.Fatalf(
			"phase-one cross-replica replay = created %d %q old %d %q",
			phaseOneCreated.status,
			phaseOneCreated.body,
			phaseOneOldReplay.status,
			phaseOneOldReplay.body,
		)
	}

	newActiveA := newMyAPIKeyServerWithActiveKey(
		t,
		store,
		authenticator,
		[]application.APIKeyReplayKey{oldKey, newKey},
		"new-v2",
	)
	newActiveB := newMyAPIKeyServerWithActiveKey(
		t,
		store,
		authenticator,
		[]application.APIKeyReplayKey{oldKey, newKey},
		"new-v2",
	)
	newCreated := createMyAPIKeyWithIdempotency(
		t,
		newActiveA,
		accessToken,
		`{"name":"rotation-new"}`,
		"rotation-new-first",
		"rotation-new",
	)
	newReplayed := createMyAPIKeyWithIdempotency(
		t,
		newActiveB,
		accessToken,
		`{"name":"rotation-new"}`,
		"rotation-new-replay",
		"rotation-new",
	)
	if newCreated.status != http.StatusCreated ||
		newReplayed.body != newCreated.body {
		t.Fatalf(
			"phase-two cross-replica replay = created %d %q replay %d %q",
			newCreated.status,
			newCreated.body,
			newReplayed.status,
			newReplayed.body,
		)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_rate_limit.rs:12
//	test: protected_surface_is_per_principal_rate_limited
//
// Adaptations:
//   - The Rust runtime is replaced by the Go HTTP edge and real PostgreSQL 19 Beta 2.
//   - Fixed test accounts and a catalog instrument replace source fixture
//     builders while preserving the same public and protected routes.
//
// Assertions preserved:
//   - A principal initially receives 200 from its account positions route and
//     then receives 429 after exhausting its protected-request bucket.
//   - The 429 includes Retry-After and the too_many_requests error code.
//   - A second principal retains an independent bucket on its own positions
//     route.
//   - Repeated anonymous instrument-catalog reads are never rate-limited.
func TestProtectedSurfaceIsPerPrincipalRateLimited(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET client_rate_limit_max_requests = 5,
		       client_rate_limit_window_seconds = 60,
		       version = version + 1
		 WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id,
			login,
			normalized_login,
			email,
			normalized_email,
			password_hash
		)
		SELECT
			'urn:xb:user:second-owner',
			'secondtrader',
			'secondtrader',
			'second@xb.local',
			'second@xb.local',
			password_hash
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:bot-owner'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, -0.0001, 0.0005
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES
			('urn:xb:account:rate-owner', 'NETTING'),
			('urn:xb:account:rate-second', 'NETTING');
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES
			('urn:xb:user:bot-owner', 'urn:xb:account:rate-owner'),
			('urn:xb:user:second-owner', 'urn:xb:account:rate-second')`); err != nil {
		t.Fatal(err)
	}
	accessToken := loginMyAPIKeyOwner(t, serverURL)
	ownerHeaders := map[string]string{"authorization": "Bearer " + accessToken}
	positionsURL := serverURL + "/v1/accounts/urn:xb:account:rate-owner/positions"
	var limited *http.Response
	for range 50 {
		response := requestJSON(
			t,
			http.MethodGet,
			positionsURL,
			"",
			ownerHeaders,
		)
		if response.StatusCode == http.StatusTooManyRequests {
			limited = response
			break
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf(
				"under-limit protected read status = %d, want 200",
				response.StatusCode,
			)
		}
	}
	if limited == nil {
		t.Fatal("owner did not reach the protected-request limit within 50 requests")
	}
	var limitedBody map[string]any
	decodeAndClose(t, limited, &limitedBody)
	if got := limited.Header.Get("retry-after"); got != "60" {
		t.Fatalf("rate-limit Retry-After = %q, want 60", got)
	}
	if limitedBody["code"] != "too_many_requests" {
		t.Fatalf("rate-limit body = %#v", limitedBody)
	}
	secondAccessToken := loginMyAPIKey(
		t,
		serverURL,
		"secondtrader",
	)
	isolated := requestJSON(
		t,
		http.MethodGet,
		serverURL+"/v1/accounts/urn:xb:account:rate-second/positions",
		"",
		map[string]string{"authorization": "Bearer " + secondAccessToken},
	)
	_ = isolated.Body.Close()
	if isolated.StatusCode != http.StatusOK {
		t.Fatalf("second owner status = %d, want 200", isolated.StatusCode)
	}
	for range 10 {
		public := requestJSON(
			t,
			http.MethodGet,
			serverURL+"/v1/instruments",
			"",
			nil,
		)
		_ = public.Body.Close()
		if public.StatusCode == http.StatusTooManyRequests {
			t.Fatal("anonymous instrument catalog was rate-limited")
		}
	}
}

func TestUserAPIKeyCreationIsRateLimitedPerPrincipal(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET client_rate_limit_max_requests = 3,
		       client_rate_limit_window_seconds = 60,
		       version = version + 1
		 WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id,
			login,
			normalized_login,
			email,
			normalized_email,
			password_hash
		)
		SELECT
			'urn:xb:user:second-owner',
			'secondtrader',
			'secondtrader',
			'second@xb.local',
			'second@xb.local',
			password_hash
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:bot-owner'`); err != nil {
		t.Fatal(err)
	}
	accessToken := loginMyAPIKeyOwner(t, serverURL)
	protectedRead := requestJSON(
		t,
		http.MethodGet,
		serverURL+"/v1/me",
		"",
		map[string]string{"authorization": "Bearer " + accessToken},
	)
	_ = protectedRead.Body.Close()
	if protectedRead.StatusCode != http.StatusOK {
		t.Fatalf("protected read status = %d", protectedRead.StatusCode)
	}
	missingIdempotency := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"missing-idempotency"}`,
		map[string]string{"authorization": "Bearer " + accessToken},
	)
	_ = missingIdempotency.Body.Close()
	if missingIdempotency.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"missing-idempotency status = %d, want 400",
			missingIdempotency.StatusCode,
		)
	}
	admitted := createMyAPIKey(
		t,
		serverURL,
		accessToken,
		`{"name":"rate-bot-a"}`,
		"request-rate-a",
	)
	if admitted.status != http.StatusCreated {
		t.Fatalf(
			"admitted mutation status = %d, body = %s",
			admitted.status,
			admitted.body,
		)
	}
	otherServerURL := newMyAPIKeyServer(
		t,
		platformpostgres.NewCompatibilityStore(apiPool),
		newMyAPIKeyAuthenticator(t),
		[]application.APIKeyReplayKey{testAPIKeyReplayKey("test-v1", 1)},
	)
	limitedMutation := createMyAPIKey(
		t,
		otherServerURL,
		accessToken,
		`{"name":"rate-bot-c"}`,
		"request-rate-c",
	)
	if limitedMutation.status != http.StatusTooManyRequests {
		t.Fatalf(
			"rate-limited status = %d, body = %s",
			limitedMutation.status,
			limitedMutation.body,
		)
	}
	if got := limitedMutation.header.Get("retry-after"); got != "60" {
		t.Fatalf("rate-limit Retry-After = %q, want 60", got)
	}
	if !strings.Contains(limitedMutation.body, `"code":"too_many_requests"`) ||
		!strings.Contains(limitedMutation.body, `"message":"rate_limited"`) {
		t.Fatalf("rate-limit body = %s", limitedMutation.body)
	}
	secondAccessToken := loginMyAPIKey(
		t,
		serverURL,
		"secondtrader",
	)
	isolated := createMyAPIKey(
		t,
		serverURL,
		secondAccessToken,
		`{"name":"second-owner-bot"}`,
		"request-second-owner",
	)
	if isolated.status != http.StatusCreated {
		t.Fatalf(
			"second owner status = %d, body = %s",
			isolated.status,
			isolated.body,
		)
	}
	var (
		keyCount   int
		auditCount int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM identity.api_keys),
			(SELECT count(*) FROM audit.events
			  WHERE action = 'user-key.create')`,
	).Scan(&keyCount, &auditCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 2 || auditCount != 2 {
		t.Fatalf(
			"rate-limited durable state = keys %d audits %d",
			keyCount,
			auditCount,
		)
	}
}

func TestUserAPIKeyZeroRatePolicyOmitsRetryAfter(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET client_rate_limit_max_requests = 0,
		       client_rate_limit_window_seconds = 0,
		       version = version + 1
		 WHERE singleton`); err != nil {
		t.Fatal(err)
	}
	accessToken := loginMyAPIKeyOwner(t, serverURL)

	valid := createMyAPIKey(
		t,
		serverURL,
		accessToken,
		`{"name":"zero-rate-valid"}`,
		"zero-rate-valid",
	)
	if valid.status != http.StatusTooManyRequests ||
		valid.header.Get("retry-after") != "" ||
		!strings.Contains(valid.body, `"code":"too_many_requests"`) ||
		!strings.Contains(valid.body, `"message":"rate_limited"`) {
		t.Fatalf(
			"zero-rate valid response = %d headers %#v body %s",
			valid.status,
			valid.header,
			valid.body,
		)
	}

	invalidResponse := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{`,
		map[string]string{
			"authorization":   "Bearer " + accessToken,
			"idempotency-key": "zero-rate-invalid",
			"x-request-id":    "zero-rate-invalid",
		},
	)
	var invalidBody string
	{
		var raw bytes.Buffer
		if _, err := raw.ReadFrom(invalidResponse.Body); err != nil {
			_ = invalidResponse.Body.Close()
			t.Fatal(err)
		}
		_ = invalidResponse.Body.Close()
		invalidBody = raw.String()
	}
	if invalidResponse.StatusCode != http.StatusTooManyRequests ||
		invalidResponse.Header.Get("retry-after") != "" ||
		!strings.Contains(invalidBody, `"code":"too_many_requests"`) ||
		!strings.Contains(invalidBody, `"message":"rate_limited"`) {
		t.Fatalf(
			"zero-rate invalid response = %d headers %#v body %s",
			invalidResponse.StatusCode,
			invalidResponse.Header,
			invalidBody,
		)
	}

	var (
		rateCount   int64
		keyCount    int
		auditCount  int
		replayCount int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT request_count FROM identity.client_rate_limits
			  WHERE owner_user_id = 'urn:xb:user:bot-owner'),
			(SELECT count(*) FROM identity.api_keys),
			(SELECT count(*) FROM audit.events
			  WHERE action = 'user-key.create'),
			(SELECT count(*) FROM identity.api_key_replays)`,
	).Scan(
		&rateCount,
		&keyCount,
		&auditCount,
		&replayCount,
	); err != nil {
		t.Fatal(err)
	}
	if rateCount != 1 || keyCount != 0 || auditCount != 0 ||
		replayCount != 0 {
		t.Fatalf(
			"zero-rate durable state = rate %d keys %d audits %d replays %d",
			rateCount,
			keyCount,
			auditCount,
			replayCount,
		)
	}
}

func TestInvalidUserAPIKeyRequestsConsumeSharedRate(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET client_rate_limit_max_requests = 4,
		       client_rate_limit_window_seconds = 60,
		       version = version + 1
		 WHERE singleton;
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		)
		SELECT
			'urn:xb:user:invalid-isolated',
			'invalidisolated',
			'invalidisolated',
			'invalid-isolated@xb.local',
			'invalid-isolated@xb.local',
			password_hash
		  FROM identity.users
		 WHERE user_id = 'urn:xb:user:bot-owner'`); err != nil {
		t.Fatal(err)
	}
	accessToken := loginMyAPIKeyOwner(t, serverURL)
	requests := []struct {
		body string
		key  string
	}{
		{body: `{"name":"missing"}`},
		{body: `{`, key: "invalid-json"},
		{
			body: `{"name":"overflow","tenantId":"urn:xb:tenant:7n42DGM5Tflk9n8mt7Fhc8"}`,
			key:  "invalid-tenant",
		},
		{body: `{"name":""}`, key: "invalid-name"},
	}
	for index, invalid := range requests {
		response := requestJSON(
			t,
			http.MethodPost,
			serverURL+"/v1/me/api-keys",
			invalid.body,
			map[string]string{
				"authorization":   "Bearer " + accessToken,
				"idempotency-key": invalid.key,
				"x-request-id":    "invalid-rate-" + strconv.Itoa(index),
			},
		)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf(
				"invalid request %d status = %d, want 400",
				index,
				response.StatusCode,
			)
		}
	}
	limited := createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		`{"name":"invalid-request-id"}`,
		strings.Repeat("r", 129),
		"invalid-request-id",
	)
	if limited.status != http.StatusTooManyRequests ||
		limited.header.Get("retry-after") != "60" ||
		!strings.Contains(limited.body, `"code":"too_many_requests"`) {
		t.Fatalf(
			"invalid request rate limit = %d headers %#v body %s",
			limited.status,
			limited.header,
			limited.body,
		)
	}
	var (
		rateCount   int64
		keyCount    int
		auditCount  int
		replayCount int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			(SELECT request_count FROM identity.client_rate_limits
			  WHERE owner_user_id = 'urn:xb:user:bot-owner'),
			(SELECT count(*) FROM identity.api_keys),
			(SELECT count(*) FROM audit.events
			  WHERE action = 'user-key.create'),
			(SELECT count(*) FROM identity.api_key_replays)`,
	).Scan(
		&rateCount,
		&keyCount,
		&auditCount,
		&replayCount,
	); err != nil {
		t.Fatal(err)
	}
	if rateCount != 4 || keyCount != 0 || auditCount != 0 ||
		replayCount != 0 {
		t.Fatalf(
			"invalid durable state = rate %d keys %d audits %d replays %d",
			rateCount,
			keyCount,
			auditCount,
			replayCount,
		)
	}
	secondAccessToken := loginMyAPIKey(t, serverURL, "invalidisolated")
	isolated := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"missing"}`,
		map[string]string{"authorization": "Bearer " + secondAccessToken},
	)
	_ = isolated.Body.Close()
	if isolated.StatusCode != http.StatusBadRequest {
		t.Fatalf("isolated principal status = %d", isolated.StatusCode)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:280
//	test: per_owner_api_key_cap_is_enforced
//
// Adaptations:
//   - The handler-level source test is exercised through the real HTTP and
//     PostgreSQL 19 Beta 2 boundary.
//   - Concurrent requests at the final slot prove that the cap cannot race.
//
// Assertions preserved:
//   - Two keys may be created when the configured cap is two.
//   - A creation past that cap is rejected with conflict.
func TestPerOwnerAPIKeyCapIsEnforced(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 2)
	defer admin.Close()
	defer apiPool.Close()
	accessToken := loginMyAPIKeyOwner(t, serverURL)

	first := createMyAPIKey(
		t,
		serverURL,
		accessToken,
		`{"name":"bot-0"}`,
		"request-bot-0",
	)
	if first.status != http.StatusCreated {
		t.Fatalf("first status = %d, body = %s", first.status, first.body)
	}

	const contenders = 8
	type result struct {
		status int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, contenders)
	var wait sync.WaitGroup
	for index := range contenders {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			request, err := http.NewRequestWithContext(
				context.Background(),
				http.MethodPost,
				serverURL+"/v1/me/api-keys",
				strings.NewReader(`{"name":"bot-final"}`),
			)
			if err != nil {
				results <- result{err: err}
				return
			}
			request.Header.Set("content-type", "application/json")
			request.Header.Set("authorization", "Bearer "+accessToken)
			requestID := "request-contender-" + string(rune('a'+index))
			request.Header.Set("x-request-id", requestID)
			request.Header.Set("idempotency-key", requestID)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				results <- result{err: err}
				return
			}
			_ = response.Body.Close()
			results <- result{status: response.StatusCode}
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)

	created := 0
	conflicts := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		switch result.status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("contender status = %d", result.status)
		}
	}
	if created != 1 || conflicts != contenders-1 {
		t.Fatalf("created=%d conflicts=%d", created, conflicts)
	}

	over := createMyAPIKey(
		t,
		serverURL,
		accessToken,
		`{"name":"bot-over"}`,
		"request-bot-over",
	)
	if over.status != http.StatusConflict {
		t.Fatalf("over-cap status = %d, body = %s", over.status, over.body)
	}
	var keyCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.api_keys
		 WHERE owner_user_id = 'urn:xb:user:bot-owner'
		   AND revoked_at IS NULL`,
	).Scan(&keyCount); err != nil {
		t.Fatal(err)
	}
	if keyCount != 2 {
		t.Fatalf("active key count = %d, want 2", keyCount)
	}
	var auditCount int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM audit.events
		 WHERE action = 'user-key.create'
		   AND outcome = 'success'`,
	).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("successful audit count = %d, want 2", auditCount)
	}
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_audit_trail.rs:209
//	test: user_api_key_create_is_audited_with_actor
//
// Adaptations:
//   - The source command dispatch is exercised through the authenticated HTTP
//     boundary and least-privilege PostgreSQL API role.
//
// Assertions preserved:
//   - One successful user-key.create audit fact is recorded.
//   - The actor is the authenticated user who created the key.
func TestUserAPIKeyCreateIsAuditedWithActor(t *testing.T) {
	ctx, admin, apiPool, serverURL := newMyAPIKeyFixture(t, 25)
	defer admin.Close()
	defer apiPool.Close()
	accessToken := loginMyAPIKeyOwner(t, serverURL)

	created := createMyAPIKey(
		t,
		serverURL,
		accessToken,
		`{"name":"trading-bot"}`,
		"request-audited-key",
	)
	if created.status != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", created.status, created.body)
	}
	var (
		actorKind  string
		actorID    string
		action     string
		targetKind string
		targetID   string
		outcome    string
		requestID  string
		detail     string
		eventCount int
	)
	if err := admin.QueryRow(ctx, `
		SELECT
			count(*) OVER (),
			actor_kind,
			actor_id,
			action,
			target_kind,
			target_id,
			outcome,
			request_id,
			detail::text
		  FROM audit.events
		 WHERE action = 'user-key.create'`,
	).Scan(
		&eventCount,
		&actorKind,
		&actorID,
		&action,
		&targetKind,
		&targetID,
		&outcome,
		&requestID,
		&detail,
	); err != nil {
		t.Fatal(err)
	}
	if actorKind != "user" ||
		eventCount != 1 ||
		actorID != "urn:xb:user:bot-owner" ||
		action != "user-key.create" ||
		targetKind != "api_key" ||
		targetID != created.created.ID ||
		outcome != "success" ||
		requestID != "request-audited-key" {
		t.Fatalf(
			"audit actor=%s/%s action=%s target=%s/%s outcome=%s request=%s",
			actorKind,
			actorID,
			action,
			targetKind,
			targetID,
			outcome,
			requestID,
		)
	}
	if strings.Contains(detail, created.created.Token) {
		t.Fatal("audit detail contains the returned API-key token")
	}

	_, directInsertErr := apiPool.Exec(ctx, `
		INSERT INTO identity.api_keys (
			api_key_id,
			owner_user_id,
			name,
			key_hash,
			prefix,
			scopes,
			created_at
		) VALUES (
			'00000000-0000-4000-8000-000000000001',
			'urn:xb:user:bot-owner',
			'bypass',
			decode(repeat('00', 32), 'hex'),
			'000000000001',
			ARRAY[]::text[],
			'2026-07-26T00:00:00Z'
		)`)
	var postgresError *pgconn.PgError
	if !errors.As(directInsertErr, &postgresError) ||
		postgresError.Code != "42501" {
		t.Fatalf(
			"direct API-role insert error = %v, want SQLSTATE 42501",
			directInsertErr,
		)
	}
}

type myAPIKeyHTTPResult struct {
	status  int
	body    string
	header  http.Header
	created edge.APIKeyCreated
}

type postCommitUnknownAPIKeyStore struct {
	*platformpostgres.CompatibilityStore
	mu     sync.Mutex
	failed bool
}

func (store *postCommitUnknownAPIKeyStore) CreateUserAPIKey(
	ctx context.Context,
	creation application.UserAPIKeyCreation,
) (application.UserAPIKeyCreationResult, error) {
	result, err := store.CompatibilityStore.CreateUserAPIKey(ctx, creation)
	if err != nil {
		return application.UserAPIKeyCreationResult{}, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.failed {
		store.failed = true
		return application.UserAPIKeyCreationResult{},
			errors.New("injected unknown outcome after commit")
	}
	return result, nil
}

func createMyAPIKey(
	t *testing.T,
	serverURL string,
	accessToken string,
	body string,
	requestID string,
) myAPIKeyHTTPResult {
	return createMyAPIKeyWithIdempotency(
		t,
		serverURL,
		accessToken,
		body,
		requestID,
		requestID,
	)
}

func createMyAPIKeyWithIdempotency(
	t *testing.T,
	serverURL string,
	accessToken string,
	body string,
	requestID string,
	idempotencyKey string,
) myAPIKeyHTTPResult {
	t.Helper()
	headers := map[string]string{
		"authorization": "Bearer " + accessToken,
		"x-request-id":  requestID,
	}
	if idempotencyKey != "" {
		headers["idempotency-key"] = idempotencyKey
	}
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		body,
		headers,
	)
	var result myAPIKeyHTTPResult
	result.status = response.StatusCode
	result.header = response.Header.Clone()
	var raw bytes.Buffer
	if _, err := raw.ReadFrom(response.Body); err != nil {
		_ = response.Body.Close()
		t.Fatal(err)
	}
	_ = response.Body.Close()
	result.body = raw.String()
	if result.status == http.StatusCreated {
		if err := json.Unmarshal(raw.Bytes(), &result.created); err != nil {
			t.Fatal(err)
		}
	}
	return result
}

func newMyAPIKeyFixture(
	t *testing.T,
	maxKeys int,
) (context.Context, *pgxpool.Pool, *pgxpool.Pool, string) {
	t.Helper()
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetCompatibilityDatabase(ctx, admin); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		UPDATE identity.api_key_policy
		   SET max_active_per_owner = $1,
		       version = version + 1
		 WHERE singleton`,
		maxKeys,
	); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	passwordHash, err := application.HashPassword(
		"trade-password",
		bytes.NewReader(bytes.Repeat([]byte{31}, 16)),
	)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES (
			'urn:xb:user:bot-owner', 'bottrader', 'bottrader',
			'bot@xb.local', 'bot@xb.local', $1
		)`,
		passwordHash,
	); err != nil {
		admin.Close()
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
		admin.Close()
		t.Fatal(err)
	}
	authenticator := newMyAPIKeyAuthenticator(t)
	store := platformpostgres.NewCompatibilityStore(apiPool)
	serverURL := newMyAPIKeyServer(
		t,
		store,
		authenticator,
		[]application.APIKeyReplayKey{testAPIKeyReplayKey("test-v1", 1)},
	)
	return ctx, admin, apiPool, serverURL
}

func newMyAPIKeyAuthenticator(t *testing.T) *edge.HMACAuthenticator {
	t.Helper()
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-my-api-keys-client-secret"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return authenticator
}

func newMyAPIKeyServer(
	t *testing.T,
	store application.IdentityStore,
	authenticator *edge.HMACAuthenticator,
	replayKeys []application.APIKeyReplayKey,
) string {
	return newMyAPIKeyServerWithActiveKey(
		t,
		store,
		authenticator,
		replayKeys,
		replayKeys[0].ID,
	)
}

func newMyAPIKeyServerWithActiveKey(
	t *testing.T,
	store application.IdentityStore,
	authenticator *edge.HMACAuthenticator,
	replayKeys []application.APIKeyReplayKey,
	activeKeyID string,
) string {
	t.Helper()
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Entropy: &incrementingReader{
				next: byte(apiKeyServerEntropySeed.Add(1) * 32),
			},
			APIKeyReplayKeys:        replayKeys,
			APIKeyReplayActiveKeyID: activeKeyID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		Trading:       tradingReader(store),
	}).Handler())
	t.Cleanup(server.Close)
	return server.URL
}

func tradingReader(store application.IdentityStore) edge.TradingReader {
	reader, _ := store.(edge.TradingReader)
	return reader
}

func testAPIKeyReplayKey(id string, seed byte) application.APIKeyReplayKey {
	key := application.APIKeyReplayKey{ID: id}
	for index := range key.Key {
		key.Key[index] = seed + byte(index)
	}
	return key
}

func loginMyAPIKeyOwner(t *testing.T, serverURL string) string {
	return loginMyAPIKey(t, serverURL, "bottrader")
}

func loginMyAPIKey(
	t *testing.T,
	serverURL string,
	login string,
) string {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/auth/login",
		`{"login":"`+login+`","password":"trade-password"}`,
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, response, &authenticated)
	if response.StatusCode != http.StatusOK || authenticated.AccessToken == "" {
		t.Fatalf("login status=%d body=%#v", response.StatusCode, authenticated)
	}
	return authenticated.AccessToken
}

type incrementingReader struct {
	mu   sync.Mutex
	next byte
}

var apiKeyServerEntropySeed atomic.Uint32

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy unavailable")
}

func (reader *incrementingReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for index := range buffer {
		reader.next++
		buffer[index] = reader.next
	}
	return len(buffer), nil
}
