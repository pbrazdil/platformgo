package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
//	source: apps/app/tests/it/identity/e2e_trader.rs:229
//	test: client_creates_own_api_key
//
// Adaptations:
//   - The Rust runtime is replaced by the Go HTTP edge and real PostgreSQL 17.
//   - Fixture password length follows the accepted Go credential policy; the
//     source test asserts successful authentication, not a password minimum.
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
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		`{"name":"my-bot","scopes":["orders:write"]}`,
		map[string]string{
			"authorization": "Bearer " + accessToken,
			"x-request-id":  "request-create-my-bot",
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
	wantHash := sha256.Sum256(secret)
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
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:280
//	test: per_owner_api_key_cap_is_enforced
//
// Adaptations:
//   - The handler-level source test is exercised through the real HTTP and
//     PostgreSQL 17 boundary.
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
			request.Header.Set("x-request-id", "request-contender-"+string(rune('a'+index)))
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
	)
	if err := admin.QueryRow(ctx, `
		SELECT
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

	if _, err := apiPool.Exec(ctx, `
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
		)`,
	); err == nil {
		t.Fatal("least-privilege API role inserted an API key directly")
	}
}

type myAPIKeyHTTPResult struct {
	status  int
	body    string
	created edge.APIKeyCreated
}

func createMyAPIKey(
	t *testing.T,
	serverURL string,
	accessToken string,
	body string,
	requestID string,
) myAPIKeyHTTPResult {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/me/api-keys",
		body,
		map[string]string{
			"authorization": "Bearer " + accessToken,
			"x-request-id":  requestID,
		},
	)
	var result myAPIKeyHTTPResult
	result.status = response.StatusCode
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
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-my-api-keys-client-secret"),
	})
	if err != nil {
		apiPool.Close()
		admin.Close()
		t.Fatal(err)
	}
	store := platformpostgres.NewCompatibilityStore(apiPool)
	identity, err := application.NewIdentity(
		store,
		authenticator,
		application.IdentityConfig{
			Entropy:            &incrementingReader{},
			MaxAPIKeysPerOwner: maxKeys,
		},
	)
	if err != nil {
		apiPool.Close()
		admin.Close()
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
	}).Handler())
	t.Cleanup(server.Close)
	return ctx, admin, apiPool, server.URL
}

func loginMyAPIKeyOwner(t *testing.T, serverURL string) string {
	t.Helper()
	response := requestJSON(
		t,
		http.MethodPost,
		serverURL+"/v1/auth/login",
		`{"login":"bottrader","password":"trade-password"}`,
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

func (reader *incrementingReader) Read(buffer []byte) (int, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	for index := range buffer {
		reader.next++
		buffer[index] = reader.next
	}
	return len(buffer), nil
}
