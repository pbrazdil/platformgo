package compatibility_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

type nativeLoginClock struct {
	value time.Time
}

func (clock nativeLoginClock) Now() time.Time {
	return clock.value
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_web_refresh_cookie.rs:50
//	test: native_login_keeps_refresh_in_body_and_sets_no_cookie
//
// Owner-approved adaptations:
//   - The absent Go admin-login plane is not simulated. Native token placement
//     is accepted at the production client POST /v1/auth/login boundary.
//   - The route and principal deviation is recorded in
//     ports/decisions/native-login-refresh-placement-preserves-current-go-client-boundary.md.
//   - A deterministic Argon2id PHC fixture preserves the source's short
//     password without claiming current-Go password-creation compatibility.
//
// Assertions preserved:
//   - Native password login returns 200.
//   - The response emits no Set-Cookie header.
//   - The JSON body contains a non-empty accessToken.
//   - The JSON body contains a non-empty refreshToken.
//
// Invariant strengthening:
//   - The least-privilege API role persists exactly one session containing the
//     SHA-256 refresh-token hash, never the raw refresh credential.
func TestNativeLoginKeepsRefreshInBodyAndSetsNoCookie(t *testing.T) {
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
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	const (
		userID   = "urn:xb:user:native-login"
		login    = "root"
		email    = "root@xb.local"
		password = "admin-pw"
	)
	passwordHash := traderProfilePasswordHash(password)
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES ($1,$2,$2,$3,$3,$4)`,
		userID,
		login,
		email,
		passwordHash,
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
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	clock := nativeLoginClock{
		value: time.Date(2026, time.July, 27, 23, 15, 0, 0, time.UTC),
	}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-native-login-client-secret"),
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Clock:   clock,
			Entropy: bytes.NewReader(bytes.Repeat([]byte{47}, 32)),
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

	nativeLoginRequireSessionCount(t, admin, userID, 0)
	response := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"root","password":"admin-pw"}`,
		nil,
	)
	if cookies := response.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf(
			"native login Set-Cookie header count = %d, want 0",
			len(cookies),
		)
	}
	var authenticated edge.LoginResponse
	decodeAndClose(t, response, &authenticated)
	if response.StatusCode != http.StatusOK ||
		authenticated.AccessToken == "" ||
		authenticated.RefreshToken == "" {
		t.Fatalf(
			"native login status=%d access-present=%t refresh-present=%t",
			response.StatusCode,
			authenticated.AccessToken != "",
			authenticated.RefreshToken != "",
		)
	}

	nativeLoginRequireHashedSession(
		t,
		admin,
		userID,
		authenticated.RefreshToken,
	)
}

func nativeLoginRequireSessionCount(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	want int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		  FROM identity.sessions
		 WHERE user_id = $1`,
		userID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("session count=%d, want %d", count, want)
	}
}

func nativeLoginRequireHashedSession(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	refreshToken string,
) {
	t.Helper()
	nativeLoginRequireSessionCount(t, pool, userID, 1)
	var storedHash []byte
	if err := pool.QueryRow(context.Background(), `
		SELECT refresh_hash
		  FROM identity.sessions
		 WHERE user_id = $1`,
		userID,
	).Scan(&storedHash); err != nil {
		t.Fatal(err)
	}
	wantHash := sha256.Sum256([]byte(refreshToken))
	if !bytes.Equal(storedHash, wantHash[:]) ||
		bytes.Equal(storedHash, []byte(refreshToken)) {
		t.Fatalf(
			"stored refresh hash matches=%t raw-stored=%t",
			bytes.Equal(storedHash, wantHash[:]),
			bytes.Equal(storedHash, []byte(refreshToken)),
		)
	}
}
