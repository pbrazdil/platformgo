package compatibility_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/migrations"
)

type brokerTokenSourceUser struct {
	ID      *string `json:"id"`
	Created *bool   `json:"created"`
}

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/identity/e2e_broker.rs:343
//	test: broker_token_exchange_on_behalf_of
//
// Adaptations:
//   - Source-created broker keys are represented by production configured HMAC
//     credentials with the same scopes and tenant.
//   - The Rust runtime is replaced by the production Go HTTP edge, identity
//     application service, least-privilege API role, and real PostgreSQL.
//   - Explicit distinct request IDs preserve the source's two independent user
//     creation requests rather than accidentally proving same-key replay.
//
// Assertions preserved:
//   - A scoped broker creates one user with HTTP 201 and created=true.
//   - A second request with the same email in different case returns HTTP 201,
//     created=false, and the same user ID.
//   - A requested 120-second delegated token returns HTTP 200 and authenticates
//     the client profile with the original login and user ID.
//   - A broker without tokens:mint receives HTTP 403.
//   - Minting for a well-formed unknown user receives HTTP 400.
func TestBrokerTokenExchangeOnBehalfOf(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
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

	const (
		brokerTenant = "urn:xb:tenant:broker-token-source"
		fullToken    = "xbk_broker_token_full.full-secret"
		plainToken   = "xbk_broker_token_plain.plain-secret"
	)
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: []byte("phase3-broker-token-client-secret!"),
		BrokerCredentials: []edge.BrokerCredential{
			{
				Prefix:     "xbk_broker_token_full",
				SecretHash: edge.HashBrokerSecret("full-secret"),
				Subject:    "urn:xb:apikey:broker-token-full",
				Tenant:     brokerTenant,
				Scopes:     []string{"accounts:write", "tokens:mint"},
			},
			{
				Prefix:     "xbk_broker_token_plain",
				SecretHash: edge.HashBrokerSecret("plain-secret"),
				Subject:    "urn:xb:apikey:broker-token-plain",
				Tenant:     brokerTenant,
				Scopes:     []string{"accounts:write"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := application.NewIdentity(
		platformpostgres.NewCompatibilityStore(apiPool),
		authenticator,
		application.IdentityConfig{
			Entropy: bytes.NewReader(bytes.Repeat([]byte{73}, 64)),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var requestSequence atomic.Uint64
	server := httptest.NewServer(edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Identity:      identity,
		RequestID: func() string {
			return fmt.Sprintf(
				"broker-token-source-%d",
				requestSequence.Add(1),
			)
		},
	}).Handler())
	defer server.Close()

	firstResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/broker/v1/users",
		`{"login":"crm-trader-1","email":"trader1@crm.example"}`,
		map[string]string{"x-api-key": fullToken},
	)
	var created brokerTokenSourceUser
	decodeAndClose(t, firstResponse, &created)
	if firstResponse.StatusCode != http.StatusCreated ||
		created.Created == nil ||
		!*created.Created ||
		created.ID == nil ||
		*created.ID == "" {
		t.Fatalf(
			"first broker user status=%d body=%#v",
			firstResponse.StatusCode,
			created,
		)
	}

	secondResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+"/broker/v1/users",
		`{"login":"different-handle","email":"TRADER1@crm.example"}`,
		map[string]string{"x-api-key": fullToken},
	)
	var converged brokerTokenSourceUser
	decodeAndClose(t, secondResponse, &converged)
	if secondResponse.StatusCode != http.StatusCreated ||
		converged.Created == nil ||
		*converged.Created ||
		converged.ID == nil ||
		*converged.ID != *created.ID {
		t.Fatalf(
			"converged broker user status=%d first=%#v second=%#v",
			secondResponse.StatusCode,
			created,
			converged,
		)
	}

	tokenPath := server.URL + "/broker/v1/users/" + *created.ID + "/token"
	tokenResponse := requestJSON(
		t,
		http.MethodPost,
		tokenPath,
		`{"ttlSecs":120}`,
		map[string]string{"x-api-key": fullToken},
	)
	var delegated edge.BrokerTokenResponse
	decodeAndClose(t, tokenResponse, &delegated)
	if tokenResponse.StatusCode != http.StatusOK ||
		delegated.ExpiresInSecs != 120 ||
		delegated.AccessToken == "" {
		t.Fatalf(
			"broker token status=%d body=%#v",
			tokenResponse.StatusCode,
			delegated,
		)
	}

	profileResponse := requestJSON(
		t,
		http.MethodGet,
		server.URL+"/v1/me",
		"",
		map[string]string{
			"authorization": "Bearer " + delegated.AccessToken,
		},
	)
	var profile edge.UserProfile
	decodeAndClose(t, profileResponse, &profile)
	if profileResponse.StatusCode != http.StatusOK ||
		profile.Login != "crm-trader-1" ||
		profile.UserID != *created.ID {
		t.Fatalf(
			"delegated profile status=%d body=%#v",
			profileResponse.StatusCode,
			profile,
		)
	}

	noMintResponse := requestJSON(
		t,
		http.MethodPost,
		tokenPath,
		`{}`,
		map[string]string{"x-api-key": plainToken},
	)
	defer noMintResponse.Body.Close()
	if noMintResponse.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"no-mint broker token status=%d, want 403",
			noMintResponse.StatusCode,
		)
	}

	unknownResponse := requestJSON(
		t,
		http.MethodPost,
		server.URL+
			"/broker/v1/users/urn:xb:user:2zzzzzzzzzzzzzzzzzzzzzz/token",
		`{}`,
		map[string]string{"x-api-key": fullToken},
	)
	defer unknownResponse.Body.Close()
	if unknownResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"unknown broker user token status=%d, want 400",
			unknownResponse.StatusCode,
		)
	}

	var rows int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.users
		 WHERE broker_subject = $1
		   AND normalized_email = 'trader1@crm.example'`,
		brokerTenant,
	).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("converged PostgreSQL identities=%d, want 1", rows)
	}
}
