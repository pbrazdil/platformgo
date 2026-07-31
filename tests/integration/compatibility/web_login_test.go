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

const webLoginOrigin = "https://admin.web.test"

type webLoginClock struct {
	value time.Time
}

func (clock webLoginClock) Now() time.Time {
	return clock.value
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_web_refresh_cookie.rs:74
//	test: web_login_sets_httponly_cookie_and_omits_body_refresh
//
// Owner-approved adaptations:
//   - The absent Go admin/browser-login plane is not simulated. The
//     Origin-bearing request is accepted at production client
//     POST /v1/auth/login.
//   - The intentionally rejected credentialed-CORS, cookie, and body-omission
//     assertions are recorded in
//     ports/decisions/web-login-cookie-placement-preserves-current-go-client-boundary.md.
//   - A deterministic Argon2id PHC fixture preserves the source's short
//     password without claiming current-Go password-creation compatibility.
//
// Assertions preserved:
//   - Password login returns 200.
//   - Access-Control-Allow-Origin contains the exact configured origin.
//   - The JSON body contains a non-empty accessToken.
//
// Assertions intentionally changed by the owner-approved decision:
//   - Access-Control-Allow-Credentials is absent instead of true.
//   - Set-Cookie is absent instead of carrying a refresh credential.
//   - The JSON body contains a non-empty refreshToken instead of omitting it.
//
// Invariant strengthening:
//   - The least-privilege API role commits exactly one session containing the
//     SHA-256 refresh-token hash, never the raw refresh credential.
func TestWebLoginPreservesCurrentGoClientBoundary(t *testing.T) {
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
		userID   = "urn:xb:user:web-login"
		login    = "root"
		email    = "root@xb.local"
		password = "admin-pw"
	)
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES ($1,$2,$2,$3,$3,$4)`,
		userID,
		login,
		email,
		traderProfilePasswordHash(password),
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

	clock := webLoginClock{
		value: time.Date(2026, time.July, 28, 13, 15, 0, 0, time.UTC),
	}
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-web-login-client-secret-32"),
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
			Entropy: bytes.NewReader(bytes.Repeat([]byte{53}, 32)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		AllowOrigin:   webLoginOrigin,
		RequestID: func() string {
			return "phase3-web-login-request"
		},
	}).Handler())
	defer server.Close()

	webLoginRequireSessionCounts(t, admin, userID, 0, 0)
	response := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"root","password":"admin-pw"}`,
		map[string]string{"origin": webLoginOrigin},
	)
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("web-shaped client login status = %d, want 200", response.StatusCode)
	}
	allowOrigins := response.Header.Values("Access-Control-Allow-Origin")
	if len(allowOrigins) != 1 || allowOrigins[0] != webLoginOrigin {
		_ = response.Body.Close()
		t.Fatalf(
			"allow-origin count=%d exact=%t, want count=1 exact=true",
			len(allowOrigins),
			len(allowOrigins) == 1 && allowOrigins[0] == webLoginOrigin,
		)
	}
	if headers := response.Header.Values("Access-Control-Allow-Credentials"); len(headers) != 0 {
		_ = response.Body.Close()
		t.Fatalf("allow-credentials header count = %d, want 0", len(headers))
	}
	if headers := response.Header.Values("Set-Cookie"); len(headers) != 0 {
		_ = response.Body.Close()
		t.Fatalf("Set-Cookie header count = %d, want 0", len(headers))
	}

	var authenticated edge.LoginResponse
	decodeAndClose(t, response, &authenticated)
	if authenticated.AccessToken == "" || authenticated.RefreshToken == "" {
		t.Fatalf(
			"web-shaped client login access-present=%t refresh-present=%t",
			authenticated.AccessToken != "",
			authenticated.RefreshToken != "",
		)
	}
	webLoginRequireHashedSession(
		t,
		admin,
		userID,
		authenticated.RefreshToken,
	)
}

func webLoginRequireSessionCounts(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	wantUser int,
	wantGlobal int,
) {
	t.Helper()
	var userCount int
	var globalCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			count(*) FILTER (WHERE user_id = $1),
			count(*)
		  FROM identity.sessions`,
		userID,
	).Scan(&userCount, &globalCount); err != nil {
		t.Fatal(err)
	}
	if userCount != wantUser || globalCount != wantGlobal {
		t.Fatalf(
			"session counts user=%d global=%d, want user=%d global=%d",
			userCount,
			globalCount,
			wantUser,
			wantGlobal,
		)
	}
}

func webLoginRequireHashedSession(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	refreshToken string,
) {
	t.Helper()
	webLoginRequireSessionCounts(t, pool, userID, 1, 1)
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
