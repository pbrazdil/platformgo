package compatibility_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
	"golang.org/x/crypto/argon2"
)

type traderProfileClock struct {
	value time.Time
}

func (clock traderProfileClock) Now() time.Time {
	return clock.value
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_trader.rs:32
//	test: login_and_own_profile_with_cross_audience_rejection
//
// Owner-approved adaptations:
//   - The current Go profile remains exactly userId, login, email, and status;
//     kycStatus stays absent because Go has no durable KYC authority.
//   - The absent Go admin-login plane is not simulated. A correctly signed,
//     otherwise-valid admin-audience token isolates the production client
//     authenticator's fail-closed audience check.
//   - A deterministic Argon2id PHC fixture preserves the source password even
//     though current Go rejects newly created passwords shorter than 12 bytes.
//
// Decision:
//
//	ports/decisions/trader-profile-preserves-current-go-identity-boundary.md
func TestLoginAndOwnProfileWithCrossAudienceRejection(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	if err := resetCompatibilityDatabase(ctx, adminPool); err != nil {
		t.Fatal(err)
	}
	if err := migrateAndProvisionCompatibilityFixture(t, ctx, adminPool, migrations.Files, 7); err != nil {
		t.Fatal(err)
	}

	const (
		userID     = "urn:xb:user:trader-profile-1"
		otherID    = "urn:xb:user:trader-profile-other"
		password   = "trade-pw"
		wrong      = "nope"
		clientName = "trader1"
		email      = "trader1@xb.local"
	)
	passwordHash := traderProfilePasswordHash(password)
	if _, err := adminPool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id, login, normalized_login, email, normalized_email,
			password_hash
		) VALUES
			($1, $2, $2, $3, $3, $4),
			($5, 'other-trader', 'other-trader', 'other@xb.local',
				'other@xb.local', $4)`,
		userID,
		clientName,
		email,
		passwordHash,
		otherID,
	); err != nil {
		t.Fatal(err)
	}

	apiDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		adminPool,
		databaseURL,
		"platformgo_api",
	)
	apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()

	now := time.Date(2026, time.July, 27, 20, 0, 0, 0, time.UTC)
	clock := traderProfileClock{value: now}
	clientSecret := []byte("phase3-trader-profile-client-secret")
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: clientSecret,
		Clock:             clock,
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Clock: clock,
			Entropy: bytes.NewReader(append(
				bytes.Repeat([]byte{23}, 32),
				bytes.Repeat([]byte{29}, 32)...,
			)),
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

	login := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"trader1","password":"trade-pw"}`,
		nil,
	)
	var authenticated edge.LoginResponse
	decodeAndClose(t, login, &authenticated)
	if login.StatusCode != http.StatusOK ||
		authenticated.AccessToken == "" ||
		authenticated.RefreshToken == "" {
		t.Fatalf(
			"login status=%d access-present=%t refresh-present=%t",
			login.StatusCode,
			authenticated.AccessToken != "",
			authenticated.RefreshToken != "",
		)
	}
	traderProfileRequireSessionCount(t, adminPool, userID, 1)

	profileResponse := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me",
		"",
		map[string]string{
			"authorization": "Bearer " + authenticated.AccessToken,
		},
	)
	var profile map[string]string
	decodeAndClose(t, profileResponse, &profile)
	wantProfile := map[string]string{
		"userId": userID,
		"login":  clientName,
		"email":  email,
		"status": "active",
	}
	if profileResponse.StatusCode != http.StatusOK ||
		!reflect.DeepEqual(profile, wantProfile) {
		t.Fatalf("profile status=%d body=%#v", profileResponse.StatusCode, profile)
	}
	traderProfileRequireRateCount(t, adminPool, userID, 1)

	badPassword := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/v1/auth/login",
		`{"login":"`+clientName+`","password":"`+wrong+`"}`,
		map[string]string{
			"x-request-id": "trader-profile-bad-password",
		},
	)
	traderProfileRequireUnauthorized(
		t,
		badPassword,
		"trader-profile-bad-password",
	)
	traderProfileRequireSessionCount(t, adminPool, userID, 1)

	anonymous := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me",
		"",
		map[string]string{
			"x-request-id": "trader-profile-anonymous",
		},
	)
	traderProfileRequireUnauthorized(
		t,
		anonymous,
		"trader-profile-anonymous",
	)

	adminAudienceToken := traderProfileAudienceToken(
		t,
		clientSecret,
		edge.ClientClaims{
			Subject: userID, Audience: string(edge.AudienceAdmin),
			Expires: now.Add(time.Minute).Unix(),
		},
	)
	crossAudience := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me",
		"",
		map[string]string{
			"authorization": "Bearer " + adminAudienceToken,
			"x-request-id":  "trader-profile-cross-audience",
		},
	)
	traderProfileRequireUnauthorized(
		t,
		crossAudience,
		"trader-profile-cross-audience",
	)
	traderProfileRequireRateCount(t, adminPool, userID, 1)
}

func traderProfilePasswordHash(password string) string {
	salt := bytes.Repeat([]byte{17}, 16)
	key := argon2.IDKey(
		[]byte(password),
		salt,
		3,
		64*1024,
		1,
		32,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=65536,t=3,p=1$%s$%s",
		argon2.Version,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
}

func traderProfileAudienceToken(
	t *testing.T,
	secret []byte,
	claims edge.ClientClaims,
) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["typ"] = "JWT"
	signed, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func traderProfileRequireUnauthorized(
	t *testing.T,
	response *http.Response,
	requestID string,
) {
	t.Helper()
	var body map[string]string
	decodeAndClose(t, response, &body)
	want := map[string]string{
		"code":      "unauthorized",
		"message":   "unauthorized",
		"requestId": requestID,
	}
	bodyMatches := reflect.DeepEqual(body, want)
	if response.StatusCode != http.StatusUnauthorized || !bodyMatches {
		t.Fatalf(
			"unauthorized response status=%d generic-body=%t",
			response.StatusCode,
			bodyMatches,
		)
	}
}

func traderProfileRequireSessionCount(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	want int,
) {
	t.Helper()
	var count int
	if err := pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM identity.sessions WHERE user_id = $1`,
		userID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("session count=%d, want %d", count, want)
	}
}

func traderProfileRequireRateCount(
	t *testing.T,
	pool *pgxpool.Pool,
	userID string,
	want int64,
) {
	t.Helper()
	var count int64
	if err := pool.QueryRow(
		context.Background(),
		`SELECT request_count
		   FROM identity.client_rate_limits
		  WHERE owner_user_id = $1`,
		userID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("client rate count=%d, want %d", count, want)
	}
}
